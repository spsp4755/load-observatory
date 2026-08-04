package core

import (
	"strings"
	"testing"
)

func stableRun(id string, load int, ttft int64) Run {
	return Run{
		ID:     id,
		Config: RunConfig{Mode: LoadModeVU, VUs: load, MaxErrorPercent: 2, MaxP95Millis: 600000, MaxTTFTP95Millis: 1000},
		Result: RunResult{
			Successes: 100, Total: 100, Issued: 100, Completed: 100, CompletionPercent: 100,
			TTFT: Distribution{P95Millis: ttft}, Latency: Distribution{P95Millis: 5000},
			ThroughputRPS: float64(load) * 1.5, Tokens: TokenUsage{OutputPerSecond: float64(load) * 40},
		},
	}
}

func TestSweepLadderDoublesAndAlwaysEndsAtMax(t *testing.T) {
	if got := SweepLadder(5, 40); len(got) != 4 || got[0] != 5 || got[3] != 40 {
		t.Fatalf("ladder %v, want 5,10,20,40", got)
	}
	// A max that is not a power-of-two multiple still gets measured exactly.
	got := SweepLadder(1, 30)
	if got[len(got)-1] != 30 {
		t.Fatalf("ladder %v does not end at the configured max", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("ladder is not increasing: %v", got)
		}
	}
	if single := SweepLadder(8, 8); len(single) != 1 || single[0] != 8 {
		t.Fatalf("single-rung ladder %v", single)
	}
}

// A binary search finds a boundary; a capacity report needs the curve. Every rung
// must be measured and recorded.
func TestSweepMeasuresEveryRungAndRecordsTheCurve(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 5, MaxLoad: 40, Run: RunConfig{Mode: LoadModeVU}}}
	loads := []int{5, 10, 20, 40}
	for i, load := range loads {
		next, more := AdvanceSearch(&search, stableRun("run-x", load, 200))
		if i < len(loads)-1 {
			if !more || next != loads[i+1] {
				t.Fatalf("after load %d expected next %d, got %d (more=%t)", load, loads[i+1], next, more)
			}
		} else if more {
			t.Fatalf("sweep continued past the configured maximum: next=%d", next)
		}
	}
	if len(search.Steps) != 4 {
		t.Fatalf("expected 4 measured steps, got %d", len(search.Steps))
	}
	for i, step := range search.Steps {
		if step.Load != loads[i] || !step.Stable || step.ThroughputRPS == 0 || step.OutputTokensPerSec == 0 {
			t.Fatalf("step %d not recorded with its curve data: %+v", i, step)
		}
	}
	if search.RecommendedLoad != 40 {
		t.Fatalf("recommended load %d, want 40", search.RecommendedLoad)
	}
	if !strings.Contains(search.Message, "최대 부하를 올려") {
		t.Fatalf("a sweep that never failed should say the real limit may be higher: %q", search.Message)
	}
}

// The knee is defined by the SLOs, and it is the highest load that held
// continuously from the bottom.
func TestKneeIsTheHighestLoadMeetingTheSLO(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 5, MaxLoad: 80, Run: RunConfig{Mode: LoadModeVU}}}
	AdvanceSearch(&search, stableRun("r1", 5, 200))
	AdvanceSearch(&search, stableRun("r2", 10, 300))
	AdvanceSearch(&search, stableRun("r3", 20, 900))
	// 40 breaks the TTFT SLO, then 80 breaks it too, so the sweep stops.
	AdvanceSearch(&search, stableRun("r4", 40, 4000))
	_, more := AdvanceSearch(&search, stableRun("r5", 80, 9000))
	if more {
		t.Fatal("sweep continued after two consecutive failing rungs")
	}
	if search.RecommendedLoad != 20 {
		t.Fatalf("knee %d, want 20 (the last rung inside the SLO)", search.RecommendedLoad)
	}
	if search.ProvisionLoad != 14 {
		t.Fatalf("provision load %d, want 14 (70%% of the knee)", search.ProvisionLoad)
	}
	if !strings.Contains(search.Message, "20") || !strings.Contains(search.Message, "14") {
		t.Fatalf("message does not report both knee and provisioning: %q", search.Message)
	}
}

// A rung that passes after a lower one failed is luck, not headroom.
func TestKneeIgnoresAPassingRungAboveAFailure(t *testing.T) {
	steps := []AutoSearchStep{
		{Load: 5, Stable: true},
		{Load: 10, Stable: false},
		{Load: 20, Stable: true},
	}
	if knee := KneeLoad(steps); knee != 5 {
		t.Fatalf("knee %d, want 5: capacity must hold continuously from the bottom", knee)
	}
}

// Two consecutive failures end the sweep, but one failure does not — the rung
// after the knee shows how sharply the curve turns.
func TestSweepContinuesPastOneFailureButStopsAtTwo(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 1, MaxLoad: 64, Run: RunConfig{Mode: LoadModeVU}}}
	AdvanceSearch(&search, stableRun("r1", 1, 100))
	AdvanceSearch(&search, stableRun("r2", 2, 100))
	_, more := AdvanceSearch(&search, stableRun("r3", 4, 5000))
	if !more {
		t.Fatal("sweep stopped after a single failing rung; one rung past the knee is worth measuring")
	}
	_, more = AdvanceSearch(&search, stableRun("r4", 8, 5000))
	if more {
		t.Fatal("sweep continued after two consecutive failing rungs")
	}
	if search.RecommendedLoad != 2 {
		t.Fatalf("knee %d, want 2", search.RecommendedLoad)
	}
}

func TestSweepFailingAtTheStartingLoadReportsNoCapacity(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 5, MaxLoad: 40, Run: RunConfig{Mode: LoadModeVU}}}
	AdvanceSearch(&search, stableRun("r1", 5, 9000))
	_, more := AdvanceSearch(&search, stableRun("r2", 10, 9000))
	if more {
		t.Fatal("sweep continued although it never met the SLO")
	}
	if search.RecommendedLoad != 0 || search.ProvisionLoad != 0 {
		t.Fatalf("a sweep that never met the SLO must recommend nothing: %+v", search)
	}
	if !strings.Contains(search.Message, "시작 부하부터") {
		t.Fatalf("message does not say the starting load already failed: %q", search.Message)
	}
}

// A rung whose requests mostly did not finish cannot count as capacity.
func TestSweepTreatsAnUnfinishedRungAsFailing(t *testing.T) {
	run := stableRun("r1", 10, 100)
	run.Result.Completed = 3
	run.Result.CompletionPercent = 30
	run.Config.MinCompletionPercent = 95
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 10, MaxLoad: 40, Run: RunConfig{Mode: LoadModeVU}}}
	AdvanceSearch(&search, run)
	if search.Steps[0].Stable {
		t.Fatal("a rung that finished 30% of its requests was recorded as stable")
	}
	if !strings.Contains(search.Steps[0].Reason, "finished") {
		t.Fatalf("step reason does not explain the shortfall: %q", search.Steps[0].Reason)
	}
}
