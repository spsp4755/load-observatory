// A long generation takes minutes, not seconds. A 60-second run against a
// 20,480-token budget cannot finish even one response, so it measures the onset
// of contention rather than the capacity it claims to. These helpers turn the
// configured token budget into the measurement window it actually needs.

// ponytail: a single assumed generation rate stands in for the real hardware.
// It is a form field, not a constant, because a real GPU never matches paper.
export const defaultTokensPerSecond = 110;

export function estimateWorkload({
  outputBudget = 0,
  tokensPerSecond = defaultTokensPerSecond,
  durationSeconds = 0,
  steadyStateSeconds = 0,
  drainSeconds = 0,
  callsPerUser = 1,
} = {}) {
  const rate = Number(tokensPerSecond) > 0 ? Number(tokensPerSecond) : defaultTokensPerSecond;
  const budget = Math.max(0, Number(outputBudget) || 0);
  const calls = Math.max(1, Number(callsPerUser) || 1);
  const duration = Math.max(0, Number(durationSeconds) || 0);
  const steady = Math.max(0, Number(steadyStateSeconds) || 0);
  const drain = Math.max(0, Number(drainSeconds) || 0);

  const secondsPerRequest = budget / rate;
  const secondsPerUserCycle = secondsPerRequest * calls;
  const steadyWindowSeconds = Math.max(0, duration - steady);
  const cyclesInSteadyWindow = secondsPerUserCycle > 0 ? steadyWindowSeconds / secondsPerUserCycle : Infinity;

  // One full cycle to reach steady state, then at least two more to measure it.
  const recommendedSteadyStateSeconds = Math.ceil(secondsPerUserCycle);
  const recommendedDurationSeconds = Math.max(60, Math.ceil(secondsPerUserCycle * 3));
  const recommendedDrainSeconds = Math.min(600, Math.ceil(secondsPerUserCycle * 1.5));

  const estimate = {
    secondsPerRequest,
    secondsPerUserCycle,
    steadyWindowSeconds,
    cyclesInSteadyWindow,
    recommendedSteadyStateSeconds,
    recommendedDurationSeconds,
    recommendedDrainSeconds,
    drainCoversResponse: drain >= secondsPerUserCycle,
  };

  // The controller rejects this outright, so say why before the request is sent.
  if (duration > 0 && steady >= duration) {
    return {
      ...estimate,
      level: "error",
      invalid: true,
      message: `측정 시작 지점 ${formatSeconds(steady)}이 실행 시간 ${formatSeconds(duration)}보다 늦거나 같아 측정할 구간이 남지 않습니다. 실행 시간을 늘리거나 측정 시작 지점을 앞으로 당기세요.`,
    };
  }
  if (!budget || !Number.isFinite(cyclesInSteadyWindow)) {
    return { ...estimate, level: "ok", message: "" };
  }
  if (cyclesInSteadyWindow < 1) {
    return {
      ...estimate,
      level: "error",
      message: `출력 ${budget.toLocaleString()} 토큰은 초당 ${rate} 토큰 기준 한 번에 약 ${formatSeconds(secondsPerUserCycle)} 걸립니다. 현재 측정 구간 ${formatSeconds(steadyWindowSeconds)}에는 응답 한 건도 끝나지 않아 이 결과로는 수용 가능한 사용자 수를 판정할 수 없습니다. 실행 시간을 ${formatSeconds(recommendedDurationSeconds)} 이상으로 올리세요.`,
    };
  }
  if (cyclesInSteadyWindow < 2) {
    return {
      ...estimate,
      level: "warn",
      message: `측정 구간에 응답이 약 ${cyclesInSteadyWindow.toFixed(1)}회만 들어갑니다. 사용자당 약 ${formatSeconds(secondsPerUserCycle)}가 필요하므로, 안정 용량을 판정하려면 실행 시간을 ${formatSeconds(recommendedDurationSeconds)} 이상으로 두세요.`,
    };
  }
  if (!estimate.drainCoversResponse) {
    return {
      ...estimate,
      level: "warn",
      message: `종료 유예가 ${formatSeconds(drain)}로 응답 1건(약 ${formatSeconds(secondsPerUserCycle)})보다 짧습니다. 종료 시점에 진행 중인 요청이 취소로 집계됩니다. 유예를 ${formatSeconds(recommendedDrainSeconds)} 이상으로 두세요.`,
    };
  }
  return { ...estimate, level: "ok", message: `측정 구간에 응답이 약 ${cyclesInSteadyWindow.toFixed(1)}회 들어갑니다.` };
}

// workloadShape derives the output budget and call count one virtual user gets
// through per cycle, which is what turns a token budget into a time estimate.
export function workloadShape(form = {}) {
  const scenario = form.scenario || [];
  const journeys = form.journeys || [];
  const tasks = [...scenario, ...journeys.flatMap((journey) => journey.scenario || [])];
  const limits = tasks.map((task) => Number(task.max_tokens || 0)).filter((value) => value > 0);
  if (form.agentWorkflow && scenario.length) {
    return {
      callsPerUser: scenario.length,
      outputBudget: scenario.reduce((total, task) => total + Number(task.max_tokens || form.maxTokens || 0), 0),
    };
  }
  return { callsPerUser: 1, outputBudget: limits.length ? Math.max(...limits) : Number(form.maxTokens || 0) };
}

export function formatSeconds(value) {
  const seconds = Math.max(0, Math.round(Number(value) || 0));
  if (seconds < 60) return `${seconds}초`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return rest ? `${minutes}분 ${rest}초` : `${minutes}분`;
}
