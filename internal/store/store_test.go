package store

import (
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
)

func TestClaimRunSplitsConfiguredShards(t *testing.T) {
	s := NewMemoryStore()
	target := s.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.1"})
	s.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 4, DurationSeconds: 1, Shards: 2})
	first, ok := s.ClaimRun()
	if !ok || first.Shard.ID == "" || first.Run.Config.VUs != 2 {
		t.Fatalf("first shard: %+v", first)
	}
	second, ok := s.ClaimRun()
	if !ok || second.Shard.ID == first.Shard.ID || second.Run.Config.VUs != 2 {
		t.Fatalf("second shard: %+v", second)
	}
}

func TestCompleteShardWaitsForAllShardResults(t *testing.T) {
	s := NewMemoryStore()
	target := s.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.1"})
	run := s.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 1, Shards: 2})
	first, _ := s.ClaimRun()
	second, _ := s.ClaimRun()
	if completed, _ := s.CompleteShard(first.Shard.ID, core.RunResult{Successes: 1, Total: 1}); completed.Status == "completed" {
		t.Fatal("parent completed before second shard")
	}
	completed, ok := s.CompleteShard(second.Shard.ID, core.RunResult{Successes: 2, Total: 2})
	if !ok || completed.Status != "completed" || completed.Result.Successes != 3 {
		t.Fatalf("unexpected merged run: %+v", completed)
	}
	_ = run
}
