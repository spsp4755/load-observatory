import assert from "node:assert/strict";
import test from "node:test";
import { estimateWorkload, formatSeconds } from "../src/duration-advice.js";

test("a 60 second run cannot finish one 20480 token response", () => {
  const advice = estimateWorkload({ outputBudget: 20480, tokensPerSecond: 110, durationSeconds: 60, steadyStateSeconds: 12, drainSeconds: 12 });
  assert.equal(advice.level, "error");
  assert.ok(advice.cyclesInSteadyWindow < 1, "expected less than one response per steady window");
  assert.ok(advice.secondsPerRequest > 180, `expected over three minutes per request, got ${advice.secondsPerRequest}`);
  assert.ok(advice.recommendedDurationSeconds >= 300, "recommendation should be at least five minutes");
  assert.match(advice.message, /판정할 수 없습니다/);
});

test("a long enough run reports how many responses fit the measured window", () => {
  const advice = estimateWorkload({ outputBudget: 1280, tokensPerSecond: 110, durationSeconds: 300, steadyStateSeconds: 30, drainSeconds: 30 });
  assert.equal(advice.level, "ok");
  assert.ok(advice.cyclesInSteadyWindow > 2, "expected several responses in the steady window");
});

test("a window holding only one and a half responses is flagged", () => {
  const advice = estimateWorkload({ outputBudget: 11000, tokensPerSecond: 110, durationSeconds: 180, steadyStateSeconds: 30, drainSeconds: 200 });
  assert.equal(advice.level, "warn");
  assert.match(advice.message, /실행 시간을/);
});

test("a drain shorter than one response is flagged as future cancellations", () => {
  const advice = estimateWorkload({ outputBudget: 4096, tokensPerSecond: 110, durationSeconds: 600, steadyStateSeconds: 40, drainSeconds: 5 });
  assert.equal(advice.level, "warn");
  assert.equal(advice.drainCoversResponse, false);
  assert.match(advice.message, /취소로 집계/);
});

test("an agent session multiplies the cycle by its step count", () => {
  const single = estimateWorkload({ outputBudget: 4096, callsPerUser: 1, durationSeconds: 600 });
  const session = estimateWorkload({ outputBudget: 4096, callsPerUser: 4, durationSeconds: 600 });
  assert.equal(session.secondsPerUserCycle, single.secondsPerUserCycle * 4);
});

test("a run with no token budget makes no claim", () => {
  const advice = estimateWorkload({ outputBudget: 0, durationSeconds: 60 });
  assert.equal(advice.level, "ok");
  assert.equal(advice.message, "");
});

test("seconds are formatted for operators", () => {
  assert.equal(formatSeconds(45), "45초");
  assert.equal(formatSeconds(120), "2분");
  assert.equal(formatSeconds(186), "3분 6초");
});

test("a steady-state start at or past the run duration leaves nothing to measure", () => {
  const advice = estimateWorkload({ outputBudget: 20480, durationSeconds: 60, steadyStateSeconds: 60, drainSeconds: 120 });
  assert.equal(advice.level, "error");
  assert.equal(advice.invalid, true);
  assert.match(advice.message, /측정할 구간이 남지 않습니다/);
});

test("an unpinned output length makes every time estimate an upper bound only", () => {
  const unpinned = estimateWorkload({ outputBudget: 20480, durationSeconds: 600, steadyStateSeconds: 60, drainSeconds: 300 });
  assert.equal(unpinned.upperBoundOnly, true);
  assert.match(unpinned.message, /상한/);
  const pinned = estimateWorkload({ outputBudget: 20480, durationSeconds: 600, steadyStateSeconds: 60, drainSeconds: 300, outputLengthPinned: true });
  assert.equal(pinned.upperBoundOnly, false);
  assert.doesNotMatch(pinned.message, /상한/);
  assert.equal(pinned.secondsPerUserCycle, unpinned.secondsPerUserCycle, "the estimate itself is unchanged, only its confidence");
});
