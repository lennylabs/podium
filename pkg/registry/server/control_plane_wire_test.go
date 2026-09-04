package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// memberNames returns the sorted member names of a JSON object without
// interpreting any of them, so an assertion reads the wire names rather
// than a Go struct's fields. Sharing an encoder and a decoder is what made
// the existing control-plane tests blind to the field names.
func memberNames(t *testing.T, raw []byte) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode object: %v\nobject: %s", err, raw)
	}
	names := make([]string, 0, len(obj))
	for k := range obj {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// memberOf returns one member of a JSON object, failing when it is absent.
func memberOf(t *testing.T, raw []byte, name string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode object: %v\nobject: %s", err, raw)
	}
	got, ok := obj[name]
	if !ok {
		t.Fatalf("object carries no %q member\nobject: %s", name, raw)
	}
	return got
}

// Spec: §7.2.1, §4.7.8, §7.3.3 — GET /v1/quota and GET /v1/admin/tenants
// report the same five limits, so they name them identically. The two
// endpoints disagreed before store.Quota took its tags: the quota read
// emitted the Go field names while the tenant object emitted the
// snake_case ones. Each endpoint keeps the tenant identifier §7.2.1
// permits, because the tenant is the subject of the record that carries
// it.
func TestQuota_LimitNamesMatchTheTenantObject(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, true)
	if err := st.GrantOperator(context.Background(), operatorCaller.Sub); err != nil {
		t.Fatalf("GrantOperator: %v", err)
	}
	if err := st.UpdateTenant(context.Background(), store.Tenant{
		ID: "default", Name: "default", Active: true,
		Quota: store.Quota{
			StorageBytes: 1 << 20, SearchQPS: 10, MaterializeRate: 5,
			AuditVolumePerDay: 1000, MaxUserLayers: 7,
		},
	}); err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}

	code, quotaBody := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/quota", "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/quota = %d, body=%s", code, quotaBody)
	}
	code, tenantsBody := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/admin/tenants", "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/admin/tenants = %d, body=%s", code, tenantsBody)
	}

	quotaLimits := memberOf(t, quotaBody, "limits")
	var tenants struct {
		Tenants []json.RawMessage `json:"tenants"`
	}
	if err := json.Unmarshal(tenantsBody, &tenants); err != nil {
		t.Fatalf("decode tenant list: %v\nbody: %s", err, tenantsBody)
	}
	if len(tenants.Tenants) == 0 {
		t.Fatalf("tenant list is empty\nbody: %s", tenantsBody)
	}
	want := []string{
		"audit_volume_per_day", "materialize_rate", "max_user_layers",
		"search_qps", "storage_bytes",
	}
	if got := memberNames(t, quotaLimits); !reflect.DeepEqual(got, want) {
		t.Errorf("GET /v1/quota limits = %v, want %v", got, want)
	}
	for _, raw := range tenants.Tenants {
		if got := memberNames(t, memberOf(t, raw, "quota")); !reflect.DeepEqual(got, want) {
			t.Errorf("tenant object quota = %v, want %v", got, want)
		}
		// §7.2.1 permits a tenant identifier on a record whose subject is
		// the tenant itself, which is what each element of the §7.3.3
		// tenant list is.
		if got := string(memberOf(t, raw, "id")); got != `"default"` {
			t.Errorf("tenant object id = %s, want \"default\"", got)
		}
	}
	if got := string(memberOf(t, quotaBody, "tenant_id")); got != `"default"` {
		t.Errorf("GET /v1/quota tenant_id = %s, want \"default\"", got)
	}

	// The values under the shared names are the same five numbers, so the
	// key-set equality is an agreement about the same record rather than a
	// coincidence of two unrelated shapes.
	var limits map[string]int64
	if err := json.Unmarshal(quotaLimits, &limits); err != nil {
		t.Fatalf("decode limits: %v", err)
	}
	var tenantQuota map[string]int64
	if err := json.Unmarshal(memberOf(t, tenants.Tenants[0], "quota"), &tenantQuota); err != nil {
		t.Fatalf("decode tenant quota: %v", err)
	}
	if !reflect.DeepEqual(limits, tenantQuota) {
		t.Errorf("limits = %v, tenant quota = %v, want the same numbers", limits, tenantQuota)
	}
	if limits["max_user_layers"] != 7 {
		t.Errorf("max_user_layers = %d, want 7", limits["max_user_layers"])
	}
}

// Spec: §7.2.1, §4.7 — GET /v1/admin/show-effective returns one object
// per layer under the §7.2.1 names. core.EffectiveLayer reached the wire
// untagged before, so every row carried the Go field names.
func TestShowEffective_RowMemberNames(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}},
		{ID: "alice-only", Visibility: layer.Visibility{Users: []string{"alice"}}},
	})
	code, body := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/admin/show-effective?user_id=bob", "")
	if code != http.StatusOK {
		t.Fatalf("show-effective = %d, body=%s", code, body)
	}
	var env struct {
		Layers []json.RawMessage `json:"layers"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	if len(env.Layers) != 2 {
		t.Fatalf("layers = %d, want 2\nbody: %s", len(env.Layers), body)
	}
	want := []string{"layer_id", "reason", "visible"}
	for _, raw := range env.Layers {
		if got := memberNames(t, raw); !reflect.DeepEqual(got, want) {
			t.Errorf("show-effective row = %v, want %v", got, want)
		}
	}
}

// reembedEmbedder embeds every text with a fixed vector, and returns an
// error for any batch whose text carries failMarker, so a bulk pass reports
// both a success and a failure in one call.
type reembedEmbedder struct {
	failMarker string
}

func (*reembedEmbedder) ID() string      { return "fake" }
func (*reembedEmbedder) Model() string   { return "fake-model" }
func (*reembedEmbedder) Dimensions() int { return 8 }
func (e *reembedEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	for _, txt := range texts {
		if e.failMarker != "" && strings.Contains(txt, e.failMarker) {
			return nil, errEmbedRefused
		}
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, 8)
		out[i][0] = 1
	}
	return out, nil
}

// errEmbedRefused is the failure the fake embedding provider returns, so a
// ReembedFailure's reason has a stable value to carry.
var errEmbedRefused = errors.New("provider refused the batch")

// bootReembedServer boots a registry over two manifests with a vector
// backend wired, so both arms of POST /v1/admin/reembed run to completion.
// The registry authenticates no caller, which is the deployment
// WithUnauthenticatedReembed exists for.
func bootReembedServer(t *testing.T, failMarker string) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "default", ArtifactID: "alpha", Version: "1.0.0",
			Description: "alpha desc", Body: []byte("body alpha"), Layer: "L"},
		{TenantID: "default", ArtifactID: "beta", Version: "1.0.0",
			Description: "beta desc", Body: []byte("body beta"), Layer: "L"},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	reg := core.New(st, "default", nil).
		WithVectorSearch(vector.NewMemory(8), &reembedEmbedder{failMarker: failMarker})
	srv := server.New(reg, server.WithUnauthenticatedReembed())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §7.2.1, §4.7 — both arms of POST /v1/admin/reembed answer under
// the §7.2.1 names. The single-artifact arm hand-builds its body and was
// already lowercase; the bulk arm marshals core.ReembedResult, which
// reached the wire under the Go field names before it took its tags.
func TestAdminReembed_BothArmsAnswerInSnakeCase(t *testing.T) {
	t.Parallel()
	ts := bootReembedServer(t, "")

	code, body := tenantHTTP(t, http.MethodPost,
		ts.URL+"/v1/admin/reembed?artifact=alpha&version=1.0.0", "")
	if code != http.StatusOK {
		t.Fatalf("single-artifact reembed = %d, body=%s", code, body)
	}
	if got := memberNames(t, body); !reflect.DeepEqual(got, []string{"reembedded"}) {
		t.Errorf("single-artifact envelope = %v, want [reembedded]", got)
	}
	if got := memberNames(t, memberOf(t, body, "reembedded")); !reflect.DeepEqual(got, []string{"id", "version"}) {
		t.Errorf("reembedded = %v, want [id version]", got)
	}

	code, body = tenantHTTP(t, http.MethodPost, ts.URL+"/v1/admin/reembed", "")
	if code != http.StatusOK {
		t.Fatalf("bulk reembed = %d, body=%s", code, body)
	}
	want := []string{"failed", "succeeded", "total"}
	if got := memberNames(t, body); !reflect.DeepEqual(got, want) {
		t.Errorf("bulk envelope = %v, want %v", got, want)
	}
	if got := string(memberOf(t, body, "failed")); got != "null" {
		t.Errorf("failed on a pass with no failure = %s, want null", got)
	}
}

// Spec: §7.2.1, §4.7 — a bulk pass that records a failure emits each
// ReembedFailure under the §7.2.1 names. The failure list is appended to
// only when an embedding call fails, so a pass against a provider that
// answers every batch leaves the three member names unasserted.
func TestAdminReembed_FailureEntryMemberNames(t *testing.T) {
	t.Parallel()
	ts := bootReembedServer(t, "beta")
	code, body := tenantHTTP(t, http.MethodPost, ts.URL+"/v1/admin/reembed", "")
	if code != http.StatusOK {
		t.Fatalf("bulk reembed = %d, body=%s", code, body)
	}
	want := []string{"failed", "succeeded", "total"}
	if got := memberNames(t, body); !reflect.DeepEqual(got, want) {
		t.Errorf("bulk envelope = %v, want %v", got, want)
	}
	var failed []json.RawMessage
	if err := json.Unmarshal(memberOf(t, body, "failed"), &failed); err != nil {
		t.Fatalf("decode failed: %v\nbody: %s", err, body)
	}
	if len(failed) == 0 {
		t.Fatalf("failed is empty, want the refused manifest\nbody: %s", body)
	}
	wantEntry := []string{"artifact_id", "reason", "version"}
	for _, raw := range failed {
		if got := memberNames(t, raw); !reflect.DeepEqual(got, wantEntry) {
			t.Errorf("failed entry = %v, want %v", got, wantEntry)
		}
		if got := string(memberOf(t, raw, "artifact_id")); got != `"beta"` {
			t.Errorf("artifact_id = %s, want \"beta\"", got)
		}
	}
}
