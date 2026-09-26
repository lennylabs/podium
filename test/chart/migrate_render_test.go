// Render tests for the §13.4 migrate Job and the render guards around it.
//
// The first start of a release that migrates a stored value rewrites every
// stored row before it serves, and the rewrite requires every registry process
// on the previous release stopped. The chart runs the rewrite as a post-upgrade
// hook Job while the Deployment is held at zero replicas, so no probe kills the
// pass and no previous-version pod runs beside it. These tests pin what a
// `helm template` render can check: the Job's arguments and record, its parity
// with the Deployment's pod, and every pure-value refusal. The lookup-based
// gates read a live cluster, so their pass and fail branches run in the kind
// test (kind_upgrade_test.go); TestChart_LiveGatesFailClosedOffline pins the
// offline branch.
package chart

import (
	"reflect"
	"strings"
	"testing"
)

// Keys of the record the migrate Job carries and the gates read back.
const (
	annMode            = "podium.lennylabs.dev/migration-mode"
	annFormat          = "podium.lennylabs.dev/stored-row-format"
	annImage           = "podium.lennylabs.dev/image"
	annIncludeUnsigned = "podium.lennylabs.dev/include-unsigned"
	annRevision        = "podium.lennylabs.dev/release-revision"
	annReviewed        = "podium.lennylabs.dev/reviewed-dry-run"
	annPreImage        = "podium.lennylabs.dev/pre-migration-image"
	annDeploymentUID   = "podium.lennylabs.dev/deployment-uid"
	annPodConfig       = "podium.lennylabs.dev/pod-config"
	storedRowFormat    = "content_hash_framing"
	migrateContainer   = "sign-stored-rows"
)

// upgradeArgs builds `helm template` arguments for a migration render: an
// upgrade, the signing Secret, zero replicas, and the lookup gates off, so the
// render reaches the Job body. A test that asserts a gate builds its own.
func upgradeArgs(mode string, sets ...string) []string {
	args := []string{"--is-upgrade",
		"--set", withSigningKey,
		"--set", "replicaCount=0",
		"--set", "migration.preflight=false",
		"--set", "migration.mode=" + mode,
	}
	if mode == "run" {
		args = append(args, "--set", "migration.reviewedDryRun=u1")
	}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	return args
}

// renderMigration renders a migration and fails the test on a refusal.
func renderMigration(t *testing.T, mode string, sets ...string) string {
	t.Helper()
	args := upgradeArgs(mode, sets...)
	out, err := renderArgs(t, args...)
	if err != nil {
		t.Fatalf("helm template %v: %v\n%s", args, err, out)
	}
	return out
}

// mustFail renders and asserts a refusal whose message names every want.
func mustFail(t *testing.T, what string, args []string, want ...string) {
	t.Helper()
	out, err := renderArgs(t, args...)
	if err == nil {
		t.Errorf("%s rendered; want a refusal", what)
		return
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s: the refusal does not name %q: %s", what, w, out)
		}
	}
}

// The default render serves and runs no Job, and it records on the
// Deployment's own metadata that the store carries this release's stored-row
// format, so the next upgrade's preflight reads it. On the pod template the
// annotation would cause a rollout.
//
// Spec: §13.4
func TestChart_NoMigrateJobByDefault(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey)
	if jobs := workloads(t, m, "Job"); len(jobs) != 0 {
		t.Fatalf("the default render carries %d Job(s); want none", len(jobs))
	}
	d := oneWorkload(t, m, "Deployment")
	if got := d.Metadata.Annotations[annFormat]; got != storedRowFormat {
		t.Errorf("the Deployment's %s is %q; want %q", annFormat, got, storedRowFormat)
	}
	if _, ok := d.Spec.Template.Metadata.Annotations[annFormat]; ok {
		t.Errorf("the pod template carries %s, so recording the migration rolls the pods", annFormat)
	}
}

// A dry run renders one post-upgrade hook Job that lists the plan and writes
// nothing. The Job carries the record the reviewed-dry-run gate reads back,
// runs once with no probe or port, and the Deployment stays at zero replicas
// and does not claim the migration.
//
// Spec: §13.4
func TestChart_MigrateJobDryRun(t *testing.T) {
	t.Parallel()
	m := renderMigration(t, "dry-run")
	job := oneWorkload(t, m, "Job")
	c := containerOf(t, job, migrateContainer)

	if want := []string{"sign-stored-rows", "--include-unsigned", "--dry-run"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("dry-run args are %v; want %v", c.Args, want)
	}
	ann := job.Metadata.Annotations
	for k, want := range map[string]string{
		"helm.sh/hook":               "post-upgrade",
		"helm.sh/hook-delete-policy": "before-hook-creation",
		annMode:                      "dry-run",
		annFormat:                    storedRowFormat,
		annImage:                     c.Image,
		annIncludeUnsigned:           "true",
	} {
		if ann[k] != want {
			t.Errorf("Job annotation %s is %q; want %q", k, ann[k], want)
		}
	}
	if ann[annRevision] == "" {
		t.Errorf("the Job carries no %s", annRevision)
	}
	if _, ok := ann[annReviewed]; ok {
		t.Errorf("a dry-run Job carries %s", annReviewed)
	}
	// Offline there is no live Deployment, so the UID is empty; the key must
	// still render for the gates to compare.
	if uid, ok := ann[annDeploymentUID]; !ok || uid != "" {
		t.Errorf("the Job's %s is %q (present %t); want an empty value offline", annDeploymentUID, uid, ok)
	}
	if len(ann[annPodConfig]) != 64 {
		t.Errorf("the Job's %s is %q; want a sha256 digest", annPodConfig, ann[annPodConfig])
	}
	ps := job.Spec.Template.Spec
	if ps.RestartPolicy != "Never" {
		t.Errorf("restartPolicy is %q; want Never", ps.RestartPolicy)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit is %v; want 0", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds != nil {
		t.Errorf("activeDeadlineSeconds is %d by default; want none", *job.Spec.ActiveDeadlineSeconds)
	}
	if len(c.Ports) != 0 || c.StartupProbe != nil || c.LivenessProbe != nil || c.ReadinessProbe != nil {
		t.Errorf("the Job container carries ports or probes: %+v", c)
	}

	d := oneWorkload(t, m, "Deployment")
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 {
		t.Errorf("the Deployment renders replicas %v beside the Job; want 0", d.Spec.Replicas)
	}
	if _, ok := d.Metadata.Annotations[annFormat]; ok {
		t.Errorf("a migration render claims %s before the run", annFormat)
	}

	job = oneWorkload(t, renderMigration(t, "dry-run", "migration.includeUnsigned=false"), "Job")
	c = containerOf(t, job, migrateContainer)
	if want := []string{"sign-stored-rows", "--dry-run"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("dry-run args with includeUnsigned=false are %v; want %v", c.Args, want)
	}
	if got := job.Metadata.Annotations[annIncludeUnsigned]; got != "false" {
		t.Errorf("%s is %q; want false", annIncludeUnsigned, got)
	}
}

// The run applies the unsigned-row policy of the dry run it names, and records
// that dry run's UID.
//
// Spec: §13.4
func TestChart_MigrateJobRunArgs(t *testing.T) {
	t.Parallel()
	job := oneWorkload(t, renderMigration(t, "run"), "Job")
	c := containerOf(t, job, migrateContainer)
	if want := []string{"sign-stored-rows", "--include-unsigned"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("run args are %v; want %v", c.Args, want)
	}
	if got := job.Metadata.Annotations[annReviewed]; got != "u1" {
		t.Errorf("%s is %q; want u1", annReviewed, got)
	}
	if got := job.Metadata.Annotations[annMode]; got != "run" {
		t.Errorf("%s is %q; want run", annMode, got)
	}

	c = containerOf(t, oneWorkload(t, renderMigration(t, "run", "migration.includeUnsigned=false"), "Job"), migrateContainer)
	if want := []string{"sign-stored-rows"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("run args with includeUnsigned=false are %v; want %v", c.Args, want)
	}
}

// The Job must read the same store, object store, and signing key the
// registry serves from, or it rewrites a different store or signs under a
// different key. Every shared pod piece is compared across the value sets that
// switch a piece on.
//
// Spec: §13.4, §13.12
func TestChart_MigrateJobMirrorsTheDeployment(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"default":          nil,
		"bundled postgres": {"postgresql.enabled=true", "postgresql.existingSecret=pg"},
		"s3": {"config.objectStore.bucket=b", "config.objectStore.region=r",
			"config.objectStore.endpoint=http://minio:9000", "config.objectStore.s3ForcePathStyle=true"},
		"filesystem":    {"config.objectStore.type=filesystem", "objects.enabled=true"},
		"runtime keys":  {"runtimeKeys.enabled=true", "runtimeKeys.secretName=rk"},
		"extra env":     {"extraEnv[0].name=EXTRA", "extraEnv[0].value=x"},
		"no secret":     {"existingSecret="},
		"config leaves": {"config.migrationObjectReadTimeout=2m", "config.auditLogPath=https://siem.acme.com/podium"},
		"pod metadata": {"podLabels.team=platform", "podAnnotations.vault\\.hashicorp\\.com/agent-inject=true",
			"nodeSelector.pool=db", "tolerations[0].key=dedicated", "tolerations[0].operator=Exists",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key=zone",
			"affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator=Exists"},
	}
	for name, sets := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := renderMigration(t, "dry-run", sets...)
			d := oneWorkload(t, m, "Deployment")
			job := oneWorkload(t, m, "Job")
			dc := containerOf(t, d, "podium-server")
			jc := containerOf(t, job, migrateContainer)

			for _, f := range []struct {
				field     string
				dep, jobv any
			}{
				{"env", dc.Env, jc.Env},
				{"envFrom", dc.EnvFrom, jc.EnvFrom},
				{"volumeMounts", dc.VolumeMounts, jc.VolumeMounts},
				{"image", dc.Image, jc.Image},
				{"imagePullPolicy", dc.ImagePullPolicy, jc.ImagePullPolicy},
				{"container securityContext", dc.SecurityContext, jc.SecurityContext},
				{"volumes", d.Spec.Template.Spec.Volumes, job.Spec.Template.Spec.Volumes},
				{"pod securityContext", d.Spec.Template.Spec.SecurityContext, job.Spec.Template.Spec.SecurityContext},
				{"nodeSelector", d.Spec.Template.Spec.NodeSelector, job.Spec.Template.Spec.NodeSelector},
				{"tolerations", d.Spec.Template.Spec.Tolerations, job.Spec.Template.Spec.Tolerations},
				{"affinity", d.Spec.Template.Spec.Affinity, job.Spec.Template.Spec.Affinity},
				// An injector such as the Vault agent authenticates with the
				// service-account token, so a Job pod without it would not
				// receive the credentials the Deployment pod receives.
				{"automountServiceAccountToken", d.Spec.Template.Spec.AutomountServiceAccountToken, job.Spec.Template.Spec.AutomountServiceAccountToken},
			} {
				if !reflect.DeepEqual(f.dep, f.jobv) {
					t.Errorf("%s differs:\nDeployment: %v\nJob:        %v", f.field, f.dep, f.jobv)
				}
			}
			if ro, _ := jc.SecurityContext["readOnlyRootFilesystem"].(bool); !ro {
				t.Error("the Job container's root filesystem is writable")
			}
			for k, v := range d.Spec.Template.Metadata.Labels {
				if k == "app.kubernetes.io/name" || k == "app.kubernetes.io/instance" {
					continue // the selector pair, which the Job pod must not carry
				}
				if job.Spec.Template.Metadata.Labels[k] != v {
					t.Errorf("the Job pod lacks the pod label %s=%s", k, v)
				}
			}
			for k, v := range d.Spec.Template.Metadata.Annotations {
				if job.Spec.Template.Metadata.Annotations[k] != v {
					t.Errorf("the Job pod lacks the pod annotation %s=%s", k, v)
				}
			}
			if name == "bundled postgres" {
				names := strings.Join(envNames(jc), ",")
				pw := strings.Index(names, "PODIUM_PG_PASSWORD")
				dsn := strings.Index(names, "PODIUM_POSTGRES_DSN")
				if pw < 0 || dsn < 0 || pw > dsn {
					t.Errorf("the Job's env order is %s; PODIUM_PG_PASSWORD must precede PODIUM_POSTGRES_DSN for $(VAR) expansion", names)
				}
			}
			if name == "pod metadata" && len(d.Spec.Template.Spec.Tolerations) == 0 {
				t.Error("the pod-metadata case rendered no tolerations, so it compared nothing")
			}
		})
	}
}

// The Service and the Deployment select by the registry's name and instance
// labels. A Job pod that matched them would receive traffic, count as a
// Deployment pod, and trip the stop gate the Job itself runs behind.
//
// Spec: §13.4
func TestChart_MigrateJobIsNotSelected(t *testing.T) {
	t.Parallel()
	for _, sets := range [][]string{nil, {"podLabels.app\\.kubernetes\\.io/name=podium"},
		{"migration.podLabels.app\\.kubernetes\\.io/name=podium", "migration.podLabels.app\\.kubernetes\\.io/component=x"}} {
		m := renderMigration(t, "dry-run", sets...)
		job := oneWorkload(t, m, "Job")
		pod := job.Spec.Template.Metadata.Labels
		for what, sel := range map[string]map[string]string{
			"Service selector":       serviceSelector(t, m),
			"Deployment matchLabels": oneWorkload(t, m, "Deployment").Spec.Selector.MatchLabels,
		} {
			if len(sel) == 0 {
				t.Fatalf("the %s is empty", what)
			}
			matches := true
			for k, v := range sel {
				if pod[k] != v {
					matches = false
				}
			}
			if matches {
				t.Errorf("with %v the Job pod labels %v match the %s %v", sets, pod, what, sel)
			}
		}
		for _, labels := range []map[string]string{pod, job.Metadata.Labels} {
			if labels["app.kubernetes.io/instance"] != "t" || labels["app.kubernetes.io/component"] != "migrate" {
				t.Errorf("labels %v lack the instance and component=migrate pair the cleanup selector matches", labels)
			}
		}
	}
}

// A Job name is also a label value on its pods, so it has to fit 63
// characters, and the suffix has to survive a base that already fills them.
//
// Spec: §13.4
func TestChart_MigrateJobNameFitsALabel(t *testing.T) {
	t.Parallel()
	job := oneWorkload(t, renderMigration(t, "dry-run", "fullnameOverride="+strings.Repeat("a", 63)), "Job")
	name := job.Metadata.Name
	if len(name) > 63 || !strings.HasSuffix(name, "-migrate") {
		t.Errorf("the Job name %q is %d characters; want at most 63 ending in -migrate", name, len(name))
	}
}

// The plan holds every row in memory, so the Job inherits the registry's
// resources unless the operator sizes it. A mesh opt-out set for the Job must
// not reach the serving pods.
//
// Spec: §13.4
func TestChart_MigrateJobResourcesAndAnnotations(t *testing.T) {
	t.Parallel()
	m := renderMigration(t, "dry-run", "podAnnotations.mesh=inject", "migration.podAnnotations.mesh=skip")
	d := oneWorkload(t, m, "Deployment")
	job := oneWorkload(t, m, "Job")
	if !reflect.DeepEqual(containerOf(t, job, migrateContainer).Resources, containerOf(t, d, "podium-server").Resources) {
		t.Error("an empty migration.resources does not inherit .Values.resources")
	}
	if got := job.Spec.Template.Metadata.Annotations["mesh"]; got != "skip" {
		t.Errorf("the Job pod's mesh annotation is %q; migration.podAnnotations must win", got)
	}
	if got := d.Spec.Template.Metadata.Annotations["mesh"]; got != "inject" {
		t.Errorf("the Deployment pod's mesh annotation is %q; migration.podAnnotations must not reach it", got)
	}

	// A mesh reads its injection label ahead of its annotation, so a
	// podLabels injection label needs a label opt-out on the Job pod.
	const inject = "sidecar.istio.io/inject"
	m = renderMigration(t, "dry-run", "podLabels.sidecar\\.istio\\.io/inject=true",
		"migration.podLabels.sidecar\\.istio\\.io/inject=false", "podLabels.team=platform")
	d = oneWorkload(t, m, "Deployment")
	job = oneWorkload(t, m, "Job")
	if got := job.Spec.Template.Metadata.Labels[inject]; got != "false" {
		t.Errorf("the Job pod's %s label is %q; migration.podLabels must win", inject, got)
	}
	if got := job.Spec.Template.Metadata.Labels["team"]; got != "platform" {
		t.Errorf("the Job pod's team label is %q; podLabels must still reach it", got)
	}
	if got := d.Spec.Template.Metadata.Labels[inject]; got != "true" {
		t.Errorf("the Deployment pod's %s label is %q; migration.podLabels must not reach it", inject, got)
	}

	m = renderMigration(t, "dry-run",
		"migration.resources.limits.memory=8Gi",
		"migration.activeDeadlineSeconds=3600",
		"migration.backoffLimit=2")
	job = oneWorkload(t, m, "Job")
	// migration.resources merges per key over resources, so the one key the
	// operator raises leaves the CPU settings and the memory request in place.
	res := containerOf(t, job, migrateContainer).Resources
	limits, _ := res["limits"].(map[string]any)
	requests, _ := res["requests"].(map[string]any)
	if limits["memory"] != "8Gi" || limits["cpu"] != "1" || requests["cpu"] != "200m" || requests["memory"] != "256Mi" {
		t.Errorf("migration.resources.limits.memory=8Gi rendered %v; want limits.memory=8Gi over the registry's cpu 200m/1 and memory request 256Mi", res)
	}
	regLimits, _ := containerOf(t, oneWorkload(t, m, "Deployment"), "podium-server").Resources["limits"].(map[string]any)
	if regLimits["memory"] != "1Gi" {
		t.Errorf("the Deployment's limits.memory is %v; migration.resources must not reach it", regLimits["memory"])
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 3600 {
		t.Errorf("activeDeadlineSeconds is %v; want 3600", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 2 {
		t.Errorf("backoffLimit is %v; want 2", job.Spec.BackoffLimit)
	}
}

// Each pure-value guard refuses the render and names the value that fixes it.
//
// Spec: §13.4
func TestChart_MigrationGuards(t *testing.T) {
	t.Parallel()
	base := []string{"--is-upgrade", "--set", withSigningKey, "--set", "migration.preflight=false"}
	with := func(args ...string) []string { return append(append([]string{}, base...), args...) }

	mustFail(t, "an unknown mode", with("--set", "replicaCount=0", "--set", "migration.mode=off"),
		"migration.mode", "disabled", "dry-run", "run")
	mustFail(t, "a mode with serving replicas", with("--set", "replicaCount=3", "--set", "migration.mode=dry-run"),
		"replicaCount=0", "stopped", "docs/deployment/clustered.md")
	mustFail(t, "a mode with signing off", with("--set", "replicaCount=0", "--set", "migration.mode=dry-run", "--set", "signing.mode=none"),
		"signing.mode=registry-key", "config.signature_provider_unavailable", "migration.unprobedStart")
	mustFail(t, "run without a reviewed dry run", with("--set", "replicaCount=0", "--set", "migration.mode=run"),
		"migration.reviewedDryRun", "dry-run", "jsonpath='{.metadata.uid}'")
	mustFail(t, "a mode on an install", []string{"--set", withSigningKey, "--set", "migration.preflight=false",
		"--set", "replicaCount=0", "--set", "migration.mode=dry-run"},
		"helm upgrade", "replicaCount=0", "docs/deployment/clustered.md")

	unprobed := []string{"--set", "signing.mode=none", "--set", "migration.unprobedStart=true",
		"--set", "replicaCount=1", "--set", "strategy.type=Recreate"}
	if out, err := renderArgs(t, with(unprobed...)...); err != nil {
		t.Errorf("the signing-off unprobedStart render refused: %v\n%s", err, out)
	}
	mustFail(t, "unprobedStart with three replicas", with(append(unprobed, "--set", "replicaCount=3")...),
		"migration.unprobedStart", "replicaCount=1")
	mustFail(t, "unprobedStart with RollingUpdate", with(append(unprobed, "--set", "strategy.type=RollingUpdate")...),
		"migration.unprobedStart", "strategy.type=Recreate")
	mustFail(t, "unprobedStart with a mode", with(append(unprobed, "--set", "migration.mode=dry-run", "--set", "replicaCount=0")...),
		"migration.unprobedStart")
	// With signing on, the boot rewrite of a Postgres store leaves every
	// unsigned row unsigned and runs no reviewed dry run, so the Job path
	// applies.
	mustFail(t, "unprobedStart with signing on", with(append(unprobed, "--set", "signing.mode=registry-key")...),
		"migration.unprobedStart", "signing.mode=none", "migration.mode=dry-run")

	// The install acknowledgement: the chart cannot see the store, so a serving
	// install states that the store is empty or migrated.
	mustFail(t, "an install with serving replicas and no storeReady", []string{"--set", withSigningKey},
		"migration.storeReady=true", "content-hash-framing completion record in data_migrations", "replicaCount=0",
		"migration.previousImage")
	// An install has no live Deployment to read the previous image from, so an
	// install over an existing store names it for the image gate.
	mustFail(t, "a zero-replica install with no previousImage", []string{"--set", withSigningKey, "--set", "replicaCount=0"},
		"migration.previousImage", "migration.storeReady=true", "image gate")
	for what, args := range map[string][]string{
		"storeReady=true":                   {"--set", withSigningKey, "--set", "migration.storeReady=true"},
		"replicaCount=0 with previousImage": {"--set", withSigningKey, "--set", "replicaCount=0", "--set", "migration.previousImage=old:0.4.0"},
		"replicaCount=0 with storeReady":    {"--set", withSigningKey, "--set", "replicaCount=0", "--set", "migration.storeReady=true"},
		"an upgrade with storeReady":        {"--set", withSigningKey, "--is-upgrade", "--set", "migration.storeReady=true"},
		"a zero-replica upgrade":            {"--set", withSigningKey, "--is-upgrade", "--set", "replicaCount=0"},
	} {
		if out, err := renderArgs(t, args...); err != nil {
			t.Errorf("an install acknowledgement case (%s) refused: %v\n%s", what, err, out)
		}
	}
}

// With no cluster to read, the lookup gates cannot confirm that the release's
// pods are stopped, that the image is new, or that a dry run was reviewed, so
// a migration render with the gates on fails closed. A serving upgrade render
// sees no live Deployment, which is also what a helm upgrade after the
// Deployment was deleted by hand sees: it cannot tell whether the store is
// migrated, so it requires the storeReady acknowledgement an install requires.
//
// Spec: §13.4
func TestChart_LiveGatesFailClosedOffline(t *testing.T) {
	t.Parallel()
	gated := []string{"--is-upgrade", "--set", withSigningKey, "--set", "replicaCount=0"}
	mustFail(t, "an offline dry-run render", append(gated, "--set", "migration.mode=dry-run"), "image gate")
	out, err := renderArgs(t, append(gated, "--set", "migration.mode=run", "--set", "migration.reviewedDryRun=u1")...)
	if err == nil {
		t.Error("an offline run render passed the lookup gates")
	} else if !strings.Contains(out, "image gate") && !strings.Contains(out, "reviewed-dry-run gate") {
		t.Errorf("an offline run render failed without naming a gate: %s", out)
	}
	mustFail(t, "a serving upgrade with no live Deployment and no storeReady",
		[]string{"--is-upgrade", "--set", withSigningKey, "--set", "replicaCount=3"},
		"serving preflight", "no live Deployment", "migration.storeReady=true",
		"content-hash-framing completion record in data_migrations", "migration.previousImage")
	out, err = renderArgs(t, "--is-upgrade", "--set", withSigningKey, "--set", "replicaCount=3", "--set", "migration.storeReady=true")
	if err != nil {
		t.Fatalf("a serving upgrade with no live Deployment and storeReady refused: %v\n%s", err, out)
	}
	if got := oneWorkload(t, out, "Deployment").Metadata.Annotations[annFormat]; got != storedRowFormat {
		t.Errorf("a serving upgrade with storeReady records %s=%q; want %s", annFormat, got, storedRowFormat)
	}
}

// Under server-side apply a field manager that never applied
// strategy.rollingUpdate cannot remove the value the API server defaults, and
// the switch to Recreate is then rejected. The chart renders the field
// explicitly under RollingUpdate and omits it under Recreate.
//
// Spec: §13.4
func TestChart_StrategyRendering(t *testing.T) {
	t.Parallel()
	strategy := func(sets ...string) map[string]any {
		return oneWorkload(t, render(t, append([]string{withSigningKey}, sets...)...), "Deployment").Spec.Strategy
	}
	s := strategy()
	ru, _ := s["rollingUpdate"].(map[string]any)
	if s["type"] != "RollingUpdate" || ru["maxSurge"] != "25%" || ru["maxUnavailable"] != "25%" {
		t.Errorf("the default strategy is %v; want RollingUpdate with explicit 25%% surge and unavailability", s)
	}
	ru, _ = strategy("strategy.rollingUpdate.maxSurge=1")["rollingUpdate"].(map[string]any)
	if ru["maxSurge"] != 1 {
		t.Errorf("strategy.rollingUpdate.maxSurge=1 renders %v", ru["maxSurge"])
	}
	s = strategy("strategy.type=Recreate", "strategy.rollingUpdate.maxSurge=1")
	if _, ok := s["rollingUpdate"]; ok || s["type"] != "Recreate" {
		t.Errorf("a Recreate strategy renders %v; want type Recreate and no rollingUpdate key", s)
	}
}

// A signing-off first start runs the rewrite at boot, so the kubelet must not
// restart it: the startup and liveness probes go, and readiness stays so the
// pod receives no traffic mid-pass. The render claims no migration, because
// the boot pass may still hold rows back.
//
// Spec: §13.4
func TestChart_UnprobedStartOmitsKillingProbes(t *testing.T) {
	t.Parallel()
	d := oneWorkload(t, render(t, "signing.mode=none", "migration.unprobedStart=true",
		"replicaCount=1", "strategy.type=Recreate"), "Deployment")
	c := containerOf(t, d, "podium-server")
	if c.StartupProbe != nil || c.LivenessProbe != nil {
		t.Errorf("unprobedStart keeps a killing probe: startup %v, liveness %v", c.StartupProbe, c.LivenessProbe)
	}
	if c.ReadinessProbe == nil {
		t.Error("unprobedStart drops the readiness probe, so the pod receives traffic mid-pass")
	}
	if _, ok := d.Metadata.Annotations[annFormat]; ok {
		t.Errorf("an unprobedStart render claims %s", annFormat)
	}
}

// migration.preflight=false is the operator's assertion that the rewrite is
// complete, so a serving render records it; a migration render never does.
//
// Spec: §13.4
func TestChart_PreflightOffWritesAnnotationOnlyWhenServing(t *testing.T) {
	t.Parallel()
	// An upgrade, so neither the install acknowledgement nor a passing serving
	// preflight can write the annotation, and only the preflight=false branch
	// does.
	out, err := renderArgs(t, "--is-upgrade", "--set", withSigningKey, "--set", "migration.preflight=false")
	if err != nil {
		t.Fatalf("a serving upgrade with preflight=false refused: %v\n%s", err, out)
	}
	d := oneWorkload(t, out, "Deployment")
	if got := d.Metadata.Annotations[annFormat]; got != storedRowFormat {
		t.Errorf("a serving upgrade with preflight=false records %s=%q; want %s", annFormat, got, storedRowFormat)
	}
	// The zero-replica step precedes the migrate Job, so preflight=false there
	// must not mark the store migrated, which would let the next serving render
	// pass on the annotation alone.
	out, err = renderArgs(t, "--is-upgrade", "--set", withSigningKey, "--set", "replicaCount=0", "--set", "migration.preflight=false")
	if err != nil {
		t.Fatalf("a zero-replica upgrade with preflight=false refused: %v\n%s", err, out)
	}
	if _, ok := oneWorkload(t, out, "Deployment").Metadata.Annotations[annFormat]; ok {
		t.Errorf("a zero-replica upgrade with preflight=false records %s", annFormat)
	}
	d = oneWorkload(t, renderMigration(t, "dry-run"), "Deployment")
	if _, ok := d.Metadata.Annotations[annFormat]; ok {
		t.Errorf("a migration render with preflight=false records %s", annFormat)
	}
}

// An install has no live Deployment to read, so an install over an existing
// store records the previous image the operator names, and the image gate
// refuses a migrate Job on it. An install that states the store is ready
// records the migration as complete, so its first serving upgrade passes the
// preflight.
//
// Spec: §13.4
func TestChart_InstallRecordsTheStoreState(t *testing.T) {
	t.Parallel()
	out, err := renderArgs(t, "--set", withSigningKey, "--set", "replicaCount=0", "--set", "migration.previousImage=old:0.4.0")
	if err != nil {
		t.Fatalf("a zero-replica install with previousImage refused: %v\n%s", err, out)
	}
	d := oneWorkload(t, out, "Deployment")
	if got := d.Metadata.Annotations[annPreImage]; got != "old:0.4.0" {
		t.Errorf("an install over an existing store records %s=%q; want old:0.4.0", annPreImage, got)
	}
	if _, ok := d.Metadata.Annotations[annFormat]; ok {
		t.Errorf("an install over an existing store records %s", annFormat)
	}
	d = oneWorkload(t, render(t, withSigningKey, "replicaCount=0"), "Deployment")
	if got := d.Metadata.Annotations[annFormat]; got != storedRowFormat {
		t.Errorf("a zero-replica install with storeReady records %s=%q; want %s", annFormat, got, storedRowFormat)
	}
	if _, ok := d.Metadata.Annotations[annPreImage]; ok {
		t.Errorf("a zero-replica install with storeReady records %s", annPreImage)
	}
}

// A run must read the store, the object store, and the signing key through the
// configuration its reviewed dry run read, or it attests rows nobody reviewed.
// The digest the gate compares is equal across the two modes and changes with
// each piece of that configuration.
//
// Spec: §13.4
func TestChart_MigrateJobRecordsItsPodConfig(t *testing.T) {
	t.Parallel()
	digest := func(mode string, sets ...string) string {
		return oneWorkload(t, renderMigration(t, mode, sets...), "Job").Metadata.Annotations[annPodConfig]
	}
	base := digest("dry-run")
	if got := digest("run"); got != base {
		t.Errorf("the run's pod-config %s differs from the dry run's %s under the same values", got, base)
	}
	if got := digest("run", "migration.includeUnsigned=false", "migration.reviewedDryRun=u2"); got != base {
		t.Errorf("a migration value changed the pod-config digest: %s, want %s", got, base)
	}
	for what, set := range map[string]string{
		"config.objectStore.endpoint": "config.objectStore.endpoint=http://other:9000",
		"signing.secretName":          "signing.secretName=other",
		"signing.key":                 "signing.key=other.key",
		"existingSecret":              "existingSecret=other",
		"migrationObjectReadTimeout":  "config.migrationObjectReadTimeout=5m",
	} {
		if got := digest("run", set); got == base {
			t.Errorf("changing %s leaves the pod-config digest at %s", what, got)
		}
	}
}

// The object-read deadline and the audit sink reach both pods, ahead of
// extraEnv so an operator entry can still override them, and render nothing
// when unset so the registry's own default applies.
//
// Spec: §13.4, §13.12
func TestChart_MigrationConfigReachesBothPods(t *testing.T) {
	t.Parallel()
	m := renderMigration(t, "dry-run",
		"config.migrationObjectReadTimeout=2m",
		"config.auditLogPath=https://siem.acme.com/podium",
		"extraEnv[0].name=EXTRA", "extraEnv[0].value=x")
	for _, kind := range []string{"Deployment", "Job"} {
		w := oneWorkload(t, m, kind)
		c := w.Spec.Template.Spec.Containers[0]
		if got := envValue(c, "PODIUM_MIGRATION_OBJECT_READ_TIMEOUT"); got != "2m" {
			t.Errorf("%s PODIUM_MIGRATION_OBJECT_READ_TIMEOUT is %q; want 2m", kind, got)
		}
		if got := envValue(c, "PODIUM_AUDIT_LOG_PATH"); got != "https://siem.acme.com/podium" {
			t.Errorf("%s PODIUM_AUDIT_LOG_PATH is %q", kind, got)
		}
		names := envNames(c)
		if names[len(names)-1] != "EXTRA" {
			t.Errorf("%s env does not end with the extraEnv entry: %v", kind, names)
		}
	}
	m = renderMigration(t, "dry-run")
	for _, kind := range []string{"Deployment", "Job"} {
		c := oneWorkload(t, m, kind).Spec.Template.Spec.Containers[0]
		for _, name := range []string{"PODIUM_MIGRATION_OBJECT_READ_TIMEOUT", "PODIUM_AUDIT_LOG_PATH"} {
			for _, n := range envNames(c) {
				if n == name {
					t.Errorf("%s renders %s with the value unset", kind, name)
				}
			}
		}
	}
}

// A zero-replica render prints the stop step's wait command, including the
// phase selector that lets it return past an evicted pod. `helm template` does
// not render NOTES, and a client-side install dry run needs no cluster.
//
// Spec: §13.4
func TestChart_NotesStopStep(t *testing.T) {
	t.Parallel()
	out, err := helmRun(t, "install", "t", chartDir, "--dry-run=client",
		"--set", withSigningKey, "--set", "replicaCount=0", "--set", "migration.previousImage=old:0.4.0")
	if err != nil {
		t.Fatalf("helm install --dry-run=client: %v\n%s", err, out)
	}
	i := strings.Index(out, "NOTES:")
	if i < 0 {
		t.Fatalf("rendered output carries no NOTES section:\n%s", out)
	}
	notes := out[i:]
	for _, want := range []string{
		"kubectl wait --for=delete pod",
		"app.kubernetes.io/name=podium,app.kubernetes.io/instance=t",
		"--field-selector=status.phase!=Succeeded,status.phase!=Failed",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the NOTES do not carry %q:\n%s", want, notes)
		}
	}
}
