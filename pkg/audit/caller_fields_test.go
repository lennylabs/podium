package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spec §8.1: the structured caller attributes (email, groups, public-mode
// flag, public-mode network, and each caller.client member) and the tenant
// participate in the tamper-evident hash chain, so each one must change the
// canonical body. A client member enters as its own caller_client_<member>
// part, and a client with no members adds nothing (§8.6).
//
// Spec: §8.1, §8.6
func TestCanonicalBody_IncludesCallerFields(t *testing.T) {
	base := Event{Type: EventArtifactLoaded, Caller: "alice"}
	cases := map[string]func(*Event){
		"caller_email":               func(e *Event) { e.CallerEmail = "alice@acme.com" },
		"caller_groups":              func(e *Event) { e.CallerGroups = []string{"eng"} },
		"caller_public_mode":         func(e *Event) { e.PublicMode = true },
		"caller_network":             func(e *Event) { e.CallerNetwork = &CallerNetwork{SourceIP: "203.0.113.7"} },
		"tenant":                     func(e *Event) { e.Tenant = "acme" },
		"caller_client_kind":         func(e *Event) { e.CallerClient = &CallerClient{Kind: "v"} },
		"caller_client_name":         func(e *Event) { e.CallerClient = &CallerClient{Name: "v"} },
		"caller_client_version":      func(e *Event) { e.CallerClient = &CallerClient{Version: "v"} },
		"caller_client_host":         func(e *Event) { e.CallerClient = &CallerClient{Host: "v"} },
		"caller_client_host_version": func(e *Event) { e.CallerClient = &CallerClient{HostVersion: "v"} },
		"caller_client_harness":      func(e *Event) { e.CallerClient = &CallerClient{Harness: "v"} },
	}
	want := string(base.canonicalBody())
	for name, mutate := range cases {
		got := base
		mutate(&got)
		body := string(got.canonicalBody())
		if body == want {
			t.Errorf("%s must change the canonical body (tamper-evidence)", name)
		}
		if strings.HasPrefix(name, "caller_client_") && !strings.Contains(body, name+"=v") {
			t.Errorf("canonical body lacks the %s=v part", name)
		}
	}
	empty := base
	empty.CallerClient = &CallerClient{}
	if string(empty.canonicalBody()) != want {
		t.Errorf("a client with no members must not change the canonical body")
	}
}

// spec §8.1 / §13.2.2 / §13.10: the caller
// identity attributes serialize under a nested "caller" object whose keys
// are the dotted names the spec illustrates (caller.identity, caller.email,
// caller.groups, caller.network, caller.public_mode), and the chain
// verifies. A SIEM consumer keying on caller.public_mode resolves the
// nested field directly. The tenant attribute serializes as a top-level
// "tenant" key, omitted when empty.
//
// Spec: §8.1
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
		Tenant: "acme",
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
		`"caller":{"identity":"alice","email":"alice@acme.com","groups":["eng","sec"]}`,
		`"caller":{"identity":"system:public","network":{"source_ip":"203.0.113.7","forwarded_user":"bob"},"public_mode":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %s\nlog:\n%s", want, got)
		}
	}
	assertTenantWireKey(t, got)
	// Neither event carries a client, so neither writes caller.client.
	if strings.Contains(got, `"client"`) {
		t.Errorf("events without a CallerClient wrote caller.client\nlog:\n%s", got)
	}
	// The spec's flat snake_case keys must no longer appear; the dotted names
	// live inside the nested caller object instead.
	for _, gone := range []string{"caller_email", "caller_groups", "caller_public_mode", "caller_network"} {
		if strings.Contains(got, gone) {
			t.Errorf("audit log still emits the flat key %q (should be nested under caller)\nlog:\n%s", gone, got)
		}
	}
	// The authenticated event must not emit public-mode-only fields.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if strings.Contains(lines[0], "public_mode") || strings.Contains(lines[0], "network") {
		t.Errorf("authenticated event leaked public-mode fields: %s", lines[0])
	}
}

// assertTenantWireKey checks the §8.1 tenant attribute on the two lines
// TestFileSink_CallerFieldsRoundTrip writes: the labeled event carries a
// top-level "tenant" key that eventFromJSON reads back, and the unlabeled
// event writes no "tenant" key at all.
func assertTenantWireKey(t *testing.T, log string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), log)
	}
	var labeled jsonEvent
	if err := json.Unmarshal([]byte(lines[0]), &labeled); err != nil {
		t.Fatalf("decode labeled line: %v", err)
	}
	if labeled.Tenant != "acme" {
		t.Errorf("top-level tenant = %q, want acme\nline: %s", labeled.Tenant, lines[0])
	}
	if got := eventFromJSON(labeled).Tenant; got != "acme" {
		t.Errorf("eventFromJSON tenant = %q, want acme", got)
	}
	if strings.Contains(lines[1], `"tenant"`) {
		t.Errorf("unlabeled event wrote a tenant key: %s", lines[1])
	}
}

// legacyUnlabeledLine is a JSON line written by the FileSink before the
// §8.1 tenant attribute existed, with the hash that writer stored. It is
// committed as a literal so a change to canonicalBody that alters the hash
// of an unlabeled record fails here instead of failing Verify on every
// existing log.
const legacyUnlabeledLine = `{"type":"artifact.loaded","timestamp":"2026-09-01T12:00:00Z","trace_id":"abc123","caller":{"identity":"alice","email":"alice@acme.com","groups":["eng"]},"target":"finance/close","context":{"version":"1.0.0"},"hash":"c202f8c770967ed85941b279e852cadb007f5abc8487d74a048b2e701273e10f"}`

// An unlabeled record keeps the hash the pre-tenant writer stored, and a
// log holding it still verifies with no migration. A record with a
// caller.client appended after it leaves the mixed log verifiable, so a
// registry that starts recording the attribute needs no migration either.
//
// Spec: §8.1, §8.6
func TestEvent_UnlabeledHashUnchanged(t *testing.T) {
	var je jsonEvent
	if err := json.Unmarshal([]byte(legacyUnlabeledLine), &je); err != nil {
		t.Fatalf("decode legacy line: %v", err)
	}
	e := eventFromJSON(je)
	if e.Tenant != "" {
		t.Fatalf("legacy line decoded with tenant %q, want empty", e.Tenant)
	}
	sum := sha256.Sum256(append(e.canonicalBody(), []byte(e.PrevHash)...))
	if got := hex.EncodeToString(sum[:]); got != je.Hash {
		t.Errorf("recomputed hash = %s, want stored %s", got, je.Hash)
	}
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte(legacyUnlabeledLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Fatalf("legacy log failed verify: %v", err)
	}
	if err := sink.Append(context.Background(), Event{
		Type: EventArtifactLoaded, Caller: "alice",
		CallerClient: &CallerClient{Kind: "mcp", Name: "podium-mcp", Host: "Claude Code"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(context.Background()); err != nil {
		t.Fatalf("mixed legacy and caller.client log failed verify: %v", err)
	}
}

// spec §8.1 / §13.2.2 / §13.10: the nested
// caller object decodes through dotted-path field access, which is how a
// SIEM consumer queries caller.identity, caller.public_mode, and
// caller.client.host_version. Asserting against a typed decode pins the exact
// key names the spec names.
//
// Spec: §8.1
func TestFileSink_CallerDottedKeysDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sink.Append(ctx, Event{
		Type: EventDomainLoaded, Caller: "system:public", PublicMode: true,
		CallerNetwork: &CallerNetwork{SourceIP: "203.0.113.7", ForwardedUser: "bob"},
		CallerClient:  &CallerClient{Kind: "mcp", Host: "Claude Code", HostVersion: "2.1.0"},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Caller struct {
			Identity   string `json:"identity"`
			PublicMode bool   `json:"public_mode"`
			Network    struct {
				SourceIP      string `json:"source_ip"`
				ForwardedUser string `json:"forwarded_user"`
			} `json:"network"`
			Client struct {
				Kind        string `json:"kind"`
				Host        string `json:"host"`
				HostVersion string `json:"host_version"`
			} `json:"client"`
		} `json:"caller"`
	}
	line := strings.TrimSpace(string(data))
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Caller.Identity != "system:public" {
		t.Errorf("caller.identity = %q, want system:public", rec.Caller.Identity)
	}
	if !rec.Caller.PublicMode {
		t.Errorf("caller.public_mode = false, want true")
	}
	if rec.Caller.Network.SourceIP != "203.0.113.7" {
		t.Errorf("caller.network.source_ip = %q, want 203.0.113.7", rec.Caller.Network.SourceIP)
	}
	if rec.Caller.Network.ForwardedUser != "bob" {
		t.Errorf("caller.network.forwarded_user = %q, want bob", rec.Caller.Network.ForwardedUser)
	}
	if c := rec.Caller.Client; c.Kind != "mcp" || c.Host != "Claude Code" || c.HostVersion != "2.1.0" {
		t.Errorf("caller.client = %+v, want kind mcp, host Claude Code, host_version 2.1.0", c)
	}
}

// An event whose only caller attribute is the client still writes the caller
// object, so caller.client reaches the file and the record verifies. The
// callerForJSON nil check lists every caller attribute; leaving the client out
// of it drops the key on write and breaks Verify for that record.
//
// Spec: §8.1, §8.6
func TestFileSink_ClientOnlyCallerSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sink.Append(ctx, Event{Type: EventArtifactLoaded, CallerClient: &CallerClient{Kind: "sync", Harness: "codex"}}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"caller":{"client":{"kind":"sync","harness":"codex"}}`) {
		t.Errorf("file line lacks caller.client: %s", data)
	}
}

// Every sink normalizes the client before it hashes, so a NUL byte in a
// member is stored sanitized, the chain verifies, and the caller's own value
// is left unmodified.
//
// Spec: §7.2.2, §8.1, §8.6
func TestSinks_NormalizeCallerClient(t *testing.T) {
	ctx := context.Background()
	raw := Event{Type: EventArtifactLoaded, Caller: "alice", CallerClient: &CallerClient{Kind: "MCP", Host: "evil\x00host"}}
	want := CallerClient{Kind: "other", Host: "evil_host"}

	path := filepath.Join(t.TempDir(), "audit.log")
	fs, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Append(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := fs.Verify(ctx); err != nil {
		t.Fatalf("FileSink.Verify: %v", err)
	}
	events, err := readAllEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].CallerClient == nil || *events[0].CallerClient != want {
		t.Fatalf("file records = %+v, want one with client %+v", events, want)
	}

	mem := NewMemory()
	if err := mem.Append(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := mem.Verify(ctx); err != nil {
		t.Fatalf("Memory.Verify: %v", err)
	}
	if got := mem.Events()[0].CallerClient; got == nil || *got != want {
		t.Errorf("memory record client = %+v, want %+v", got, want)
	}
	if raw.CallerClient.Host != "evil\x00host" {
		t.Errorf("Append mutated the caller's CallerClient")
	}
}

// spec §8.1 / §8.6: tampering with a recorded caller attribute breaks the
// hash chain, since the attributes are part of the canonical body.
//
// Spec: §8.1, §8.6
func TestFileSink_TamperedCallerEmailBreaksChain(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
	}{
		{"caller.email", `"email":"alice@acme.com"`, `"email":"mallory@evil.com"`},
		{"caller.client.host", `"host":"Claude Code"`, `"host":"Cursor"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.log")
			sink, err := NewFileSink(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := sink.Append(ctx, Event{
				Type: EventArtifactLoaded, Caller: "alice", CallerEmail: "alice@acme.com",
				CallerClient: &CallerClient{Kind: "mcp", Host: "Claude Code"},
			}); err != nil {
				t.Fatal(err)
			}
			if err := sink.Verify(ctx); err != nil {
				t.Fatalf("baseline verify: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tampered := strings.Replace(string(data), tc.from, tc.to, 1)
			if tampered == string(data) {
				t.Fatalf("tamper precondition failed: %s not found in log", tc.from)
			}
			if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
				t.Fatal(err)
			}
			reopened, err := NewFileSink(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := reopened.Verify(ctx); !errors.Is(err, ErrChainBroken) {
				t.Fatalf("expected ErrChainBroken after tampering %s, got %v", tc.name, err)
			}
		})
	}
}
