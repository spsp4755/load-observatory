package core

import "fmt"

func ValidateAutoSearchConfig(config AutoSearchConfig) error {
	if err := ValidateRunConfig(config.Run); err != nil {
		return err
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

func AdvanceSearch(search *AutoSearch, run Run) (int, bool) {
	load := run.Config.VUs
	if run.Config.Mode == LoadModeRPS {
		load = run.Config.RPS
	}
	if IsRunStable(run) {
		search.StableLoad = load
		if search.FailedLoad > load {
			if search.FailedLoad-load <= 1 {
				search.Status = AutoSearchCompleted
				search.RecommendedLoad = load
				search.Message = "maximum stable load found"
				return 0, false
			}
			next := (load + search.FailedLoad) / 2
			search.NextLoad = next
			return next, true
		}
		if load >= search.Config.MaxLoad {
			search.Status = AutoSearchCompleted
			search.RecommendedLoad = load
			search.Message = "configured maximum remained stable"
			return 0, false
		}
		next := load * 2
		if next > search.Config.MaxLoad {
			next = search.Config.MaxLoad
		}
		search.NextLoad = next
		return next, true
	}
	search.FailedLoad = load
	reason := InstabilityMessage(run)
	if search.StableLoad == 0 {
		search.Status = AutoSearchCompleted
		search.Message = "starting load was not stable: " + reason
		return 0, false
	}
	if search.FailedLoad-search.StableLoad <= 1 {
		search.Status = AutoSearchCompleted
		search.RecommendedLoad = search.StableLoad
		search.Message = "maximum stable load found"
		return 0, false
	}
	next := (search.StableLoad + search.FailedLoad) / 2
	search.NextLoad = next
	return next, true
}
