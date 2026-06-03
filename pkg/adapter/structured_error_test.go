package adapter

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/spi"
)

// TestStructuredError_RegistryGet asserts the HarnessAdapter lookup returns a
// structured *spi.Error carrying the §6.10 config.unknown_harness code, per the
// §9.3 "Structured errors" constraint. The code prefix is preserved in the
// message so the §6.7 unknown-harness wire path is unchanged.
//
// spec: §9.3 "Structured errors"; §6.10 config.unknown_harness.
func TestStructuredError_RegistryGet(t *testing.T) {
	t.Parallel()
	_, err := DefaultRegistry().Get("not-a-real-harness")
	if err == nil {
		t.Fatal("Get(unknown) returned nil error")
	}
	e, ok := spi.AsError(err)
	if !ok {
		t.Fatalf("Get error is not a structured *spi.Error: %T %v", err, err)
	}
	if e.Code != "config.unknown_harness" {
		t.Errorf("Code = %q, want config.unknown_harness", e.Code)
	}
	if e.Retryable {
		t.Errorf("Retryable = true, want false for an unknown harness")
	}
	if !strings.Contains(e.Error(), "config.unknown_harness") {
		t.Errorf("Error() = %q, want it to contain the code", e.Error())
	}
	if e.Details["harness"] != "not-a-real-harness" {
		t.Errorf("Details[harness] = %v, want not-a-real-harness", e.Details["harness"])
	}
}

// TestStructuredError_TranslationError asserts the §6.9 untranslatable-artifact
// failure is a structured *spi.Error carrying materialize.untranslatable and
// the offending harness in Details. claude-desktop is ✗ for rule_mode: glob.
//
// spec: §9.3 "Structured errors"; §6.9 / §6.10 materialize.untranslatable.
func TestStructuredError_TranslationError(t *testing.T) {
	t.Parallel()
	rule := &manifest.Artifact{Type: manifest.TypeRule, RuleMode: manifest.RuleModeGlob}
	err := TranslationError("claude-desktop", rule)
	if err == nil {
		t.Fatal("TranslationError returned nil for a ✗ cell")
	}
	e, ok := spi.AsError(err)
	if !ok {
		t.Fatalf("TranslationError is not a structured *spi.Error: %T %v", err, err)
	}
	if e.Code != "materialize.untranslatable" {
		t.Errorf("Code = %q, want materialize.untranslatable", e.Code)
	}
	if e.Details["harness"] != "claude-desktop" {
		t.Errorf("Details[harness] = %v, want claude-desktop", e.Details["harness"])
	}
	if !strings.Contains(e.Error(), "materialize.untranslatable") {
		t.Errorf("Error() = %q, want it to contain the code", e.Error())
	}
}
