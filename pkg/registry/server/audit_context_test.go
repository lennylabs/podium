package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
)

// spec §8.1: the trace id is taken from a well-formed W3C traceparent and
// rejected when malformed or all-zero.
func TestParseTraceparent(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := parseTraceparent(valid); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("valid traceparent: got %q", got)
	}
	for _, bad := range []string{
		"",
		"garbage",
		"00-tooshort-00f067aa0ba902b7-01",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // invalid all-zero trace id
		"00-4bf92f3577b34da6a3ce929d0e0e4736",                     // missing fields
	} {
		if got := parseTraceparent(bad); got != "" {
			t.Errorf("malformed traceparent %q: expected empty, got %q", bad, got)
		}
	}
}

// spec §8.1: authenticated callers carry email and groups (no network);
// public-mode callers carry source IP, forwarded user, and the public flag
// (no email or groups). The trace id is generated when no header is present.
func TestAuditMetaFrom_AuthenticatedVsPublic(t *testing.T) {
	authedReq := httptest.NewRequest("GET", "/v1/load_domain", nil)
	authedReq.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	authed := auditMetaFrom(authedReq, layer.Identity{
		Sub: "alice", Email: "alice@acme.com", Groups: []string{"eng"}, IsAuthenticated: true,
	})
	if authed.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id from header: got %q", authed.TraceID)
	}
	if authed.PublicMode {
		t.Error("authenticated caller marked public")
	}
	if authed.Email != "alice@acme.com" || len(authed.Groups) != 1 {
		t.Errorf("authenticated caller missing email/groups: %+v", authed)
	}
	if authed.SourceIP != "" || authed.ForwardedUser != "" {
		t.Errorf("authenticated caller should not capture network: %+v", authed)
	}

	pubReq := httptest.NewRequest("GET", "/v1/load_domain", nil)
	pubReq.RemoteAddr = "203.0.113.7:54321"
	pubReq.Header.Set("X-Forwarded-User", "bob")
	pub := auditMetaFrom(pubReq, layer.Identity{IsPublic: true})
	if !pub.PublicMode {
		t.Error("public caller not marked public")
	}
	if pub.SourceIP != "203.0.113.7" || pub.ForwardedUser != "bob" {
		t.Errorf("public caller missing network: %+v", pub)
	}
	if pub.Email != "" || len(pub.Groups) != 0 {
		t.Errorf("public caller should not carry email/groups: %+v", pub)
	}
	if pub.TraceID == "" {
		t.Error("trace id should be generated when no traceparent header is present")
	}
}

// spec §8.1: emitAuditEvent records the structured caller identity, and a nil
// sink is a no-op.
func TestEmitAuditEvent_PublicCallerNetwork(t *testing.T) {
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/admin/grants", nil)
	req.RemoteAddr = "203.0.113.7:9999"
	// nil sink must not panic.
	emitAuditEvent(nil, req, layer.Identity{IsPublic: true}, audit.EventAdminGranted, "carol", nil)

	emitAuditEvent(sink, req, layer.Identity{IsPublic: true}, audit.EventAdminGranted, "carol",
		map[string]string{"action": "grant"})

	data, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"type":"admin.granted"`,
		`"caller":{"identity":"system:public"`,
		`"target":"carol"`,
		`"public_mode":true`,
		`"source_ip":"203.0.113.7"`,
		`"action":"grant"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted event missing %s\nlog:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `"trace_id"`) {
		t.Errorf("emitted event missing generated trace_id\nlog:\n%s", got)
	}
}

// spec §8.1: an authenticated caller's event records email and groups and no
// public-mode network block.
func TestEmitAuditEvent_AuthenticatedCaller(t *testing.T) {
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("DELETE", "/v1/admin/grants?user_id=carol", nil)
	id := layer.Identity{Sub: "alice", Email: "alice@acme.com", Groups: []string{"admins"}, IsAuthenticated: true}
	emitAuditEvent(sink, req, id, audit.EventAdminGranted, "carol", map[string]string{"action": "revoke"})

	got, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{`"caller":{"identity":"alice"`, `"email":"alice@acme.com"`, `"groups":["admins"]`, `"action":"revoke"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s\nlog:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"network"`) || strings.Contains(s, "public_mode") {
		t.Errorf("authenticated caller leaked public-mode fields:\n%s", s)
	}
}
