package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// adminFaultStore fails every admin-grant lookup with an error whose text
// stands in for internal store detail.
type adminFaultStore struct{ *store.Memory }

const adminFaultDetail = "store: invalid org id \"podium:unrouted\" for schema isolation"

func (adminFaultStore) IsAdmin(context.Context, string, string) (bool, error) {
	return false, errors.New(adminFaultDetail)
}

// Spec: §4.7.2 — an admin check that cannot be evaluated denies the request
// with auth.forbidden, and the client-facing message carries none of the store
// error's text, which is logged instead. The test swaps the global log
// output, so it does not run in parallel.
func TestRequireAdmin_UnevaluableCheckHidesStoreDetail(t *testing.T) {
	st := adminFaultStore{store.NewMemory()}
	srv := server.New(core.New(st, "t", nil),
		server.WithIdentityResolver(func(*http.Request) layer.Identity {
			return layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}
		}))
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/show-effective?user_id=bob@acme.com", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var env struct{ Code, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
	}
	if env.Code != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", env.Code)
	}
	if strings.Contains(rec.Body.String(), "schema isolation") || strings.Contains(rec.Body.String(), "podium:unrouted") {
		t.Errorf("response leaks the store error: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), adminFaultDetail) || !strings.Contains(logs.String(), "alice@acme.com") {
		t.Errorf("log = %q; want the store error and the caller", logs.String())
	}
}
