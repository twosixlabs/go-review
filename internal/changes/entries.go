package changes

import (
	"path/filepath"
	"strings"

	"github.com/russellbrasser/go-review/internal/review"
)

func entryProvenance(e review.EntryPoint, r *review.Report) string {
	if r == nil {
		return ""
	}
	if e.Kind == "http" {
		for _, endpoint := range r.HTTPEndpoints {
			if endpoint.ID != e.ID {
				continue
			}
			for _, owner := range r.Symbols {
				if owner.ID == endpoint.RegistrationID && symbolKey(owner) != "" {
					return strings.Join([]string{"http-registration", symbolKey(owner), endpoint.API, endpoint.Receiver, endpoint.Handler}, "\x00")
				}
			}
		}
		return ""
	}
	if e.Kind == "wasm" && r.EntryPoints != nil {
		for _, evidence := range e.Evidence {
			if evidence.Kind != "wit-export" {
				continue
			}
			for _, source := range r.EntryPoints.Sources {
				if source.ID == evidence.SourceID {
					return strings.Join([]string{"wit-export", source.Path, e.World, e.Interface, e.Function}, "\x00")
				}
			}
		}
	}
	return ""
}

// Exact contract matches are removed first. A changed HTTP contract is paired
// only when one unmatched registration remains on each side with the same
// owner/API/receiver/handler provenance; repeated ambiguous registrations stay apart.
func entryMatches(before, after *review.Report, bEntries, aEntries []review.EntryPoint, declarations map[string]review.Symbol) map[string]review.EntryPoint {
	matches := map[string]review.EntryPoint{}
	used := map[string]bool{}
	for _, key := range []func(review.EntryPoint, *review.Report) string{entryKey, entryProvenance} {
		bucketsB, bucketsA := map[string][]review.EntryPoint{}, map[string][]review.EntryPoint{}
		for _, e := range bEntries {
			if _, found := matches[e.ID]; !found {
				if k := key(e, before); k != "" {
					bucketsB[k] = append(bucketsB[k], e)
				}
			}
		}
		for _, e := range aEntries {
			if !used[e.ID] {
				if k := key(e, after); k != "" {
					bucketsA[k] = append(bucketsA[k], e)
				}
			}
		}
		for k, bs := range bucketsB {
			as := bucketsA[k]
			if len(bs) == 1 && len(as) == 1 {
				matches[bs[0].ID] = as[0]
				used[as[0].ID] = true
			}
		}
	}
	for _, b := range bEntries {
		if _, found := matches[b.ID]; found || (b.Kind != "library" && b.Kind != "main") {
			continue
		}
		for _, a := range aEntries {
			if !used[a.ID] && a.Kind == b.Kind && declarations[b.SymbolID].ID == a.SymbolID {
				matches[b.ID] = a
				used[a.ID] = true
				break
			}
		}
	}
	return matches
}

func entryLocation(r *review.Report, evidence review.EntryPointEvidence) (string, Range) {
	if evidence.Location != nil {
		for _, file := range r.Files {
			if file.ID == evidence.Location.FileID {
				return file.Path, Range{Start: evidence.Location.Line, Lines: max(1, evidence.Location.EndLine-evidence.Location.Line+1)}
			}
		}
	}
	if r.EntryPoints != nil && evidence.SourceID != "" {
		for _, source := range r.EntryPoints.Sources {
			if source.ID == evidence.SourceID {
				return source.Path, Range{Start: evidence.Line, Lines: max(1, evidence.EndLine-evidence.Line+1)}
			}
		}
	}
	return "", Range{}
}

// Entry provenance can change outside its handler (HTTP wiring, WIT contract,
// export directive). Associate exact changed spans, not the whole registering
// function, so unrelated routes sharing a handler are not all marked changed.
func (c *Comparison) associateEntryEvidence() error {
	for _, module := range c.Modules {
		for _, side := range []string{"before", "after"} {
			m := module.Before
			if side == "after" {
				m = module.After
			}
			if m.SnapshotID == "" {
				continue
			}
			r, err := c.Report(module.ID, side)
			if err != nil {
				return err
			}
			for f := range c.Flows {
				flow := &c.Flows[f]
				if flow.ModuleID != module.ID {
					continue
				}
				entry := flow.Before
				if side == "after" {
					entry = flow.After
				}
				if entry == nil || (entry.Kind != "http" && entry.Kind != "wasm") {
					continue
				}
				for n := range c.Records {
					record := &c.Records[n]
					var witness []Witness
					for _, evidence := range entry.Evidence {
						path, span := entryLocation(r, evidence)
						if path == "" {
							continue
						}
						path = filepath.ToSlash(filepath.Join(module.Path, path))
						for _, file := range c.Files {
							object := file.Before
							if side == "after" {
								object = file.After
							}
							if object.Path != path {
								continue
							}
							for _, item := range file.Items {
								if !containsID(record.ItemIDs, item.ID) {
									continue
								}
								changed := item.Before
								if side == "after" {
									changed = item.After
								}
								if changed.Lines > 0 && span.Start <= changed.Start+changed.Lines-1 && changed.Start <= span.Start+span.Lines-1 {
									location := evidence.Location
									if location == nil {
										location = &review.Location{FileID: evidence.SourceID, Line: span.Start, EndLine: span.Start + span.Lines - 1}
									}
									witness = append(witness, Witness{Kind: evidence.Kind, To: entry.SymbolID, Location: location})
								}
							}
						}
					}
					if len(witness) > 0 {
						flow.Associations = append(flow.Associations, Association{RecordID: record.ID, Side: side, Quality: "entry-contract-change", Witness: witness})
						record.FlowIDs = unique(record.FlowIDs, flow.ID)
					}
				}
			}
		}
	}
	included := c.Flows[:0]
	for _, flow := range c.Flows {
		if len(flow.Associations) > 0 {
			included = append(included, flow)
		}
	}
	c.Flows = included
	return nil
}

func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
