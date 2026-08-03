export function toRunConfig({ targetId, mode, vus, rps, duration, prompt, maxTokens }) {
  const config = { target_id: targetId, mode, duration_seconds: Number(duration), prompt, max_tokens: Number(maxTokens) };
  if (mode === "rps") config.rps = Number(rps);
  else config.vus = Number(vus);
  return config;
}
