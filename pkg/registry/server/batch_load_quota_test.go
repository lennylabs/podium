package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

const batchLoadPath = "/v1/artifacts:batchLoad"

// newRatedBatchFixture boots the batch fixture with a limiter whose deployment
// materialization rate is rate. The fixture's single-tenant registry stores no
// rate, so rate is the enforced burst.
func newRatedBatchFixture(t *testing.T, rate int) *httptest.Server {
	t.Helper()
	ts, _ := newBatchFixture(t, server.WithQuotaLimiter(
		server.NewQuotaLimiter(server.QuotaLimits{MaterializeRate: rate})))
	return ts
}

// postBatch posts body to the batch endpoint, sending org in quotaOrgHeader
// when it is non-empty, and returns the status and the raw response body.
func postBatch(t *testing.T, url, org string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+batchLoadPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set(quotaOrgHeader, org)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST batch: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}
	return resp.StatusCode, raw
}

// batchLoadIDs posts ids as org, asserts the 200 batch status, and decodes the
// per-item envelopes.
func batchLoadIDs(t *testing.T, url, org string, ids ...string) []server.BatchLoadEnvelope {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	status, raw := postBatch(t, url, org, body)
	if status != http.StatusOK {
		t.Fatalf("batch status = %d (%s), want 200", status, raw)
	}
	var out []server.BatchLoadEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, raw)
	}
	if len(out) != len(ids) {
		t.Fatalf("len = %d, want %d", len(out), len(ids))
	}
	return out
}

// itemCode returns an item's error code, or "" for an item with no error.
func itemCode(e server.BatchLoadEnvelope) string {
	if e.Error == nil {
		return ""
	}
	return e.Error.Code
}

// wantItemOK asserts that item i loaded.
func wantItemOK(t *testing.T, out []server.BatchLoadEnvelope, i int) {
	t.Helper()
	if out[i].Status != "ok" || out[i].Error != nil {
		t.Errorf("item %d (%s) = status %q, error %+v, want ok", i, out[i].ID, out[i].Status, out[i].Error)
	}
}

// wantItemQuota asserts that item i is a refused quota item: the enriched
// envelope and no loaded content.
func wantItemQuota(t *testing.T, out []server.BatchLoadEnvelope, i int) {
	t.Helper()
	e := out[i]
	if e.Status != "error" || itemCode(e) != codeMaterialize {
		t.Fatalf("item %d (%s) = status %q, code %q, want error %s", i, e.ID, e.Status, itemCode(e), codeMaterialize)
	}
	if !e.Error.Retryable || e.Error.SuggestedAction == "" {
		t.Errorf("item %d envelope not enriched: %+v", i, e.Error)
	}
	if e.ManifestBody != "" || e.ContentHash != "" || e.DeliveryHash != "" {
		t.Errorf("item %d carries content: manifest %q, content_hash %q, delivery_hash %q",
			i, e.ManifestBody, e.ContentHash, e.DeliveryHash)
	}
}

// Spec: §7.6.2, §4.7.8 — each item is charged in request order against the
// tenant's materialization rate. With a rate of 1, the first item spends the
// only token, and it and every later item come back as per-item quota errors
// inside the 200 response. The batch leaves the bucket empty, so an immediate
// load_artifact is refused too. The bucket refills one token per second, and
// the follow-up request runs well inside that window.
// Matrix: §6.10 (quota.materialize_rate_exceeded)
func TestBatchLoad_ChargesEachItemAndRefusesThePrefixTail(t *testing.T) {
	t.Parallel()
	ts := newRatedBatchFixture(t, 1)

	out := batchLoadIDs(t, ts.URL, "", "team/a", "team/b", "team/a")
	wantItemOK(t, out, 0)
	wantItemQuota(t, out, 1)
	wantItemQuota(t, out, 2)

	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=team/a")
	if err != nil {
		t.Fatalf("GET load_artifact: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read load_artifact: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests || tenantErrCode(t, raw) != codeMaterialize {
		t.Errorf("load_artifact after batch = %d (%s), want 429 %s", resp.StatusCode, raw, codeMaterialize)
	}
}

// Spec: §7.6.2, §4.7.8 — a duplicate ID costs one token per occurrence. With a
// rate of 2, both occurrences of team/a are admitted and team/b is refused.
func TestBatchLoad_ChargesDuplicateOccurrences(t *testing.T) {
	t.Parallel()
	ts := newRatedBatchFixture(t, 2)

	out := batchLoadIDs(t, ts.URL, "", "team/a", "team/a", "team/b")
	wantItemOK(t, out, 0)
	wantItemOK(t, out, 1)
	wantItemQuota(t, out, 2)
}

// Spec: §7.6.2, §4.7.8 — an item is charged whatever its outcome. A missing ID
// spends the only token and comes back as visibility.denied, so the visible
// team/a after it is refused by the quota.
func TestBatchLoad_ChargesItemsThatFailToLoad(t *testing.T) {
	t.Parallel()
	ts := newRatedBatchFixture(t, 1)

	out := batchLoadIDs(t, ts.URL, "", "team/missing", "team/a")
	if itemCode(out[0]) != "visibility.denied" {
		t.Errorf("item 0 code = %q, want visibility.denied", itemCode(out[0]))
	}
	wantItemQuota(t, out, 1)
}

// Spec: §7.6.2, §4.7.8 — a request rejected as a whole charges nothing. With a
// rate of 1, a wrong method, a malformed body, an empty ID list, and a list
// above the cap each fail with a 4xx, and the single token is still there for
// the valid batch that follows.
func TestBatchLoad_RequestRejectionsChargeNothing(t *testing.T) {
	t.Parallel()
	ts := newRatedBatchFixture(t, 1)

	resp, err := http.Get(ts.URL + batchLoadPath)
	if err != nil {
		t.Fatalf("GET batch: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("GET batch = %d, want 4xx", resp.StatusCode)
	}
	over, err := json.Marshal(map[string]any{"ids": strings.Split(strings.Repeat("x,", server.BatchLoadCap)+"x", ",")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for name, body := range map[string][]byte{
		"malformed": []byte("{"),
		"empty ids": []byte(`{"ids":[]}`),
		"above cap": over,
	} {
		if status, raw := postBatch(t, ts.URL, "", body); status < 400 || status >= 500 {
			t.Errorf("%s batch = %d (%s), want 4xx", name, status, raw)
		}
	}

	out := batchLoadIDs(t, ts.URL, "", "team/a")
	wantItemOK(t, out, 0)
}

// Spec: §7.6.2, §4.7.8, §6.3.1 — a batch is charged to the tenant routing
// resolved, under the record routing carried. Tenant A stores a rate of 1, so its second item is
// refused. Tenant B stores no rate and takes the deployment default of 2, so
// neither of its items is. The fixture holds no artifacts, so only the
// presence or absence of the quota code is asserted.
func TestBatchLoad_ChargesTheRoutedTenant(t *testing.T) {
	t.Parallel()
	f := newQuotaRoutingFixture(t)

	a := batchLoadIDs(t, f.url, quotaTenantA, "x", "y")
	if itemCode(a[0]) == codeMaterialize || itemCode(a[1]) != codeMaterialize {
		t.Errorf("tenant A codes = %q, %q, want only item 1 refused with %s",
			itemCode(a[0]), itemCode(a[1]), codeMaterialize)
	}
	for i, e := range batchLoadIDs(t, f.url, quotaTenantB, "x", "y") {
		if itemCode(e) == codeMaterialize {
			t.Errorf("tenant B item %d refused with %s, want admitted", i, codeMaterialize)
		}
	}
}
