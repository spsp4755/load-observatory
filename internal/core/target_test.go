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
