import assert from "node:assert/strict";
import test from "node:test";
import { compareRuns, filterRuns, policyText, presets, sortRuns } from "../src/record-utils.js";

const runs = [
  { id: "run-1", status: "completed", config: { target_id: "model", vus: 5, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 10, successes: 10, latency: { p95_millis: 100 }, throughput_rps: 5 } },
  { id: "run-2", status: "completed", config: { target_id: "model", vus: 10, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 10, successes: 9, failures: 1, latency: { p95_millis: 3000 }, throughput_rps: 8 } },
];
test("presets use the configured five-times token values", () => assert.deepEqual([presets.short.maxTokens, presets.coding.maxTokens, presets.long.maxTokens], ["1280", "20480", "163840"]));
test("filters stable runs and sorts P95", () => { assert.equal(filterRuns(runs, { verdict: "stable" }).length, 1); assert.equal(sortRuns(runs, "p95")[0].id, "run-2"); });
test("compares two selected runs", () => assert.equal(compareRuns(runs[0], runs[1]).find((row) => row.key === "load").delta, 5));
test("uses the mixed default for legacy runs without a variation value", () => assert.equal(policyText({ config: { cache_policy: "mixed" } }), "혼합 (30% 변형)"));
