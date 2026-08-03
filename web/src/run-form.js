export function toRunConfig({ targetId, mode, vus, rps, duration, prompt, maxTokens, maxErrorPercent, maxP95Millis, cachePolicy = "mixed", variationPercent = "30", shards = "3", maxTTFTP95Millis = "0", minOutputTokensPerSecond = "0" }) {
	const config = { target_id: targetId, mode, duration_seconds: Number(duration), prompt, max_tokens: Number(maxTokens), max_error_percent: Number(maxErrorPercent), max_p95_millis: Number(maxP95Millis), cache_policy: cachePolicy, variation_percent: Number(variationPercent), shards: Number(shards), max_ttft_p95_millis: Number(maxTTFTP95Millis), min_output_tokens_per_second: Number(minOutputTokensPerSecond) };
  if (mode === "rps") config.rps = Number(rps);
  else config.vus = Number(vus);
  return config;
}

export function toSearchConfig(form) {
	return { run: toRunConfig({ ...form, targetId: form.targetId }), start_load: Number(form.startLoad), max_load: Number(form.maxLoad) };
}
