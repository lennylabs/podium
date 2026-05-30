package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
)

// auditMetaKey is the context key under which per-request audit metadata
// (§8.1) is carried from the HTTP boundary to the audit emit path.
type auditMetaKey struct{}

// AuditMeta carries the per-request audit attributes the §8.1 caller-
// identity contract requires but that only exist at the HTTP boundary:
// the W3C trace id, the structured caller identity (email and groups for
// authenticated callers), and the public-mode network attributes (source
// IP and any upstream X-Forwarded-User). The serverboot audit adapter and
// the server's own write handlers read it from the request context to
// populate the persisted audit event.
//
// spec: §8.1 ("Caller identity in audit events"), §8.1 trace id.
type AuditMeta struct {
	TraceID       string
	Email         string
	Groups        []string
	PublicMode    bool
	SourceIP      string
	ForwardedUser string
}

// withAuditMeta returns ctx carrying m so downstream emit paths can
// recover the request's trace id and caller attributes.
func withAuditMeta(ctx context.Context, m AuditMeta) context.Context {
	return context.WithValue(ctx, auditMetaKey{}, m)
}

// AuditMetaFromContext recovers the per-request audit metadata stored by
// the identity middleware. The boolean is false when no metadata was
// attached (for example a non-HTTP caller), in which case the audit event
// carries no trace id or structured caller attributes.
func AuditMetaFromContext(ctx context.Context) (AuditMeta, bool) {
	m, ok := ctx.Value(auditMetaKey{}).(AuditMeta)
	return m, ok
}

// auditMetaFrom builds the per-request audit metadata from the HTTP
// request and the resolved identity. The trace id is taken from a valid
// W3C `traceparent` header when present (§8.1 "W3C Trace Context") and
// otherwise generated so every request-scoped event carries one and both
// audit streams can share it. Email and groups come from the OAuth-token
// identity for authenticated callers; the source IP and X-Forwarded-User
// are captured for public-mode callers per §8.1.
func auditMetaFrom(r *http.Request, id layer.Identity) AuditMeta {
	m := AuditMeta{
		TraceID:    traceIDFromRequest(r),
		PublicMode: id.IsPublic || !id.IsAuthenticated,
	}
	if !m.PublicMode {
		m.Email = id.Email
		m.Groups = id.Groups
	} else {
		m.SourceIP = sourceIP(r)
		m.ForwardedUser = r.Header.Get("X-Forwarded-User")
	}
	return m
}

// traceIDFromRequest extracts the 16-byte trace-id from a W3C
// `traceparent` header (`version-traceid-spanid-flags`) when it is
// well-formed, and otherwise mints a fresh random trace id. A request
// without a usable header still yields a stable id shared by every event
// emitted while serving it.
func traceIDFromRequest(r *http.Request) string {
	if tp := r.Header.Get("traceparent"); tp != "" {
		if id := parseTraceparent(tp); id != "" {
			return id
		}
	}
	return newTraceID()
}

// parseTraceparent returns the trace-id field of a W3C traceparent header,
// or "" when the header is malformed or carries the all-zero invalid
// trace-id. Only the trace-id (the 32-hex second field) is used; spans are
// not modeled by the registry.
func parseTraceparent(tp string) string {
	parts := strings.Split(tp, "-")
	if len(parts) < 4 {
		return ""
	}
	traceID := strings.ToLower(parts[1])
	if len(traceID) != 32 || !isHex(traceID) || traceID == "00000000000000000000000000000000" {
		return ""
	}
	return traceID
}

// newTraceID mints a random 16-byte (32-hex) trace id.
func newTraceID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(buf[:])
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sourceIP returns the client IP from RemoteAddr, stripping the port.
func sourceIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// callerIdentityString renders the §8.1 caller identity string: the OAuth
// sub-claim for authenticated callers, or "system:public" for public-mode
// and anonymous callers (mirrors core.callerOf so the registry and server
// emit paths agree).
func callerIdentityString(id layer.Identity) string {
	if id.IsPublic || !id.IsAuthenticated || id.Sub == "" {
		return "system:public"
	}
	return id.Sub
}

// withAuditMetaMiddleware wraps next so every request carries the §8.1
// per-request audit metadata (the W3C trace id and the structured caller
// identity) in its context. The core read-event emitter and the HTTP
// write handlers recover it to populate the persisted audit event.
func (s *Server) withAuditMetaMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withAuditMeta(r.Context(), auditMetaFrom(r, s.identity(r)))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// emitAuditEvent writes one registry audit event to the §8.3 sink with the
// §8.1 caller-identity attributes attached. It reads the per-request audit
// metadata from the context when the identity middleware attached it and
// otherwise derives it from the request directly, so write handlers that
// are not behind the middleware still record a trace id and caller
// network. A nil sink is a no-op.
func emitAuditEvent(sink *audit.FileSink, r *http.Request, id layer.Identity, typ audit.EventType, target string, fields map[string]string) {
	if sink == nil {
		return
	}
	m, ok := AuditMetaFromContext(r.Context())
	if !ok {
		m = auditMetaFrom(r, id)
	}
	ev := audit.Event{
		Type:       typ,
		TraceID:    m.TraceID,
		Caller:     callerIdentityString(id),
		Target:     target,
		Context:    fields,
		PublicMode: m.PublicMode,
	}
	if m.PublicMode {
		ev.CallerNetwork = &audit.CallerNetwork{SourceIP: m.SourceIP, ForwardedUser: m.ForwardedUser}
	} else {
		ev.CallerEmail = m.Email
		ev.CallerGroups = m.Groups
	}
	_ = sink.Append(r.Context(), ev)
}
