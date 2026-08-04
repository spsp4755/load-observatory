import React, { useEffect, useState } from "react";
import {
  cancelRun,
  cancelSearch,
  checkTarget,
  createRun,
  createSearch,
  createTarget,
  deleteTarget,
  getHealth,
  getRun,
  getSearch,
  listRuns,
  listTargets,
} from "./api.js";
import { compareRuns, policyText, presets } from "./record-utils.js";
import { loadSavedValue, saveValue } from "./form-storage.js";
import { toRunConfig, toSearchConfig } from "./run-form.js";
import {
  getVerdict,
  milliseconds,
  rate,
  summarizeMonitoring,
} from "./results.js";
import { recommendWorkload } from "./workload-profiles.js";
import { operationalSummary } from "./operational-summary.js";

const initial = loadSavedValue(window.localStorage, "load-observatory-form", {
  type: "model",
  url: "http://model.internal:8000/v1/chat/completions",
  model: "",
  prompt: presets.coding.prompt,
  maxTokens: presets.coding.maxTokens,
  cachePolicy: "mixed",
  variationPercent: "30",
  mode: "vu",
  vus: "10",
  rps: "100",
  duration: "60",
  maxErrorPercent: "2",
  maxP95Millis: "2000",
  maxTTFTP95Millis: "0",
  maxTTPOTP95Millis: "0",
  minOutputTokensPerSecond: "0",
  minGoodputPercent: "0",
  maxInFlight: "0",
  warmupRequests: "3",
  cooldownSeconds: "0",
  stages: [],
  scenario: [],
  startLoad: "5",
  maxLoad: "40",
});
const emptyProfile = {
  name: "",
  type: "model",
  url: "http://model.internal:8000/v1/chat/completions",
  model: "",
  apiKey: "",
};
const Metric = ({ label, value }) => (
  <section className="metric">
    <span>{label}</span>
    <strong>{value}</strong>
  </section>
);
const loadText = (run) =>
  run.config.mode === "rps" ? `${run.config.rps} RPS` : `${run.config.vus} VU`;
const agentWorkflowTemplate = [
  { name: "파일 검색", weight: 1, max_tokens: 1280, think_time_millis: 800, prompt: "개발 작업: HTTP 엔드포인트 오류를 조사하세요.\n\n도구 결과 — 파일 검색:\ninternal/api/handler.go\ninternal/api/handler_test.go\n\n관련 파일을 읽고 수정 계획을 작성하세요." },
  { name: "코드 수정", weight: 1, max_tokens: 20480, think_time_millis: 1200, prompt: "도구 결과 — 코드 변경 준비:\nhandler.go의 입력 검증 누락을 찾았습니다.\n\n앞선 조사 결과를 반영해 production-ready Go 수정안을 작성하세요." },
  { name: "테스트", weight: 1, max_tokens: 4096, think_time_millis: 1500, prompt: "도구 결과 — 테스트 실행:\nFAIL TestCreateHandlerMissingBody: expected 400, got 500\n\n실패 원인을 분석하고 최소 수정 및 테스트 보강안을 작성하세요." },
  { name: "검토", weight: 1, max_tokens: 4096, think_time_millis: 2500, prompt: "도구 결과 — 수정 diff와 재실행 테스트가 제공되었습니다.\n\n변경 사항을 검토하고 배포 전 위험 요소와 최종 요약을 작성하세요." },
];

function Details({ run, onCancel }) {
  if (!run)
    return (
      <section className="panel empty">
        실행 기록을 선택하면 상세 분석이 표시됩니다.
      </section>
    );
  const result = run.result || {};
  const total =
    result.total || (result.successes || 0) + (result.failures || 0);
  const latency = result.latency || {};
  const ttft = result.ttft || {};
  const ttfo = result.ttfo || {};
  const itl = result.itl || {};
  const tpot = result.tpot || {};
  const tokens = result.tokens || {};
  const verdict = getVerdict(run);
  const operational = operationalSummary(run);
  const monitoring = summarizeMonitoring(run.monitoring);
  const distribution = (values) => (
    <div className="distribution-values">
      {[
        ["최소", "min_millis"],
        ["평균", "avg_millis"],
        ["P50", "p50_millis"],
        ["P95", "p95_millis"],
        ["P99", "p99_millis"],
        ["최대", "max_millis"],
      ].map(([label, key]) => (
        <span key={key}>
          {label}
          <b>{milliseconds(values[key])}</b>
        </span>
      ))}
    </div>
  );
  return (
    <section className="details">
      <section className={`panel verdict ${verdict.status}`}>
        <div>
          <h3>
            {verdict.status === "stable"
              ? "안정 구간"
              : verdict.status === "at-risk"
                ? "위험 구간"
                : "판정 대기"}
          </h3>
          <p>{verdict.recommendation}</p>
        </div>
        <span>
          오류 기준 {run.config.max_error_percent ?? 2}% · P95 기준{" "}
          {milliseconds(run.config.max_p95_millis ?? 2000)}
        </span>
        {["queued", "running"].includes(run.status) && (
          <button className="danger" onClick={() => onCancel(run.id)}>
            실행 중지
          </button>
        )}
      </section>
      <section className={`panel operational-decision ${operational.status}`}>
        <div>
          <h3>운영 판단</h3>
          <strong>{operational.headline}</strong>
          <p>{operational.nextAction}</p>
        </div>
        <span className="decision-cause">판단 근거: {operational.cause}</span>
      </section>
      <section className="panel">
        <h3>실행 설정</h3>
        <div className="config-summary">
          <span>
            부하 <b>{loadText(run)}</b>
          </span>
          <span>
            시간 <b>{run.config.duration_seconds}초</b>
          </span>
          <span>
            최대 출력 <b>{run.config.max_tokens} 토큰</b>
          </span>
          <span>
            캐시 <b>{policyText(run)}</b>
          </span>
        </div>
        <p className="prompt-preview">{run.config.prompt}</p>
      </section>
      <section className="panel">
        <h3>운영 요약</h3>
        <div className="metrics">
          <Metric label="총 요청" value={total || "—"} />
          <Metric label="성공률" value={rate(result.successes || 0, total)} />
          <Metric
            label="처리량"
            value={
              result.throughput_rps
                ? `${result.throughput_rps.toFixed(2)} RPS`
                : "—"
            }
          />
          <Metric label="오류율" value={rate(result.failures || 0, total)} />
          <Metric label="Goodput" value={result.goodput_percent == null ? "—" : `${result.goodput_percent.toFixed(1)}%`} />
          <Metric label="누락된 도착" value={result.dropped_arrivals ?? 0} />
          {result.agent_sessions > 0 && <><Metric label="에이전트 세션" value={result.agent_sessions} /><Metric label="세션 완료율" value={`${(result.completed_sessions / result.agent_sessions * 100).toFixed(1)}%`} /></>}
        </div>
      </section>
      <section className="panel">
        <h3>지연 분석</h3>
        {result.latency_scope === "worst_shard_p95" && (
          <p className="muted">
            분산 실행의 P95는 각 샤드 P95 중 가장 높은 값입니다. 안정성 판정을
            위한 보수적 기준입니다.
          </p>
        )}
        <div className="distribution">
          <div>
            <h4>응답 시간</h4>
            {distribution(latency)}
          </div>
          <div>
            <h4>첫 토큰 도착 (TTFT)</h4>
            {distribution(ttft)}
          </div>
          <div>
            <h4>최종 출력 첫 토큰 (TTFO)</h4>
            {distribution(ttfo)}
          </div>
          <div>
            <h4>토큰 간 지연 (ITL)</h4>
            {distribution(itl)}
          </div>
          <div>
            <h4>출력 토큰당 시간 (TPOT)</h4>
            {distribution(tpot)}
          </div>
        </div>
      </section>
      {tokens.completion > 0 && (
        <section className="panel">
          <h3>모델 출력 분석</h3>
          <div className="metrics">
            <Metric label="입력 토큰" value={tokens.prompt} />
            <Metric label="출력 토큰" value={tokens.completion} />
            <Metric label="추론 토큰" value={tokens.reasoning || "—"} />
            <Metric
              label="출력 속도"
              value={`${tokens.output_per_second?.toFixed(1) || 0} tok/s`}
            />
          </div>
        </section>
      )}
      <section className="panel">
        <h3>인프라 모니터링</h3>
        {monitoring.available ? (
          <div className="metrics">
            <Metric
              label="GPU 사용률 (최대)"
              value={`${monitoring.gpu.toFixed(1)}%`}
            />
            <Metric
              label="GPU 메모리 (최대)"
              value={`${monitoring.gpuMemory.toFixed(1)}%`}
            />
            <Metric
              label="CPU 사용률 (최대)"
              value={`${monitoring.cpu.toFixed(1)}%`}
            />
            <Metric
              label="메모리 사용률 (최대)"
              value={`${monitoring.memory.toFixed(1)}%`}
            />
          </div>
        ) : (
          <p className="muted">수집 불가: {monitoring.message}</p>
        )}
      </section>
      <section className="panel">
        <h3>시간별 추세</h3>
        <table>
          <thead>
            <tr>
              <th>경과</th>
              <th>요청</th>
              <th>성공</th>
              <th>실패</th>
              <th>P95</th>
            </tr>
          </thead>
          <tbody>
            {(result.timeline || []).length ? (
              result.timeline.map((point) => (
                <tr key={point.second}>
                  <td>{point.second + 1}초</td>
                  <td>{point.requests}</td>
                  <td>{point.successes}</td>
                  <td>{point.failures}</td>
                  <td>{milliseconds(point.p95_millis)}</td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan="5">추세 데이터가 없습니다.</td>
              </tr>
            )}
          </tbody>
        </table>
      </section>
      <section className="split">
        <section className="panel">
          <h3>응답 상태</h3>
          <table>
            <tbody>
              {Object.entries(result.status_counts || {}).length ? (
                Object.entries(result.status_counts).map(([status, count]) => (
                  <tr key={status}>
                    <td>HTTP {status}</td>
                    <td>{count}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td>데이터 없음</td>
                </tr>
              )}
            </tbody>
          </table>
        </section>
        <section className="panel">
          <h3>최근 오류</h3>
          {(result.errors || []).length ? (
            <ul className="errors">
              {result.errors.map((item, index) => (
                <li key={`${item}-${index}`}>{item}</li>
              ))}
            </ul>
          ) : (
            <p className="muted">오류가 없습니다.</p>
          )}
        </section>
      </section>
    </section>
  );
}

function ScenarioControls({ form, setForm }) {
  const applyAgentWorkflow = () => setForm((current) => recommendWorkload("agent", current));
  const applySimpleProfile = () => setForm((current) => recommendWorkload("simple", current));
  const applyRAGProfile = () => setForm((current) => recommendWorkload("rag", current));
  const applyLongAgentProfile = () => setForm((current) => recommendWorkload("long-agent", current));
  const applyMixedProfile = () => setForm((current) => recommendWorkload("mixed", current));
  const addScenario = () =>
    setForm((current) => ({
      ...current,
      scenario: [
        ...(current.scenario || []),
        {
          name: `작업 ${(current.scenario || []).length + 1}`,
          prompt: current.prompt,
          weight: 1,
          think_time_millis: 0,
        },
      ],
    }));
  const updateScenario = (index, key, value) =>
    setForm((current) => ({
      ...current,
      scenario: current.scenario.map((task, taskIndex) =>
        taskIndex === index
          ? {
              ...task,
              [key]: key === "name" || key === "prompt" ? value : Number(value),
            }
          : task,
      ),
    }));
  const addStage = () =>
    setForm((current) => ({
      ...current,
      stages: [
        ...(current.stages || []),
        {
          duration_seconds: 30,
          target_load: Number(
            current.mode === "rps" ? current.rps : current.vus,
          ),
        },
      ],
    }));
  const updateStage = (index, key, value) =>
    setForm((current) => ({
      ...current,
      stages: current.stages.map((stage, stageIndex) =>
        stageIndex === index ? { ...stage, [key]: Number(value) } : stage,
      ),
    }));
  const scenario = form.scenario || [];
  const journeys = form.journeys || [];
  const configuredTokenLimits = [...scenario, ...journeys.flatMap((journey) => journey.scenario || [])]
    .map((task) => Number(task.max_tokens || 0))
    .filter((value) => value > 0);
  const callsPerUser = form.agentWorkflow ? scenario.length : 1;
  const outputBudget = form.agentWorkflow
    ? scenario.reduce((total, task) => total + Number(task.max_tokens || form.maxTokens || 0), 0)
    : configuredTokenLimits.length ? Math.max(...configuredTokenLimits) : Number(form.maxTokens || 0);
  const workloadName = form.agentWorkflow ? "순차 에이전트 세션" : scenario.length > 1 ? "혼합 요청 사용자군" : "단일 질의 요청";
  const callsLabel = journeys.length ? "사용자군별 호출" : "사용자당 호출";
  const callsValue = journeys.length ? "단일 1회 / 에이전트 3회" : `${callsPerUser}회`;
  return (
    <section className="advanced panel">
      <section className="workload-plan" aria-live="polite">
        <div><span>현재 테스트 계획</span><strong>{workloadName}</strong></div>
        <div><span>{callsLabel}</span><strong>{callsValue}</strong></div>
        <div><span>{form.agentWorkflow ? "사용자당 최대 출력 예산" : "요청당 최대 출력 예산"}</span><strong>{outputBudget.toLocaleString()} 토큰</strong></div>
        <p>VU는 동시 사용자 수입니다. 혼합 사용자군은 가중치 비율로 요청을 섞고, 에이전트 워크로드는 각 단계를 순서대로 실행합니다.</p>
      </section>
      <h3>추천 워크로드</h3>
      <div className="workflow-grid">
      <div className="workflow-choice"><div><strong>간단 질의 사용자</strong><p>짧은 질문과 응답을 반복하는 일반 채팅 사용자 프로필입니다.</p></div><button type="button" className={!form.agentWorkflow ? "active" : "small"} onClick={applySimpleProfile}>간단 질의로 구성</button></div>
      <div className="workflow-choice"><div><strong>RAG 사용자</strong><p>긴 검색 컨텍스트를 읽고 근거 기반 응답을 만드는 사용자 프로필입니다.</p></div><button type="button" className="small" onClick={applyRAGProfile}>RAG로 구성</button></div>
      <div className="workflow-choice"><div><strong>개발 에이전트 세션</strong><p>파일 검색 · 코드 수정 · 테스트 · 검토를 하나의 사용자 세션으로 순차 실행합니다.</p></div><button type="button" className={form.agentWorkflow ? "active" : "small"} onClick={applyAgentWorkflow}>{form.agentWorkflow ? "개발 에이전트 적용됨" : "개발 에이전트로 구성"}</button></div>
      <div className="workflow-choice"><div><strong>장기 에이전트 작업</strong><p>탐색부터 구현·재검토까지 6단계의 긴 개발 작업을 재현합니다.</p></div><button type="button" className="small" onClick={applyLongAgentProfile}>장기 작업으로 구성</button></div>
      <div className="workflow-choice mixed"><div><strong>현실적 혼합 사용자군</strong><p>간단 질의 60% · RAG 25% · 개발 작업 15%를 하나의 부하 테스트에 섞습니다.</p></div><button type="button" className="small" onClick={applyMixedProfile}>혼합 사용자군으로 구성</button></div>
      </div>
      <details className="advanced-editor">
        <summary>고급 설정 직접 편집</summary>
        <p>안전 한계, 부하 단계, 단계별 프롬프트와 최대 출력 토큰을 조정합니다.</p>
        <div className="advanced-editor-content">
          <h3>현실적인 사용자 시나리오</h3>
      <div className="grid">
        <label>
          워밍업 요청
          <small>측정 시작 전 대상 연결과 캐시를 안정화하는 요청 수입니다. 결과에는 포함하지 않습니다.</small>
          <input
            type="number"
            min="0"
            max="1000"
            value={form.warmupRequests || "0"}
            onChange={(event) =>
              setForm((value) => ({
                ...value,
                warmupRequests: event.target.value,
              }))
            }
          />
        </label>
        <label>
          최대 진행 요청
          <small>RPS 테스트에서 동시에 보낼 수 있는 요청 상한입니다. 0은 기본 상한을 사용합니다.</small>
          <input
            type="number"
            min="0"
            max="500"
            value={form.maxInFlight || "0"}
            onChange={(event) =>
              setForm((value) => ({
                ...value,
                maxInFlight: event.target.value,
              }))
            }
          />
        </label>
        <label>
          TTFT P95 제한 (ms)
          <small>첫 토큰이 도착하기까지의 P95 허용 시간입니다. 0은 자동 중단 기준을 사용하지 않습니다.</small>
          <input
            type="number"
            min="0"
            value={form.maxTTFTP95Millis || "0"}
            onChange={(event) =>
              setForm((value) => ({
                ...value,
                maxTTFTP95Millis: event.target.value,
              }))
            }
          />
        </label>
        <label>
          TPOT P95 제한 (ms)
          <small>출력 토큰 하나당 걸리는 P95 허용 시간입니다. 긴 응답 품질을 판단할 때 사용합니다.</small>
          <input
            type="number"
            min="0"
            value={form.maxTTPOTP95Millis || "0"}
            onChange={(event) =>
              setForm((value) => ({
                ...value,
                maxTTPOTP95Millis: event.target.value,
              }))
            }
          />
        </label>
        <label>
          최소 Goodput (%)
          <small>지연과 출력 속도 기준을 모두 통과한 성공 요청의 최소 비율입니다. 0은 기준을 사용하지 않습니다.</small>
          <input
            type="number"
            min="0"
            max="100"
            value={form.minGoodputPercent || "0"}
            onChange={(event) =>
              setForm((value) => ({
                ...value,
                minGoodputPercent: event.target.value,
              }))
            }
          />
        </label>
      </div>
      <h4>단계형 부하</h4>
      <p className="advanced-help">각 단계는 지정한 시간 동안 목표 VU 또는 RPS로 실행됩니다. 예: 30초 10 VU → 60초 20 VU.</p>
      {(form.stages || []).map((stage, index) => (
        <div className="scenario-row" key={index}>
          <input
            aria-label={`stage duration ${index + 1}`}
            type="number"
            min="1"
            value={stage.duration_seconds}
            onChange={(event) =>
              updateStage(index, "duration_seconds", event.target.value)
            }
          />
          <span>초 · 목표</span>
          <input
            aria-label={`stage load ${index + 1}`}
            type="number"
            min="1"
            value={stage.target_load}
            onChange={(event) =>
              updateStage(index, "target_load", event.target.value)
            }
          />
          <button
            type="button"
            className="small danger"
            onClick={() =>
              setForm((current) => ({
                ...current,
                stages: current.stages.filter(
                  (_, stageIndex) => stageIndex !== index,
                ),
              }))
            }
          >
            삭제
          </button>
        </div>
      ))}
      <button type="button" className="small" onClick={addStage}>
        단계 추가
      </button>
      <h4>가중 시나리오</h4>
      <p className="advanced-help">가중치는 전체 요청 중 해당 시나리오의 비율입니다. 최대 토큰은 단계별 상한, 대기는 다음 요청 전 사용자 행동 시간을 뜻합니다.</p>
      <div className="scenario-columns" aria-hidden="true">
        <span><b>단계명</b><small>결과에 표시할 이름</small></span>
        <span><b>프롬프트</b><small>실제 사용자 요청 또는 도구 결과</small></span>
        <span><b>가중치</b><small>전체 요청 중 비율</small></span>
        <span><b>최대 토큰</b><small>이 요청의 출력 상한</small></span>
        <span><b>대기 (ms)</b><small>다음 요청까지 대기</small></span>
        <span />
      </div>
      <p className="scenario-mobile-help">입력 안내: 단계명은 결과 표시용, 프롬프트는 실제 입력, 가중치는 요청 비율, 최대 토큰은 출력 상한, 대기는 다음 요청 전 시간입니다.</p>
      {(form.scenario || []).map((task, index) => (
        <div className="scenario-row scenario-task" key={index}>
          <input
            aria-label={`scenario name ${index + 1}`}
            value={task.name}
            onChange={(event) =>
              updateScenario(index, "name", event.target.value)
            }
          />
          <input
            aria-label={`scenario prompt ${index + 1}`}
            value={task.prompt}
            onChange={(event) =>
              updateScenario(index, "prompt", event.target.value)
            }
          />
          <input
            aria-label={`scenario weight ${index + 1}`}
            type="number"
            min="1"
            value={task.weight}
            onChange={(event) =>
              updateScenario(index, "weight", event.target.value)
            }
          />
          <input
            aria-label={`scenario max tokens ${index + 1}`}
            title="단계별 최대 출력 토큰"
            placeholder="최대 토큰"
            type="number"
            min="1"
            max="1000000"
            value={task.max_tokens || form.maxTokens}
            onChange={(event) =>
              updateScenario(index, "max_tokens", event.target.value)
            }
          />
          <input
            aria-label={`scenario think time ${index + 1}`}
            type="number"
            min="0"
            value={task.think_time_millis}
            onChange={(event) =>
              updateScenario(index, "think_time_millis", event.target.value)
            }
          />
          <button
            type="button"
            className="small danger"
            onClick={() =>
              setForm((current) => ({
                ...current,
                scenario: current.scenario.filter(
                  (_, taskIndex) => taskIndex !== index,
                ),
              }))
            }
          >
            삭제
          </button>
        </div>
      ))}
      <button type="button" className="small" onClick={addScenario}>
        시나리오 추가
      </button>
        </div>
      </details>
    </section>
  );
}

function TestSettings({
  form,
  update,
  profiles,
  selectedProfileId,
  selectProfile,
}) {
  return (
    <div className="grid">
      <label className="wide">
        등록된 대상
        <select
          value={selectedProfileId}
          onChange={(event) => selectProfile(event.target.value)}
        >
          <option value="">새 대상 직접 입력</option>
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name} — {profile.model || profile.url}
            </option>
          ))}
        </select>
      </label>
      <label>
        대상 유형
        <select name="type" value={form.type} onChange={update}>
          <option value="model">GPU 모델 API</option>
          <option value="web">웹 API</option>
        </select>
      </label>
      <label className="wide">
        대상 URL
        <input name="url" value={form.url} onChange={update} required />
      </label>
      {form.type === "model" && (
        <>
          <label className="wide">
            모델명
            <input name="model" value={form.model} onChange={update} required />
          </label>
          <label>
            출력 최대 토큰
            <input
              name="maxTokens"
              type="number"
              min="1"
              max="1000000"
              value={form.maxTokens}
              onChange={update}
              required
            />
          </label>
          <label className="wide">
            프롬프트
            <textarea
              name="prompt"
              value={form.prompt}
              onChange={update}
              required
            />
          </label>
          <div className="preset-row">
            {Object.entries(presets).map(([key, preset]) => (
              <button
                type="button"
                className="preset"
                key={key}
                onClick={() => {
                  update({ target: { name: "prompt", value: preset.prompt } });
                  update({
                    target: { name: "maxTokens", value: preset.maxTokens },
                  });
                }}
              >
                {key === "short"
                  ? "짧은 응답"
                  : key === "coding"
                    ? "코딩 작업"
                    : "긴 출력"}
              </button>
            ))}
          </div>
        </>
      )}
      <label>
        캐시 정책
        <select name="cachePolicy" value={form.cachePolicy} onChange={update}>
          <option value="mixed">혼합: 반복 + 변형</option>
          <option value="reuse">캐시 활용: 반복 요청</option>
          <option value="bypass">캐시 우회: 모두 변형</option>
        </select>
      </label>
      {form.cachePolicy === "mixed" && (
        <label>
          변형 요청 비율 (%)
          <input
            name="variationPercent"
            type="number"
            min="0"
            max="100"
            value={form.variationPercent}
            onChange={update}
          />
        </label>
      )}
      <label>
        부하 방식
        <select name="mode" value={form.mode} onChange={update}>
          <option value="vu">동시 사용자 (VU)</option>
          <option value="rps">요청률 (RPS)</option>
        </select>
      </label>
      <label>
        {form.mode === "rps" ? "요청률" : "동시 사용자"}
        <input
          name={form.mode === "rps" ? "rps" : "vus"}
          type="number"
          min="1"
          value={form.mode === "rps" ? form.rps : form.vus}
          onChange={update}
        />
      </label>
      <label>
        실행 시간 (초)
        <input
          name="duration"
          type="number"
          min="1"
          value={form.duration}
          onChange={update}
        />
      </label>
      <label>
        허용 오류율 (%)
        <input
          name="maxErrorPercent"
          type="number"
          min="0"
          max="100"
          value={form.maxErrorPercent}
          onChange={update}
        />
      </label>
      <label>
        허용 P95 (ms)
        <input
          name="maxP95Millis"
          type="number"
          min="1"
          value={form.maxP95Millis}
          onChange={update}
        />
      </label>
    </div>
  );
}

function Profiles({ profiles, profile, update, save, remove, check }) {
  return (
    <>
      <section className="panel">
        <h3>모델/대상 등록</h3>
        <p className="muted">
          인증키는 저장 후 다시 표시되지 않습니다. 등록 대상을 선택하면 저장된
          인증키로 테스트합니다.
        </p>
        <div className="grid">
          <label>
            표시 이름
            <input
              name="name"
              value={profile.name}
              onChange={update}
              required
            />
          </label>
          <label>
            대상 유형
            <select name="type" value={profile.type} onChange={update}>
              <option value="model">GPU 모델 API</option>
              <option value="web">웹 API</option>
            </select>
          </label>
          <label className="wide">
            대상 URL
            <input name="url" value={profile.url} onChange={update} required />
          </label>
          {profile.type === "model" && (
            <label className="wide">
              모델명
              <input
                name="model"
                value={profile.model}
                onChange={update}
                required
              />
            </label>
          )}
          <label className="wide">
            인증키 (선택)
            <input
              name="apiKey"
              type="password"
              autoComplete="new-password"
              value={profile.apiKey}
              onChange={update}
            />
          </label>
        </div>
        <button onClick={save}>등록</button>
      </section>
      <section className="panel">
        <h3>등록된 대상</h3>
        {profiles.length === 0 ? (
          <p className="muted">등록된 대상이 없습니다.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>이름</th>
                <th>유형</th>
                <th>모델</th>
                <th>URL</th>
                <th>인증키</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {profiles.map((item) => (
                <tr key={item.id}>
                  <td>{item.name}</td>
                  <td>{item.type === "model" ? "모델" : "웹"}</td>
                  <td>{item.model || "-"}</td>
                  <td>{item.url}</td>
                  <td>{item.has_api_key ? "저장됨" : "없음"}</td>
                  <td>
                    <button className="small" onClick={() => check(item)}>
                      연결 확인
                    </button>
                    <button className="danger" onClick={() => remove(item)}>
                      삭제
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}

function Records({ runs, choose }) {
  return (
    <section className="panel">
      <h3>테스트 기록</h3>
      <table>
        <thead>
          <tr>
            <th>실행</th>
            <th>유형</th>
            <th>부하</th>
            <th>캐시</th>
            <th>성공률</th>
            <th>P95</th>
            <th>판정</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((item) => {
            const result = item.result || {};
            const total =
              result.total || (result.successes || 0) + (result.failures || 0);
            return (
              <tr
                className="selectable"
                key={item.id}
                onClick={() => choose(item)}
              >
                <td>{item.id}</td>
                <td>{item.search_id ? "자동 탐색" : "수동"}</td>
                <td>{loadText(item)}</td>
                <td>{policyText(item)}</td>
                <td>{rate(result.successes || 0, total)}</td>
                <td>
                  {milliseconds(
                    result.latency?.p95_millis ?? result.p95_millis,
                  )}
                </td>
                <td>{getVerdict(item).status}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
}

function Comparison({ runs }) {
  const completed = runs.filter((item) => item.status === "completed");
  const [leftID, setLeftID] = useState("");
  const [rightID, setRightID] = useState("");
  const left = completed.find((item) => item.id === leftID);
  const right = completed.find((item) => item.id === rightID);
  const labels = {
    load: "부하",
    successes: "성공",
    failures: "실패",
    throughput_rps: "처리량 (RPS)",
    output_per_second: "출력 속도 (tok/s)",
    p95: "P95 (ms)",
  };
  return (
    <section className="panel">
      <h3>실행 비교</h3>
      {completed.length < 2 ? (
        <p className="muted">완료된 실행이 2개 이상이면 비교할 수 있습니다.</p>
      ) : (
        <>
          <div className="grid">
            <label>
              기준 실행
              <select
                value={leftID}
                onChange={(event) => setLeftID(event.target.value)}
              >
                <option value="">선택</option>
                {completed.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.id} · {loadText(item)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              비교 실행
              <select
                value={rightID}
                onChange={(event) => setRightID(event.target.value)}
              >
                <option value="">선택</option>
                {completed.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.id} · {loadText(item)}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {left && right && (
            <table>
              <thead>
                <tr>
                  <th>지표</th>
                  <th>{left.id}</th>
                  <th>{right.id}</th>
                  <th>차이</th>
                </tr>
              </thead>
              <tbody>
                {compareRuns(left, right).map((row) => (
                  <tr key={row.key}>
                    <td>{labels[row.key]}</td>
                    <td>{row.left.toFixed(2)}</td>
                    <td>{row.right.toFixed(2)}</td>
                    <td>
                      {row.delta > 0 ? "+" : ""}
                      {row.delta.toFixed(2)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </section>
  );
}

export default function App() {
  const [page, setPage] = useState("test");
  const [mode, setMode] = useState("manual");
  const [form, setForm] = useState(initial);
  const [run, setRun] = useState(null);
  const [runs, setRuns] = useState([]);
  const [search, setSearch] = useState(null);
  const [health, setHealth] = useState(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [profiles, setProfiles] = useState([]);
  const [profile, setProfile] = useState(emptyProfile);
  const [selectedProfileId, setSelectedProfileId] = useState(() =>
    loadSavedValue(window.localStorage, "load-observatory-profile", ""),
  );
  const refreshProfiles = () =>
    listTargets()
      .then(setProfiles)
      .catch((err) => setError(err.message));
  const refresh = () =>
    listRuns()
      .then(setRuns)
      .catch((err) => setError(err.message));
  useEffect(() => {
    refreshProfiles();
  }, []);
  useEffect(() => {
    saveValue(window.localStorage, "load-observatory-form", form);
  }, [form]);
  useEffect(() => {
    saveValue(
      window.localStorage,
      "load-observatory-profile",
      selectedProfileId,
    );
  }, [selectedProfileId]);
  useEffect(() => {
    if (page === "records") refresh();
    if (page === "profiles") refreshProfiles();
  }, [page]);
  useEffect(() => {
    if (!run || ["completed", "cancelled"].includes(run.status)) return;
    const timer = setInterval(
      () =>
        getRun(run.id)
          .then(setRun)
          .catch((err) => setError(err.message)),
      1000,
    );
    return () => clearInterval(timer);
  }, [run]);
  useEffect(() => {
    if (!search || search.status !== "running") return;
    const timer = setInterval(
      () =>
        getSearch(search.id)
          .then(setSearch)
          .catch((err) => setError(err.message)),
      1000,
    );
    return () => clearInterval(timer);
  }, [search]);
  useEffect(() => {
    if (page === "monitor")
      getHealth()
        .then(setHealth)
        .catch((err) => setError(err.message));
  }, [page]);
  const update = (event) => {
    if (["type", "url", "model"].includes(event.target.name))
      setSelectedProfileId("");
    setForm((value) => ({ ...value, [event.target.name]: event.target.value }));
  };
  const updateProfile = (event) =>
    setProfile((value) => ({
      ...value,
      [event.target.name]: event.target.value,
    }));
  const selectProfile = (id) => {
    setSelectedProfileId(id);
    const item = profiles.find((value) => value.id === id);
    if (item)
      setForm((value) => ({
        ...value,
        type: item.type,
        url: item.url,
        model: item.model || "",
      }));
  };
  const saveProfile = async () => {
    try {
      if (
        !profile.name ||
        !profile.url ||
        (profile.type === "model" && !profile.model)
      )
        throw new Error("이름, URL, 모델명을 입력하세요.");
      await createTarget({
        name: profile.name,
        type: profile.type,
        url: profile.url,
        model: profile.model,
        api_key: profile.apiKey,
      });
      setProfile(emptyProfile);
      await refreshProfiles();
    } catch (err) {
      setError(err.message);
    }
  };
  const removeProfile = async (item) => {
    try {
      await deleteTarget(item.id);
      if (selectedProfileId === item.id) setSelectedProfileId("");
      await refreshProfiles();
    } catch (err) {
      setError(err.message);
    }
  };
  const checkProfile = async (item) => {
    try {
      const result = await checkTarget(item.id);
      setError("");
      setNotice(
        `${item.name}: HTTP ${result.status_code} · ${result.latency_millis} ms`,
      );
    } catch (err) {
      setNotice("");
      setError(err.message);
    }
  };
  const stopRun = async (id) => {
    try {
      const cancelled = await cancelRun(id);
      setRun(cancelled);
      refresh();
    } catch (err) {
      setError(err.message);
    }
  };
  const start = async () => {
    try {
      const targetId =
        selectedProfileId ||
        (
          await createTarget({
            name: form.type === "model" ? "Model API" : "Web endpoint",
            type: form.type,
            url: form.url,
            model: form.model,
          })
        ).id;
      if (mode === "automatic")
        setSearch(await createSearch(toSearchConfig({ ...form, targetId })));
      else setRun(await createRun(toRunConfig({ ...form, targetId })));
    } catch (err) {
      setError(err.message);
    }
  };
  const titles = {
    test: "부하 테스트",
    records: "테스트 기록",
    monitor: "모니터링",
    profiles: "모델 등록",
  };
  return (
    <main className="shell">
      <aside>
        <h1>Load Observatory</h1>
        <nav>
          {[
            ["test", "부하 테스트"],
            ["records", "테스트 기록"],
            ["monitor", "모니터링"],
            ["profiles", "모델 등록"],
          ].map(([id, label]) => (
            <button
              className={page === id ? "active" : ""}
              key={id}
              onClick={() => setPage(id)}
            >
              {label}
            </button>
          ))}
        </nav>
      </aside>
      <div className="content">
        <header>
          <div>
            <h2>{titles[page]}</h2>
            <p>캐시 상황을 반영한 실제 사용 패턴으로 한계를 측정합니다.</p>
          </div>
          <div className="header-status">
            {run && ["queued", "running"].includes(run.status) && (
              <button className="active-run" onClick={() => setPage("test")}>
                실행 중 · {loadText(run)} · 측정 계속 진행 중
              </button>
            )}
            <span className="online">● Controller online</span>
          </div>
        </header>
        {error && <p className="error">{error}</p>}
        {notice && <p className="online">{notice}</p>}
        {page === "test" && (
          <>
            <section className="mode-tabs">
              <button
                className={mode === "manual" ? "active" : ""}
                onClick={() => setMode("manual")}
              >
                수동 테스트
              </button>
              <button
                className={mode === "automatic" ? "active" : ""}
                onClick={() => setMode("automatic")}
              >
                자동 용량 탐색
              </button>
            </section>
            <section className="panel">
              <h3>
                {mode === "manual"
                  ? "한 번의 부하를 측정합니다"
                  : "최대 안정 부하를 자동으로 찾습니다"}
              </h3>
              <TestSettings
                form={form}
                update={update}
                profiles={profiles}
                selectedProfileId={selectedProfileId}
                selectProfile={selectProfile}
              />
              <ScenarioControls form={form} setForm={setForm} />
              {mode === "automatic" && (
                <div className="grid auto-fields">
                  <label>
                    시작 부하
                    <input
                      name="startLoad"
                      type="number"
                      min="1"
                      value={form.startLoad}
                      onChange={update}
                    />
                  </label>
                  <label>
                    최대 부하
                    <input
                      name="maxLoad"
                      type="number"
                      min="1"
                      value={form.maxLoad}
                      onChange={update}
                    />
                  </label>
                </div>
              )}
              <button onClick={start}>
                {mode === "manual" ? "테스트 시작" : "자동 탐색 시작"}
              </button>
              {search && (
                <p className="search-progress">
                  {search.status} · {search.message} · 권장 최대{" "}
                  {search.recommended_load || search.stable_load || "—"}
                </p>
              )}
              {search?.status === "running" && (
                <button
                  className="danger"
                  onClick={() => cancelSearch(search.id).then(setSearch)}
                >
                  중지
                </button>
              )}
            </section>
            <Details run={run} onCancel={stopRun} />
          </>
        )}
        {page === "records" && (
          <>
            <Records runs={runs} choose={setRun} />
            <Comparison runs={runs} />
            <Details run={run} onCancel={stopRun} />
          </>
        )}
        {page === "monitor" && (
          <section className="summary">
            <Metric
              label="Controller"
              value={health?.controller_online ? "온라인" : "확인 중"}
            />
            <Metric
              label="Agent"
              value={health?.agent_online ? "온라인" : "오프라인"}
            />
            <Metric label="대기" value={health?.queued_runs ?? "—"} />
            <Metric label="실행 중" value={health?.running_runs ?? "—"} />
          </section>
        )}
        {page === "profiles" && (
          <Profiles
            profiles={profiles}
            profile={profile}
            update={updateProfile}
            save={saveProfile}
            remove={removeProfile}
            check={checkProfile}
          />
        )}
      </div>
    </main>
  );
}
