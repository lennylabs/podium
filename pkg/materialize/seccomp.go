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
// .podium/<profile>.json relative to destination, written through
// WriteStateFile: a .podium directory that is a symbolic link refuses the
// write, and the temporary is created exclusively, so neither a committed
// .podium link nor a committed link at the temporary name carries the write
// outside the destination (§6.6 step 5). It returns the absolute path written
// and true; when no baseline ships for the profile it returns ("", false,
// nil) and writes nothing.
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
	rel := sandboxProfilePath(profile)
	if err := WriteStateFile(absDest, rel, data, 0o644); err != nil {
		return "", false, err
	}
	return filepath.Join(absDest, filepath.FromSlash(rel)), true, nil
}

// CheckSandboxProfile returns the refusal WriteSandboxProfile would return for
// profile under destination, such as a .podium directory that is a symbolic
// link, without writing anything. A profile with no shipped baseline passes.
// podium-mcp calls it before it writes an artifact's files, so a load whose
// sandbox profile the write would refuse leaves the destination unchanged
// rather than holding the artifact without its profile.
//
// Spec: §6.6 step 5, §4.4.1
func CheckSandboxProfile(destination, profile string) error {
	if _, ok := SandboxBaselineProfile(profile); !ok {
		return nil
	}
	return CheckStateFile(destination, sandboxProfilePath(profile))
}

// sandboxProfilePath is the slash-separated path, relative to the
// destination, where the baseline for profile lands.
func sandboxProfilePath(profile string) string {
	return ".podium/" + profile + ".json"
}
