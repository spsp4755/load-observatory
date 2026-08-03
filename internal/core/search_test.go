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

func TestAdvanceSearchDoublesAndBisects(t *testing.T) {
	search := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{MaxLoad: 40}}
	if next, more := AdvanceSearch(&search, searchRun(5, true)); !more || next != 10 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	if next, more := AdvanceSearch(&search, searchRun(10, false)); !more || next != 7 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	if next, more := AdvanceSearch(&search, searchRun(7, true)); !more || next != 8 {
		t.Fatalf("got next %d, more %t", next, more)
	}
	if _, more := AdvanceSearch(&search, searchRun(8, false)); more || search.RecommendedLoad != 7 {
		t.Fatalf("unexpected final search: %+v", search)
	}
}

func TestAdvanceSearchHandlesFirstFailureAndMaximum(t *testing.T) {
	first := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{MaxLoad: 10}}
	if _, more := AdvanceSearch(&first, searchRun(5, false)); more || first.RecommendedLoad != 0 {
		t.Fatalf("unexpected first failure: %+v", first)
	}
	if first.Message != "starting load was not stable: error rate 3.0% > allowed 2.0%" {
		t.Fatalf("unexpected message: %s", first.Message)
	}
	ceiling := AutoSearch{Status: AutoSearchRunning, Config: AutoSearchConfig{MaxLoad: 10}}
	if _, more := AdvanceSearch(&ceiling, searchRun(10, true)); more || ceiling.RecommendedLoad != 10 {
		t.Fatalf("unexpected ceiling: %+v", ceiling)
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
