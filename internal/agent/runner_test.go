package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

func TestRunCountsSuccessfulRequests(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	if result.Successes == 0 {
		t.Fatal("no request succeeded")
	}
}

func TestTraceReplayUsesEventScheduleAndPayload(t *testing.T) {
	started := time.Now()
	type observed struct {
		at        time.Duration
		prompt    string
		maxTokens int
	}
	requests := make(chan observed, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requests <- observed{at: time.Since(started), prompt: payload.Messages[0].Content, maxTokens: payload.MaxTokens}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":4}}`))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{
		Mode: core.LoadModeRPS, RPS: 1, DurationSeconds: 1, DrainSeconds: 1, MaxTokens: 32,
		Trace:          []core.TraceEvent{{TimestampMillis: 0, Name: "first", Prompt: "first prompt", MaxTokens: 7}, {TimestampMillis: 120, Name: "second", Prompt: "second prompt", MaxTokens: 9}},
		TraceTimeScale: 1,
	})
	first, second := <-requests, <-requests
	if first.prompt != "first prompt" || first.maxTokens != 7 || second.prompt != "second prompt" || second.maxTokens != 9 {
		t.Fatalf("trace payloads not replayed: first=%+v second=%+v", first, second)
	}
	if gap := second.at - first.at; gap < 80*time.Millisecond || gap > 350*time.Millisecond {
		t.Fatalf("trace gap = %s, want about 120ms", gap)
	}
	if result.Issued != 2 || result.Completed != 2 {
		t.Fatalf("trace lifecycle = issued %d completed %d", result.Issued, result.Completed)
	}
}

func TestRunTargetSendsSelectedModel(t *testing.T) {
	model := "qwen/qwen3.6-35b-a3b"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != model {
			t.Fatalf("got model %q", request.Model)
		}
		if request.MaxTokens != 4096 || len(request.Messages) != 1 || request.Messages[0].Content != "write a Go API" {
			t.Fatalf("unexpected model payload: %+v", request)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: model}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "write a Go API", MaxTokens: 4096})
	if result.Successes == 0 {
		t.Fatal("model request did not succeed")
	}
}

func TestRunTargetBypassesModelCacheWithWorkloadNonce(t *testing.T) {
	prompts := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		prompts <- request.Messages[0].Content
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 1, CachePolicy: core.CachePolicyBypass})
	if prompt := <-prompts; prompt == "test" {
		t.Fatal("cache-bypass request did not include a workload nonce")
	}
}

func TestRunTargetBypassesWebCacheWithWorkloadQuery(t *testing.T) {
	queries := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeWeb, URL: target.URL}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 1, CachePolicy: core.CachePolicyBypass})
	if query := <-queries; query == "" {
		t.Fatal("cache-bypass web request did not include a workload query")
	}
}

func TestRunTargetSendsTargetAPIKey(t *testing.T) {
	keys := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model", APIKey: "test-key"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 1})
	if key := <-keys; key != "Bearer test-key" {
		t.Fatalf("got authorization %q", key)
	}
}

func TestRunTargetCollectsDetailedMetricsAndUsage(t *testing.T) {
	requests := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests%2 == 0 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":12,"completion_tokens_details":{"reasoning_tokens":3}}}`))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 32})
	if result.Total == 0 || result.Latency.P95Millis < 0 || result.Tokens.Completion == 0 || result.StatusCounts["503"] == 0 || len(result.Timeline) == 0 {
		t.Fatalf("unexpected detailed result: %+v", result)
	}
}

func TestRunTargetMeasuresFirstTokenFromStreamingModelResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !request.Stream {
			t.Fatal("model request must enable streaming to measure first-token latency")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n"))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 32})
	if result.Successes == 0 || result.TTFTP95Millis == 0 || result.Tokens.Completion == 0 {
		t.Fatalf("expected streaming first-token and token metrics, got %+v", result)
	}
}

func TestRunTargetCollectsStreamingCadenceAndGoodput(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n"))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n"))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" more\"}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":8}}\n\ndata: [DONE]\n\n"))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 8})
	if result.TTFO.P95Millis == 0 || result.ITL.P95Millis == 0 || result.TPOT.P95Millis == 0 || result.GoodputPercent == 0 {
		t.Fatalf("expected streaming cadence metrics, got %+v", result)
	}
}

func TestRunTargetDropsRPSArrivalsBeyondInFlightLimit(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeRPS, RPS: 100, DurationSeconds: 1, MaxInFlight: 1})
	if result.DroppedArrivals == 0 || result.Total == 0 {
		t.Fatalf("expected limited in-flight requests and dropped arrivals, got %+v", result)
	}
}

func TestRunTargetCompletesAgentSessionsInSequence(t *testing.T) {
	var prompts []string
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		prompts = append(prompts, request.Messages[0].Content)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer target.Close()
	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "base", MaxTokens: 8, AgentWorkflow: true, Scenario: []core.ScenarioTask{{Name: "search", Prompt: "tool: searched files", Weight: 1}, {Name: "edit", Prompt: "tool: patch applied", Weight: 1}}})
	mu.Lock()
	defer mu.Unlock()
	if result.AgentSessions == 0 || result.CompletedSessions == 0 || len(prompts) < 2 || prompts[0] != "tool: searched files" || prompts[1] != "tool: patch applied" {
		t.Fatalf("agent workflow not sequential: result=%+v prompts=%v", result, prompts)
	}
}

func TestRunTargetRunsOneCapturedJobPerVirtualUser(t *testing.T) {
	var calls int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1200,"completion_tokens":20}}`))
	}))
	defer target.Close()
	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{
		Mode: core.LoadModeVU, VUs: 3, DurationSeconds: 5, MaxTokens: 64,
		AgentWorkflow: true, SessionsPerVU: 1, AccumulateContext: true,
		Scenario: []core.ScenarioTask{{Name: "search", Prompt: "inspect repository", Weight: 1, PromptTokens: 1200}, {Name: "edit", Prompt: "implement change", Weight: 1, PromptTokens: 2400}},
	})
	if calls != 6 || result.AgentSessions != 3 || result.CompletedSessions != 3 {
		t.Fatalf("expected 3 two-turn jobs, calls=%d result=%+v", calls, result)
	}
}

func TestRunTargetMixesSingleRequestsAndAgentSessions(t *testing.T) {
	var prompts []string
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		prompts = append(prompts, request.Messages[0].Content)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer target.Close()
	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 8, Journeys: []core.UserJourney{
		{Name: "chat", Weight: 1, Scenario: []core.ScenarioTask{{Name: "chat", Prompt: "short question", Weight: 1, MaxTokens: 8}}},
		{Name: "agent", Weight: 1, AgentWorkflow: true, Scenario: []core.ScenarioTask{{Name: "search", Prompt: "tool: search", Weight: 1, MaxTokens: 8}, {Name: "edit", Prompt: "tool: edit", Weight: 1, MaxTokens: 8}}},
	}})
	mu.Lock()
	defer mu.Unlock()
	if result.AgentSessions == 0 || len(prompts) < 3 {
		t.Fatalf("mixed journeys did not execute agent and request traffic: result=%+v prompts=%v", result, prompts)
	}
}

func TestRunDoesNotCountDeadlineCancellationAsFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	if result.Failures != 0 {
		t.Fatalf("deadline cancellation counted as failure: %d", result.Failures)
	}
}
