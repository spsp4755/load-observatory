const phases = ["저장소 탐색", "관련 파일 읽기", "수정 계획", "코드 구현", "테스트 실행", "실패 수정", "코드 검토", "마무리"];
const prompts = [
  "저장소 구조와 관련 파일을 검색하고, 구현 위치와 의존 관계를 파악하세요.",
  "검색된 파일과 테스트를 읽고 현재 동작, 입력 검증, 오류 처리 경로를 요약하세요.",
  "요구사항을 충족하는 변경 계획을 세우고 수정할 파일과 검증 항목을 정하세요.",
  "앞선 조사와 계획을 바탕으로 production-ready 코드를 구현하세요.",
  "관련 단위·통합 테스트를 실행한 결과를 분석하고 누락된 검증을 추가하세요.",
  "실패한 테스트와 로그를 근거로 원인을 찾고 최소한의 안전한 수정안을 적용하세요.",
  "변경 diff를 검토해 회귀, 보안, 동시성, 오류 처리 문제를 확인하세요.",
  "전체 작업을 최종 검증하고 변경 사항, 테스트 결과, 남은 위험을 정리하세요.",
];

const clamp = (value, min, max) => Math.max(min, Math.min(max, value));

export function captureToWorkload(capture, current = {}, settings = {}) {
  const events = capture?.events || [];
  if (!events.length) throw new Error("재생할 캡처 이벤트가 없습니다.");
  const scenario = events.map((event, index) => {
    const phase = Math.min(phases.length - 1, Math.floor(index * phases.length / events.length));
    const next = events[index + 1];
    const thinkScale = Number(settings.replay_think_time_scale) || 1;
    const think = next ? clamp(Math.round((next.timestamp_ms - event.timestamp_ms - (event.latency_ms || 0)) * thinkScale), 0, 60000) : 0;
    return {
      name: `${index + 1}. ${phases[phase]}`,
      prompt: prompts[phase],
      weight: 1,
      prompt_tokens: clamp(event.prompt_tokens || 1, 1, 1000000),
      max_tokens: clamp(event.max_tokens || event.output_tokens || Number(current.maxTokens) || 4096, 1, 1000000),
      think_time_millis: think,
    };
  });
  const thinkScale = Number(settings.replay_think_time_scale) || 1;
  const spanSeconds = Math.ceil(((events.at(-1)?.timestamp_ms || 0) * thinkScale + (events.at(-1)?.latency_ms || 0)) / 1000);
  const bufferSeconds = Number(settings.replay_buffer_seconds) || 300;
  return {
    ...current,
    mode: "vu",
    agentWorkflow: true,
    accumulateContext: true,
    sessionsPerVU: "1",
    scenario,
    journeys: [],
    trace: [],
    stages: [],
    vus: String(Number(settings.default_replay_vus) || Number(current.vus) || 30),
    duration: String(clamp(Math.max(60, spanSeconds + bufferSeconds), 1, 3600)),
    steadyStateSeconds: "0",
    drainSeconds: String(clamp(Number(settings.replay_drain_seconds) || 0, 0, 600)),
    maxTokens: String(Math.max(...scenario.map((step) => step.max_tokens))),
    prompt: scenario[0].prompt,
  };
}

export function captureStats(capture) {
  const events = capture?.events || [];
  return {
    calls: events.length,
    durationSeconds: Math.ceil(((events.at(-1)?.timestamp_ms || 0) + (events.at(-1)?.latency_ms || 0)) / 1000),
    maxPromptTokens: Math.max(0, ...events.map((event) => event.prompt_tokens || 0)),
    maxOutputTokens: Math.max(0, ...events.map((event) => event.output_tokens || 0)),
  };
}
