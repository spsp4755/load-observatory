package controller

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

func exportableRun(t *testing.T) (*store.MemoryStore, string) {
	t.Helper()
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "m", Type: core.TargetTypeModel, URL: "http://10.0.0.10/v1/chat/completions", Model: "qwen"})
	run := memory.CreateRun(core.RunConfig{
		TargetID: target.ID, Mode: core.LoadModeVU, VUs: 8, DurationSeconds: 10, DrainSeconds: 4,
		SteadyStateSeconds: 2, CachePolicy: core.CachePolicyBypass,
		Server: core.ServerConfig{MaxNumSeqs: 256},
	})
	memory.AddMonitoring(run.ID, core.MonitoringSample{Status: "collected", Backend: "vllm", Metrics: map[string]float64{
		core.MetricRequestsWaiting: 0, core.MetricKVCacheUsage: 0.8,
	}})
	memory.SetDetectedServer(run.ID, core.ServerConfig{Version: "0.11.0", Model: "qwen", BlockSize: 16, PrefixCaching: "on"})
	memory.CompleteShard(shardOf(t, memory, run.ID), core.RunResult{
		Successes: 40, Total: 40, Issued: 44, Completed: 40, Cancelled: 4, CompletionPercent: 90.9,
		SteadySamples: 40, ThroughputRPS: 4.0, GoodputPercent: 95,
		Latency: core.Distribution{P50Millis: 400, P95Millis: 900}, TTFT: core.Distribution{P95Millis: 120},
		Timeline:  []core.TimelinePoint{{Second: 0, TargetLoad: 8, Active: 8, Issued: 8, Completed: 6}},
		Scenarios: []core.ScenarioResult{{Name: "short", Issued: 20, Completed: 20, CompletionPercent: 100, InputTokens: 300, OutputTokens: 900}},
	})
	return memory, run.ID
}

// A report attachment has to carry the reasoning and the provenance, not just the
// numbers, or the reader cannot tell whether the numbers mean anything.
func TestJSONExportCarriesVerdictsAndProvenance(t *testing.T) {
	memory, id := exportableRun(t)
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+id+"/export.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, id+".json") {
		t.Fatalf("not offered as a download: %q", disposition)
	}
	var exported struct {
		ID         string `json:"id"`
		Saturation struct {
			State string `json:"state"`
		} `json:"saturation"`
		Validity struct {
			Trustworthy bool `json:"trustworthy"`
		} `json:"validity"`
		Provenance struct {
			Server         core.ServerConfig `json:"server"`
			Gaps           []string          `json:"gaps"`
			TTFTComparable bool              `json:"ttft_comparable"`
		} `json:"provenance"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.ID != id || exported.Saturation.State == "" {
		t.Fatalf("export missing run identity or verdict: %+v", exported)
	}
	// The scraped version and the entered concurrency ceiling both survive.
	if exported.Provenance.Server.Version != "0.11.0" || exported.Provenance.Server.MaxNumSeqs != 256 {
		t.Fatalf("provenance not merged into the export: %+v", exported.Provenance.Server)
	}
	if exported.Provenance.TTFTComparable {
		t.Fatal("max_num_batched_tokens is unknown, so TTFT must not be claimed comparable")
	}
	if len(exported.Provenance.Gaps) == 0 {
		t.Fatal("export does not state what is unknown")
	}
}

func TestCSVExportIsParsableAndCarriesEverySection(t *testing.T) {
	memory, id := exportableRun(t)
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs/"+id+"/export.csv", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	if ctype := response.Header().Get("Content-Type"); !strings.HasPrefix(ctype, "text/csv") {
		t.Fatalf("content type %q", ctype)
	}
	rows, err := csv.NewReader(strings.NewReader(response.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("CSV is not parsable: %v", err)
	}
	if len(rows) < 2 || strings.Join(rows[0], ",") != "section,key,subkey,value" {
		t.Fatalf("unexpected header: %v", rows[0])
	}
	sections := map[string]bool{}
	values := map[string]string{}
	for _, row := range rows[1:] {
		if len(row) != 4 {
			t.Fatalf("ragged row: %v", row)
		}
		sections[row[0]] = true
		values[row[0]+"/"+row[1]+"/"+row[2]] = row[3]
	}
	for _, want := range []string{"run", "provenance", "lifecycle", "throughput", "distribution", "verdict", "scenario", "timeline", "server_metrics"} {
		if !sections[want] {
			t.Fatalf("CSV is missing the %q section", want)
		}
	}
	if values["lifecycle/cancelled/"] != "4" {
		t.Fatalf("cancelled count not exported: %q", values["lifecycle/cancelled/"])
	}
	if values["provenance/ttft_comparable/"] != "false" {
		t.Fatalf("TTFT comparability not exported: %q", values["provenance/ttft_comparable/"])
	}
	if values["scenario/short/input_tokens"] != "300" {
		t.Fatalf("per-scenario input tokens not exported: %q", values["scenario/short/input_tokens"])
	}
	if values["distribution/latency/p95_millis"] != "900" {
		t.Fatalf("latency distribution not exported: %q", values["distribution/latency/p95_millis"])
	}
}

func TestExportOfAnUnknownRunIsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	NewServer(store.NewMemoryStore()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs/run-nope/export.csv", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("got %d", response.Code)
	}
}
