package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

// The run response has to carry the reasoning, not just the numbers, or the
// operator cannot tell a slow model from a slow client path.
func TestRunResponseCarriesSaturationValidityAndAttribution(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: "http://10.0.0.10/v1/chat/completions", Model: "m"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 8, DurationSeconds: 10, CachePolicy: core.CachePolicyBypass})
	// A saturated server: sustained queue plus preemption, and a client TTFT the
	// server's own phases explain.
	for range 12 {
		memory.AddMonitoring(run.ID, core.MonitoringSample{Status: "collected", Backend: "vllm", Metrics: map[string]float64{
			core.MetricRequestsWaiting: 4, core.MetricRequestsRunning: 8, core.MetricKVCacheUsage: 0.97,
			core.MetricPreemptionRate: 2, core.MetricQueueTimeP95: 600, core.MetricPrefillTimeP95: 350,
			core.MetricPrefixCacheHitRate: 0.03,
		}})
	}
	memory.CompleteShard(shardOf(t, memory, run.ID), core.RunResult{
		Successes: 20, Total: 20, Issued: 20, Completed: 20, SteadySamples: 20,
		TTFT: core.Distribution{P95Millis: 1000},
	})

	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	var view struct {
		Saturation  core.SaturationVerdict  `json:"saturation"`
		Validity    core.RunValidity        `json:"validity"`
		Attribution core.LatencyAttribution `json:"attribution"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Saturation.State != core.SaturationSaturated {
		t.Fatalf("saturation not reported: %+v", view.Saturation)
	}
	if !view.Validity.Trustworthy {
		t.Fatalf("clean run marked untrustworthy: %v", view.Validity.Reasons)
	}
	if view.Attribution.Verdict != core.AttributionServer {
		t.Fatalf("attribution not server_bound: %+v", view.Attribution)
	}
}

// The list response must summarise without shipping every per-second sample.
func TestListRunsKeepsVerdictsButDropsTheSampleSeries(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 5})
	for range 30 {
		memory.AddMonitoring(run.ID, core.MonitoringSample{Status: "collected", Metrics: map[string]float64{core.MetricRequestsWaiting: 0, core.MetricKVCacheUsage: 0.5}})
	}
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))

	var views []struct {
		Monitoring []core.MonitoringSample `json:"monitoring"`
		Saturation core.SaturationVerdict  `json:"saturation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 run, got %d", len(views))
	}
	if len(views[0].Monitoring) != 0 {
		t.Fatalf("list response shipped %d samples", len(views[0].Monitoring))
	}
	if views[0].Saturation.State != core.SaturationHeadroom {
		t.Fatalf("verdict missing from list response: %+v", views[0].Saturation)
	}
}

// Samples must be stamped with their offset so they align with the client-side
// per-second timeline.
func TestMonitoringSamplesAreStampedRelativeToRunStart(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 5})
	// Before the run is claimed there is no start time to offset from.
	memory.AddMonitoring(run.ID, core.MonitoringSample{Status: "collected", Metrics: map[string]float64{core.MetricKVCacheUsage: 0.1}})
	memory.ClaimRun()
	memory.AddMonitoring(run.ID, core.MonitoringSample{Status: "collected", Metrics: map[string]float64{core.MetricKVCacheUsage: 0.2}})

	stored, _ := memory.GetRun(run.ID)
	if stored.StartedUnix == 0 {
		t.Fatal("run start time was not recorded when it was claimed")
	}
	if len(stored.Monitoring) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(stored.Monitoring))
	}
	if stored.Monitoring[1].AtSecond < 0 {
		t.Fatalf("sample offset is negative: %d", stored.Monitoring[1].AtSecond)
	}
}

// Only running runs get sampled; a finished run must not keep accumulating.
func TestActiveRunIDsListsOnlyRunningRuns(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 5, Shards: 1})
	if ids := memory.ActiveRunIDs(); len(ids) != 0 {
		t.Fatalf("queued run reported as active: %v", ids)
	}
	shard := shardOf(t, memory, run.ID)
	if ids := memory.ActiveRunIDs(); len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("running run not listed: %v", ids)
	}
	memory.CompleteShard(shard, core.RunResult{Successes: 1, Total: 1, Issued: 1, Completed: 1})
	if ids := memory.ActiveRunIDs(); len(ids) != 0 {
		t.Fatalf("completed run still reported as active: %v", ids)
	}
}

func shardOf(t *testing.T, memory *store.MemoryStore, runID string) string {
	t.Helper()
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("claim failed: %d %s", response.Code, response.Body.String())
	}
	var assignment core.Assignment
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&assignment); err != nil {
		t.Fatal(err)
	}
	if assignment.Run.ID != runID {
		t.Fatalf("claimed %q, wanted %q", assignment.Run.ID, runID)
	}
	return assignment.Shard.ID
}
