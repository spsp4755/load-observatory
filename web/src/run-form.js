export function toRunConfig({ targetId, mode, vus, rps, duration, prompt, maxTokens, maxErrorPercent, maxP95Millis }) {
	const config = { target_id: targetId, mode, duration_seconds: Number(duration), prompt, max_tokens: Number(maxTokens), max_error_percent: Number(maxErrorPercent), max_p95_millis: Number(maxP95Millis) };
  if (mode === "rps") config.rps = Number(rps);
  else config.vus = Number(vus);
  return config;
}

export function toSearchConfig(form) {
	return { run: toRunConfig({ ...form, targetId: form.targetId }), start_load: Number(form.startLoad), max_load: Number(form.maxLoad) };
}
