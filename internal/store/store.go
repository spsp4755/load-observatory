package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

type MemoryStore struct {
	mu        sync.RWMutex
	nextID    int
	targets   map[string]core.Target
	runs      map[string]core.Run
	searches  map[string]core.AutoSearch
	searchRun map[string]string
	agentSeen time.Time
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
		if run.Status == "queued" {
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
	run := core.Run{ID: fmt.Sprintf("run-%d", s.nextID), Status: "queued", Config: config}
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
		if run.Status == "queued" {
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

func (s *MemoryStore) CreateRun(config core.RunConfig) core.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	run := core.Run{ID: fmt.Sprintf("run-%d", s.nextID), Status: "queued", Config: config}
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
	run.Status = "completed"
	run.Result = result
	s.runs[id] = run
	return run, true
}
