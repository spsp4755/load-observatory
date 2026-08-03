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
	TouchAgent()
	Health() (int, int, bool)
	CreateSearch(core.AutoSearchConfig) core.AutoSearch
	GetSearch(string) (core.AutoSearch, bool)
	ListSearches() []core.AutoSearch
	CancelSearch(string) (core.AutoSearch, bool)
	AdvanceSearch(string)
}

type MemoryStore struct {
	mu        sync.RWMutex
	nextID    int
	targets   map[string]core.Target
	runs      map[string]core.Run
	searches  map[string]core.AutoSearch
	searchRun map[string]string
	agentSeen time.Time
}

func (s *MemoryStore) Snapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(struct {
		NextID    int                        `json:"next_id"`
		Targets   map[string]core.Target     `json:"targets"`
		Runs      map[string]core.Run        `json:"runs"`
		Searches  map[string]core.AutoSearch `json:"searches"`
		SearchRun map[string]string          `json:"search_run"`
	}{s.nextID, s.targets, s.runs, s.searches, s.searchRun})
}

func (s *MemoryStore) Restore(data []byte) error {
	var state struct {
		NextID    int                        `json:"next_id"`
		Targets   map[string]core.Target     `json:"targets"`
		Runs      map[string]core.Run        `json:"runs"`
		Searches  map[string]core.AutoSearch `json:"searches"`
		SearchRun map[string]string          `json:"search_run"`
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
	return &MemoryStore{targets: map[string]core.Target{}, runs: map[string]core.Run{}, searches: map[string]core.AutoSearch{}, searchRun: map[string]string{}}
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
	return run
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
	for id, run := range s.runs {
		if run.Status != "queued" {
			continue
		}
		target, ok := s.targets[run.Config.TargetID]
		if !ok {
			continue
		}
		run.Status = "running"
		s.runs[id] = run
		return core.Assignment{Run: run, Target: target}, true
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
