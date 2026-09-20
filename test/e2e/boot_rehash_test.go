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
			if err := st.RehashManifest(ctx, rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, preFramingDigest(rec), rec.Signature); err != nil {
				t.Fatalf("downgrade %s: %v", rec.ArtifactID, err)
			}
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("the first start stored no row to downgrade")
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
	out := map[string]string{}
	for _, tenant := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, tenant.ID)
		if err != nil {
			t.Fatalf("list manifests: %v", err)
		}
		for _, rec := range recs {
			out[rec.TenantID+"/"+rec.ArtifactID+"@"+rec.Version] = rec.ContentHash
		}
	}
	return out
}

// loadRehashSkill loads the fixture artifact through the MCP server against an
// empty cache directory and returns the error text the response carried, or the
// empty string when the load succeeded.
func loadRehashSkill(t testing.TB, baseURL string) string {
	t.Helper()
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + baseURL, "PODIUM_CACHE_DIR=" + t.TempDir(), "PODIUM_MATERIALIZE_ROOT=" + t.TempDir()},
		toolCall(1, "load_artifact", map[string]any{"id": rehashSkillID}))
	result := rpcResult(t, res.Stdout, 1)
	if e, ok := result["error"]; ok && e != nil {
		return fmt.Sprint(e)
	}
	return ""
}

// Spec: §4.7.6, §6.6, §13.4 — the registry rewrites every stored content hash
// on the first start of this version, once per store. A row still at the
// previous release's digest fails a consumer's §6.6 step-2 check, which is what
// makes the completion record observable: with it set the second start skips the
// pass and the load is refused, and with it cleared the next start rewrites the
// row and the same load succeeds.
func TestE2E_BootRehashesStoredContentHashes(t *testing.T) {
	home := t.TempDir()
	sqlitePath := filepath.Join(home, "podium.db")
	env := []string{"HOME=" + home, "PODIUM_REGISTRY_STORE=sqlite", "PODIUM_SQLITE_PATH=" + sqlitePath}
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
// load of a copied artifact succeeds.
func TestE2E_MigratedTargetRewritesCopiedRows(t *testing.T) {
	srcHome := t.TempDir()
	srcDB := filepath.Join(srcHome, "podium.db")
	srcObjs := filepath.Join(srcHome, "objects")
	srcEnv := []string{
		"HOME=" + srcHome,
		"PODIUM_REGISTRY_STORE=sqlite",
		"PODIUM_SQLITE_PATH=" + srcDB,
		"PODIUM_FILESYSTEM_ROOT=" + srcObjs,
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
