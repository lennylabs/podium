package typeprovider_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/typeprovider"
)

func TestRegistry_BuiltinProviderHasIDAndNoValidate(t *testing.T) {
	t.Parallel()
	provider, ok := typeprovider.Default.Get(manifest.TypeSkill)
	if !ok {
		t.Fatal("default skill provider not present")
	}
	if id := provider.ID(); id == "" {
		t.Errorf("ID returned empty string")
	}
	if diags := provider.Validate(&manifest.Artifact{Type: manifest.TypeSkill}); diags != nil {
		t.Errorf("builtin Validate returned %v, want nil", diags)
	}
}
