import assert from "node:assert/strict";
import test from "node:test";
import { getVerdict } from "../src/results.js";

test("getVerdict recommends the configured load when a completed run is stable", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "vu", vus: 10, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 1, latency: { p95_millis: 250 } } });
  assert.equal(verdict.status, "stable");
  assert.match(verdict.recommendation, /10/);
});

test("getVerdict flags threshold breaches", () => {
  const verdict = getVerdict({ status: "completed", config: { mode: "rps", rps: 50, max_error_percent: 2, max_p95_millis: 2000 }, result: { total: 100, failures: 3, latency: { p95_millis: 2500 } } });
  assert.equal(verdict.status, "at-risk");
});
