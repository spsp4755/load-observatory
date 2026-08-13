package core

import (
	"fmt"
	"sort"
)

func ValidateAutoSearchConfig(config AutoSearchConfig) error {
	if err := ValidateRunConfig(config.Run); err != nil {
		return err
	}
	if len(config.Run.Trace) > 0 {
		return fmt.Errorf("trace replay cannot be used for automatic capacity search")
	}
	load := config.StartLoad
	if load < 1 || config.MaxLoad < load {
		return fmt.Errorf("start_load must be between 1 and max_load")
	}
	if config.Run.Mode == LoadModeVU && config.MaxLoad > MaxVUs {
		return fmt.Errorf("max_load must be at most %d", MaxVUs)
	}
	if config.Run.Mode == LoadModeRPS && config.MaxLoad > MaxRPS {
		return fmt.Errorf("max_load must be at most %d", MaxRPS)
	}
	return nil
}

// CompletionShortfall reports the completion-rate gate when a run finished too
// few of the requests it started. A run where most requests were still in flight
// at the deadline measured nothing about capacity, so it is never "stable".
func CompletionShortfall(run Run) (float64, float64, bool) {
	limit := run.Config.MinCompletionPercent
	if limit == 0 {
		limit = 95
	}
	// Results from an Agent that does not report the lifecycle cannot be gated.
	if run.Result.Issued == 0 {
		return 0, limit, false
	}
	return run.Result.CompletionPercent, limit, run.Result.CompletionPercent < limit
}

func IsRunStable(run Run) bool {
	total := run.Result.Total
	if total == 0 {
		total = run.Result.Successes + run.Result.Failures
	}
	if total == 0 {
		return false
	}
	if _, _, short := CompletionShortfall(run); short {
		return false
	}
	errorRate := float64(run.Result.Failures) * 100 / float64(total)
	p95 := run.Result.Latency.P95Millis
	if p95 == 0 {
		p95 = run.Result.P95Millis
	}
	if errorRate > run.Config.MaxErrorPercent || p95 > run.Config.MaxP95Millis {
		return false
	}
	if run.Config.MaxTTFTP95Millis > 0 && run.Result.TTFT.P95Millis > run.Config.MaxTTFTP95Millis {
		return false
	}
	return run.Config.MinOutputTokensPerSecond == 0 || run.Result.Tokens.OutputPerSecond >= run.Config.MinOutputTokensPerSecond
}

func InstabilityMessage(run Run) string {
	total := run.Result.Total
	if total == 0 {
		total = run.Result.Successes + run.Result.Failures
	}
	if total == 0 {
		return "no completed requests"
	}
	if actual, limit, short := CompletionShortfall(run); short {
		return fmt.Sprintf("only %.1f%% of %d started requests finished, below the required %.1f%%", actual, run.Result.Issued, limit)
	}
	errorRate := float64(run.Result.Failures) * 100 / float64(total)
	if errorRate > run.Config.MaxErrorPercent {
		return fmt.Sprintf("error rate %.1f%% > allowed %.1f%%", errorRate, run.Config.MaxErrorPercent)
	}
	p95 := run.Result.Latency.P95Millis
	if p95 == 0 {
		p95 = run.Result.P95Millis
	}
	if p95 > run.Config.MaxP95Millis {
		return fmt.Sprintf("P95 %dms > allowed %dms", p95, run.Config.MaxP95Millis)
	}
	if run.Config.MaxTTFTP95Millis > 0 && run.Result.TTFT.P95Millis > run.Config.MaxTTFTP95Millis {
		return fmt.Sprintf("TTFT P95 %dms > allowed %dms", run.Result.TTFT.P95Millis, run.Config.MaxTTFTP95Millis)
	}
	if run.Config.MinOutputTokensPerSecond > 0 && run.Result.Tokens.OutputPerSecond < run.Config.MinOutputTokensPerSecond {
		return fmt.Sprintf("output rate %.1f tok/s < required %.1f tok/s", run.Result.Tokens.OutputPerSecond, run.Config.MinOutputTokensPerSecond)
	}
	return "result did not meet stability criteria"
}

// SweepLadder plans the concurrency rungs to measure, doubling from start up to
// max and always ending exactly at max. Continuous batching means throughput
// keeps rising while per-request latency barely moves until the knee, so the
// curve has to be sampled across a wide range rather than bisected: a binary
// search finds a boundary but never the shape.
func SweepLadder(start, max int) []int {
	if start < 1 {
		start = 1
	}
	if max < start {
		max = start
	}
	var ladder []int
	for load := start; load < max; load *= 2 {
		ladder = append(ladder, load)
		if len(ladder) >= 12 {
			break
		}
	}
	if len(ladder) == 0 || ladder[len(ladder)-1] != max {
		ladder = append(ladder, max)
	}
	return ladder
}

// unstableRungsBeforeStopping is how many consecutive failing rungs to measure
// before abandoning the sweep. One rung past the knee shows how sharply the curve
// turns; more than that is wasted time on a server already past its limit.
const unstableRungsBeforeStopping = 2

// AdvanceSearch records the finished rung and returns the next load to run. It
// walks the whole ladder rather than bisecting, so the result is a curve with a
// knee rather than a single boundary value.
func AdvanceSearch(search *AutoSearch, run Run) (int, bool) {
	load := run.Config.VUs
	if run.Config.Mode == LoadModeRPS {
		load = run.Config.RPS
	}
	if len(search.Ladder) == 0 {
		search.Ladder = SweepLadder(search.Config.StartLoad, search.Config.MaxLoad)
	}
	search.Steps = append(search.Steps, stepFrom(load, run))
	if IsRunStable(run) {
		search.StableLoad = max(search.StableLoad, load)
	} else if search.FailedLoad == 0 || load < search.FailedLoad {
		search.FailedLoad = load
	}

	// Measuring one rung past the knee shows how sharply the curve turns, but only
	// once a knee exists. If nothing has met the SLO yet there is nothing to
	// measure past, and a higher load is guaranteed to be worse.
	if search.StableLoad == 0 && !IsRunStable(run) {
		finishSearch(search)
		return 0, false
	}
	// Stop once the server has failed consecutively: the curve past that point
	// says nothing useful about capacity.
	if consecutiveUnstableTail(search.Steps) >= unstableRungsBeforeStopping {
		finishSearch(search)
		return 0, false
	}
	for _, rung := range search.Ladder {
		if rung > load {
			search.NextLoad = rung
			return rung, true
		}
	}
	finishSearch(search)
	return 0, false
}

func stepFrom(load int, run Run) AutoSearchStep {
	stable := IsRunStable(run)
	step := AutoSearchStep{
		Load: load, RunID: run.ID, Stable: stable,
		ThroughputRPS:      run.Result.ThroughputRPS,
		OutputTokensPerSec: run.Result.Tokens.OutputPerSecond,
		TTFTP95Millis:      run.Result.TTFT.P95Millis,
		TPOTP95Millis:      run.Result.TPOT.P95Millis,
		LatencyP95Millis:   run.Result.Latency.P95Millis,
		GoodputPercent:     run.Result.GoodputPercent,
		CompletionPercent:  run.Result.CompletionPercent,
	}
	if step.LatencyP95Millis == 0 {
		step.LatencyP95Millis = run.Result.P95Millis
	}
	if !stable {
		step.Reason = InstabilityMessage(run)
	}
	return step
}

func consecutiveUnstableTail(steps []AutoSearchStep) int {
	count := 0
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Stable {
			break
		}
		count++
	}
	return count
}

// finishSearch picks the capacity to report. The knee is the highest load that
// still met every SLO, defined by the SLOs rather than by eyeballing the curve.
func finishSearch(search *AutoSearch) {
	search.Status = AutoSearchCompleted
	search.NextLoad = 0
	knee := KneeLoad(search.Steps)
	search.RecommendedLoad = knee
	if knee == 0 {
		search.Message = "시작 부하부터 SLO를 충족하지 못했습니다. 부하를 더 낮추거나 SLO를 재검토하세요."
		if len(search.Steps) > 0 && search.Steps[0].Reason != "" {
			search.Message += " (" + search.Steps[0].Reason + ")"
		}
		return
	}
	search.ProvisionLoad = knee * provisionHeadroomPercent / 100
	if search.ProvisionLoad < 1 {
		search.ProvisionLoad = 1
	}
	if knee >= search.Config.MaxLoad {
		search.Message = fmt.Sprintf("설정한 최대 부하 %d까지 SLO를 충족했습니다. 실제 한계는 더 높을 수 있으니 최대 부하를 올려 다시 측정하세요.", knee)
		return
	}
	search.Message = fmt.Sprintf("SLO를 충족하는 최대 부하는 %d입니다. 운영 제공은 여유를 두고 %d 수준을 권장합니다.", knee, search.ProvisionLoad)
}

// KneeLoad returns the highest load that met every SLO. A higher rung that passes
// after a lower one failed is not counted: capacity has to hold continuously from
// the bottom, or the passing rung was luck rather than headroom.
func KneeLoad(steps []AutoSearchStep) int {
	ordered := append([]AutoSearchStep(nil), steps...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Load < ordered[j].Load })
	knee := 0
	for _, step := range ordered {
		if !step.Stable {
			break
		}
		knee = step.Load
	}
	return knee
}
