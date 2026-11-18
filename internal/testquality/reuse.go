package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ReusedAssessment struct {
	AssessmentID string   `json:"assessmentID"`
	Identity     Identity `json:"identity"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
}

// Reuse is deliberately project-wide: without a complete dependency graph,
// unchanged test bytes alone cannot establish unchanged behavior or helpers.
// Ignore analysis timestamps and artifact locations, not source or review inputs.
func reuseFingerprint(a *Assessment) (string, error) {
	modules := map[string]any{}
	for _, report := range a.Reports {
		r := report.Report
		sources := make([]string, 0)
		for _, file := range files(r) {
			sources = append(sources, file.Path+"\x00"+file.SHA256)
		}
		sort.Strings(sources)
		modules[r.Root+"\x00"+r.Module] = map[string]any{
			"source": r.SourceSHA256, "files": sources, "inputs": r.Inputs,
			"scope": r.Scope, "environment": r.Environment,
			"tool": r.ToolVersion, "schema": r.SchemaVersion,
		}
	}
	raw, err := json.Marshal(struct {
		Version     string
		Modules     map[string]any
		Unavailable []string
	}{a.Version, modules, a.Unavailable})
	if err != nil {
		return "", err
	}
	return Digest(raw), nil
}

func (s Store) reuseSaved(ctx context.Context, a *Assessment) error {
	prior, err := s.ListSummaries(a.Root)
	if err != nil {
		return fmt.Errorf("list prior Test Quality results: %w", err)
	}
	sort.Slice(prior, func(i, j int) bool {
		if prior[i].UpdatedAt.Equal(prior[j].UpdatedAt) {
			return prior[i].ID < prior[j].ID
		}
		return prior[i].UpdatedAt.After(prior[j].UpdatedAt)
	})
	fingerprint, err := reuseFingerprint(a)
	if err != nil {
		return err
	}
	for _, summary := range prior {
		if err := ctx.Err(); err != nil {
			return err
		}
		needed := false
		for _, item := range a.Items {
			needed = needed || item.Status == StatusUnreviewed
		}
		if !needed {
			break
		}
		if summary.SavedSummary == nil || summary.SavedSummary.Counts.Scored == 0 {
			continue
		}
		old, err := s.Load(summary.ID)
		if err != nil {
			return fmt.Errorf("verify prior assessment %s for reuse: %w", summary.ID, err)
		}
		oldFingerprint, err := reuseFingerprint(old)
		if err != nil {
			return err
		}
		if fingerprint != oldFingerprint {
			continue
		}
		if err := copySavedResults(a, old); err != nil {
			return fmt.Errorf("reuse assessment %s: %w", old.ID, err)
		}
	}
	return nil
}

func copySavedResults(a, old *Assessment) error {
	snapshots := map[string]string{}
	citations := map[string]Citation{}
	lineCounts := map[string]int{}
	for _, before := range old.Reports {
		for _, after := range a.Reports {
			if before.Report.Root != after.Report.Root || before.Report.Module != after.Report.Module {
				continue
			}
			snapshots[before.SnapshotID] = after.SnapshotID
			targets := map[string]Citation{}
			targetLines := map[string]int{}
			for _, target := range files(after.Report) {
				key := target.Path + "\x00" + target.SHA256
				targets[key] = Citation{SnapshotID: after.SnapshotID, FileID: target.ID}
				targetLines[key] = strings.Count(target.Source, "\n") + 1
			}
			for _, source := range files(before.Report) {
				sourceKey := source.Path + "\x00" + source.SHA256
				if target, ok := targets[sourceKey]; ok {
					key := before.SnapshotID + "\x00" + source.ID
					citations[key] = target
					lineCounts[key] = targetLines[sourceKey]
				}
			}
		}
	}
	remap := func(c Citation) (Citation, error) {
		key := c.SnapshotID + "\x00" + c.FileID
		target, ok := citations[key]
		if !ok || c.StartLine < 1 || c.EndLine < c.StartLine || c.EndLine > lineCounts[key] {
			return Citation{}, fmt.Errorf("saved citation does not match retained source: %+v", c)
		}
		target.StartLine, target.EndLine = c.StartLine, c.EndLine
		return target, nil
	}
	current := map[Identity]int{}
	for i, item := range a.Items {
		current[item.Identity] = i
	}
	for _, saved := range old.Items {
		id := saved.Identity
		id.SnapshotID = snapshots[id.SnapshotID]
		index, ok := current[id]
		if !ok || a.Items[index].Status != StatusUnreviewed || saved.Status != StatusScored ||
			a.Items[index].Name != saved.Name || a.Items[index].Package != saved.Package || a.Items[index].TestKind != saved.TestKind {
			continue
		}
		declaration, err := remap(saved.Declaration)
		if err != nil {
			return err
		}
		if declaration != a.Items[index].Declaration {
			continue
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		var result Responsibility
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		result.Identity, result.Declaration = id, declaration
		rewrite := func(evidence []Citation) error {
			for i, c := range evidence {
				mapped, err := remap(c)
				if err != nil {
					return err
				}
				evidence[i] = mapped
			}
			return nil
		}
		for _, rating := range result.Ratings {
			if err := rewrite(rating.Evidence); err != nil {
				return err
			}
		}
		for _, finding := range result.Findings {
			if err := rewrite(finding.Evidence); err != nil {
				return err
			}
		}
		if result.ReusedFrom == nil {
			result.ReusedFrom = &ReusedAssessment{AssessmentID: old.ID, Identity: saved.Identity, Provider: old.Provider, Model: old.Model}
		}
		a.Items[index] = result
	}
	return nil
}
