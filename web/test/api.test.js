import assert from "node:assert/strict";
import test from "node:test";
import { createRun, listTargets } from "../src/api.js";

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

test("listTargets reads saved model profiles", async () => {
  const originalFetch = global.fetch;
  global.fetch = async () => new Response(JSON.stringify([{ id: "target-1", name: "LM Studio" }]), { status: 200 });
  assert.equal((await listTargets())[0].name, "LM Studio");
  global.fetch = originalFetch;
});
