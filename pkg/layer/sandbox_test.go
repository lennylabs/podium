package layer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Spec: §4.6, §7.3.1 — the sandbox-profile reason is fixed text that names no
// profile value, the constraint is sandbox_profile_conflict, and the sentinel
// carries the reason and matches through a wrapped chain.
func TestSandboxProfileConflict_ReasonConstraintAndSentinel(t *testing.T) {
	t.Parallel()
	want := "extends: sandbox_profile differs from the profile the parent sets, and neither is unrestricted; omit sandbox_profile to inherit the parent's profile"
	if SandboxProfileConflictReason != want {
		t.Errorf("SandboxProfileConflictReason = %q, want %q", SandboxProfileConflictReason, want)
	}
	if !strings.HasPrefix(SandboxProfileConflictReason, extendsReasonPrefix) {
		t.Errorf("SandboxProfileConflictReason = %q, want prefix %q", SandboxProfileConflictReason, extendsReasonPrefix)
	}
	// The reason itself contains "unrestricted", so the non-disclosure check
	// covers only the restricting values a parent could set.
	for _, value := range []string{"read-only-fs", "network-isolated", "seccomp-strict"} {
		if strings.Contains(SandboxProfileConflictReason, value) {
			t.Errorf("SandboxProfileConflictReason = %q, must not name %q", SandboxProfileConflictReason, value)
		}
	}
	if SandboxProfileConflictConstraint != "sandbox_profile_conflict" {
		t.Errorf("SandboxProfileConflictConstraint = %q, want sandbox_profile_conflict", SandboxProfileConflictConstraint)
	}
	if got := ErrSandboxProfileConflict.Error(); got != SandboxProfileConflictReason {
		t.Errorf("ErrSandboxProfileConflict.Error() = %q, want %q", got, SandboxProfileConflictReason)
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrSandboxProfileConflict), ErrSandboxProfileConflict) {
		t.Error("errors.Is did not match a wrapped ErrSandboxProfileConflict")
	}
}
