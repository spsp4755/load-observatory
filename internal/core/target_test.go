package core

import "testing"

func TestValidateTargetRejectsPublicHost(t *testing.T) {
	if err := ValidateTarget("https://example.com/v1/chat/completions"); err == nil {
		t.Fatal("public host accepted")
	}
}

func TestValidateTargetAcceptsPrivateAddress(t *testing.T) {
	if err := ValidateTarget("http://10.20.30.40:8080/health"); err != nil {
		t.Fatalf("private target rejected: %v", err)
	}
}

func TestValidateTargetWithAllowedHostSuffixesAcceptsConfiguredGateway(t *testing.T) {
	allowed := []string{".internal", ".kubagents-ofc.koreacb.com"}
	if err := ValidateTargetWithAllowedHostSuffixes("https://proxy-gateway.kubagents-ofc.koreacb.com/v1/chat/completions", allowed); err != nil {
		t.Fatalf("configured target rejected: %v", err)
	}
}

func TestValidateTargetWithAllowedHostSuffixesRejectsLookalikeDomains(t *testing.T) {
	allowed := []string{".kubagents-ofc.koreacb.com"}
	for _, rawURL := range []string{
		"https://evilkubagents-ofc.koreacb.com/v1/chat/completions",
		"https://proxy-gateway.kubagents-ofc.koreacb.com.attacker.example/v1/chat/completions",
	} {
		if err := ValidateTargetWithAllowedHostSuffixes(rawURL, allowed); err == nil {
			t.Fatalf("lookalike target accepted: %s", rawURL)
		}
	}
}

func TestValidateRunConfigRejectsTooManyVUs(t *testing.T) {
	config := RunConfig{Mode: LoadModeVU, VUs: MaxVUs + 1, DurationSeconds: 60}
	if err := ValidateRunConfig(config); err == nil {
		t.Fatal("VU limit missing")
	}
}

func TestValidateRunConfigRejectsTooManyModelTokens(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: MaxModelTokens + 1})
	if err == nil {
		t.Fatal("expected max token validation error")
	}
}

func TestValidateRunConfigAcceptsOneMillionModelTokens(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 1000000})
	if err != nil {
		t.Fatalf("one million tokens rejected: %v", err)
	}
}

func TestValidateRunConfigAcceptsOrderedTraceReplay(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeRPS, RPS: 1, DurationSeconds: 5, MaxTokens: 32, TraceTimeScale: 2, Trace: []TraceEvent{{TimestampMillis: 0, PromptTokens: 32, MaxTokens: 8}, {TimestampMillis: 1000, Prompt: "real request", MaxTokens: 16}}})
	if err != nil {
		t.Fatalf("valid trace rejected: %v", err)
	}
}

func TestValidateRunConfigRejectsUnorderedTraceReplay(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeRPS, RPS: 1, DurationSeconds: 5, MaxTokens: 32, Trace: []TraceEvent{{TimestampMillis: 1000}, {TimestampMillis: 500}}})
	if err == nil {
		t.Fatal("unordered trace accepted")
	}
}

func TestValidateRunConfigRejectsUnknownCachePolicy(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 1, CachePolicy: "unknown"})
	if err == nil {
		t.Fatal("unknown cache policy accepted")
	}
}

func TestValidateRunConfigAcceptsWeightedScenarioAndStages(t *testing.T) {
	err := ValidateRunConfig(RunConfig{Mode: LoadModeRPS, RPS: 10, DurationSeconds: 5, MaxTokens: 1, MaxInFlight: 3, WarmupRequests: 2, CooldownSeconds: 1, Stages: []LoadStage{{DurationSeconds: 2, TargetLoad: 5}}, Scenario: []ScenarioTask{{Name: "coding", Prompt: "write code", Weight: 3, ThinkTimeMillis: 10}}})
	if err != nil {
		t.Fatal(err)
	}
}
