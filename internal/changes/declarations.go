package changes

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"sort"
	"strconv"

	"github.com/russellbrasser/go-review/internal/review"
)

func sourceShapes(r *review.Report, excluded map[string]review.Symbol) map[string][]review.Symbol {
	shapes := map[string][]review.Symbol{}
	if r == nil {
		return shapes
	}
	for _, file := range r.Files {
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, file.Path, file.Source, 0)
		if err != nil {
			continue
		}
		for _, decl := range tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name == "init" {
				continue
			}
			for _, symbol := range r.Symbols {
				if symbol.FileID != file.ID || symbol.Name != fn.Name.Name || symbol.Line != fset.Position(fn.Pos()).Line || symbol.Kind == "closure" {
					continue
				}
				if _, matched := excluded[symbol.ID]; matched {
					continue
				}
				fn.Name.Name = "__declaration"
				var normalized bytes.Buffer
				if err := printer.Fprint(&normalized, fset, fn); err == nil {
					key := identity(symbol.Package, symbol.Kind, normalized.String())
					shapes[key] = append(shapes[key], symbol)
				}
				fn.Name.Name = symbol.Name
			}
		}
	}
	return shapes
}

func (c *Comparison) declarationMatches(module Module, before, after *review.Report, bkeys, akeys map[string][]review.Symbol) (map[string]review.Symbol, map[string]review.Symbol) {
	bmatch, amatch := map[string]review.Symbol{}, map[string]review.Symbol{}
	match := func(b, a review.Symbol, reason string) {
		bmatch[b.ID] = a
		amatch[a.ID] = b
		bl, al := b.Location, a.Location
		c.Correspondences = append(c.Correspondences, Correspondence{Kind: "declaration", ModuleID: module.ID, BeforeID: b.ID, AfterID: a.ID, BeforeLocation: &bl, AfterLocation: &al, Reason: reason})
	}
	for key, bs := range bkeys {
		as := akeys[key]
		if len(bs) == 1 && len(as) == 1 {
			match(bs[0], as[0], "Unique module/package/declaration kind/receiver/name")
		}
	}
	bshape, ashape := sourceShapes(before, bmatch), sourceShapes(after, amatch)
	for key, bs := range bshape {
		as := ashape[key]
		if len(bs) == 1 && len(as) == 1 {
			match(bs[0], as[0], "Unique identical parsed declaration structure in the same package, excluding declaration name; source-backed move/rename")
		}
	}
	c.matchClosures(module, before, after, bmatch, amatch)
	c.matchCalls(module, before, after, bmatch, amatch)
	return bmatch, amatch
}

type ownedSpan struct {
	span  Range
	owner string
}

func partitionSpan(r *review.Report, modulePath, path string, span Range) []ownedSpan {
	if span.Lines == 0 {
		return nil
	}
	boundaries := []int{span.Start, span.Start + span.Lines}
	for _, s := range ownersFor(r, modulePath, path, span) {
		boundaries = append(boundaries, max(span.Start, s.Line), min(span.Start+span.Lines, s.EndLine+1))
	}
	sort.Ints(boundaries)
	out := []ownedSpan{}
	for i := 1; i < len(boundaries); i++ {
		start, end := boundaries[i-1], boundaries[i]
		if start == end {
			continue
		}
		rangePart := Range{Start: start, Lines: end - start}
		owners := ownersFor(r, modulePath, path, rangePart)
		owner := ""
		if len(owners) > 0 {
			owner = owners[0].ID
		}
		if len(out) > 0 && out[len(out)-1].owner == owner {
			out[len(out)-1].span.Lines += rangePart.Lines
		} else {
			out = append(out, ownedSpan{rangePart, owner})
		}
	}
	return out
}

// Partition source ranges, not whole patches, so adjacent/nested declarations
// cannot hide one another. Each side's changed lines occur in exactly one item.
func (c *Comparison) splitModuleItems(module Module, before, after *review.Report, matches map[string]review.Symbol) {
	for f := range c.Files {
		file := &c.Files[f]
		next := []Item{}
		for _, item := range file.Items {
			if item.Kind != "text" {
				next = append(next, item)
				continue
			}
			bs, as := partitionSpan(before, module.Path, file.Before.Path, item.Before), partitionSpan(after, module.Path, file.After.Path, item.After)
			unmatchedOwners := len(bs) == 1 && len(as) == 1 && bs[0].owner != "" && as[0].owner != "" && matches[bs[0].owner].ID != as[0].owner
			if len(bs) <= 1 && len(as) <= 1 && !unmatchedOwners {
				next = append(next, item)
				continue
			}
			used := map[int]bool{}
			add := func(b, a Range) {
				next = append(next, Item{ID: identity(item.ID, strconv.Itoa(len(next))), ParentID: item.ID, FileID: item.FileID, Kind: item.Kind, Before: b, After: a})
			}
			for _, b := range bs {
				paired := -1
				if target, ok := matches[b.owner]; ok {
					for i, a := range as {
						if !used[i] && a.owner == target.ID {
							paired = i
							break
						}
					}
				}
				if paired >= 0 {
					used[paired] = true
					add(b.span, as[paired].span)
				} else {
					add(b.span, Range{Start: item.After.Start})
				}
			}
			for i, a := range as {
				if !used[i] {
					add(Range{Start: item.Before.Start}, a.span)
				}
			}
		}
		file.Items = next
	}
}
