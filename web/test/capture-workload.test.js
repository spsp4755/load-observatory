import test from "node:test";
import assert from "node:assert/strict";
import { captureStats, captureToWorkload } from "../src/capture-workload.js";

test("capture becomes one realistic accumulated coding job per VU", () => {
  const capture = { events: [
    { timestamp_ms: 0, latency_ms: 1000, prompt_tokens: 1500, max_tokens: 4096 },
    { timestamp_ms: 4000, latency_ms: 2000, prompt_tokens: 12000, max_tokens: 20000, output_tokens: 6000 },
  ] };
  const workload = captureToWorkload(capture, { vus: "30", maxTokens: "100" }, { default_replay_vus: 50, replay_think_time_scale: 0.5, replay_buffer_seconds: 400, replay_drain_seconds: 180 });
  assert.equal(workload.sessionsPerVU, "1");
  assert.equal(workload.accumulateContext, true);
  assert.equal(workload.scenario[0].think_time_millis, 1500);
  assert.equal(workload.vus, "50");
  assert.equal(workload.drainSeconds, "180");
  assert.equal(workload.scenario[1].prompt_tokens, 12000);
  assert.equal(workload.maxTokens, "20000");
  assert.deepEqual(captureStats(capture), { calls: 2, durationSeconds: 6, maxPromptTokens: 12000, maxOutputTokens: 6000 });
});
