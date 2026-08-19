package main

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/readiness"
)

func TestFormatDoctorProviderProbeReportsModelAvailability(t *testing.T) {
	out := formatDoctorProviderProbe(readiness.Check{
		ID:      "provider_model",
		OK:      true,
		Message: `omlx model "local-coder" available (2 listed)`,
		Latency: 17,
	})
	for _, want := range []string{"LLM ok", "local-coder", "2 listed", "17 ms"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatDoctorProviderProbeExplainsMissingModel(t *testing.T) {
	out := formatDoctorProviderProbe(readiness.Check{
		ID:       "provider_model",
		OK:       false,
		Severity: "critical",
		Message:  `omlx model "missing" not listed; available: other`,
		Endpoint: "http://127.0.0.1:8000/v1",
		FixLabel: "Select an installed model or update the stack",
		FixHint:  "Use Settings -> Model Stack, or set model to one of the listed local models.",
	})
	for _, want := range []string{
		"LLM check failed",
		"missing",
		"endpoint: http://127.0.0.1:8000/v1",
		"tip: Use Settings",
		"fix: Select an installed model",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
