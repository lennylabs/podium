package materialize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seccompDoc is the subset of the OCI/Docker seccomp JSON the test
// inspects to confirm the shipped profile is a real strict allowlist.
type seccompDoc struct {
	DefaultAction string `json:"defaultAction"`
	Syscalls      []struct {
		Names  []string `json:"names"`
		Action string   `json:"action"`
	} `json:"syscalls"`
}

// Spec: §4.4.1 — Podium ships a baseline seccomp profile for
// sandbox_profile: seccomp-strict ("Strict syscall allowlist (per a
// baseline profile shipped with Podium)"). The shipped document must be a
// real strict allowlist: default-deny, with safe syscalls allowed and
// privileged ones (sockets, ptrace) left to the default ERRNO action.
func TestSeccompStrictProfile_IsStrictAllowlist(t *testing.T) {
	t.Parallel()
	var doc seccompDoc
	if err := json.Unmarshal(SeccompStrictProfile(), &doc); err != nil {
		t.Fatalf("shipped seccomp profile is not valid JSON: %v", err)
	}
	if doc.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Errorf("defaultAction = %q, want SCMP_ACT_ERRNO (default-deny)", doc.DefaultAction)
	}
	allowed := map[string]bool{}
	for _, group := range doc.Syscalls {
		if group.Action != "SCMP_ACT_ALLOW" {
			continue
		}
		for _, n := range group.Names {
			allowed[n] = true
		}
	}
	// A materialized script needs basic file I/O, memory, and process
	// lifecycle.
	for _, want := range []string{"read", "write", "openat", "mmap", "exit_group"} {
		if !allowed[want] {
			t.Errorf("strict allowlist is missing required syscall %q", want)
		}
	}
	// Networking and tracing must not be in the baseline allowlist; they
	// fall through to the default ERRNO action.
	for _, deny := range []string{"socket", "connect", "ptrace", "mount"} {
		if allowed[deny] {
			t.Errorf("strict allowlist must not permit privileged syscall %q", deny)
		}
	}
}

// Spec: §4.4.1 — only seccomp-strict has a shipped baseline; the other
// profiles describe host constraints, not a syscall allowlist.
func TestSandboxBaselineProfile_OnlySeccompStrict(t *testing.T) {
	t.Parallel()
	if data, ok := SandboxBaselineProfile(SandboxProfileSeccompStrict); !ok || len(data) == 0 {
		t.Errorf("seccomp-strict should have a shipped baseline, got ok=%v len=%d", ok, len(data))
	}
	for _, p := range []string{"unrestricted", "read-only-fs", "network-isolated", ""} {
		if _, ok := SandboxBaselineProfile(p); ok {
			t.Errorf("profile %q should not have a shipped baseline", p)
		}
	}
}

// SeccompStrictProfile returns a copy: mutating the result must not
// corrupt the embedded bytes handed to the next caller.
func TestSeccompStrictProfile_ReturnsCopy(t *testing.T) {
	t.Parallel()
	first := SeccompStrictProfile()
	if len(first) == 0 {
		t.Fatal("profile is empty")
	}
	for i := range first {
		first[i] = 0
	}
	second := SeccompStrictProfile()
	if second[0] == 0 {
		t.Error("mutating a returned profile corrupted the embedded bytes")
	}
}

// Spec: §4.4.1 — the materialization layer delivers the baseline profile
// so the host can honor seccomp-strict. WriteSandboxProfile writes it
// atomically under .podium/ and is a no-op for profiles without a
// baseline.
func TestWriteSandboxProfile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	path, ok, err := WriteSandboxProfile(dest, SandboxProfileSeccompStrict)
	if err != nil || !ok {
		t.Fatalf("write seccomp-strict: ok=%v err=%v", ok, err)
	}
	if want := filepath.Join(dest, ".podium", "seccomp-strict.json"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written profile: %v", err)
	}
	if string(onDisk) != string(SeccompStrictProfile()) {
		t.Error("written profile bytes differ from the shipped baseline")
	}

	// A profile without a baseline writes nothing.
	p, ok, err := WriteSandboxProfile(dest, "read-only-fs")
	if err != nil || ok || p != "" {
		t.Errorf("read-only-fs should be a no-op, got path=%q ok=%v err=%v", p, ok, err)
	}

	// An empty destination is rejected (defensive, matches Write).
	if _, _, err := WriteSandboxProfile("", SandboxProfileSeccompStrict); err == nil {
		t.Error("empty destination should error")
	}
}
