package workbench

import (
	"context"
	"errors"

	"github.com/russellbrasser/go-review/internal/changes"
)

// ShutdownChanges cancels the sole collection owner and waits before releasing
// unused previews. In-flight inputs belong to the job until its done signal.
func (s *Server) ShutdownChanges(ctx context.Context) error {
	s.mu.Lock()
	s.changeClosed = true
	job, preview := s.changeJob, s.changePreview
	s.changePreview = nil
	if job != nil {
		job.cancel()
	}
	s.mu.Unlock()
	var err error
	if job != nil {
		select {
		case <-job.done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if preview != nil {
		err = errors.Join(err, changes.ReleaseInputs(*preview))
	}
	return err
}
