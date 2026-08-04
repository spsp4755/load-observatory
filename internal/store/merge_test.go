package store

import (
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
)

// A distributed run must report one row per elapsed second, not one row per
// shard per second, or the per-second view double-counts and misreads.
func TestCompleteShardMergesTimelineBySecond(t *testing.T) {
	memory := NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 4, DurationSeconds: 2, Shards: 2})
	shards := shardIDs(memory, run.ID)
	if len(shards) != 2 {
		t.Fatalf("expected 2 shards, got %d", len(shards))
	}

	shardResult := core.RunResult{
		Successes: 5, Total: 5, Issued: 6, Completed: 5, Cancelled: 1, SteadySamples: 5,
		Timeline: []core.TimelinePoint{
			{Second: 0, Issued: 3, Completed: 3, Successes: 3, Requests: 3, Active: 2, TargetLoad: 2, P95Millis: 100},
			{Second: 1, Issued: 3, Completed: 2, Successes: 2, Requests: 2, Cancelled: 1, Active: 2, TargetLoad: 2, P95Millis: 200},
		},
	}
	memory.CompleteShard(shards[0], shardResult)
	completed, _ := memory.CompleteShard(shards[1], shardResult)

	if len(completed.Result.Timeline) != 2 {
		t.Fatalf("timeline not merged by second: %+v", completed.Result.Timeline)
	}
	first := completed.Result.Timeline[0]
	if first.Second != 0 || first.Issued != 6 || first.Completed != 6 || first.Active != 4 || first.TargetLoad != 4 {
		t.Fatalf("first second not summed across shards: %+v", first)
	}
	if completed.Result.Timeline[1].P95Millis != 200 {
		t.Fatalf("per-second P95 should take the worst shard: %+v", completed.Result.Timeline[1])
	}
	if completed.Result.Issued != 12 || completed.Result.Completed != 10 || completed.Result.Cancelled != 2 {
		t.Fatalf("lifecycle counters not summed: %+v", completed.Result)
	}
	if completed.Result.CompletionPercent == 0 || completed.Result.CompletionPercent >= 100 {
		t.Fatalf("merged completion rate wrong: %.2f", completed.Result.CompletionPercent)
	}
}

func TestCompleteShardMergesScenariosByName(t *testing.T) {
	memory := NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 2, Shards: 2})
	shards := shardIDs(memory, run.ID)

	shardResult := core.RunResult{Successes: 2, Total: 2, Issued: 2, Completed: 2, SteadySamples: 2, Scenarios: []core.ScenarioResult{
		{Name: "short", Issued: 1, Completed: 1, OutputTokens: 10, Latency: core.Distribution{P95Millis: 50, MinMillis: 10, MaxMillis: 50}},
		{Name: "long", Issued: 1, Completed: 1, OutputTokens: 90, Latency: core.Distribution{P95Millis: 900, MinMillis: 800, MaxMillis: 900}},
	}}
	memory.CompleteShard(shards[0], shardResult)
	completed, _ := memory.CompleteShard(shards[1], shardResult)

	if len(completed.Result.Scenarios) != 2 {
		t.Fatalf("scenarios not merged by name: %+v", completed.Result.Scenarios)
	}
	byName := map[string]core.ScenarioResult{}
	for _, scenario := range completed.Result.Scenarios {
		byName[scenario.Name] = scenario
	}
	if byName["long"].OutputTokens != 180 || byName["long"].Issued != 2 {
		t.Fatalf("long scenario not summed: %+v", byName["long"])
	}
	if byName["long"].Latency.P95Millis != 900 || byName["short"].Latency.P95Millis != 50 {
		t.Fatalf("per-scenario latency blended across scenarios: %+v", byName)
	}
	if byName["short"].CompletionPercent != 100 {
		t.Fatalf("per-scenario completion rate wrong: %+v", byName["short"])
	}
}

func TestShardProgressIsExposedOnTheRunAndClearedWhenDone(t *testing.T) {
	memory := NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 4, DurationSeconds: 2, Shards: 1})
	shards := shardIDs(memory, run.ID)

	if memory.SetShardProgress("shard-missing", core.RunProgress{}) {
		t.Fatal("progress accepted for an unknown shard")
	}
	if !memory.SetShardProgress(shards[0], core.RunProgress{Phase: "load", TargetLoad: 4, Active: 3, Completed: 7, CompletedRPS: 3.5}) {
		t.Fatal("progress rejected for a known shard")
	}
	live, _ := memory.GetRun(run.ID)
	if len(live.Progress) != 1 || live.Progress[0].Active != 3 || live.Progress[0].ShardID != shards[0] {
		t.Fatalf("live progress not exposed on the run: %+v", live.Progress)
	}
	done, _ := memory.CompleteShard(shards[0], core.RunResult{Successes: 1, Total: 1, Issued: 1, Completed: 1})
	if len(done.Progress) != 0 {
		t.Fatalf("progress kept after completion: %+v", done.Progress)
	}
	after, _ := memory.GetRun(run.ID)
	if len(after.Progress) != 0 {
		t.Fatalf("stale progress served for a finished run: %+v", after.Progress)
	}
}

func shardIDs(memory *MemoryStore, runID string) []string {
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	var ids []string
	for id, shard := range memory.shards {
		if shard.RunID == runID {
			ids = append(ids, id)
		}
	}
	return ids
}
