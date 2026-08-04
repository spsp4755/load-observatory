const simplePrompt = "Answer the user's question concisely and accurately.";

export function recommendWorkload(kind, form) {
  if (kind === "rag") {
    const prompt = "Use the retrieved documents below to answer accurately.\n\nRetrieved context:\n- API gateway retries idempotent requests up to three times.\n- Writes require an idempotency key and return 409 for duplicate keys.\n- The service-level objective is P95 under 800 ms.\n\nQuestion: explain the safe retry strategy with an implementation example.";
    return { ...form, journeys: [], agentWorkflow: false, mode: "vu", prompt, maxTokens: "8192", warmupRequests: form.warmupRequests || "3", scenario: [{ name: "RAG 질의", prompt, weight: 1, max_tokens: 8192, think_time_millis: 1200 }] };
  }
  if (kind === "long-agent") {
    return { ...form, journeys: [], agentWorkflow: true, mode: "vu", warmupRequests: form.warmupRequests || "3", scenario: [
      { name: "탐색", prompt: "Tool result: repository search returned 12 relevant files. Identify the implementation path.", weight: 1, max_tokens: 16384, think_time_millis: 1200 },
      { name: "계획", prompt: "Tool result: dependency graph and existing tests are available. Produce a safe migration plan.", weight: 1, max_tokens: 32768, think_time_millis: 1600 },
      { name: "구현", prompt: "Tool result: target files are open. Implement the requested feature with validation and tests.", weight: 1, max_tokens: 131072, think_time_millis: 2200 },
      { name: "테스트", prompt: "Tool result: test suite failed with two validation errors. Diagnose and patch the code.", weight: 1, max_tokens: 65536, think_time_millis: 1800 },
      { name: "재검토", prompt: "Tool result: tests pass. Review the diff for edge cases, security, and operational risk.", weight: 1, max_tokens: 32768, think_time_millis: 3000 },
      { name: "마무리", prompt: "Tool result: code review comments are resolved. Summarize the completed work and remaining risk.", weight: 1, max_tokens: 16384, think_time_millis: 3000 },
    ] };
  }
  if (kind === "mixed") {
    const scenario = [
      { name: "간단 질의", prompt: "Answer the user's question concisely and accurately.", weight: 60, max_tokens: 1280, think_time_millis: 700 },
      { name: "RAG 질의", prompt: "Use the retrieved documents below to answer accurately.\n\nRetrieved context: API writes require an idempotency key and return 409 for duplicates.\n\nQuestion: explain a safe retry strategy.", weight: 25, max_tokens: 8192, think_time_millis: 1200 },
      { name: "개발 작업", prompt: "Tool result: relevant files and a failing validation test were found. Diagnose the issue and propose a production-ready Go change with tests.", weight: 15, max_tokens: 32768, think_time_millis: 2200 },
    ];
    return { ...form, agentWorkflow: false, mode: "vu", warmupRequests: form.warmupRequests || "3", scenario, journeys: [
      { name: "간단 질의", weight: 60, scenario: [scenario[0]] },
      { name: "RAG 질의", weight: 25, scenario: [scenario[1]] },
      { name: "개발 에이전트", weight: 15, agent_workflow: true, scenario: [
        { name: "파일 검색", prompt: "Tool result: repository search returned relevant files. Identify the implementation path.", weight: 1, max_tokens: 8192, think_time_millis: 800 },
        { name: "코드 수정", prompt: "Tool result: validation is missing. Implement a production-ready Go change with tests.", weight: 1, max_tokens: 65536, think_time_millis: 1200 },
        { name: "테스트", prompt: "Tool result: a validation test failed. Diagnose and patch the code.", weight: 1, max_tokens: 32768, think_time_millis: 1500 },
      ] },
    ] };
  }
  if (kind === "agent") {
    return { ...form, journeys: [], agentWorkflow: true, mode: "vu", warmupRequests: form.warmupRequests || "3", scenario: [
      { name: "파일 검색", prompt: "도구 결과 — 파일 검색:\ninternal/api/handler.go\ninternal/api/handler_test.go\n\n관련 파일을 읽고 수정 계획을 작성하세요.", weight: 1, max_tokens: 8192, think_time_millis: 800 },
      { name: "코드 수정", prompt: "도구 결과 — 코드 변경 준비:\n입력 검증 누락을 찾았습니다. production-ready Go 수정안을 작성하세요.", weight: 1, max_tokens: 65536, think_time_millis: 1200 },
      { name: "테스트", prompt: "도구 결과 — 테스트 실행:\nFAIL TestCreateHandlerMissingBody: expected 400, got 500\n\n실패 원인을 분석하고 수정하세요.", weight: 1, max_tokens: 32768, think_time_millis: 1500 },
      { name: "검토", prompt: "도구 결과 — 수정 diff와 재실행 테스트가 제공되었습니다. 배포 전 위험 요소를 검토하세요.", weight: 1, max_tokens: 16384, think_time_millis: 2500 },
    ] };
  }
  return { ...form, journeys: [], agentWorkflow: false, mode: "vu", prompt: simplePrompt, maxTokens: "1280", warmupRequests: form.warmupRequests || "3", scenario: [{ name: "간단 질의", prompt: simplePrompt, weight: 1, max_tokens: 1280, think_time_millis: 500 }] };
}
