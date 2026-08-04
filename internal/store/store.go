package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

type Store interface {
	CreateTarget(core.Target) core.Target
	GetTarget(string) (core.Target, bool)
	ListTargets() []core.Target
	DeleteTarget(string) DeleteTargetResult
	CreateRun(core.RunConfig) core.Run
	GetRun(string) (core.Run, bool)
	CancelRun(string) (core.Run, bool)
	ListRuns() []core.Run
	ClaimRun() (core.Assignment, bool)
	CompleteRun(string, core.RunResult) (core.Run, bool)
	CompleteShard(string, core.RunResult) (core.Run, bool)
	SetShardProgress(string, core.RunProgress) bool
	AddMonitoring(string, core.MonitoringSample)
	ActiveRunIDs() []string
	TouchAgent()
	Health() (int, int, bool)
	CreateSearch(core.AutoSearchConfig) core.AutoSearch
	GetSearch(string) (core.AutoSearch, bool)
	ListSearches() []core.AutoSearch
	CancelSearch(string) (core.AutoSearch, bool)
	AdvanceSearch(string)
}

type DeleteTargetResult int

const (
	TargetDeleted DeleteTargetResult = iota
	TargetNotFound
	TargetInUse
)

type MemoryStore struct {
	mu           sync.RWMutex
	nextID       int
	targets      map[string]core.Target
	runs         map[string]core.Run
	searches     map[string]core.AutoSearch
	searchRun    map[string]string
	shards       map[string]core.Shard
	shardResults map[string]core.RunResult
	// progress is live in-run telemetry keyed by shard ID. It is deliberately not
	// part of the snapshot: it is worthless once the run ends.
	progress  map[string]core.RunProgress
	agentSeen time.Time
}

func (s *MemoryStore) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(struct {
		NextID       int                        `json:"next_id"`
		Targets      map[string]core.Target     `json:"targets"`
		Runs         map[string]core.Run        `json:"runs"`
		Searches     map[string]core.AutoSearch `json:"searches"`
		SearchRun    map[string]string          `json:"search_run"`
		Shards       map[string]core.Shard      `json:"shards"`
		ShardResults map[string]core.RunResult  `json:"shard_results"`
	}{s.nextID, s.targets, s.runs, s.searches, s.searchRun, s.shards, s.shardResults})
}

func (s *MemoryStore) Restore(data []byte) error {
	var state struct {
		NextID       int                        `json:"next_id"`
		Targets      map[string]core.Target     `json:"targets"`
		Runs         map[string]core.Run        `json:"runs"`
		Searches     map[string]core.AutoSearch `json:"searches"`
		SearchRun    map[string]string          `json:"search_run"`
		Shards       map[string]core.Shard      `json:"shards"`
		ShardResults map[string]core.RunResult  `json:"shard_results"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = state.NextID
	s.targets = state.Targets
	s.runs = state.Runs
	s.searches = state.Searches
	s.searchRun = state.SearchRun
	s.shards = state.Shards
	s.shardResults = state.ShardResults
	if s.targets == nil {
		s.targets = map[string]core.Target{}
	}
	if s.runs == nil {
		s.runs = map[string]core.Run{}
	}
	if s.searches == nil {
		s.searches = map[string]core.AutoSearch{}
	}
	if s.searchRun == nil {
		s.searchRun = map[string]string{}
	}
	if s.shards == nil {
		s.shards = map[string]core.Shard{}
	}
	if s.shardResults == nil {
		s.shardResults = map[string]core.RunResult{}
	}
	if s.progress == nil {
		s.progress = map[string]core.RunProgress{}
	}
	return nil
}

func (s *MemoryStore) SetShardProgress(shardID string, progress core.RunProgress) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.shards[shardID]; !ok {
		return false
	}
	progress.ShardID = shardID
	s.progress[shardID] = progress
	return true
}

func (s *MemoryStore) clearRunProgressLocked(runID string) {
	for shardID, shard := range s.shards {
		if shard.RunID == runID {
			delete(s.progress, shardID)
		}
	}
}

func (s *MemoryStore) runProgressLocked(runID string) []core.RunProgress {
	var live []core.RunProgress
	for shardID, shard := range s.shards {
		if shard.RunID != runID {
			continue
		}
		if progress, ok := s.progress[shardID]; ok {
			live = append(live, progress)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].ShardID < live[j].ShardID })
	return live
}

func (s *MemoryStore) ListRuns() []core.Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]core.Run, 0, len(s.runs))
	for _, run := range s.runs {
		run.Progress = s.runProgressLocked(run.ID)
		runs = append(runs, run)
	}
	return runs
}

func (s *MemoryStore) TouchAgent() { s.mu.Lock(); defer s.mu.Unlock(); s.agentSeen = time.Now() }

func (s *MemoryStore) Health() (int, int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	queued, running := 0, 0
	for _, run := range s.runs {
		if run.Status == "queued" || run.Status == "running" {
			queued++
		}
		if run.Status == "running" {
			running++
		}
	}
	return queued, running, time.Since(s.agentSeen) < 5*time.Second
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{targets: map[string]core.Target{}, runs: map[string]core.Run{}, searches: map[string]core.AutoSearch{}, searchRun: map[string]string{}, shards: map[string]core.Shard{}, shardResults: map[string]core.RunResult{}, progress: map[string]core.RunProgress{}}
}

func (s *MemoryStore) CreateSearch(config core.AutoSearchConfig) core.AutoSearch {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	// Plan the whole sweep up front so the operator can see what will be measured.
	search := core.AutoSearch{
		ID: fmt.Sprintf("search-%d", s.nextID), Status: core.AutoSearchRunning, Config: config,
		NextLoad: config.StartLoad, Ladder: core.SweepLadder(config.StartLoad, config.MaxLoad),
	}
	s.queueSearchRunLocked(&search, config.StartLoad)
	s.searches[search.ID] = search
	return search
}

func (s *MemoryStore) queueSearchRunLocked(search *core.AutoSearch, load int) {
	config := search.Config.Run
	if config.Mode == core.LoadModeRPS {
		config.RPS = load
	} else {
		config.VUs = load
	}
	s.nextID++
	run := core.Run{ID: fmt.Sprintf("run-%d", s.nextID), Status: "queued", SearchID: search.ID, Config: config}
	run.Config.WorkloadID = run.ID
	s.runs[run.ID] = run
	s.queueShardsLocked(run)
	search.RunIDs = append(search.RunIDs, run.ID)
	s.searchRun[run.ID] = search.ID
}

func (s *MemoryStore) GetSearch(id string) (core.AutoSearch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search, ok := s.searches[id]
	return search, ok
}

func (s *MemoryStore) ListSearches() []core.AutoSearch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	searches := make([]core.AutoSearch, 0, len(s.searches))
	for _, search := range s.searches {
		searches = append(searches, search)
	}
	return searches
}

func (s *MemoryStore) CancelSearch(id string) (core.AutoSearch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	search, ok := s.searches[id]
	if !ok {
		return core.AutoSearch{}, false
	}
	search.Status = core.AutoSearchCancelled
	search.Message = "cancelled by user"
	s.searches[id] = search
	for _, runID := range search.RunIDs {
		_, _ = s.cancelRunLocked(runID)
	}
	return search, true
}

func (s *MemoryStore) CancelRun(id string) (core.Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelRunLocked(id)
}

func (s *MemoryStore) cancelRunLocked(id string) (core.Run, bool) {
	run, ok := s.runs[id]
	if !ok {
		return core.Run{}, false
	}
	if run.Status != "queued" && run.Status != "running" {
		return run, true
	}
	run.Status = "cancelled"
	s.runs[id] = run
	for shardID, shard := range s.shards {
		if shard.RunID == id && shard.Status == "queued" {
			shard.Status = "cancelled"
			s.shards[shardID] = shard
		}
	}
	return run, true
}

func (s *MemoryStore) AdvanceSearch(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	searchID, ok := s.searchRun[runID]
	if !ok {
		return
	}
	search := s.searches[searchID]
	if search.Status != core.AutoSearchRunning {
		return
	}
	run := s.runs[runID]
	next, more := core.AdvanceSearch(&search, run)
	if more {
		s.queueSearchRunLocked(&search, next)
	}
	s.searches[searchID] = search
}

func (s *MemoryStore) CreateTarget(target core.Target) core.Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	target.ID = fmt.Sprintf("target-%d", s.nextID)
	s.targets[target.ID] = target
	return target
}

func (s *MemoryStore) GetTarget(id string) (core.Target, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.targets[id]
	return target, ok
}

func (s *MemoryStore) ListTargets() []core.Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	targets := make([]core.Target, 0, len(s.targets))
	for _, target := range s.targets {
		targets = append(targets, target)
	}
	return targets
}

func (s *MemoryStore) DeleteTarget(id string) DeleteTargetResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.targets[id]; !ok {
		return TargetNotFound
	}
	for _, run := range s.runs {
		if run.Config.TargetID == id && (run.Status == "queued" || run.Status == "running") {
			return TargetInUse
		}
	}
	delete(s.targets, id)
	return TargetDeleted
}

func (s *MemoryStore) CreateRun(config core.RunConfig) core.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	run := core.Run{ID: fmt.Sprintf("run-%d", s.nextID), Status: "queued", Config: config}
	run.Config.WorkloadID = run.ID
	s.runs[run.ID] = run
	s.queueShardsLocked(run)
	return run
}

func (s *MemoryStore) queueShardsLocked(run core.Run) {
	count := run.Config.Shards
	if count < 1 {
		count = 1
	}
	for i := 0; i < count; i++ {
		s.nextID++
		shard := core.Shard{ID: fmt.Sprintf("shard-%d", s.nextID), RunID: run.ID, Status: "queued", Index: i}
		s.shards[shard.ID] = shard
	}
}

func (s *MemoryStore) GetRun(id string) (core.Run, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[id]
	if ok {
		run.Progress = s.runProgressLocked(id)
	}
	return run, ok
}

func (s *MemoryStore) ClaimRun() (core.Assignment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for shardID, shard := range s.shards {
		if shard.Status != "queued" {
			continue
		}
		run := s.runs[shard.RunID]
		if run.Status != "queued" && run.Status != "running" {
			shard.Status = "cancelled"
			s.shards[shardID] = shard
			continue
		}
		target, ok := s.targets[run.Config.TargetID]
		if !ok {
			run.Status = "cancelled"
			s.runs[run.ID] = run
			shard.Status = "cancelled"
			s.shards[shardID] = shard
			continue
		}
		shard.Status = "running"
		s.shards[shardID] = shard
		run.Status = "running"
		if run.StartedUnix == 0 {
			run.StartedUnix = time.Now().Unix()
		}
		s.runs[run.ID] = run
		// Each shard counts its own requests from 1, so the cache-bypass nonce
		// must carry the shard ID too or two shards emit identical prompts and
		// hit each other's prefix cache.
		run.Config.WorkloadID = run.ID + "-" + shard.ID
		count := run.Config.Shards
		if count < 1 {
			count = 1
		}
		if run.Config.Mode == core.LoadModeVU {
			base, extra := run.Config.VUs/count, run.Config.VUs%count
			run.Config.VUs = base
			if shard.Index < extra {
				run.Config.VUs++
			}
		} else {
			base, extra := run.Config.RPS/count, run.Config.RPS%count
			run.Config.RPS = base
			if shard.Index < extra {
				run.Config.RPS++
			}
		}
		return core.Assignment{Run: run, Target: target, Shard: shard}, true
	}
	return core.Assignment{}, false
}

func (s *MemoryStore) CompleteRun(id string, result core.RunResult) (core.Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return core.Run{}, false
	}
	if run.Status == "cancelled" {
		return run, true
	}
	run.Status = "completed"
	run.Result = result
	s.runs[id] = run
	return run, true
}

func (s *MemoryStore) CompleteShard(id string, result core.RunResult) (core.Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	shard, ok := s.shards[id]
	if !ok {
		return core.Run{}, false
	}
	shard.Status = "completed"
	s.shards[id] = shard
	s.shardResults[id] = result
	run := s.runs[shard.RunID]
	if run.Status == "cancelled" {
		return run, true
	}
	for _, item := range s.shards {
		if item.RunID == run.ID && item.Status != "completed" {
			return run, true
		}
	}
	merged := core.RunResult{StatusCounts: map[string]int64{}}
	var distributionWeight int64
	timeline := map[int64]core.TimelinePoint{}
	scenarios := map[string]*core.ScenarioResult{}
	scenarioSamples := map[string]*core.RunSamples{}
	var scenarioOrder []string
	pooled := &core.RunSamples{}
	pooledAny := false
	for shardID, item := range s.shards {
		if item.RunID != run.ID {
			continue
		}
		value := s.shardResults[shardID]
		merged.Successes += value.Successes
		merged.Failures += value.Failures
		merged.Total += value.Total
		merged.DroppedArrivals += value.DroppedArrivals
		merged.AgentSessions += value.AgentSessions
		merged.CompletedSessions += value.CompletedSessions
		merged.Issued += value.Issued
		merged.Completed += value.Completed
		merged.Cancelled += value.Cancelled
		merged.HTTPFailures += value.HTTPFailures
		merged.TransportErrors += value.TransportErrors
		merged.SteadySamples += value.SteadySamples
		merged.SteadySeconds = max(merged.SteadySeconds, value.SteadySeconds)
		merged.DrainedSeconds = max(merged.DrainedSeconds, value.DrainedSeconds)
		merged.MissingUsageResponses += value.MissingUsageResponses
		merged.ContentChunks += value.ContentChunks
		merged.OutputLengthPinned = value.OutputLengthPinned
		merged.ContextAccumulated = value.ContextAccumulated
		if value.Samples != nil {
			pooledAny = true
			appendSamples(pooled, value.Samples)
		}
		merged.Tokens.Prompt += value.Tokens.Prompt
		merged.Tokens.Completion += value.Tokens.Completion
		merged.Tokens.Reasoning += value.Tokens.Reasoning
		merged.Errors = append(merged.Errors, value.Errors...)
		if value.StoppedByGuardrail {
			merged.StoppedByGuardrail = true
			merged.GuardrailMessage = value.GuardrailMessage
		}
		for _, point := range value.Timeline {
			timeline[point.Second] = mergeTimelinePoint(timeline[point.Second], point)
		}
		for _, scenario := range value.Scenarios {
			if scenarios[scenario.Name] == nil {
				scenarios[scenario.Name] = &core.ScenarioResult{Name: scenario.Name}
				scenarioOrder = append(scenarioOrder, scenario.Name)
				scenarioSamples[scenario.Name] = &core.RunSamples{}
			}
			mergeScenario(scenarios[scenario.Name], scenario)
			if scenario.Samples != nil {
				appendSamples(scenarioSamples[scenario.Name], scenario.Samples)
			}
		}
		// Weight percentiles by the steady-state samples they were computed from,
		// falling back to successes for results from an older Agent.
		weight := value.SteadySamples
		if weight == 0 {
			weight = value.Successes
		}
		if weight > 0 {
			merged.Latency = mergeDistribution(merged.Latency, value.Latency, distributionWeight, weight)
			merged.TTFT = mergeDistribution(merged.TTFT, value.TTFT, distributionWeight, weight)
			merged.TTFO = mergeDistribution(merged.TTFO, value.TTFO, distributionWeight, weight)
			merged.TPOT = mergeDistribution(merged.TPOT, value.TPOT, distributionWeight, weight)
			merged.ITL = mergeDistribution(merged.ITL, value.ITL, distributionWeight, weight)
			distributionWeight += weight
		}
		if value.Successes > 0 {
			// Weight by everything that finished, matching the shard's own goodput
			// denominator, so a shard that mostly errored cannot be under-weighted.
			merged.GoodputPercent += value.GoodputPercent * float64(value.Total)
		}
		for code, count := range value.StatusCounts {
			merged.StatusCounts[code] += count
		}
	}
	merged.Timeline = sortedTimeline(timeline)
	// A percentile is not a linear statistic, so recompute every distribution from
	// the pooled raw samples rather than from the per-shard percentiles. Falls back
	// to the weighted per-shard merge only for results that carry no samples.
	if pooledAny {
		merged.Latency = distribution(pooled.Latency)
		merged.TTFT = distribution(pooled.TTFT)
		merged.TTFO = distribution(pooled.TTFO)
		merged.ITL = distribution(pooled.ITL)
		merged.TPOT = distribution(pooled.TPOT)
		merged.LatencyScope = "pooled_samples"
	}
	for _, name := range scenarioOrder {
		scenario := *scenarios[name]
		if scenario.Issued > 0 {
			scenario.CompletionPercent = float64(scenario.Completed) * 100 / float64(scenario.Issued)
		}
		if samples := scenarioSamples[name]; samples != nil && len(samples.Latency) > 0 {
			scenario.Latency = distribution(samples.Latency)
			scenario.TTFT = distribution(samples.TTFT)
		}
		scenario.Samples = nil
		merged.Scenarios = append(merged.Scenarios, scenario)
	}
	merged.P95Millis, merged.TTFTP95Millis = merged.Latency.P95Millis, merged.TTFT.P95Millis
	if merged.Total > 0 {
		merged.GoodputPercent /= float64(merged.Total)
	}
	if merged.Issued > 0 {
		merged.CompletionPercent = float64(merged.Completed) * 100 / float64(merged.Issued)
	}
	if run.Config.Shards > 1 && !pooledAny {
		merged.LatencyScope = "worst_shard_p95"
	}
	if run.Config.DurationSeconds > 0 {
		merged.ThroughputRPS = float64(merged.Total) / float64(run.Config.DurationSeconds)
		merged.Tokens.OutputPerSecond = float64(merged.Tokens.Completion) / float64(run.Config.DurationSeconds)
	}
	// Raw samples exist only to be pooled. Drop them from the stored result and
	// from the per-shard results so they never reach the snapshot or the API.
	merged.Samples = nil
	run.Status = "completed"
	run.Result = merged
	s.runs[run.ID] = run
	s.clearRunProgressLocked(run.ID)
	s.clearShardSamplesLocked(run.ID)
	return run, true
}

func (s *MemoryStore) clearShardSamplesLocked(runID string) {
	for shardID, shard := range s.shards {
		if shard.RunID != runID {
			continue
		}
		if result, ok := s.shardResults[shardID]; ok {
			result.Samples = nil
			for i := range result.Scenarios {
				result.Scenarios[i].Samples = nil
			}
			s.shardResults[shardID] = result
		}
	}
}

func appendSamples(into *core.RunSamples, from *core.RunSamples) {
	into.Latency = append(into.Latency, from.Latency...)
	into.TTFT = append(into.TTFT, from.TTFT...)
	into.TTFO = append(into.TTFO, from.TTFO...)
	into.ITL = append(into.ITL, from.ITL...)
	into.TPOT = append(into.TPOT, from.TPOT...)
	if from.Decimated {
		into.Decimated = true
	}
}

// distribution computes exact order statistics over pooled samples.
func distribution(values []int64) core.Distribution {
	if len(values) == 0 {
		return core.Distribution{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum int64
	for _, value := range sorted {
		sum += value
	}
	return core.Distribution{
		MinMillis: sorted[0],
		AvgMillis: sum / int64(len(sorted)),
		P50Millis: percentile(sorted, 50),
		P95Millis: percentile(sorted, 95),
		P99Millis: percentile(sorted, 99),
		MaxMillis: sorted[len(sorted)-1],
	}
}

func percentile(sorted []int64, percentage int) int64 {
	return sorted[(len(sorted)*percentage+99)/100-1]
}

// mergeTimelinePoint sums the counters of the same elapsed second across shards
// so a distributed run reports one row per second instead of one row per shard.
func mergeTimelinePoint(current, next core.TimelinePoint) core.TimelinePoint {
	return core.TimelinePoint{
		Second:     next.Second,
		Requests:   current.Requests + next.Requests,
		Successes:  current.Successes + next.Successes,
		Failures:   current.Failures + next.Failures,
		Issued:     current.Issued + next.Issued,
		Completed:  current.Completed + next.Completed,
		Cancelled:  current.Cancelled + next.Cancelled,
		Active:     current.Active + next.Active,
		Waiting:    current.Waiting + next.Waiting,
		TargetLoad: current.TargetLoad + next.TargetLoad,
		P95Millis:  max(current.P95Millis, next.P95Millis),
	}
}

func mergeScenario(current *core.ScenarioResult, next core.ScenarioResult) {
	previous := current.Completed
	current.Issued += next.Issued
	current.Completed += next.Completed
	current.Failures += next.Failures
	current.Cancelled += next.Cancelled
	current.OutputTokens += next.OutputTokens
	current.InputTokens += next.InputTokens
	current.OutputPerSecond += next.OutputPerSecond
	if next.Completed > 0 {
		current.Latency = mergeDistribution(current.Latency, next.Latency, previous, next.Completed)
		current.TTFT = mergeDistribution(current.TTFT, next.TTFT, previous, next.Completed)
	}
}

func sortedTimeline(points map[int64]core.TimelinePoint) []core.TimelinePoint {
	timeline := make([]core.TimelinePoint, 0, len(points))
	for _, point := range points {
		timeline = append(timeline, point)
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Second < timeline[j].Second })
	return timeline
}

func mergeDistribution(current, next core.Distribution, currentWeight, nextWeight int64) core.Distribution {
	if currentWeight == 0 {
		return next
	}
	return core.Distribution{
		MinMillis: min(current.MinMillis, next.MinMillis),
		AvgMillis: (current.AvgMillis*currentWeight + next.AvgMillis*nextWeight) / (currentWeight + nextWeight),
		P50Millis: max(current.P50Millis, next.P50Millis),
		P95Millis: max(current.P95Millis, next.P95Millis),
		P99Millis: max(current.P99Millis, next.P99Millis),
		MaxMillis: max(current.MaxMillis, next.MaxMillis),
	}
}

func min(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
func max(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

// maxMonitoringSamples bounds the per-second server-side series kept per run,
// covering the longest allowed run plus its drain and cooldown.
const maxMonitoringSamples = core.MaxDurationSeconds + 900

func (s *MemoryStore) AddMonitoring(id string, sample core.MonitoringSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok || len(run.Monitoring) >= maxMonitoringSamples {
		return
	}
	// Stamp the sample with its offset from the moment the run started so the
	// server-side series lines up with the client-side timeline.
	if run.StartedUnix > 0 {
		sample.AtSecond = time.Now().Unix() - run.StartedUnix
	}
	run.Monitoring = append(run.Monitoring, sample)
	s.runs[id] = run
}

// ActiveRunIDs lists the runs currently executing, so the Controller knows which
// ones to attach server-side samples to.
func (s *MemoryStore) ActiveRunIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for id, run := range s.runs {
		if run.Status == "running" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
