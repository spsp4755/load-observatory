package core

import "testing"

func searchRun(load int, stable bool) Run {
	failures := int64(0)
	p95 := int64(100)
	if !stable {
		failures = 3
		p95 = 2500
	}
	return Run{Config: RunConfig{Mode: LoadModeVU, VUs: load, MaxErrorPercent: 2, MaxP95Millis: 2000}, Result: RunResult{Total: 100, Failures: failures, Latency: Distribution{P95Millis: p95}}}
}

// The search walks the planned ladder instead of bisecting, because the
// deliverable is the latency-throughput curve, not just its boundary.
func TestAdvanceSearchWalksTheLadder(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 5, MaxLoad: 40}}
	if next, more := AdvanceSearch(&search, searchRun(5, true)); !more || next != 10 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	if next, more := AdvanceSearch(&search, searchRun(10, true)); !more || next != 20 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	// 20 fails, but one rung past the knee is still worth measuring.
	if next, more := AdvanceSearch(&search, searchRun(20, false)); !more || next != 40 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	if _, more := AdvanceSearch(&search, searchRun(40, false)); more || search.RecommendedLoad != 10 {
		t.Fatalf("unexpected final search: recommended=%d steps=%+v", search.RecommendedLoad, search.Steps)
	}
}

// Escalating after the starting load already failed only wastes time on a rung
// guaranteed to be worse, because no knee has been found to measure past.
func TestAdvanceSearchStopsImmediatelyWhenTheStartingLoadFails(t *testing.T) {
	first := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 5, MaxLoad: 40}}
	if _, more := AdvanceSearch(&first, searchRun(5, false)); more || first.RecommendedLoad != 0 {
		t.Fatalf("search escalated off a failing starting load: %+v", first)
	}
	if len(first.Steps) != 1 {
		t.Fatalf("expected exactly one measured rung, got %d", len(first.Steps))
	}
}

func TestAdvanceSearchReportsWhenTheCeilingHeld(t *testing.T) {
	ceiling := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{StartLoad: 10, MaxLoad: 10}}
	if _, more := AdvanceSearch(&ceiling, searchRun(10, true)); more || ceiling.RecommendedLoad != 10 {
		t.Fatalf("unexpected ceiling: %+v", ceiling)
	}
	if ceiling.ProvisionLoad != 7 {
		t.Fatalf("provision load %d, want 7", ceiling.ProvisionLoad)
	}
}

func TestInstabilityMessageUsesTTFTAndOutputRate(t *testing.T) {
	run := Run{Config: RunConfig{MaxErrorPercent: 2, MaxP95Millis: 2000, MaxTTFTP95Millis: 500, MinOutputTokensPerSecond: 10}, Result: RunResult{Total: 1, Successes: 1, TTFT: Distribution{P95Millis: 600}, Tokens: TokenUsage{OutputPerSecond: 5}}}
	if got := InstabilityMessage(run); got != "TTFT P95 600ms > allowed 500ms" {
		t.Fatalf("got %q", got)
	}
	run.Result.TTFT.P95Millis = 100
	if got := InstabilityMessage(run); got != "output rate 5.0 tok/s < required 10.0 tok/s" {
		t.Fatalf("got %q", got)
	}
}
