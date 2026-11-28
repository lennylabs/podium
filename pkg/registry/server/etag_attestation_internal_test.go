package server

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §7.2 — the entity tag is the content hash for a response with no
// attestation member, the content hash with a fingerprint of the members
// otherwise, which changes with them, and none for a revalidation whose
// members could not be read.
func TestValidatorFor_AttestationMembers(t *testing.T) {
	t.Parallel()
	plain := &core.LoadArtifactResult{ContentHash: "sha256:aa"}
	verified := &core.LoadArtifactResult{ContentHash: "sha256:aa", AttestationOutcome: &core.ServedOutcome{Outcome: "verified"}}
	failed := &core.LoadArtifactResult{ContentHash: "sha256:aa", AttestationOutcome: &core.ServedOutcome{Outcome: "failed"}}
	if validatorFor(plain) != `"sha256:aa"` || !strings.HasPrefix(validatorFor(verified), `"sha256:aa+`) ||
		len(validatorFor(verified)) != len(`"sha256:aa+"`)+32 || validatorFor(verified) == validatorFor(failed) {
		t.Errorf("tags %s %s %s", validatorFor(plain), validatorFor(verified), validatorFor(failed))
	}
	if validatorFor(&core.LoadArtifactResult{ContentHash: "sha256:aa", ProvenanceUnavailable: true}) != "" ||
		validatorFor(&core.LoadArtifactResult{AttestationOutcome: verified.AttestationOutcome}) != "" {
		t.Error("a tag where none applies")
	}
}
