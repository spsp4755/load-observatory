package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

type Store interface {
	CreateTarget(core.Target) core.Target
	GetTarget(string) (core.Target, bool)
	ListTargets() []core.Target
	DeleteTarget(string) bool
	CreateRun(core.RunConfig) core.Run
	GetRun(string) (core.Run, bool)
	ListRuns() []core.Run
	ClaimRun() (core.Assignment, bool)
	CompleteRun(string, core.RunResult) (core.Run, bool)
	CompleteShard(string, core.RunResult) (core.Run, bool)
	AddMonitoring(string, core.MonitoringSample)
	TouchAgent()
	Health() (int, int, bool)
	CreateSearch(core.AutoSearchConfig) core.AutoSearch
	GetSearch(string) (core.AutoSearch, bool)
	ListSearches() []core.AutoSearch
	CancelSearch(string) (core.AutoSearch, bool)
	AdvanceSearch(string)
}

type MemoryStore struct {
	mu           sync.RWMutex
	nextID       int
	targets      map[string]core.Target
	runs         map[string]core.Run
	searches     map[string]core.AutoSearch
	searchRun    map[string]string
	shards       map[string]core.Shard
	shardResults map[string]core.RunResult
	agentSeen    time.Time
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
	return nil
}

func (s *MemoryStore) ListRuns() []core.Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]core.Run, 0, len(s.runs))
	for _, run := range s.runs {
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
	return &MemoryStore{targets: map[string]core.Target{}, runs: map[string]core.Run{}, searches: map[string]core.AutoSearch{}, searchRun: map[string]string{}, shards: map[string]core.Shard{}, shardResults: map[string]core.RunResult{}}
}

func (s *MemoryStore) CreateSearch(config core.AutoSearchConfig) core.AutoSearch {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	search := core.AutoSearch{ID: fmt.Sprintf("search-%d", s.nextID), Status: core.AutoSearchRunning, Config: config, NextLoad: config.StartLoad}
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
		run := s.runs[runID]
		if run.Status == "queued" || run.Status == "running" {
			run.Status = "cancelled"
			s.runs[runID] = run
		}
	}
	return search, true
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

func (s *MemoryStore) DeleteTarget(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.targets[id]; !ok {
		return false
	}
	delete(s.targets, id)
	return true
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
		target, ok := s.targets[run.Config.TargetID]
		if !ok {
			continue
		}
		shard.Status = "running"
		s.shards[shardID] = shard
		run.Status = "running"
		s.runs[run.ID] = run
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
	for _, item := range s.shards {
		if item.RunID == run.ID && item.Status != "completed" {
			return run, true
		}
	}
	merged := core.RunResult{StatusCounts: map[string]int64{}}
	for shardID, item := range s.shards {
		if item.RunID != run.ID {
			continue
		}
		value := s.shardResults[shardID]
		merged.Successes += value.Successes
		merged.Failures += value.Failures
		merged.Total += value.Total
		merged.Tokens.Prompt += value.Tokens.Prompt
		merged.Tokens.Completion += value.Tokens.Completion
		merged.Tokens.Reasoning += value.Tokens.Reasoning
		merged.Timeline = append(merged.Timeline, value.Timeline...)
		merged.Errors = append(merged.Errors, value.Errors...)
		if value.Latency.P95Millis > merged.Latency.P95Millis {
			merged.Latency.P95Millis = value.Latency.P95Millis
		}
		if value.TTFT.P95Millis > merged.TTFT.P95Millis {
			merged.TTFT.P95Millis = value.TTFT.P95Millis
		}
		for code, count := range value.StatusCounts {
			merged.StatusCounts[code] += count
		}
	}
	merged.P95Millis, merged.TTFTP95Millis = merged.Latency.P95Millis, merged.TTFT.P95Millis
	if run.Config.DurationSeconds > 0 {
		merged.ThroughputRPS = float64(merged.Total) / float64(run.Config.DurationSeconds)
		merged.Tokens.OutputPerSecond = float64(merged.Tokens.Completion) / float64(run.Config.DurationSeconds)
	}
	run.Status = "completed"
	run.Result = merged
	s.runs[run.ID] = run
	return run, true
}

func (s *MemoryStore) AddMonitoring(id string, sample core.MonitoringSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if ok {
		run.Monitoring = append(run.Monitoring, sample)
		s.runs[id] = run
	}
}
