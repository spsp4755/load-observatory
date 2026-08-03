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

func IsRunStable(run Run) bool {
	total := run.Result.Total
	if total == 0 {
		total = run.Result.Successes + run.Result.Failures
	}
	if total == 0 {
		return false
	}
	errorRate := float64(run.Result.Failures) * 100 / float64(total)
	p95 := run.Result.Latency.P95Millis
	if p95 == 0 {
		p95 = run.Result.P95Millis
	}
	return errorRate <= run.Config.MaxErrorPercent && p95 <= run.Config.MaxP95Millis
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
	if search.StableLoad == 0 {
		search.Status = AutoSearchCompleted
		search.Message = "starting load was not stable"
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
