package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/attest/ghactions"
	"github.com/lennylabs/podium/pkg/store"
)

// fakeGitHub answers repository records by path, counts every request, and
// records the Authorization header of the last one.
type fakeGitHub struct {
	*httptest.Server
	requests atomic.Int32
	auth     atomic.Value
}

func newFakeGitHub(t *testing.T, records map[string]string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.auth.Store(r.Header.Get("Authorization"))
		body, ok := records[r.URL.Path]
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		case body == "slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case body == "500":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func ghRecord(fullName, branch, visibility string) string {
	return `{"id": 123456, "full_name": "` + fullName + `", "default_branch": "` + branch + `", "visibility": "` + visibility + `", "owner": {"id": 7890}}`
}

// presetCall parses register-style trust flags from args and resolves the
// preset against api.
func presetCall(t *testing.T, api, repo string, env map[string]string, args ...string) (map[string]any, int, string) {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	trust := addTrustFlags(fs, false)
	ref := fs.String("ref", "", "")
	preset := fs.String("trust-preset", "github-actions", "")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	members, code := resolveTrustPreset(context.Background(),
		presetRequest{name: *preset, repo: repo, ref: *ref, trust: trust, set: setFlags(fs)},
		presetEnv{getenv: func(k string) string { return env[k] }, apiBase: api, stderr: &stderr})
	return members, code, stderr.String()
}

// Spec: §7.3.1 (Trust preset) — the preset resolves the policy from the
// record, sends the token as a bearer token, prints the resolved policy,
// and lets --trust, --ref, and --trust-signer replace their values.
func TestTrustPreset_Resolves(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{"/repos/acme/finance": ghRecord("acme/finance", "main", "public")})
	members, code, stderr := presetCall(t, api.URL, "https://github.com/acme/finance.git", map[string]string{"GITHUB_TOKEN": "tok"})
	if code != -1 {
		t.Fatalf("code = %d\n%s", code, stderr)
	}
	reqs := members["trust_requirements"].([]store.TrustRequirement)
	s := reqs[0].Signers[0]
	if members["ref"] != "podium/signed" || members["trust_mode"] != "require" || len(reqs) != 1 ||
		s.Identity != "https://github.com/acme/finance/.github/workflows/podium-attest.yml@refs/heads/main" ||
		s.SourceRepositoryID != "123456" || s.SourceRepositoryOwnerID != "7890" || s.SourceRepository != "https://github.com/acme/finance" {
		t.Errorf("members = %+v", members)
	}
	if api.auth.Load() != "Bearer tok" {
		t.Errorf("Authorization = %v", api.auth.Load())
	}
	if !strings.Contains(stderr, "trust preset github-actions resolved for acme/finance:") || !strings.Contains(stderr, `"source_repository_id": "123456"`) {
		t.Errorf("stderr lacks the resolved policy:\n%s", stderr)
	}
	b, _ := json.Marshal(members)
	if !strings.Contains(string(b), `"trust_requirements":[{"signers":[{"issuer":"https://token.actions.githubusercontent.com"`) {
		t.Errorf("members encode as %s", b)
	}

	members, code, _ = presetCall(t, api.URL, "git@github.com:acme/finance.git", nil, "--trust", "warn", "--ref", "signed",
		"--trust-signer", "https://token.actions.githubusercontent.com=https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main")
	s = members["trust_requirements"].([]store.TrustRequirement)[0].Signers[0]
	if code != -1 || members["trust_mode"] != "warn" || members["ref"] != "signed" ||
		!strings.HasPrefix(s.Identity, "https://github.com/acme/.github/") || s.SourceRepositoryRef != "refs/heads/main" {
		t.Errorf("overrides: code %d members %+v", code, members)
	}
}

// Spec: §7.3.1 (Trust preset) — a usage error exits 2 before any request.
func TestTrustPreset_UsageErrors(t *testing.T) {
	api := newFakeGitHub(t, nil)
	cases := []struct {
		name, repo string
		env        map[string]string
		args       []string
		want       string
	}{
		{"unknown preset", "https://github.com/acme/x", nil, []string{"--trust-preset", "gitlab"}, "is no preset"},
		{"no repo", "", nil, nil, "applies to a --repo registration"},
		{"binding flag", "https://github.com/acme/x", nil, []string{"--trust-signer", "a=b", "--trust-source-owner-id", "1", "--trust-instance", "github"}, "sets --trust-source-owner-id, --trust-instance"},
		{"timeout", "https://github.com/acme/x", map[string]string{"PODIUM_GITHUB_API_TIMEOUT": "0s"}, nil, "PODIUM_GITHUB_API_TIMEOUT"},
	}
	for _, c := range cases {
		_, code, stderr := presetCall(t, api.URL, c.repo, c.env, c.args...)
		if code != 2 || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: code %d, stderr %q, want %q", c.name, code, stderr, c.want)
		}
	}
	if n := api.requests.Load(); n != 0 {
		t.Errorf("usage errors sent %d requests", n)
	}
	api2 := newFakeGitHub(t, map[string]string{"/repos/acme/x": ghRecord("acme/x", "main", "public")})
	if _, code, stderr := presetCall(t, api2.URL, "https://github.com/acme/x", nil, "--trust-signer", "no-equals"); code != 2 || !strings.Contains(stderr, "is not <issuer>=<identity>") {
		t.Errorf("bad signer: %d %q", code, stderr)
	}
}

// Spec: §7.3.1 (Trust preset) — every refusal registers nothing, exits 1,
// and names the explicit flags; a --repo outside github.com sends no
// request even with a token set.
func TestTrustPreset_Refusals(t *testing.T) {
	api := newFakeGitHub(t, map[string]string{
		"/repos/acme/old":     ghRecord("acme/new", "main", "public"),
		"/repos/acme/secret":  ghRecord("acme/secret", "main", "private"),
		"/repos/acme/inside":  ghRecord("acme/inside", "main", "internal"),
		"/repos/acme/broken":  "500",
		"/repos/acme/slowpoe": "slow",
	})
	token := map[string]string{"GH_TOKEN": "tok"}
	_, code, stderr := presetCall(t, api.URL, "https://gitlab.com/acme/finance", token)
	if code != 1 || !strings.Contains(stderr, "so no request was sent") || !strings.Contains(stderr, "--trust-signer '<issuer>=<identity>'") {
		t.Errorf("other host: %d %q", code, stderr)
	}
	if n := api.requests.Load(); n != 0 {
		t.Fatalf("the other host's registration sent %d requests", n)
	}

	cases := []struct {
		repo string
		env  map[string]string
		want []string
	}{
		{"https://github.com/acme/old", nil, []string{"with the record of acme/new", "Register with --repo https://github.com/acme/new"}},
		{"https://github.com/acme/secret", nil, []string{"as private", "--trust-instance github", "--trust-source-repository-id 123456"}},
		{"https://github.com/acme/inside", nil, []string{"as internal", "--trust-instance github"}},
		{"https://github.com/acme/gone", nil, []string{"HTTP 404", "GH_TOKEN or GITHUB_TOKEN", "--trust-source-repository-id '<repository-id>'"}},
		{"https://github.com/acme/broken", nil, []string{"HTTP 500", "--trust-signer"}},
		{"https://github.com/acme/slowpoe", map[string]string{"PODIUM_GITHUB_API_TIMEOUT": "100ms"}, []string{"no answer within 100ms (PODIUM_GITHUB_API_TIMEOUT)"}},
	}
	for _, c := range cases {
		members, code, stderr := presetCall(t, api.URL, c.repo, c.env)
		if code != 1 || members != nil {
			t.Errorf("%s: code %d members %v", c.repo, code, members)
		}
		for _, w := range append(c.want, "othing was registered") {
			if !strings.Contains(stderr, w) {
				t.Errorf("%s: stderr lacks %q:\n%s", c.repo, w, stderr)
			}
		}
	}
	if got := visibilityName(ghactions.Record{}); got != "not public" {
		t.Errorf("visibilityName = %q", got)
	}
}
