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
	return &MemoryStore{targets: map[string]core.Target{}, runs: map[string]core.Run{}}
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
