package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

type Server struct{ store *store.MemoryStore }

func NewServer(memory *store.MemoryStore) *Server { return &Server{store: memory} }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/targets":
		s.createTarget(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runs":
		s.createRun(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/claim":
		s.claimRun(w)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agent/runs/") && strings.HasSuffix(r.URL.Path, "/result"):
		s.completeRun(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/runs/"):
		s.getRun(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) claimRun(w http.ResponseWriter) {
	assignment, ok := s.store.ClaimRun()
	if !ok { w.WriteHeader(http.StatusNoContent); return }
	writeJSON(w, http.StatusOK, assignment)
}

func (s *Server) completeRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agent/runs/"), "/result")
	var result core.RunResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	run, ok := s.store.CompleteRun(id, result)
	if !ok { http.NotFound(w, r); return }
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var target core.Target
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil || target.Name == "" {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	if target.Type != core.TargetTypeWeb && target.Type != core.TargetTypeModel {
		http.Error(w, "type must be web or model", http.StatusBadRequest)
		return
	}
	if target.Type == core.TargetTypeModel && target.Model == "" {
		http.Error(w, "model name is required", http.StatusBadRequest)
		return
	}
	if err := core.ValidateTarget(target.URL); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.store.CreateTarget(target))
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var config core.RunConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if _, ok := s.store.GetTarget(config.TargetID); !ok {
		http.Error(w, "target not found", http.StatusNotFound)
		return
	}
	if err := core.ValidateRunConfig(config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.store.CreateRun(config))
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	run, ok := s.store.GetRun(id)
	if !ok { http.NotFound(w, r); return }
	writeJSON(w, http.StatusOK, run)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
