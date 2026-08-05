// completionShortfall reports when a run finished too few of the requests it
// started. "30 VU 중 10개만 완료" measured nothing about capacity, so this must
// outrank every latency verdict rather than sit beside it.
export function completionShortfall(run) {
  const result = run?.result || {};
  const issued = result.issued || 0;
  if (!issued) return null;
  const completed = result.completed || 0;
  const percent = result.completion_percent ?? (completed / issued) * 100;
  const limit = run?.config?.min_completion_percent || 95;
  if (percent >= limit) return null;
  const cancelled = result.cancelled || 0;
  return {
    percent,
    limit,
    issued,
    completed,
    cancelled,
    message: `시작한 요청 ${issued}건 중 ${completed}건(${percent.toFixed(1)}%)만 완료했습니다. ${cancelled}건은 측정 종료 시점에 아직 진행 중이었습니다. 기준 완료율은 ${limit}%입니다.`,
  };
}

export function getVerdict(run) {
  if (!run || run.status !== "completed") return { status: "pending", recommendation: "실행 완료 후 안정성 판정을 제공합니다." };
  const result = run.result || {};
  const total = result.total || (result.successes || 0) + (result.failures || 0);
  if (!total) return { status: "at-risk", recommendation: "완료된 요청이 없어 결과를 신뢰할 수 없습니다." };
  const errorRate = (result.failures || 0) / total * 100;
  const p95 = result.latency?.p95_millis ?? result.p95_millis ?? 0;
  const errorLimit = run.config?.max_error_percent ?? 2;
  const p95Limit = run.config?.max_p95_millis ?? 2000;
  const ttftLimit = run.config?.max_ttft_p95_millis || 0;
  const tpotLimit = run.config?.max_tpot_p95_millis || 0;
  const goodputLimit = run.config?.min_goodput_percent || 0;
  const ttft = result.ttft?.p95_millis || 0;
  const tpot = result.tpot?.p95_millis || 0;
  const goodput = result.goodput_percent || 0;
  const shortfall = completionShortfall(run);
  if (shortfall) return { status: "at-risk", recommendation: `${shortfall.message} 이 결과는 안정 용량으로 판정하지 않습니다.` };
  if (result.dropped_arrivals > 0) return { status: "at-risk", recommendation: `요청 ${result.dropped_arrivals}건이 최대 진행 요청 제한으로 시작되지 않았습니다. 이 구간은 테스트 실행기 용량도 함께 늘려야 합니다.` };
  if ((ttftLimit && ttft > ttftLimit) || (tpotLimit && tpot > tpotLimit) || (goodputLimit && goodput < goodputLimit)) return { status: "at-risk", recommendation: "LLM 응답 체감 성능 SLO를 충족하지 못했습니다. TTFT·TPOT·Goodput을 확인하세요." };
  if (errorRate > errorLimit || p95 > p95Limit) return { status: "at-risk", recommendation: "현재 부하는 위험 구간입니다. 동시 사용자 또는 요청률을 낮춰 재측정하세요." };
  const load = run.config?.mode === "rps" ? `${run.config.rps} RPS` : `${run.config?.vus || 0} VU`;
  return { status: "stable", recommendation: `${load}까지는 현재 기준에서 안정적입니다. 더 높은 단계로 재시험해 한계를 확인하세요.` };
}

export const rate = (part, total) => total ? `${(part / total * 100).toFixed(1)}%` : "—";
export const milliseconds = (value) => value == null ? "—" : `${value} ms`;

const provenanceFields = [
  ["vLLM 버전", "version"],
  ["모델", "model"],
  ["max_num_seqs", "max_num_seqs"],
  ["max_num_batched_tokens", "max_num_batched_tokens"],
  ["gpu_memory_utilization", "gpu_memory_utilization"],
  ["block_size", "block_size"],
  ["tensor_parallel_size", "tensor_parallel_size"],
  ["prefix caching", "prefix_caching"],
  ["chunked prefill", "chunked_prefill"],
];

// provenanceDifferences lists the server settings that differ between two runs.
// Two capacity numbers measured under different settings are not comparable, so
// the UI must say that rather than show a delta that looks meaningful.
export function provenanceDifferences(left, right) {
  const a = left?.provenance?.server || {};
  const b = right?.provenance?.server || {};
  const show = (value) => (value === undefined || value === null || value === "" || value === 0 ? "미확인" : String(value));
  return provenanceFields
    .filter(([, key]) => show(a[key]) !== show(b[key]))
    .map(([label, key]) => `${label}: ${show(a[key])} ↔ ${show(b[key])}`);
}

// Comparing a cache-bypass run with a cache-reuse run compares two different
// questions, so that difference is called out separately from server settings.
export function workloadDifferences(left, right) {
  const differences = [];
  const pairs = [
    ["캐시 정책", left?.config?.cache_policy, right?.config?.cache_policy],
    ["출력 길이 고정", left?.result?.output_length_pinned, right?.result?.output_length_pinned],
    ["대화 이력 누적", left?.result?.context_accumulated, right?.result?.context_accumulated],
    ["최대 출력 토큰", left?.config?.max_tokens, right?.config?.max_tokens],
  ];
  for (const [label, a, b] of pairs) {
    if (String(a) !== String(b)) differences.push(`${label}: ${a} ↔ ${b}`);
  }
  return differences;
}

// Metric keys shared with the Go side.
export const metricKeys = {
  requestsRunning: "requests_running",
  requestsWaiting: "requests_waiting",
  kvCacheUsage: "kv_cache_usage",
  preemptionRate: "preemption_rate",
  queueTimeP95: "queue_time_p95_millis",
  prefillTimeP95: "prefill_time_p95_millis",
  prefixCacheHitRate: "prefix_cache_hit_rate",
  gpuUtilization: "gpu_utilization",
  gpuMemoryUsed: "gpu_memory_used",
  dramActive: "dram_active",
  tensorActive: "tensor_active",
  smActive: "sm_active",
  smOccupancy: "sm_occupancy",
  smClockMHz: "sm_clock_mhz",
  cpuUtilization: "cpu_utilization",
  memoryUsed: "memory_used",
};

// summarizeMonitoring reports peak and mean per metric, and reports a metric that
// was never collected as absent rather than as zero — an unmeasured GPU must not
// read as an idle one.
export function summarizeMonitoring(samples = []) {
  const usable = samples.filter((sample) => sample.metrics && Object.keys(sample.metrics).length);
  if (!usable.length) {
    return {
      available: false,
      metrics: {},
      backend: "",
      message: samples.find((sample) => sample.message)?.message || "서버측 모니터링 데이터가 없습니다.",
    };
  }
  const metrics = {};
  for (const sample of usable) {
    for (const [key, value] of Object.entries(sample.metrics)) {
      const entry = metrics[key] || (metrics[key] = { count: 0, sum: 0, peak: -Infinity, last: 0 });
      entry.count += 1;
      entry.sum += value;
      entry.peak = Math.max(entry.peak, value);
      entry.last = value;
    }
  }
  for (const entry of Object.values(metrics)) entry.mean = entry.sum / entry.count;
  return {
    available: true,
    metrics,
    backend: usable.find((sample) => sample.backend)?.backend || "",
    samples: usable.length,
    message: samples.find((sample) => sample.status === "partial" && sample.message)?.message || "",
  };
}

export const hasMetric = (summary, key) => Boolean(summary?.metrics?.[key]);
export const metricPeak = (summary, key) => summary?.metrics?.[key]?.peak;
export const metricMean = (summary, key) => summary?.metrics?.[key]?.mean;

// gpuBoundLabel classifies what the GPU was limited by. GPU utilization alone is
// misleading: it reads ~100% at batch size 1 because it only asks whether any
// kernel ran. DRAM activity is what saturates during decode.
export function gpuBoundLabel(summary) {
  const dram = metricMean(summary, metricKeys.dramActive);
  const tensor = metricMean(summary, metricKeys.tensorActive);
  if (dram == null) return null;
  if (dram >= 0.6 && (tensor == null || tensor < dram))
    return { bound: "memory", text: "메모리 대역폭 바운드 — 정상적인 decode 상태입니다. GPU 증설보다 배치·양자화·KV 설정이 효과적입니다." };
  if (tensor != null && tensor >= 0.5)
    return { bound: "compute", text: "연산(텐서 코어) 바운드 — prefill이 우세합니다. 입력 길이 분포와 chunked prefill 예산을 확인하세요." };
  return { bound: "underused", text: "GPU가 충분히 쓰이지 않았습니다. 동시 사용자를 늘려 배치를 키울 여지가 있습니다." };
}
