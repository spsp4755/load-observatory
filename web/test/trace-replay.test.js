import assert from "node:assert/strict";
import test from "node:test";
import { parseTraceReplay, traceDurationSeconds } from "../src/trace-replay.js";

test("parses GuideLLM-style JSONL and normalizes timestamps", () => {
  const events = parseTraceReplay([
    JSON.stringify({ timestamp: 1000, input_length: 128, output_length: 64 }),
    JSON.stringify({ timestamp: 2250, scenario: "coding", prompt: "fix it", output_tokens: 256 }),
  ].join("\n"));
  assert.deepEqual(events, [
    { timestamp_ms: 0, name: "trace", prompt: "", prompt_tokens: 128, max_tokens: 64 },
    { timestamp_ms: 1250, name: "coding", prompt: "fix it", prompt_tokens: 0, max_tokens: 256 },
  ]);
  assert.equal(traceDurationSeconds(events, 2), 2);
});

test("rejects an unordered trace", () => {
  assert.throws(() => parseTraceReplay('[{"timestamp_ms":10},{"timestamp_ms":5}]'), /시간순/);
});
