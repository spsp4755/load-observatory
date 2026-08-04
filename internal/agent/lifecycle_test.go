package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

// A request still generating when the load window closes must be allowed to
// finish during the drain window instead of being cut and lost.
func TestDrainLetsInFlightRequestsFinishAfterTheLoadWindow(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, DrainSeconds: 5})
	if result.Issued == 0 {
		t.Fatal("no request was issued")
	}
	if result.Completed == 0 || result.Successes == 0 {
		t.Fatalf("drain did not let the in-flight request finish: %+v", result)
	}
	if result.Cancelled != 0 {
		t.Fatalf("request cancelled despite an ample drain window: %+v", result)
	}
	if result.CompletionPercent < 100 {
		t.Fatalf("completion rate should be 100%%, got %.1f", result.CompletionPercent)
	}
}

// Without a drain window the deadline still cuts the request, but it must be
// reported as cancelled rather than vanishing from the totals.
func TestUnfinishedRequestsAreCountedAsCancelledNotDropped(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 3, DurationSeconds: 1})
	if result.Issued != 3 {
		t.Fatalf("expected 3 issued requests, got %d (%+v)", result.Issued, result)
	}
	if result.Cancelled != 3 {
		t.Fatalf("expected 3 cancelled requests, got %d (%+v)", result.Cancelled, result)
	}
	if result.Failures != 0 {
		t.Fatalf("cancellation must not count as a failure: %+v", result)
	}
	if result.CompletionPercent != 0 {
		t.Fatalf("expected 0%% completion, got %.1f", result.CompletionPercent)
	}
}

// A slow target that finishes only part of what it was sent must report the
// shortfall, so "30 VU" is never read as "30 VU served".
func TestPartialCompletionIsVisibleInTheResult(t *testing.T) {
	var seen atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1) == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		<-r.Context().Done()
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 4, DurationSeconds: 1})
	if result.Completed == 0 || result.Cancelled == 0 {
		t.Fatalf("expected a mix of completed and cancelled requests: %+v", result)
	}
	if result.Issued != result.Successes+result.HTTPFailures+result.TransportErrors+result.Cancelled {
		t.Fatalf("lifecycle counters do not add up to issued: %+v", result)
	}
	if result.CompletionPercent >= 100 {
		t.Fatalf("partial completion reported as complete: %+v", result)
	}
}

// Percentiles must describe the steady window only, so warmup and ramp-up
// requests cannot flatter the numbers used for the capacity decision.
func TestSteadyStateWindowExcludesEarlyRequestsFromPercentiles(t *testing.T) {
	var seen atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first second answers instantly, later requests take 200ms.
		if seen.Add(1) <= 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 3, SteadyStateSeconds: 1})
	if result.SteadySeconds != 1 {
		t.Fatalf("steady window not reported: %+v", result)
	}
	if result.SteadySamples == 0 || result.SteadySamples >= result.Successes {
		t.Fatalf("steady samples should be a strict subset of successes: samples=%d successes=%d", result.SteadySamples, result.Successes)
	}
	if result.Latency.MinMillis < 150 {
		t.Fatalf("fast pre-steady requests leaked into the distribution: %+v", result.Latency)
	}
}

// Each scenario step needs its own completion rate and latency, so a slow step
// cannot hide inside a blended average.
func TestScenarioResultsAreReportedPerStep(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":2,"completion_tokens":7}}`))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{
		Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 16, Prompt: "base",
		Scenario: []core.ScenarioTask{
			{Name: "short", Prompt: "short question", Weight: 1, MaxTokens: 16},
			{Name: "long", Prompt: "long question", Weight: 1, MaxTokens: 16},
		},
	})
	if len(result.Scenarios) != 2 {
		t.Fatalf("expected one result per scenario, got %+v", result.Scenarios)
	}
	for _, scenario := range result.Scenarios {
		if scenario.Name == "" || scenario.Issued == 0 || scenario.Completed == 0 {
			t.Fatalf("scenario %q missing counters: %+v", scenario.Name, scenario)
		}
		if scenario.CompletionPercent == 0 || scenario.OutputTokens == 0 {
			t.Fatalf("scenario %q missing completion or token throughput: %+v", scenario.Name, scenario)
		}
	}
}

// Agent workflow steps must be attributed to their own step names.
func TestAgentWorkflowStepsAreAttributedByName(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"}, core.RunConfig{
		Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 8, AgentWorkflow: true,
		Scenario: []core.ScenarioTask{
			{Name: "search", Prompt: "tool: search", Weight: 1},
			{Name: "edit", Prompt: "tool: edit", Weight: 1},
		},
	})
	names := map[string]bool{}
	for _, scenario := range result.Scenarios {
		names[scenario.Name] = true
	}
	if !names["search"] || !names["edit"] {
		t.Fatalf("agent steps not attributed by name: %+v", result.Scenarios)
	}
}

// The live snapshot is what the operator watches while the run is in progress.
func TestProgressReportsTargetLoadAgainstRealActivity(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	updates := make(chan core.RunProgress, 16)
	RunTargetWithProgress(context.Background(), core.Target{Type: core.TargetTypeWeb, URL: target.URL},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 4, DurationSeconds: 3}, func(p core.RunProgress) {
			select {
			case updates <- p:
			default:
			}
		})
	close(updates)
	sawActivity := false
	for progress := range updates {
		if progress.TargetLoad != 4 {
			t.Fatalf("target load not reported: %+v", progress)
		}
		if progress.Active > 0 && progress.Completed > 0 && progress.CompletedRPS > 0 {
			sawActivity = true
		}
	}
	if !sawActivity {
		t.Fatal("no live snapshot reported active requests and completed throughput")
	}
}

func TestTimelineSeparatesIssuedCompletedAndCancelled(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 2})
	var issued, completed int64
	for _, point := range result.Timeline {
		issued += point.Issued
		completed += point.Completed
	}
	if issued != result.Issued {
		t.Fatalf("timeline issued %d does not match result issued %d", issued, result.Issued)
	}
	if completed != result.Completed {
		t.Fatalf("timeline completed %d does not match result completed %d", completed, result.Completed)
	}
}

// A cancelled request never finished, so its partial elapsed time must not drag
// the per-second percentile below what the target actually served.
func TestCancelledRequestsDoNotPolluteTheTimelinePercentile(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 1})
	for _, point := range result.Timeline {
		if point.Cancelled > 0 && point.Completed == 0 && point.P95Millis != 0 {
			t.Fatalf("cancelled-only second reported a latency percentile: %+v", point)
		}
	}
}
