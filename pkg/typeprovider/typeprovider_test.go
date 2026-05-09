package typeprovider_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/typeprovider"
)

// Spec: §9 TypeProvider SPI — the default registry pre-registers
// every first-class type so callers can look them up without
// extra setup.
func TestDefaultRegistry_HasAllFirstClassTypes(t *testing.T) {
	for _, want := range []manifest.ArtifactType{
		manifest.TypeSkill,
		manifest.TypeAgent,
		manifest.TypeContext,
		manifest.TypeCommand,
		manifest.TypeRule,
		manifest.TypeHook,
		manifest.TypeMCPServer,
	} {
		if _, ok := typeprovider.Default.Get(want); !ok {
			t.Errorf("type %q is not registered", want)
		}
	}
}

// Spec: §9 — registering the same type twice fails so
// deployments fail loud on conflicts.
func TestRegistry_RejectsDuplicateRegistration(t *testing.T) {
	r := typeprovider.NewRegistry()
	if err := r.Register(macroProvider{}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(macroProvider{})
	if err == nil {
		t.Fatal("err = nil, want duplicate-registration error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %v, want already-registered", err)
	}
}

// Spec: §9 — Validate dispatches to the registered provider's
// Validate when the artifact's type matches.
func TestRegistry_ValidateDispatches(t *testing.T) {
	r := typeprovider.NewRegistry()
	if err := r.Register(macroProvider{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	a := &manifest.Artifact{Type: "macro"}
	got := r.Validate(a)
	if len(got) != 1 || got[0].Code != "macro.no-name" {
		t.Errorf("got %+v, want one macro.no-name diagnostic", got)
	}
}

// Spec: §9 — Types returns every registered type id sorted.
func TestRegistry_TypesIsSorted(t *testing.T) {
	r := typeprovider.NewRegistry()
	_ = r.Register(macroProvider{})
	_ = r.Register(otherProvider{})
	got := r.Types()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "macro" || got[1] != "z-other" {
		t.Errorf("Types() = %v, want sorted [macro z-other]", got)
	}
}

type macroProvider struct{}

func (macroProvider) ID() string                       { return "macro" }
func (macroProvider) Type() manifest.ArtifactType      { return "macro" }
func (macroProvider) Validate(a *manifest.Artifact) []typeprovider.Diagnostic {
	if a.Name == "" {
		return []typeprovider.Diagnostic{{Severity: "error", Code: "macro.no-name", Message: "macro: name required"}}
	}
	return nil
}

type otherProvider struct{}

func (otherProvider) ID() string                                            { return "other" }
func (otherProvider) Type() manifest.ArtifactType                           { return "z-other" }
func (otherProvider) Validate(*manifest.Artifact) []typeprovider.Diagnostic { return nil }
