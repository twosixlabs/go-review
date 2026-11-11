package workbench

// Caller holds s.mu; the resulting value contains immutable metadata only.
func (s *Server) changeStatusLocked() map[string]any {
	status := map[string]any{"running": s.changeJob != nil, "error": s.changeError}
	if s.changeJob != nil {
		status["jobID"] = s.changeJob.ID
		status["progress"] = s.changeJob.progress
	}
	if s.changePreview != nil {
		status["preview"] = map[string]any{"previewID": s.changePreviewID, "before": s.changePreview.Before, "after": s.changePreview.After, "scope": s.changeScope}
	}
	if s.comparison != nil {
		status["comparisonID"] = s.comparison.ID
		status["revisions"] = s.comparison.Revisions
	}
	return status
}
