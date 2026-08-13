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
	if completed, _ := s.CompleteShard(first.Shard.ID, core.RunResult{Successes: 1, Total: 1, Latency: core.Distribution{MinMillis: 5, AvgMillis: 10, P50Millis: 8, P95Millis: 12, P99Millis: 14, MaxMillis: 15}}); completed.Status == "completed" {
		t.Fatal("parent completed before second shard")
	}
	completed, ok := s.CompleteShard(second.Shard.ID, core.RunResult{Successes: 2, Total: 2, Latency: core.Distribution{MinMillis: 10, AvgMillis: 20, P50Millis: 18, P95Millis: 30, P99Millis: 35, MaxMillis: 40}})
	if !ok || completed.Status != "completed" || completed.Result.Successes != 3 || completed.Result.LatencyScope != "worst_shard_p95" || completed.Result.Latency.MinMillis != 5 || completed.Result.Latency.AvgMillis != 16 || completed.Result.Latency.MaxMillis != 40 {
		t.Fatalf("unexpected merged run: %+v", completed)
	}
	_ = run
}

func TestCaptureSettingsSplitAnIdleClientIntoANewJob(t *testing.T) {
	s := NewMemoryStore()
	s.SetCaptureSettings(core.CaptureSettings{Enabled: true, SessionIdleMinutes: 5, MaxEventsPerSession: 64, RetentionSessions: 20, DefaultReplayVUs: 30, ReplayThinkTimeScale: 1, ReplayBufferSeconds: 300, ReplayDrainSeconds: 120})
	first := core.CaptureSession{ID: "capture-client", TargetID: "target-1", StartedUnixMillis: 1_000, UpdatedUnixMillis: 2_000}
	s.RecordCapture(first, core.CaptureEvent{PromptTokens: 100})
	second := core.CaptureSession{ID: "capture-client", TargetID: "target-1", StartedUnixMillis: 400_000, UpdatedUnixMillis: 401_000}
	s.RecordCapture(second, core.CaptureEvent{PromptTokens: 200})
	captures := s.ListCaptures()
	if len(captures) != 2 || captures[0].Events[0].PromptTokens != 200 || captures[1].Events[0].PromptTokens != 100 {
		t.Fatalf("idle client was not split into two jobs: %#v", captures)
	}
}
