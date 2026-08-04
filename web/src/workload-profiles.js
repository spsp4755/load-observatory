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
      { name: "간단 질의", prompt: "우리 팀의 사내 Git 서비스에서 개인 액세스 토큰을 교체하려고 합니다. 기존 토큰을 바로 삭제해도 되는지, 안전한 교체 순서를 5줄 이내로 알려주세요.", weight: 60, max_tokens: 1280, think_time_millis: 700 },
      { name: "RAG 질의", prompt: "아래 운영 문서를 근거로 장애 대응 절차를 정리해 주세요.\n\n[검색 결과: api-gateway/runbook.md]\n- 쓰기 요청은 Idempotency-Key가 있어야 재시도가 안전하다.\n- Gateway는 네트워크 오류에 한해 최대 3회 지수 백오프 재시도한다.\n- 같은 키의 중복 쓰기는 409를 반환한다.\n- 고객 영향 알림 기준은 5분간 오류율 2% 초과다.\n\n질문: 결제 생성 API 호출이 타임아웃됐을 때 클라이언트와 서버가 각각 어떤 순서로 대응해야 하나요?", weight: 25, max_tokens: 8192, think_time_millis: 1200 },
      { name: "개발 작업", prompt: "Go 서비스에서 POST /v1/projects 요청이 빈 name 값을 받으면 500을 반환합니다. 아래 조사 결과를 바탕으로 수정 계획과 핵심 코드 변경을 제안해 주세요.\n\n[도구 결과: rg -n \"CreateProject\" internal/]\ninternal/api/projects.go:42: func CreateProject(...)\ninternal/api/projects_test.go:88: TestCreateProjectMissingName\n\n[테스트 결과]\nFAIL TestCreateProjectMissingName: expected 400, got 500\n\n요구사항: 기존 성공 응답은 바꾸지 말고, 입력 검증과 회귀 테스트를 포함하세요.", weight: 15, max_tokens: 32768, think_time_millis: 2200 },
    ];
    return { ...form, agentWorkflow: false, mode: "vu", warmupRequests: form.warmupRequests || "3", scenario, journeys: [
      { name: "간단 질의", weight: 60, scenario: [scenario[0]] },
      { name: "RAG 질의", weight: 25, scenario: [scenario[1]] },
      { name: "개발 에이전트", weight: 15, agent_workflow: true, scenario: [
        { name: "파일 검색", prompt: "프로젝트 생성 API가 빈 name에서 500을 반환한다는 보고가 있습니다.\n\n[도구 결과: 파일 검색]\ninternal/api/projects.go\ninternal/api/projects_test.go\ninternal/core/validation.go\n\n관련 호출 흐름을 읽고, 변경해야 할 파일과 위험 요소를 정리하세요.", weight: 1, max_tokens: 8192, think_time_millis: 800 },
        { name: "코드 수정", prompt: "[도구 결과: 코드 일부]\nfunc CreateProject(req CreateProjectRequest) error {\n  project, err := service.Create(req.Name)\n  return writeJSON(project, err)\n}\n\n빈 name을 API 경계에서 400으로 처리하고, 기존 성공/중복 오류 동작을 유지하는 Go 패치를 작성하세요.", weight: 1, max_tokens: 65536, think_time_millis: 1200 },
        { name: "테스트", prompt: "[도구 결과: 테스트 실행]\nFAIL TestCreateProjectMissingName: expected 400, got 500\nPASS TestCreateProjectDuplicateName\n\n누락된 입력 검증 테스트와 수정 후 확인해야 할 회귀 테스트를 작성하고, 실패 원인을 설명하세요.", weight: 1, max_tokens: 32768, think_time_millis: 1500 },
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
