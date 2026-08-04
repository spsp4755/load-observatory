package core

import (
	"strings"
	"testing"
)

// The whole point of the completion gate: 30 VU where only 10 requests finished
// measured nothing about capacity, so it must never be reported as stable.
func TestRunWithMostRequestsUnfinishedIsNotStable(t *testing.T) {
	run := Run{
		Config: RunConfig{Mode: LoadModeVU, VUs: 30, MaxErrorPercent: 2, MaxP95Millis: 60000, MinCompletionPercent: 95},
		Result: RunResult{
			Successes: 10, Total: 10, Issued: 30, Completed: 10, Cancelled: 20,
			CompletionPercent: 33.3, Latency: Distribution{P95Millis: 5000},
		},
	}
	if IsRunStable(run) {
		t.Fatal("a run that finished a third of its requests was judged stable")
	}
	message := InstabilityMessage(run)
	if !strings.Contains(message, "33.3%") || !strings.Contains(message, "30 started") {
		t.Fatalf("instability message hides the shortfall: %q", message)
	}
}

func TestFullyCompletedRunWithinSLOStaysStable(t *testing.T) {
	run := Run{
		Config: RunConfig{Mode: LoadModeVU, VUs: 30, MaxErrorPercent: 2, MaxP95Millis: 60000, MinCompletionPercent: 95},
		Result: RunResult{
			Successes: 30, Total: 30, Issued: 30, Completed: 30,
			CompletionPercent: 100, Latency: Distribution{P95Millis: 5000},
		},
	}
	if !IsRunStable(run) {
		t.Fatalf("fully completed run within SLO judged unstable: %s", InstabilityMessage(run))
	}
}

// Results from an Agent that predates the lifecycle counters must not be gated
// on a completion rate it never reported.
func TestResultWithoutLifecycleCountersIsNotGated(t *testing.T) {
	run := Run{
		Config: RunConfig{Mode: LoadModeVU, VUs: 5, MaxErrorPercent: 2, MaxP95Millis: 2000},
		Result: RunResult{Successes: 10, Total: 10, Latency: Distribution{P95Millis: 100}},
	}
	if _, _, short := CompletionShortfall(run); short {
		t.Fatal("gated a result that reports no request lifecycle")
	}
	if !IsRunStable(run) {
		t.Fatal("legacy result judged unstable by the completion gate")
	}
}

// The auto search must not raise the load off an unfinished run.
func TestAutoSearchStopsWhenTheStartingLoadDidNotComplete(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{MaxLoad: 40, Run: RunConfig{Mode: LoadModeVU}}}
	run := Run{
		Config: RunConfig{Mode: LoadModeVU, VUs: 10, MaxErrorPercent: 2, MaxP95Millis: 60000, MinCompletionPercent: 95},
		Result: RunResult{Successes: 2, Total: 2, Issued: 10, Completed: 2, Cancelled: 8, CompletionPercent: 20, Latency: Distribution{P95Millis: 1000}},
	}
	next, more := AdvanceSearch(&search, run)
	if more || next != 0 {
		t.Fatalf("search escalated the load off an unfinished run: next=%d", next)
	}
	if search.RecommendedLoad != 0 {
		t.Fatalf("unfinished run produced a recommended capacity: %d", search.RecommendedLoad)
	}
	if !strings.Contains(search.Message, "finished") {
		t.Fatalf("search message does not explain the shortfall: %q", search.Message)
	}
}

func TestSteadyStateWindowMustBeShorterThanTheRun(t *testing.T) {
	base := RunConfig{Mode: LoadModeVU, VUs: 1, DurationSeconds: 60, MaxTokens: 1024}
	valid := base
	valid.SteadyStateSeconds = 30
	if err := ValidateRunConfig(valid); err != nil {
		t.Fatalf("valid steady window rejected: %v", err)
	}
	invalid := base
	invalid.SteadyStateSeconds = 60
	if err := ValidateRunConfig(invalid); err == nil {
		t.Fatal("steady window equal to the run duration was accepted, leaving no samples")
	}
}
