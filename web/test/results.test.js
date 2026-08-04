import assert from "node:assert/strict";
import test from "node:test";
import { getVerdict, gpuBoundLabel, hasMetric, metricKeys, metricMean, metricPeak, summarizeMonitoring } from "../src/results.js";

test("getVerdict recommends the configured load when a completed run is stable", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "vu", vus: 10, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 1, latency: { p95_millis: 250 } } });
  assert.equal(verdict.status, "stable");
  assert.match(verdict.recommendation, /10/);
});

test("getVerdict flags threshold breaches", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "rps", rps: 50, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 3, latency: { p95_millis: 2500 } } });
  assert.equal(verdict.status, "at-risk");
});

test("summarizeMonitoring reports peak and mean per metric", () => {
  const summary = summarizeMonitoring([
    { status: "collected", backend: "vllm", metrics: { gpu_utilization: 55, kv_cache_usage: 0.4 } },
    { status: "collected", backend: "vllm", metrics: { gpu_utilization: 85, kv_cache_usage: 0.6 } },
  ]);
  assert.equal(summary.available, true);
  assert.equal(summary.backend, "vllm");
  assert.equal(metricPeak(summary, metricKeys.gpuUtilization), 85);
  assert.equal(metricMean(summary, metricKeys.gpuUtilization), 70);
  assert.equal(metricMean(summary, metricKeys.kvCacheUsage), 0.5);
});

// A metric that was never collected must be absent, not zero: an unmeasured GPU
// must never read as an idle one.
test("summarizeMonitoring reports an uncollected metric as absent, not zero", () => {
  const summary = summarizeMonitoring([{ status: "partial", metrics: { gpu_utilization: 99 } }]);
  assert.equal(hasMetric(summary, metricKeys.gpuUtilization), true);
  assert.equal(hasMetric(summary, metricKeys.dramActive), false);
  assert.equal(metricPeak(summary, metricKeys.dramActive), undefined);
});

// GPU utilization at ~100% with low DRAM activity is an under-used GPU, not a
// saturated one. This is the classic LLM capacity-planning error.
test("gpuBoundLabel does not read a busy-looking GPU as saturated", () => {
  const idle = summarizeMonitoring([{ status: "collected", metrics: { gpu_utilization: 99, dram_active: 0.12, tensor_active: 0.05 } }]);
  assert.equal(gpuBoundLabel(idle).bound, "underused");

  const memoryBound = summarizeMonitoring([{ status: "collected", metrics: { gpu_utilization: 99, dram_active: 0.78, tensor_active: 0.2 } }]);
  assert.equal(gpuBoundLabel(memoryBound).bound, "memory");
  assert.match(gpuBoundLabel(memoryBound).text, /정상/);

  const computeBound = summarizeMonitoring([{ status: "collected", metrics: { gpu_utilization: 99, dram_active: 0.3, tensor_active: 0.7 } }]);
  assert.equal(gpuBoundLabel(computeBound).bound, "compute");
});

// Without the profiling fields no GPU-bound claim can be made at all.
test("gpuBoundLabel makes no claim without DCGM profiling fields", () => {
  const summary = summarizeMonitoring([{ status: "partial", metrics: { gpu_utilization: 99 } }]);
  assert.equal(gpuBoundLabel(summary), null);
});

test("summarizeMonitoring preserves an unavailable reason", () => {
  const summary = summarizeMonitoring([{ status: "unavailable", message: "Prometheus URL not configured" }]);
  assert.equal(summary.available, false);
  assert.match(summary.message, /Prometheus/);
});
