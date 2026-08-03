import assert from "node:assert/strict";
import test from "node:test";
import { getVerdict, summarizeMonitoring } from "../src/results.js";

test("getVerdict recommends the configured load when a completed run is stable", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "vu", vus: 10, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 1, latency: { p95_millis: 250 } } });
  assert.equal(verdict.status, "stable");
  assert.match(verdict.recommendation, /10/);
});

test("getVerdict flags threshold breaches", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "rps", rps: 50, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 3, latency: { p95_millis: 2500 } } });
  assert.equal(verdict.status, "at-risk");
});

test("summarizeMonitoring reports peak resource usage and collection state", () => {
  const summary = summarizeMonitoring([{ status: "collected", gpu_utilization: 55, gpu_memory_used: 40, cpu_utilization: 20, memory_used: 10 }, { status: "collected", gpu_utilization: 82, gpu_memory_used: 64, cpu_utilization: 35, memory_used: 30 }]);
  assert.deepEqual(summary, { available: true, gpu: 82, gpuMemory: 64, cpu: 35, memory: 30, message: "" });
});

test("summarizeMonitoring preserves an unavailable reason", () => {
  const summary = summarizeMonitoring([{ status: "unavailable", message: "Prometheus URL not configured" }]);
  assert.equal(summary.available, false);
  assert.match(summary.message, /Prometheus/);
});
