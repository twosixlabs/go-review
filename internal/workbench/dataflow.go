package workbench

import (
	"encoding/json"
	"net/http"

	"github.com/russellbrasser/go-review/internal/review"
)

func (s *Server) dataflow(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SymbolID     string `json:"symbolID"`
		SourceSHA256 string `json:"sourceSHA256"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.SymbolID == "" || request.SourceSHA256 == "" {
		failure(w, 400, "symbolID and sourceSHA256 are required")
		return
	}
	snapshot := s.current()
	if snapshot == nil || snapshot.SourceSHA256 != request.SourceSHA256 {
		failure(w, 409, "snapshot changed; reload the current snapshot before reading data flow")
		return
	}
	graph, err := review.AnalyzeDataFlow(r.Context(), snapshot, request.SymbolID)
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(graph)
}
