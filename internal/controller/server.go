package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/monitor"
	"github.com/spsp4755/load-observatory/internal/store"
)

type Server struct {
	store   store.Store
	monitor monitor.Client
}

func NewServer(memory store.Store) *Server { return &Server{store: memory} }
func NewServerWithMonitor(memory store.Store, client monitor.Client) *Server {
	return &Server{store: memory, monitor: client}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/targets":
		s.createTarget(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/targets":
		s.listTargets(w)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/targets/"):
		s.deleteTarget(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runs":
		s.createRun(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/searches":
		s.createSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/searches":
		writeJSON(w, http.StatusOK, s.store.ListSearches())
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/searches/"):
		s.getSearch(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/searches/") && strings.HasSuffix(r.URL.Path, "/cancel"):
		s.cancelSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/runs":
		writeJSON(w, http.StatusOK, s.store.ListRuns())
	case r.Method == http.MethodGet && r.URL.Path == "/api/health":
		queued, running, agentOnline := s.store.Health()
		writeJSON(w, http.StatusOK, map[string]any{"controller_online": true, "agent_online": agentOnline, "queued_runs": queued, "running_runs": running})
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/heartbeat":
		s.store.TouchAgent()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/claim":
		s.claimRun(w)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agent/runs/") && strings.HasSuffix(r.URL.Path, "/result"):
		s.completeShard(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/runs/"):
		s.getRun(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) claimRun(w http.ResponseWriter) {
	assignment, ok := s.store.ClaimRun()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, assignment)
}

func (s *Server) completeShard(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agent/runs/"), "/result"), "/shards/")
	if len(parts) != 2 || parts[1] == "" {
		http.Error(w, "invalid shard result path", http.StatusBadRequest)
		return
	}
	var result core.RunResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	run, ok := s.store.CompleteShard(parts[1], result)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if run.Status == "completed" {
		s.store.AddMonitoring(run.ID, s.monitor.Sample())
		s.store.AdvanceSearch(run.ID)
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) createSearch(w http.ResponseWriter, r *http.Request) {
	var config core.AutoSearchConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if _, ok := s.store.GetTarget(config.Run.TargetID); !ok {
		http.Error(w, "target not found", http.StatusNotFound)
		return
	}
	if config.Run.MaxTokens == 0 {
		config.Run.MaxTokens = 4096
	}
	if config.Run.MaxErrorPercent == 0 {
		config.Run.MaxErrorPercent = 2
	}
	if config.Run.MaxP95Millis == 0 {
		config.Run.MaxP95Millis = 2000
	}
	applyWorkloadDefaults(&config.Run)
	if config.StartLoad == 0 {
		if config.Run.Mode == core.LoadModeRPS {
			config.StartLoad = 10
		} else {
			config.StartLoad = 5
		}
	}
	if config.Run.Mode == core.LoadModeRPS {
		config.Run.RPS = config.StartLoad
	} else {
		config.Run.VUs = config.StartLoad
	}
	if err := core.ValidateAutoSearchConfig(config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, s.store.CreateSearch(config))
}

func (s *Server) getSearch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/searches/")
	search, ok := s.store.GetSearch(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, search)
}

func (s *Server) cancelSearch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/searches/"), "/cancel")
	search, ok := s.store.CancelSearch(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, search)
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
	writeJSON(w, http.StatusCreated, publicTarget(s.store.CreateTarget(target)))
}

func (s *Server) listTargets(w http.ResponseWriter) {
	targets := s.store.ListTargets()
	for i := range targets {
		targets[i] = publicTarget(targets[i])
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	if !s.store.DeleteTarget(strings.TrimPrefix(r.URL.Path, "/api/targets/")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func publicTarget(target core.Target) core.Target { target.APIKey = ""; return target }

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
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	if config.MaxErrorPercent == 0 {
		config.MaxErrorPercent = 2
	}
	if config.MaxP95Millis == 0 {
		config.MaxP95Millis = 2000
	}
	applyWorkloadDefaults(&config)
	if err := core.ValidateRunConfig(config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run := s.store.CreateRun(config)
	s.store.AddMonitoring(run.ID, s.monitor.Sample())
	writeJSON(w, http.StatusCreated, run)
}

func applyWorkloadDefaults(config *core.RunConfig) {
	if config.CachePolicy == "" {
		config.CachePolicy = core.CachePolicyMixed
	}
	if config.CachePolicy == core.CachePolicyMixed && config.VariationPercent == 0 {
		config.VariationPercent = 30
	}
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	run, ok := s.store.GetRun(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
