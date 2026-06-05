package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// decodeEnvelope reads the §6.10 structured error envelope a recorder
// captured so an assertion can check the code, status, and remediation
// fields a write-path helper emitted.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var env ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, rec.Body.String())
	}
	return env
}

// writeObjectStoreError maps an object-store read error to the §6.10 envelope.
// A missing key is a 404 registry.not_found; any other error is a 500
// registry.unavailable carrying the underlying message.
//
// Spec: §6.10
func TestWriteObjectStoreError_NotFoundVsUnavailable(t *testing.T) {
	t.Parallel()
	s := &Server{}

	t.Run("not_found", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.writeObjectStoreError(rec, fmt.Errorf("wrapped: %w", objectstore.ErrNotFound))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		env := decodeEnvelope(t, rec)
		if env.Code != "registry.not_found" {
			t.Errorf("code = %q, want registry.not_found", env.Code)
		}
		if env.Message != "object not found" {
			t.Errorf("message = %q, want %q", env.Message, "object not found")
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.writeObjectStoreError(rec, errors.New("backend down"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		env := decodeEnvelope(t, rec)
		if env.Code != "registry.unavailable" {
			t.Errorf("code = %q, want registry.unavailable", env.Code)
		}
		if env.Message != "backend down" {
			t.Errorf("message = %q, want the underlying error text", env.Message)
		}
		// registry.unavailable is a transient code: the §6.10 registry marks
		// it retryable with an operator hint.
		if !env.Retryable {
			t.Errorf("registry.unavailable should be retryable")
		}
		if env.SuggestedAction == "" {
			t.Errorf("registry.unavailable should carry a suggested_action")
		}
	})
}

// inlineBytes returns a small resource's bytes for inline delivery: the inline
// copy on the record when present, otherwise an object-store read by content
// hash. With no object store and no inline copy it returns an error naming the
// resource path.
//
// Spec: §7.2
func TestInlineBytes_InlineThenObjectStoreThenError(t *testing.T) {
	t.Parallel()

	t.Run("inline_copy_wins", func(t *testing.T) {
		// An inline copy is returned without consulting the object store, so a
		// nil store is irrelevant on this branch.
		s := &Server{}
		ref := store.ResourceRef{Path: "a.txt", Inline: []byte("hello")}
		got, err := s.inlineBytes(context.Background(), ref)
		if err != nil {
			t.Fatalf("inlineBytes: %v", err)
		}
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("object_store_read", func(t *testing.T) {
		mem := objectstore.NewMemory()
		const hex = "abc123"
		if err := mem.Put(context.Background(), hex, []byte("from-store"), "text/plain"); err != nil {
			t.Fatalf("Put: %v", err)
		}
		s := &Server{objectStore: mem}
		ref := store.ResourceRef{Path: "b.txt", ContentHash: "sha256:" + hex}
		got, err := s.inlineBytes(context.Background(), ref)
		if err != nil {
			t.Fatalf("inlineBytes: %v", err)
		}
		if string(got) != "from-store" {
			t.Errorf("got %q, want %q", got, "from-store")
		}
	})

	t.Run("missing_object_returns_error", func(t *testing.T) {
		s := &Server{objectStore: objectstore.NewMemory()}
		ref := store.ResourceRef{Path: "gone.txt", ContentHash: "sha256:deadbeef"}
		if _, err := s.inlineBytes(context.Background(), ref); err == nil {
			t.Fatalf("expected error reading a missing object")
		} else if !strings.Contains(err.Error(), "gone.txt") {
			t.Errorf("error %q should name the resource path", err)
		}
	})

	t.Run("no_object_store_no_inline", func(t *testing.T) {
		s := &Server{}
		ref := store.ResourceRef{Path: "c.txt", ContentHash: "sha256:x"}
		if _, err := s.inlineBytes(context.Background(), ref); err == nil {
			t.Fatalf("expected error when no object store is configured")
		} else if !strings.Contains(err.Error(), "no object store configured") {
			t.Errorf("error %q should report the missing store", err)
		}
	})
}

// contentHashETag formats a resolved content hash as a strong HTTP ETag (a
// quoted opaque string). An empty hash yields an empty ETag so the caller
// skips the header rather than emitting an empty quoted string.
//
// Spec: §12
func TestContentHashETag_EmptyVsQuoted(t *testing.T) {
	t.Parallel()
	if got := contentHashETag(""); got != "" {
		t.Errorf("empty hash: got %q, want empty string", got)
	}
	if got := contentHashETag("sha256:abc"); got != `"sha256:abc"` {
		t.Errorf("got %q, want %q", got, `"sha256:abc"`)
	}
}

// presignResource builds a §7.2 large-resource link. With no object store it
// returns an error naming the resource. With a store it presigns the bare
// content-hash key and copies the ref's hash, size, and content type onto the
// link.
//
// Spec: §7.2
func TestPresignResource_NoStoreErrorAndLinkFields(t *testing.T) {
	t.Parallel()

	t.Run("no_object_store", func(t *testing.T) {
		s := &Server{}
		ref := store.ResourceRef{Path: "big.bin", ContentHash: "sha256:zz"}
		if _, err := s.presignResource(context.Background(), ref); err == nil {
			t.Fatalf("expected error with no object store")
		} else if !strings.Contains(err.Error(), "big.bin") {
			t.Errorf("error %q should name the resource path", err)
		}
	})

	t.Run("link_fields", func(t *testing.T) {
		mem := objectstore.NewMemory()
		mem.BaseURL = "https://cdn.example"
		s := &Server{objectStore: mem}
		ref := store.ResourceRef{
			Path:        "data/big.bin",
			ContentHash: "sha256:feedface",
			Size:        4096,
			ContentType: "application/octet-stream",
		}
		link, err := s.presignResource(context.Background(), ref)
		if err != nil {
			t.Fatalf("presignResource: %v", err)
		}
		// The presigned key is the bare hash (the "sha256:" prefix stripped),
		// so the URL ends in the hex digest.
		if !strings.HasSuffix(link.URL, "/feedface") {
			t.Errorf("URL = %q, want a presign of the bare hash key", link.URL)
		}
		if link.ContentHash != ref.ContentHash {
			t.Errorf("ContentHash = %q, want %q", link.ContentHash, ref.ContentHash)
		}
		if link.Size != ref.Size {
			t.Errorf("Size = %d, want %d", link.Size, ref.Size)
		}
		if link.ContentType != ref.ContentType {
			t.Errorf("ContentType = %q, want %q", link.ContentType, ref.ContentType)
		}
	})
}

// WithNotifier installs the §9.1 operational-notification sink and returns the
// endpoint for chaining. notifyIngestFailure fires the installed sink with
// severity "error", a title naming the failing layer, the pipeline error as
// the body, and layer/tenant tags. A nil notifier makes notifyIngestFailure a
// no-op.
//
// Spec: §9.1
func TestNotifyIngestFailure_FiresInstalledNotifier(t *testing.T) {
	t.Parallel()

	t.Run("nil_notifier_is_noop", func(t *testing.T) {
		e := NewLayerEndpoint(store.NewMemory(), "t", NewModeTracker())
		// No WithNotifier: the call must not panic and must do nothing.
		e.notifyIngestFailure(context.Background(), "team-shared", errors.New("boom"))
	})

	t.Run("installed_notifier_receives_event", func(t *testing.T) {
		var (
			gotSeverity string
			gotTitle    string
			gotBody     string
			gotTags     map[string]string
			calls       int
		)
		e := NewLayerEndpoint(store.NewMemory(), "acme", NewModeTracker())
		ret := e.WithNotifier(func(_ context.Context, severity, title, body string, tags map[string]string) {
			calls++
			gotSeverity, gotTitle, gotBody, gotTags = severity, title, body, tags
		})
		if ret != e {
			t.Errorf("WithNotifier should return the endpoint for chaining")
		}

		e.notifyIngestFailure(context.Background(), "team-shared", errors.New("source unreachable"))
		if calls != 1 {
			t.Fatalf("notifier called %d times, want 1", calls)
		}
		if gotSeverity != "error" {
			t.Errorf("severity = %q, want error", gotSeverity)
		}
		if !strings.Contains(gotTitle, "team-shared") {
			t.Errorf("title = %q, should name the failing layer", gotTitle)
		}
		if gotBody != "source unreachable" {
			t.Errorf("body = %q, want the pipeline error text", gotBody)
		}
		if gotTags["layer"] != "team-shared" {
			t.Errorf("tags[layer] = %q, want team-shared", gotTags["layer"])
		}
		if gotTags["tenant"] != "acme" {
			t.Errorf("tags[tenant] = %q, want acme", gotTags["tenant"])
		}
	})
}

// writeReingestError maps each ingest-pipeline sentinel to its §6.10 code and
// HTTP status, and falls back to 500 registry.unavailable for an unrecognized
// error.
//
// Spec: §6.10
func TestWriteReingestError_SentinelMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"frozen", ingest.ErrFrozen, http.StatusConflict, "ingest.frozen"},
		{"history_rewritten", ingest.ErrHistoryRewritten, http.StatusConflict, "ingest.history_rewritten"},
		{"lint_failed", ingest.ErrLintFailed, http.StatusUnprocessableEntity, "ingest.lint_failed"},
		{"invalid_artifact", ingest.ErrInvalidArtifact, http.StatusUnprocessableEntity, "ingest.lint_failed"},
		{"quota_exceeded", ingest.ErrQuotaExceeded, http.StatusTooManyRequests, "quota.storage_exceeded"},
		{"audit_volume", ingest.ErrAuditVolumeExceeded, http.StatusTooManyRequests, "quota.audit_volume_exceeded"},
		{"public_mode_sensitive", ingest.ErrPublicModeSensitive, http.StatusUnprocessableEntity, "ingest.public_mode_rejects_sensitive"},
		{"source_unreachable", source.ErrSourceUnreachable, http.StatusBadGateway, "ingest.source_unreachable"},
		{"invalid_config", source.ErrInvalidConfig, http.StatusBadRequest, "registry.invalid_argument"},
		{"unknown_default", errors.New("some other failure"), http.StatusInternalServerError, "registry.unavailable"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			writeReingestError(rec, fmt.Errorf("ctx: %w", tc.err))
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			env := decodeEnvelope(t, rec)
			if env.Code != tc.code {
				t.Errorf("code = %q, want %q", env.Code, tc.code)
			}
		})
	}
}

// webhookSignatureHeader selects the signature credential header for the named
// GitProvider. GitLab reads X-Gitlab-Token; Bitbucket prefers X-Hub-Signature
// and falls back to X-Hub-Signature-256; GitHub and custom providers prefer
// X-Hub-Signature-256 and fall back to X-Hub-Signature.
//
// Spec: §9.1
func TestWebhookSignatureHeader_ProviderSelection(t *testing.T) {
	t.Parallel()

	newReq := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/ingest/webhook/x", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	t.Run("gitlab_token", func(t *testing.T) {
		r := newReq(map[string]string{"X-Gitlab-Token": "tok", "X-Hub-Signature-256": "ignored"})
		if got := webhookSignatureHeader(r, "gitlab"); got != "tok" {
			t.Errorf("gitlab: got %q, want tok", got)
		}
	})

	t.Run("bitbucket_prefers_v1", func(t *testing.T) {
		r := newReq(map[string]string{"X-Hub-Signature": "v1", "X-Hub-Signature-256": "v256"})
		if got := webhookSignatureHeader(r, "bitbucket"); got != "v1" {
			t.Errorf("bitbucket: got %q, want v1", got)
		}
	})

	t.Run("bitbucket_falls_back_to_256", func(t *testing.T) {
		r := newReq(map[string]string{"X-Hub-Signature-256": "v256"})
		if got := webhookSignatureHeader(r, "bitbucket"); got != "v256" {
			t.Errorf("bitbucket fallback: got %q, want v256", got)
		}
	})

	t.Run("github_prefers_256", func(t *testing.T) {
		r := newReq(map[string]string{"X-Hub-Signature": "v1", "X-Hub-Signature-256": "v256"})
		if got := webhookSignatureHeader(r, "github"); got != "v256" {
			t.Errorf("github: got %q, want v256", got)
		}
	})

	t.Run("github_falls_back_to_v1", func(t *testing.T) {
		r := newReq(map[string]string{"X-Hub-Signature": "v1"})
		if got := webhookSignatureHeader(r, "github"); got != "v1" {
			t.Errorf("github fallback: got %q, want v1", got)
		}
	})

	t.Run("custom_provider_uses_github_convention", func(t *testing.T) {
		r := newReq(map[string]string{"X-Hub-Signature-256": "v256"})
		if got := webhookSignatureHeader(r, "acme-forge"); got != "v256" {
			t.Errorf("custom provider: got %q, want v256", got)
		}
	})

	t.Run("absent_header_yields_empty", func(t *testing.T) {
		r := newReq(nil)
		if got := webhookSignatureHeader(r, "github"); got != "" {
			t.Errorf("absent header: got %q, want empty", got)
		}
	})
}

// newBucket returns nil for a non-positive rate (the disabled case allow()
// treats as always-allow) and otherwise a bucket whose capacity equals the
// rate and that starts full.
//
// Spec: §4.7.8
func TestNewBucket_DisabledAndEnabled(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{0, -1} {
		if b := newBucket(rate); b != nil {
			t.Errorf("newBucket(%d) = %p, want nil", rate, b)
		}
	}

	b := newBucket(3)
	if b == nil {
		t.Fatalf("newBucket(3) = nil, want a bucket")
	}
	if b.capacity != 3 {
		t.Errorf("capacity = %d, want 3", b.capacity)
	}
	if b.rate != 3 {
		t.Errorf("rate = %v, want 3", b.rate)
	}
	// A fresh bucket starts full, so it allows exactly capacity calls before
	// it drains.
	for i := 0; i < 3; i++ {
		if !b.allow() {
			t.Errorf("call %d denied; a fresh bucket should start full", i)
		}
	}
	if b.allow() {
		t.Errorf("call past capacity allowed; bucket should be drained")
	}

	// A nil bucket (the disabled case) always allows.
	var nilBucket *rateBucket
	if !nilBucket.allow() {
		t.Errorf("nil bucket should always allow")
	}
}

// sourceIP returns the client IP from RemoteAddr with the port stripped, the
// bare RemoteAddr when it carries no port, and an empty string when RemoteAddr
// is unset.
//
// Spec: §8.1
func TestSourceIP_PortStrippingAndEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"host_port", "203.0.113.7:54321", "203.0.113.7"},
		{"ipv6_host_port", "[2001:db8::1]:443", "2001:db8::1"},
		{"bare_host_no_port", "203.0.113.9", "203.0.113.9"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/v1/load_domain", nil)
			r.RemoteAddr = tc.remoteAddr
			if got := sourceIP(r); got != tc.want {
				t.Errorf("sourceIP(%q) = %q, want %q", tc.remoteAddr, got, tc.want)
			}
		})
	}
}

// restore rejects a non-POST method, a write while in read-only mode, and a
// missing id query parameter before it touches the store. An admin-defined
// soft-deleted layer requires admin authorization.
//
// Spec: §8.4
func TestRestore_GuardBranches(t *testing.T) {
	t.Parallel()

	t.Run("method_not_allowed", func(t *testing.T) {
		t.Parallel()
		e := NewLayerEndpoint(store.NewMemory(), "t", NewModeTracker())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/layers/restore?id=x", nil)
		e.restore(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if env := decodeEnvelope(t, rec); env.Code != "registry.invalid_argument" {
			t.Errorf("code = %q, want registry.invalid_argument", env.Code)
		}
	})

	t.Run("read_only_rejected", func(t *testing.T) {
		t.Parallel()
		mode := NewModeTracker()
		mode.Set(ModeReadOnly)
		e := NewLayerEndpoint(store.NewMemory(), "t", mode)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/layers/restore?id=x", nil)
		e.restore(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if env := decodeEnvelope(t, rec); env.Code != "registry.read_only" {
			t.Errorf("code = %q, want registry.read_only", env.Code)
		}
	})

	t.Run("missing_id", func(t *testing.T) {
		t.Parallel()
		e := NewLayerEndpoint(store.NewMemory(), "t", NewModeTracker())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/layers/restore", nil)
		e.restore(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if env := decodeEnvelope(t, rec); env.Code != "registry.invalid_argument" {
			t.Errorf("code = %q, want registry.invalid_argument", env.Code)
		}
	})

	t.Run("admin_layer_requires_admin", func(t *testing.T) {
		t.Parallel()
		const tenantID = "t"
		st := store.NewMemory()
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenantID}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		// Seed and soft-delete an admin-defined (non-user-defined) layer so
		// the deleted-list lookup finds a recoverable layer the caller is not
		// authorized to restore.
		if err := st.PutLayerConfig(context.Background(), store.LayerConfig{
			TenantID: tenantID, ID: "org-shared", SourceType: "local", LocalPath: "/tmp/x",
		}); err != nil {
			t.Fatalf("PutLayerConfig: %v", err)
		}
		if err := st.DeleteLayerConfig(context.Background(), tenantID, "org-shared"); err != nil {
			t.Fatalf("DeleteLayerConfig: %v", err)
		}
		e := NewLayerEndpoint(st, tenantID, NewModeTracker()).
			WithAdminAuth(func(*http.Request) error { return ErrAdminRequired })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/layers/restore?id=org-shared", nil)
		e.restore(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if env := decodeEnvelope(t, rec); env.Code != "auth.forbidden" {
			t.Errorf("code = %q, want auth.forbidden", env.Code)
		}
	})

	t.Run("no_recoverable_layer", func(t *testing.T) {
		t.Parallel()
		st := store.NewMemory()
		if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		e := NewLayerEndpoint(st, "t", NewModeTracker())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/layers/restore?id=ghost", nil)
		e.restore(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if env := decodeEnvelope(t, rec); env.Code != "registry.not_found" {
			t.Errorf("code = %q, want registry.not_found", env.Code)
		}
	})
}
