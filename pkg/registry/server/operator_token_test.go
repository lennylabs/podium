package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/server"
)

const testOperatorToken = "0123456789abcdef0123456789abcdef"

// postPass sends a POST to path with the operator token header when token is
// not empty, and returns the status and the §6.10 code.
func postPass(t *testing.T, base, path, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(server.OperatorTokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, env.Code
}

var operatorPasses = []string{"/v1/admin/reembed", "/v1/admin/overlap/rebuild"}

// Spec: §4.7, §7.3.1, §13.10 — in public mode the re-embed and overlap
// rebuild endpoints admit a request carrying PODIUM_OPERATOR_TOKEN in
// X-Podium-Operator-Token and refuse a request without it, or with another
// value, with auth.forbidden.
func TestOperatorPass_PublicModeRequiresTheToken(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "", nil,
		server.WithPublicMode(), server.WithUnauthenticatedReembed(), server.WithOperatorToken(testOperatorToken))
	for _, path := range operatorPasses {
		for _, bad := range []string{"", "wrong-token", testOperatorToken[:31]} {
			if status, code := postPass(t, ts.URL, path, bad); status != http.StatusForbidden || code != "auth.forbidden" {
				t.Errorf("%s with token %q: %d %q, want 403 auth.forbidden", path, bad, status, code)
			}
		}
		if status, code := postPass(t, ts.URL, path, testOperatorToken); status == http.StatusForbidden {
			t.Errorf("%s with the operator token: 403 %q, want the handler to run", path, code)
		}
	}
}

// Spec: §4.7, §7.3.1, §13.10 — a public-mode registry with no operator token
// refuses both passes for every request, the would-be token included.
func TestOperatorPass_PublicModeWithoutTokenRefusesEveryRequest(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "", nil, server.WithPublicMode(), server.WithUnauthenticatedReembed())
	for _, path := range operatorPasses {
		for _, token := range []string{"", testOperatorToken} {
			status, code := postPass(t, ts.URL, path, token)
			if status != http.StatusForbidden || code != "auth.forbidden" {
				t.Errorf("%s with token %q: %d %q, want 403 auth.forbidden", path, token, status, code)
			}
		}
	}
}

// Spec: §4.7, §7.3.1 — a registry with no identity provider still admits
// both passes without a token, because it authenticates no caller and is not
// in public mode, and the token is not read there.
func TestOperatorPass_NoIdentityProviderNeedsNoToken(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "", nil, server.WithUnauthenticatedReembed(), server.WithOperatorToken(testOperatorToken))
	for _, path := range operatorPasses {
		if status, code := postPass(t, ts.URL, path, ""); status == http.StatusForbidden {
			t.Errorf("%s without a token: 403 %q, want the handler to run", path, code)
		}
	}
}
