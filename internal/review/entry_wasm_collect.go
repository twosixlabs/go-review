package review

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const wasmSourceLimit = 2 << 20
const wasmFileLimit = 4096

type wasmConfig struct {
	worlds, paths []string
	evidence      []EntryPointEvidence
	base          string
}
type wasmDirective struct {
	name, symbol string
	evidence     EntryPointEvidence
	forwarded    map[string]EntryPointEvidence
}

// collectWasmEntryPoints never invokes a compiler, resolves a remote package, or
// follows filesystem links. Incomplete static evidence stays explicitly unresolved.
func collectWasmEntryPoints(ctx context.Context, r *Report) (*EntryPointInventory, error) {
	inv := &EntryPointInventory{Kind: "wasm", Entries: []EntryPoint{}, Sources: []Document{}, Initializers: []string{}, Limitations: []string{}}
	var configs []wasmConfig
	var sources []Document
	var directives []wasmDirective
	detected := r.Environment["GOARCH"] == "wasm" || r.Environment["GOOS"] == "wasip1"
	files := map[string]File{}
	for _, f := range append(append([]File{}, r.Files...), r.SupportingFiles...) {
		if r.Scope.pathReason(r.Scope.relative(r.Root, f.Path)) != "" {
			continue
		}
		files[filepath.ToSlash(f.Path)] = f
		if len(f.Source) > wasmSourceLimit {
			inv.Limitations = append(inv.Limitations, "Retained Go source exceeds 2 MiB Wasm scan limit: "+f.Path)
			if strings.Contains(f.Source, "//go:wasmexport ") || strings.Contains(f.Source, "//export ") {
				detected = true
			}
			continue
		}
		directives = append(directives, wasmDirectives(f, Document{}, r)...)
	}
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("Wasm source root is a symlink: %s", root)
	}
	scopeRoot := root
	if r.Scope.Root != "" {
		scopeRoot, err = filepath.Abs(r.Scope.Root)
		if err != nil {
			return nil, err
		}
		if !wasmWithin(scopeRoot, root) {
			return nil, fmt.Errorf("Wasm module root is outside review scope")
		}
	}
	scanned := 0
	visited := map[string]bool{}
	witOnly, workloadOnly := false, false
	var visit fs.WalkDirFunc
	visit = func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			inv.Limitations = append(inv.Limitations, "Cannot inspect Wasm source: "+walkErr.Error())
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && r.Scope.pathReason(r.Scope.relative(root, rel)) != "" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			inv.Limitations = append(inv.Limitations, "Skipped symlink in Wasm discovery: "+rel)
			return nil
		}
		if visited[path] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited[path] = true
		if d.IsDir() {
			if path != root {
				if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if info, e := os.Lstat(filepath.Join(path, "go.mod")); e == nil && !info.IsDir() && !(workloadOnly && (path == scopeRoot || wasmWithin(path, root))) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		scanned++
		if scanned > wasmFileLimit {
			inv.Limitations = append(inv.Limitations, "Wasm discovery file limit reached (4096); remaining files were not inspected")
			return fs.SkipAll
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if witOnly && ext != ".wit" || workloadOnly && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		config := d.Name() == "componentize-go.toml" || d.Name() == "wasmcloud.toml"
		if ext != ".wit" && ext != ".yaml" && ext != ".yml" && ext != ".go" && !config {
			return nil
		}
		if ext == ".go" {
			if _, ok := files[rel]; ok {
				return nil
			}
			if strings.HasSuffix(rel, "_test.go") {
				return nil
			}
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > wasmSourceLimit {
			inv.Limitations = append(inv.Limitations, "Wasm source exceeds 2 MiB limit: "+rel)
			if config || ext == ".wit" {
				detected = true
			}
			return nil
		}
		// Recheck immediately before opening; links are never intentionally followed.
		data, e := wasmReadFile(scopeRoot, path)
		if e != nil {
			inv.Limitations = append(inv.Limitations, "Cannot read Wasm source "+rel+": "+e.Error())
			return nil
		}
		src := Document{ID: stableID("doc", rel), Path: rel, SHA256: digest(data), Text: string(data)}
		if config {
			detected = true
			c, e := wasmTOMLConfig(src)
			if e != nil {
				return e
			}
			configs = append(configs, c)
			sources = append(sources, src)
		} else if ext == ".yaml" || ext == ".yml" {
			c, workload, e := wasmYAMLConfig(src)
			if e != nil {
				return e
			}
			if workload {
				detected = true
				configs = append(configs, c)
				sources = append(sources, src)
			}
		} else if ext == ".wit" {
			sources = append(sources, src)
			if !wasmDependencyWIT(rel) {
				detected = true
			}
		} else {
			ds := wasmDirectives(File{Path: rel, Source: src.Text}, src, r)
			if len(ds) > 0 {
				directives = append(directives, ds...)
				sources = append(sources, src)
			}
		}
		return nil
	}
	err = filepath.WalkDir(root, visit)
	if err != nil {
		return nil, err
	}
	// Configured paths can leave the module, but never the review checkout or
	// cross a symlink, excluded directory, or another module boundary.
	for _, c := range append([]wasmConfig{}, configs...) {
		for _, p := range c.paths {
			resolved := filepath.Clean(filepath.Join(root, c.base, filepath.FromSlash(p)))
			if !wasmWithin(scopeRoot, resolved) || !wasmSafeDirectory(scopeRoot, resolved, root, r.Scope) {
				inv.Limitations = append(inv.Limitations, "Configured WIT directory outside scope, excluded, unavailable or unsafe: "+p)
				continue
			}
			witOnly = true
			if err := filepath.WalkDir(resolved, visit); err != nil {
				return nil, err
			}
			witOnly = false
		}
	}
	if scopeRoot != root {
		workloadOnly = true
		if err := filepath.WalkDir(scopeRoot, visit); err != nil {
			return nil, err
		}
	}
	// A dependency package's worlds are definitions, not selected components.
	w := witResolver{worlds: map[string][]witDecl{}, interfaces: map[string][]witDecl{}, inv: inv}
	var localWorlds []witDecl
	packages := map[string]string{}
	parsed := map[string]witPackage{}
	for _, src := range sources {
		if !strings.HasSuffix(src.Path, ".wit") {
			continue
		}
		p := parseWIT(src)
		parsed[src.Path] = p
		if p.name != "" {
			packages[filepath.Dir(src.Path)] = p.name
		}
	}
	for _, src := range sources {
		if !strings.HasSuffix(src.Path, ".wit") {
			continue
		}
		p := parsed[src.Path]
		inv.Limitations = append(inv.Limitations, p.limitations...)
		for _, d := range p.worlds {
			if d.pkg == "" {
				d.pkg = packages[filepath.Dir(src.Path)]
			}
			key := witIdentity(d.pkg, d.name)
			w.worlds[key] = append(w.worlds[key], d)
			if !wasmDependencyWIT(src.Path) {
				localWorlds = append(localWorlds, d)
			}
		}
		for _, d := range p.interfaces {
			if d.pkg == "" {
				d.pkg = packages[filepath.Dir(src.Path)]
			}
			key := witIdentity(d.pkg, d.name)
			w.interfaces[key] = append(w.interfaces[key], d)
		}
	}
	var selected []string
	var configEvidence []EntryPointEvidence
	for _, c := range configs {
		selected = append(selected, c.worlds...)
		configEvidence = append(configEvidence, c.evidence...)
	}
	sort.Strings(selected)
	selected = wasmUniqueStrings(selected)
	if len(selected) > 0 {
		detected = true
	}
	if len(localWorlds) > 0 {
		detected = true
	}
	if len(directives) > 0 {
		detected = true
	}
	if !detected {
		for _, limitation := range inv.Limitations {
			r.Warnings = append(r.Warnings, "Wasm discovery: "+limitation)
		}
		return nil, nil
	}
	inv.Sources = sources
	enumeratedWorld := false
	if len(selected) == 0 {
		if len(localWorlds) == 1 {
			d := localWorlds[0]
			w.world(d, witIdentity(d.pkg, d.name), configEvidence, map[string]bool{})
			enumeratedWorld = true
		} else if len(localWorlds) > 1 {
			inv.Limitations = append(inv.Limitations, "No configured WIT world; all module-local worlds are retained as candidates")
			for _, d := range localWorlds {
				w.world(d, witIdentity(d.pkg, d.name), configEvidence, map[string]bool{})
			}
			enumeratedWorld = true
		}
	} else {
		for _, ref := range selected {
			var matches []witDecl
			if strings.Contains(ref, "/") {
				matches = w.worlds[ref]
			} else {
				for _, ds := range w.worlds {
					for _, d := range ds {
						if d.name == ref && !wasmDependencyWIT(d.source.Path) {
							matches = append(matches, d)
						}
					}
				}
			}
			if len(matches) != 1 {
				inv.Limitations = append(inv.Limitations, "Selected WIT world unavailable or ambiguous: "+ref)
				inv.Entries = append(inv.Entries, EntryPoint{ID: stableID("entry", "wasm:"+ref), Kind: "wasm", Name: ref, World: ref, Resolution: "unresolved", Reason: "Selected WIT world unavailable or ambiguous; exports cannot be enumerated", Evidence: configEvidence})
				continue
			}
			w.world(matches[0], witIdentity(matches[0].pkg, matches[0].name), configEvidence, map[string]bool{})
			enumeratedWorld = true
		}
	}
	used := map[string]bool{}
	for i := range inv.Entries {
		e := &inv.Entries[i]
		for _, d := range directives {
			if wasmMatches(*e, d.name) {
				wasmBind(r, e, d)
				used[d.name] = true
			}
		}
	}
	// Without local WIT, actual ABI directives are the only available API evidence.
	// Do not introduce directives from unselected worlds when WIT selected an API.
	if !enumeratedWorld {
		for _, d := range directives {
			if used[d.name] {
				continue
			}
			used[d.name] = true
			iface, fn, hasIface := strings.Cut(d.name, "#")
			if !hasIface {
				fn = iface
				iface = ""
			}
			e := EntryPoint{ID: stableID("entry", "wasm:abi:"+d.name), Kind: "wasm", Name: d.name, Interface: iface, Function: fn, Resolution: "unresolved", Evidence: []EntryPointEvidence{}}
			wasmBind(r, &e, d)
			inv.Entries = append(inv.Entries, e)
		}
	}
	if len(localWorlds) == 0 {
		inv.Limitations = append(inv.Limitations, "No module-local WIT world is available; SDK/ABI exports may not describe the complete component API")
	}
	if len(inv.Entries) == 0 {
		inv.Limitations = append(inv.Limitations, "Wasm project detected but no component exports could be established; main is not an entry-point fallback")
	}
	byID := map[string]int{}
	entries := make([]EntryPoint, 0, len(inv.Entries))
	for _, entry := range inv.Entries {
		if index, ok := byID[entry.ID]; ok {
			entries[index].Evidence = append(entries[index].Evidence, entry.Evidence...)
		} else {
			byID[entry.ID] = len(entries)
			entries = append(entries, entry)
		}
	}
	inv.Entries = entries
	sort.Slice(inv.Entries, func(i, j int) bool { return inv.Entries[i].ID < inv.Entries[j].ID })
	sort.Slice(inv.Sources, func(i, j int) bool { return inv.Sources[i].Path < inv.Sources[j].Path })
	sort.Strings(inv.Limitations)
	inv.Limitations = wasmUniqueStrings(inv.Limitations)
	return inv, nil
}

func wasmUniqueStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func wasmWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func wasmSafeDirectory(scope, path, module string, reviewScope ReviewScope) bool {
	if !wasmWithin(scope, path) {
		return false
	}
	rel, _ := filepath.Rel(scope, path)
	if reviewScope.pathReason(filepath.ToSlash(rel)) != "" {
		return false
	}
	current := scope
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range append([]string{"."}, parts...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if current != module && !wasmWithin(current, module) {
			if _, err := os.Lstat(filepath.Join(current, "go.mod")); err == nil {
				return false
			}
		}
	}
	return true
}

func wasmReadFile(root, path string) ([]byte, error) {
	f, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	info, err := f.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	file, err := f.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("source changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, wasmSourceLimit+1))
	if len(data) > wasmSourceLimit {
		return nil, fmt.Errorf("source size limit exceeded")
	}
	return data, err
}

func wasmDependencyWIT(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "deps" || part == "dependencies" {
			return true
		}
	}
	return false
}

func wasmTOMLConfig(src Document) (wasmConfig, error) {
	var tree map[string]any
	if err := toml.Unmarshal([]byte(src.Text), &tree); err != nil {
		return wasmConfig{}, fmt.Errorf("parse %s: %w", src.Path, err)
	}
	c := wasmConfig{base: filepath.Dir(src.Path), evidence: []EntryPointEvidence{{SourceID: src.ID, Line: 1, EndLine: 1, Kind: "configuration", Detail: src.Path}}}
	var walk func(map[string]any) error
	walk = func(m map[string]any) error {
		for key, value := range m {
			switch key {
			case "worlds", "wit_paths":
				values, ok := value.([]any)
				if !ok {
					return fmt.Errorf("%s must be an array of strings", key)
				}
				for _, v := range values {
					s, ok := v.(string)
					if !ok {
						return fmt.Errorf("%s must contain strings", key)
					}
					if key == "worlds" {
						c.worlds = append(c.worlds, s)
					} else {
						c.paths = append(c.paths, s)
					}
				}
			case "wit_world", "wit_path":
				s, ok := value.(string)
				if !ok {
					return fmt.Errorf("%s must be a string", key)
				}
				if key == "wit_world" {
					c.worlds = append(c.worlds, s)
				} else {
					c.paths = append(c.paths, s)
				}
			default:
				if child, ok := value.(map[string]any); ok {
					if err := walk(child); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(tree); err != nil {
		return c, fmt.Errorf("parse %s: %w", src.Path, err)
	}
	for i, line := range strings.Split(src.Text, "\n") {
		if strings.Contains(line, "world") || strings.Contains(line, "wit_path") {
			c.evidence = append(c.evidence, EntryPointEvidence{SourceID: src.ID, Line: i + 1, EndLine: i + 1, Kind: "configuration", Detail: strings.TrimSpace(line)})
		}
	}
	sort.Strings(c.worlds)
	sort.Strings(c.paths)
	return c, nil
}

func wasmYAMLConfig(src Document) (wasmConfig, bool, error) {
	c := wasmConfig{}
	decoder := yaml.NewDecoder(strings.NewReader(src.Text))
	workload := false
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Unrelated YAML is not configuration for this collector.
			if strings.Contains(src.Text, "runtime.wasmcloud.dev/") || strings.Contains(src.Text, "wasmcloud.com/") {
				return c, false, fmt.Errorf("parse %s: %w", src.Path, err)
			}
			return c, false, nil
		}
		if len(node.Content) == 0 {
			continue
		}
		root := node.Content[0]
		if root.Kind != yaml.MappingNode {
			continue
		}
		api, kind := "", ""
		for i := 0; i+1 < len(root.Content); i += 2 {
			switch root.Content[i].Value {
			case "apiVersion":
				api = root.Content[i+1].Value
			case "kind":
				kind = root.Content[i+1].Value
			}
		}
		if !(strings.HasPrefix(api, "runtime.wasmcloud.dev/") || strings.HasPrefix(api, "wasmcloud.com/")) || !(kind == "Workload" || kind == "Application") {
			continue
		}
		workload = true
		c.evidence = append(c.evidence, EntryPointEvidence{SourceID: src.ID, Line: root.Line, EndLine: root.Line, Kind: "configuration", Detail: kind + " " + api + " (deployment intent, not execution evidence)"})
		c.evidence = append(c.evidence, wasmTriggerEvidence(root, src.ID)...)
	}
	return c, workload, nil
}

func wasmTriggerEvidence(root *yaml.Node, sourceID string) []EntryPointEvidence {
	var out []EntryPointEvidence
	var visit func(*yaml.Node, string)
	visit = func(n *yaml.Node, key string) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				visit(n.Content[i+1], n.Content[i].Value)
			}
		case yaml.SequenceNode:
			for _, child := range n.Content {
				visit(child, key)
			}
		case yaml.ScalarNode:
			switch key {
			case "subject", "subjects", "topic", "topics", "stream", "durable", "consumer", "queue", "bucket":
				out = append(out, EntryPointEvidence{SourceID: sourceID, Line: n.Line, EndLine: n.Line, Kind: "trigger-configuration", Detail: key + "=" + n.Value + " (configured, not observed)"})
			}
		}
	}
	visit(root, "")
	return out
}

func wasmDirectives(f File, src Document, r *Report) []wasmDirective {
	set := token.NewFileSet()
	tree, err := parser.ParseFile(set, f.Path, f.Source, parser.ParseComments)
	if err != nil {
		return nil
	}
	var out []wasmDirective
	for _, decl := range tree.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		for _, comment := range fn.Doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			var name string
			if strings.HasPrefix(text, "go:wasmexport ") {
				name = strings.TrimSpace(strings.TrimPrefix(text, "go:wasmexport "))
			} else if strings.HasPrefix(text, "export ") {
				name = strings.TrimSpace(strings.TrimPrefix(text, "export "))
			} else {
				continue
			}
			if wasmHelper(name) {
				continue
			}
			for strings.HasPrefix(name, "[async-lift]") {
				name = strings.TrimPrefix(name, "[async-lift]")
			}
			line := set.Position(fn.Pos()).Line
			symbol := ""
			for _, s := range r.Symbols {
				if s.FileID == f.ID && f.ID != "" && s.Line == line && s.Name == fn.Name.Name {
					symbol = s.ID
					break
				}
			}
			ev := EntryPointEvidence{SourceID: src.ID, Line: set.Position(comment.Pos()).Line, EndLine: set.Position(comment.End()).Line, Kind: "wasmexport", Detail: text}
			if f.ID != "" {
				ev.Location = &Location{FileID: f.ID, Line: ev.Line, EndLine: ev.EndLine, Column: 1, EndColumn: set.Position(comment.End()).Column}
			}
			out = append(out, wasmDirective{name: name, symbol: symbol, evidence: ev, forwarded: wasmImportedCalls(set, tree, fn, f, src, r)})
		}
	}
	return out
}

func wasmHelper(name string) bool {
	return strings.Contains(name, "[callback]") || strings.Contains(name, "[post-return]") || strings.Contains(name, "[task-return]") || strings.HasPrefix(name, "cabi_") || strings.Contains(name, "[dtor]")
}

// Supporting generated bindings intentionally have no metric symbols or call
// graph. Their AST still proves an imported package's exact function selector,
// including selectors inside the function literal passed to an async runner.
func wasmImportedCalls(set *token.FileSet, tree *ast.File, fn *ast.FuncDecl, f File, src Document, r *Report) map[string]EntryPointEvidence {
	imports := map[string]string{}
	for _, imp := range tree.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		alias := filepath.Base(path)
		for _, p := range r.Packages {
			if p.Path == path {
				alias = p.Name
				break
			}
		}
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias != "_" && alias != "." {
			imports[alias] = path
		}
	}
	retained := map[string]bool{}
	for _, file := range r.Files {
		if !generatedSource(file.Source) {
			retained[file.ID] = true
		}
	}
	targets := map[string][]Symbol{}
	for _, s := range r.Symbols {
		if retained[s.FileID] && s.Kind == "function" {
			targets[s.Package+"\x00"+s.Name] = append(targets[s.Package+"\x00"+s.Name], s)
		}
	}
	result := map[string]EntryPointEvidence{}
	if fn.Body == nil {
		return result
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		alias, ok := selector.X.(*ast.Ident)
		if !ok || alias.Obj != nil {
			return true
		}
		pkg, ok := imports[alias.Name]
		if !ok {
			return true
		}
		matches := targets[pkg+"\x00"+selector.Sel.Name]
		if len(matches) != 1 {
			return true
		}
		begin, end := set.Position(call.Pos()), set.Position(call.End())
		ev := EntryPointEvidence{SourceID: src.ID, Line: begin.Line, EndLine: end.Line, Kind: "wrapper-call", Detail: pkg + "." + selector.Sel.Name + " (exact imported selector in export wrapper)"}
		if f.ID != "" {
			ev.Location = &Location{FileID: f.ID, Line: begin.Line, Column: begin.Column, EndLine: end.Line, EndColumn: end.Column, Offset: begin.Offset, EndOffset: end.Offset}
		}
		result[matches[0].ID] = ev
		return true
	})
	return result
}

func wasmMatches(e EntryPoint, name string) bool {
	if e.Function == "" {
		return false
	}
	if e.Interface == "" {
		return name == e.Function
	}
	return name == e.Interface+"#"+e.Function
}

func wasmBind(r *Report, e *EntryPoint, d wasmDirective) {
	e.Evidence = append(e.Evidence, d.evidence)
	if d.symbol == "" {
		e.Reason = "Export directive matches WIT, but its Go boundary is absent from the analyzed symbol graph"
		if len(d.forwarded) == 1 {
			for symbol, ev := range d.forwarded {
				e.SymbolID = symbol
				e.Resolution = "resolved"
				e.Reason = "Export directive and exact imported wrapper call establish the handwritten implementation; generated boundary is excluded from metrics"
				e.Evidence = append(e.Evidence, ev)
			}
		}
		return
	}
	e.BoundarySymbolID = d.symbol
	e.SymbolID = d.symbol
	e.Resolution = "resolved"
	e.Reason = "Verified export directive boundary; no unique handwritten forwarding target could be established"
	symbols := map[string]Symbol{}
	files := map[string]File{}
	for _, s := range r.Symbols {
		symbols[s.ID] = s
	}
	for _, f := range r.Files {
		files[f.ID] = f
	}
	var candidates []Call
	for _, call := range r.Calls {
		if call.From != d.symbol || call.To == "" {
			continue
		}
		s, ok := symbols[call.To]
		if !ok || s.Kind == "builtin" {
			continue
		}
		f, ok := files[s.FileID]
		if !ok || generatedSource(f.Source) || strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		// Only source-owned functions reached by a verified static edge count.
		if s.ID == d.symbol || s.Name == "main" || strings.HasPrefix(s.Name, "wasm_export_") {
			continue
		}
		candidates = append(candidates, call)
	}
	unique := map[string]Call{}
	for _, c := range candidates {
		unique[c.To] = c
	}
	if len(unique) == 1 {
		for _, c := range unique {
			e.SymbolID = c.To
			e.Reason = "Export directive and resolved wrapper-call edge establish the Go implementation"
			loc := c.Location
			e.Evidence = append(e.Evidence, EntryPointEvidence{Kind: "wrapper-call", Detail: c.ID, Location: &loc})
		}
	}
}
