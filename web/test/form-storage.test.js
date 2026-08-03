import assert from "node:assert/strict";
import test from "node:test";
import { loadSavedValue, saveValue } from "../src/form-storage.js";

test("restores saved form values over defaults", () => {
  const values = new Map([["form", JSON.stringify({ maxTokens: "4096", vus: "20" })]]);
  const storage = { getItem: (key) => values.get(key) || null, setItem: () => {} };
  assert.deepEqual(loadSavedValue(storage, "form", { maxTokens: "1280", vus: "10", mode: "vu" }), { maxTokens: "4096", vus: "20", mode: "vu" });
});

test("ignores malformed saved values", () => {
  const storage = { getItem: () => "{", setItem: () => {} };
  assert.deepEqual(loadSavedValue(storage, "form", { mode: "vu" }), { mode: "vu" });
});

test("saves a JSON value", () => {
  let saved;
  saveValue({ setItem: (key, value) => { saved = { key, value }; } }, "profile", "target-1");
  assert.deepEqual(saved, { key: "profile", value: '"target-1"' });
});

test("restores a selected target id", () => {
  const storage = { getItem: () => '"target-1"', setItem: () => {} };
  assert.equal(loadSavedValue(storage, "profile", ""), "target-1");
});
