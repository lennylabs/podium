package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Spec: §7.6.1 / §7.3 — every subcommand that takes a positional accepts its
// flags on either side of that positional. Go's flag package stops parsing at
// the first non-flag argument, so a command that handed the raw argv to
// fs.Parse dropped `podium search hello --top-k 3` down to the default top_k
// and answered with ten results at exit 0. These tests pin the flag-after-
// positional ordering by asserting the value the flag controls reaches the
// registry, and they pin the flags-first ordering and the missing-argument
// usage error alongside it.

// recorder captures the request one CLI invocation sends to a stub registry.
type recorder struct {
	query url.Values
	body  map[string]any
}

// recordingRegistry serves response to every request and records the last
// request's query string and decoded JSON body.
func recordingRegistry(t *testing.T, response string) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.query = r.URL.Query()
		rec.body = nil
		if raw, err := io.ReadAll(r.Body); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &rec.body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(ts.Close)
	return ts, rec
}

// assertUsageExit runs cmd with args and requires exit 2 plus the usage line.
func assertUsageExit(t *testing.T, name string, cmd func([]string) int, args []string, wantUsage string) {
	t.Helper()
	var rc int
	stderr := captureStderr(t, func() { rc = cmd(args) })
	if rc != 2 {
		t.Errorf("%s(%v) rc = %d, want 2", name, args, rc)
	}
	if !strings.Contains(stderr, wantUsage) {
		t.Errorf("%s(%v) stderr = %q, want it to contain %q", name, args, stderr, wantUsage)
	}
}

const emptySearchResponse = `{"query":"hello","total_matched":0,"results":[]}`

func TestSearchCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, emptySearchResponse)

	// The defect: --top-k written after the query was dropped and the
	// request carried the default top_k=10.
	if rc := searchCmd([]string{"--registry", ts.URL, "hello", "--top-k", "3"}); rc != 0 {
		t.Fatalf("searchCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("top_k"); got != "3" {
		t.Errorf("top_k = %q, want 3", got)
	}
	if got := rec.query.Get("query"); got != "hello" {
		t.Errorf("query = %q, want hello", got)
	}

	if rc := searchCmd([]string{"--registry", ts.URL, "--top-k", "3", "hello"}); rc != 0 {
		t.Fatalf("flags-first searchCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("top_k"); got != "3" {
		t.Errorf("flags-first top_k = %q, want 3", got)
	}

	assertUsageExit(t, "searchCmd", searchCmd,
		[]string{"--registry", ts.URL}, "usage: podium search <query> [flags]")
}

func TestDomainSearch_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"domains":[]}`)

	if rc := domainSearch([]string{"--registry", ts.URL, "finance", "--top-k", "3"}); rc != 0 {
		t.Fatalf("domainSearch rc = %d, want 0", rc)
	}
	if got := rec.query.Get("top_k"); got != "3" {
		t.Errorf("top_k = %q, want 3", got)
	}
	if got := rec.query.Get("query"); got != "finance" {
		t.Errorf("query = %q, want finance", got)
	}

	if rc := domainSearch([]string{"--registry", ts.URL, "--top-k", "3", "finance"}); rc != 0 {
		t.Fatalf("flags-first domainSearch rc = %d, want 0", rc)
	}
	if got := rec.query.Get("top_k"); got != "3" {
		t.Errorf("flags-first top_k = %q, want 3", got)
	}

	assertUsageExit(t, "domainSearch", domainSearch,
		[]string{"--registry", ts.URL}, "usage: podium domain search <query> [flags]")
}

func TestDomainShow_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"path":"finance","children":[]}`)

	// --json after the path was dropped, so the command printed the human
	// rendering instead of the structured envelope.
	var rc int
	out := captureStdout(t, func() { rc = domainShow([]string{"--registry", ts.URL, "finance", "--json"}) })
	if rc != 0 {
		t.Fatalf("domainShow rc = %d, want 0", rc)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("--json output = %q, want the raw JSON envelope", out)
	}
	if got := rec.query.Get("path"); got != "finance" {
		t.Errorf("path = %q, want finance", got)
	}

	out = captureStdout(t, func() { rc = domainShow([]string{"--registry", ts.URL, "--json", "finance"}) })
	if rc != 0 {
		t.Fatalf("flags-first domainShow rc = %d, want 0", rc)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("flags-first --json output = %q, want the raw JSON envelope", out)
	}

	// The path is optional here, so an omitted positional is not an error:
	// the request carries no path and the command exits 0.
	out = captureStdout(t, func() { rc = domainShow([]string{"--registry", ts.URL, "--json"}) })
	if rc != 0 {
		t.Fatalf("pathless domainShow rc = %d, want 0", rc)
	}
	if _, ok := rec.query["path"]; ok {
		t.Errorf("pathless call sent path = %q, want it absent", rec.query.Get("path"))
	}
}

const signableArtifact = `{"content_hash":"sha256:abc","signature":"noop:sha256:abc"}`

func TestSignCmd_FlagsAfterPositional(t *testing.T) {
	ts, _ := recordingRegistry(t, signableArtifact)

	// --provider after the artifact was dropped, so an unknown provider
	// signed with the noop default and exited 0.
	var rc int
	stderr := captureStderr(t, func() {
		rc = signCmd([]string{"--registry", ts.URL, "finance/run-close", "--provider", "bogus"})
	})
	if rc != 1 {
		t.Errorf("signCmd rc = %d, want 1", rc)
	}
	if !strings.Contains(stderr, "unknown signature provider: bogus") {
		t.Errorf("stderr = %q, want the unknown-provider error", stderr)
	}

	stderr = captureStderr(t, func() {
		rc = signCmd([]string{"--registry", ts.URL, "--provider", "bogus", "finance/run-close"})
	})
	if rc != 1 || !strings.Contains(stderr, "unknown signature provider: bogus") {
		t.Errorf("flags-first signCmd rc = %d, stderr = %q", rc, stderr)
	}

	// The artifact is optional here; without it and without --content-hash
	// the command reports the argument error.
	assertUsageExit(t, "signCmd", signCmd,
		[]string{"--registry", ts.URL},
		"error: provide <artifact> or --content-hash sha256:<hex>")
}

func TestVerifyCmd_FlagsAfterPositional(t *testing.T) {
	ts, _ := recordingRegistry(t, signableArtifact)

	var rc int
	stderr := captureStderr(t, func() {
		rc = verifyCmd([]string{"--registry", ts.URL, "finance/run-close", "--provider", "bogus"})
	})
	if rc != 1 {
		t.Errorf("verifyCmd rc = %d, want 1", rc)
	}
	if !strings.Contains(stderr, "unknown signature provider: bogus") {
		t.Errorf("stderr = %q, want the unknown-provider error", stderr)
	}

	stderr = captureStderr(t, func() {
		rc = verifyCmd([]string{"--registry", ts.URL, "--provider", "bogus", "finance/run-close"})
	})
	if rc != 1 || !strings.Contains(stderr, "unknown signature provider: bogus") {
		t.Errorf("flags-first verifyCmd rc = %d, stderr = %q", rc, stderr)
	}

	assertUsageExit(t, "verifyCmd", verifyCmd,
		[]string{"--registry", ts.URL},
		"error: provide <artifact>, or --content-hash and --signature")
}

func TestImpactCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"dependents":[]}`)

	var rc int
	_ = captureStdout(t, func() { rc = impactCmd([]string{"finance/run-close", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("impactCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "finance/run-close" {
		t.Errorf("id = %q, want finance/run-close", got)
	}

	rec.query = nil
	_ = captureStdout(t, func() { rc = impactCmd([]string{"--registry", ts.URL, "finance/run-close"}) })
	if rc != 0 {
		t.Fatalf("flags-first impactCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "finance/run-close" {
		t.Errorf("flags-first id = %q, want finance/run-close", got)
	}

	assertUsageExit(t, "impactCmd", impactCmd,
		[]string{"--registry", ts.URL}, "usage: podium impact <artifact-id>")
}

func TestAdminGrantCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"granted":true}`)

	var rc int
	_ = captureStdout(t, func() { rc = adminGrantCmd([]string{"alice@acme.com", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("adminGrantCmd rc = %d, want 0", rc)
	}
	if got, _ := rec.body["user_id"].(string); got != "alice@acme.com" {
		t.Errorf("user_id = %q, want alice@acme.com", got)
	}

	rec.body = nil
	_ = captureStdout(t, func() { rc = adminGrantCmd([]string{"--registry", ts.URL, "alice@acme.com"}) })
	if rc != 0 {
		t.Fatalf("flags-first adminGrantCmd rc = %d, want 0", rc)
	}
	if got, _ := rec.body["user_id"].(string); got != "alice@acme.com" {
		t.Errorf("flags-first user_id = %q, want alice@acme.com", got)
	}

	assertUsageExit(t, "adminGrantCmd", adminGrantCmd,
		[]string{"--registry", ts.URL}, "usage: podium admin grant <user-id>")
	// A second positional is still an argument error.
	assertUsageExit(t, "adminGrantCmd", adminGrantCmd,
		[]string{"alice@acme.com", "bob@acme.com", "--registry", ts.URL},
		"usage: podium admin grant <user-id>")
}

func TestAdminRevokeCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{}`)

	var rc int
	_ = captureStderr(t, func() { rc = adminRevokeCmd([]string{"alice@acme.com", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("adminRevokeCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("user_id"); got != "alice@acme.com" {
		t.Errorf("user_id = %q, want alice@acme.com", got)
	}

	rec.query = nil
	_ = captureStderr(t, func() { rc = adminRevokeCmd([]string{"--registry", ts.URL, "alice@acme.com"}) })
	if rc != 0 {
		t.Fatalf("flags-first adminRevokeCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("user_id"); got != "alice@acme.com" {
		t.Errorf("flags-first user_id = %q, want alice@acme.com", got)
	}

	assertUsageExit(t, "adminRevokeCmd", adminRevokeCmd,
		[]string{"--registry", ts.URL}, "usage: podium admin revoke <user-id>")
}

func TestAdminShowEffectiveCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"layers":[]}`)

	var rc int
	_ = captureStdout(t, func() {
		rc = adminShowEffectiveCmd([]string{"alice@acme.com", "--group", "eng", "--registry", ts.URL})
	})
	if rc != 0 {
		t.Fatalf("adminShowEffectiveCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("user_id"); got != "alice@acme.com" {
		t.Errorf("user_id = %q, want alice@acme.com", got)
	}
	if got := rec.query.Get("group"); got != "eng" {
		t.Errorf("group = %q, want eng", got)
	}

	rec.query = nil
	_ = captureStdout(t, func() {
		rc = adminShowEffectiveCmd([]string{"--registry", ts.URL, "--group", "eng", "alice@acme.com"})
	})
	if rc != 0 {
		t.Fatalf("flags-first adminShowEffectiveCmd rc = %d, want 0", rc)
	}
	if got := rec.query.Get("group"); got != "eng" {
		t.Errorf("flags-first group = %q, want eng", got)
	}

	assertUsageExit(t, "adminShowEffectiveCmd", adminShowEffectiveCmd,
		[]string{"--registry", ts.URL}, "usage: podium admin show-effective <user-id>")
}

func TestAdminEraseCmd_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"erased":true}`)

	var rc int
	_ = captureStdout(t, func() {
		rc = adminEraseCmd([]string{"alice@acme.com", "--salt", "pepper", "--registry", ts.URL})
	})
	if rc != 0 {
		t.Fatalf("adminEraseCmd rc = %d, want 0", rc)
	}
	if got, _ := rec.body["user_id"].(string); got != "alice@acme.com" {
		t.Errorf("user_id = %q, want alice@acme.com", got)
	}
	if got, _ := rec.body["salt"].(string); got != "pepper" {
		t.Errorf("salt = %q, want pepper", got)
	}

	rec.body = nil
	_ = captureStdout(t, func() {
		rc = adminEraseCmd([]string{"--registry", ts.URL, "--salt", "pepper", "alice@acme.com"})
	})
	if rc != 0 {
		t.Fatalf("flags-first adminEraseCmd rc = %d, want 0", rc)
	}
	if got, _ := rec.body["salt"].(string); got != "pepper" {
		t.Errorf("flags-first salt = %q, want pepper", got)
	}

	assertUsageExit(t, "adminEraseCmd", adminEraseCmd,
		[]string{"--salt", "pepper", "--registry", ts.URL}, "usage: podium admin erase <user-id>")
}

func TestLayerReingest_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{}`)

	var rc int
	_ = captureStdout(t, func() {
		rc = layerReingest([]string{
			"team", "--registry", ts.URL,
			"--break-glass", "--justification", "incident-42",
		})
	})
	if rc != 0 {
		t.Fatalf("layerReingest rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "team" {
		t.Errorf("id = %q, want team", got)
	}
	if got, _ := rec.body["break_glass"].(bool); !got {
		t.Errorf("break_glass = %v, want true", rec.body["break_glass"])
	}
	if got, _ := rec.body["justification"].(string); got != "incident-42" {
		t.Errorf("justification = %q, want incident-42", got)
	}

	rec.body = nil
	_ = captureStdout(t, func() {
		rc = layerReingest([]string{
			"--registry", ts.URL, "--break-glass", "--justification", "incident-42", "team",
		})
	})
	if rc != 0 {
		t.Fatalf("flags-first layerReingest rc = %d, want 0", rc)
	}
	if got, _ := rec.body["justification"].(string); got != "incident-42" {
		t.Errorf("flags-first justification = %q, want incident-42", got)
	}

	assertUsageExit(t, "layerReingest", layerReingest,
		[]string{"--registry", ts.URL},
		"usage: podium layer reingest [--break-glass --justification <text>] <id>")
}

// Spec: §4.5 — `podium domain analyze [<path>]` reads the subtree from the
// positional, and the flags parse on either side of it.
func TestDomainAnalyze_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"metrics":{}}`)

	var rc int
	_ = captureStdout(t, func() { rc = domainAnalyze([]string{"finance", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("domainAnalyze rc = %d, want 0", rc)
	}
	if got := rec.query.Get("path"); got != "finance" {
		t.Errorf("path = %q, want finance", got)
	}

	rec.query = nil
	_ = captureStdout(t, func() { rc = domainAnalyze([]string{"--registry", ts.URL, "finance"}) })
	if rc != 0 {
		t.Fatalf("flags-first domainAnalyze rc = %d, want 0", rc)
	}
	if got := rec.query.Get("path"); got != "finance" {
		t.Errorf("flags-first path = %q, want finance", got)
	}
}

// Spec: §7.3 — `podium layer unregister <id>` takes --registry on either side
// of the id. Before the fix the id was the first non-flag argument, so
// --registry after it left three unparsed arguments and the NArg guard
// rejected the invocation with the usage line at exit 2.
func TestLayerUnregister_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"status":"unregistered"}`)

	var rc int
	_ = captureStdout(t, func() { rc = layerUnregister([]string{"team", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("layerUnregister rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "team" {
		t.Errorf("id = %q, want team", got)
	}

	rec.query = nil
	_ = captureStdout(t, func() { rc = layerUnregister([]string{"--registry", ts.URL, "team"}) })
	if rc != 0 {
		t.Fatalf("flags-first layerUnregister rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "team" {
		t.Errorf("flags-first id = %q, want team", got)
	}

	assertUsageExit(t, "layerUnregister", layerUnregister,
		[]string{"--registry", ts.URL}, "usage: podium layer unregister <id>")
}

// Spec: §8.4 — `podium layer restore <id>` has the same argument structure as
// unregister and had the same parsing defect.
func TestLayerRestore_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"status":"restored"}`)

	var rc int
	_ = captureStdout(t, func() { rc = layerRestore([]string{"team", "--registry", ts.URL}) })
	if rc != 0 {
		t.Fatalf("layerRestore rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "team" {
		t.Errorf("id = %q, want team", got)
	}

	rec.query = nil
	_ = captureStdout(t, func() { rc = layerRestore([]string{"--registry", ts.URL, "team"}) })
	if rc != 0 {
		t.Fatalf("flags-first layerRestore rc = %d, want 0", rc)
	}
	if got := rec.query.Get("id"); got != "team" {
		t.Errorf("flags-first id = %q, want team", got)
	}

	assertUsageExit(t, "layerRestore", layerRestore,
		[]string{"--registry", ts.URL}, "usage: podium layer restore <id>")
}

// Spec: §4.3 — `podium artifact scaffold <path>` writes a directory whose
// ARTIFACT.md reflects the flags. This command dropped the flags silently:
// the <path> positional satisfied the NArg guard and every flag after it was
// discarded, so the scaffolder ran with an empty --type and prompted or
// failed instead of writing the requested type. The test therefore asserts
// the generated file, which the exit status alone would not catch.
func TestArtifactScaffold_FlagsAfterPositional(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "personal", "release-orchestrator")
	exit, _, stderr := runScaffold([]string{
		target,
		"--type", "agent",
		"--description", "Coordinate the release across services.",
		"--yes",
	}, "")
	if exit != 0 {
		t.Fatalf("artifactScaffold exit = %d, want 0 (stderr=%s)", exit, stderr)
	}
	body, err := os.ReadFile(filepath.Join(target, "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("read ARTIFACT.md: %v", err)
	}
	if !strings.Contains(string(body), "type: agent") {
		t.Errorf("--type after the path did not reach the manifest: %s", body)
	}
	if !strings.Contains(string(body), "Coordinate the release across services.") {
		t.Errorf("--description after the path did not reach the manifest: %s", body)
	}

	flagsFirst := filepath.Join(root, "personal", "release-coordinator")
	exit, _, stderr = runScaffold([]string{
		"--type", "agent",
		"--description", "Coordinate the release across services.",
		"--yes",
		flagsFirst,
	}, "")
	if exit != 0 {
		t.Fatalf("flags-first artifactScaffold exit = %d, want 0 (stderr=%s)", exit, stderr)
	}
	body, err = os.ReadFile(filepath.Join(flagsFirst, "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("flags-first read ARTIFACT.md: %v", err)
	}
	if !strings.Contains(string(body), "type: agent") {
		t.Errorf("flags-first type missing from the manifest: %s", body)
	}

	exit, _, stderr = runScaffold([]string{"--type", "agent", "--yes"}, "")
	if exit != 2 {
		t.Errorf("missing positional exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "usage: podium artifact scaffold --type <type> [flags] <path>") {
		t.Errorf("missing positional stderr = %q, want the usage line", stderr)
	}
}

// Spec: §7.3.1 — `podium layer reorder <id> [<id> ...]` forwards its operands as
// the layer sequence. Parsing that stopped at the first operand carried the flag
// and its value into that sequence, so the request named "--registry" and a URL
// as two of the layers to order, and it went to the environment's registry
// rather than the one the flag named. The operands are the payload here, so the
// assertion reads the request body rather than an exit status.
func TestLayerReorder_FlagsAfterPositional(t *testing.T) {
	ts, rec := recordingRegistry(t, `{"status":"reordered"}`)

	var rc int
	_ = captureStdout(t, func() {
		rc = layerReorder([]string{"a", "b", "--registry", ts.URL})
	})
	if rc != 0 {
		t.Fatalf("layerReorder rc = %d, want 0", rc)
	}
	if got := orderOf(t, rec); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("order = %v, want [a b]; the flag and its value must not be sent as layers", got)
	}

	rec.body = nil
	_ = captureStdout(t, func() {
		rc = layerReorder([]string{"--registry", ts.URL, "a", "b"})
	})
	if rc != 0 {
		t.Fatalf("flags-first layerReorder rc = %d, want 0", rc)
	}
	if got := orderOf(t, rec); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("flags-first order = %v, want [a b]", got)
	}

	assertUsageExit(t, "layerReorder", layerReorder,
		[]string{"--registry", ts.URL}, "usage: podium layer reorder <id> [<id> ...]")
}

// orderOf reads the reorder request's layer sequence out of the recorded body.
func orderOf(t *testing.T, rec *recorder) []string {
	t.Helper()
	raw, ok := rec.body["order"].([]any)
	if !ok {
		t.Fatalf("request body carried no order array: %v", rec.body)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("order element %v is not a string", v)
		}
		out = append(out, s)
	}
	return out
}
