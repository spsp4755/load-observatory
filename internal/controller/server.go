package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spsp4755/load-observatory/internal/auth"
	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/monitor"
	"github.com/spsp4755/load-observatory/internal/store"
)

type Server struct {
	store   store.Store
	monitor monitor.Client
	auth    *auth.Gate
}

func NewServer(memory store.Store) *Server {
	return &Server{store: memory, auth: auth.NewGate(auth.Config{})}
}
func NewServerWithMonitor(memory store.Store, client monitor.Client) *Server {
	server := &Server{store: memory, monitor: client, auth: auth.NewGate(auth.Config{})}
	go server.sampleServerMetrics()
	return server
}

// WithAuth enables Keycloak (or any OIDC provider) login. Without this call,
// the human-facing API stays open - the behavior every existing deployment
// and test relies on.
func (s *Server) WithAuth(gate *auth.Gate) *Server {
	s.auth = gate
	return s
}

// sampleServerMetrics attaches one server-side sample per second to every running
// run. Two samples per run would not let a client-side latency be attributed to a
// server-side cause, which is the whole point of collecting them.
func (s *Server) sampleServerMetrics() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		active := s.store.ActiveRunIDs()
		if len(active) == 0 {
			continue
		}
		// One scrape shared by every running run.
		sample := s.monitor.Sample()
		detected := s.monitor.ServerConfig()
		for _, id := range active {
			s.store.AddMonitoring(id, sample)
			// Static for the run's lifetime, so the store keeps only the first.
			if detected != (core.ServerConfig{}) {
				s.store.SetDetectedServer(id, detected)
			}
		}
	}
}

// ServeHTTP splits requests into three trust levels: the OIDC dance and the
// health check are public by nature, the agent endpoints are machine-to-
// machine (an agent has no browser to run a login redirect in, so it is
// trusted at the network level like it always was), and everything else - the
// browser-facing API a human uses to register targets and launch runs - sits
// behind a session.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/auth/login":
		s.auth.Login(w, r)
	case r.URL.Path == "/auth/callback":
		s.auth.Callback(w, r)
	case r.URL.Path == "/auth/logout":
		s.auth.Logout(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/health":
		queued, running, agentOnline := s.store.Health()
		writeJSON(w, http.StatusOK, map[string]any{"controller_online": true, "agent_online": agentOnline, "queued_runs": queued, "running_runs": running, "login_required": s.auth.Enabled()})
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/heartbeat":
		s.store.TouchAgent()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/claim":
		s.claimRun(w)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agent/runs/") && strings.HasSuffix(r.URL.Path, "/result"):
		s.completeShard(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agent/runs/") && strings.HasSuffix(r.URL.Path, "/progress"):
		s.reportProgress(w, r)
	default:
		s.auth.Require(http.HandlerFunc(s.serveHumanAPI)).ServeHTTP(w, r)
	}
}

func (s *Server) serveHumanAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/session":
		s.auth.Session(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/targets":
		s.createTarget(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/targets":
		s.listTargets(w)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/targets/") && strings.HasSuffix(r.URL.Path, "/check"):
		s.checkTarget(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/targets/"):
		s.deleteTarget(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runs":
		s.createRun(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/runs/") && strings.HasSuffix(r.URL.Path, "/cancel"):
		s.cancelRun(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/searches":
		s.createSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/searches":
		writeJSON(w, http.StatusOK, s.store.ListSearches())
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/searches/"):
		s.getSearch(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/searches/") && strings.HasSuffix(r.URL.Path, "/cancel"):
		s.cancelSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/runs":
		writeJSON(w, http.StatusOK, listView(s.store.ListRuns()))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/runs/") && (strings.HasSuffix(r.URL.Path, "/export.json") || strings.HasSuffix(r.URL.Path, "/export.csv")):
		s.exportRun(w, r)
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
		// Re-read so the response carries the whole sample series the verdicts
		// are derived from, including the one just added.
		if fresh, ok := s.store.GetRun(run.ID); ok {
			run = fresh
		}
	}
	writeJSON(w, http.StatusOK, view(run))
}

func (s *Server) reportProgress(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/agent/runs/"), "/progress"), "/shards/")
	if len(parts) != 2 || parts[1] == "" {
		http.Error(w, "invalid shard progress path", http.StatusBadRequest)
		return
	}
	var progress core.RunProgress
	if err := json.NewDecoder(r.Body).Decode(&progress); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !s.store.SetShardProgress(parts[1], progress) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if config.Run.Shards == 0 {
		config.Run.Shards = 3
	}
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

func (s *Server) checkTarget(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/targets/"), "/check")
	target, ok := s.store.GetTarget(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	method, body := http.MethodGet, bytes.NewReader(nil)
	if target.Type == core.TargetTypeModel {
		payload, _ := json.Marshal(map[string]any{"model": target.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 1, "stream": false})
		method, body = http.MethodPost, bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.URL, body)
	if err != nil {
		http.Error(w, "invalid target request", http.StatusBadRequest)
		return
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if target.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		http.Error(w, "target connection failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
		http.Error(w, "target returned "+response.Status, http.StatusBadGateway)
		return
	}
	result := map[string]any{"ok": true, "status_code": response.StatusCode, "latency_millis": time.Since(started).Milliseconds()}
	if target.Type == core.TargetTypeModel {
		result["supports_ignore_eos"] = probeIgnoreEOS(ctx, target)
	}
	writeJSON(w, http.StatusOK, result)
}

// probeIgnoreEOS reports whether the target accepts the vLLM/SGLang ignore_eos
// extension. Pinning output length makes TPOT and ITL comparable between runs,
// but a server that rejects unknown fields would fail every request, so the
// operator needs to know before enabling it.
func probeIgnoreEOS(ctx context.Context, target core.Target) bool {
	payload, err := json.Marshal(map[string]any{"model": target.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "max_tokens": 1, "stream": false, "ignore_eos": true, "min_tokens": 1})
	if err != nil {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	if target.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	switch s.store.DeleteTarget(strings.TrimPrefix(r.URL.Path, "/api/targets/")) {
	case store.TargetDeleted:
		w.WriteHeader(http.StatusNoContent)
	case store.TargetInUse:
		http.Error(w, "target is used by an active run", http.StatusConflict)
	default:
		http.NotFound(w, r)
	}
}

func publicTarget(target core.Target) core.Target {
	target.HasAPIKey = target.APIKey != ""
	target.APIKey = ""
	return target
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
	if config.Shards == 0 {
		config.Shards = 3
	}
	if err := core.ValidateRunConfig(config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run := s.store.CreateRun(config)
	if user, ok := auth.UserFromContext(r.Context()); ok {
		s.store.SetCreatedBy(run.ID, user.Name)
		run.CreatedBy = user.Name
	}
	s.store.AddMonitoring(run.ID, s.monitor.Sample())
	writeJSON(w, http.StatusCreated, view(run))
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/cancel")
	run, ok := s.store.CancelRun(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, view(run))
}

func applyWorkloadDefaults(config *core.RunConfig) {
	if config.CachePolicy == "" {
		config.CachePolicy = core.CachePolicyMixed
	}
	if config.CachePolicy == core.CachePolicyMixed && config.VariationPercent == 0 {
		config.VariationPercent = 30
	}
	// A long generation must be allowed to finish rather than be cut at the
	// deadline, and the ramp-up must not pollute the evaluated percentiles.
	// Both scale with the run so short smoke tests stay short.
	if config.DrainSeconds == 0 {
		config.DrainSeconds = min(120, config.DurationSeconds/5)
	}
	if config.SteadyStateSeconds == 0 {
		config.SteadyStateSeconds = min(60, config.DurationSeconds/5)
	}
	if config.MinCompletionPercent == 0 {
		config.MinCompletionPercent = 95
	}
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	run, ok := s.store.GetRun(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, view(run))
}

// runView is a Run plus the verdicts derived from its server-side samples. They
// are computed on read rather than stored so an existing run gains them as the
// reasoning improves, and so they cannot drift from the samples they summarise.
type runView struct {
	core.Run
	Saturation  core.SaturationVerdict  `json:"saturation"`
	Validity    core.RunValidity        `json:"validity"`
	Attribution core.LatencyAttribution `json:"attribution"`
	Provenance  provenanceView          `json:"provenance"`
}

// provenanceView is the server configuration this run's numbers depend on, plus
// what is still unknown about it. A capacity number without its provenance is an
// anecdote: it cannot be compared with the next run's.
type provenanceView struct {
	Server core.ServerConfig `json:"server"`
	// Gaps names the settings still unknown. Conflicts names the ones where the
	// operator's entry disagrees with what the server reports.
	Gaps      []string `json:"gaps,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	// TTFTComparable is false while any setting that changes the meaning of a TTFT
	// number is unknown, chiefly max_num_batched_tokens under chunked prefill.
	TTFTComparable bool `json:"ttft_comparable"`
}

func view(run core.Run) runView {
	effective := core.EffectiveServerConfig(run.Config.Server, run.DetectedServer)
	gaps, ttftComparable := core.ProvenanceGaps(effective)
	// The saturation verdict must reason from the effective configuration, not
	// from a stale value the operator typed.
	config := run.Config
	config.Server = effective
	return runView{
		Run:         run,
		Saturation:  core.AssessSaturation(run.Monitoring, config),
		Validity:    core.AssessRunValidity(run.Monitoring, run.Result, config),
		Attribution: core.AttributeTTFT(run.Result, run.Monitoring),
		Provenance: provenanceView{
			Server:         effective,
			Gaps:           gaps,
			Conflicts:      core.ProvenanceConflicts(run.Config.Server, run.DetectedServer),
			TTFTComparable: ttftComparable,
		},
	}
}

// listView keeps the verdicts but drops the per-second sample series, which would
// otherwise dominate the size of a list response.
func listView(runs []core.Run) []runView {
	views := make([]runView, 0, len(runs))
	for _, run := range runs {
		item := view(run)
		item.Monitoring = nil
		views = append(views, item)
	}
	return views
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
