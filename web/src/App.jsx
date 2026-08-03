import React, { useEffect, useState } from "react";
import { createRun, createTarget, getRun } from "./api.js";
import { toRunConfig } from "./run-form.js";

const initial = { type: "model", url: "http://model.internal:8000/v1/chat/completions", model: "", mode: "vu", vus: "10", rps: "100", duration: "60" };

function Metric({ label, value, tone = "" }) {
  return <section className={`metric ${tone}`}><span>{label}</span><strong>{value}</strong></section>;
}

export default function App() {
  const [form, setForm] = useState(initial);
  const [run, setRun] = useState(null);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!run || run.status === "completed") return undefined;
    const timer = setInterval(async () => {
      try { setRun(await getRun(run.id)); } catch (err) { setError(err.message); }
    }, 2000);
    return () => clearInterval(timer);
  }, [run]);

  const update = (event) => setForm({ ...form, [event.target.name]: event.target.value });
  const start = async (event) => {
    event.preventDefault();
    setSubmitting(true); setError("");
    try {
      const target = await createTarget({ name: form.type === "model" ? "Model API" : "Web endpoint", type: form.type, url: form.url, model: form.model });
      setRun(await createRun(toRunConfig({ ...form, targetId: target.id })));
    } catch (err) { setError(err.message); }
    finally { setSubmitting(false); }
  };

  const result = run?.result || {};
  const total = (result.successes || 0) + (result.failures || 0);
  return <main className="shell">
    <aside><h1>Load Observatory</h1><nav><a className="active">부하 테스트</a><a>테스트 기록</a><a>모니터링</a></nav><p className="system">폐쇄망 · 내부 대상만 허용</p></aside>
    <div className="content">
      <header><div><h2>부하 테스트</h2><p>모델 API와 웹 서비스의 처리 한계를 측정합니다.</p></div><span className="online">● Controller online</span></header>
      <form className="panel" onSubmit={start}>
        <h3>테스트 설정</h3>
        <div className="grid">
          <label>대상 유형<select name="type" value={form.type} onChange={update}><option value="model">GPU 모델 API</option><option value="web">웹/API</option></select></label>
          <label className="wide">내부 URL<input name="url" value={form.url} onChange={update} required /></label>
          {form.type === "model" && <label className="wide">모델명<input name="model" value={form.model} onChange={update} placeholder="예: qwen/qwen3.6-35b-a3b" required /></label>}
          <label>부하 방식<select name="mode" value={form.mode} onChange={update}><option value="vu">동시 사용자 (VU)</option><option value="rps">요청률 (RPS)</option></select></label>
          {form.mode === "vu" ? <label>동시 사용자<input name="vus" type="number" min="1" max="500" value={form.vus} onChange={update} required /></label> : <label>요청률<input name="rps" type="number" min="1" max="2000" value={form.rps} onChange={update} required /></label>}
          <label>실행 시간 (초)<input name="duration" type="number" min="1" max="3600" value={form.duration} onChange={update} required /></label>
        </div>
        <button disabled={submitting}>{submitting ? "생성 중…" : "테스트 시작"}</button>
        {error && <p className="error" role="alert">{error}</p>}
      </form>
      <section className="panel results"><div className="section-title"><h3>실행 결과</h3><span className={`status ${run?.status || "idle"}`}>{run?.status || "대기"}</span></div>
        <div className="metrics"><Metric label="성공 요청" value={result.successes ?? "—"} tone="good" /><Metric label="실패 요청" value={result.failures ?? "—"} tone="bad" /><Metric label="P95 응답 시간" value={result.p95_millis == null ? "—" : `${result.p95_millis} ms`} /><Metric label="P95 TTFT" value={result.ttft_p95_millis == null ? "—" : `${result.ttft_p95_millis} ms`} /></div>
        <div className="limit"><span>한계점 판정</span><strong>{total ? `${Math.round((result.failures || 0) / total * 100)}% 오류율` : "실행 후 표시"}</strong><p>P95 2초 또는 오류율 2% 초과 시 병목 구간으로 표시합니다.</p></div>
      </section>
    </div>
  </main>;
}
