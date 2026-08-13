import assert from "node:assert/strict";
import test from "node:test";
import { getVerdict, gpuBoundLabel, hasMetric, metricKeys, metricMean, metricPeak, provenanceDifferences, summarizeMonitoring, workloadDifferences } from "../src/results.js";

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

// Two capacity numbers measured under different server settings are not
// comparable, and the UI has to say that rather than show a meaningless delta.
test("provenanceDifferences names the settings that changed between runs", () => {
  const left = { provenance: { server: { model: "qwen", max_num_seqs: 256, max_num_batched_tokens: 8192 } } };
  const right = { provenance: { server: { model: "qwen", max_num_seqs: 256, max_num_batched_tokens: 2048 } } };
  const differences = provenanceDifferences(left, right);
  assert.equal(differences.length, 1);
  assert.match(differences[0], /max_num_batched_tokens/);
  assert.match(differences[0], /8192/);
  assert.match(differences[0], /2048/);
});

test("provenanceDifferences is empty for identical conditions", () => {
  const run = { provenance: { server: { model: "qwen", max_num_seqs: 256 } } };
  assert.deepEqual(provenanceDifferences(run, structuredClone(run)), []);
});

// A setting known on one side and unknown on the other is still not comparable.
test("provenanceDifferences flags a setting known on only one side", () => {
  const known = { provenance: { server: { max_num_batched_tokens: 8192 } } };
  const unknown = { provenance: { server: {} } };
  const differences = provenanceDifferences(known, unknown);
  assert.equal(differences.length, 1);
  assert.match(differences[0], /미확인/);
});

// Comparing a cache-bypass run with a cache-reuse run compares two different
// questions, not two load levels.
test("workloadDifferences catches a changed cache policy or pinning", () => {
  const left = { config: { cache_policy: "bypass", max_tokens: 4096 }, result: { output_length_pinned: true, context_accumulated: false } };
  const right = { config: { cache_policy: "reuse", max_tokens: 4096 }, result: { output_length_pinned: false, context_accumulated: false } };
  const differences = workloadDifferences(left, right);
  assert.equal(differences.length, 2);
  assert.ok(differences.some((d) => d.includes("캐시 정책")));
  assert.ok(differences.some((d) => d.includes("출력 길이 고정")));
});

test("workloadDifferences is empty for the same workload", () => {
  const run = { config: { cache_policy: "bypass", max_tokens: 4096 }, result: { output_length_pinned: true, context_accumulated: true } };
  assert.deepEqual(workloadDifferences(run, structuredClone(run)), []);
});

test("workloadDifferences catches changed trace replay conditions", () => {
  const left = { config: { trace_time_scale: 1, trace: [{ timestamp_ms: 0, max_tokens: 64 }] }, result: {} };
  const right = { config: { trace_time_scale: 2, trace: [{ timestamp_ms: 0, max_tokens: 128 }] }, result: {} };
  const differences = workloadDifferences(left, right);
  assert.ok(differences.some((item) => item.includes("트레이스 재생 속도")));
  assert.ok(differences.includes("트레이스 내용: 다름"));
});
