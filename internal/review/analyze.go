package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type listedModule struct {
	Path, Dir, GoVersion string
}

type listedPackage struct {
	ImportPath, Name, Dir, Export string
	GoFiles, CgoFiles, Imports    []string
	TestGoFiles, XTestGoFiles     []string
	DepOnly                       bool
	Module                        *listedModule
	Error                         *struct{ Err string }
	DepsErrors                    []struct{ Err string }
}

type parsedPackage struct {
	meta listedPackage
	ast  []*ast.File
	info *types.Info
	pkg  *types.Package
	busy bool
}

type analyzer struct {
	report   *Report
	fset     *token.FileSet
	packages map[string]*parsedPackage
	fileIDs  map[string]string
	objects  map[types.Object]string
	literals map[*ast.FuncLit]string
	exports  map[string]string
	fallback types.Importer
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func stableID(kind, key string) string { return kind + "-" + digest([]byte(key))[:24] }

// Analyze only executes Go tooling unless exact unit-test selections are supplied.
// Go commands use -mod=readonly and GOTOOLCHAIN=local to avoid manifest rewrites and
// automatic toolchain downloads. Module downloads and build-cache writes may still occur.
func Analyze(ctx context.Context, opts Options) (*Report, error) {
	scope, scopeErr := resolveScope(opts)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if len(opts.UnitSelections) > 0 && opts.CoverageProfile != "" {
		return nil, fmt.Errorf("test execution and imported coverage are mutually exclusive")
	}
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	r := &Report{
		Scope: scope, Exclusions: []ScopeExclusion{},
		SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), ToolVersion: "go-review/0.1.0 (" + runtime.Version() + ")",
		Packages: []Package{}, Files: []File{}, Symbols: []Symbol{}, Calls: []Call{}, ControlFlows: []ControlFlow{}, References: []Reference{},
		Documents: []Document{}, TestSources: []TestSource{}, Inputs: []Input{}, Warnings: []string{}, HTTPEndpoints: []HTTPEndpoint{},
		Limitations: []string{
			"AI is disabled; all evidence is local static analysis or explicitly requested Go test output.",
			"Intraprocedural SSA operand flow is available on demand. Interprocedural propagation, pointer alias analysis and runtime argument/return values are unavailable. Runtime evidence exists only when explicitly recorded.",
			"Calls are syntax/type-resolved, not a whole-program call graph. Interface dispatch and function values remain indirect. No reachability or dead-code claims.",
			"HTTP inventory recognizes type-verified net/http Handle and HandleFunc registration sites only. Startup reachability, mounted prefixes, middleware behavior, indirect registrations and other routers are not resolved. Dynamic patterns and handlers remain explicit.",
			"Cyclomatic = 1 + if/for/range + nondefault case/communication clauses + &&/||; function literals are measured separately. This is not cognitive complexity.",
			"Architecture signals are versioned source heuristics, not proven defects or architectural certification. Complexity, function size, coverage and mechanical hygiene do not contribute to the concern indicator. Unexecuted repository checks remain Not run.",
			"Production analysis uses current-build GoFiles/CgoFiles in selected ./... packages. Included test sources are snapshotted separately for source evidence, not type-checked or added to normal metrics. Excluded build tags, generated cgo stubs, dependencies and nested modules are not analyzed.",
			"Type errors and missing dependency export data degrade call resolution and are reported. External calls have labels but no local source target.",
			"Coverage is statement/block evidence, not branch coverage or function tracing. Imported profiles cannot prove source freshness; exact UTF-8 byte ranges are preserved.",
			"Markdown is rendered locally with sanitized HTML; active content and images are blocked. Comments and documents are evidence, not verified claims.",
			"Reports embed source, local paths and optional test output. Review for secrets before sharing. Snapshot consistency is best-effort, not a locked filesystem transaction.",
		},
	}
	envData, err := runGo(ctx, r, dir, false, "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOWORK", "GOVERSION", "GOTOOLCHAIN", "GOMOD")
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	if err = json.Unmarshal(envData, &r.Environment); err != nil {
		return nil, err
	}
	moduleData, err := runGo(ctx, r, dir, false, "list", "-mod=readonly", "-m", "-json")
	if err != nil {
		return nil, fmt.Errorf("locate module: %w", err)
	}
	var mod listedModule
	if err = json.Unmarshal(moduleData, &mod); err != nil {
		return nil, err
	}
	if mod.Dir == "" || mod.Path == "" {
		return nil, fmt.Errorf("target must be inside a Go module")
	}
	r.Root, r.Module = mod.Dir, mod.Path
	git := exec.CommandContext(ctx, "git", "-C", r.Root, "rev-parse", "HEAD")
	if revision, e := git.Output(); e == nil {
		r.Revision.Commit = strings.TrimSpace(string(revision))
		r.Revision.Available = true
		status := exec.CommandContext(ctx, "git", "-C", r.Root, "status", "--porcelain", "--untracked-files=normal")
		if dirty, e := status.Output(); e == nil {
			r.Revision.Dirty = len(dirty) > 0
		}
	}
	listData, listErr := runGo(ctx, r, dir, false, "list", "-mod=readonly", "-e", "-deps", "-export", "-json", "./...")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if listErr != nil {
		r.Warnings = append(r.Warnings, "go list returned an error: "+listErr.Error())
	}
	a := &analyzer{report: r, fset: token.NewFileSet(), packages: map[string]*parsedPackage{}, fileIDs: map[string]string{}, objects: map[types.Object]string{}, literals: map[*ast.FuncLit]string{}, exports: map[string]string{}}
	dec := json.NewDecoder(bytes.NewReader(listData))
	for {
		var p listedPackage
		err = dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list: %w", err)
		}
		if p.Export != "" {
			a.exports[p.ImportPath] = p.Export
		}
		if p.DepOnly {
			continue
		}
		if !inside(r.Root, p.Dir) {
			r.Warnings = append(r.Warnings, "skipped package outside module: "+p.ImportPath)
			continue
		}
		if p.Error != nil {
			r.Warnings = append(r.Warnings, p.ImportPath+": "+p.Error.Err)
		}
		for _, e := range p.DepsErrors {
			r.Warnings = append(r.Warnings, p.ImportPath+": "+e.Err)
		}
		a.packages[p.ImportPath] = &parsedPackage{meta: p}
	}
	if len(a.packages) == 0 {
		return nil, fmt.Errorf("no packages found in %s: %v", dir, listErr)
	}
	paths := make([]string, 0, len(a.packages))
	for path := range a.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p := a.packages[path]
		names := append(append([]string{}, p.meta.GoFiles...), p.meta.CgoFiles...)
		sort.Strings(names)
		for _, name := range names {
			abs := filepath.Join(p.meta.Dir, name)
			if !inside(r.Root, abs) {
				r.Warnings = append(r.Warnings, "skipped source outside module: "+abs)
				continue
			}
			src, readErr := os.ReadFile(abs)
			if readErr != nil {
				return nil, fmt.Errorf("read source %s: %w", abs, readErr)
			}
			rel, _ := filepath.Rel(r.Root, abs)
			rel = filepath.ToSlash(rel)
			id := stableID("file", p.meta.ImportPath+"/"+name)
			a.fileIDs[abs] = id
			r.Files = append(r.Files, File{ID: id, Package: path, Path: rel, SHA256: digest(src), Source: string(src)})
			f, parseErr := parser.ParseFile(a.fset, abs, src, parser.ParseComments|parser.AllErrors)
			if parseErr != nil {
				r.Warnings = append(r.Warnings, rel+": "+parseErr.Error())
			}
			if f != nil {
				p.ast = append(p.ast, f)
			}
		}
	}
	if err = collectTestSources(ctx, a, paths); err != nil {
		return nil, err
	}
	a.fallback = importer.ForCompiler(a.fset, "gc", func(path string) (io.ReadCloser, error) {
		if file := a.exports[path]; file != "" {
			return os.Open(file)
		}
		return nil, fmt.Errorf("no export data for %s", path)
	})
	for _, path := range paths {
		_, _ = a.Import(path)
	}
	for _, path := range paths {
		a.collectSymbols(a.packages[path])
	}
	for _, path := range paths {
		a.collectEdges(a.packages[path])
	}
	for _, path := range paths {
		a.collectControlFlows(a.packages[path])
	}
	documentationRoot := r.Root
	if opts.DocumentationDir == "" {
		for candidate := r.Root; ; candidate = filepath.Dir(candidate) {
			_, statErr := os.Stat(filepath.Join(candidate, ".git"))
			if statErr == nil {
				documentationRoot = candidate
				break
			}
			if !os.IsNotExist(statErr) {
				return nil, fmt.Errorf("locate documentation repository: %w", statErr)
			}
			if filepath.Dir(candidate) == candidate {
				break
			}
		}
	} else {
		documentationRoot, err = filepath.Abs(opts.DocumentationDir)
		if err != nil {
			return nil, err
		}
		info, statErr := os.Stat(documentationRoot)
		if statErr != nil {
			return nil, fmt.Errorf("documentation directory: %w", statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("documentation path is not a directory")
		}
	}
	r.DocumentationRoot = documentationRoot
	if err = a.collectDocuments(opts.ArtifactDir); err != nil {
		return nil, err
	}
	for _, name := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		data, e := os.ReadFile(filepath.Join(r.Root, name))
		if e == nil {
			r.Inputs = append(r.Inputs, Input{Path: name, SHA256: digest(data)})
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	applyScope(r)
	if err = collectOperationSources(a); err != nil {
		return nil, err
	}
	if err = a.collectEntryPoints(ctx); err != nil {
		return nil, err
	}
	a.finalize()
	r.Assessment = Assess(r)
	if opts.CoverageProfile != "" {
		f, e := os.Open(opts.CoverageProfile)
		if e != nil {
			return nil, e
		}
		e = ImportCoverage(r, f, opts.CoverageProfile)
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(opts.UnitSelections) > 0 {
		if err = CollectUnitEvidence(ctx, r, UnitEvidenceOptions{Selections: opts.UnitSelections, ArtifactDir: opts.ArtifactDir}); err != nil {
			if r.UnitEvidence == nil {
				return nil, err
			}
			r.Warnings = append(r.Warnings, "Selected unit evidence: "+err.Error())
		}
	}
	r.SystemFlow = BuildSystemFlow(r)
	return r, nil
}

func inside(root, path string) bool {
	if path == "" {
		return false
	}
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func runGo(ctx context.Context, r *Report, dir string, keep bool, args ...string) ([]byte, error) {
	return runCommand(ctx, r, dir, keep, "go", args...)
}

func runCommand(ctx context.Context, r *Report, dir string, keep bool, executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	boundReviewProcess(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	c := Command{Executable: executable, Args: args, Directory: dir, StartedAt: time.Now().UTC()}
	notify, _ := ctx.Value(commandProgressKey{}).(func(*Command))
	if notify != nil {
		// Publish a separate immutable value; retained command results change below.
		running := c
		notify(&running)
	}
	err := cmd.Run()
	if notify != nil {
		notify(nil)
	}
	c.FinishedAt = time.Now().UTC()
	c.StdoutSHA256 = digest(out.Bytes())
	c.Stderr = stderr.String()
	if err != nil {
		c.ExitCode = -1
		if e, ok := err.(*exec.ExitError); ok {
			c.ExitCode = e.ExitCode()
		}
	}
	if keep || (len(args) > 0 && args[0] == "env") {
		c.Stdout = out.String()
	}
	r.Commands = append(r.Commands, c)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: %v", ctx.Err(), err)
		}
		return out.Bytes(), fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// Import owns the package cache so local calls share the same go/types objects
// across packages instead of mixing source objects with dependency export objects.
func (a *analyzer) Import(path string) (*types.Package, error) {
	p, ok := a.packages[path]
	if !ok {
		return a.fallback.Import(path)
	}
	if p.pkg != nil {
		return p.pkg, nil
	}
	if p.busy {
		return nil, fmt.Errorf("import cycle: %s", path)
	}
	p.busy = true
	defer func() { p.busy = false }()
	p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Instances: map[*ast.Ident]types.Instance{}}
	pack := Package{ID: stableID("pkg", path), Path: path, Name: p.meta.Name, Imports: append([]string{}, p.meta.Imports...), FileIDs: []string{}, TypeErrors: []string{}}
	sort.Strings(pack.Imports)
	for _, f := range p.ast {
		if f.Doc != nil {
			if pack.Doc != "" {
				pack.Doc += "\n"
			}
			pack.Doc += f.Doc.Text()
		}
		pack.FileIDs = append(pack.FileIDs, a.fileIDs[a.fset.PositionFor(f.Pos(), false).Filename])
	}
	config := types.Config{Importer: a, Sizes: types.SizesFor("gc", a.report.Environment["GOARCH"]), Error: func(err error) {
		if len(pack.TypeErrors) < 50 {
			pack.TypeErrors = append(pack.TypeErrors, err.Error())
		}
	}}
	if p.meta.Module != nil && p.meta.Module.GoVersion != "" {
		config.GoVersion = "go" + p.meta.Module.GoVersion
	}
	pkg, err := config.Check(path, a.fset, p.ast, p.info)
	p.pkg = pkg
	if len(pack.TypeErrors) > 0 {
		a.report.Warnings = append(a.report.Warnings, fmt.Sprintf("%s: type checking incomplete (%d errors shown)", path, len(pack.TypeErrors)))
	}
	if len(p.meta.CgoFiles) > 0 {
		a.report.Warnings = append(a.report.Warnings, path+": cgo source analyzed without generated cgo stubs")
	}
	a.report.Packages = append(a.report.Packages, pack)
	return pkg, err
}

func (a *analyzer) location(start, end token.Pos) Location {
	s, e := a.fset.PositionFor(start, false), a.fset.PositionFor(end, false)
	return Location{FileID: a.fileIDs[s.Filename], Line: s.Line, Column: s.Column, EndLine: e.Line, EndColumn: e.Column, Offset: s.Offset, EndOffset: e.Offset}
}

func (a *analyzer) collectDocuments(outputDir string) error {
	out, _ := filepath.Abs(outputDir)
	return filepath.WalkDir(a.report.DocumentationRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			a.report.Warnings = append(a.report.Warnings, "document scan: "+err.Error())
			return nil
		}
		if rel, e := filepath.Rel(a.report.Scope.Root, path); e == nil {
			if reason := a.report.Scope.pathReason(filepath.ToSlash(rel)); reason != "" {
				local, e := filepath.Rel(a.report.Root, path)
				if e != nil {
					return e
				}
				recordExclusion(a.report, filepath.ToSlash(local), reason)
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if path != a.report.DocumentationRoot && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == "testwork" || d.Name() == "go-review-report" || (outputDir != "" && path == out)) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Size() > 2<<20 {
			a.report.Warnings = append(a.report.Warnings, "markdown larger than 2 MiB skipped: "+path)
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(a.report.DocumentationRoot, path)
		rel = filepath.ToSlash(rel)
		a.report.Documents = append(a.report.Documents, Document{ID: stableID("doc", rel), Path: rel, SHA256: digest(data), Text: string(data)})
		return nil
	})
}

func (a *analyzer) finalize() {
	r := a.report
	sort.Slice(r.Packages, func(i, j int) bool { return r.Packages[i].Path < r.Packages[j].Path })
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	sort.Slice(r.Symbols, func(i, j int) bool { return r.Symbols[i].ID < r.Symbols[j].ID })
	sort.Slice(r.Calls, func(i, j int) bool { return r.Calls[i].ID < r.Calls[j].ID })
	sort.Slice(r.HTTPEndpoints, func(i, j int) bool { return r.HTTPEndpoints[i].ID < r.HTTPEndpoints[j].ID })
	sort.Slice(r.References, func(i, j int) bool {
		x, y := r.References[i], r.References[j]
		if x.FileID != y.FileID {
			return x.FileID < y.FileID
		}
		if x.Offset != y.Offset {
			return x.Offset < y.Offset
		}
		return x.To < y.To
	})
	sort.Slice(r.Documents, func(i, j int) bool { return r.Documents[i].Path < r.Documents[j].Path })
	sort.Slice(r.TestSources, func(i, j int) bool { return r.TestSources[i].File.Path < r.TestSources[j].File.Path })
	sort.Strings(r.Warnings)
	var manifest strings.Builder
	if r.Scope.Root != "" {
		scopeData, _ := json.Marshal(r.Scope)
		fmt.Fprintf(&manifest, "review-scope\x00%s\n", scopeData)
	}
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		fmt.Fprintf(&manifest, "%s\x00%s\n", f.Path, f.SHA256)
	}
	for _, source := range r.TestSources {
		fmt.Fprintf(&manifest, "%s\x00%s\n", source.File.Path, source.File.SHA256)
	}
	for _, d := range r.Documents {
		fmt.Fprintf(&manifest, "%s\x00%s\n", d.Path, d.SHA256)
	}
	for _, in := range r.Inputs {
		fmt.Fprintf(&manifest, "%s\x00%s\n", in.Path, in.SHA256)
	}
	r.SourceSHA256 = digest([]byte(manifest.String()))
	stampOperationSourceAnchors(r)
}
