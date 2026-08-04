import assert from "node:assert/strict";
import test from "node:test";
import { toRunConfig } from "../src/run-form.js";
import { toSearchConfig } from "../src/run-form.js";

test("toRunConfig converts VU form values to a controller payload", () => {
  assert.deepEqual(
    toRunConfig({ targetId: "target-1", mode: "vu", vus: "10", rps: "100", duration: "60", prompt: "write a Go API", maxTokens: "4096", maxErrorPercent: "2", maxP95Millis: "2000" }),
    { target_id: "target-1", mode: "vu", vus: 10, duration_seconds: 60, prompt: "write a Go API", max_tokens: 4096, max_error_percent: 2, max_p95_millis: 2000, cache_policy: "mixed", variation_percent: 30, shards: 3, max_ttft_p95_millis: 0, min_output_tokens_per_second: 0, max_tpot_p95_millis: 0, min_goodput_percent: 0, max_in_flight: 0, warmup_requests: 0, cooldown_seconds: 0, stages: [], scenario: [], agent_workflow: false },
  );
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
