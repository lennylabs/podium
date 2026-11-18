package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// anonymousOrTestUser resolves a request carrying no test user as the
// anonymous caller, which sees public layers only (§4.6), and any other
// request as the harness does.
func anonymousOrTestUser(r *http.Request) layer.Identity {
	sub := r.Header.Get("X-Test-User")
	if sub == "" {
		return layer.Identity{}
	}
	id := layer.Identity{Sub: sub, IsAuthenticated: true}
	if g := r.Header.Get("X-Test-Group"); g != "" {
		id.Groups = []string{g}
	}
	return id
}

// overlapBody returns the status and the compacted body of GET /v1/overlap
// with the given query for user in group, where an empty user is anonymous.
func (h *overlapHarness) overlapBody(query url.Values, user, group string) (int, string) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.api.URL+"/v1/overlap?"+query.Encode(), nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
		req.Header.Set("X-Test-Group", group)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("GET /v1/overlap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read /v1/overlap: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		h.t.Fatalf("compact /v1/overlap: %v\n%s", err, body)
	}
	return resp.StatusCode, compact.String()
}

// Spec: §13.10, §7.3.1 — the similarity surface's server-driven Read cell.
// The overlap read the web UI issues is offered to every caller that can
// search, an anonymous caller included, and names only artifacts the caller
// can see. A read restricted to an artifact the caller cannot see answers the
// same body as a read for an artifact that does not exist and for one that
// overlaps nothing, so the viewer has nothing to tell the three apart by; a
// caller who can see the artifact receives its overlap.
func TestWebUISimilarity_HiddenArtifactReadsAsNoOverlap(t *testing.T) {
	t.Parallel()
	h := newOverlapHarness(t, server.WithIdentityResolver(anonymousOrTestUser))
	h.write("secret", "sec/twin", "1.0.0", "secretive twin of the invoice skill")
	h.reingest("secret")
	h.write("team", "fin/invoices", "1.0.0", "reconciles invoices for the month end")
	h.write("team", "fin/payables", "1.0.0", "reconciles payables for the month end")
	h.write("team", "ops/onboard", "1.0.0", "onboarding guide for a new hire's first week")
	h.reingest("team")

	status, catalog := h.overlapBody(url.Values{}, "", "")
	if status != http.StatusOK {
		t.Fatalf("anonymous GET /v1/overlap: status %d\n%s", status, catalog)
	}
	if !strings.Contains(catalog, `"artifact_id":"fin/invoices"`) {
		t.Errorf("the anonymous report lacks the public overlap:\n%s", catalog)
	}
	if strings.Contains(catalog, "sec/twin") {
		t.Errorf("the anonymous report names an artifact of a group layer:\n%s", catalog)
	}

	read := func(id string) string {
		t.Helper()
		status, body := h.overlapBody(url.Values{"artifact": {id}}, "", "")
		if status != http.StatusOK {
			t.Fatalf("anonymous GET /v1/overlap?artifact=%s: status %d\n%s", id, status, body)
		}
		return body
	}
	hidden := read("sec/twin")
	unknown := read("no/such-artifact")
	quiet := read("ops/onboard")
	if hidden != unknown || hidden != quiet {
		t.Errorf("the reads differ:\nhidden:  %s\nunknown: %s\nquiet:   %s", hidden, unknown, quiet)
	}
	if !strings.Contains(hidden, `"diagnostics":[]`) {
		t.Errorf("a hidden artifact's read carries diagnostics: %s", hidden)
	}

	_, visible := h.overlapBody(url.Values{"artifact": {"sec/twin"}}, "bob", "eng")
	if !strings.Contains(visible, `"artifact_id":"sec/twin"`) {
		t.Errorf("a caller who can see sec/twin receives no overlap for it:\n%s", visible)
	}
}
