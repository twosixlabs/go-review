package review

import (
	"context"
	"go/types"
	"sort"
	"strings"
)

// EntryPointInventory separates externally callable operations from supporting
// initialization. A nil inventory means an older snapshot, not an empty API.
type EntryPointInventory struct {
	Kind         string       `json:"kind"`
	Entries      []EntryPoint `json:"entries"`
	Initializers []string     `json:"initializers"`
	Sources      []Document   `json:"sources"`
	Limitations  []string     `json:"limitations"`
}

type EntryPoint struct {
	ID               string               `json:"id"`
	Kind             string               `json:"kind"`
	Name             string               `json:"name"`
	SymbolID         string               `json:"symbolID,omitempty"`
	BoundarySymbolID string               `json:"boundarySymbolID,omitempty"`
	Resolution       string               `json:"resolution"`
	Reason           string               `json:"reason,omitempty"`
	World            string               `json:"world,omitempty"`
	Interface        string               `json:"interface,omitempty"`
	Function         string               `json:"function,omitempty"`
	Evidence         []EntryPointEvidence `json:"evidence"`
}

type EntryPointEvidence struct {
	SourceID string    `json:"sourceID,omitempty"`
	Line     int       `json:"line,omitempty"`
	EndLine  int       `json:"endLine,omitempty"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail,omitempty"`
	Location *Location `json:"location,omitempty"`
}

func (a *analyzer) collectEntryPoints(ctx context.Context) error {
	r := a.report
	inventory, err := collectWasmEntryPoints(ctx, r)
	if err != nil {
		return err
	}
	if inventory == nil {
		inventory = &EntryPointInventory{Kind: "library", Entries: []EntryPoint{},
			Sources: []Document{}, Initializers: []string{}, Limitations: []string{
				"Entry points are source declarations, not proof of runtime reachability.",
			}}
		var mains []Symbol
		hasMainPackage := false
		for _, p := range r.Packages {
			hasMainPackage = hasMainPackage || p.Name == "main"
		}
		for _, s := range r.Symbols {
			if s.Kind == "function" && s.Name == "main" && a.packages[s.Package].meta.Name == "main" {
				mains = append(mains, s)
			}
		}
		if hasMainPackage {
			inventory.Kind = "cli"
			for _, s := range mains {
				inventory.Entries = append(inventory.Entries, symbolEntry("main", s))
			}
			if len(r.HTTPEndpoints) > 0 {
				inventory.Kind = "service"
			}
			if len(mains) == 0 {
				inventory.Limitations = append(inventory.Limitations,
					"A main package was retained but no main function is available in the selected build.")
			}
		} else {
			surviving := make(map[string]bool)
			for _, s := range r.Symbols {
				surviving[s.ID] = true
			}
			for obj, id := range a.objects {
				fn, ok := obj.(*types.Func)
				if !ok || !fn.Exported() || !surviving[id] || fn.Pkg() == nil || internalPackage(fn.Pkg().Path()) {
					continue
				}
				signature, ok := fn.Type().(*types.Signature)
				if !ok {
					continue
				}
				if recv := signature.Recv(); recv != nil {
					t := recv.Type()
					if pointer, ok := t.(*types.Pointer); ok {
						t = pointer.Elem()
					}
					named, ok := t.(*types.Named)
					if !ok || !named.Obj().Exported() {
						continue
					}
				}
				for _, s := range r.Symbols {
					if s.ID == id {
						inventory.Entries = append(inventory.Entries, symbolEntry("library", s))
						break
					}
				}
			}
			inventory.Limitations = append(inventory.Limitations,
				"Public API entries are exported callable declarations on accessible packages and exported receiver types; types, constants and variables remain available in package evidence. Interface dispatch and promoted method reachability are not inferred.")
		}
	}
	for _, e := range r.HTTPEndpoints {
		resolution := "resolved"
		if e.HandlerID == "" {
			resolution = "unresolved"
		}
		name := e.Pattern
		if !e.PatternKnown {
			name = e.PatternExpression
		}
		inventory.Entries = append(inventory.Entries, EntryPoint{ID: e.ID, Kind: "http", Name: name,
			SymbolID: e.HandlerID, Resolution: resolution, Reason: e.Reason,
			Evidence: []EntryPointEvidence{{Kind: "http-registration", Location: &e.Location}}})
	}
	for _, s := range r.Symbols {
		if s.Kind == "function" && s.Name == "init" {
			inventory.Initializers = append(inventory.Initializers, s.ID)
		}
	}
	sort.Slice(inventory.Entries, func(i, j int) bool {
		return inventory.Entries[i].ID < inventory.Entries[j].ID
	})
	sort.Strings(inventory.Initializers)
	r.EntryPoints = inventory
	for _, source := range inventory.Sources {
		r.Inputs = append(r.Inputs, Input{Path: source.Path, SHA256: source.SHA256})
	}
	return nil
}

func internalPackage(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "internal" {
			return true
		}
	}
	return false
}

func symbolEntry(kind string, s Symbol) EntryPoint {
	name := s.Qualified
	if name == "" {
		name = s.Name
	}
	id := stableID("entry", kind+":"+s.ID)
	if kind == "main" {
		id = "startup:" + s.ID
	}
	return EntryPoint{ID: id, Kind: kind, Name: name, SymbolID: s.ID,
		Resolution: "resolved", Evidence: []EntryPointEvidence{{Kind: "go-declaration", Location: &s.Location}}}
}
