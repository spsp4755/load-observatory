import assert from "node:assert/strict";
import test from "node:test";
import { cancelRun, checkTarget, createRun, listTargets, updateCaptureSettings } from "../src/api.js";

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

test("cancelRun posts only the selected run cancellation endpoint", async () => {
  const originalFetch = global.fetch;
  let request;
  global.fetch = async (url, options) => {
    request = { url, options };
    return new Response(JSON.stringify({ id: "run-1", status: "cancelled" }), { status: 200 });
  };
  const run = await cancelRun("run-1");
  assert.equal(request.url, "/api/runs/run-1/cancel");
  assert.equal(request.options.method, "POST");
  assert.equal(run.status, "cancelled");
  global.fetch = originalFetch;
});

test("checkTarget posts only the selected target check endpoint", async () => {
  const originalFetch = global.fetch;
  let request;
  global.fetch = async (url, options) => {
    request = { url, options };
    return new Response(JSON.stringify({ ok: true, status_code: 200 }), { status: 200 });
  };
  assert.equal((await checkTarget("target-1")).ok, true);
  assert.equal(request.url, "/api/targets/target-1/check");
  assert.equal(request.options.method, "POST");
  global.fetch = originalFetch;
});

test("capture settings are saved through the authenticated human API", async () => {
  const originalFetch = global.fetch;
  let request;
  global.fetch = async (url, options) => {
    request = { url, options };
    return new Response(JSON.stringify({ enabled: true, token_configured: true }), { status: 200 });
  };
  const result = await updateCaptureSettings({ enabled: true, token: "generated-secret" });
  assert.equal(request.url, "/api/capture-settings");
  assert.equal(request.options.method, "PUT");
  assert.equal(JSON.parse(request.options.body).token, "generated-secret");
  assert.equal(result.token_configured, true);
  global.fetch = originalFetch;
});
