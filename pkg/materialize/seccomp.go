package materialize

import (
	_ "embed"
	"path/filepath"
)

// seccompStrict is the baseline seccomp profile Podium ships for
// sandbox_profile: seccomp-strict (spec §4.4.1, which describes the
// profile as "a baseline profile shipped with Podium"). It is an
// OCI/Docker seccomp JSON document (LinuxSeccomp): a strict syscall
// allowlist with a default ERRNO action. Embedding it makes the
// baseline travel inside the binary so a host with sandbox capability
// can honor the profile without a separate download.
//
//go:embed seccomp-strict.json
var seccompStrict []byte

// SandboxProfileSeccompStrict is the sandbox_profile value whose baseline
// Podium ships (§4.4.1). The other profiles (read-only-fs,
// network-isolated, unrestricted) describe host constraints rather than a
// syscall allowlist, so no profile document ships for them.
const SandboxProfileSeccompStrict = "seccomp-strict"

// SeccompStrictProfile returns a copy of the baseline seccomp profile for
// sandbox_profile: seccomp-strict (§4.4.1). The bytes are an OCI/Docker
// seccomp JSON document the host applies when it honors the profile.
func SeccompStrictProfile() []byte {
	out := make([]byte, len(seccompStrict))
	copy(out, seccompStrict)
	return out
}

// SandboxBaselineProfile returns the baseline profile document Podium
// ships for the named sandbox_profile and whether one exists. Only
// seccomp-strict has a shipped baseline (§4.4.1).
func SandboxBaselineProfile(profile string) ([]byte, bool) {
	if profile == SandboxProfileSeccompStrict {
		return SeccompStrictProfile(), true
	}
	return nil, false
}

// WriteSandboxProfile writes the baseline profile Podium ships for the
// named sandbox_profile under destination so a host with sandbox
// capability can honor it (§4.4.1). The file lands at
// .podium/<profile>.json relative to destination, written atomically
// (temp + rename) under the same containment check as Write. It returns
// the absolute path written and true; when no baseline ships for the
// profile it returns ("", false, nil) and writes nothing.
func WriteSandboxProfile(destination, profile string) (string, bool, error) {
	data, ok := SandboxBaselineProfile(profile)
	if !ok {
		return "", false, nil
	}
	if destination == "" {
		return "", false, ErrEmptyDestination
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return "", false, err
	}
	rel := filepath.Join(".podium", profile+".json")
	full, err := resolveSandboxedPath(absDest, rel)
	if err != nil {
		return "", false, err
	}
	if err := writeAtomic(full, data, 0o644); err != nil {
		return "", false, err
	}
	return full, true, nil
}
