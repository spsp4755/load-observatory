export const presets = {
  short: { prompt: "Answer in one short paragraph.", maxTokens: "1280" },
  coding: { prompt: "Implement a production-ready Go HTTP endpoint with validation and tests.", maxTokens: "20480" },
  long: { prompt: "Provide a detailed implementation plan with code examples and edge cases.", maxTokens: "163840" },
};
export const policyText = (run) => run.config?.cache_policy === "bypass" ? "캐시 우회" : run.config?.cache_policy === "reuse" ? "캐시 활용" : `혼합 (${run.config?.variation_percent ?? 30}% 변형)`;

export function filterRuns(runs, { query = "", status = "all", verdict = "all" }) {
  const text = query.toLowerCase();
  return runs.filter((run) => {
    const result = run.result || {}; const total = result.total || (result.successes || 0) + (result.failures || 0); const rate = total ? (result.failures || 0) / total * 100 : 100;
    const stable = run.status === "completed" && total > 0 && rate <= (run.config.max_error_percent ?? 2) && (result.latency?.p95_millis ?? result.p95_millis ?? 0) <= (run.config.max_p95_millis ?? 2000);
    return (!text || `${run.id} ${run.config.target_id} ${run.search_id || ""}`.toLowerCase().includes(text)) && (status === "all" || run.status === status) && (verdict === "all" || (verdict === "stable" ? stable : !stable));
  });
}

export function sortRuns(runs, sort) {
  const copy = [...runs];
  const total = (run) => run.result?.total || (run.result?.successes || 0) + (run.result?.failures || 0);
  if (sort === "p95") return copy.sort((a, b) => (b.result?.latency?.p95_millis ?? b.result?.p95_millis ?? 0) - (a.result?.latency?.p95_millis ?? a.result?.p95_millis ?? 0));
  if (sort === "errors") return copy.sort((a, b) => ((b.result?.failures || 0) / (total(b) || 1)) - ((a.result?.failures || 0) / (total(a) || 1)));
  if (sort === "throughput") return copy.sort((a, b) => (b.result?.throughput_rps || 0) - (a.result?.throughput_rps || 0));
  return copy.reverse();
}

export function compareRuns(left, right) {
  const value = (run, key) => key === "load" ? (run.config.mode === "rps" ? run.config.rps : run.config.vus) : key === "p95" ? (run.result?.latency?.p95_millis ?? run.result?.p95_millis ?? 0) : key === "output_per_second" ? (run.result?.tokens?.output_per_second || 0) : run.result?.[key] || 0;
  return ["load", "successes", "failures", "throughput_rps", "output_per_second", "p95"].map((key) => ({ key, left: value(left, key), right: value(right, key), delta: value(right, key) - value(left, key) }));
}
