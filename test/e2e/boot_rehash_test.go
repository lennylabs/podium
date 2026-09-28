package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// rehashSkillID is the artifact the boot-rehash tests carry: a skill with an
// ARTIFACT.md, a SKILL.md, and two bundled resources, so the digest covers
// every slot of the §4.7.6 serialization.
const rehashSkillID = "ops/acme/rehash-me"

func rehashRegistry(t testing.TB) string {
	t.Helper()
	return writeRegistry(t, map[string]string{
		rehashSkillID + "/ARTIFACT.md":     "---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		rehashSkillID + "/SKILL.md":        skillBody("rehash-me"),
		rehashSkillID + "/references/a.md": "reference A\n",
		rehashSkillID + "/references/b.md": "reference B\n",
	})
}

// preFramingDigest is the digest the previous release computed: the SHA-256
// over the unframed concatenation of the manifest, the SKILL.md, and each
// resource's path and body in ascending path order. It is written out here
// rather than imported, because the tree keeps no exported form of it.
func preFramingDigest(rec store.ManifestRecord) string {
	h := sha256.New()
	_, _ = h.Write(rec.Frontmatter)
	_, _ = h.Write(rec.SkillRaw)
	paths := make([]string, 0, len(rec.Resources))
	bodies := map[string][]byte{}
	for _, ref := range rec.Resources {
		paths = append(paths, ref.Path)
		bodies[ref.Path] = ref.Inline
	}
	sort.Strings(paths)
	for _, p := range paths {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write(bodies[p])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// downgradeStoredRows moves every stored row back to the previous release's
// digest through RehashManifest, with the framed hash as the compare value. It
// runs while no server process holds the store, and it returns the number of
// rows it moved.
func downgradeStoredRows(t testing.TB, sqlitePath string) int {
	t.Helper()
	return rewriteStoredRows(t, sqlitePath, func(rec store.ManifestRecord) (string, string) {
		return preFramingDigest(rec), rec.Signature
	})
}

// clearStoredSignatures removes the envelope from every stored row and keeps
// its content hash, which is the state of a store a registry filled with
// signing off. It runs while no server process holds the store.
func clearStoredSignatures(t testing.TB, sqlitePath string) int {
	t.Helper()
	return rewriteStoredRows(t, sqlitePath, func(rec store.ManifestRecord) (string, string) {
		return rec.ContentHash, ""
	})
}

// rewriteStoredRows writes every stored row's content hash and signature as
// next returns them, through RehashManifest with the stored pair as the
// compare values, and returns the number of rows it wrote.
func rewriteStoredRows(t testing.TB, sqlitePath string, next func(store.ManifestRecord) (hash, signature string)) int {
	t.Helper()
	st, err := store.OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	moved := 0
	for _, tenant := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list manifests: %v", err)
		}
		for _, rec := range recs {
			hash, signature := next(rec)
			if err := st.RehashManifest(ctx, rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, rec.Signature, hash, signature); err != nil {
				t.Fatalf("rewrite %s: %v", rec.ArtifactID, err)
			}
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("the first start stored no row to rewrite")
	}
	return moved
}

func clearRehashMarker(t testing.TB, sqlitePath string) {
	t.Helper()
	st, err := store.OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.SetDataMigrationApplied(context.Background(), store.DataMigrationContentHashFraming, false); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
}

func storedHashes(t testing.TB, sqlitePath string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rec := range storedRows(t, sqlitePath) {
		out[rec.TenantID+"/"+rec.ArtifactID+"@"+rec.Version] = rec.ContentHash
	}
	return out
}

// storedRows returns every stored manifest row, soft-deleted ones included,
// across every tenant.
func storedRows(t testing.TB, sqlitePath string) []store.ManifestRecord {
	t.Helper()
	st, err := store.OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		t.Fatalf("list tenants: %v", err)
	}
	var out []store.ManifestRecord
	for _, tenant := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list manifests: %v", err)
		}
		out = append(out, recs...)
	}
	return out
}

// requireSameHashes fails the test when a stored hash moved from want.
func requireSameHashes(t testing.TB, sqlitePath string, want map[string]string) {
	t.Helper()
	got := storedHashes(t, sqlitePath)
	if len(got) != len(want) {
		t.Fatalf("the store holds %d row(s), want %d", len(got), len(want))
	}
	for key, hash := range want {
		if got[key] != hash {
			t.Errorf("%s = %s, want the unchanged %s", key, got[key], hash)
		}
	}
}

// refusedStart runs one start that must be refused and returns its combined
// output after checking that it names each of wants.
func refusedStart(t *testing.T, env []string, wants ...string) string {
	t.Helper()
	res := runPodium(t, "", env, "serve", "--standalone", "--bind", "127.0.0.1:0")
	out := res.Stdout + res.Stderr
	if res.Exit == 0 {
		t.Fatalf("a start over an unmigrated store exited 0\n%s", out)
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("refused start output does not name %q:\n%s", want, out)
		}
	}
	return out
}

// loadRehashSkill loads the fixture artifact through the MCP server against an
// empty cache directory and returns the error text the response carried, or the
// empty string when the load succeeded.
func loadRehashSkill(t testing.TB, baseURL string) string {
	t.Helper()
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + baseURL, "PODIUM_CACHE_DIR=" + t.TempDir(), "PODIUM_MATERIALIZE_ROOT=" + t.TempDir(), "PODIUM_VERIFY_SIGNATURES=never"},
		toolCall(1, "load_artifact", map[string]any{"id": rehashSkillID}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		return fmt.Sprint(e)
	}
	return ""
}

// Spec: §4.7.6, §6.6, §13.4 — the registry rewrites every stored content hash
// on the first start of this version, once per store. A row still at the
// previous release's digest is refused by the registry's §13.4 stored-row
// admission check with materialize.content_hash_mismatch, which is what makes
// the completion record observable: with it set the second start skips the
// pass and the load is refused, and with it cleared the next start rewrites the
// row and the same load succeeds. It pins, through the compiled binary, the
// boot rewrite of the zero-configuration signing-off standalone store, which
// sits in the directory of the default key location.
func TestE2E_BootRehashesStoredContentHashes(t *testing.T) {
	home := t.TempDir()
	sqlitePath := filepath.Join(home, ".podium", "standalone", "podium.db")
	// The subject is the content-hash rewrite, so signing is off (§13.10
	// signs by default). Neither PODIUM_SQLITE_PATH nor PODIUM_SIGN_KEY_PATH
	// is set, so the store takes its default path beside the default key.
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SIGN=none"}
	reg := rehashRegistry(t)

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if msg := loadRehashSkill(t, first.BaseURL); msg != "" {
		t.Fatalf("load against the first start failed: %s", msg)
	}
	stopProc(first.cmd)

	// The marker the first start set makes the second start skip the pass,
	// so the downgraded row is served at the previous digest and the load is
	// refused.
	downgradeStoredRows(t, sqlitePath)
	second := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	msg := loadRehashSkill(t, second.BaseURL)
	if !strings.Contains(msg, "materialize.content_hash_mismatch") {
		t.Errorf("load against a pre-upgrade row = %q, want materialize.content_hash_mismatch", msg)
	}
	stopProc(second.cmd)

	// Clearing the record makes the next start run the pass.
	clearRehashMarker(t, sqlitePath)
	third := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	if msg := loadRehashSkill(t, third.BaseURL); msg != "" {
		t.Fatalf("load after the rewrite failed: %s", msg)
	}
	if !strings.Contains(third.log(), "rehash:") {
		t.Errorf("the server log carries no rehash summary line:\n%s", third.log())
	}
	after := storedHashes(t, sqlitePath)
	stopProc(third.cmd)

	// A further start is a no-op: the record is set and no stored hash moves.
	fourth := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	stopProc(fourth.cmd)
	again := storedHashes(t, sqlitePath)
	if len(again) != len(after) {
		t.Fatalf("a further start changed the row count: %d, want %d", len(again), len(after))
	}
	for key, hash := range after {
		if again[key] != hash {
			t.Errorf("%s moved on a further start: %s, want %s", key, again[key], hash)
		}
	}
}

// Spec: §13.4, §13.12 — with signing off, a start over a store outside the
// directory of the default key location is refused while no completion is
// recorded, and podium-server sign-stored-rows performs the content-hash
// rewrite without a signer: its dry run reports no signing key and a plan
// digest, the run given that digest moves every downgraded row back to the
// current digest and records completion, and the next start runs no rewrite
// and serves the artifact.
func TestE2E_SigningOffRewriteThroughSignStoredRows(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sqlitePath := filepath.Join(home, "podium.db")
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SQLITE_PATH=" + sqlitePath, "PODIUM_SIGN=none"}
	reg := rehashRegistry(t)

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	stopProc(first.cmd)
	current := storedHashes(t, sqlitePath)
	downgradeStoredRows(t, sqlitePath)
	clearRehashMarker(t, sqlitePath)
	downgraded := storedHashes(t, sqlitePath)

	out := refusedStart(t, env, "sign-stored-rows", "--dry-run", "PODIUM_SIGN=none")
	if strings.Contains(out, "--plan-digest") {
		t.Errorf("the signing-off refusal names --plan-digest:\n%s", out)
	}
	requireSameHashes(t, sqlitePath, downgraded)

	dry := runServerBin(t, env, "sign-stored-rows", "--dry-run")
	if dry.Exit != 0 || !strings.Contains(dry.Stdout, "signing_key=- verify_keys=-") {
		t.Fatalf("dry run exit=%d, want 0 and a header with no signing key\nstdout:\n%s\nstderr:\n%s", dry.Exit, dry.Stdout, dry.Stderr)
	}
	run := runServerBin(t, env, "sign-stored-rows", "--plan-digest="+planDigestOf(t, dry.Stdout))
	if run.Exit != 0 {
		t.Fatalf("run exit=%d, want 0\nstdout:\n%s\nstderr:\n%s", run.Exit, run.Stdout, run.Stderr)
	}
	after := storedHashes(t, sqlitePath)
	if len(after) != len(current) {
		t.Fatalf("the run changed the row count: %d, want %d", len(after), len(current))
	}
	for key, hash := range current {
		if after[key] != hash {
			t.Errorf("%s = %s after the run, want the current digest %s", key, after[key], hash)
		}
	}

	next := startServerArgs(t, env, "serve", "--standalone")
	if strings.Contains(next.log(), "rehash:") {
		t.Errorf("the start after the run ran the rewrite again:\n%s", next.log())
	}
	if msg := loadRehashSkill(t, next.BaseURL); msg != "" {
		t.Fatalf("load after the signing-off run failed: %s", msg)
	}
}

// Spec: §4.7.9, §13.4 — a start configured to sign whose key file is absent is
// refused while the rewrite has not completed and a stored row carries a
// signature, and it generates no key, so the operator's next start with the key
// that signed those rows is the one that rewrites them.
func TestE2E_BootRefusesAGeneratedSigningKey(t *testing.T) {
	home := t.TempDir()
	sqlitePath := filepath.Join(home, "podium.db")
	keyPath := filepath.Join(home, "registry-signing.key")
	reg := rehashRegistry(t)
	env := []string{
		"HOME=" + home,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + sqlitePath,
		"PODIUM_SIGN=registry-key",
		"PODIUM_SIGN_KEY_PATH=" + keyPath,
	}

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	stopProc(first.cmd)
	downgradeStoredRows(t, sqlitePath)
	clearRehashMarker(t, sqlitePath)

	absent := filepath.Join(home, "absent-signing.key")
	refusedEnv := append(append([]string{}, env[:len(env)-1]...), "PODIUM_SIGN_KEY_PATH="+absent)
	res := runPodium(t, "", refusedEnv, "serve", "--standalone", "--layer-path", reg, "--bind", "127.0.0.1:0")
	if res.Exit == 0 {
		t.Fatalf("a start that would generate a signing key exited 0\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "PODIUM_SIGN_KEY_PATH") {
		t.Errorf("output does not name PODIUM_SIGN_KEY_PATH:\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(absent); err == nil {
		t.Error("a key file exists after a refused start")
	}
}

// Spec: §13.4, §13.10 — podium admin migrate-to-standard clears the target
// store's record of the rewrite before it copies the first manifest row, so the
// target registry's next start rewrites the rows it copied and a consumer's
// load of a copied artifact succeeds. The target's key location shares the
// target store's directory: a signing-off target there keeps the boot rewrite,
// where a target elsewhere is refused until sign-stored-rows runs.
func TestE2E_MigratedTargetRewritesCopiedRows(t *testing.T) {
	srcHome := t.TempDir()
	srcDB := filepath.Join(srcHome, "podium.db")
	srcObjs := filepath.Join(srcHome, "objects")
	srcEnv := []string{
		"HOME=" + srcHome,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + srcDB,
		"PODIUM_FILESYSTEM_ROOT=" + srcObjs,
		"PODIUM_SIGN=none",
	}
	source := startServerArgs(t, srcEnv, "serve", "--standalone", "--layer-path", rehashRegistry(t))
	stopProc(source.cmd)
	downgradeStoredRows(t, srcDB)

	// The target is a store a registry already started on, so its record is
	// set. Its fixture shares no artifact ID with the source, because a
	// shared key at a differing hash makes the copy fail closed.
	tgtHome := t.TempDir()
	tgtDB := filepath.Join(tgtHome, "podium.db")
	tgtObjs := filepath.Join(tgtHome, "objects")
	tgtEnv := []string{
		"HOME=" + tgtHome,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + tgtDB,
		"PODIUM_FILESYSTEM_ROOT=" + tgtObjs,
		"PODIUM_SIGN=none",
		"PODIUM_SIGN_KEY_PATH=" + filepath.Join(tgtHome, "registry-signing.key"),
	}
	otherReg := writeRegistry(t, map[string]string{
		"ops/acme/other/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"ops/acme/other/SKILL.md":    skillBody("other"),
	})
	pre := startServerArgs(t, tgtEnv, "serve", "--standalone", "--layer-path", otherReg)
	stopProc(pre.cmd)

	res := runPodium(t, "", []string{"HOME=" + srcHome},
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--source-objects", srcObjs,
		"--target-store", "sqlite",
		"--target-sqlite", tgtDB,
		"--target-objects-type", "filesystem",
		"--target-objects", tgtObjs,
	)
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard exited %d\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}

	// The target starts with no layer path, so its rows are the copied ones.
	target := startServerArgs(t, tgtEnv, "serve", "--standalone")
	if msg := loadRehashSkill(t, target.BaseURL); msg != "" {
		t.Fatalf("load of a copied artifact from the migrated target failed: %s", msg)
	}
}

// Spec: §13.4, §4.7.9 — late signing. A store a registry filled with signing
// off keeps its rows unsigned after a restart with signing on, because the
// completion record is set and the rewrite does not run again, so a
// verifying load is refused with materialize.signature_missing.
// A reviewed sign-stored-rows --include-unsigned run, given the plan digest of
// its dry run, attests those rows beside the serving registry, and the same
// load succeeds with no restart.
// Matrix: §6.10 (materialize.signature_missing)
func TestE2E_LateSigningThroughSignStoredRows(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keyPath := filepath.Join(home, "registry-signing.key")
	env := rotationEnv(home, keyPath)
	reg := rehashRegistry(t)

	unsigned := startServerArgs(t, append(append([]string{}, env...), "PODIUM_SIGN=none"), "serve", "--standalone", "--layer-path", reg)
	stopProc(unsigned.cmd)

	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + firstLine(generateKeyFile(t, keyPath).Stdout)
	signing := startServerArgs(t, env, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, signing.BaseURL, rehashSkillID, verifyKey); !strings.HasPrefix(errStr, "materialize.signature_missing") {
		t.Fatalf("load of a row stored with signing off = %q, want materialize.signature_missing\nstderr: %s", errStr, res.Stderr)
	}

	dry := signStoredRows(t, env, "--include-unsigned", "--dry-run")
	if dry.Exit != 0 {
		t.Fatalf("sign-stored-rows --include-unsigned --dry-run exit=%d\nstdout:\n%s\nstderr:\n%s", dry.Exit, dry.Stdout, dry.Stderr)
	}
	res := signStoredRows(t, env, "--include-unsigned", "--plan-digest="+planDigestOf(t, dry.Stdout))
	if res.Exit != 0 || !strings.Contains(res.Stdout, "rehash: 0 unsigned left") {
		t.Fatalf("sign-stored-rows --include-unsigned exit=%d, want 0 and no unsigned row left\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	if errStr, res := bridgeLoad(t, signing.BaseURL, rehashSkillID, verifyKey); errStr != "" {
		t.Fatalf("load after sign-stored-rows = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, signing.log())
	}
}

// Spec: §13.4, §13.12 — while no completion is recorded, a start over a
// SQLite store outside the directory of the signing key file is refused and
// rewrites nothing. The reviewed sign-stored-rows procedure, a
// --include-unsigned dry run and a run with its plan digest, attests the
// unsigned row and records completion, and the next start serves it to a
// verifying consumer.
func TestE2E_FirstStartRefusesAnUnmigratedStoreWhenTheKeyIsElsewhere(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "registry-signing.key")
	env := rotationEnv(home, keyPath)
	sqlitePath := filepath.Join(home, "podium.db")
	reg := rehashRegistry(t)
	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + firstLine(generateKeyFile(t, keyPath).Stdout)

	first := startServerArgs(t, env, "serve", "--standalone", "--layer-path", reg)
	stopProc(first.cmd)
	clearStoredSignatures(t, sqlitePath)
	clearRehashMarker(t, sqlitePath)
	before := storedHashes(t, sqlitePath)

	refusedStart(t, env, "sign-stored-rows", "--plan-digest")
	requireSameHashes(t, sqlitePath, before)
	for _, rec := range storedRows(t, sqlitePath) {
		if rec.Signature != "" {
			t.Errorf("%s was signed by a refused start", rec.ArtifactID)
		}
	}

	attestUnsigned(t, env)
	restarted := startServerArgs(t, env, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, restarted.BaseURL, rehashSkillID, verifyKey); errStr != "" {
		t.Fatalf("load after sign-stored-rows = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, restarted.log())
	}
}

// attestUnsigned runs the reviewed procedure that attests unsigned rows: a
// --include-unsigned dry run, then a run given its plan digest, which must
// leave no row unsigned.
func attestUnsigned(t *testing.T, env []string) {
	t.Helper()
	dry := signStoredRows(t, env, "--dry-run", "--include-unsigned")
	if dry.Exit != 0 {
		t.Fatalf("sign-stored-rows --dry-run --include-unsigned exit=%d\nstdout:\n%s\nstderr:\n%s", dry.Exit, dry.Stdout, dry.Stderr)
	}
	res := signStoredRows(t, env, "--include-unsigned", "--plan-digest="+planDigestOf(t, dry.Stdout))
	if res.Exit != 0 || !strings.Contains(res.Stdout, "rehash: 0 unsigned left") {
		t.Fatalf("sign-stored-rows --include-unsigned exit=%d, want 0 and no unsigned row left\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// Spec: §13.4, §4.7.9 — a v0.4.0 store upgraded with signing on and
// PODIUM_SIGN_KEY_PATH naming a new path outside the store's directory holds
// unsigned rows and no key file. The start is refused, names podium admin
// signing-key generate with that path ahead of the dry run, and creates no
// key, because sign-stored-rows never generates one. After the key is
// generated, the reviewed procedure records completion and the next start
// serves the row to a verifying consumer.
func TestE2E_UnmigratedStoreWithoutAKeyFileMigratesAfterSigningKeyGenerate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "registry-signing.key")
	env := rotationEnv(home, keyPath)
	sqlitePath := filepath.Join(home, "podium.db")

	first := startServerArgs(t, append(append([]string{}, env...), "PODIUM_SIGN=none"), "serve", "--standalone", "--layer-path", rehashRegistry(t))
	stopProc(first.cmd)
	downgradeStoredRows(t, sqlitePath)
	clearRehashMarker(t, sqlitePath)
	before := storedHashes(t, sqlitePath)

	generate := "podium admin signing-key generate --key-file " + keyPath
	out := refusedStart(t, env, generate, "--dry-run")
	if strings.Index(out, generate) > strings.Index(out, "--dry-run") {
		t.Errorf("the refusal names the dry run before signing-key generate:\n%s", out)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("stat %s after a refused start = %v, want absence", keyPath, err)
	}
	requireSameHashes(t, sqlitePath, before)

	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + firstLine(generateKeyFile(t, keyPath).Stdout)
	attestUnsigned(t, env)
	restarted := startServerArgs(t, env, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, restarted.BaseURL, rehashSkillID, verifyKey); errStr != "" {
		t.Fatalf("load after sign-stored-rows = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, restarted.log())
	}
}

// Spec: §13.4, §13.10 — migrate-to-standard clears the record on a target
// SQLite store outside the directory of the signing key file, so the target's
// start is refused and leaves the copied row unchanged. The copied row was
// signed under the shared key, so sign-stored-rows without flags records
// completion, and the target then serves it to a verifying consumer.
func TestE2E_MigratedTargetOutsideTheKeyDirectoryRefusesUntilSignStoredRows(t *testing.T) {
	t.Parallel()
	srcHome := t.TempDir()
	srcDB := filepath.Join(srcHome, "podium.db")
	srcObjs := filepath.Join(srcHome, "objects")
	keyPath := filepath.Join(srcHome, "registry-signing.key")
	verifyKey := "PODIUM_SIGNATURE_VERIFY_KEY=" + firstLine(generateKeyFile(t, keyPath).Stdout)
	source := startServerArgs(t, rotationEnv(srcHome, keyPath), "serve", "--standalone", "--layer-path", rehashRegistry(t))
	stopProc(source.cmd)

	tgtHome := t.TempDir()
	tgtDB := filepath.Join(tgtHome, "podium.db")
	tgtObjs := filepath.Join(tgtHome, "objects")
	tgtEnv := []string{
		"HOME=" + tgtHome,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + tgtDB,
		"PODIUM_FILESYSTEM_ROOT=" + tgtObjs,
		"PODIUM_SIGN=registry-key",
		"PODIUM_SIGN_KEY_PATH=" + keyPath,
	}
	res := runPodium(t, "", []string{"HOME=" + srcHome},
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--source-objects", srcObjs,
		"--target-store", "sqlite",
		"--target-sqlite", tgtDB,
		"--target-objects-type", "filesystem",
		"--target-objects", tgtObjs,
	)
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard exited %d\nstdout:\n%s\nstderr:\n%s", res.Exit, res.Stdout, res.Stderr)
	}
	copied := storedHashes(t, tgtDB)

	refusedStart(t, tgtEnv, "sign-stored-rows")
	requireSameHashes(t, tgtDB, copied)

	if run := signStoredRows(t, tgtEnv); run.Exit != 0 {
		t.Fatalf("sign-stored-rows on the target exit=%d\nstdout:\n%s\nstderr:\n%s", run.Exit, run.Stdout, run.Stderr)
	}
	target := startServerArgs(t, tgtEnv, "serve", "--standalone")
	if errStr, res := bridgeLoad(t, target.BaseURL, rehashSkillID, verifyKey); errStr != "" {
		t.Fatalf("load from the migrated target = %q, want success\nstderr: %s\nlog:\n%s", errStr, res.Stderr, target.log())
	}
}
