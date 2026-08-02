export function toRunConfig({ targetId, mode, vus, rps, duration }) {
  const config = { target_id: targetId, mode, duration_seconds: Number(duration) };
  if (mode === "rps") config.rps = Number(rps);
  else config.vus = Number(vus);
  return config;
}
