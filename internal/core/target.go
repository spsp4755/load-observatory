package core

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

func ValidateTarget(rawURL string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return errors.New("target must be an absolute HTTP URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("target must use HTTP or HTTPS")
	}
	if u.User != nil {
		return errors.New("target must not include credentials")
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsPrivate() {
			return errors.New("target IP must be private")
		}
		return nil
	}
	if !strings.HasSuffix(host, ".internal") {
		return errors.New("target hostname must end in .internal")
	}
	return nil
}

func ValidateRunConfig(config RunConfig) error {
	if config.DurationSeconds < 1 || config.DurationSeconds > MaxDurationSeconds {
		return errors.New("duration must be between 1 and 3600 seconds")
	}
	switch config.Mode {
	case LoadModeVU:
		if config.VUs < 1 || config.VUs > MaxVUs {
			return errors.New("VUs must be between 1 and 500")
		}
	case LoadModeRPS:
		if config.RPS < 1 || config.RPS > MaxRPS {
			return errors.New("RPS must be between 1 and 2000")
		}
	default:
		return errors.New("mode must be vu or rps")
	}
	if config.MaxTokens < 1 || config.MaxTokens > MaxModelTokens {
		return errors.New("max_tokens must be between 1 and 1000000")
	}
	if config.CachePolicy != "" && config.CachePolicy != CachePolicyMixed && config.CachePolicy != CachePolicyReuse && config.CachePolicy != CachePolicyBypass {
		return errors.New("cache_policy must be mixed, reuse, or bypass")
	}
	if config.VariationPercent < 0 || config.VariationPercent > 100 {
		return errors.New("variation_percent must be between 0 and 100")
	}
	if config.Shards < 0 || config.Shards > 64 {
		return errors.New("shards must be between 1 and 64")
	}
	if config.MaxInFlight < 0 || config.MaxInFlight > MaxVUs {
		return errors.New("max_in_flight must be between 1 and 500")
	}
	if config.WarmupRequests < 0 || config.WarmupRequests > 1000 || config.CooldownSeconds < 0 || config.CooldownSeconds > 300 {
		return errors.New("warmup_requests or cooldown_seconds out of range")
	}
	if config.MinGoodputPercent < 0 || config.MinGoodputPercent > 100 || config.MaxTTPOTP95Millis < 0 {
		return errors.New("invalid LLM guardrail")
	}
	if len(config.Stages) > 12 || len(config.Scenario) > 8 {
		return errors.New("too many stages or scenario tasks")
	}
	for _, stage := range config.Stages {
		if stage.DurationSeconds < 1 || stage.DurationSeconds > MaxDurationSeconds || stage.TargetLoad < 1 || (config.Mode == LoadModeVU && stage.TargetLoad > MaxVUs) || (config.Mode == LoadModeRPS && stage.TargetLoad > MaxRPS) {
			return errors.New("invalid load stage")
		}
	}
	for _, task := range config.Scenario {
		if task.Name == "" || task.Prompt == "" || task.Weight < 1 || task.Weight > 100 || task.ThinkTimeMillis < 0 || task.ThinkTimeMillis > 60000 || task.MaxTokens < 0 || task.MaxTokens > MaxModelTokens {
			return errors.New("invalid scenario task")
		}
	}
	return nil
}
