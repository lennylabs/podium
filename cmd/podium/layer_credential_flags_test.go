package main

import (
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

// credentialFlagsRegistry is an httptest registry that answers every request
// with 200 and records each request body, so a case can assert both the body
// a command sent and that a refused command sent nothing.
type credentialFlagsRegistry struct {
	*httptest.Server

	// mu guards bodies, which the handler appends to from the server's request
	// goroutine while the test goroutine reads it.
	mu     sync.Mutex
	bodies []map[string]any
}

func newCredentialFlagsRegistry(t *testing.T) *credentialFlagsRegistry {
	t.Helper()
	reg := &credentialFlagsRegistry{}
	reg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request body %q: %v", raw, err)
		}
		reg.mu.Lock()
		reg.bodies = append(reg.bodies, body)
		reg.mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"team-private"}`))
	}))
	t.Cleanup(reg.Close)
	t.Setenv("PODIUM_REGISTRY", reg.URL)
	return reg
}

// requests returns a copy of the bodies the registry has received.
func (r *credentialFlagsRegistry) requests() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.bodies...)
}

// onlyBody returns the one body the registry received, failing the test when
// the command sent no request or more than one.
func (r *credentialFlagsRegistry) onlyBody(t *testing.T) map[string]any {
	t.Helper()
	got := r.requests()
	if len(got) != 1 {
		t.Fatalf("registry received %d requests, want 1: %v", len(got), got)
	}
	return got[0]
}

// writeTokenFile writes content to a fresh file and returns its path.
func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

// withLayerStdin points layerStdin at content for the rest of the test.
func withLayerStdin(t *testing.T, content string) {
	t.Helper()
	prev := layerStdin
	layerStdin = strings.NewReader(content)
	t.Cleanup(func() { layerStdin = prev })
}

// runLayerCommand runs cmd in process and returns its exit code and standard
// error. Standard output is captured so a passing case prints nothing.
func runLayerCommand(t *testing.T, cmd func([]string) int, args ...string) (int, string) {
	t.Helper()
	var code int
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() { code = cmd(args) })
	})
	return code, stderr
}

// credentialCommands names the two commands that take the credential flags,
// each with the arguments that make the rest of its invocation valid.
var credentialCommands = []struct {
	name string
	cmd  func([]string) int
	base []string
}{
	{"register", layerRegister, []string{"--id", "team-private", "--repo", "https://git.acme.com/acme/x.git", "--ref", "main"}},
	{"update", layerUpdate, []string{"--id", "team-private"}},
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// the CLI refuses before any request a token file that names no token, a token
// file it cannot read, and a token file combined with --clear-repo-credential,
// and no refusal carries the file's contents.
func TestLayerCredentialFlags_Refusals(t *testing.T) {
	type refusal struct {
		name    string
		args    func(t *testing.T) []string
		wantErr string
		secret  string
	}
	refusals := []refusal{
		{
			name:    "empty token file",
			args:    func(t *testing.T) []string { return []string{"--repo-token-file", writeTokenFile(t, "")} },
			wantErr: "error: --repo-token-file names an empty token\n",
		},
		{
			name:    "token file holding only a newline",
			args:    func(t *testing.T) []string { return []string{"--repo-token-file", writeTokenFile(t, "\n")} },
			wantErr: "error: --repo-token-file names an empty token\n",
		},
		{
			name: "empty standard input",
			args: func(t *testing.T) []string {
				withLayerStdin(t, "")
				return []string{"--repo-token-file", "-"}
			},
			wantErr: "error: --repo-token-file names an empty token\n",
		},
		{
			name: "nonexistent token file",
			args: func(t *testing.T) []string {
				return []string{"--repo-token-file", filepath.Join(t.TempDir(), "missing")}
			},
			wantErr: "error: layer: read --repo-token-file:",
		},
		{
			name: "token file with --clear-repo-credential",
			args: func(t *testing.T) []string {
				return []string{"--repo-token-file", writeTokenFile(t, "s3cr3tpw"), "--clear-repo-credential"}
			},
			wantErr: "error: --repo-token-file cannot be combined with --clear-repo-credential\n",
			secret:  "s3cr3tpw",
		},
		{
			name:    "username without a token file",
			args:    func(*testing.T) []string { return []string{"--repo-username", "alice"} },
			wantErr: "error: --repo-username requires --repo-token-file\n",
		},
	}
	for _, c := range credentialCommands {
		for _, r := range refusals {
			t.Run(c.name+"/"+r.name, func(t *testing.T) {
				reg := newCredentialFlagsRegistry(t)
				args := append(append([]string(nil), c.base...), r.args(t)...)
				code, stderr := runLayerCommand(t, c.cmd, args...)
				if code != 2 {
					t.Errorf("exit = %d, want 2; stderr: %s", code, stderr)
				}
				if !strings.HasPrefix(stderr, r.wantErr) {
					t.Errorf("stderr = %q, want it to begin with %q", stderr, r.wantErr)
				}
				if r.secret != "" && strings.Contains(stderr, r.secret) {
					t.Errorf("stderr carries the token file's contents: %q", stderr)
				}
				if got := reg.requests(); len(got) != 0 {
					t.Errorf("registry received %d requests, want none: %v", len(got), got)
				}
			})
		}
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// the credential members apply to a git source, so register refuses a
// credential flag on a --local registration before any request.
func TestLayerCredentialFlags_RegisterLocalRefused(t *testing.T) {
	reg := newCredentialFlagsRegistry(t)
	code, stderr := runLayerCommand(t, layerRegister, "--id", "other",
		"--local", t.TempDir(), "--repo-token-file", writeTokenFile(t, "s3cr3tpw"))
	if code != 2 {
		t.Errorf("exit = %d, want 2; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "apply to a --repo registration") {
		t.Errorf("stderr = %q, want the --repo registration message", stderr)
	}
	if got := reg.requests(); len(got) != 0 {
		t.Errorf("registry received %d requests, want none: %v", len(got), got)
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// register maps --clear-repo-credential onto clear_repo_credential: true and
// sends no credential member beside it.
func TestLayerCredentialFlags_RegisterClear(t *testing.T) {
	reg := newCredentialFlagsRegistry(t)
	code, stderr := runLayerCommand(t, layerRegister, "--id", "team-private",
		"--repo", "https://git.acme.com/acme/x.git", "--ref", "main", "--clear-repo-credential")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	body := reg.onlyBody(t)
	if body["clear_repo_credential"] != true {
		t.Errorf("clear_repo_credential = %v, want true; body: %v", body["clear_repo_credential"], body)
	}
	for _, k := range []string{"repo_token", "repo_username"} {
		if _, ok := body[k]; ok {
			t.Errorf("body carries %s: %v", k, body)
		}
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// register sends the username as given and the token with one trailing line
// ending removed, and an empty --repo-username beside a token file is sent as
// the empty string.
func TestLayerCredentialFlags_RegisterCredential(t *testing.T) {
	cases := []struct {
		name     string
		username string
	}{
		{"named username", "alice-user"},
		{"empty username", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newCredentialFlagsRegistry(t)
			code, stderr := runLayerCommand(t, layerRegister, "--id", "team-private",
				"--repo", "https://git.acme.com/acme/x.git", "--ref", "main",
				"--repo-username", tc.username, "--repo-token-file", writeTokenFile(t, "s3cr3tpw\r\n"))
			if code != 0 {
				t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
			}
			body := reg.onlyBody(t)
			if got, ok := body["repo_username"]; !ok || got != tc.username {
				t.Errorf("repo_username = %v (present %v), want %q; body: %v", got, ok, tc.username, body)
			}
			if body["repo_token"] != "s3cr3tpw" {
				t.Errorf("repo_token = %q, want %q", body["repo_token"], "s3cr3tpw")
			}
			if _, ok := body["clear_repo_credential"]; ok {
				t.Errorf("body carries clear_repo_credential: %v", body)
			}
		})
	}
}

// Spec: §7.3.1 (Setting, replacing, and removing a repository credential) —
// an empty --repo-username without a token file is neither refused as a
// username without a token nor sent alone. On update it leaves the body empty,
// so the command refuses for want of a mutable field; on register it sends no
// credential member.
func TestLayerCredentialFlags_EmptyUsernameWithoutToken(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		reg := newCredentialFlagsRegistry(t)
		code, stderr := runLayerCommand(t, layerUpdate, "--id", "team-private", "--repo-username", "")
		if code != 2 {
			t.Errorf("exit = %d, want 2; stderr: %s", code, stderr)
		}
		if want := "error: at least one mutable field must be provided\n"; stderr != want {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
		if got := reg.requests(); len(got) != 0 {
			t.Errorf("registry received %d requests, want none: %v", len(got), got)
		}
	})
	t.Run("register", func(t *testing.T) {
		reg := newCredentialFlagsRegistry(t)
		code, stderr := runLayerCommand(t, layerRegister, "--id", "team-private",
			"--repo", "https://git.acme.com/acme/x.git", "--ref", "main", "--repo-username", "")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
		}
		body := reg.onlyBody(t)
		for _, k := range []string{"repo_username", "repo_token"} {
			if _, ok := body[k]; ok {
				t.Errorf("body carries %s: %v", k, body)
			}
		}
	})
}
