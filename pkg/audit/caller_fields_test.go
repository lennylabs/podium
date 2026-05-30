package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec §8.1: the structured caller attributes (email, groups, public-mode
// flag, public-mode network) participate in the tamper-evident hash chain,
// so each one must change the canonical body.
func TestCanonicalBody_IncludesCallerFields(t *testing.T) {
	base := Event{Type: EventArtifactLoaded, Caller: "alice"}
	cases := map[string]func(*Event){
		"caller_email":       func(e *Event) { e.CallerEmail = "alice@acme.com" },
		"caller_groups":      func(e *Event) { e.CallerGroups = []string{"eng"} },
		"caller_public_mode": func(e *Event) { e.PublicMode = true },
		"caller_network":     func(e *Event) { e.CallerNetwork = &CallerNetwork{SourceIP: "203.0.113.7"} },
	}
	want := string(base.canonicalBody())
	for name, mutate := range cases {
		got := base
		mutate(&got)
		if string(got.canonicalBody()) == want {
			t.Errorf("%s must change the canonical body (tamper-evidence)", name)
		}
	}
}

// spec §8.1: caller attributes round-trip through the FileSink JSON-Lines log
// with the documented field names, and the chain verifies.
func TestFileSink_CallerFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	authed := Event{
		Type: EventArtifactLoaded, TraceID: "abc123", Caller: "alice",
		CallerEmail: "alice@acme.com", CallerGroups: []string{"eng", "sec"},
	}
	pub := Event{
		Type: EventDomainLoaded, TraceID: "def456", Caller: "system:public",
		PublicMode: true, CallerNetwork: &CallerNetwork{SourceIP: "203.0.113.7", ForwardedUser: "bob"},
	}
	if err := sink.Append(ctx, authed); err != nil {
		t.Fatal(err)
	}
	if err := sink.Append(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("chain verify: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"trace_id":"abc123"`,
		`"caller_email":"alice@acme.com"`,
		`"caller_groups":["eng","sec"]`,
		`"caller_public_mode":true`,
		`"caller_network":{"source_ip":"203.0.113.7","forwarded_user":"bob"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %s\nlog:\n%s", want, got)
		}
	}
	// The authenticated event must not emit public-mode-only fields.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if strings.Contains(lines[0], "caller_public_mode") || strings.Contains(lines[0], "caller_network") {
		t.Errorf("authenticated event leaked public-mode fields: %s", lines[0])
	}
}

// spec §8.1 / §8.6: tampering with a recorded caller attribute breaks the
// hash chain, since the attributes are part of the canonical body.
func TestFileSink_TamperedCallerEmailBreaksChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sink.Append(ctx, Event{Type: EventArtifactLoaded, Caller: "alice", CallerEmail: "alice@acme.com"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("baseline verify: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), "alice@acme.com", "mallory@evil.com", 1)
	if tampered == string(data) {
		t.Fatal("tamper precondition failed: email not found in log")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Verify(ctx); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken after tampering caller_email, got %v", err)
	}
}
