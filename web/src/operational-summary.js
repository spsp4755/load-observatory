import { completionShortfall } from "./results.js";

export function operationalSummary(run) {
  if (!run || run.status !== "completed") {
    return { status: "pending", cause: "pending", headline: "실행 완료 후 운영 판단을 제공합니다.", nextAction: "테스트가 완료되면 결과를 다시 확인하세요." };
  }
  const result = run.result || {};
  const config = run.config || {};
  const total = result.total || (result.successes || 0) + (result.failures || 0);
  const errorRate = total ? (result.failures || 0) * 100 / total : 100;
  const p95 = result.latency?.p95_millis ?? result.p95_millis ?? 0;
  const ttft = result.ttft?.p95_millis || 0;
  const tpot = result.tpot?.p95_millis || 0;
  const errorLimit = config.max_error_percent ?? 2;
  const p95Limit = config.max_p95_millis ?? 2000;
  const load = config.mode === "rps" ? `${config.rps} RPS` : `${config.vus} VU`;
  if (!total) return { status: "at-risk", cause: "no-data", headline: "판단할 완료 요청이 없습니다.", nextAction: "대상 연결과 실행 시간을 확인한 뒤 다시 실행하세요." };
  const shortfall = completionShortfall(run);
  if (shortfall)
    return {
      status: "at-risk",
      cause: "incomplete",
      headline: shortfall.message,
      nextAction: `이 실행은 ${load} 수용 가능을 증명하지 못합니다. 응답 1건이 끝날 만큼 실행 시간과 종료 유예를 늘려 다시 측정하세요.`,
    };
  if (result.dropped_arrivals > 0) return { status: "at-risk", cause: "generator", headline: "부하 발생기 한계로 요청 일부가 시작되지 않았습니다.", nextAction: "최대 진행 요청을 높이거나 Agent Pod를 늘린 뒤 다시 측정하세요." };
  if (errorRate > errorLimit) return { status: "at-risk", cause: "errors", headline: `오류율 ${errorRate.toFixed(1)}%가 기준 ${errorLimit}%를 넘었습니다.`, nextAction: "현재 부하를 운영 한계로 보고, 오류 응답과 모델 서버 큐를 확인하세요." };
  if (p95 > p95Limit) return { status: "at-risk", cause: "latency", headline: `E2E P95 ${p95} ms가 기준 ${p95Limit} ms를 넘었습니다.`, nextAction: "현재 부하를 운영 한계로 보고, TTFT와 GPU 메모리 사용량을 함께 확인하세요." };
  if (config.max_ttft_p95_millis > 0 && ttft > config.max_ttft_p95_millis) return { status: "at-risk", cause: "ttft", headline: `TTFT P95 ${ttft} ms가 기준을 넘었습니다.`, nextAction: "모델 대기열과 프리필 병목을 확인한 뒤 동시 사용자를 낮추세요." };
  if (config.max_tpot_p95_millis > 0 && tpot > config.max_tpot_p95_millis) return { status: "at-risk", cause: "tpot", headline: `TPOT P95 ${tpot} ms가 기준을 넘었습니다.`, nextAction: "긴 출력 단계의 최대 토큰과 동시 사용자를 조정하세요." };
  const nextLoad = config.mode === "rps" ? Math.ceil((config.rps || 1) * 1.5) : Math.ceil((config.vus || 1) * 1.5);
  const unit = config.mode === "rps" ? "RPS" : "VU";
  return { status: "stable", cause: "none", headline: `${load}에서 설정한 SLO를 충족했습니다.`, nextAction: `다음 검증은 ${nextLoad} ${unit}로 올려 한계를 확인하세요.` };
}
