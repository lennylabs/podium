package e2e

// End-to-end coverage of the §7.3.1 patch semantics on an admin-defined
// layer's visibility, through the compiled binaries.
//
// `POST|PUT /v1/layers/update` applies the visibility members the body
// carries and preserves the ones it omits, so "public": false withdraws the
// axis and "groups": [] empties the list. The CLI builds that body from the
// flags the operator set, which is what makes `--public=false`,
// `--clear-groups`, and `--clear-users` expressible. A plain `go test`
// coverage profile records nothing for the spawned binary, so the CLI arms sit
// here rather than in cmd/podium alone.
//
// The write and the §8.1 event follow what the patch changed: a patch storing
// nothing appends no audit line, and a change no re-resolve can observe (a
// webhook-secret rotation) records its audit event and wakes no §7.5.4
// watcher.
//
// Every arm boots the registry through startServer or startServerArgs, which
// run on darwin as on linux. The oidc-jwt stack skips on darwin, and no arm
// here depends on it.
//
// Spec: §7.3.1 (the update patch semantics and the unchanged write), §8.1 (the
// layer event follows what the write changed), §7.5.4 (the wake narrows to a
// change a re-resolve can observe), §4.6 (a record setting no visibility field
// reaches no resolved caller).

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// narrowRepo is a network git URL, so the §7.3.1 local-source rule refuses no
// arm below and the registry stores a git layer, which is the class a webhook
// secret can be rotated on.
const narrowRepo = "https://github.com/acme/company.git"

// narrowEnv is the CLI environment for an unauthenticated standalone
// registry: the target URL, a test-scoped keychain name, and a private HOME.
func narrowEnv(t *testing.T, srv *serverProc) []string {
	t.Helper()
	return []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_TOKEN_KEYCHAIN_NAME=podium-visibility-narrowing-test",
		"HOME=" + t.TempDir(),
	}
}

// Spec: §7.3.1 — `podium layer update` withdraws an admin-defined layer's
// visibility one member at a time, and `podium layer list` reads the
// withdrawal back. Each arm patches one member and asserts that the members it
// omitted kept their stored values, which is the half a body built from
// non-zero flag values cannot express: `--public=false` parses to the
// boolean's zero value, and the shipped guard dropped it.
func TestLayerCLI_UpdateWithdrawsAdminDefinedVisibility(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	env := narrowEnv(t, srv)

	reg := runPodium(t, "", env, "layer", "register", "--id", "company",
		"--repo", narrowRepo, "--ref", "main",
		"--public", "--organization", "--group", "engineering", "--user", "alice@acme.com")
	cliWantExit(t, reg, 0, "register an admin-defined layer on every visibility axis")

	before, seen := cliLayer(t, env, "company")
	if !seen || !before.Public || !before.Organization ||
		!strings.Contains(strings.Join(before.Groups, ","), "engineering") ||
		len(before.Users) != 1 {
		t.Fatalf("registered company is %+v (seen=%v), want every visibility member set", before, seen)
	}

	// ---- --public=false withdraws the axis and satisfies the field guard ----
	// The flag carries the boolean's zero value, so this invocation is also
	// the arm proving the CLI's "at least one mutable field" guard reads the
	// flags the operator set rather than the values they hold.
	res := runPodium(t, "", env, "layer", "update", "--id", "company", "--public=false")
	cliWantExit(t, res, 0, "withdraw the public axis")
	if strings.Contains(res.Stderr, "at least one mutable field") {
		t.Errorf("--public=false alone was dropped by the mutable-field guard:\n%s", res.Stderr)
	}
	afterPublic, _ := cliLayer(t, env, "company")
	if afterPublic.Public {
		t.Errorf("company is still public after --public=false: %+v", afterPublic)
	}
	if !afterPublic.Organization || len(afterPublic.Groups) != 1 || len(afterPublic.Users) != 1 {
		t.Errorf("withdrawing public disturbed the omitted members: %+v", afterPublic)
	}

	// ---- --clear-groups empties the group list ------------------------------
	cliWantExit(t, runPodium(t, "", env, "layer", "update", "--id", "company", "--clear-groups"),
		0, "empty the group list")
	afterGroups, _ := cliLayer(t, env, "company")
	if len(afterGroups.Groups) != 0 {
		t.Errorf("groups = %v after --clear-groups, want empty", afterGroups.Groups)
	}
	if len(afterGroups.Users) != 1 || !afterGroups.Organization {
		t.Errorf("emptying groups disturbed the omitted members: %+v", afterGroups)
	}

	// ---- --clear-users empties the user list --------------------------------
	cliWantExit(t, runPodium(t, "", env, "layer", "update", "--id", "company", "--clear-users"),
		0, "empty the user list")
	afterUsers, _ := cliLayer(t, env, "company")
	if len(afterUsers.Users) != 0 {
		t.Errorf("users = %v after --clear-users, want empty", afterUsers.Users)
	}

	// ---- the last axis withdraws too ----------------------------------------
	cliWantExit(t, runPodium(t, "", env, "layer", "update", "--id", "company", "--organization=false"),
		0, "withdraw the organization axis")
	// Spec: §4.6 — the record now sets no visibility field, which is a stored
	// state the list still reports to an admin-arm caller.
	final, seen := cliLayer(t, env, "company")
	if !seen {
		t.Fatalf("company left the list after every axis was withdrawn")
	}
	if final.Public || final.Organization || len(final.Groups) != 0 || len(final.Users) != 0 {
		t.Errorf("company is %+v, want a record setting no visibility field", final)
	}
}

// narrowRegister registers an admin-defined git layer over HTTP and fails the
// test unless the registry stores it.
func narrowRegister(t *testing.T, srv *serverProc, id string, body map[string]any) {
	t.Helper()
	body["id"] = id
	body["source_type"] = "git"
	body["repo"] = narrowRepo
	body["ref"] = "main"
	st, out := apiDo(t, http.MethodPost, srv.BaseURL+"/v1/layers", body)
	apiWantStatus(t, st, 201, "register "+id, out)
}

// Spec: §7.5.4 / §8.1 — a webhook-secret rotation records its audit event and
// wakes no watcher, and a visibility change on the same layer wakes one. The
// subscription is held open across both, so the assertion is a count: exactly
// one layer.config_changed reaches the stream. An unconditional wake, which is
// what the registry published before, delivers two.
func TestLayerUpdate_RotationWakesNoWatcher(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")
	narrowRegister(t, srv, "company", map[string]any{"public": true})

	// The subscription is registered before either patch, so no event the
	// arms below drive can be missed.
	stream := openEventStream(t, srv, "layer.config_changed")

	st, body := apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=company",
		map[string]any{"rotate_webhook_secret": true})
	apiWantStatus(t, st, 200, "rotate the webhook secret", body)

	st, body = apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=company",
		map[string]any{"public": false})
	apiWantStatus(t, st, 200, "withdraw the public axis", body)

	ev := stream.next(t, 10*time.Second)
	if ev.Event != "layer.config_changed" {
		t.Fatalf("first event is %q, want layer.config_changed", ev.Event)
	}
	select {
	case extra, ok := <-stream.events:
		if ok {
			t.Errorf("a second event reached the stream (%q); the rotation woke a watcher", extra.Event)
		}
	case <-time.After(2 * time.Second):
	}
}

// countAuditLines returns how many lines of the audit log at path name the
// given event type.
func countAuditLines(t *testing.T, path, eventType string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read audit log: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, eventType) {
			n++
		}
	}
	return n
}

// waitAuditLines polls the audit log until it carries want lines naming the
// event type, and returns the count it settled on.
func waitAuditLines(t *testing.T, path, eventType string, want int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := countAuditLines(t, path, eventType)
		if got >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Spec: §7.3.1 / §8.1 — a patch that stores nothing writes no record and
// appends no audit line, and a patch that changes a member appends exactly
// one. The no-op body restates the stored visibility verbatim, which is what a
// client echoing a layer object back sends.
func TestLayerUpdate_NoOpPatchAppendsNoAuditLine(t *testing.T) {
	t.Parallel()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	srv := startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_AUDIT_LOG_PATH=" + auditPath},
		"serve", "--standalone")
	narrowRegister(t, srv, "company", map[string]any{"public": true, "groups": []string{"engineering"}})

	base := waitAuditLines(t, auditPath, "layer.config_changed", 1, 10*time.Second)
	if base < 1 {
		t.Fatalf("the registration appended no layer.config_changed line\nlog:\n%s", brReadOrEmpty(auditPath))
	}

	st, body := apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=company",
		map[string]any{"public": true, "organization": false, "groups": []string{"engineering"}, "users": nil})
	apiWantStatus(t, st, 200, "restate the stored visibility", body)

	// The write is synchronous with the response, so a settled read after a
	// short grace period is the assertion rather than a race.
	time.Sleep(500 * time.Millisecond)
	if got := countAuditLines(t, auditPath, "layer.config_changed"); got != base {
		t.Errorf("the no-op patch appended %d line(s)\nlog:\n%s", got-base, brReadOrEmpty(auditPath))
	}

	st, body = apiDo(t, http.MethodPut, srv.BaseURL+"/v1/layers/update?id=company",
		map[string]any{"public": false})
	apiWantStatus(t, st, 200, "withdraw the public axis", body)
	waitAuditLines(t, auditPath, "layer.config_changed", base+1, 10*time.Second)
	// Settle before the count, so a second line arriving just behind the first
	// is read here rather than after the assertion.
	time.Sleep(500 * time.Millisecond)
	if got := countAuditLines(t, auditPath, "layer.config_changed"); got != base+1 {
		t.Errorf("the withdrawal appended %d line(s), want exactly 1\nlog:\n%s", got-base, brReadOrEmpty(auditPath))
	}
}
