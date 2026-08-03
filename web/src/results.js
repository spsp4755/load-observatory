export function getVerdict(run) {
  if (!run || run.status !== "completed") return { status: "pending", recommendation: "실행 완료 후 안정성 판정을 제공합니다." };
  const result = run.result || {};
  const total = result.total || (result.successes || 0) + (result.failures || 0);
  if (!total) return { status: "at-risk", recommendation: "완료된 요청이 없어 결과를 신뢰할 수 없습니다." };
  const errorRate = (result.failures || 0) / total * 100;
  const p95 = result.latency?.p95_millis ?? result.p95_millis ?? 0;
  const errorLimit = run.config?.max_error_percent ?? 2;
  const p95Limit = run.config?.max_p95_millis ?? 2000;
  if (errorRate > errorLimit || p95 > p95Limit) return { status: "at-risk", recommendation: "현재 부하는 위험 구간입니다. 동시 사용자 또는 요청률을 낮춰 재측정하세요." };
  const load = run.config?.mode === "rps" ? `${run.config.rps} RPS` : `${run.config?.vus || 0} VU`;
  return { status: "stable", recommendation: `${load}까지는 현재 기준에서 안정적입니다. 더 높은 단계로 재시험해 한계를 확인하세요.` };
}

export const rate = (part, total) => total ? `${(part / total * 100).toFixed(1)}%` : "—";
export const milliseconds = (value) => value == null ? "—" : `${value} ms`;
