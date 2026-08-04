import assert from "node:assert/strict";
import test from "node:test";
import { operationalSummary } from "../src/operational-summary.js";

test("identifies latency as the limiting factor", () => {
  const summary = operationalSummary({
    status: "completed",
    config: { mode: "vu", vus: 20, max_error_percent: 2, max_p95_millis: 1000 },
    result: { total: 100, successes: 100, failures: 0, latency: { p95_millis: 1500 } },
  });
  assert.equal(summary.status, "at-risk");
  assert.equal(summary.cause, "latency");
});

test("recommends the next capacity step for a stable load", () => {
  const summary = operationalSummary({
    status: "completed",
    config: { mode: "vu", vus: 20, max_error_percent: 2, max_p95_millis: 1000 },
    result: { total: 100, successes: 100, failures: 0, latency: { p95_millis: 600 } },
  });
  assert.equal(summary.status, "stable");
  assert.match(summary.nextAction, /30 VU/);
});
