package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
