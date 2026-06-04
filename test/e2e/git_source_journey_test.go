package e2e

// End-to-end git-source reingest journeys (gaps G-JOURNEY-1, G-JOURNEY-2).
//
// These tests drive the full author-to-consumer path for a git-source layer
// against a real standalone `podium serve` process: an author merges a commit
// to a tracked ref, the registry ingests it (webhook-driven or poll-driven),
// and a consumer searches and loads the result. A second commit bumps the
// artifact version, and the test asserts the new version becomes searchable and
// loadable while the prior version stays addressable.
//
// The source repository is a file:// git repo the test seeds and commits into,
// so the journey runs with no external network. The standalone server resolves
// the built-in git source provider (internal/serverboot/reingest.go), clones
// the file:// repo through go-git, and runs the §7.3.1 ingest pipeline, so a
// version bump records a new immutable manifest version under one canonical id.
//
// Spec: §7.3.1 Ingestion triggers — "Git provider webhook" validates the
// signature, fetches the new commit, and ingests; "Polling watcher" has
// `podium layer watch <id>` poll a git ref without a configured webhook at a
// configurable interval. §4.7.6 (immutable versions; latest resolves to the
// most recent non-deprecated version). §14.10 (register a public git repo as a
// layer and pull it). Gaps G-JOURNEY-1 and G-JOURNEY-2.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	layerwebhook "github.com/lennylabs/podium/pkg/layer/webhook"
)

// gitJourneyRepo is a seeded file:// git repository under test. commit writes a
// single artifact at a path and commits it on the master branch, so successive
// commits at a bumped version stage multiple versions of one canonical id.
type gitJourneyRepo struct {
	dir  string
	repo *git.Repository
	wt   *git.Worktree
}

// newGitJourneyRepo initializes an empty file:// git repository in a temp dir.
func newGitJourneyRepo(t testing.TB) *gitJourneyRepo {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("git Worktree: %v", err)
	}
	return &gitJourneyRepo{dir: dir, repo: repo, wt: wt}
}

// url returns the file:// clone URL the git source provider fetches.
func (g *gitJourneyRepo) url() string { return "file://" + g.dir }

// commitContextArtifact writes a type:context ARTIFACT.md at relDir/ARTIFACT.md
// carrying the given version and description, stages it, and commits it on the
// current branch (master). Each call fully replaces the prior bytes at that
// path, so a bumped version commits as a new manifest version on reingest.
func (g *gitJourneyRepo) commitContextArtifact(t testing.TB, relDir, version, desc, message string) {
	t.Helper()
	artDir := filepath.Join(g.dir, filepath.FromSlash(relDir))
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", artDir, err)
	}
	body := "---\ntype: context\nversion: " + version +
		"\ndescription: " + desc + "\nsensitivity: low\n---\n\n" + desc + " body (v" + version + ").\n"
	if err := os.WriteFile(filepath.Join(artDir, "ARTIFACT.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write ARTIFACT.md: %v", err)
	}
	if _, err := g.wt.Add(relDir + "/ARTIFACT.md"); err != nil {
		t.Fatalf("git add %s: %v", relDir, err)
	}
	if _, err := g.wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("git commit %q: %v", message, err)
	}
}

// gitRegisterResponse captures the §14.10 registration fields the journey uses.
type gitRegisterResponse struct {
	WebhookURL    string `json:"webhook_url"`
	WebhookSecret string `json:"webhook_secret"`
}

// registerGitLayer POSTs /v1/layers to register a git-source layer over repoURL
// at ref master and returns the advertised webhook URL and HMAC secret.
func registerGitLayer(t testing.TB, srv *serverProc, id, repoURL string) gitRegisterResponse {
	t.Helper()
	st, body := apiDo(t, "POST", srv.BaseURL+"/v1/layers", map[string]any{
		"id": id, "source_type": "git", "repo": repoURL, "ref": "master",
	})
	apiWantStatus(t, st, 201, "register git layer "+id, body)
	var reg gitRegisterResponse
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatalf("decode register response: %v\n%s", err, body)
	}
	if reg.WebhookSecret == "" {
		t.Fatalf("register %q returned no webhook secret: %s", id, body)
	}
	return reg
}

// deliverWebhook POSTs a valid-HMAC GitHub-style push delivery to the layer's
// advertised webhook URL and returns the ingest summary, asserting the delivery
// reached the pipeline (HTTP 200) rather than failing verification (401) or
// routing (404). A file:// repo is reachable, so a valid signature ingests.
func deliverWebhook(t testing.TB, reg gitRegisterResponse) map[string]any {
	t.Helper()
	payload := []byte(`{"ref":"refs/heads/master"}`)
	sig, err := layerwebhook.Sign("github", payload, reg.WebhookSecret)
	if err != nil {
		t.Fatalf("sign webhook: %v", err)
	}
	req, _ := http.NewRequest("POST", reg.WebhookURL, bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", sig)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook delivery: HTTP %d, want 200 (verification + ingest)\nbody: %s",
			resp.StatusCode, out.Bytes())
	}
	return apiJSONObj(t, out.Bytes())
}

// searchVersion GETs /v1/search_artifacts?query= and returns the resolved
// version of the first result whose id matches, asserting the artifact is
// searchable. Search collapses an id to its latest non-deprecated version, so
// the returned version is the latest one ingested.
func searchVersion(t testing.TB, srv *serverProc, query, id string) string {
	t.Helper()
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query="+query)
	if st != http.StatusOK {
		t.Fatalf("search_artifacts %q: HTTP %d\nbody: %s", query, st, body)
	}
	var resp struct {
		Results []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode search response: %v\nbody: %s", err, body)
	}
	for _, r := range resp.Results {
		if r.ID == id {
			return r.Version
		}
	}
	t.Fatalf("search_artifacts %q did not return id %q\nbody: %s", query, id, body)
	return ""
}

// assertIngestAccepted asserts the ingest summary accepted exactly one artifact
// at the expected (id, version).
func assertIngestAccepted(t testing.TB, summary map[string]any, id, version string) {
	t.Helper()
	if got := summary["accepted"]; got != float64(1) {
		t.Errorf("accepted = %v, want 1\nsummary: %v", got, summary)
	}
	arts, _ := summary["artifacts"].([]any)
	for _, a := range arts {
		m, _ := a.(map[string]any)
		if m["id"] == id && m["version"] == version {
			return
		}
	}
	t.Errorf("ingest summary did not report %s@%s\nartifacts: %v", id, version, summary["artifacts"])
}

// TestGitJourney_WebhookReingestNewVersionSearchable runs the G-JOURNEY-1 path:
// register a git layer over a seeded file:// repo, deliver a valid-HMAC webhook
// for the initial commit, assert the artifact ingests and is searchable and
// loadable, push a new-version commit, deliver the webhook again, and assert
// search_artifacts and load_artifact return the new version while the prior
// version stays addressable by explicit version.
func TestGitJourney_WebhookReingestNewVersionSearchable(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	repo := newGitJourneyRepo(t)

	const id = "vendor-skills/lint-pr"
	// Initial commit at v1.0.0.
	repo.commitContextArtifact(t, id, "1.0.0",
		"Lint a pull request for vendor coding standards before review.", "initial")
	reg := registerGitLayer(t, srv, "vendor-skills", repo.url())

	// Webhook delivery 1 ingests the initial commit.
	summary1 := deliverWebhook(t, reg)
	assertIngestAccepted(t, summary1, id, "1.0.0")

	// The artifact is searchable at v1.0.0.
	if v := searchVersion(t, srv, "vendor", id); v != "1.0.0" {
		t.Errorf("after first delivery, search version = %q, want 1.0.0", v)
	}
	// And loadable at v1.0.0 (default resolves the only version).
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "1.0.0" {
		t.Errorf("first load: HTTP %d version=%q, want 200/1.0.0", st, got.Version)
	}

	// The author merges a second commit bumping the version to 2.0.0.
	repo.commitContextArtifact(t, id, "2.0.0",
		"Lint a pull request for vendor coding standards before review.", "bump to 2.0.0")

	// Webhook delivery 2 ingests the new commit as a new version.
	summary2 := deliverWebhook(t, reg)
	assertIngestAccepted(t, summary2, id, "2.0.0")

	// search_artifacts now resolves the new latest version 2.0.0.
	if v := searchVersion(t, srv, "vendor", id); v != "2.0.0" {
		t.Errorf("after second delivery, search version = %q, want 2.0.0 (new version searchable)", v)
	}
	// load_artifact default resolves the new latest 2.0.0.
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("latest load: HTTP %d version=%q, want 200/2.0.0", st, got.Version)
	}
	// The prior version stays addressable by explicit version (§4.7.6 immutability).
	if st, got := loadArtifact(t, srv, id, "1.0.0"); st != 200 || got.Version != "1.0.0" {
		t.Errorf("explicit prior load: HTTP %d version=%q, want 200/1.0.0", st, got.Version)
	}
	if st, got := loadArtifact(t, srv, id, "2.0.0"); st != 200 || got.Version != "2.0.0" {
		t.Errorf("explicit new load: HTTP %d version=%q, want 200/2.0.0", st, got.Version)
	}
}

// TestGitJourney_PollReingestDetectsNewCommit runs the G-JOURNEY-2 path:
// register a git layer with no webhook over a seeded file:// repo, run a poll
// cycle to ingest the initial commit, commit a new version, run the poll cycle
// again, and assert the registry detects the changed ref, load_artifact returns
// the updated artifact, and the audit log records two poll-driven reingests
// whose layer.ingested references are the two distinct commit SHAs.
//
// A poll cycle is one `/v1/layers/reingest` poke, which is exactly one
// iteration of the `podium layer watch <id>` loop (cmd/podium/layer.go). The
// layer is registered but no webhook is ever delivered, so the only ingest
// trigger is the poll.
func TestGitJourney_PollReingestDetectsNewCommit(t *testing.T) {
	t.Parallel()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + auditPath},
		"serve", "--standalone")
	repo := newGitJourneyRepo(t)

	const id = "team-runbooks/rotate-creds"
	repo.commitContextArtifact(t, id, "1.0.0",
		"Rotate service credentials following the team runbook procedure.", "initial")
	// Register with no webhook delivery; poll is the only trigger.
	registerGitLayer(t, srv, "team-runbooks", repo.url())

	// Poll cycle 1 detects the initial commit and ingests it.
	pollReingest(t, srv, "team-runbooks", id, "1.0.0")
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "1.0.0" {
		t.Errorf("after poll 1: load HTTP %d version=%q, want 200/1.0.0", st, got.Version)
	}

	// The author commits a new version; no webhook fires.
	repo.commitContextArtifact(t, id, "2.0.0",
		"Rotate service credentials following the team runbook procedure.", "bump to 2.0.0")

	// Poll cycle 2 detects the changed ref and reingests the new version.
	pollReingest(t, srv, "team-runbooks", id, "2.0.0")
	if st, got := loadArtifact(t, srv, id, ""); st != 200 || got.Version != "2.0.0" {
		t.Errorf("after poll 2: load HTTP %d version=%q, want 200/2.0.0 (poll detected new commit)", st, got.Version)
	}

	// The audit log records the poll-driven reingests: two layer.ingested
	// events for the layer, each carrying the commit SHA it ingested, and the
	// two references differ because the second poll detected the new commit.
	refs := auditIngestReferences(t, auditPath, "team-runbooks")
	if len(refs) < 2 {
		t.Fatalf("audit log recorded %d layer.ingested references for the layer, want >=2\nrefs: %v", len(refs), refs)
	}
	first, last := refs[0], refs[len(refs)-1]
	if first == "" || last == "" {
		t.Errorf("layer.ingested events carried empty references: %v", refs)
	}
	if first == last {
		t.Errorf("the two poll cycles recorded the same commit reference %q; the poll did not detect the new commit", first)
	}
	// Both poll cycles published the artifact at the two versions.
	pubs := auditPublishedVersions(t, auditPath, id)
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !pubs[want] {
			t.Errorf("audit log missing artifact.published for %s@%s\nversions: %v", id, want, pubs)
		}
	}
}

// pollReingest issues one poll cycle (a single /v1/layers/reingest poke) and
// asserts it accepted the expected (id, version).
func pollReingest(t testing.TB, srv *serverProc, layerID, id, version string) {
	t.Helper()
	st, body := apiDo(t, "POST", srv.BaseURL+"/v1/layers/reingest?id="+layerID, nil)
	apiWantStatus(t, st, 200, "poll reingest "+layerID, body)
	assertIngestAccepted(t, apiJSONObj(t, body), id, version)
}

// loadArtifact GETs /v1/load_artifact for id (empty version resolves latest)
// and returns the HTTP status and decoded envelope.
func loadArtifact(t testing.TB, srv *serverProc, id, version string) (int, exLoadResp) {
	t.Helper()
	url := srv.BaseURL + "/v1/load_artifact?id=" + id
	if version != "" {
		url += "&version=" + version
	}
	st, body := getRaw(t, url)
	var r exLoadResp
	if st == 200 {
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("decode load_artifact %s@%s: %v\nbody: %s", id, version, err, body)
		}
	}
	return st, r
}

// auditEvent is the subset of an audit log line the journey assertions read.
type auditEvent struct {
	Type    string            `json:"type"`
	Target  string            `json:"target"`
	Context map[string]string `json:"context"`
}

// readAuditEvents reads the JSON-lines audit log, returning the decoded events.
// It polls briefly because the file sink flushes asynchronously after the
// triggering request returns.
func readAuditEvents(t testing.TB, path string) []auditEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var events []auditEvent
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && len(b) > 0 {
			events = events[:0]
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var ev auditEvent
				if json.Unmarshal([]byte(line), &ev) == nil {
					events = append(events, ev)
				}
			}
			if len(events) > 0 {
				return events
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return events
}

// auditIngestReferences returns the source references (commit SHAs) of the
// layer.ingested events for the given layer, in log order.
func auditIngestReferences(t testing.TB, path, layerID string) []string {
	t.Helper()
	var refs []string
	for _, ev := range readAuditEvents(t, path) {
		if ev.Type == "layer.ingested" && ev.Target == layerID {
			refs = append(refs, ev.Context["reference"])
		}
	}
	return refs
}

// auditPublishedVersions returns the set of versions for which an
// artifact.published event named the given artifact id.
func auditPublishedVersions(t testing.TB, path, id string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, ev := range readAuditEvents(t, path) {
		if ev.Type == "artifact.published" && ev.Target == id {
			out[ev.Context["version"]] = true
		}
	}
	return out
}
