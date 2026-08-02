import assert from "node:assert/strict";
import test from "node:test";
import { toRunConfig } from "../src/run-form.js";

test("toRunConfig converts VU form values to a controller payload", () => {
  assert.deepEqual(
    toRunConfig({ targetId: "target-1", mode: "vu", vus: "10", rps: "100", duration: "60" }),
    { target_id: "target-1", mode: "vu", vus: 10, duration_seconds: 60 },
  );
});
