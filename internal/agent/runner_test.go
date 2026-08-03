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
		var request struct{ Model string `json:"model"` }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil { t.Fatal(err) }
		if request.Model != model { t.Fatalf("got model %q", request.Model) }
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: model}, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	if result.Successes == 0 { t.Fatal("model request did not succeed") }
}

func TestRunDoesNotCountDeadlineCancellationAsFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	if result.Failures != 0 { t.Fatalf("deadline cancellation counted as failure: %d", result.Failures) }
}
