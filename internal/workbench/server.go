// Package workbench exposes local analysis through an interactive browser UI.
package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

type Server struct {
	mu            sync.RWMutex
	collect       sync.Mutex
	report        *review.Report
	review        *review.RepositoryReview
	progress      review.ReviewProgress
	reviewRunning bool
	reviewError   string
	agent         intelligence.Config
}

func New(report *review.Report) *Server {
	review.PrepareSystemFlow(report)
	return &Server{report: report}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", webui.Handler())
	mux.Handle("/mcp", s.mcpHandler())
	mux.HandleFunc("/api/snapshot", s.snapshot)
	mux.HandleFunc("/api/analyze", s.analyze)
	mux.HandleFunc("/api/coverage", s.coverage)
	mux.HandleFunc("/api/unit-tests", s.unitTests)
	mux.HandleFunc("/api/dataflow", s.dataflow)
	mux.HandleFunc("/api/review", s.repositoryReview)
	mux.HandleFunc("/api/review/select", s.selectReviewModule)
	mux.HandleFunc("/api/review/scope", s.reviewScope)
	mux.HandleFunc("/api/chat/status", s.chatStatus)
	mux.HandleFunc("/api/chat", s.chat)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			failure(w, http.StatusForbidden, "loopback Host required")
			return
		}
		// Reject cross-site requests to a privileged local source-reading API.
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || u.Scheme != "http" {
				failure(w, http.StatusForbidden, "cross-origin requests are not allowed")
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			failure(w, http.StatusForbidden, "cross-site requests are not allowed")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func failure(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		failure(w, 405, "GET required")
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.report == nil {
		failure(w, 404, "no module analyzed yet")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.report)
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		failure(w, 405, "POST required")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		failure(w, 415, "application/json required")
		return
	}
	var request struct {
		Directory              string `json:"directory"`
		DocumentationDirectory string `json:"documentationDirectory"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		failure(w, 400, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if request.Directory == "" {
		failure(w, 400, "directory is required")
		return
	}
	if !s.collect.TryLock() {
		failure(w, 409, "analysis already running")
		return
	}
	defer s.collect.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	report, err := review.Analyze(ctx, review.Options{Dir: request.Directory, DocumentationDir: request.DocumentationDirectory})
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	run, err := review.SaveAnalysis(report)
	if err != nil {
		failure(w, 500, "replace review evidence: "+err.Error())
		return
	}
	s.mu.Lock()
	s.report = report
	s.review = run
	s.reviewError = ""
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}
