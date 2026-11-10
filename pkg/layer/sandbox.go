package layer

import "errors"

// SandboxProfileConflictConstraint is the details.constraint value of a §4.6
// sandbox-profile rejection.
//
// Spec: §4.6, §7.3.1
const SandboxProfileConflictConstraint = "sandbox_profile_conflict"

// SandboxProfileConflictReason is the reason both modes give a child whose
// sandbox_profile and its parent's resolved sandbox_profile both restrict and
// differ. It names neither value and no parent, so it discloses nothing the
// §4.6 hidden-parent rule withholds.
//
// Spec: §4.6
const SandboxProfileConflictReason = extendsReasonPrefix + "sandbox_profile differs from the profile the parent sets, and neither is unrestricted; omit sandbox_profile to inherit the parent's profile"

// ErrSandboxProfileConflict is the error a filesystem walk with no drop
// callback fails with for a §4.6 sandbox-profile refusal, matched with errors.Is.
//
// Spec: §4.6, §13.11.3
var ErrSandboxProfileConflict = errors.New(SandboxProfileConflictReason)
