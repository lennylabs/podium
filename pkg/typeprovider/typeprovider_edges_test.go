package typeprovider_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/typeprovider"
)

type emptyTypeProvider struct{}

func (emptyTypeProvider) ID() string                                           { return "empty" }
func (emptyTypeProvider) Type() manifest.ArtifactType                          { return "" }
func (emptyTypeProvider) Validate(*manifest.Artifact) []typeprovider.Diagnostic { return nil }

// Spec: §9 — Register rejects a nil provider so a misconfigured
// import doesn't silently land an inert entry.
func TestRegister_RejectsNilProvider(t *testing.T) {
	t.Parallel()
	r := typeprovider.NewRegistry()
	err := r.Register(nil)
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("err = %v, want nil-provider rejection", err)
	}
}

// Spec: §9 — Register rejects a provider whose Type() is empty
// because the registry keys on type id; an empty type would
// shadow other registrations.
func TestRegister_RejectsEmptyType(t *testing.T) {
	t.Parallel()
	r := typeprovider.NewRegistry()
	err := r.Register(emptyTypeProvider{})
	if err == nil || !strings.Contains(err.Error(), "empty type") {
		t.Errorf("err = %v, want empty-type rejection", err)
	}
}

// Spec: §9 — Validate on a registry without any matching
// provider returns nil (other lint rules cover the unknown-type
// case).
func TestValidate_NoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	r := typeprovider.NewRegistry()
	diags := r.Validate(&manifest.Artifact{Type: "macro"})
	if diags != nil {
		t.Errorf("diags = %v, want nil", diags)
	}
}

// Spec: §9 — Validate on nil input is a no-op.
func TestValidate_NilArtifactIsNoop(t *testing.T) {
	t.Parallel()
	r := typeprovider.NewRegistry()
	if got := r.Validate(nil); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}
