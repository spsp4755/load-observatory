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
  completionShortfall,
  getVerdict,
  gpuBoundLabel,
  hasMetric,
  metricKeys,
  metricMean,
  metricPeak,
  milliseconds,
  provenanceDifferences,
  rate,
  summarizeMonitoring,
  workloadDifferences,
} from "./results.js";
import { recommendWorkload } from "./workload-profiles.js";
import { operationalSummary } from "./operational-summary.js";
import {
  defaultTokensPerSecond,
  estimateWorkload,
  formatSeconds,
  workloadShape,
} from "./duration-advice.js";

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
  duration: "600",
  drainSeconds: "120",
  steadyStateSeconds: "60",
  minCompletionPercent: "95",
  ignoreEOS: false,
  maxNumSeqs: "0",
  maxNumBatchedTokens: "0",
  tensorParallelSize: "0",
  accumulateContext: false,
  arrivalPattern: "uniform",
  outputTokensStdev: "0",
  promptPadTokens: "0",
  promptPadStdev: "0",
  tokensPerSecond: String(defaultTokensPerSecond),
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

const phaseLabels = {
  warmup: "워밍업",
  load: "부하 측정",
  drain: "종료 유예 (진행 중 요청 대기)",
  cooldown: "쿨다운",
  done: "정리",
};

// LiveProgress answers the question a running test must answer every second:
// is the target load actually being served, or is it queueing up behind a
// model that cannot keep pace?
function LiveProgress({ run }) {
  const shards = run?.progress || [];
  if (!["queued", "running"].includes(run?.status)) return null;
  if (!shards.length)
    return (
      <section className="panel">
        <h3>실시간 진행</h3>
        <p className="muted">Agent가 첫 측정치를 보고하면 매 초 상태가 표시됩니다.</p>
      </section>
    );
  const sum = (key) => shards.reduce((total, shard) => total + (shard[key] || 0), 0);
  const target = sum("target_load");
  const active = sum("active");
  const waiting = sum("waiting");
  const completed = sum("completed");
  const issued = sum("issued");
  const cancelled = sum("cancelled");
  const completedRPS = shards.reduce((total, shard) => total + (shard.completed_rps || 0), 0);
  const second = Math.max(...shards.map((shard) => shard.second || 0));
  const phase = shards.find((shard) => shard.phase === "drain")?.phase || shards[0].phase;
  const served = target ? (active / target) * 100 : 0;
  return (
    <section className="panel live-progress">
      <h3>
        실시간 진행 · {formatSeconds(second)} 경과
        <span className={`phase-tag ${phase}`}>{phaseLabels[phase] || phase}</span>
      </h3>
      <div className="metrics">
        <Metric label="목표 부하" value={target || "—"} />
        <Metric label="실제 활성 요청" value={active} />
        <Metric label="대기 (생각 시간·슬롯)" value={waiting} />
        <Metric label="완료 RPS" value={completedRPS.toFixed(2)} />
        <Metric label="발행" value={issued} />
        <Metric label="완료" value={completed} />
      </div>
      {target > 0 && active < target && (
        <p className="warn-line">
          목표 {target} 중 {active}건만 실제로 처리 중입니다({served.toFixed(0)}%). 나머지는 생각 시간 대기이거나 아직 발행되지
          않았습니다. 이 격차가 계속되면 목표 부하가 실제로 가해지지 않고 있다는 뜻입니다.
        </p>
      )}
      {cancelled > 0 && (
        <p className="warn-line">진행 중 취소 {cancelled}건. 종료 유예 시간을 늘리면 완료로 집계됩니다.</p>
      )}
      <table>
        <thead>
          <tr>
            <th>샤드</th>
            <th>단계</th>
            <th>목표</th>
            <th>활성</th>
            <th>대기</th>
            <th>발행</th>
            <th>완료</th>
            <th>실패</th>
            <th>취소</th>
            <th>완료 RPS</th>
          </tr>
        </thead>
        <tbody>
          {shards.map((shard) => (
            <tr key={shard.shard_id}>
              <td>{shard.shard_id}</td>
              <td>{phaseLabels[shard.phase] || shard.phase}</td>
              <td>{shard.target_load}</td>
              <td>{shard.active}</td>
              <td>{shard.waiting}</td>
              <td>{shard.issued}</td>
              <td>{shard.completed}</td>
              <td>{shard.failures}</td>
              <td>{shard.cancelled}</td>
              <td>{(shard.completed_rps || 0).toFixed(2)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

const percentOf = (monitoring, key, digits = 0) => {
  const peak = metricPeak(monitoring, key);
  return peak == null ? "—" : `${peak.toFixed(digits)}%`;
};
const ratioOf = (monitoring, key) => {
  const mean = metricMean(monitoring, key);
  return mean == null ? "미수집" : `${(mean * 100).toFixed(0)}%`;
};
const countOf = (monitoring, key) => {
  const peak = metricPeak(monitoring, key);
  return peak == null ? "미수집" : peak.toFixed(0);
};

// ServerState shows what the model server itself reported, which is what makes a
// client-side latency attributable to a cause rather than just observed.
function ServerState({ monitoring }) {
  if (!monitoring.available) return null;
  const bound = gpuBoundLabel(monitoring);
  const backendLabel = { vllm: "vLLM", sglang: "SGLang", tgi: "TGI" }[monitoring.backend] || "알 수 없음";
  return (
    <section className="panel">
      <h3>
        모델 서버 상태
        <span className="phase-tag">{backendLabel}</span>
      </h3>
      {monitoring.message && <p className="warn-line">{monitoring.message}</p>}
      <div className="metrics">
        <Metric label="대기 요청 (최대)" value={countOf(monitoring, metricKeys.requestsWaiting)} />
        <Metric label="동시 실행 (최대)" value={countOf(monitoring, metricKeys.requestsRunning)} />
        <Metric label="KV 캐시 (평균)" value={ratioOf(monitoring, metricKeys.kvCacheUsage)} />
        <Metric label="Preemption (최대/초)" value={countOf(monitoring, metricKeys.preemptionRate)} />
        <Metric label="prefix 캐시 히트 (평균)" value={ratioOf(monitoring, metricKeys.prefixCacheHitRate)} />
        <Metric label="서버 큐 대기 P95" value={millisOf(monitoring, metricKeys.queueTimeP95)} />
      </div>
      <p className="muted">
        KV 캐시 사용률이 높은 것 자체는 정상입니다. vLLM은 배치를 키우려고 캐시를 의도적으로 채웁니다. 포화는{" "}
        <b>대기 요청이 지속되고 preemption이 발생할 때</b>입니다.
      </p>
      {bound && (
        <>
          <h4>GPU 병목 성격</h4>
          <div className="metrics">
            <Metric label="메모리 대역폭 (평균)" value={ratioOf(monitoring, metricKeys.dramActive)} />
            <Metric label="텐서 코어 (평균)" value={ratioOf(monitoring, metricKeys.tensorActive)} />
            <Metric label="SM 점유율 (평균)" value={ratioOf(monitoring, metricKeys.smOccupancy)} />
            <Metric label="SM 클럭 (평균)" value={mhzOf(monitoring, metricKeys.smClockMHz)} />
          </div>
          <p className={bound.bound === "underused" ? "warn-line" : "muted"}>{bound.text}</p>
        </>
      )}
    </section>
  );
}

const millisOf = (monitoring, key) => {
  const mean = metricMean(monitoring, key);
  return mean == null ? "미수집" : `${mean.toFixed(0)} ms`;
};
const mhzOf = (monitoring, key) => {
  const mean = metricMean(monitoring, key);
  return mean == null ? "미수집" : `${mean.toFixed(0)} MHz`;
};

// SweepCurve shows the measured latency-throughput curve and where the knee is.
// A single stable/unstable answer cannot locate capacity; the shape has to be
// visible, because throughput keeps rising while latency barely moves until the
// knee and then turns almost vertical.
function SweepCurve({ search, form }) {
  const steps = search.steps || [];
  const ladder = search.ladder || [];
  const unit = form?.mode === "rps" ? "RPS" : "VU";
  const knee = search.recommended_load || 0;
  return (
    <section className="panel sweep">
      <h3>
        용량 sweep
        <span className={`phase-tag ${search.status}`}>{search.status}</span>
      </h3>
      <p className="muted">{search.message || `계획된 단계: ${ladder.join(" → ")} ${unit}`}</p>
      {knee > 0 && (
        <div className="metrics">
          <Metric label={`SLO 충족 최대 부하`} value={`${knee} ${unit}`} />
          <Metric label="운영 권장 (여유 30%)" value={`${search.provision_load || "—"} ${unit}`} />
          <Metric label="측정된 단계" value={`${steps.length} / ${ladder.length || "—"}`} />
        </div>
      )}
      {steps.length > 0 && (
        <div className="scroll-x">
          <table>
            <thead>
              <tr>
                <th>부하</th>
                <th>완료 RPS</th>
                <th>출력 tok/s</th>
                <th>TTFT P95</th>
                <th>TPOT P95</th>
                <th>E2E P95</th>
                <th>Goodput</th>
                <th>완료율</th>
                <th>판정</th>
              </tr>
            </thead>
            <tbody>
              {steps.map((step) => (
                <tr key={`${step.load}-${step.run_id}`} className={step.stable ? "" : "row-at-risk"}>
                  <td>
                    {step.load} {unit}
                    {step.load === knee && <b className="knee-tag">← 한계</b>}
                  </td>
                  <td>{(step.throughput_rps || 0).toFixed(2)}</td>
                  <td>{(step.output_tokens_per_second || 0).toFixed(1)}</td>
                  <td>{milliseconds(step.ttft_p95_millis)}</td>
                  <td>{milliseconds(step.tpot_p95_millis)}</td>
                  <td>{milliseconds(step.latency_p95_millis)}</td>
                  <td>{(step.goodput_percent || 0).toFixed(1)}%</td>
                  <td>{(step.completion_percent || 0).toFixed(1)}%</td>
                  <td>{step.stable ? "충족" : step.reason || "미충족"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {steps.length > 1 && (
        <p className="muted">
          부하를 올려도 처리량은 늘고 지연은 거의 그대로인 구간이 이어지다가, 한계를 지나면 지연이 급격히 꺾입니다. 이는 continuous
          batching 때문이며 일반 웹 서버와 반대입니다. 그래서 단일 동시성 측정은 수용량을 크게 과소평가합니다.
        </p>
      )}
    </section>
  );
}

// Provenance records the server settings the numbers depend on. Without them a
// capacity report is an anecdote: it cannot be compared with the next run's.
function Provenance({ run }) {
  const provenance = run.provenance;
  if (!provenance) return null;
  const server = provenance.server || {};
  const rows = [
    ["vLLM 버전", server.version],
    ["모델", server.model],
    ["max_num_seqs", server.max_num_seqs],
    ["max_num_batched_tokens", server.max_num_batched_tokens],
    ["gpu_memory_utilization", server.gpu_memory_utilization],
    ["block_size", server.block_size],
    ["tensor_parallel_size", server.tensor_parallel_size],
    ["prefix caching", server.prefix_caching],
    ["chunked prefill", server.chunked_prefill],
  ];
  return (
    <section className="panel">
      <h3>측정 조건 (provenance)</h3>
      <p className="muted">
        용량 수치는 이 설정 아래에서만 유효합니다. 설정이 다른 실행끼리는 비교할 수 없습니다.
      </p>
      {!provenance.ttft_comparable && (
        <p className="warn-line">
          TTFT를 다른 실행과 비교할 수 없습니다. chunked prefill이 기본 활성이라 TTFT는 프롬프트 길이가 아니라{" "}
          <code>max_num_batched_tokens</code>의 함수인데, 이 값(또는 prefix caching·chunked prefill 여부)이 확인되지
          않았습니다.
        </p>
      )}
      {(provenance.conflicts || []).length > 0 && (
        <p className="warn-line">
          입력한 설정과 서버가 보고한 설정이 다릅니다. 서버 보고값을 사용했습니다:
          <br />
          {provenance.conflicts.join(" · ")}
        </p>
      )}
      <div className="scroll-x">
        <table>
          <tbody>
            {rows.map(([label, value]) => (
              <tr key={label}>
                <td>{label}</td>
                <td>{value ? String(value) : <span className="muted">미확인</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

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
  const shortfall = completionShortfall(run);
  const scenarios = result.scenarios || [];
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
        {run.status === "completed" && (
          <span className="export-links">
            <a className="small" href={`/api/runs/${run.id}/export.json`} download>
              JSON 내보내기
            </a>
            <a className="small" href={`/api/runs/${run.id}/export.csv`} download>
              CSV 내보내기
            </a>
          </span>
        )}
      </section>
      {run.validity && run.validity.trustworthy === false && (
        <section className="panel verdict at-risk">
          <div>
            <h3>이 실행은 신뢰할 수 없습니다</h3>
            <ul className="errors">
              {(run.validity.reasons || []).map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
            <p>아래 수치는 참고용입니다. 원인을 해소한 뒤 다시 측정하세요.</p>
          </div>
        </section>
      )}
      {run.saturation && run.saturation.state !== "unknown" && (
        <section className={`panel saturation ${run.saturation.state}`}>
          <div>
            <h3>서버 포화 판정</h3>
            <strong>{run.saturation.headline}</strong>
            {run.saturation.detail && <p>{run.saturation.detail}</p>}
          </div>
          <span className="decision-cause">
            대기 최대 {run.saturation.peak_waiting?.toFixed(0) ?? 0} · KV 평균{" "}
            {((run.saturation.avg_kv_cache_usage ?? 0) * 100).toFixed(0)}% · preemption{" "}
            {(run.saturation.preemption_rate ?? 0).toFixed(2)}/초
          </span>
        </section>
      )}
      {run.attribution?.available && (
        <section className={`panel attribution ${run.attribution.verdict}`}>
          <div>
            <h3>병목 귀속</h3>
            <strong>{run.attribution.headline}</strong>
            {run.attribution.verdict === "metrics_not_this_run" ? (
              <p>
                서버 대기 {run.attribution.server_queue_millis?.toFixed(0)} ms + prefill{" "}
                {run.attribution.server_prefill_millis?.toFixed(0)} ms &gt; 클라이언트 TTFT P95{" "}
                {run.attribution.client_ttft_millis} ms. Prometheus 쿼리를 이 모델·인스턴스로 좁히거나, 측정 중 다른 트래픽을
                차단한 뒤 다시 측정하세요.
              </p>
            ) : (
              <p>
                클라이언트 TTFT P95 {run.attribution.client_ttft_millis} ms = 서버 대기{" "}
                {run.attribution.server_queue_millis?.toFixed(0)} ms + prefill{" "}
                {run.attribution.server_prefill_millis?.toFixed(0)} ms + 설명되지 않음{" "}
                {run.attribution.unaccounted_millis?.toFixed(0)} ms (
                {run.attribution.unaccounted_percent?.toFixed(0)}%)
              </p>
            )}
          </div>
        </section>
      )}
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
            종료 유예{" "}
            <b>{run.config.drain_seconds ? formatSeconds(run.config.drain_seconds) : "없음"}</b>
          </span>
          <span>
            측정 시작{" "}
            <b>
              {run.config.steady_state_seconds
                ? `${formatSeconds(run.config.steady_state_seconds)} 이후`
                : "즉시"}
            </b>
          </span>
          <span>
            최대 출력 <b>{run.config.max_tokens} 토큰</b>
          </span>
          <span>
            출력 길이{" "}
            <b className={result.output_length_pinned ? "" : "unpinned"}>
              {result.output_length_pinned ? "고정 (ignore_eos)" : "미고정"}
            </b>
          </span>
          <span>
            대화 이력{" "}
            <b className={result.context_accumulated ? "" : "unpinned"}>
              {result.context_accumulated ? "누적 (멀티턴)" : "턴별 독립"}
            </b>
          </span>
          <span>
            캐시 <b>{policyText(run)}</b>
          </span>
        </div>
        <p className="prompt-preview">{run.config.prompt}</p>
      </section>
      {result.issued > 0 && (
        <section className={`panel lifecycle ${shortfall ? "at-risk" : ""}`}>
          <h3>요청 처리 결과</h3>
          <p className="muted">
            시작된 요청과 끝난 요청을 분리해 집계합니다. 시간 종료로 취소된 요청은 실패가 아니지만, 완료로도 세지 않습니다.
          </p>
          <div className="metrics">
            <Metric label="발행 (시작)" value={result.issued} />
            <Metric label="완료 (응답 수신)" value={result.completed ?? 0} />
            <Metric
              label="완료율"
              value={`${(result.completion_percent ?? 0).toFixed(1)}%`}
            />
            <Metric label="시간 종료 취소" value={result.cancelled ?? 0} />
            <Metric label="HTTP 실패" value={result.http_failures ?? 0} />
            <Metric label="연결·전송 오류" value={result.transport_errors ?? 0} />
          </div>
          {shortfall ? (
            <p className="warn-line">
              {shortfall.message} 이 실행은 {loadText(run)} 수용 가능을 증명하지 못하므로 안정 용량으로 판정하지 않습니다.
            </p>
          ) : (
            <p className="muted">
              시작한 요청의 {(result.completion_percent ?? 0).toFixed(1)}%가 끝났습니다. 완료율이 기준
              {" "}{run.config.min_completion_percent || 95}% 이상이므로 이 부하 구간의 결과를 판정에 사용합니다.
            </p>
          )}
          {result.drained_seconds > 0 && (
            <p className="muted">
              종료 유예 {formatSeconds(result.drained_seconds)} 동안 진행 중이던 요청이 끝날 때까지 기다렸습니다.
            </p>
          )}
        </section>
      )}
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
          {result.latency_from_intended_arrival && (
            <Metric label="발생기 지연 P95" value={milliseconds(result.generator_delay?.p95_millis)} />
          )}
          {result.agent_sessions > 0 && <><Metric label="에이전트 세션" value={result.agent_sessions} /><Metric label="세션 완료율" value={`${(result.completed_sessions / result.agent_sessions * 100).toFixed(1)}%`} /></>}
        </div>
      </section>
      <section className="panel">
        <h3>지연 분석</h3>
        {result.steady_state_samples > 0 ? (
          <p className="muted">
            아래 P50·P95·TTFT·TPOT는 워밍업과 램프업을 제외한 안정 구간
            {result.steady_state_seconds > 0
              ? ` (${formatSeconds(result.steady_state_seconds)} 이후)`
              : ""}
            의 성공 요청 {result.steady_state_samples}건만으로 계산했습니다.
            {result.samples_decimated && (
              <>
                {" "}
                표본이 상한에 도달해 균일하게 솎아냈으므로, percentile은 추정값입니다.
              </>
            )}
          </p>
        ) : (
          result.issued > 0 && (
            <p className="warn-line">
              안정 구간에 완료된 요청이 없어 지연 분포를 신뢰할 수 없습니다. 실행 시간을 늘리거나 측정 시작 지점을 앞으로
              당기세요.
            </p>
          )
        )}
        {result.latency_scope === "worst_shard_p95" && (
          <p className="warn-line">
            이 실행의 P95는 각 샤드 P95 중 가장 높은 값입니다. percentile은 선형 통계가 아니어서 샤드별 P95로는 전체 P95를
            계산할 수 없습니다. 최신 Agent로 다시 측정하면 원시 표본을 합산해 정확한 값을 냅니다.
          </p>
        )}
        {result.latency_scope === "pooled_samples" && (
          <p className="muted">
            분산 실행의 percentile은 모든 샤드의 원시 표본을 합산해 한 번에 계산했습니다. 단일 프로세스 실행과 동일한 값입니다.
          </p>
        )}
        {result.latency_from_intended_arrival && (
          <p className="muted">
            지연은 요청이 <b>보내진 시점이 아니라 보내져야 했던 시점</b>부터 측정했습니다. 그러지 않으면 발생기 안에서 대기한
            시간이 꼬리에서 사라집니다(coordinated omission).
            {result.generator_delay?.p95_millis > 100 && (
              <>
                {" "}
                발생기 지연 P95가 {milliseconds(result.generator_delay.p95_millis)}입니다 — 이만큼은 대상이 아니라{" "}
                <b>부하 발생기가 만든 지연</b>입니다. Agent Pod를 늘리세요.
              </>
            )}
          </p>
        )}
        {!result.output_length_pinned && result.issued > 0 && (
          <p className="warn-line">
            출력 길이가 고정되지 않아 응답마다 생성 토큰 수가 다릅니다. <b>TPOT·ITL을 다른 실행과 비교할 수 없습니다.</b>{" "}
            비교가 필요하면 실행 설정에서 「출력 길이 고정(ignore_eos)」을 켜세요.
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
      {(tokens.completion > 0 || result.missing_usage_responses > 0) && (
        <section className="panel">
          <h3>모델 출력 분석</h3>
          {result.missing_usage_responses > 0 && (
            <p className="warn-line">
              성공 응답 {result.missing_usage_responses}건이 <code>usage</code> 필드를 반환하지 않았습니다. 이 응답의 토큰
              수는 알 수 없어 아래 토큰 지표와 tok/s를 신뢰할 수 없습니다. 참고용으로 수신한 스트림 청크는{" "}
              <b>{(result.content_chunks || 0).toLocaleString()}개</b>이며, 서버가 청크 하나에 여러 토큰을 담을 수 있으므로{" "}
              <b>청크 수는 토큰 수가 아닙니다.</b> 대상이 <code>stream_options.include_usage</code>를 지원하는지 확인하세요.
            </p>
          )}
          {tokens.completion > 0 && (
            <div className="metrics">
              <Metric label="입력 토큰" value={tokens.prompt} />
              <Metric label="출력 토큰" value={tokens.completion} />
              <Metric label="추론 토큰" value={tokens.reasoning || "—"} />
              <Metric
                label="출력 속도"
                value={`${tokens.output_per_second?.toFixed(1) || 0} tok/s`}
              />
            </div>
          )}
        </section>
      )}
      {scenarios.length > 0 && (
        <section className="panel">
          <h3>시나리오별 결과</h3>
          <p className="muted">
            단계마다 완료율·지연·토큰 처리량을 따로 집계합니다. 느린 단계가 전체 평균에 묻히지 않도록 분리했습니다.
          </p>
          <table>
            <thead>
              <tr>
                <th>단계</th>
                <th>발행</th>
                <th>완료</th>
                <th>완료율</th>
                <th>취소</th>
                <th>실패</th>
                <th>P50</th>
                <th>P95</th>
                <th>TTFT P95</th>
                <th>입력 토큰</th>
                <th>출력 토큰</th>
                <th>출력 속도</th>
              </tr>
            </thead>
            <tbody>
              {scenarios.map((scenario) => (
                <tr
                  key={scenario.name}
                  className={scenario.completion_percent < (run.config.min_completion_percent || 95) ? "row-at-risk" : ""}
                >
                  <td>{scenario.name}</td>
                  <td>{scenario.issued}</td>
                  <td>{scenario.completed}</td>
                  <td>{(scenario.completion_percent || 0).toFixed(1)}%</td>
                  <td>{scenario.cancelled}</td>
                  <td>{scenario.failures}</td>
                  <td>{milliseconds(scenario.latency?.p50_millis)}</td>
                  <td>{milliseconds(scenario.latency?.p95_millis)}</td>
                  <td>{milliseconds(scenario.ttft?.p95_millis)}</td>
                  <td>{(scenario.input_tokens || 0).toLocaleString()}</td>
                  <td>{(scenario.output_tokens || 0).toLocaleString()}</td>
                  <td>{(scenario.output_per_second || 0).toFixed(1)} tok/s</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
      <Provenance run={run} />
      <ServerState monitoring={monitoring} />
      <section className="panel">
        <h3>인프라 모니터링</h3>
        {monitoring.available ? (
          <>
            <div className="metrics">
              <Metric label="GPU 사용률 (최대)" value={percentOf(monitoring, metricKeys.gpuUtilization, 1)} />
              <Metric label="GPU 메모리 (최대)" value={percentOf(monitoring, metricKeys.gpuMemoryUsed, 1)} />
              <Metric label="CPU 사용률 (최대)" value={percentOf(monitoring, metricKeys.cpuUtilization, 1)} />
              <Metric label="메모리 사용률 (최대)" value={percentOf(monitoring, metricKeys.memoryUsed, 1)} />
            </div>
            {hasMetric(monitoring, metricKeys.gpuUtilization) && (
              <p className="muted">
                GPU 사용률은 「커널이 하나라도 실행됐는지」를 재는 값이라 동시 요청 1건에서도 100%에 가깝습니다.{" "}
                <b>이 값만으로 용량 한계를 판단하면 실제 수용량을 크게 과소평가합니다.</b> 위 「GPU 병목 성격」을 보세요.
              </p>
            )}
          </>
        ) : (
          <p className="muted">수집 불가: {monitoring.message}</p>
        )}
      </section>
      <section className="panel">
        <h3>초당 추세</h3>
        <p className="muted">
          목표 부하와 실제 활성 요청의 격차가 곧 대상이 부하를 받아내지 못한 정도입니다. 완료는 그 초에 응답이 끝난 건수입니다.
        </p>
        <div className="scroll-x">
          <table>
            <thead>
              <tr>
                <th>경과</th>
                <th>목표</th>
                <th>활성</th>
                <th>대기</th>
                <th>발행</th>
                <th>완료</th>
                <th>성공</th>
                <th>실패</th>
                <th>취소</th>
                <th>P95</th>
              </tr>
            </thead>
            <tbody>
              {(result.timeline || []).length ? (
                result.timeline.map((point) => (
                  <tr
                    key={point.second}
                    className={point.target_load > 0 && point.active < point.target_load ? "row-at-risk" : ""}
                  >
                    <td>{point.second + 1}초</td>
                    <td>{point.target_load || "—"}</td>
                    <td>{point.active || 0}</td>
                    <td>{point.waiting || 0}</td>
                    <td>{point.issued || 0}</td>
                    <td>{point.completed || 0}</td>
                    <td>{point.successes}</td>
                    <td>{point.failures}</td>
                    <td>{point.cancelled || 0}</td>
                    <td>{milliseconds(point.p95_millis)}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan="10">추세 데이터가 없습니다.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
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

function ScenarioControls({ form, setForm, advice, applyAdvice }) {
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
  const { callsPerUser, outputBudget } = workloadShape(form);
  const workloadName = form.agentWorkflow ? "순차 에이전트 세션" : scenario.length > 1 ? "혼합 요청 사용자군" : "단일 질의 요청";
  const callsLabel = journeys.length ? "사용자군별 호출" : "사용자당 호출";
  const callsValue = journeys.length ? "단일 1회 / 에이전트 3회" : `${callsPerUser}회`;
  return (
    <section className="advanced panel">
      <section className="workload-plan" aria-live="polite">
        <div><span>현재 테스트 계획</span><strong>{workloadName}</strong></div>
        <div><span>{callsLabel}</span><strong>{callsValue}</strong></div>
        <div><span>{form.agentWorkflow ? "사용자당 최대 출력 예산" : "요청당 최대 출력 예산"}</span><strong>{outputBudget.toLocaleString()} 토큰</strong></div>
        <div><span>예상 소요 시간 (1{form.agentWorkflow ? "세션" : "요청"})</span><strong>{formatSeconds(advice.secondsPerUserCycle)}</strong></div>
        <p>VU는 동시 사용자 수입니다. 혼합 사용자군은 가중치 비율로 요청을 섞고, 에이전트 워크로드는 각 단계를 순서대로 실행합니다.</p>
      </section>
      {advice.message && (
        <section className={`duration-advice ${advice.level}`} aria-live="polite">
          <div>
            <strong>
              {advice.level === "error"
                ? "측정 시간이 부족합니다"
                : advice.level === "warn"
                  ? "측정 시간을 확인하세요"
                  : "측정 시간 적정"}
            </strong>
            <p>{advice.message}</p>
          </div>
          {advice.level !== "ok" && (
            <button type="button" className="small" onClick={applyAdvice}>
              권장값 적용 (실행 {formatSeconds(advice.recommendedDurationSeconds)} · 측정 시작{" "}
              {formatSeconds(advice.recommendedSteadyStateSeconds)} · 유예{" "}
              {formatSeconds(advice.recommendedDrainSeconds)})
            </button>
          )}
        </section>
      )}
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
      {form.mode === "rps" && (
        <label>
          도착 패턴
          <small>
            실제 호출자는 일정 간격으로 오지 않습니다. Poisson은 평균 요청률은 같지만 간격이 불규칙해, TTFT가 폭발하는 큐 현상이
            드러납니다.
          </small>
          <select name="arrivalPattern" value={form.arrivalPattern || "uniform"} onChange={update}>
            <option value="uniform">균일 간격</option>
            <option value="poisson">Poisson (불규칙)</option>
          </select>
        </label>
      )}
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
        <small>부하를 발행하는 시간입니다. 종료 유예는 여기에 포함되지 않습니다.</small>
        <input
          name="duration"
          type="number"
          min="1"
          value={form.duration}
          onChange={update}
        />
      </label>
      <label>
        종료 유예 (초)
        <small>실행 시간이 끝나면 새 요청을 멈추고, 진행 중인 요청이 끝날 때까지 이만큼 기다립니다.</small>
        <input
          name="drainSeconds"
          type="number"
          min="0"
          max="600"
          value={form.drainSeconds ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        측정 시작 지점 (초)
        <small>이 시간 이전에 시작된 요청은 P50·P95·TTFT·TPOT 계산에서 제외합니다.</small>
        <input
          name="steadyStateSeconds"
          type="number"
          min="0"
          value={form.steadyStateSeconds ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        최소 완료율 (%)
        <small>시작한 요청 중 끝난 비율의 하한입니다. 이 값 미만이면 안정 용량으로 판정하지 않습니다.</small>
        <input
          name="minCompletionPercent"
          type="number"
          min="0"
          max="100"
          value={form.minCompletionPercent ?? "95"}
          onChange={update}
        />
      </label>
      <label>
        서버 max_num_seqs (선택)
        <small>모델 서버의 동시 실행 상한입니다. 입력하면 「하드웨어 한계」와 「설정 한계」를 구분해 판정합니다.</small>
        <input
          name="maxNumSeqs"
          type="number"
          min="0"
          value={form.maxNumSeqs ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        출력 길이 편차 (± 토큰)
        <small>요청마다 최대 출력 토큰을 이 범위 안에서 흔듭니다. 실제 트래픽은 응답 길이가 하나로 고정되지 않습니다.</small>
        <input
          name="outputTokensStdev"
          type="number"
          min="0"
          value={form.outputTokensStdev ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        프롬프트 추가 길이 (토큰)
        <small>
          프롬프트를 이만큼 늘려 입력 길이 분포를 만듭니다. 토크나이저를 반입하지 않으므로 문자 수 기준 <b>근사값</b>입니다.
        </small>
        <input
          name="promptPadTokens"
          type="number"
          min="0"
          value={form.promptPadTokens ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        프롬프트 길이 편차 (± 토큰)
        <small>추가 길이를 요청마다 이 범위 안에서 흔듭니다.</small>
        <input
          name="promptPadStdev"
          type="number"
          min="0"
          value={form.promptPadStdev ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        서버 max_num_batched_tokens (선택)
        <small>
          단계당 토큰 예산입니다. chunked prefill이 기본 활성이라 TTFT는 프롬프트 길이가 아니라 이 예산의 함수입니다.{" "}
          <b>입력하지 않으면 TTFT를 다른 실행과 비교할 수 없습니다.</b>
        </small>
        <input
          name="maxNumBatchedTokens"
          type="number"
          min="0"
          value={form.maxNumBatchedTokens ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        서버 tensor_parallel_size (선택)
        <small>TP 차수입니다. 실행 간 비교 근거로 기록합니다.</small>
        <input
          name="tensorParallelSize"
          type="number"
          min="0"
          value={form.tensorParallelSize ?? "0"}
          onChange={update}
        />
      </label>
      <label>
        예상 생성 속도 (tok/s)
        <small>실행 시간이 충분한지 계산하는 데만 씁니다. 실제 측정값으로 보정하세요.</small>
        <input
          name="tokensPerSecond"
          type="number"
          min="1"
          value={form.tokensPerSecond ?? String(defaultTokensPerSecond)}
          onChange={update}
        />
      </label>
      <label className="wide checkbox-field">
        <span>
          대화 이력 누적 (멀티턴)
          <small>
            에이전트·멀티턴 시나리오에서 각 턴의 답변을 다음 요청에 함께 보냅니다. 실제 챗·에이전트처럼 프롬프트가 턴마다
            커지며, 이 증가가 KV 캐시 압력과 TTFT 상승의 주된 원인입니다. 끄면 각 단계를 독립 요청으로 보내 그 부하를
            측정하지 않습니다.
          </small>
        </span>
        <input
          name="accumulateContext"
          type="checkbox"
          checked={Boolean(form.accumulateContext)}
          onChange={(event) =>
            update({ target: { name: "accumulateContext", value: event.target.checked } })
          }
        />
      </label>
      <label className="wide checkbox-field">
        <span>
          출력 길이 고정 (<code>ignore_eos</code>)
          <small>
            모든 응답이 정확히 최대 토큰까지 생성되어 TPOT·ITL을 실행 간 비교할 수 있습니다. vLLM·SGLang 확장이므로, 대상이
            모르는 필드를 거부하면 모든 요청이 실패합니다. 「모델 등록 → 연결 확인」에서 지원 여부를 먼저 확인하세요.
          </small>
        </span>
        <input
          name="ignoreEOS"
          type="checkbox"
          checked={Boolean(form.ignoreEOS)}
          onChange={(event) =>
            update({ target: { name: "ignoreEOS", value: event.target.checked } })
          }
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
            <th>완료율</th>
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
                <td className={completionShortfall(item) ? "cell-at-risk" : ""}>
                  {result.issued
                    ? `${(result.completion_percent ?? 0).toFixed(1)}%`
                    : "—"}
                </td>
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

// ComparabilityWarning refuses to let a delta between two runs read as meaningful
// when they were not measured under the same conditions.
function ComparabilityWarning({ left, right }) {
  const server = provenanceDifferences(left, right);
  const workload = workloadDifferences(left, right);
  if (!server.length && !workload.length) {
    return <p className="muted">두 실행의 서버 설정과 워크로드 조건이 같습니다. 아래 차이는 부하 차이로 해석할 수 있습니다.</p>;
  }
  return (
    <p className="warn-line">
      <b>두 실행의 측정 조건이 다릅니다.</b> 아래 차이를 부하 차이로 해석하면 안 됩니다.
      {workload.length > 0 && (
        <>
          <br />
          워크로드: {workload.join(" · ")}
        </>
      )}
      {server.length > 0 && (
        <>
          <br />
          서버 설정: {server.join(" · ")}
        </>
      )}
    </p>
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
          {left && right && <ComparabilityWarning left={left} right={right} />}
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
  const { callsPerUser, outputBudget } = workloadShape(form);
  const advice = estimateWorkload({
    outputBudget,
    callsPerUser,
    tokensPerSecond: form.tokensPerSecond,
    durationSeconds: form.duration,
    steadyStateSeconds: form.steadyStateSeconds,
    drainSeconds: form.drainSeconds,
    outputLengthPinned: Boolean(form.ignoreEOS),
  });
  const applyAdvice = () =>
    setForm((current) => ({
      ...current,
      duration: String(advice.recommendedDurationSeconds),
      steadyStateSeconds: String(advice.recommendedSteadyStateSeconds),
      drainSeconds: String(advice.recommendedDrainSeconds),
    }));
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
      const pinning =
        result.supports_ignore_eos === undefined
          ? ""
          : result.supports_ignore_eos
            ? " · 출력 길이 고정(ignore_eos) 지원"
            : " · 출력 길이 고정(ignore_eos) 미지원 — 켜지 마세요";
      setNotice(
        `${item.name}: HTTP ${result.status_code} · ${result.latency_millis} ms${pinning}`,
      );
    } catch (err) {
      setNotice("");
      setError(err.message);
    }
  };
  // The list response omits the per-second sample series to stay small, so a run
  // selected from it has to be re-fetched for the server-side detail panels.
  const selectRun = async (item) => {
    setRun(item);
    try {
      setRun(await getRun(item.id));
    } catch (err) {
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
      if (advice.invalid) throw new Error(advice.message);
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
              <ScenarioControls
                form={form}
                setForm={setForm}
                advice={advice}
                applyAdvice={applyAdvice}
              />
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
              {search && <SweepCurve search={search} form={form} />}
              {search?.status === "running" && (
                <button
                  className="danger"
                  onClick={() => cancelSearch(search.id).then(setSearch)}
                >
                  중지
                </button>
              )}
            </section>
            <LiveProgress run={run} />
            <Details run={run} onCancel={stopRun} />
          </>
        )}
        {page === "records" && (
          <>
            <Records runs={runs} choose={selectRun} />
            <Comparison runs={runs} />
            <LiveProgress run={run} />
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
