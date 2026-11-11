package review

import (
	"fmt"
	"strings"
	"unicode"
)

// This deliberately bounded WIT reader records unsupported exports instead of
// pretending to validate or elaborate the complete component-model type system.
type witToken struct {
	text string
	line int
}
type witDecl struct {
	name, pkg string
	body      []witToken
	source    Document
	line      int
}
type witPackage struct {
	worlds, interfaces []witDecl
	limitations        []string
	name               string
}

func witTokens(text string) ([]witToken, error) {
	var out []witToken
	line := 1
	for i := 0; i < len(text); {
		c := text[i]
		if c == '\n' {
			line++
			i++
			continue
		}
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if strings.HasPrefix(text[i:], "//") {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(text[i:], "/*") {
			depth := 1
			i += 2
			for i < len(text) && depth > 0 {
				if strings.HasPrefix(text[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(text[i:], "*/") {
					depth--
					i += 2
				} else {
					if text[i] == '\n' {
						line++
					}
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated WIT comment")
			}
			continue
		}
		start := i
		if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '%' || c == '_' {
			i++
			for i < len(text) {
				x := text[i]
				if !(unicode.IsLetter(rune(x)) || unicode.IsDigit(rune(x)) || x == '-' || x == '_' || x == '.' || x == '+') {
					break
				}
				i++
			}
		} else {
			i++
		}
		out = append(out, witToken{text: strings.TrimPrefix(text[start:i], "%"), line: line})
		if len(out) > 200000 {
			return nil, fmt.Errorf("WIT token limit exceeded")
		}
	}
	return out, nil
}

func witJoin(ts []witToken) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.text)
	}
	return b.String()
}

func witBlock(ts []witToken, open int) (body []witToken, next int, ok bool) {
	depth := 0
	for i := open; i < len(ts); i++ {
		switch ts[i].text {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return ts[open+1 : i], i + 1, true
			}
		}
	}
	return nil, len(ts), false
}

func parseWIT(src Document) witPackage {
	var result witPackage
	ts, err := witTokens(src.Text)
	if err != nil {
		result.limitations = append(result.limitations, src.Path+": "+err.Error())
		return result
	}
	pkg := ""
	for i := 0; i < len(ts); {
		t := ts[i]
		if t.text == "package" {
			j := i + 1
			for j < len(ts) && ts[j].text != ";" && ts[j].text != "{" {
				j++
			}
			if j == len(ts) || ts[j].text != ";" {
				result.limitations = append(result.limitations, src.Path+": nested or malformed package declaration is unsupported")
				return result
			}
			pkg = witJoin(ts[i+1 : j])
			result.name = pkg
			i = j + 1
			continue
		}
		if (t.text == "world" || t.text == "interface") && i+2 < len(ts) && ts[i+2].text == "{" {
			body, next, ok := witBlock(ts, i+2)
			if !ok {
				result.limitations = append(result.limitations, fmt.Sprintf("%s:%d: unterminated %s", src.Path, t.line, t.text))
				return result
			}
			d := witDecl{name: ts[i+1].text, pkg: pkg, body: body, source: src, line: t.line}
			if t.text == "world" {
				result.worlds = append(result.worlds, d)
			} else {
				result.interfaces = append(result.interfaces, d)
			}
			i = next
			continue
		}
		i++
	}
	return result
}

func witIdentity(pkg, name string) string {
	base, version, hasVersion := strings.Cut(pkg, "@")
	if base == "" {
		return name
	}
	id := base + "/" + name
	if hasVersion {
		id += "@" + version
	}
	return id
}

// Statements are split only at top level; record/variant/resource bodies and
// inline interfaces must not leak declarations into their containing world.
func witStatements(ts []witToken) [][]witToken {
	var out [][]witToken
	for i := 0; i < len(ts); {
		start := i
		depth := 0
		for i < len(ts) {
			t := ts[i].text
			i++
			if t == "{" {
				depth++
			}
			if t == "}" {
				depth--
				if depth == 0 {
					break
				}
			}
			if t == ";" && depth == 0 {
				break
			}
		}
		out = append(out, witUnannotated(ts[start:i]))
	}
	return out
}

func witUnannotated(ts []witToken) []witToken {
	for len(ts) > 1 && ts[0].text == "@" {
		ts = ts[2:]
		if len(ts) > 0 && ts[0].text == "(" {
			depth := 0
			end := 0
			for end < len(ts) {
				t := ts[end].text
				end++
				if t == "(" {
					depth++
				}
				if t == ")" {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			ts = ts[end:]
		}
	}
	return ts
}

type witResolver struct {
	worlds, interfaces map[string][]witDecl
	inv                *EntryPointInventory
}

func (w *witResolver) lookup(ref, pkg string, world bool) (witDecl, bool) {
	m := w.interfaces
	if world {
		m = w.worlds
	}
	if !strings.Contains(ref, "/") {
		ref = witIdentity(pkg, ref)
	}
	ds := m[ref]
	if len(ds) == 1 {
		return ds[0], true
	}
	return witDecl{}, false
}

func witEvidence(d witDecl, line int, kind, detail string) EntryPointEvidence {
	return EntryPointEvidence{SourceID: d.source.ID, Line: line, EndLine: line, Kind: kind, Detail: detail}
}

func (w *witResolver) world(d witDecl, selected string, ev []EntryPointEvidence, seen map[string]bool) {
	key := witIdentity(d.pkg, d.name)
	if len(seen) > 64 || seen[key] {
		w.inv.Limitations = append(w.inv.Limitations, "Cyclic or excessive WIT world includes: "+key)
		return
	}
	seen[key] = true
	defer delete(seen, key)
	for _, s := range witStatements(d.body) {
		if len(s) < 2 {
			continue
		}
		if s[0].text == "include" {
			end := len(s)
			if s[end-1].text == ";" {
				end--
			}
			ref := witJoin(s[1:end])
			renamed := false
			for _, t := range s[1:end] {
				if t.text == "with" {
					renamed = true
				}
			}
			if renamed {
				w.unresolved(d, selected, ref, "", "WIT include renaming is unsupported", s[0].line, ev)
				continue
			}
			if inc, ok := w.lookup(ref, d.pkg, true); ok {
				w.world(inc, selected, append(append([]EntryPointEvidence{}, ev...), witEvidence(d, s[0].line, "wit-include", ref)), seen)
			} else {
				w.unresolved(d, selected, ref, "", "Included WIT world unavailable or ambiguous", s[0].line, ev)
			}
			continue
		}
		if s[0].text != "export" {
			continue
		}
		end := len(s)
		if s[end-1].text == ";" {
			end--
		}
		body := s[1:end]
		line := s[0].line
		if len(body) == 0 {
			continue
		}
		exportEv := append(append([]EntryPointEvidence{}, ev...), witEvidence(d, line, "wit-export", witJoin(body)))
		// A direct function's colon is immediately followed by func/async.
		if len(body) > 2 && body[1].text == ":" && (body[2].text == "func" || body[2].text == "async") {
			w.entry(d, selected, "", body[0].text, line, exportEv)
			continue
		}
		if len(body) > 3 && body[1].text == ":" && body[2].text == "interface" && body[3].text == "{" {
			b, _, ok := witBlock(body, 3)
			if !ok {
				w.unresolved(d, selected, body[0].text, "", "Malformed inline WIT export", line, exportEv)
				continue
			}
			w.functions(witDecl{pkg: d.pkg, name: body[0].text, body: b, source: d.source, line: line}, selected, body[0].text, exportEv)
			continue
		}
		ref := witJoin(body)
		// Alias: external:package/interface is distinguishable from the
		// namespace colon because the latter occurs before a slash.
		if len(body) > 2 && body[1].text == ":" {
			colons := 0
			for _, t := range body {
				if t.text == ":" {
					colons++
				}
			}
			if colons > 1 || !strings.Contains(ref, "/") {
				ref = witJoin(body[2:])
			}
		}
		if iface, ok := w.lookup(ref, d.pkg, false); ok {
			identity := ref
			if !strings.Contains(ref, "/") {
				identity = witIdentity(iface.pkg, iface.name)
			}
			w.functions(iface, selected, identity, exportEv)
		} else {
			w.unresolved(d, selected, ref, "", "Exported WIT interface unavailable or ambiguous", line, exportEv)
		}
	}
}

func (w *witResolver) functions(d witDecl, world, iface string, ev []EntryPointEvidence) {
	count := 0
	for _, s := range witStatements(d.body) {
		if len(s) > 2 && s[1].text == ":" && (s[2].text == "func" || s[2].text == "async") {
			w.entry(d, world, iface, s[0].text, s[0].line, append(append([]EntryPointEvidence{}, ev...), witEvidence(d, s[0].line, "wit-function", s[0].text)))
			count++
		} else if len(s) > 0 && s[0].text == "resource" {
			w.unresolved(d, world, iface, witJoin(s), "Exported WIT resource methods are unsupported", s[0].line, ev)
			count++
		}
	}
	if count == 0 {
		w.inv.Limitations = append(w.inv.Limitations, "Exported WIT interface has no discovered callable functions: "+iface)
	}
}

func (w *witResolver) entry(d witDecl, world, iface, fn string, line int, ev []EntryPointEvidence) {
	name := fn
	if iface != "" {
		name = iface + "#" + fn
	}
	w.inv.Entries = append(w.inv.Entries, EntryPoint{ID: stableID("entry", "wasm:"+world+":"+name), Kind: "wasm", Name: name, World: world, Interface: iface, Function: fn, Resolution: "unresolved", Reason: "No matching export directive and verified Go binding in the retained source", Evidence: ev})
}

func (w *witResolver) unresolved(d witDecl, world, iface, fn, reason string, line int, ev []EntryPointEvidence) {
	w.entry(d, world, iface, fn, line, append(append([]EntryPointEvidence{}, ev...), witEvidence(d, line, "unresolved", reason)))
	w.inv.Entries[len(w.inv.Entries)-1].Reason = reason
	w.inv.Limitations = append(w.inv.Limitations, reason+": "+iface)
}
