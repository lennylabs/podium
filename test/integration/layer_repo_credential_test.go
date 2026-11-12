package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// The credential fixtures are distinctive on purpose. A single-letter fixture
// matches unrelated text such as the "source: unreachable" prefix.
const (
	repoCredentialUser     = "alice-user"
	repoCredentialPassword = "s3cr3tpw"
)

// remoteRequest is one request a failingGitRemote received.
type remoteRequest struct {
	// authorized reports whether the request carried an Authorization
	// header, and user and password the basic-auth pair it decoded to.
	authorized     bool
	user, password string
}

// failingGitRemote is an HTTP remote that answers every request with 500 and
// records every request with the basic-auth pair it carried.
type failingGitRemote struct {
	*httptest.Server

	// mu guards requests, which the handler appends to from the server's
	// request goroutines.
	mu       sync.Mutex
	requests []remoteRequest
}

func newFailingGitRemote(t *testing.T) *failingGitRemote {
	t.Helper()
	remote := &failingGitRemote{}
	remote.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := remoteRequest{authorized: r.Header.Get("Authorization") != ""}
		req.user, req.password, _ = r.BasicAuth()
		remote.mu.Lock()
		remote.requests = append(remote.requests, req)
		remote.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(remote.Close)
	return remote
}

// requestsSince returns a copy of the requests recorded after the first n.
func (f *failingGitRemote) requestsSince(n int) []remoteRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n > len(f.requests) {
		n = len(f.requests)
	}
	return append([]remoteRequest(nil), f.requests[n:]...)
}

// requestCount returns how many requests the remote has received.
func (f *failingGitRemote) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// basicAuth returns the basic-auth pairs of the requests recorded after the
// first n that carried one.
func (f *failingGitRemote) basicAuth(n int) (users, passwords []string) {
	for _, req := range f.requestsSince(n) {
		if req.authorized {
			users = append(users, req.user)
			passwords = append(passwords, req.password)
		}
	}
	return users, passwords
}

// notificationRecorder is a §9.1 notification sink that keeps every title and
// body it receives.
type notificationRecorder struct {
	// mu guards texts, which handlers on separate request goroutines append to.
	mu    sync.Mutex
	texts []string
}

func (n *notificationRecorder) notify(_ context.Context, _, title, body string, _ map[string]string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.texts = append(n.texts, title, body)
}

func (n *notificationRecorder) recorded() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.texts...)
}

// assertNoRepoCredential fails when text carries either part of the credential
// the layer's repo was registered with.
func assertNoRepoCredential(t *testing.T, what, text string) {
	t.Helper()
	for _, part := range []string{repoCredentialUser, repoCredentialPassword} {
		if strings.Contains(text, part) {
			t.Errorf("%s contains credential part %q: %s", what, part, text)
		}
	}
}

// assertSourceUnreachable fails unless resp is the §6.10
// ingest.source_unreachable envelope with no credential part in its body.
func assertSourceUnreachable(t *testing.T, what string, resp *http.Response) {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("%s: read body: %v", what, err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("%s: status = %d, want 502 (body %s)", what, resp.StatusCode, raw)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("%s: decode body %s: %v", what, raw, err)
	}
	if envelope.Code != "ingest.source_unreachable" {
		t.Errorf("%s: code = %q, want ingest.source_unreachable", what, envelope.Code)
	}
	assertNoRepoCredential(t, what+" body", string(raw))
}

// Spec: §7.3.1 (Repository credentials) / §6.10 / §9.1 — a git layer
// registered with the repo_username and repo_token members clones with that
// credential, and a failed
// clone reports it nowhere: the 502 ingest.source_unreachable body on the
// manual reingest and on the inbound webhook route, and the operational
// notification body, carry neither part of it.
func TestLayerRepoCredential_CloneFailureReportsNoCredential(t *testing.T) {
	t.Parallel()
	st := openCredentialStore(t)
	remote := newFailingGitRemote(t)
	notifications := &notificationRecorder{}
	ts := newCredentialLayerServer(t, st, notifications)

	registered := registerThroughMembers(t, ts.URL, remote.URL+"/acme/private.git",
		repoCredentialUser, repoCredentialPassword)
	// The response reports the repo without its userinfo, and the clone below
	// still sends the stored credential.
	assertNoRepoCredential(t, "register response repo", registered.Layer.Repo)
	if wantRepo := remote.URL + "/acme/private.git"; registered.Layer.Repo != wantRepo {
		t.Errorf("register response repo = %q, want %q", registered.Layer.Repo, wantRepo)
	}

	assertSourceUnreachable(t, "manual reingest", postReingest(t, ts.URL))

	delivery := []byte(`{"ref":"refs/heads/main"}`)
	signature, err := webhook.Sign("github", delivery, registered.WebhookSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/ingest/webhook/private", bytes.NewReader(delivery))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Hub-Signature-256", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook delivery: %v", err)
	}
	assertSourceUnreachable(t, "inbound webhook", resp)

	texts := notifications.recorded()
	// Each failed trigger fires one notification, recorded as a title and a body.
	if len(texts) != 4 {
		t.Errorf("recorded %d notification texts, want 4 (a title and a body per trigger): %q", len(texts), texts)
	}
	for _, text := range texts {
		assertNoRepoCredential(t, "notification", text)
	}

	// The clone reads the registered value, so the remote still receives the
	// credential after the responses above reported none of it.
	users, passwords := remote.basicAuth(0)
	if len(users) == 0 {
		t.Fatalf("the remote recorded no basic-auth request; the clone did not send the stored credential")
	}
	for i := range users {
		if users[i] != repoCredentialUser || passwords[i] != repoCredentialPassword {
			t.Errorf("remote request %d carried basic auth %q:%q, want the stored credential", i, users[i], passwords[i])
		}
	}
}

// newCredentialLayerServer serves the layer routes and the inbound webhook
// route over st, with the git reingest runner and a notification sink.
func newCredentialLayerServer(t *testing.T, st store.Store, notifications *notificationRecorder) *httptest.Server {
	t.Helper()
	endpoint := server.NewLayerEndpoint(st, "t", server.NewModeTracker()).
		WithReingestRunner(gitReingestRunner(st)).
		WithNotifier(notifications.notify)
	// The inbound webhook route is a separate mount from the layer routes.
	mux := http.NewServeMux()
	mux.Handle("/v1/layers", endpoint.Handler())
	mux.Handle("/v1/layers/", endpoint.Handler())
	mux.Handle("/v1/ingest/webhook/", endpoint.WebhookHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// openCredentialStore opens a SQLite store on a temp file with tenant "t".
func openCredentialStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return st
}

// sendLayerJSON sends body to path on base with method and returns the
// status and the response body.
func sendLayerJSON(t *testing.T, method, base, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", path, err)
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, out
}

// registerThroughMembers registers git layer "private" on the bare remote
// repo with the credential in the repo_username and repo_token members, and
// returns the decoded response.
func registerThroughMembers(t *testing.T, base, repo, username, token string) server.LayerRegisterResponse {
	t.Helper()
	status, body := sendLayerJSON(t, http.MethodPost, base, "/v1/layers", map[string]any{
		"id": "private", "source_type": "git", "repo": repo, "ref": "main",
		"repo_username": username, "repo_token": token,
	})
	var registered server.LayerRegisterResponse
	decodeErr := json.Unmarshal(body, &registered)
	if status != http.StatusCreated || decodeErr != nil || registered.WebhookSecret == "" {
		t.Fatalf("register status=%d decode=%v secret-present=%t, want 201 with a webhook secret",
			status, decodeErr, registered.WebhookSecret != "")
	}
	for _, part := range []string{username, token} {
		if strings.Contains(string(body), part) {
			t.Errorf("register response carries credential part %q: %s", part, body)
		}
	}
	return registered
}

// postReingest triggers a manual reingest of "private".
func postReingest(t *testing.T, base string) *http.Response {
	t.Helper()
	resp, err := http.Post(base+"/v1/layers/reingest?id=private", "application/json", nil)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	return resp
}

// listedRepoCredential returns the repo_credential the list reports for
// "private", failing the test when the member is omitted.
func listedRepoCredential(t *testing.T, base string) bool {
	t.Helper()
	resp, err := http.Get(base + "/v1/layers")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Layers []struct {
			ID             string `json:"id"`
			RepoCredential *bool  `json:"repo_credential"`
		} `json:"layers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, l := range out.Layers {
		if l.ID == "private" {
			if l.RepoCredential == nil {
				t.Fatalf("the list omits repo_credential on an https git layer")
			}
			return *l.RepoCredential
		}
	}
	t.Fatalf("the list does not carry layer private")
	return false
}

// assertRemoteSaw fails unless the remote received at least one request after
// the first n and every one of them carried the basic-auth pair user and
// password, or, when user and password are both empty, no Authorization
// header.
func assertRemoteSaw(t *testing.T, remote *failingGitRemote, n int, user, password string) {
	t.Helper()
	requests := remote.requestsSince(n)
	if len(requests) == 0 {
		t.Fatalf("the remote recorded no request; the reingest did not reach it")
	}
	for i, req := range requests {
		switch {
		case user == "" && password == "":
			if req.authorized {
				t.Errorf("remote request %d carried an Authorization header after the clear", i)
			}
		case !req.authorized || req.user != user || req.password != password:
			t.Errorf("remote request %d carried basic auth %t %q:%q, want %q:%q",
				i, req.authorized, req.user, req.password, user, password)
		}
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// over SQLite, the credential a registration carries in repo_username and
// repo_token reaches the remote as the basic-auth pair of the clone, an
// update with repo_token replaces the pair while the stored username is kept,
// and clear_repo_credential stops the clone from sending any Authorization
// header. The layer object reports repo_credential true while the remote
// receives the pair and false after the clear.
func TestLayerRepoCredential_MembersReachTheRemote(t *testing.T) {
	t.Parallel()
	st := openCredentialStore(t)
	remote := newFailingGitRemote(t)
	ts := newCredentialLayerServer(t, st, &notificationRecorder{})

	registerThroughMembers(t, ts.URL, remote.URL+"/acme/private.git", "x-access-token", "tok1")
	mark := remote.requestCount()
	assertSourceUnreachable(t, "reingest with tok1", postReingest(t, ts.URL))
	assertRemoteSaw(t, remote, mark, "x-access-token", "tok1")
	if !listedRepoCredential(t, ts.URL) {
		t.Errorf("repo_credential = false while the remote receives the credential")
	}

	status, body := sendLayerJSON(t, http.MethodPut, ts.URL, "/v1/layers/update?id=private",
		map[string]any{"repo_token": "tok2"})
	if status != http.StatusOK {
		t.Fatalf("rotation: status %d, body=%s", status, body)
	}
	mark = remote.requestCount()
	assertSourceUnreachable(t, "reingest with tok2", postReingest(t, ts.URL))
	assertRemoteSaw(t, remote, mark, "x-access-token", "tok2")

	status, body = sendLayerJSON(t, http.MethodPut, ts.URL, "/v1/layers/update?id=private",
		map[string]any{"clear_repo_credential": true})
	if status != http.StatusOK {
		t.Fatalf("clear: status %d, body=%s", status, body)
	}
	mark = remote.requestCount()
	assertSourceUnreachable(t, "reingest after the clear", postReingest(t, ts.URL))
	assertRemoteSaw(t, remote, mark, "", "")
	if listedRepoCredential(t, ts.URL) {
		t.Errorf("repo_credential = true after the clear")
	}
}
