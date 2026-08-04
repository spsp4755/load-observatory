export function toRunConfig({ targetId, mode, vus, rps, duration, prompt, maxTokens, maxErrorPercent, maxP95Millis, cachePolicy = "mixed", variationPercent = "30", shards = "3", maxTTFTP95Millis = "0", minOutputTokensPerSecond = "0", maxTTPOTP95Millis = "0", minGoodputPercent = "0", maxInFlight = "0", warmupRequests = "0", cooldownSeconds = "0", drainSeconds = "0", steadyStateSeconds = "0", minCompletionPercent = "0", stages = [], scenario = [], agentWorkflow = false, journeys = [] }) {
	const config = { target_id: targetId, mode, duration_seconds: Number(duration), prompt, max_tokens: Number(maxTokens), max_error_percent: Number(maxErrorPercent), max_p95_millis: Number(maxP95Millis), cache_policy: cachePolicy, variation_percent: Number(variationPercent), shards: Number(shards), max_ttft_p95_millis: Number(maxTTFTP95Millis), min_output_tokens_per_second: Number(minOutputTokensPerSecond), max_tpot_p95_millis: Number(maxTTPOTP95Millis), min_goodput_percent: Number(minGoodputPercent), max_in_flight: Number(maxInFlight), warmup_requests: Number(warmupRequests), cooldown_seconds: Number(cooldownSeconds), drain_seconds: Number(drainSeconds), steady_state_seconds: Number(steadyStateSeconds), min_completion_percent: Number(minCompletionPercent), stages, scenario, agent_workflow: agentWorkflow, journeys };
  if (mode === "rps") config.rps = Number(rps);
  else config.vus = Number(vus);
  return config;
}

export function toSearchConfig(form) {
	return { run: toRunConfig({ ...form, targetId: form.targetId }), start_load: Number(form.startLoad), max_load: Number(form.maxLoad) };
}
