import assert from "node:assert/strict";
import test from "node:test";
import { recommendWorkload } from "../src/workload-profiles.js";

test("recommends a sequential development-agent workload", () => {
  const profile = recommendWorkload("agent", { warmupRequests: "3" });
  assert.equal(profile.agentWorkflow, true);
  assert.equal(profile.scenario.length, 4);
  assert.equal(profile.scenario[1].max_tokens, 65536);
});

test("keeps RAG and long-agent workloads distinct", () => {
  const rag = recommendWorkload("rag", {});
  const longAgent = recommendWorkload("long-agent", {});
  assert.equal(rag.agentWorkflow, false);
  assert.equal(rag.maxTokens, "8192");
  assert.equal(longAgent.agentWorkflow, true);
  assert.equal(longAgent.scenario.length, 6);
});

test("recommends a weighted mixed-user workload", () => {
  const mixed = recommendWorkload("mixed", { warmupRequests: "3" });
  assert.equal(mixed.agentWorkflow, false);
  assert.deepEqual(mixed.scenario.map((task) => task.weight), [60, 25, 15]);
  assert.equal(mixed.scenario.length, 3);
});
