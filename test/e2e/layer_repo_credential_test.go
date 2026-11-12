package e2e

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// basicAuthPair is the credential one request to the git remote carried.
type basicAuthPair struct {
	user, password string
}

// remoteRequest is one request the git remote received: whether it carried an
// Authorization header, and the basic-auth pair that header decoded to.
type remoteRequest struct {
	authorization bool
	basicOK       bool
	pair          basicAuthPair
}

// failingGitRemote is an HTTP remote that answers every request with 500 and
// records each request with the basic-auth pair it carried. The pair comes
// from the Authorization header: a request line never carries URL userinfo,
// so the request URL says nothing about the credential a clone sent.
type failingGitRemote struct {
	*httptest.Server

	// mu guards reqs, which the handler appends to from the server's request
	// goroutines.
	mu   sync.Mutex
	reqs []remoteRequest
}

func newFailingGitRemote(t *testing.T) *failingGitRemote {
	t.Helper()
	remote := &failingGitRemote{}
	remote.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		remote.mu.Lock()
		remote.reqs = append(remote.reqs, remoteRequest{
			authorization: r.Header.Get("Authorization") != "",
			basicOK:       ok,
			pair:          basicAuthPair{user, password},
		})
		remote.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(remote.Close)
	return remote
}

// requests returns a copy of every request the remote has received.
func (f *failingGitRemote) requests() []remoteRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]remoteRequest(nil), f.reqs...)
}

// recorded returns the basic-auth pairs of the requests that carried one.
func (f *failingGitRemote) recorded() []basicAuthPair {
	var out []basicAuthPair
	for _, r := range f.requests() {
		if r.basicOK {
			out = append(out, r.pair)
		}
	}
	return out
}

// reportedLayerRepo reads GET /v1/layers and returns the repo the response
// carries for id.
func reportedLayerRepo(t *testing.T, baseURL, id string) string {
	t.Helper()
	resp, err := http.Get(baseURL + "/v1/layers")
	if err != nil {
		t.Fatalf("GET /v1/layers: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /v1/layers: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/layers = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Layers []struct {
			ID   string `json:"id"`
			Repo string `json:"repo"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode /v1/layers: %v: %s", err, body)
	}
	for _, l := range out.Layers {
		if l.ID == id {
			return l.Repo
		}
	}
	t.Fatalf("GET /v1/layers does not carry layer %s: %s", id, body)
	return ""
}

// rawLayerRepoRow is the repo and repo_userinfo columns of one layer_configs
// row, read with no store read rule in between.
type rawLayerRepoRow struct {
	Repo     string
	Userinfo sql.NullString
}

// readRawLayerRepoRow reads the repo columns of layer id from the SQLite file
// at path. A standalone registry holds one tenant, so the ID names one row.
func readRawLayerRepoRow(t *testing.T, path, id string) rawLayerRepoRow {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	var row rawLayerRepoRow
	if err := db.QueryRow(`SELECT repo, repo_userinfo FROM layer_configs WHERE id = ?`, id).
		Scan(&row.Repo, &row.Userinfo); err != nil {
		t.Fatalf("select repo, repo_userinfo for %s: %v", id, err)
	}
	return row
}

// writeRawLayerRepoRow sets the repo columns of layer id in the SQLite file at
// path to row, with no store write rule in between. It runs while no server
// process holds the file.
func writeRawLayerRepoRow(t *testing.T, path, id string, row rawLayerRepoRow) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	res, err := db.Exec(`UPDATE layer_configs SET repo = ?, repo_userinfo = ? WHERE id = ?`, row.Repo, row.Userinfo, id)
	if err != nil {
		t.Fatalf("update repo, repo_userinfo for %s: %v", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("update repo, repo_userinfo for %s affected %d row(s) (err %v), want 1", id, n, err)
	}
}

// credentialCLI runs the podium CLI against one registry and keeps every
// output, so a test can assert at its end that none carries a fixture.
type credentialCLI struct {
	t       *testing.T
	baseURL string
	outputs []string
}

// layer runs `podium layer <sub> --registry <base> <args...>`, feeding stdin
// to the process when it is non-empty.
func (c *credentialCLI) layer(stdin, sub string, args ...string) cliResult {
	c.t.Helper()
	full := append([]string{"layer", sub, "--registry", c.baseURL}, args...)
	var res cliResult
	if stdin == "" {
		res = runPodium(c.t, "", nil, full...)
	} else {
		res = runPodiumStdin(c.t, "", nil, stdin, full...)
	}
	c.outputs = append(c.outputs, res.Stdout+res.Stderr)
	return res
}

// reingest runs `podium layer reingest` for id against a remote that answers
// 500 and returns the requests the remote received during the command. It
// fails the test when the remote received none, because an absence check over
// no requests would pass without a clone.
func (c *credentialCLI) reingest(remote *failingGitRemote, id, what string) []remoteRequest {
	c.t.Helper()
	before := len(remote.requests())
	res := c.layer("", "reingest", id)
	if res.Exit == 0 {
		c.t.Fatalf("%s: reingest against a remote that answers 500 exited 0:\nstdout: %s", what, res.Stdout)
	}
	sent := remote.requests()[before:]
	if len(sent) == 0 {
		c.t.Fatalf("%s: the remote received no request during the reingest\nreingest stderr: %s", what, res.Stderr)
	}
	return sent
}

// wantSent fails the test unless every request carries want as its
// basic-auth pair.
func wantSent(t *testing.T, sent []remoteRequest, want basicAuthPair, what string) {
	t.Helper()
	for i, r := range sent {
		if !r.basicOK || r.pair != want {
			t.Errorf("%s: remote request %d carried basic auth %v %q:%q, want %q:%q",
				what, i, r.basicOK, r.pair.user, r.pair.password, want.user, want.password)
		}
	}
}

// wantNoAuthorization fails the test when a request carries an Authorization
// header.
func wantNoAuthorization(t *testing.T, sent []remoteRequest, what string) {
	t.Helper()
	for i, r := range sent {
		if r.authorization {
			t.Errorf("%s: remote request %d carried an Authorization header", what, i)
		}
	}
}

// listLayer runs `podium layer list`, decodes its output as JSON, and returns
// the layer object of id.
func (c *credentialCLI) listLayer(id string) listedLayer {
	c.t.Helper()
	res := c.layer("", "list")
	cliWantExit(c.t, res, 0, "podium layer list")
	var out struct {
		Layers []listedLayer `json:"layers"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		c.t.Fatalf("decode podium layer list output: %v\n%s", err, res.Stdout)
	}
	for _, l := range out.Layers {
		if l.ID == id {
			return l
		}
	}
	c.t.Fatalf("podium layer list does not carry layer %s:\n%s", id, res.Stdout)
	return listedLayer{}
}

// wantRepoCredential fails the test unless the listed layer carries
// repo_credential with the value want.
func wantRepoCredential(t *testing.T, l listedLayer, want bool, what string) {
	t.Helper()
	if l.RepoCredential == nil || *l.RepoCredential != want {
		t.Errorf("%s: repo_credential = %v, want %v", what, l.RepoCredential, want)
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) /
// §7.3.1 (Re-registration with a reported repo) — through the binary, the CLI
// credential flags set, rotate, keep, and remove a git layer's credential, and
// the clone sends exactly the stored pair. The token is read from a file or
// standard input with one line ending removed, an omitted --repo-username
// keeps the stored username, a re-registration with the reported repo keeps
// the credential, and --clear-repo-credential removes it on update and on
// register. URL userinfo is refused with url_credential, the CLI refuses a
// username without a token and a credential flag on a --local registration
// before any request, and no output or server log line carries a fixture.
func TestLayerRepoCredential_Flags_CLI(t *testing.T) {
	t.Parallel()
	const (
		user1 = "x-access-token"
		user2 = "alice@acme.com"
		tok4  = "p@ss/w:rd?#% x"
	)
	remote := newFailingGitRemote(t)
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_SIGN=none"}, "serve", "--standalone")
	cli := &credentialCLI{t: t, baseURL: srv.BaseURL}
	registered := remote.URL + "/acme/private.git"

	// Step 1: register with a username and a token file.
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("tok1\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	cliWantExit(t, cli.layer("", "register", "--id", "private", "--repo", registered, "--ref", "main",
		"--repo-username", user1, "--repo-token-file", tokenFile), 0, "step 1: register with the credential flags")
	wantSent(t, cli.reingest(remote, "private", "step 1"), basicAuthPair{user1, "tok1"}, "step 1")

	// Step 2: rotate through standard input; one "\r\n" is removed.
	cliWantExit(t, cli.layer("tok2\r\n", "update", "--id", "private", "--repo-username", user1,
		"--repo-token-file", "-"), 0, "step 2: rotate the token from standard input")
	wantSent(t, cli.reingest(remote, "private", "step 2"), basicAuthPair{user1, "tok2"}, "step 2")
	listed := cli.listLayer("private")
	wantRepoCredential(t, listed, true, "step 2")
	if listed.Repo != registered {
		t.Errorf("step 2: reported repo = %q, want %q", listed.Repo, registered)
	}

	// Step 3: an empty username is sent as the empty string.
	cliWantExit(t, cli.layer("tok3", "update", "--id", "private", "--repo-username", "",
		"--repo-token-file", "-"), 0, "step 3: set an empty username")
	wantSent(t, cli.reingest(remote, "private", "step 3"), basicAuthPair{"", "tok3"}, "step 3")

	// Step 4: a username and a token with reserved characters reach the remote
	// as given, through go-git's re-encoding of the userinfo.
	cliWantExit(t, cli.layer(tok4, "update", "--id", "private", "--repo-username", user2,
		"--repo-token-file", "-"), 0, "step 4: set a credential with reserved characters")
	wantSent(t, cli.reingest(remote, "private", "step 4"), basicAuthPair{user2, tok4}, "step 4")

	// Step 5: an update without --repo-username keeps the stored username.
	cliWantExit(t, cli.layer("tok5", "update", "--id", "private", "--repo-token-file", "-"), 0,
		"step 5: rotate the token alone")
	wantSent(t, cli.reingest(remote, "private", "step 5"), basicAuthPair{user2, "tok5"}, "step 5")

	// Step 6: a re-registration with the reported repo keeps the credential.
	reported := cli.listLayer("private").Repo
	cliWantExit(t, cli.layer("", "register", "--id", "private", "--repo", reported, "--ref", "main"), 0,
		"step 6: re-register with the reported repo")
	wantSent(t, cli.reingest(remote, "private", "step 6"), basicAuthPair{user2, "tok5"}, "step 6")

	// Step 7: --clear-repo-credential on update removes the credential.
	cliWantExit(t, cli.layer("", "update", "--id", "private", "--clear-repo-credential"), 0,
		"step 7: clear the credential on update")
	wantNoAuthorization(t, cli.reingest(remote, "private", "step 7"), "step 7")
	wantRepoCredential(t, cli.listLayer("private"), false, "step 7")

	// Step 8: a token alone after the clear carries no stored username, and
	// --clear-repo-credential on register removes the credential again.
	cliWantExit(t, cli.layer("tok6", "update", "--id", "private", "--repo-token-file", "-"), 0,
		"step 8: set a token on the cleared layer")
	wantSent(t, cli.reingest(remote, "private", "step 8"), basicAuthPair{"", "tok6"}, "step 8")
	reported = cli.listLayer("private").Repo
	cliWantExit(t, cli.layer("", "register", "--id", "private", "--repo", reported, "--ref", "main",
		"--clear-repo-credential"), 0, "step 8: clear the credential on register")
	wantNoAuthorization(t, cli.reingest(remote, "private", "step 8 register clear"), "step 8 register clear")

	// Step 9: URL userinfo on an http repo is refused with url_credential.
	host := strings.TrimPrefix(remote.URL, "http://")
	refused := cli.layer("", "register", "--id", "leaked", "--repo", "http://s3cr3tuser:s3cr3tpw@"+host+"/x",
		"--ref", "main")
	cliWantExit(t, refused, 1, "step 9: register a repo with URL userinfo")
	for _, want := range []string{"HTTP 400", "url_credential"} {
		cliContains(t, refused.Stderr, want, "step 9 refusal")
	}

	// Step 10: a username without a token file is refused before any request.
	before := len(remote.requests())
	noToken := cli.layer("", "update", "--id", "private", "--repo-username", "alice")
	cliWantExit(t, noToken, 2, "step 10: a username without a token file")
	cliContains(t, noToken.Stderr, "error: --repo-username requires --repo-token-file", "step 10 refusal")
	if after := len(remote.requests()); after != before {
		t.Errorf("step 10: the remote received %d requests during a refused update", after-before)
	}

	// Step 11: a credential flag applies to a --repo registration only.
	local := cli.layer("", "register", "--id", "other", "--local", t.TempDir(), "--repo-token-file", tokenFile)
	cliWantExit(t, local, 2, "step 11: a token file on a --local registration")
	cliContains(t, local.Stderr, "--repo registration", "step 11 refusal")

	// Step 12: no output and no server log line carries a fixture.
	fixtures := []string{user1, user2, "tok1", "tok2", "tok3", tok4, "tok5", "tok6", "s3cr3tuser", "s3cr3tpw"}
	texts := append(append([]string(nil), cli.outputs...), srv.log())
	for i, text := range texts {
		for _, fixture := range fixtures {
			if strings.Contains(text, fixture) {
				t.Errorf("output %d carries the credential fixture %q:\n%s", i, fixture, text)
			}
		}
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) /
// §7.3.1 (Re-registration with a reported repo) — a git layer registered
// through --repo-username and --repo-token-file keeps its credential across a
// restart of the binary on the same SQLite file. The row the first process
// wrote holds the userinfo apart from the rest of the URL. After the restart
// the list reports the repo without userinfo, a registration with the reported
// value keeps the credential, a reingest sends the registered credential to
// the remote, and the log of neither process names a credential part.
func TestLayerRepoCredential_SurvivesRestart_CLI(t *testing.T) {
	t.Parallel()
	const (
		user     = "alice-user"
		password = "s3cr3tpw"
	)
	remote := newFailingGitRemote(t)
	reportedWant := remote.URL + "/acme/private.git"

	home := t.TempDir()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	sqlitePath := filepath.Join(home, "podium.db")
	env := []string{
		"HOME=" + home,
		"PODIUM_SQLITE_PATH=" + sqlitePath,
		"PODIUM_SIGN=none",
	}
	srv := startServerArgs(t, env, "serve", "--standalone")
	registered := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", "private", "--repo", reportedWant, "--ref", "main",
		"--repo-username", user, "--repo-token-file", tokenFile)
	cliWantExit(t, registered, 0, "register a layer through the credential flags")
	stopProc(srv.cmd)

	// The stopped process wrote the userinfo apart from the rest of the URL.
	wantRow := rawLayerRepoRow{Repo: reportedWant, Userinfo: sql.NullString{String: user + ":" + password, Valid: true}}
	if got := readRawLayerRepoRow(t, sqlitePath, "private"); got != wantRow {
		t.Errorf("stored row of private = %+v, want %+v", got, wantRow)
	}

	// The second process reads the layer from the file the first one wrote.
	srv2 := startServerArgs(t, env, "serve", "--standalone")
	reported := reportedLayerRepo(t, srv2.BaseURL, "private")
	if reported != reportedWant {
		t.Fatalf("reported repo after the restart = %q, want %q", reported, reportedWant)
	}

	// The re-registration sends no credential member, so the keep rule holds
	// the stored pair and the reingest below still sends it.
	reregistered := runPodium(t, "", nil, "layer", "register", "--registry", srv2.BaseURL,
		"--id", "private", "--repo", reported, "--ref", "main")
	cliWantExit(t, reregistered, 0, "re-registration with the reported repo after the restart")

	// Only requests the second process makes count, so the pairs recorded
	// before this point are skipped.
	before := len(remote.recorded())
	reingest := runPodium(t, "", nil, "layer", "reingest", "--registry", srv2.BaseURL, "private")
	if reingest.Exit == 0 {
		t.Fatalf("reingest against a remote that answers 500 exited 0:\nstdout: %s", reingest.Stdout)
	}
	sent := remote.recorded()[before:]
	if len(sent) == 0 {
		t.Fatalf("the remote recorded no basic-auth request after the restart; the clone sent no credential\nreingest stderr: %s\nserver log:\n%s",
			reingest.Stderr, srv2.log())
	}
	for i, pair := range sent {
		if pair.user != user || pair.password != password {
			t.Errorf("remote request %d carried basic auth %q:%q, want the registered credential", i, pair.user, pair.password)
		}
	}

	outputs := map[string]string{
		"log of the first process":  srv.log(),
		"log of the second process": srv2.log(),
		"register output":           registered.Stdout + registered.Stderr,
		"re-registration output":    reregistered.Stdout + reregistered.Stderr,
		"reingest output":           reingest.Stdout + reingest.Stderr,
	}
	for what, text := range outputs {
		for _, part := range []string{user, password} {
			if strings.Contains(text, part) {
				t.Errorf("%s carries the credential part %q:\n%s", what, part, text)
			}
		}
	}
}

// Spec: §13.4 — a row an earlier release wrote, with the credential whole in
// repo, takes the two-column form at the next start of the binary, before the
// registry serves, and the start logs the count alone.
// Spec: §7.3.1 (Repository credentials) — after the rewrite the list reports
// the repo without userinfo and a reingest sends the registered credential.
func TestLayerRepoCredential_EarlierFormRowRewrittenAtStart(t *testing.T) {
	t.Parallel()
	const (
		user     = "alice-user"
		password = "s3cr3tpw"
	)
	remote := newFailingGitRemote(t)
	bare := remote.URL + "/acme/private.git"
	whole := strings.Replace(bare, "://", "://"+user+":"+password+"@", 1)

	home := t.TempDir()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	sqlitePath := filepath.Join(home, "podium.db")
	env := []string{
		"HOME=" + home,
		"PODIUM_SQLITE_PATH=" + sqlitePath,
		"PODIUM_SIGN=none",
	}
	srv := startServerArgs(t, env, "serve", "--standalone")
	registered := runPodium(t, "", nil, "layer", "register", "--registry", srv.BaseURL,
		"--id", "private", "--repo", bare, "--ref", "main",
		"--repo-username", user, "--repo-token-file", tokenFile)
	cliWantExit(t, registered, 0, "register a layer through the credential flags")
	stopProc(srv.cmd)

	// The row takes the form an earlier release writes.
	writeRawLayerRepoRow(t, sqlitePath, "private", rawLayerRepoRow{Repo: whole})

	srv2 := startServerArgs(t, env, "serve", "--standalone")
	cli := &credentialCLI{t: t, baseURL: srv2.BaseURL}
	listed := cli.listLayer("private")
	sent := cli.reingest(remote, "private", "reingest after the rewrite")
	stopProc(srv2.cmd)

	wantSent(t, sent, basicAuthPair{user, password}, "reingest after the rewrite")
	wantRow := rawLayerRepoRow{Repo: bare, Userinfo: sql.NullString{String: user + ":" + password, Valid: true}}
	if got := readRawLayerRepoRow(t, sqlitePath, "private"); listed.Repo != bare || got != wantRow {
		t.Errorf("after the restart: listed repo %q, want %q; stored row %+v, want %+v", listed.Repo, bare, got, wantRow)
	}
	log2 := srv2.log()
	if !strings.Contains(log2, "layer repo migration: rewrote 1 stored layer(s)") {
		t.Errorf("the second process's log does not report the rewrite:\n%s", log2)
	}
	outputs := append([]string{srv.log(), log2, registered.Stdout + registered.Stderr}, cli.outputs...)
	for _, text := range outputs {
		for _, part := range []string{user, password} {
			if strings.Contains(text, part) {
				t.Errorf("an output carries the credential part %q:\n%s", part, text)
			}
		}
	}
}
