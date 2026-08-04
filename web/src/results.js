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

export function summarizeMonitoring(samples = []) {
  const available = samples.filter((sample) => sample.status === "collected");
  if (!available.length) return { available: false, gpu: 0, gpuMemory: 0, cpu: 0, memory: 0, message: samples.find((sample) => sample.message)?.message || "모니터링 데이터가 없습니다." };
  return {
    available: true,
    gpu: Math.max(...available.map((sample) => sample.gpu_utilization || 0)),
    gpuMemory: Math.max(...available.map((sample) => sample.gpu_memory_used || 0)),
    cpu: Math.max(...available.map((sample) => sample.cpu_utilization || 0)),
    memory: Math.max(...available.map((sample) => sample.memory_used || 0)),
    message: "",
  };
}
