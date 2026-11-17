package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// overlapArtifactJSON is the artifact part of a GET /v1/overlap diagnostic or
// peer.
type overlapArtifactJSON struct {
	ArtifactID  string `json:"artifact_id"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Layer       string `json:"layer"`
}

// rawOverlap returns the status and body of GET /v1/overlap for user in group.
func (h *overlapHarness) rawOverlap(user, group string) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.api.URL+"/v1/overlap", nil)
	req.Header.Set("X-Test-User", user)
	req.Header.Set("X-Test-Group", group)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("GET /v1/overlap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read /v1/overlap: %v", err)
	}
	return resp.StatusCode, body
}

// Spec: §7.3.1 — each diagnostic of GET /v1/overlap and each of its peers
// carries the artifact's description, type, and serving layer, on
// lint.thin_description, lint.colliding_descriptions, and
// lint.semantic_overlap, and a caller never receives the fields of an
// artifact it cannot see.
func TestSemanticOverlap_ReportCarriesArtifactFields(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t)
	h.write("secret", "sec/copy", "1.0.0", "settles vendor invoices for the month")
	h.reingest("secret")
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables")
	h.write("team", "fin/settle", "1.0.0", "settles vendor invoices for the month")
	h.reingest("team")
	h.write("hook", "ops/settle", "1.0.0", "settles vendor invoices for the month")
	h.reingest("hook")

	status, body := h.rawOverlap("alice", "")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/overlap: status %d\n%s", status, body)
	}
	var report struct {
		Diagnostics []struct {
			overlapArtifactJSON
			Code  string `json:"code"`
			Peers []struct {
				overlapArtifactJSON
				Similarity float64 `json:"similarity"`
			} `json:"peers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	seen := map[string]overlapArtifactJSON{}
	for _, d := range report.Diagnostics {
		seen[d.Code+" "+d.ArtifactID] = d.overlapArtifactJSON
		for _, p := range d.Peers {
			seen[d.Code+" "+d.ArtifactID+" > "+p.ArtifactID] = p.overlapArtifactJSON
		}
	}
	invoices := overlapArtifactJSON{"fin/invoices", "reconciles invoices", "agent", "team"}
	payables := overlapArtifactJSON{"fin/payables", "reconciles payables", "agent", "team"}
	settle := overlapArtifactJSON{"fin/settle", "settles vendor invoices for the month", "agent", "team"}
	opsSettle := overlapArtifactJSON{"ops/settle", "settles vendor invoices for the month", "agent", "hook"}
	want := map[string]overlapArtifactJSON{
		"lint.thin_description fin/invoices":                  invoices,
		"lint.semantic_overlap fin/invoices":                  invoices,
		"lint.semantic_overlap fin/invoices > fin/payables":   payables,
		"lint.colliding_descriptions fin/settle":              settle,
		"lint.colliding_descriptions fin/settle > ops/settle": opsSettle,
		"lint.colliding_descriptions ops/settle":              opsSettle,
		"lint.colliding_descriptions ops/settle > fin/settle": settle,
	}
	for key, w := range want {
		if got, ok := seen[key]; !ok || got != w {
			t.Errorf("%s = %+v (present %v), want %+v", key, got, ok, w)
		}
	}
	for _, leak := range []string{"sec/copy", `"secret"`} {
		if strings.Contains(string(body), leak) {
			t.Errorf("alice's report contains %s:\n%s", leak, body)
		}
	}
	_, bob := h.rawOverlap("bob", "eng")
	var compact bytes.Buffer
	if err := json.Compact(&compact, bob); err != nil {
		t.Fatalf("compact bob's report: %v", err)
	}
	if !strings.Contains(compact.String(), `"artifact_id":"sec/copy","description":"settles vendor invoices for the month","type":"agent","layer":"secret"`) {
		t.Errorf("bob's report lacks the restricted artifact's fields:\n%s", bob)
	}
}
