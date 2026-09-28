// Live test of the chart's §13.4 upgrade procedure on a kind cluster.
//
// The render tests pin every values-only refusal, because the chart reads
// nothing from the cluster. The hook Job's lifecycle, the plan-digest
// binding of the run, and the registry's boot refusal over a store that
// v0.4.0 wrote appear only on a cluster, so this test installs v0.4.0, seeds
// its store, and walks the upgrade procedure docs/deployment/clustered.md
// states, step by step, including every refusal the procedure relies on.
//
// It is opt-in: it runs only with PODIUM_LIVE_KIND=1 and with docker, kind,
// kubectl, helm, and git on PATH, and it skips otherwise, so `go test ./...`
// and the coverage run start no container. `make test-live-kind` runs it.
package chart

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	kindCluster  = "podium-live"
	kindContext  = "kind-" + kindCluster
	liveRepo     = "podium-live"
	liveCurrent  = "current"
	liveNext     = "current2"
	liveOld      = "v0.4.0"
	liveRelease  = "podium"
	liveFullname = "podium-podium"
	liveJob      = "podium-podium-migrate"
	liveSelector = "app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium"
	liveCleanup  = "app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate"
	liveRecord   = "content-hash-framing"
	minioImage   = "minio/minio:RELEASE.2024-10-29T16-01-48Z"
	mcImage      = "minio/mc:RELEASE.2024-10-29T15-34-59Z"
	pgImage      = "pgvector/pgvector:pg16"
	busyboxImage = "busybox:1.36"
	// bundledPGImage is the chart's postgresql.image default, loaded so a
	// case that enables the bundled Postgres does not depend on a node pull.
	bundledPGImage = "postgres:17-alpine"
)

// liveEnv holds what every case shares: the repository, the v0.4.0 worktree,
// the built CLI, and the signing key file.
type liveEnv struct {
	root     string
	worktree string
	work     string
	podium   string
	keyFile  string
	pubKey   string
}

// TestChart_KindUpgradeFromV040 drives the upgrade from v0.4.0 through the
// chart on a kind cluster.
//
// Spec: §13.4
func TestChart_KindUpgradeFromV040(t *testing.T) {
	if os.Getenv("PODIUM_LIVE_KIND") != "1" {
		t.Skip("set PODIUM_LIVE_KIND=1 to run the kind upgrade test; `make test-live-kind` does")
	}
	for _, tool := range []string{"docker", "kind", "kubectl", "helm", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	env := setupKind(t)

	t.Run("signing-on", func(t *testing.T) { signingOnUpgrade(t, env, "podium-live-1") })
	t.Run("signing-off", func(t *testing.T) { signingOffUpgrade(t, env, "podium-live-2") })
	t.Run("fresh-install", func(t *testing.T) { freshInstall(t, env, "podium-live-3") })
}

// setupKind builds both images, the CLI, and the signing key, creates the
// cluster, and registers the cleanup of everything it made.
func setupKind(t *testing.T) *liveEnv {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	env := &liveEnv{root: root, work: work, worktree: filepath.Join(work, "v040")}

	mustRun(t, root, 5*time.Minute, "git", "worktree", "add", "--detach", env.worktree, "v0.4.0")
	t.Cleanup(func() { _, _ = run(root, 2*time.Minute, "git", "worktree", "remove", "--force", env.worktree) })

	mustRun(t, root, 30*time.Minute, "docker", "build", "-t", liveRepo+":"+liveCurrent, root)
	mustRun(t, env.worktree, 30*time.Minute, "docker", "build", "-t", liveRepo+":"+liveOld, env.worktree)
	mustRun(t, root, time.Minute, "docker", "tag", liveRepo+":"+liveCurrent, liveRepo+":"+liveNext)
	t.Cleanup(func() {
		_, _ = run(root, 2*time.Minute, "docker", "rmi", liveRepo+":"+liveCurrent, liveRepo+":"+liveNext, liveRepo+":"+liveOld)
	})

	env.podium = filepath.Join(work, "podium")
	mustRun(t, root, 10*time.Minute, "go", "build", "-o", env.podium, "./cmd/podium")
	env.keyFile = filepath.Join(work, "registry-signing.key")
	mustRun(t, work, time.Minute, env.podium, "admin", "signing-key", "generate", "--key-file", env.keyFile)
	raw, err := os.ReadFile(env.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(line, "public:"); ok {
			env.pubKey = strings.TrimSpace(rest)
		}
	}
	if env.pubKey == "" {
		t.Fatalf("the generated key file carries no public: line")
	}

	_, _ = run(root, 5*time.Minute, "kind", "delete", "cluster", "--name", kindCluster)
	mustRun(t, root, 10*time.Minute, "kind", "create", "cluster", "--name", kindCluster, "--wait", "180s")
	t.Cleanup(func() { _, _ = run(root, 5*time.Minute, "kind", "delete", "cluster", "--name", kindCluster) })
	for _, tag := range []string{liveCurrent, liveNext, liveOld} {
		mustRun(t, root, 10*time.Minute, "kind", "load", "docker-image", liveRepo+":"+tag, "--name", kindCluster)
	}
	loadThirdPartyImages(t, root, work)
	return env
}

// loadThirdPartyImages pulls the backing-service and helper images on the
// host and loads each into the kind node as a single-platform archive. The
// node does not pull them itself because a Docker Hub pull from inside kind
// can be refused (insufficient_scope) where the host's authenticated pull
// succeeds. `kind load docker-image` is not used either: under Docker's
// containerd image store it exports a multi-platform index whose other
// platforms' blobs are absent, and the load fails with "content digest ...
// not found". `docker save --platform` writes only the node's platform. An
// image already on the host is not pulled again, because Docker Hub refuses
// the pinned MinIO tags while a local store can still hold them.
func loadThirdPartyImages(t *testing.T, root, work string) {
	t.Helper()
	arch := strings.TrimSpace(mustRun(t, root, time.Minute, "docker", "version", "--format", "{{.Server.Arch}}"))
	platform := "linux/" + arch
	for i, image := range []string{minioImage, mcImage, pgImage, bundledPGImage, busyboxImage} {
		archive := filepath.Join(work, fmt.Sprintf("image-%d.tar", i))
		if _, err := run(root, time.Minute, "docker", "image", "inspect", image); err != nil {
			mustRun(t, root, 10*time.Minute, "docker", "pull", "--platform", platform, image)
		}
		mustRun(t, root, 10*time.Minute, "docker", "save", "--platform", platform, "-o", archive, image)
		mustRun(t, root, 10*time.Minute, "kind", "load", "image-archive", archive, "--name", kindCluster)
	}
}

// signingOnUpgrade walks the signing-on procedure, cases (a) through (n2),
// with case (l2) after serving.
func signingOnUpgrade(t *testing.T, env *liveEnv, ns string) {
	k := newCluster(t, env, ns)
	k.backingServices()
	k.secrets(true)
	k.seed()
	k.installV040(nil)
	// The procedure's retries add revisions, and Helm's default history
	// limit of 10 would prune the v0.4.0 revision the rollback in case (n)
	// returns to. Step 1 of the documented procedure notes that revision and
	// lifts the limit for the procedure's commands, and so does the test.
	t.Setenv("HELM_MAX_HISTORY", "0")
	v040Revision := k.lastRevision(t)

	newValues := k.valuesFile("new", map[string]any{
		"image":     map[string]any{"repository": liveRepo, "tag": liveCurrent},
		"signing":   map[string]any{"secretName": "podium-signing-key"},
		"migration": map[string]any{"previousImage": liveRepo + ":" + liveOld},
	})

	step := func(name string, f func(t *testing.T)) {
		if !t.Run(name, f) {
			t.FailNow()
		}
	}
	// The chart renders a serving upgrade over the unmigrated store, and the
	// registry refuses it at start: the new pods exit, the rolling update
	// keeps the v0.4.0 pods, and the store is unchanged.
	step("a-default-upgrade-refused-at-boot", func(t *testing.T) {
		before := k.storeSnapshot(t)
		k.helmFail(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "replicaCount=2",
			"--wait", "--timeout", "5m")
		k.assertBootRefused(t, before)
		k.assertReadyOn(t, 2, liveRepo+":"+liveOld)
	})
	step("c-stop-with-a-failed-pod", func(t *testing.T) {
		k.apply(t, fmt.Sprintf(failedPodManifest, ns, busyboxImage))
		k.waitFor(t, 3*time.Minute, "pod/evicted-look-alike to reach Failed", func() bool {
			return k.kubectlOut("get", "pod", "evicted-look-alike", "-o", "jsonpath={.status.phase}") == "Failed"
		})
		k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "replicaCount=0")
		k.stopWait(t)
		k.assertRegistryPods(t, 0, "")
		k.backup(t)
	})
	var digest string
	step("f-dry-run", func(t *testing.T) {
		var log, notes string
		log, notes, digest = k.dryRun(t, newValues)
		wantIn(t, notes, liveJob, "grep -E '^dry-run: plan digest ' dry-run.log", "migration.planDigest=<digest>")
		for _, id := range []string{"/demo/hello/greet@", "/demo/hello/bigref@"} {
			if !regexp.MustCompile(regexp.QuoteMeta(id) + `\S* class=\S+ target=\S+ write=true sign=true signed_by=unsigned`).MatchString(log) {
				t.Errorf("the dry run lists no sign=true signed_by=unsigned line for %s:\n%s", id, log)
			}
		}
		if k.recordPresent(t) {
			t.Fatal("the dry run wrote the completion record")
		}
	})
	// An unsigned row stored after the dry run changes the plan the run
	// attests, so the run refuses before any write and prints its own plan.
	step("g3-run-after-a-planted-row", func(t *testing.T) {
		k.plantUnsignedRow(t)
		log, _, err := k.runJob(t, newValues, digest)
		if err == nil {
			t.Fatalf("the run succeeded over a planted row:\n%s", log)
		}
		k.assertRunRefused(t, log)
		if !regexp.MustCompile(`(?m)^plan: \S*/demo/hello/greet@` + regexp.QuoteMeta(plantedVersion) + ` class=`).MatchString(log) {
			t.Errorf("the refused run prints no plan: line for the planted row:\n%s", log)
		}
		k.psql(t, fmt.Sprintf(`SELECT format('DELETE FROM %%I.manifests WHERE version = %%L', table_schema, '%s')
  FROM information_schema.tables WHERE table_name = 'manifests' \gexec
`, plantedVersion))
		_, _, digest = k.dryRun(t, newValues)
	})
	step("h-serve-after-only-a-dry-run", func(t *testing.T) {
		before := k.storeSnapshot(t)
		k.helmFail(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--wait", "--timeout", "5m")
		k.assertBootRefused(t, before)
		k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "replicaCount=0")
		k.stopWait(t)
	})
	step("i-run-during-an-object-store-outage", func(t *testing.T) {
		k.kubectlMust(t, "scale", "deployment/minio", "--replicas=0")
		k.kubectlMust(t, "wait", "--for=delete", "pod", "-l", "app=minio", "--timeout=3m")
		log, out, err := k.runJob(t, newValues, digest)
		if err == nil {
			t.Fatalf("the run succeeded during the outage:\n%s\n%s", out, log)
		}
		k.assertRunRefused(t, log)
		if !regexp.MustCompile(`(?m)^plan: .* class=body_unavailable`).MatchString(log) {
			t.Errorf("the refused run prints no body_unavailable plan: line:\n%s", log)
		}
		if got := k.kubectlOut("get", "deployment", liveFullname, "-o", "jsonpath={.spec.replicas}"); got != "0" {
			t.Fatalf("the Deployment has %s replicas after a failed run; want 0", got)
		}
		k.assertRegistryPods(t, 0, "")
	})
	step("k-rerun-after-the-outage", func(t *testing.T) {
		k.kubectlMust(t, "scale", "deployment/minio", "--replicas=1")
		k.kubectlMust(t, "rollout", "status", "deployment/minio", "--timeout=5m")
		// The rollout can finish before the Service routes to the new pod,
		// and a dry run started then holds the object-held row back as
		// body_unavailable. mc retries until MinIO answers through the Service.
		k.mc(t, "mc ls m/podium >/dev/null")
		_, _, digest = k.dryRun(t, newValues)
		log, out, err := k.runJob(t, newValues, digest)
		if err != nil {
			t.Fatalf("the reviewed run failed: %v\n%s\n%s", err, out, log)
		}
		wantIn(t, out, "--plan-digest="+digest, "run.log")
		wantIn(t, log, "rehash: 0 unsigned left")
		if !k.recordPresent(t) {
			t.Fatal("the run left the completion record unset")
		}
		if total, unsigned := k.rowCounts(t); total == 0 || unsigned != 0 {
			t.Fatalf("after the run the store holds %d row(s), %d unsigned; want every row signed", total, unsigned)
		}
	})
	step("l-serve", func(t *testing.T) {
		k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--wait", "--timeout", "10m")
		pods := k.registryPods(t)
		if len(pods) != 2 {
			t.Fatalf("%d registry pods after the serving step; want 2", len(pods))
		}
		for _, p := range pods {
			if !p.ready || p.restarts != 0 {
				t.Errorf("pod %s ready=%t restarts=%d; want Ready with 0 restarts", p.name, p.ready, p.restarts)
			}
			if log := k.kubectlOut("logs", "pod/"+p.name); strings.Contains(log, " rewritten, ") {
				t.Errorf("pod %s ran the rewrite at boot:\n%s", p.name, log)
			}
		}
		k.verifyArtifacts(t, "demo/hello/greet", "demo/hello/bigref")
	})
	// A rerun over the migrated store rewrites no content hash, so it runs on
	// a serving pod: a dry run, then a run bound to its plan digest.
	step("l2-migration-after-serving", func(t *testing.T) {
		plan := k.kubectlMust(t, "exec", "deployment/"+liveFullname, "--",
			"/usr/local/bin/podium-server", "sign-stored-rows", "--include-unsigned", "--dry-run")
		if strings.Contains(plan, "signed_by=unsigned") {
			t.Fatalf("the rerun's dry run lists an unsigned row over the migrated store:\n%s", plan)
		}
		rerun := k.kubectlMust(t, "exec", "deployment/"+liveFullname, "--",
			"/usr/local/bin/podium-server", "sign-stored-rows", "--include-unsigned", "--plan-digest="+planDigestOf(t, plan))
		wantIn(t, rerun, "rehash: 0 rewritten, ")
		if total, unsigned := k.rowCounts(t); total == 0 || unsigned != 0 {
			t.Fatalf("after the rerun the store holds %d row(s), %d unsigned; want every row signed", total, unsigned)
		}
	})
	step("m-later-upgrade", func(t *testing.T) {
		k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "image.tag="+liveNext, "--wait", "--timeout", "10m")
		if hooks := strings.TrimSpace(k.helmOK(t, "get", "hooks", liveRelease)); hooks != "" {
			t.Fatalf("a later upgrade rendered a hook:\n%s", hooks)
		}
		k.assertRegistryPods(t, 2, liveRepo+":"+liveNext)
	})
	step("n-rollback-recovery", func(t *testing.T) {
		k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "replicaCount=0")
		k.stopWait(t)
		k.restore(t)
		k.helmOK(t, "rollback", liveRelease, v040Revision, "--wait", "--timeout", "10m")
		k.assertRegistryPods(t, 2, liveRepo+":"+liveOld)
		before := k.storeSnapshot(t)
		k.helmFail(t, "upgrade", liveRelease, k.chart(), "-f", newValues, "--set", "replicaCount=2",
			"--wait", "--timeout", "5m")
		k.assertBootRefused(t, before)
		k.assertReadyOn(t, 2, liveRepo+":"+liveOld)
	})
	// An install over an existing store runs the migrate Job as a
	// post-install hook, so the procedure needs no install-specific step.
	step("n2-reinstall-over-the-restored-store", func(t *testing.T) {
		k.helmOK(t, "uninstall", liveRelease, "--wait", "--timeout", "10m")
		k.stopWait(t)
		log, out, err := k.migrationJob(t, "install", newValues, "--set", "migration.mode=dry-run")
		if err != nil {
			t.Fatalf("the dry-run install failed: %v\n%s\n%s", err, out, log)
		}
		planDigestOf(t, log)
		if k.recordPresent(t) {
			t.Fatal("the post-install dry run wrote the completion record")
		}
		k.kubectlMust(t, "delete", "job", "-l", liveCleanup)
		if got := k.kubectlOut("get", "job", "-l", liveCleanup, "-o", "name"); got != "" {
			t.Fatalf("the cleanup selector left %s", got)
		}
	})
}

// signingOffUpgrade walks the signing-off procedure, case (o): the same Job
// path as signing on, with includeUnsigned false. A serving render before the
// run is refused at boot, which is the backstop for a skipped run step.
func signingOffUpgrade(t *testing.T, env *liveEnv, ns string) {
	k := newCluster(t, env, ns)
	k.backingServices()
	k.secrets(false)
	k.seed()
	k.installV040(nil)
	values := k.valuesFile("off", map[string]any{
		"image":   map[string]any{"repository": liveRepo, "tag": liveCurrent},
		"signing": map[string]any{"mode": "none"},
		"migration": map[string]any{
			"includeUnsigned": false,
			"previousImage":   liveRepo + ":" + liveOld,
		},
	})

	k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", values, "--set", "replicaCount=0")
	k.stopWait(t)
	log, notes, digest := k.dryRun(t, values)
	wantIn(t, log, "signing_key=-")
	if strings.Contains(log, "sign=true") {
		t.Errorf("a signing-off dry run plans a signed write:\n%s", log)
	}
	wantIn(t, notes, "signed_by=unchecked")
	if strings.Contains(notes, "signature_unverified") {
		t.Errorf("the signing-off dry-run NOTES name signature_unverified:\n%s", notes)
	}

	before := k.storeSnapshot(t)
	k.helmFail(t, "upgrade", liveRelease, k.chart(), "-f", values, "--wait", "--timeout", "5m")
	k.assertBootRefused(t, before)
	k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", values, "--set", "replicaCount=0")
	k.stopWait(t)

	if log, out, err := k.runJob(t, values, digest); err != nil {
		t.Fatalf("the signing-off run failed: %v\n%s\n%s", err, out, log)
	}
	if !k.recordPresent(t) {
		t.Fatal("the signing-off run left the completion record unset")
	}

	k.helmOK(t, "upgrade", liveRelease, k.chart(), "-f", values, "--wait", "--timeout", "10m")
	pods := k.registryPods(t)
	if len(pods) != 2 {
		t.Fatalf("%d registry pods after the signing-off serving step; want 2", len(pods))
	}
	for _, p := range pods {
		if !p.ready || p.restarts != 0 {
			t.Errorf("pod %s ready=%t restarts=%d; want Ready with 0 restarts", p.name, p.ready, p.restarts)
		}
		if log := k.kubectlOut("logs", "pod/"+p.name); strings.Contains(log, " rewritten, ") {
			t.Errorf("pod %s ran the rewrite at boot:\n%s", p.name, log)
		}
	}
	probes := k.kubectlOut("get", "deployment", liveFullname, "-o",
		"jsonpath={.spec.template.spec.containers[0].startupProbe.httpGet.path} {.spec.template.spec.containers[0].livenessProbe.httpGet.path}")
	if probes != "/healthz /healthz" {
		t.Fatalf("the signing-off Deployment's startup and liveness probes are %q; want /healthz /healthz", probes)
	}
}

// freshInstall covers case (p): a serving install over an empty store, whose
// first start records completion, and the cleanup selector after an
// uninstall.
func freshInstall(t *testing.T, env *liveEnv, ns string) {
	k := newCluster(t, env, ns)
	k.backingServices()
	k.secrets(true)
	k.helmOK(t, "install", liveRelease, k.chart(),
		"--set", "image.repository="+liveRepo, "--set", "image.tag="+liveCurrent,
		"--set", "signing.secretName=podium-signing-key", "--set", "config.identityProvider.type=",
		"--set", "replicaCount=1", "--wait", "--timeout", "10m")
	if got := k.kubectlOut("get", "job", "-o", "name"); got != "" {
		t.Fatalf("a fresh install rendered a Job: %s", got)
	}
	k.assertRegistryPods(t, 1, liveRepo+":"+liveCurrent)
	k.helmOK(t, "uninstall", liveRelease, "--wait")
	k.kubectlMust(t, "delete", "job", "-l", liveCleanup)
}

// cluster runs commands against one namespace of the kind cluster.
type cluster struct {
	t   *testing.T
	env *liveEnv
	ns  string
	dir string
}

func newCluster(t *testing.T, env *liveEnv, ns string) *cluster {
	t.Helper()
	dir := filepath.Join(env.work, ns)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	k := &cluster{t: t, env: env, ns: ns, dir: dir}
	mustRun(t, dir, time.Minute, "kubectl", "--context", kindContext, "create", "namespace", ns)
	return k
}

func (k *cluster) chart() string { return filepath.Join(k.env.root, "deploy", "helm", "podium") }

func (k *cluster) kubectlArgs(args ...string) []string {
	return append([]string{"--context", kindContext, "-n", k.ns}, args...)
}

func (k *cluster) helmArgs(args ...string) []string {
	return append(args, "--kube-context", kindContext, "-n", k.ns)
}

// kubectlOut runs kubectl and returns its trimmed output, or "" on an error.
func (k *cluster) kubectlOut(args ...string) string {
	out, err := run(k.dir, 5*time.Minute, "kubectl", k.kubectlArgs(args...)...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (k *cluster) kubectlMust(t *testing.T, args ...string) string {
	t.Helper()
	return mustRun(t, k.dir, 15*time.Minute, "kubectl", k.kubectlArgs(args...)...)
}

func (k *cluster) helmOK(t *testing.T, args ...string) string {
	t.Helper()
	return mustRun(t, k.dir, 60*time.Minute, "helm", k.helmArgs(args...)...)
}

// lastRevision returns the release's current revision number, as the
// REVISION column of `helm history` prints it.
func (k *cluster) lastRevision(t *testing.T) string {
	t.Helper()
	var history []struct {
		Revision int `json:"revision"`
	}
	out := k.helmOK(t, "history", liveRelease, "-o", "json")
	// The output is combined with stderr, so a Helm warning line can precede
	// the JSON array.
	raw := out
	if loc := regexp.MustCompile(`(?m)^\[`).FindStringIndex(raw); loc != nil {
		raw = raw[loc[0]:]
	}
	if err := json.Unmarshal([]byte(raw), &history); err != nil || len(history) == 0 {
		t.Fatalf("parse helm history: %v\n%s", err, out)
	}
	return fmt.Sprint(history[len(history)-1].Revision)
}

// helmFail runs helm, requires a non-zero exit, and returns the output.
func (k *cluster) helmFail(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(k.dir, 60*time.Minute, "helm", k.helmArgs(args...)...)
	if err == nil {
		t.Fatalf("helm %v succeeded; want a refusal\n%s", args, out)
	}
	return out
}

// apply applies a manifest server-side. A client-side apply copies the whole
// object into the last-applied-configuration annotation, and the seed
// ConfigMap's body exceeds the 256 KiB annotation limit even though it fits
// the 1 MiB object limit.
func (k *cluster) apply(t *testing.T, manifest string) {
	t.Helper()
	path := filepath.Join(k.dir, fmt.Sprintf("manifest-%d.yaml", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	k.kubectlMust(t, "apply", "--server-side", "-f", path)
}

func (k *cluster) waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// backingServices deploys Postgres and a MinIO whose data survives a scale to
// zero, and creates the bucket, as S46 does.
func (k *cluster) backingServices() {
	t := k.t
	t.Helper()
	k.apply(t, fmt.Sprintf(backingManifest, k.ns, pgImage, minioImage))
	k.kubectlMust(t, "rollout", "status", "deployment/pg", "--timeout=5m")
	k.kubectlMust(t, "rollout", "status", "deployment/minio", "--timeout=5m")
	k.waitFor(t, 2*time.Minute, "Postgres to accept connections", func() bool {
		return k.kubectlOut("exec", "deploy/pg", "--", "pg_isready", "-U", "podium", "-d", "podium") != ""
	})
	k.mc(t, "mc mb -p m/podium")
}

// mc runs an mc script against the namespace's MinIO. It retries the alias
// until MinIO answers, because a Deployment's rollout can finish before its
// Service routes to the pod, and the first connection is then refused.
func (k *cluster) mc(t *testing.T, script string) string {
	t.Helper()
	name := fmt.Sprintf("mc-%d", time.Now().UnixNano())
	return k.kubectlMust(t, "run", name, "--image="+mcImage, "--restart=Never", "--rm", "-i", "--quiet",
		"--command", "--", "sh", "-c", "for i in $(seq 1 30); do mc alias set m http://minio:9000 minioadmin minioadmin >/dev/null 2>&1 && break; sleep 2; done && "+script)
}

// secrets creates the configuration Secret and, when signing, the signing
// key Secret.
func (k *cluster) secrets(signing bool) {
	t := k.t
	t.Helper()
	k.kubectlMust(t, "create", "secret", "generic", "podium-secrets",
		"--from-literal=PODIUM_POSTGRES_DSN=postgres://podium:podium@pg:5432/podium?sslmode=disable",
		"--from-literal=PODIUM_S3_BUCKET=podium",
		"--from-literal=PODIUM_S3_ENDPOINT=http://minio:9000",
		"--from-literal=PODIUM_S3_REGION=us-east-1",
		"--from-literal=AWS_ACCESS_KEY_ID=minioadmin",
		"--from-literal=AWS_SECRET_ACCESS_KEY=minioadmin")
	if signing {
		k.kubectlMust(t, "create", "secret", "generic", "podium-signing-key",
			"--from-file=registry-signing.key="+k.env.keyFile)
	}
}

// seed writes v0.4.0 rows through a standalone v0.4.0 pod that ingests a
// layer from a ConfigMap, including a resource above the inline cutoff so its
// body lands in MinIO. The v0.4.0 chart mounts no extra volume, so it cannot
// seed a layer itself.
func (k *cluster) seed() {
	t := k.t
	t.Helper()
	k.apply(t, seedConfigMap(t, k.ns))
	k.apply(t, fmt.Sprintf(seedPodManifest, k.ns, liveRepo+":"+liveOld))
	k.kubectlMust(t, "wait", "--for=condition=Ready", "pod/seed", "--timeout=5m")
	k.waitFor(t, 3*time.Minute, "the seed pod to ingest its layer", func() bool {
		total, _ := k.rowCounts(t)
		return total >= 2
	})
	k.kubectlMust(t, "delete", "pod", "seed", "--wait=true")

	total, unsigned := k.rowCounts(t)
	if total < 2 || unsigned < 1 {
		t.Fatalf("the seeded store holds %d row(s), %d unsigned; want at least 2 rows and 1 unsigned", total, unsigned)
	}
	if k.recordPresent(t) {
		t.Fatal("the seeded store already carries the completion record")
	}
	if objects := strings.TrimSpace(k.mc(t, "mc ls --recursive m/podium")); objects == "" {
		t.Fatal("the seed stored no object in MinIO, so no row is object-held")
	}
}

// installV040 installs the v0.4.0 chart from the worktree with the v0.4.0
// image and waits for it to serve.
func (k *cluster) installV040(extra []string) {
	t := k.t
	t.Helper()
	args := []string{"install", liveRelease, filepath.Join(k.env.worktree, "deploy", "helm", "podium"),
		"--set", "image.repository=" + liveRepo, "--set", "image.tag=" + liveOld,
		"--set", "replicaCount=2", "--set", "config.identityProvider.type=",
		"--wait", "--timeout", "10m"}
	k.helmOK(t, append(args, extra...)...)
	k.assertRegistryPods(t, 2, liveRepo+":"+liveOld)
}

// valuesFile writes step 1's values file: the release's own values merged
// with the overrides, as `helm get values` followed by an edit produces it.
func (k *cluster) valuesFile(name string, overrides map[string]any) string {
	t := k.t
	t.Helper()
	var values map[string]any
	if err := yaml.Unmarshal([]byte(k.helmOK(t, "get", "values", liveRelease, "-o", "yaml")), &values); err != nil {
		t.Fatalf("decode helm get values: %v", err)
	}
	if values == nil {
		values = map[string]any{}
	}
	for key, v := range overrides {
		sub, _ := values[key].(map[string]any)
		if sub == nil {
			sub = map[string]any{}
		}
		for sk, sv := range v.(map[string]any) {
			sub[sk] = sv
		}
		values[key] = sub
	}
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(k.dir, name+"-values.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stopWait runs step 2's wait for every registry pod to be deleted.
func (k *cluster) stopWait(t *testing.T) {
	t.Helper()
	k.kubectlMust(t, "wait", "--for=delete", "pod", "--timeout=10m", "-l", liveSelector,
		"--field-selector=status.phase!=Succeeded,status.phase!=Failed")
}

// migrationJob runs a helm install or upgrade that renders the migrate Job at
// zero replicas and streams the Job's log to the caller while the Job runs,
// as steps 3 and 4 of the procedure stream it to dry-run.log and run.log. A
// `kubectl logs` read after the Job ends is truncated on a large store. It
// returns the log, Helm's output, and Helm's error.
func (k *cluster) migrationJob(t *testing.T, verb, values string, sets ...string) (log, helmOut string, err error) {
	t.Helper()
	k.kubectlMust(t, "delete", "job", liveJob, "--ignore-not-found", "--wait=true")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	args := append([]string{verb, liveRelease, k.chart(), "-f", values, "--set", "replicaCount=0"}, sets...)
	helm := exec.CommandContext(ctx, "helm", k.helmArgs(append(args, "--timeout", "30m")...)...)
	var out strings.Builder
	helm.Stdout, helm.Stderr = &out, &out
	if err := helm.Start(); err != nil {
		t.Fatal(err)
	}
	k.waitFor(t, 10*time.Minute, "the migrate Job", func() bool {
		return k.kubectlOut("get", "job", liveJob, "-o", "name") != ""
	})
	log = k.kubectlMust(t, "logs", "-f", "job/"+liveJob, "--pod-running-timeout=10m")
	err = helm.Wait()
	return log, out.String(), err
}

// dryRun runs step 3 and checks that the capture is complete: the row lines
// match the planned count, the rows the digest covers match the digest line's
// count, and the log ends with the digest line. It returns the log, the
// NOTES, and the plan digest.
func (k *cluster) dryRun(t *testing.T, values string) (log, notes, digest string) {
	t.Helper()
	log, notes, err := k.migrationJob(t, "upgrade", values, "--set", "migration.mode=dry-run")
	if err != nil {
		t.Fatalf("the dry-run upgrade failed: %v\n%s\n%s", err, notes, log)
	}
	planned := regexp.MustCompile(`(?m)^dry-run: (\d+) row\(s\) planned`).FindStringSubmatch(log)
	rows := len(regexp.MustCompile(`(?m)^dry-run: .* class=`).FindAllString(log, -1))
	if planned == nil || planned[1] != fmt.Sprint(rows) {
		t.Fatalf("the capture is incomplete: %d row line(s) for %v planned\n%s", rows, planned, log)
	}
	if strings.Contains(log, "class=body_unavailable") {
		t.Fatalf("the dry run reports a body_unavailable row:\n%s", log)
	}
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "dry-run: plan digest ") {
		t.Fatalf("the dry-run log ends with %q; want the plan digest line\n%s", last, log)
	}
	return log, notes, planDigestOf(t, log)
}

// planDigestLine matches the dry run's digest line.
var planDigestLine = regexp.MustCompile(`(?m)^dry-run: plan digest (sha256:[0-9a-f]{64}) over (\d+) row\(s\)$`)

// planDigestOf returns the plan digest of a dry-run log, and fails when the
// digest line is absent or its row count differs from the row lines the
// digest covers, which are those without class=migrated.
func planDigestOf(t *testing.T, log string) string {
	t.Helper()
	m := planDigestLine.FindStringSubmatch(log)
	if m == nil {
		t.Fatalf("the dry-run log carries no plan digest line:\n%s", log)
	}
	covered := 0
	for _, line := range regexp.MustCompile(`(?m)^dry-run: .* class=\S+`).FindAllString(log, -1) {
		if !strings.Contains(line, " class=migrated ") {
			covered++
		}
	}
	if m[2] != fmt.Sprint(covered) {
		t.Fatalf("the digest covers %s row(s), and the log lists %d row(s) outside class=migrated\n%s", m[2], covered, log)
	}
	return m[1]
}

// runJob runs step 4, the run bound to the reviewed plan digest.
func (k *cluster) runJob(t *testing.T, values, digest string) (log, helmOut string, err error) {
	t.Helper()
	return k.migrationJob(t, "upgrade", values, "--set", "migration.mode=run", "--set", "migration.planDigest="+digest)
}

// assertRunRefused requires a failed run Job whose container exited with the
// changed-plan status 3, a log naming the changed plan, and no completion
// record.
func (k *cluster) assertRunRefused(t *testing.T, log string) {
	t.Helper()
	if got := k.kubectlOut("get", "job", liveJob, "-o", "jsonpath={.status.failed}"); got == "" || got == "0" {
		t.Fatalf("the run Job did not fail (status.failed=%q)", got)
	}
	code := k.kubectlOut("get", "pods", "-l", "job-name="+liveJob, "-o",
		"jsonpath={.items[0].status.containerStatuses[0].state.terminated.exitCode}")
	if code != "3" {
		t.Errorf("the run Job's container exited with %q; want 3", code)
	}
	wantIn(t, log, "plan changed")
	if k.recordPresent(t) {
		t.Fatal("a refused run wrote the completion record")
	}
}

// plantedVersion is the version of the unsigned row step g3 plants.
const plantedVersion = "9.9.9-planted"

// plantUnsignedRow copies the greet row under plantedVersion with no
// signature, as an unsigned ingest between the dry run and the run would.
func (k *cluster) plantUnsignedRow(t *testing.T) {
	t.Helper()
	before, _ := k.rowCounts(t)
	k.psql(t, fmt.Sprintf(`DO $$
DECLARE s text;
BEGIN
  FOR s IN SELECT table_schema FROM information_schema.tables WHERE table_name = 'manifests' LOOP
    EXECUTE format('CREATE TEMP TABLE planted AS SELECT * FROM %%I.manifests WHERE artifact_id = %%L LIMIT 1', s, 'demo/hello/greet');
    EXECUTE format('UPDATE planted SET version = %%L, signature = %%L', '%s', '');
    EXECUTE format('INSERT INTO %%I.manifests SELECT * FROM planted', s);
    DROP TABLE planted;
  END LOOP;
END $$;
`, plantedVersion))
	if after, _ := k.rowCounts(t); after != before+1 {
		t.Fatalf("planting a row moved the row count from %d to %d; want one more", before, after)
	}
}

// storeSnapshot returns an md5 over every manifest row's key, content hash,
// and signature in key order, across every schema that holds a manifests
// table.
func (k *cluster) storeSnapshot(t *testing.T) string {
	t.Helper()
	return k.psql(t, `SELECT 'SELECT md5(coalesce(string_agg(concat_ws(''|'', tenant_id, artifact_id, version, content_hash, signature), E''\n'' ORDER BY tenant_id, artifact_id, version), '''')) FROM ('
  || string_agg(format('SELECT tenant_id, artifact_id, version, content_hash, signature FROM %I.manifests', table_schema), ' UNION ALL ')
  || ') rows'
  FROM information_schema.tables WHERE table_name = 'manifests' \gexec
`)
}

// assertBootRefused requires a registry pod on this release's image that has
// restarted or terminated with a non-zero status, whose previous container
// log names sign-stored-rows, with the completion record still absent and the
// manifest rows unchanged from before.
func (k *cluster) assertBootRefused(t *testing.T, before string) {
	t.Helper()
	image := liveRepo + ":" + liveCurrent
	var refused string
	k.waitFor(t, 5*time.Minute, "a refused registry pod on "+image, func() bool {
		out := k.kubectlOut("get", "pods", "-l", liveSelector, "-o",
			`jsonpath={range .items[*]}{.metadata.name}={.spec.containers[0].image}={.status.containerStatuses[0].lastState.terminated.exitCode}{.status.containerStatuses[0].state.terminated.exitCode}{"\n"}{end}`)
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Split(line, "=")
			if len(parts) != 3 || parts[1] != image || parts[2] == "" || parts[2] == "0" {
				continue
			}
			if strings.Contains(k.kubectlOut("logs", "--previous", "pod/"+parts[0]), "sign-stored-rows") ||
				strings.Contains(k.kubectlOut("logs", "pod/"+parts[0]), "sign-stored-rows") {
				refused = parts[0]
				return true
			}
		}
		return false
	})
	t.Logf("pod %s refused to start over the unmigrated store", refused)
	if k.recordPresent(t) {
		t.Fatal("a refused start wrote the completion record")
	}
	if after := k.storeSnapshot(t); after != before {
		t.Fatalf("a refused start changed the manifest rows: snapshot %s, want %s", after, before)
	}
}

// assertReadyOn requires n Ready registry pods on image, whatever other pods
// the release runs.
func (k *cluster) assertReadyOn(t *testing.T, n int, image string) {
	t.Helper()
	k.waitFor(t, 10*time.Minute, fmt.Sprintf("%d Ready registry pod(s) on %q", n, image), func() bool {
		ready := 0
		for _, p := range k.registryPods(t) {
			if p.image == image && p.ready {
				ready++
			}
		}
		return ready == n
	})
}

// livePod is the subset of a registry pod the assertions read.
type livePod struct {
	name     string
	image    string
	phase    string
	ready    bool
	restarts int
}

// registryPods lists the registry pods of the release that are not in a
// terminal phase.
func (k *cluster) registryPods(t *testing.T) []livePod {
	t.Helper()
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(k.kubectlMust(t, "get", "pods", "-l", liveSelector, "-o", "json")), &list); err != nil {
		t.Fatalf("decode pods: %v", err)
	}
	var pods []livePod
	for _, it := range list.Items {
		if it.Status.Phase == "Succeeded" || it.Status.Phase == "Failed" {
			continue
		}
		p := livePod{name: it.Metadata.Name, phase: it.Status.Phase}
		if len(it.Spec.Containers) > 0 {
			p.image = it.Spec.Containers[0].Image
		}
		if len(it.Status.ContainerStatuses) > 0 {
			p.ready = it.Status.ContainerStatuses[0].Ready
			p.restarts = it.Status.ContainerStatuses[0].RestartCount
		}
		pods = append(pods, p)
	}
	return pods
}

// assertRegistryPods requires n live registry pods, each on image when image
// is set.
func (k *cluster) assertRegistryPods(t *testing.T, n int, image string) {
	t.Helper()
	k.waitFor(t, 10*time.Minute, fmt.Sprintf("%d registry pod(s) on %q", n, image), func() bool {
		pods := k.registryPods(t)
		if len(pods) != n {
			return false
		}
		for _, p := range pods {
			if image != "" && (p.image != image || !p.ready) {
				return false
			}
		}
		return true
	})
}

// psql runs a script against the namespace's database and returns its
// unaligned output.
func (k *cluster) psql(t *testing.T, script string) string {
	t.Helper()
	cmd := exec.Command("kubectl", k.kubectlArgs("exec", "-i", "deploy/pg", "--",
		"psql", "-U", "podium", "-d", "podium", "-At", "-v", "ON_ERROR_STOP=1")...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("psql: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// rowCounts returns the manifest rows across every org schema and how many of
// them carry no signature.
func (k *cluster) rowCounts(t *testing.T) (total, unsigned int) {
	t.Helper()
	out := k.psql(t, `SELECT format('SELECT count(*), count(*) FILTER (WHERE signature = %L) FROM %I.manifests', '', table_schema)
  FROM information_schema.tables WHERE table_name = 'manifests' \gexec
`)
	for _, line := range strings.Split(out, "\n") {
		var n, u int
		if _, err := fmt.Sscanf(line, "%d|%d", &n, &u); err == nil {
			total += n
			unsigned += u
		}
	}
	return total, unsigned
}

// recordPresent reports whether the store records the completed rewrite.
func (k *cluster) recordPresent(t *testing.T) bool {
	t.Helper()
	out := k.psql(t, fmt.Sprintf(`SELECT 'SELECT count(*) FROM public.data_migrations WHERE name = ''%s'''
  WHERE to_regclass('public.data_migrations') IS NOT NULL \gexec
`, liveRecord))
	return strings.TrimSpace(out) == "1"
}

// backup takes step 2's backup of the database and the bucket.
func (k *cluster) backup(t *testing.T) {
	t.Helper()
	k.kubectlMust(t, "exec", "deploy/pg", "--", "pg_dump", "-U", "podium", "-d", "podium", "-Fc", "-f", "/tmp/step2.dump")
	k.mc(t, "mc mb -p m/podium-backup && mc mirror --overwrite m/podium m/podium-backup")
}

// restore returns the database and the bucket to step 2's backup.
func (k *cluster) restore(t *testing.T) {
	t.Helper()
	k.kubectlMust(t, "exec", "deploy/pg", "--", "psql", "-U", "podium", "-d", "postgres", "-c", "DROP DATABASE podium WITH (FORCE)")
	k.kubectlMust(t, "exec", "deploy/pg", "--", "createdb", "-U", "podium", "podium")
	k.kubectlMust(t, "exec", "deploy/pg", "--", "pg_restore", "-U", "podium", "-d", "podium", "/tmp/step2.dump")
	k.mc(t, "mc mirror --overwrite --remove m/podium-backup m/podium")
	if k.recordPresent(t) {
		t.Fatal("the restored store carries the completion record")
	}
}

// verifyArtifacts fetches each artifact through a port-forward and verifies
// the delivery signature under the deployment's public key.
func (k *cluster) verifyArtifacts(t *testing.T, ids ...string) {
	t.Helper()
	port := "18180"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pf := exec.CommandContext(ctx, "kubectl", k.kubectlArgs("port-forward", "svc/"+liveFullname, port+":8080")...)
	stdout, err := pf.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := pf.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = pf.Wait() }()
	if !bufio.NewScanner(stdout).Scan() {
		t.Fatal("the port-forward did not start")
	}
	for _, id := range ids {
		cmd := exec.Command(k.env.podium, "verify", id, "--registry", "http://127.0.0.1:"+port)
		cmd.Env = append(os.Environ(),
			"PODIUM_SIGNATURE_VERIFY_KEY="+k.env.pubKey,
			"PODIUM_VERIFY_SIGNATURES=always",
			"HOME="+k.dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("podium verify %s: %v\n%s", id, err, out)
		}
	}
}

// wantIn requires every want in out.
func wantIn(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("the output does not carry %q:\n%s", w, out)
		}
	}
}

// run executes a command in dir with a deadline and returns its combined
// output.
func run(dir string, d time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(t *testing.T, dir string, d time.Duration, name string, args ...string) string {
	t.Helper()
	out, err := run(dir, d, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// backingManifest deploys Postgres and MinIO. MinIO keeps its data on a
// claim, because case (i) scales it to zero and case (k) needs the bodies
// back.
const backingManifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: pg, namespace: %[1]s}
spec:
  selector: {matchLabels: {app: pg}}
  template:
    metadata: {labels: {app: pg}}
    spec:
      containers:
        - name: pg
          image: %[2]s
          env:
            - {name: POSTGRES_USER, value: podium}
            - {name: POSTGRES_PASSWORD, value: podium}
            - {name: POSTGRES_DB, value: podium}
            - {name: PGDATA, value: /tmp/pgdata}
          ports: [{containerPort: 5432}]
---
apiVersion: v1
kind: Service
metadata: {name: pg, namespace: %[1]s}
spec:
  selector: {app: pg}
  ports: [{port: 5432}]
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: minio-data, namespace: %[1]s}
spec:
  accessModes: [ReadWriteOnce]
  resources: {requests: {storage: 1Gi}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: minio, namespace: %[1]s}
spec:
  strategy: {type: Recreate}
  selector: {matchLabels: {app: minio}}
  template:
    metadata: {labels: {app: minio}}
    spec:
      containers:
        - name: minio
          image: %[3]s
          args: [server, /data]
          env:
            - {name: MINIO_ROOT_USER, value: minioadmin}
            - {name: MINIO_ROOT_PASSWORD, value: minioadmin}
          ports: [{containerPort: 9000}]
          readinessProbe: {httpGet: {path: /minio/health/ready, port: 9000}}
          volumeMounts: [{name: data, mountPath: /data}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: minio-data}}]
---
apiVersion: v1
kind: Service
metadata: {name: minio, namespace: %[1]s}
spec:
  selector: {app: minio}
  ports: [{port: 9000}]
`

// seedPodManifest runs v0.4.0 as a standalone process outside any chart, with
// the ConfigMap layer mounted read-only and no identity provider. Each key is
// a subPath mount: a projected ConfigMap volume exposes its files through
// ..data symlinks, and v0.4.0's filesystem layer ingests none of them. The
// vector backend and embedding provider are off because their defaults
// require an OpenAI key the seed does not have.
const seedPodManifest = `apiVersion: v1
kind: Pod
metadata: {name: seed, namespace: %[1]s}
spec:
  restartPolicy: Never
  containers:
    - name: seed
      image: %[2]s
      imagePullPolicy: IfNotPresent
      envFrom: [{secretRef: {name: podium-secrets}}]
      env:
        - {name: PODIUM_LAYER_PATH, value: /layer}
        - {name: PODIUM_IDENTITY_PROVIDER, value: ""}
        - {name: PODIUM_BIND, value: "0.0.0.0:8080"}
        - {name: PODIUM_REGISTRY_STORE, value: postgres}
        - {name: PODIUM_OBJECT_STORE, value: s3}
        - {name: PODIUM_VECTOR_BACKEND, value: none}
        - {name: PODIUM_EMBEDDING_PROVIDER, value: none}
        - {name: HOME, value: /tmp}
      readinessProbe: {httpGet: {path: /healthz, port: 8080}, periodSeconds: 2}
      volumeMounts:
        - {name: layer, mountPath: /layer/demo/hello/greet/SKILL.md, subPath: greet-skill, readOnly: true}
        - {name: layer, mountPath: /layer/demo/hello/greet/ARTIFACT.md, subPath: greet-artifact, readOnly: true}
        - {name: layer, mountPath: /layer/demo/hello/bigref/SKILL.md, subPath: bigref-skill, readOnly: true}
        - {name: layer, mountPath: /layer/demo/hello/bigref/ARTIFACT.md, subPath: bigref-artifact, readOnly: true}
        - {name: layer, mountPath: /layer/demo/hello/bigref/references/big.md, subPath: bigref-body, readOnly: true}
        - {name: tmp, mountPath: /tmp}
  volumes:
    - name: tmp
      emptyDir: {}
    - name: layer
      configMap:
        name: seed-layer
`

// failedPodManifest is a pod that carries the registry's selector labels and
// ends in phase Failed, as an evicted pod does.
const failedPodManifest = `apiVersion: v1
kind: Pod
metadata:
  name: evicted-look-alike
  namespace: %[1]s
  labels: {app.kubernetes.io/name: podium, app.kubernetes.io/instance: podium}
spec:
  restartPolicy: Never
  containers:
    - {name: fail, image: %[2]s, imagePullPolicy: IfNotPresent, command: [sh, -c, "exit 1"]}
`

// seedConfigMap builds the layer: a small skill and a skill whose bundled
// resource exceeds the inline cutoff and stays below the 1 MiB ConfigMap
// limit. The body is larger than the 256 KiB annotation limit, which is why
// apply is server-side.
func seedConfigMap(t *testing.T, ns string) string {
	t.Helper()
	skill := func(name string) string {
		return fmt.Sprintf("---\nname: %s\ndescription: A seeded skill for the chart upgrade test.\n---\n\nSeeded by the kind upgrade test.\n", name)
	}
	artifact := "---\ntype: skill\nversion: 1.0.0\nwhen_to_use:\n  - \"When the upgrade test runs.\"\nsensitivity: low\n---\n\n<!-- Skill body lives in SKILL.md. -->\n"
	body := strings.Repeat("The chart upgrade test stores this line in object storage.\n", 6000)
	cm := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "seed-layer", "namespace": ns},
		"data": map[string]string{
			"greet-skill":     skill("greet"),
			"greet-artifact":  artifact,
			"bigref-skill":    skill("bigref"),
			"bigref-artifact": artifact,
			"bigref-body":     body,
		},
	}
	raw, err := json.Marshal(cm)
	if err != nil {
		t.Fatalf("encode the seed ConfigMap: %v", err)
	}
	return string(raw)
}
