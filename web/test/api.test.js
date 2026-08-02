import assert from "node:assert/strict";
import test from "node:test";
import { createRun } from "../src/api.js";

test("createRun posts the selected VU configuration", async () => {
  const originalFetch = global.fetch;
  let request;
  global.fetch = async (url, options) => {
    request = { url, options };
    return new Response(JSON.stringify({ id: "run-1", status: "queued" }), { status: 201 });
  };

  const run = await createRun({ target_id: "target-1", mode: "vu", vus: 10, duration_seconds: 60 });

  assert.equal(request.url, "/api/runs");
  assert.deepEqual(JSON.parse(request.options.body), { target_id: "target-1", mode: "vu", vus: 10, duration_seconds: 60 });
  assert.equal(run.id, "run-1");
  global.fetch = originalFetch;
});
