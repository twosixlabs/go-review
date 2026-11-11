package changes

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/russellbrasser/go-review/internal/review"
)

type Owner struct {
	SymbolID string          `json:"symbolID"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Location review.Location `json:"location"`
}

type Record struct {
	ID       string   `json:"id"`
	ModuleID string   `json:"moduleID,omitempty"`
	Title    string   `json:"title"`
	Kind     string   `json:"kind"`
	ItemIDs  []string `json:"itemIDs"`
	FileIDs  []string `json:"fileIDs"`
	Before   *Owner   `json:"before,omitempty"`
	After    *Owner   `json:"after,omitempty"`
	FlowIDs  []string `json:"flowIDs"`
	Reason   string   `json:"reason"`
}

type Witness struct {
	Kind     string           `json:"kind"`
	From     string           `json:"from,omitempty"`
	To       string           `json:"to,omitempty"`
	CallID   string           `json:"callID,omitempty"`
	Location *review.Location `json:"location,omitempty"`
}

type Association struct {
	RecordID string    `json:"recordID"`
	Side     string    `json:"side"`
	Quality  string    `json:"quality"`
	Witness  []Witness `json:"witness"`
}

type Flow struct {
	ID           string             `json:"id"`
	ModuleID     string             `json:"moduleID"`
	Name         string             `json:"name"`
	Kind         string             `json:"kind"`
	Before       *review.EntryPoint `json:"before,omitempty"`
	After        *review.EntryPoint `json:"after,omitempty"`
	Status       string             `json:"status"`
	Associations []Association      `json:"associations"`
}

type Correspondence struct {
	Kind           string           `json:"kind"`
	BeforeLocation *review.Location `json:"beforeLocation,omitempty"`
	AfterLocation  *review.Location `json:"afterLocation,omitempty"`
	ModuleID       string           `json:"moduleID"`
	BeforeID       string           `json:"beforeID"`
	AfterID        string           `json:"afterID"`
	Reason         string           `json:"reason"`
}

func symbolKey(s review.Symbol) string {
	// Closures and init declarations need richer enclosing/call-site evidence.
	// Refusing to match them is preferable to treating source offsets as identity.
	if s.Kind == "closure" || s.Name == "init" {
		return ""
	}
	receiver := ""
	if s.Kind == "method" {
		prefix, _, ok := strings.Cut(s.Qualified, ")."+s.Name+"(")
		if !ok {
			return ""
		}
		receiver = prefix + ")"
	}
	return s.Kind + "\x00" + s.Package + "\x00" + receiver + "\x00" + s.Name
}

func declarationName(s review.Symbol) string {
	if s.Kind == "method" {
		prefix, _, ok := strings.Cut(s.Qualified, ")."+s.Name+"(")
		if ok {
			return strings.TrimPrefix(prefix, "func ") + ")." + s.Name
		}
	}
	return s.Package + "." + s.Name
}
func ownersFor(r *review.Report, modulePath, path string, span Range) []review.Symbol {
	if r == nil || span.Lines == 0 {
		return nil
	}
	var fileID string
	for _, file := range r.Files {
		full := filepath.ToSlash(filepath.Join(modulePath, file.Path))
		if full == path {
			fileID = file.ID
			break
		}
	}
	if fileID == "" {
		return nil
	}
	owners := []review.Symbol{}
	for _, s := range r.Symbols {
		if s.FileID == fileID && span.Start <= s.EndLine && span.Start+span.Lines-1 >= s.Line {
			owners = append(owners, s)
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		return owners[i].EndOffset-owners[i].Offset < owners[j].EndOffset-owners[j].Offset
	})
	return owners
}

func entryKey(e review.EntryPoint, r *review.Report) string {
	if e.Kind == "http" || e.Kind == "wasm" {
		provenance := entryProvenance(e, r)
		if provenance == "" {
			return ""
		}
		return provenance + "\x00" + e.Name
	}
	if e.Kind == "library" || e.Kind == "main" {
		if r != nil {
			for _, s := range r.Symbols {
				if s.ID == e.SymbolID {
					if key := symbolKey(s); key != "" {
						return e.Kind + "\x00" + key
					}
					return ""
				}
			}
		}
	}
	return strings.Join([]string{e.Kind, e.Name, e.World, e.Interface, e.Function}, "\x00")
}

func entries(r *review.Report) []review.EntryPoint {
	if r == nil || r.EntryPoints == nil {
		return nil
	}
	out := append([]review.EntryPoint{}, r.EntryPoints.Entries...)
	for _, id := range r.EntryPoints.Initializers {
		for _, s := range r.Symbols {
			if s.ID == id {
				out = append(out, review.EntryPoint{ID: "startup:" + id, Kind: "initialization", Name: s.Qualified, SymbolID: id, Resolution: "resolved"})
			}
		}
	}
	return out
}

func reachable(r *review.Report, entry, target string) ([]Witness, bool) {
	if entry == "" || target == "" {
		return nil, false
	}
	if entry == target {
		return []Witness{{Kind: "entry-declaration", To: target}}, true
	}
	seen := map[string]bool{entry: true}
	type step struct {
		symbol string
		path   []Witness
	}
	queue := []step{{symbol: entry}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, call := range r.Calls {
			if call.From != current.symbol || call.To == "" || seen[call.To] {
				continue
			}
			location := call.Location
			path := append(append([]Witness{}, current.path...), Witness{Kind: "source-call", From: call.From, To: call.To, CallID: call.ID, Location: &location})
			if call.To == target {
				return path, true
			}
			seen[call.To] = true
			queue = append(queue, step{symbol: call.To, path: path})
		}
		for _, reference := range r.References {
			if reference.From != current.symbol || reference.To == "" || reference.Usage != "value" || seen[reference.To] {
				continue
			}
			location := reference.Location
			path := append(append([]Witness{}, current.path...), Witness{Kind: "source-reference", From: reference.From, To: reference.To, Location: &location})
			if reference.To == target {
				return path, true
			}
			seen[reference.To] = true
			queue = append(queue, step{symbol: reference.To, path: path})
		}
		for _, symbol := range r.Symbols {
			if symbol.Kind != "closure" || seen[symbol.ID] {
				continue
			}
			parent := enclosingSymbol(r, symbol)
			if parent == nil || parent.ID != current.symbol {
				continue
			}
			location := symbol.Location
			path := append(append([]Witness{}, current.path...), Witness{Kind: "closure-declaration", From: current.symbol, To: symbol.ID, Location: &location})
			if symbol.ID == target {
				return path, true
			}
			seen[symbol.ID] = true
			queue = append(queue, step{symbol: symbol.ID, path: path})
		}
	}
	return nil, false
}

// BuildContext groups unique changed material and retains source-call witnesses.
// The complete inventory remains authoritative when semantic analysis is absent.
func (c *Comparison) BuildContext() error {
	c.Records = []Record{}
	c.Flows = []Flow{}
	c.Correspondences = []Correspondence{}
	for i := range c.Files {
		file := &c.Files[i]
		file.BeforeEligibility = contentEligibility(file.Before, file.Content)
		file.AfterEligibility = contentEligibility(file.After, file.Content)
	}
	assigned := map[string]bool{}
	for _, module := range c.Modules {
		var before, after *review.Report
		if module.Before.SnapshotID != "" {
			var err error
			before, err = c.Report(module.ID, "before")
			if err != nil {
				return err
			}
		}
		if module.After.SnapshotID != "" {
			var err error
			after, err = c.Report(module.ID, "after")
			if err != nil {
				return err
			}
		}
		c.moduleEligibility(module, before, after)
		beforeKeys, afterKeys := map[string][]review.Symbol{}, map[string][]review.Symbol{}
		for _, pair := range []struct {
			r    *review.Report
			keys map[string][]review.Symbol
		}{{before, beforeKeys}, {after, afterKeys}} {
			if pair.r == nil {
				continue
			}
			for _, s := range pair.r.Symbols {
				key := symbolKey(s)
				if key != "" {
					pair.keys[key] = append(pair.keys[key], s)
				}
			}
		}
		bmatch, amatch := c.declarationMatches(module, before, after, beforeKeys, afterKeys)
		c.splitModuleItems(module, before, after, bmatch)
		byKey := map[string]*Record{}
		for _, file := range c.Files {
			for _, item := range file.Items {
				if assigned[item.ID] {
					continue
				}
				bs, as := ownersFor(before, module.Path, file.Before.Path, item.Before), ownersFor(after, module.Path, file.After.Path, item.After)
				if len(bs) == 0 && len(as) == 0 {
					continue
				}
				// A smaller enclosing declaration (e.g. closure) owns its partition.
				if len(bs) > 1 && bs[0].Line <= item.Before.Start && bs[0].EndLine >= item.Before.Start+item.Before.Lines-1 {
					bs = bs[:1]
				}
				if len(as) > 1 && as[0].Line <= item.After.Start && as[0].EndLine >= item.After.Start+item.After.Lines-1 {
					as = as[:1]
				}
				if len(bs) > 1 || len(as) > 1 {
					continue
				}
				var b, a *Owner
				key := ""
				if len(bs) == 1 {
					s := bs[0]
					b = &Owner{SymbolID: s.ID, Name: declarationName(s), Kind: s.Kind, Location: s.Location}
					key = "before:" + s.ID
				}
				if len(as) == 1 {
					s := as[0]
					a = &Owner{SymbolID: s.ID, Name: declarationName(s), Kind: s.Kind, Location: s.Location}
					if b == nil {
						key = "after:" + s.ID
					} else {
						if bmatch[bs[0].ID].ID != s.ID {
							continue
						}
					}
				}
				if a != nil && b == nil {
					if s, matched := amatch[as[0].ID]; matched {
						b = &Owner{SymbolID: s.ID, Name: declarationName(s), Kind: s.Kind, Location: s.Location}
						key = "before:" + s.ID
					}
				}
				if b != nil && a == nil {
					if s, matched := bmatch[bs[0].ID]; matched {
						a = &Owner{SymbolID: s.ID, Name: declarationName(s), Kind: s.Kind, Location: s.Location}
					}
				}
				record := byKey[key]
				if record == nil {
					title := b
					if a != nil {
						title = a
					}
					record = &Record{ID: identity(module.ID, key), ModuleID: module.ID, Title: title.Name, Kind: "implementation", Before: b, After: a, ItemIDs: []string{}, FileIDs: []string{}, FlowIDs: []string{}, Reason: "Changed retained declaration source"}
					byKey[key] = record
				}
				record.ItemIDs = append(record.ItemIDs, item.ID)
				record.FileIDs = unique(record.FileIDs, file.ID)
				assigned[item.ID] = true
			}
		}
		records := []*Record{}
		for _, record := range byKey {
			records = append(records, record)
		}
		sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
		bEntries, aEntries := entries(before), entries(after)
		entryPairs := entryMatches(before, after, bEntries, aEntries, bmatch)
		flows := []Flow{}
		matched := map[string]bool{}
		for _, b := range bEntries {
			b := b
			flow := Flow{ID: identity(module.ID, "before", b.ID), ModuleID: module.ID, Name: b.Name, Kind: b.Kind, Before: &b, Status: "before-only", Associations: []Association{}}
			if module.After.Absent {
				flow.Status = "removed"
			} else if module.After.Error != "" || module.After.Partial {
				flow.Status = "after-unavailable"
			} else if after != nil {
				flow.Status = "removed"
			}
			if a, found := entryPairs[b.ID]; found {
				flow.After = &a
				flow.Status = "matched"
				matched[a.ID] = true
				c.Correspondences = append(c.Correspondences, Correspondence{Kind: "entry", ModuleID: module.ID, BeforeID: b.ID, AfterID: a.ID, Reason: "Unique retained declaration or registration/contract provenance"})
			}
			flows = append(flows, flow)
		}
		for _, a := range aEntries {
			if matched[a.ID] {
				continue
			}
			a := a
			status := "after-only"
			if module.Before.Absent {
				status = "added"
			} else if module.Before.Error != "" || module.Before.Partial {
				status = "before-unavailable"
			} else if before != nil {
				status = "added"
			}
			flows = append(flows, Flow{ID: identity(module.ID, "after", a.ID), ModuleID: module.ID, Name: a.Name, Kind: a.Kind, After: &a, Status: status, Associations: []Association{}})
		}
		for _, flow := range flows {
			for _, record := range records {
				for _, side := range []string{"before", "after"} {
					r, e, o := before, flow.Before, record.Before
					if side == "after" {
						r, e, o = after, flow.After, record.After
					}
					if r == nil || e == nil || o == nil {
						continue
					}
					if witness, ok := reachable(r, e.SymbolID, o.SymbolID); ok {
						quality := "potential-impact"
						for _, step := range witness {
							if step.Kind == "source-reference" || step.Kind == "closure-declaration" {
								quality = "potential-reference-impact"
								break
							}
						}
						flow.Associations = append(flow.Associations, Association{RecordID: record.ID, Side: side, Quality: quality, Witness: witness})
						record.FlowIDs = unique(record.FlowIDs, flow.ID)
					}
				}
			}
			c.Flows = append(c.Flows, flow)
		}
		for _, record := range records {
			c.Records = append(c.Records, *record)
		}
	}
	for _, file := range c.Files {
		remaining := []string{}
		for _, item := range file.Items {
			if !assigned[item.ID] {
				remaining = append(remaining, item.ID)
			}
		}
		if len(remaining) == 0 {
			continue
		}
		title := file.After.Path
		if title == "" {
			title = file.Before.Path
		}
		c.Records = append(c.Records, Record{ID: identity(file.ID, "unmapped"), Title: title, Kind: "supporting", ItemIDs: remaining, FileIDs: []string{file.ID}, FlowIDs: []string{},
			Reason: "No unambiguous analyzed declaration mapping; inspect complete retained file evidence (" + file.Content + ", " + file.Status + ")"})
	}
	if err := c.associateEntryEvidence(); err != nil {
		return err
	}
	sort.Slice(c.Records, func(i, j int) bool { return c.Records[i].Title < c.Records[j].Title })
	sort.Slice(c.Flows, func(i, j int) bool { return c.Flows[i].Name < c.Flows[j].Name })
	sort.Slice(c.Correspondences, func(i, j int) bool {
		a, b := c.Correspondences[i], c.Correspondences[j]
		return strings.Join([]string{a.ModuleID, a.Kind, a.BeforeID, a.AfterID}, "\x00") < strings.Join([]string{b.ModuleID, b.Kind, b.BeforeID, b.AfterID}, "\x00")
	})
	return nil
}

func unique(values []string, value string) []string {
	for _, old := range values {
		if old == value {
			return values
		}
	}
	return append(values, value)
}
