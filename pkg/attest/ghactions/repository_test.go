package ghactions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Spec: §7.3.1 (Trust preset) — the preset covers a --repo on github.com in
// the https form and the SSH forms, and every other value is refused before
// any request.
func TestParseRepoURL(t *testing.T) {
	t.Parallel()
	ok := map[string]Repo{
		"https://github.com/acme/finance":       {"acme", "finance"},
		"https://github.com/acme/finance.git":   {"acme", "finance"},
		"https://github.com/acme/finance/":      {"acme", "finance"},
		"https://GitHub.com/Acme/Fin.Ance-x_y":  {"Acme", "Fin.Ance-x_y"},
		"git@github.com:acme/finance.git":       {"acme", "finance"},
		"git@github.com:acme/finance":           {"acme", "finance"},
		"ssh://git@github.com/acme/finance.git": {"acme", "finance"},
		"ssh://git@github.com/acme/finance":     {"acme", "finance"},
		"https://github.com/acme/.github":       {"acme", ".github"},
	}
	for raw, want := range ok {
		got, err := ParseRepoURL(raw)
		if err != nil || got != want {
			t.Errorf("ParseRepoURL(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"", "https://gitlab.com/acme/finance", "https://github.com.evil.net/acme/finance",
		"https://github.com/acme", "https://github.com/acme/finance/tree", "https://github.com/acme/finance.git/",
		"https://user@github.com/acme/finance", "https://github.com:443/acme/finance", "https://github.com:/acme/finance",
		"https://github.com/acme/finance?x=1", "https://github.com/acme/finance#x", "https://github.com/acme/fin%61nce",
		"http://github.com/acme/finance", "ssh://github.com/acme/finance", "ssh://alice@github.com/acme/finance",
		"ssh://git:pw@github.com/acme/finance", "git@gitlab.com:acme/finance", "git@github.com:acme/finance/",
		"git@github.com/acme/finance", "https://github.com/acme/..", "https://github.com/ac me/finance",
		"file:///srv/acme/finance", "github.com/acme/finance",
	} {
		if got, err := ParseRepoURL(raw); !errors.Is(err, ErrNotGitHub) {
			t.Errorf("ParseRepoURL(%q) = %v, %v; want ErrNotGitHub", raw, got, err)
		}
	}
}

const recordJSON = `{"id": 123456, "full_name": "acme/finance", "default_branch": "main", "visibility": "public", "private": false, "owner": {"id": 7890, "login": "acme"}}`

// Spec: §7.3.1 (Trust preset) — the request is one GET of the repository
// record with the token as a bearer token, and the record supplies the
// identifiers, the default branch, the visibility, and the current name.
func TestFetch_ReadsTheRecord(t *testing.T) {
	t.Parallel()
	reqs := make(chan *http.Request, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs <- r.Clone(context.Background())
		_, _ = w.Write([]byte(recordJSON))
	}))
	defer srv.Close()
	rec, err := Fetcher{Base: srv.URL + "/", Token: "tok", Timeout: time.Second}.Fetch(context.Background(), Repo{"acme", "finance"})
	if err != nil {
		t.Fatal(err)
	}
	want := Record{ID: 123456, OwnerID: 7890, FullName: "acme/finance", DefaultBranch: "main", Visibility: "public"}
	if rec != want || !rec.Public() || rec.Repo() != (Repo{"acme", "finance"}) {
		t.Errorf("record = %+v, want %+v", rec, want)
	}
	got := <-reqs
	if got.Method != http.MethodGet || got.URL.Path != "/repos/acme/finance" ||
		got.Header.Get("Authorization") != "Bearer tok" || got.Header.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("request = %s %s %v", got.Method, got.URL.Path, got.Header)
	}

	// No token, no Authorization header.
	if _, err := (Fetcher{Base: srv.URL, Timeout: time.Second}).Fetch(context.Background(), Repo{"acme", "finance"}); err != nil {
		t.Fatal(err)
	}
	if h := (<-reqs).Header.Get("Authorization"); h != "" {
		t.Errorf("Authorization = %q with no token", h)
	}
}

// Spec: §7.3.1 (Trust preset) — the API answers a renamed repository's old
// name with a redirect on api.github.com, which the request follows; a
// redirect to another host is refused and that host receives nothing.
func TestFetch_Redirects(t *testing.T) {
	t.Parallel()
	var foreign atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreign.Add(1)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/old":
			http.Redirect(w, r, "/repositories/123456", http.StatusMovedPermanently)
		case "/repositories/123456":
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("the redirected request lost its token")
			}
			_, _ = w.Write([]byte(recordJSON))
		case "/repos/acme/away":
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		case "/repos/acme/loop":
			http.Redirect(w, r, "/repos/acme/loop", http.StatusFound)
		}
	}))
	defer srv.Close()
	f := Fetcher{Base: srv.URL, Token: "tok", Timeout: time.Second}
	rec, err := f.Fetch(context.Background(), Repo{"acme", "old"})
	if err != nil || rec.FullName != "acme/finance" {
		t.Fatalf("renamed: %+v, %v", rec, err)
	}
	if _, err := f.Fetch(context.Background(), Repo{"acme", "away"}); err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Errorf("cross-host redirect: %v", err)
	}
	if n := foreign.Load(); n != 0 {
		t.Errorf("the other host received %d requests", n)
	}
	if _, err := f.Fetch(context.Background(), Repo{"acme", "loop"}); err == nil || !strings.Contains(err.Error(), "more than 5 redirects") {
		t.Errorf("redirect loop: %v", err)
	}
}

// Spec: §7.3.1 (Trust preset), §13.12 (PODIUM_GITHUB_API_TIMEOUT) — an
// answer other than 200, a request that reaches its deadline, and a body
// that is no repository record each fail the request.
func TestFetch_Failures(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/repos/acme/slow":
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/repos/acme/huge":
			_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("a", maxRecordBytes) + `"}`))
		case "/repos/acme/garbage":
			_, _ = w.Write([]byte(`not json`))
		case "/repos/acme/noids":
			_, _ = w.Write([]byte(`{"full_name":"acme/noids","default_branch":"main","visibility":"public"}`))
		case "/repos/acme/badname":
			_, _ = w.Write([]byte(`{"id":1,"owner":{"id":2},"full_name":"acme","default_branch":"main"}`))
		case "/repos/acme/badbranch":
			_, _ = w.Write([]byte(`{"id":1,"owner":{"id":2},"full_name":"acme/badbranch","default_branch":"a b"}`))
		}
	}))
	defer srv.Close()
	defer close(release)
	f := Fetcher{Base: srv.URL, Timeout: 2 * time.Second}

	_, err := f.Fetch(context.Background(), Repo{"acme", "missing"})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusNotFound || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("404: %v", err)
	}
	quick := Fetcher{Base: srv.URL, Timeout: 50 * time.Millisecond}
	if _, err := quick.Fetch(context.Background(), Repo{"acme", "slow"}); err == nil || !strings.Contains(err.Error(), TimeoutEnv) {
		t.Errorf("deadline: %v", err)
	}
	for name, want := range map[string]string{
		"huge":      "exceeds",
		"garbage":   "no repository record",
		"noids":     "no repository or owner identifier",
		"badname":   "is not <owner>/<repo>",
		"badbranch": "is not a branch name",
	} {
		if _, err := f.Fetch(context.Background(), Repo{"acme", name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	if _, err := (Fetcher{Base: "::", Timeout: time.Second}).Fetch(context.Background(), Repo{"a", "b"}); err == nil {
		t.Error("a base that is no URL was accepted")
	}
	if _, err := (Fetcher{Base: "http://127.0.0.1:1", Timeout: time.Second}).Fetch(context.Background(), Repo{"a", "b"}); err == nil || !strings.Contains(err.Error(), "request a/b") {
		t.Errorf("refused connection: %v", err)
	}
}

// Spec: §7.3.1 (Trust preset) — a record without visibility reads its
// private flag, and a record with neither is not public.
func TestDecodeRecord_Visibility(t *testing.T) {
	t.Parallel()
	for body, public := range map[string]bool{
		`{"id":1,"owner":{"id":2},"full_name":"a/b","default_branch":"main","private":false}`:                        true,
		`{"id":1,"owner":{"id":2},"full_name":"a/b","default_branch":"main","private":true}`:                         false,
		`{"id":1,"owner":{"id":2},"full_name":"a/b","default_branch":"main"}`:                                        false,
		`{"id":1,"owner":{"id":2},"full_name":"a/b","default_branch":"main","visibility":"internal","private":true}`: false,
	} {
		rec, err := decodeRecord([]byte(body))
		if err != nil || rec.Public() != public {
			t.Errorf("%s: public=%v err=%v, want %v", body, rec.Public(), err, public)
		}
	}
}

func TestValidBranch(t *testing.T) {
	t.Parallel()
	for b, want := range map[string]bool{
		"main": true, "release/1.x": true, "": false, "-x": false, "a..b": false, "a b": false,
		"a~1": false, "a^": false, "a:b": false, "a?": false, "a*": false, "a[": false, `a\b`: false, "a\x7f": false,
		"x$(touch${IFS}/tmp/p)": false, "a{b}": false, "a'b": false, "a//b": false, "a/": false, "feature/x-1_2.3": true,
	} {
		if validBranch(b) != want {
			t.Errorf("validBranch(%q) != %v", b, want)
		}
	}
}

// Spec: §7.3.1 (Trust preset), §13.12 (PODIUM_GITHUB_API_TIMEOUT)
func TestTokenAndTimeoutFromEnv(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := TokenFromEnv(env(map[string]string{"GH_TOKEN": "a", "GITHUB_TOKEN": "b"})); got != "a" {
		t.Errorf("token = %q, want GH_TOKEN", got)
	}
	if got := TokenFromEnv(env(map[string]string{"GITHUB_TOKEN": "b"})); got != "b" {
		t.Errorf("token = %q, want GITHUB_TOKEN", got)
	}
	for v, want := range map[string]time.Duration{"": DefaultTimeout, "  ": DefaultTimeout, "5s": 5 * time.Second, " 2m ": 2 * time.Minute} {
		if d, err := TimeoutFromEnv(env(map[string]string{TimeoutEnv: v})); err != nil || d != want {
			t.Errorf("timeout %q = %v, %v; want %v", v, d, err, want)
		}
	}
	for _, v := range []string{"0", "-1s", "10", "soon"} {
		if _, err := TimeoutFromEnv(env(map[string]string{TimeoutEnv: v})); err == nil || !strings.Contains(err.Error(), TimeoutEnv) {
			t.Errorf("timeout %q: %v", v, err)
		}
	}
}

// Spec: §7.3.1 (Trust preset) — a record whose default branch holds a shell
// or YAML metacharacter is refused, so the branch never reaches a printed
// command or the workflow.
func TestDecodeRecord_RefusesAShellBranch(t *testing.T) {
	t.Parallel()
	body := `{"id":1,"full_name":"acme/x","default_branch":"x$(touch${IFS}/tmp/pwned)","visibility":"private","owner":{"id":2}}`
	if _, err := decodeRecord([]byte(body)); err == nil || !strings.Contains(err.Error(), "is not a branch name") {
		t.Errorf("decodeRecord = %v", err)
	}
}

// Spec: §7.3.1 (Trust preset) — ShellQuote leaves a word of safe characters
// unchanged and single-quotes any other, escaping each single quote.
func TestShellQuote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://github.com/acme/x": "https://github.com/acme/x",
		"refs/heads/main":           "refs/heads/main",
		"a=b@c:d,e%f+g":             "a=b@c:d,e%f+g",
		"":                          "''",
		"<id>":                      "'<id>'",
		"x$(touch /tmp/p)":          "'x$(touch /tmp/p)'",
		"it's":                      `'it'\''s'`,
		"a b":                       "'a b'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
