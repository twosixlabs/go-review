package workbench

import (
	"encoding/json"
	"net/http"

	"github.com/russellbrasser/go-review/internal/review"
)

func (s *Server) reviewScope(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if r.Method != http.MethodGet {
		var request struct {
			Directory string             `json:"directory"`
			Scope     review.ReviewScope `json:"scope"`
		}
		if !decodeRequest(w, r, &request) {
			return
		}
		if !s.collect.TryLock() {
			failure(w, 409, "cannot change scope while collection is running")
			return
		}
		defer s.collect.Unlock()
		if err := review.SaveReviewScope(request.Directory, request.Scope); err != nil {
			failure(w, 422, err.Error())
			return
		}
		dir = request.Directory
	}
	scope, err := review.LoadReviewScope(dir)
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(scope)
}
