package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §7.2.2 — Header encodes every non-empty member and ParseClientHeader
// reads it back unchanged.
func TestCallerClient_HeaderRoundTrip(t *testing.T) {
	t.Parallel()
	want := CallerClient{
		Kind: "mcp", Name: "podium-mcp", Version: "0.6.0",
		Host: "Claude Code", HostVersion: "2.1.0", Harness: "claude-code",
	}
	got := ParseClientHeader(want.Header())
	if got == nil || *got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if h := (CallerClient{Kind: "cli"}).Header(); h != "kind=cli" {
		t.Errorf("Header() = %q, want empty members omitted", h)
	}
}

// Spec: §7.2.2 — the registry ignores an empty, oversized, or unparsable
// value, keeps the first occurrence of a repeated member, ignores unknown
// members, sanitizes and truncates each member, and maps an absent or
// unknown kind to other only when a member is left non-empty.
func TestParseClientHeader(t *testing.T) {
	t.Parallel()
	atLimit := "kind=cli&name=" + strings.Repeat("a", maxClientHeaderBytes-len("kind=cli&name="))
	cases := []struct {
		name string
		in   string
		want *CallerClient
	}{
		{"empty", "", nil},
		{"at the 1024-byte limit", atLimit, &CallerClient{Kind: "cli", Name: strings.Repeat("a", maxClientMemberBytes)}},
		{"over the limit", atLimit + "a", nil},
		{"unparsable", "%zz", nil},
		{"first occurrence wins", "kind=sync&kind=cli&name=podium", &CallerClient{Kind: "sync", Name: "podium"}},
		{"unknown member ignored", "kind=sdk&color=blue", &CallerClient{Kind: "sdk"}},
		{"control bytes sanitized", "kind=mcp&host=%00%1b[31m%0a@x", &CallerClient{Kind: "mcp", Host: "___31m__x"}},
		{"uppercase kind", "kind=MCP", &CallerClient{Kind: "other"}},
		{"unknown kind", "kind=agent", &CallerClient{Kind: "other"}},
		{"absent kind", "host=cursor", &CallerClient{Kind: "other", Host: "cursor"}},
		{"only empty members", "kind=&name=", nil},
		{"only unknown members", "color=blue", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseClientHeader(tc.in)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("ParseClientHeader(%q) = %+v, want nil", tc.in, got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Fatalf("ParseClientHeader(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// Spec: §7.2.2 — NormalizeClient returns nil for the zero value, truncates a
// long member to 64 bytes, and is idempotent.
func TestNormalizeClient(t *testing.T) {
	t.Parallel()
	if got := NormalizeClient(CallerClient{}); got != nil {
		t.Errorf("NormalizeClient(zero) = %+v, want nil", got)
	}
	got := NormalizeClient(CallerClient{Kind: "sdk", Name: strings.Repeat("n", 200), Harness: "a=b"})
	if got == nil || len(got.Name) != maxClientMemberBytes || got.Harness != "a_b" {
		t.Fatalf("NormalizeClient = %+v, want a 64-byte name and a sanitized harness", got)
	}
	if again := NormalizeClient(*got); again == nil || *again != *got {
		t.Errorf("NormalizeClient is not idempotent: %+v then %+v", got, again)
	}
}

// Spec: §8.1, §8.6 — each client member enters the hashed body, and a nil
// client adds nothing, so a record without the attribute keeps its hash.
func TestCanonicalBody_CallerClient(t *testing.T) {
	t.Parallel()
	base := Event{Type: EventArtifactLoaded, Caller: "alice", Tenant: "acme"}
	want := string(base.canonicalBody())
	withEmpty := base
	withEmpty.CallerClient = &CallerClient{}
	if string(withEmpty.canonicalBody()) != want {
		t.Errorf("a client with no members must not change the canonical body")
	}
	var full CallerClient
	for _, m := range full.members() {
		got := base
		c := CallerClient{}
		for _, cm := range c.members() {
			if cm.key == m.key {
				*cm.val = "v"
			}
		}
		got.CallerClient = &c
		body := string(got.canonicalBody())
		if body == want || !strings.Contains(body, "caller_client_"+m.key+"=v") {
			t.Errorf("member %s must add caller_client_%s=v to the canonical body", m.key, m.key)
		}
	}
}

// Spec: §8.1, §8.6 — an event whose only caller attribute is the client
// serializes caller.client, and every sink normalizes the client before it
// hashes, so the stored value is sanitized and the chain verifies.
func TestSinks_NormalizeCallerClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw := Event{Type: EventArtifactLoaded, CallerClient: &CallerClient{Kind: "MCP", Host: "evil\x00host"}}
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
		t.Fatalf("file record client = %+v, want %+v", events[0].CallerClient, want)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"caller":{"client":{"kind":"other","host":"evil_host"}}`) {
		t.Errorf("file line lacks caller.client: %s", data)
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

// Spec: §8.1, §8.6 — the endpoint sink POSTs the normalized caller.client and
// hashes the normalized event.
func TestEndpointSink_NormalizesCallerClient(t *testing.T) {
	t.Parallel()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	sink, err := NewEndpointSink(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{Type: EventArtifactLoaded, CallerClient: &CallerClient{Kind: "sync", Host: "a\x1bb"}}
	if err := sink.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	var je jsonEvent
	if err := json.Unmarshal(body, &je); err != nil {
		t.Fatal(err)
	}
	got := eventFromJSON(je)
	if got.CallerClient == nil || *got.CallerClient != (CallerClient{Kind: "sync", Host: "a_b"}) {
		t.Fatalf("posted client = %+v", got.CallerClient)
	}
	if err := verifyOne(got); err != nil {
		t.Errorf("posted hash does not match the sanitized event: %v", err)
	}
}

func verifyOne(e Event) error {
	m := &Memory{events: []Event{e}}
	return m.Verify(context.Background())
}

// Spec: §8.1, §8.5 — EraseUser normalizes the client onto user.erased, the
// rewritten log verifies, and a non-nil client cannot widen the zero-scope
// guard.
func TestEraseUser_CallerClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.log")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Append(ctx, Event{Type: EventArtifactLoaded, Caller: "alice@acme.com"}); err != nil {
		t.Fatal(err)
	}
	client := &CallerClient{Kind: "cli", Host: "x\x00y"}
	if _, err := EraseUser(ctx, sink, "alice@acme.com", "salt", "carol@acme.com", client, EraseScope{}); err == nil {
		t.Fatalf("EraseUser with a zero scope and a client succeeded")
	}
	if _, err := EraseUser(ctx, sink, "alice@acme.com", "salt", "carol@acme.com", client, EraseScope{Unlabeled: true}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("Verify after erase: %v", err)
	}
	events, err := readAllEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != EventUserErased || last.CallerClient == nil || *last.CallerClient != (CallerClient{Kind: "cli", Host: "x_y"}) {
		t.Fatalf("user.erased = %+v, client %+v", last, last.CallerClient)
	}
}
