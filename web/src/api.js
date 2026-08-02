async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...options.headers },
    ...options,
  });
  if (!response.ok) throw new Error(await response.text() || `HTTP ${response.status}`);
  return response.status === 204 ? null : response.json();
}

export const createTarget = (target) => request("/api/targets", { method: "POST", body: JSON.stringify(target) });
export const createRun = (run) => request("/api/runs", { method: "POST", body: JSON.stringify(run) });
export const getRun = (id) => request(`/api/runs/${id}`);
