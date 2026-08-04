import assert from "node:assert/strict";
import test from "node:test";
import { toRunConfig } from "../src/run-form.js";
import { toSearchConfig } from "../src/run-form.js";

test("toRunConfig converts VU form values to a controller payload", () => {
  assert.deepEqual(
    toRunConfig({ targetId: "target-1", mode: "vu", vus: "10", rps: "100", duration: "60", prompt: "write a Go API", maxTokens: "4096", maxErrorPercent: "2", maxP95Millis: "2000" }),
    { target_id: "target-1", mode: "vu", vus: 10, duration_seconds: 60, prompt: "write a Go API", max_tokens: 4096, max_error_percent: 2, max_p95_millis: 2000, cache_policy: "mixed", variation_percent: 30, shards: 3, max_ttft_p95_millis: 0, min_output_tokens_per_second: 0, max_tpot_p95_millis: 0, min_goodput_percent: 0, max_in_flight: 0, warmup_requests: 0, cooldown_seconds: 0, drain_seconds: 0, steady_state_seconds: 0, min_completion_percent: 0, stages: [], scenario: [], agent_workflow: false, journeys: [] },
  );
});

test("toRunConfig forwards the drain, steady-state and completion settings", () => {
  const config = toRunConfig({ targetId: "target-1", mode: "vu", vus: "30", duration: "600", prompt: "base", maxTokens: "20480", maxErrorPercent: "2", maxP95Millis: "2000", drainSeconds: "120", steadyStateSeconds: "60", minCompletionPercent: "95" });
  assert.equal(config.drain_seconds, 120);
  assert.equal(config.steady_state_seconds, 60);
  assert.equal(config.min_completion_percent, 95);
});

test("toSearchConfig uses the configured automatic search bounds", () => {
  const config = toSearchConfig({ targetId: "target-1", mode: "vu", vus: "5", rps: "10", duration: "30", prompt: "test", maxTokens: "32", maxErrorPercent: "2", maxP95Millis: "2000", startLoad: "5", maxLoad: "40" });
  assert.equal(config.start_load, 5);
  assert.equal(config.max_load, 40);
  assert.equal(config.run.vus, 5);
});

test("toRunConfig preserves stages, guardrails, and weighted scenarios", () => {
  const config = toRunConfig({ targetId: "target-1", mode: "rps", rps: "20", duration: "30", prompt: "base", maxTokens: "64", maxErrorPercent: "2", maxP95Millis: "2000", maxTTFTP95Millis: "500", minOutputTokensPerSecond: "5", maxTTPOTP95Millis: "100", minGoodputPercent: "90", maxInFlight: "12", warmupRequests: "3", cooldownSeconds: "2", stages: [{ duration_seconds: 10, target_load: 10 }], scenario: [{ name: "coding", prompt: "write a handler", weight: 3, think_time_millis: 100 }] });
  assert.equal(config.max_in_flight, 12);
  assert.equal(config.warmup_requests, 3);
  assert.equal(config.stages[0].target_load, 10);
  assert.equal(config.scenario[0].weight, 3);
  assert.equal(config.max_tpot_p95_millis, 100);
  assert.equal(config.min_goodput_percent, 90);
});

test("toRunConfig preserves mixed user journeys", () => {
  const journeys = [{ name: "agent", weight: 15, agent_workflow: true, scenario: [{ name: "search", prompt: "tool search", weight: 1, max_tokens: 4096 }] }];
  const config = toRunConfig({ targetId: "target-1", mode: "vu", vus: "10", duration: "30", prompt: "base", maxTokens: "64", maxErrorPercent: "2", maxP95Millis: "2000", journeys });
  assert.deepEqual(config.journeys, journeys);
});
