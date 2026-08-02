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
