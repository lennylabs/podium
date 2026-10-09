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

// failingGitRemote is an HTTP remote that answers every request with 500 and
// records the basic-auth pair each request carried.
type failingGitRemote struct {
	*httptest.Server

	// mu guards users and passwords, which the handler appends to from the
	// server's request goroutines.
	mu        sync.Mutex
	users     []string
	passwords []string
}

func newFailingGitRemote(t *testing.T) *failingGitRemote {
	t.Helper()
	remote := &failingGitRemote{}
	remote.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); ok {
			remote.mu.Lock()
			remote.users = append(remote.users, user)
			remote.passwords = append(remote.passwords, password)
			remote.mu.Unlock()
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(remote.Close)
	return remote
}

// basicAuth returns a copy of the recorded basic-auth pairs.
func (f *failingGitRemote) basicAuth() (users, passwords []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.users...), append([]string(nil), f.passwords...)
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

// Spec: §7.3.1 (Repository credentials) / §6.10 / §9.1 — a git layer whose
// repo carries URL userinfo keeps cloning with that credential, and a failed
// clone reports it nowhere: the 502 ingest.source_unreachable body on the
// manual reingest and on the inbound webhook route, and the operational
// notification body, carry neither part of it.
func TestLayerRepoCredential_CloneFailureReportsNoCredential(t *testing.T) {
	t.Parallel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	remote := newFailingGitRemote(t)
	notifications := &notificationRecorder{}
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

	repo := "http://" + repoCredentialUser + ":" + repoCredentialPassword + "@" +
		strings.TrimPrefix(remote.URL, "http://") + "/acme/private.git"
	registerBody, err := json.Marshal(map[string]any{
		"id": "private", "source_type": "git", "repo": repo, "ref": "main",
	})
	if err != nil {
		t.Fatalf("marshal register body: %v", err)
	}
	resp, err := http.Post(ts.URL+"/v1/layers", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var registered server.LayerRegisterResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&registered)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || decodeErr != nil || registered.WebhookSecret == "" {
		t.Fatalf("register status=%d decode=%v secret-present=%t, want 201 with a webhook secret",
			resp.StatusCode, decodeErr, registered.WebhookSecret != "")
	}

	resp, err = http.Post(ts.URL+"/v1/layers/reingest?id=private", "application/json", nil)
	if err != nil {
		t.Fatalf("reingest: %v", err)
	}
	assertSourceUnreachable(t, "manual reingest", resp)

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
	resp, err = http.DefaultClient.Do(req)
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

	// The clone reads the stored repo, so the remote still receives the
	// credential after the responses above reported none of it.
	users, passwords := remote.basicAuth()
	if len(users) == 0 {
		t.Fatalf("the remote recorded no basic-auth request; the clone did not send the stored credential")
	}
	for i := range users {
		if users[i] != repoCredentialUser || passwords[i] != repoCredentialPassword {
			t.Errorf("remote request %d carried basic auth %q:%q, want the stored credential", i, users[i], passwords[i])
		}
	}
}
