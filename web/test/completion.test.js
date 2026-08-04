import assert from "node:assert/strict";
import test from "node:test";
import { completionShortfall, getVerdict } from "../src/results.js";
import { operationalSummary } from "../src/operational-summary.js";

const partialRun = {
  status: "completed",
  config: { mode: "vu", vus: 30, max_error_percent: 2, max_p95_millis: 600000, min_completion_percent: 95 },
  result: {
    issued: 30, completed: 10, cancelled: 20, completion_percent: 33.3,
    successes: 10, failures: 0, total: 10, latency: { p95_millis: 5000 },
  },
};

test("a run that finished a third of its requests reports the shortfall", () => {
  const shortfall = completionShortfall(partialRun);
  assert.ok(shortfall, "expected a shortfall");
  assert.equal(shortfall.issued, 30);
  assert.equal(shortfall.completed, 10);
  assert.match(shortfall.message, /30건 중 10건/);
  assert.match(shortfall.message, /20건은 측정 종료 시점에 아직 진행 중/);
});

test("the shortfall outranks the latency verdict and blocks a stable judgement", () => {
  const verdict = getVerdict(partialRun);
  assert.equal(verdict.status, "at-risk");
  assert.match(verdict.recommendation, /안정 용량으로 판정하지 않습니다/);
});

test("the operational summary refuses to claim the load was served", () => {
  const summary = operationalSummary(partialRun);
  assert.equal(summary.status, "at-risk");
  assert.equal(summary.cause, "incomplete");
  assert.match(summary.nextAction, /30 VU 수용 가능을 증명하지 못합니다/);
});

test("a fully completed run within SLO is still judged stable", () => {
  const run = {
    status: "completed",
    config: { mode: "vu", vus: 30, max_error_percent: 2, max_p95_millis: 600000, min_completion_percent: 95 },
    result: { issued: 30, completed: 30, cancelled: 0, completion_percent: 100, successes: 30, failures: 0, total: 30, latency: { p95_millis: 5000 } },
  };
  assert.equal(completionShortfall(run), null);
  assert.equal(getVerdict(run).status, "stable");
  assert.equal(operationalSummary(run).status, "stable");
});

test("a result without lifecycle counters is not gated on completion", () => {
  const legacy = {
    status: "completed",
    config: { mode: "vu", vus: 5, max_error_percent: 2, max_p95_millis: 2000 },
    result: { successes: 10, failures: 0, total: 10, latency: { p95_millis: 100 } },
  };
  assert.equal(completionShortfall(legacy), null);
  assert.equal(getVerdict(legacy).status, "stable");
});
