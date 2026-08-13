async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...options.headers },
    ...options,
  });
  // A session can expire mid-use (a long run can outlive the cookie). Send the
  // browser back through the Keycloak login rather than surfacing a raw 401 the
  // user has no way to act on.
  if (response.status === 401 && path !== "/api/session") {
    window.location.assign("/auth/login");
    return new Promise(() => {}); // navigation is in flight; never resolve
  }
  if (!response.ok) throw new Error(await response.text() || `HTTP ${response.status}`);
  return response.status === 204 ? null : response.json();
}

export const createTarget = (target) => request("/api/targets", { method: "POST", body: JSON.stringify(target) });
export const listTargets = () => request("/api/targets");
export const deleteTarget = (id) => request(`/api/targets/${id}`, { method: "DELETE" });
export const checkTarget = (id) => request(`/api/targets/${id}/check`, { method: "POST" });
export const createRun = (run) => request("/api/runs", { method: "POST", body: JSON.stringify(run) });
export const cancelRun = (id) => request(`/api/runs/${id}/cancel`, { method: "POST" });
export const getRun = (id) => request(`/api/runs/${id}`);
export const listRuns = () => request("/api/runs");
export const getHealth = () => request("/api/health");
export const getSession = () => request("/api/session");
export const logout = () => request("/auth/logout", { method: "POST" }).then(() => window.location.assign("/"));
export const createSearch = (search) => request("/api/searches", { method: "POST", body: JSON.stringify(search) });
export const getSearch = (id) => request(`/api/searches/${id}`);
export const cancelSearch = (id) => request(`/api/searches/${id}/cancel`, { method: "POST" });
export const listCaptures = () => request("/api/captures");
export const deleteCapture = (id) => request(`/api/captures/${id}`, { method: "DELETE" });
export const getCaptureSettings = () => request("/api/capture-settings");
export const updateCaptureSettings = (settings) => request("/api/capture-settings", { method: "PUT", body: JSON.stringify(settings) });
