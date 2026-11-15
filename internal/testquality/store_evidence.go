package testquality

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
)

type qualityArchive struct {
	Version     string          `json:"version"`
	Metadata    json.RawMessage `json:"metadata"`
	Reports     []Artifact      `json:"reports"`
	Items       []Artifact      `json:"items"`
	Facts       *Artifact       `json:"facts,omitempty"`
	Suite       *Artifact       `json:"suite,omitempty"`
	Diagnostics []Artifact      `json:"diagnostics,omitempty"`
}

func atomicQualityFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "checkpoint-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Content-addressed records make checkpoint publication atomic without
// repeatedly encoding or rewriting immutable reports and deterministic facts.
func (s Store) saveEvidence(a *Assessment) error {
	dir := filepath.Join(s.Directory, a.ID)
	if a.archive == nil {
		a.archive = &qualityArchive{Version: a.Version}
	}
	write := func(value any) (Artifact, []byte, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return Artifact{}, nil, err
		}
		hash := Digest(raw)
		ref := Artifact{Path: hash + ".json", SHA256: hash}
		path := filepath.Join(dir, ref.Path)
		if prior, err := os.ReadFile(path); err == nil {
			if Digest(prior) != hash {
				return ref, nil, fmt.Errorf("immutable assessment record digest mismatch")
			}
		} else if !os.IsNotExist(err) {
			return ref, nil, err
		} else {
			if err := atomicQualityFile(path, raw); err != nil {
				return ref, nil, err
			}
			a.Persistence.Writes++
			a.Persistence.Bytes += int64(len(raw))
		}
		return ref, raw, nil
	}
	if len(a.archive.Reports) == 0 {
		for _, report := range a.Reports {
			ref, _, err := write(report)
			if err != nil {
				return err
			}
			a.archive.Reports = append(a.archive.Reports, ref)
		}
	}
	for i, item := range a.Items {
		if i < len(a.savedItems) && reflect.DeepEqual(item, a.savedItems[i]) {
			continue
		}
		ref, raw, err := write(item)
		if err != nil {
			return err
		}
		var saved Responsibility
		if err := json.Unmarshal(raw, &saved); err != nil {
			return err
		}
		if i == len(a.savedItems) {
			a.savedItems = append(a.savedItems, saved)
			a.archive.Items = append(a.archive.Items, ref)
		} else {
			a.savedItems[i] = saved
			a.archive.Items[i] = ref
		}
	}
	if a.archive.Facts == nil && a.Facts != nil {
		ref, _, err := write(a.Facts)
		if err != nil {
			return err
		}
		a.archive.Facts = &ref
	}
	if a.Suite != nil {
		ref, _, err := write(a.Suite)
		if err != nil {
			return err
		}
		a.archive.Suite = &ref
	}
	for i := len(a.archive.Diagnostics); i < len(a.Diagnostics); i++ {
		ref, _, err := write(a.Diagnostics[i])
		if err != nil {
			return err
		}
		a.archive.Diagnostics = append(a.archive.Diagnostics, ref)
	}
	// Construct metadata explicitly: Assessment owns a sync.Once and must not
	// be copied once its retained-source analysis has initialized.
	metadata := map[string]any{
		"id": a.ID, "version": a.Version, "root": a.Root, "reviewID": a.ReviewID,
		"status": a.Status, "error": a.Error, "provider": a.Provider, "model": a.Model,
		"createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt, "unavailable": a.Unavailable,
		"artifacts": a.Artifacts, "analysis": a.Analysis, "review": a.Review,
		"policy": a.Policy, "diagnosticReason": a.DiagnosticReason, "phase": a.Phase,
		"phaseMillis": a.PhaseMillis, "persistence": a.Persistence,
		"summary": a.SavedSummary,
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	a.archive.Metadata = raw
	data, err := json.Marshal(a.archive)
	if err != nil {
		return err
	}
	if err := atomicQualityFile(filepath.Join(dir, "assessment.json"), data); err != nil {
		return err
	}
	a.Persistence.Writes++
	a.Persistence.Bytes += int64(len(data))
	return nil
}

func (s Store) loadEvidence(id string, data []byte) (*Assessment, error) {
	var archive qualityArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		return nil, err
	}
	var a Assessment
	if err := json.Unmarshal(archive.Metadata, &a); err != nil {
		return nil, err
	}
	read := func(ref Artifact, value any) error {
		if ref.Path != ref.SHA256+".json" || len(ref.SHA256) != 64 || filepath.Base(ref.Path) != ref.Path {
			return fmt.Errorf("invalid assessment record reference")
		}
		path := filepath.Join(s.Directory, id, ref.Path)
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		base, err := filepath.EvalSymlinks(filepath.Join(s.Directory, id))
		if err != nil {
			return err
		}
		if !contained(base, real) {
			return fmt.Errorf("assessment record escapes owner")
		}
		raw, err := os.ReadFile(real)
		if err != nil {
			return err
		}
		if Digest(raw) != ref.SHA256 {
			return fmt.Errorf("assessment record digest mismatch")
		}
		return json.Unmarshal(raw, value)
	}
	for _, ref := range archive.Reports {
		var r PinnedReport
		if err := read(ref, &r); err != nil {
			return nil, err
		}
		a.Reports = append(a.Reports, r)
	}
	for _, ref := range archive.Items {
		var item Responsibility
		if err := read(ref, &item); err != nil {
			return nil, err
		}
		a.Items = append(a.Items, item)
	}
	if archive.Facts != nil {
		if err := read(*archive.Facts, &a.Facts); err != nil {
			return nil, err
		}
	}
	if archive.Suite != nil {
		if err := read(*archive.Suite, &a.Suite); err != nil {
			return nil, err
		}
	}
	for _, ref := range archive.Diagnostics {
		var record DiagnosticRecord
		if err := read(ref, &record); err != nil {
			return nil, err
		}
		a.Diagnostics = append(a.Diagnostics, record)
	}
	return &a, nil
}

// Listing reads only checkpoint metadata. Opening an archive performs full
// immutable-source/artifact verification; status polling does not.
func (s Store) ListSummaries(root string) ([]*Assessment, error) {
	entries, err := os.ReadDir(s.Directory)
	if os.IsNotExist(err) {
		return []*Assessment{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []*Assessment{}
	for _, entry := range entries {
		if !entry.IsDir() || !assessmentID.MatchString(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Directory, entry.Name(), "assessment.json"))
		if err != nil {
			return nil, err
		}
		var header struct{ Version string }
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, err
		}
		if header.Version != EvidenceRubricVersion {
			log.Printf("Skipping obsolete Test Quality assessment %s: unsupported version %q; start a new assessment", entry.Name(), header.Version)
			continue
		}
		var archive qualityArchive
		if err := json.Unmarshal(data, &archive); err != nil {
			return nil, err
		}
		a := &Assessment{}
		if err := json.Unmarshal(archive.Metadata, a); err != nil {
			return nil, err
		}
		if a.ID != entry.Name() || a.Version != header.Version {
			return nil, fmt.Errorf("assessment metadata identity mismatch")
		}
		if root == "" || a.Root == root {
			out = append(out, a)
		}
	}
	return out, nil
}
