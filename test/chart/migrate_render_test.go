// Render tests for the §13.4 migrate Job and the render guards around it.
//
// The first start of a release that migrates a stored value rewrites every
// stored row before it serves, and the rewrite requires every registry process
// on the previous release stopped. The chart runs the rewrite as a
// post-install and post-upgrade hook Job while the Deployment is held at zero
// replicas, so no probe kills the pass and no previous-version pod runs beside
// it. The chart reads nothing from the cluster, so every refusal is
// values-only and pinned here: the Job's arguments, its parity with the
// Deployment's pod, and every guard. The kind test (kind_upgrade_test.go) pins
// the hook lifecycle and the registry's boot refusal over an unmigrated store
// on a cluster.
package chart

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// annPrefix is the prefix of the record annotations the chart no longer
	// writes. No rendered object may carry a key under it.
	annPrefix        = "podium.lennylabs.dev/"
	migrateContainer = "sign-stored-rows"
	// previousImage is the migration.previousImage every migration render in
	// these tests names. It differs from the chart's default image.
	previousImage = "old:0.4.0"
)

// zeroDigest is a well-formed plan digest for a run render.
var zeroDigest = "sha256:" + strings.Repeat("0", 64)

// upgradeArgs builds `helm template` arguments for a migration render: an
// upgrade, the signing Secret, zero replicas, the previous image, and for a
// run a well-formed plan digest, so the render reaches the Job body. A test
// that asserts a guard builds its own.
func upgradeArgs(mode string, sets ...string) []string {
	args := []string{"--is-upgrade",
		"--set", withSigningKey,
		"--set", "replicaCount=0",
		"--set", "migration.previousImage=" + previousImage,
		"--set", "migration.mode=" + mode,
	}
	if mode == "run" {
		args = append(args, "--set", "migration.planDigest="+zeroDigest)
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

// assertNoRecordAnnotations fails when an annotation map carries a key under
// annPrefix.
func assertNoRecordAnnotations(t *testing.T, what string, ann map[string]string) {
	t.Helper()
	for k := range ann {
		if strings.HasPrefix(k, annPrefix) {
			t.Errorf("%s carries the record annotation %s", what, k)
		}
	}
}

// helmTemplateGuards matches every template construct that reads cluster or
// release state. A template that matched would render differently under
// helm template, helm install, helm upgrade, and a GitOps controller.
var helmTemplateGuards = regexp.MustCompile(`\blookup\b|\.Release\.IsInstall|\.Release\.IsUpgrade|\.Release\.Revision`)

// GitOps support and a render that needs no cluster permission rest on the
// chart reading nothing but its values: no template calls lookup or reads the
// release's install, upgrade, or revision state, and a migration render is
// byte-equal with and without --is-upgrade.
//
// Spec: §13.4
func TestChart_ReadsNothingFromTheCluster(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(chartDir, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read templates: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if m := helmTemplateGuards.Find(raw); m != nil {
			t.Errorf("%s reads cluster or release state through %q", e.Name(), m)
		}
	}

	upgrade := renderMigration(t, "dry-run")
	install, err := renderArgs(t, upgradeArgs("dry-run")[1:]...)
	if err != nil {
		t.Fatalf("a dry-run install render refused: %v\n%s", err, install)
	}
	if upgrade != install {
		t.Errorf("a dry-run render differs between install and upgrade:\nupgrade:\n%s\ninstall:\n%s", upgrade, install)
	}
}

// The default render serves and runs no Job, and neither the Deployment nor
// its pod template carries a record annotation.
//
// Spec: §13.4
func TestChart_NoMigrateJobByDefault(t *testing.T) {
	t.Parallel()
	m := render(t, withSigningKey)
	if jobs := workloads(t, m, "Job"); len(jobs) != 0 {
		t.Fatalf("the default render carries %d Job(s); want none", len(jobs))
	}
	d := oneWorkload(t, m, "Deployment")
	assertNoRecordAnnotations(t, "the Deployment", d.Metadata.Annotations)
	assertNoRecordAnnotations(t, "the Deployment's pod template", d.Spec.Template.Metadata.Annotations)
}

// A dry run renders one post-install and post-upgrade hook Job that lists the
// plan and writes nothing. It runs once with no probe or port, carries no
// record annotation and no plan digest, and the Deployment stays at zero
// replicas.
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
		"helm.sh/hook":               "post-install,post-upgrade",
		"helm.sh/hook-delete-policy": "before-hook-creation",
	} {
		if ann[k] != want {
			t.Errorf("Job annotation %s is %q; want %q", k, ann[k], want)
		}
	}
	assertNoRecordAnnotations(t, "the dry-run Job", ann)
	for _, a := range c.Args {
		if strings.HasPrefix(a, "--plan-digest") {
			t.Errorf("a dry-run Job passes %s", a)
		}
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
	assertNoRecordAnnotations(t, "the Deployment", d.Metadata.Annotations)

	c = containerOf(t, oneWorkload(t, renderMigration(t, "dry-run", "migration.includeUnsigned=false"), "Job"), migrateContainer)
	if want := []string{"sign-stored-rows", "--dry-run"}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("dry-run args with includeUnsigned=false are %v; want %v", c.Args, want)
	}
}

// The run applies the unsigned-row policy of its dry run and passes the
// reviewed plan digest, whatever includeUnsigned is.
//
// Spec: §13.4
func TestChart_MigrateJobRunArgs(t *testing.T) {
	t.Parallel()
	c := containerOf(t, oneWorkload(t, renderMigration(t, "run"), "Job"), migrateContainer)
	if want := []string{"sign-stored-rows", "--include-unsigned", "--plan-digest=" + zeroDigest}; !reflect.DeepEqual(c.Args, want) {
		t.Errorf("run args are %v; want %v", c.Args, want)
	}
	c = containerOf(t, oneWorkload(t, renderMigration(t, "run", "migration.includeUnsigned=false"), "Job"), migrateContainer)
	if want := []string{"sign-stored-rows", "--plan-digest=" + zeroDigest}; !reflect.DeepEqual(c.Args, want) {
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
// labels. A Job pod that matched them would receive traffic and count as a
// Deployment pod.
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

// chartAppVersion reads appVersion from Chart.yaml, the tag podium.image
// defaults to.
func chartAppVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	var chart struct {
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(raw, &chart); err != nil || chart.AppVersion == "" {
		t.Fatalf("Chart.yaml appVersion: %q, %v", chart.AppVersion, err)
	}
	return chart.AppVersion
}

// Each values-only guard refuses the render and names the value that fixes it.
// podium.validate stops at the first fail, so the cases also pin the order:
// the previousImage guards fire ahead of the plan-digest guard.
//
// Spec: §13.4
func TestChart_MigrationGuards(t *testing.T) {
	t.Parallel()
	base := []string{"--is-upgrade", "--set", withSigningKey, "--set", "migration.previousImage=" + previousImage}
	with := func(args ...string) []string { return append(append([]string{}, base...), args...) }
	zero := []string{"--set", "replicaCount=0"}

	mustFail(t, "an unknown mode", with("--set", "replicaCount=0", "--set", "migration.mode=off"),
		"migration.mode", "disabled", "dry-run", "run")
	mustFail(t, "a mode with serving replicas", with("--set", "replicaCount=3", "--set", "migration.mode=dry-run"),
		"replicaCount=0", "stopped", "docs/deployment/clustered.md")

	// With signing off the command refuses --include-unsigned, so the chart
	// refuses the default includeUnsigned=true and renders false.
	mustFail(t, "a signing-off mode with includeUnsigned=true",
		with(append(zero, "--set", "migration.mode=dry-run", "--set", "signing.mode=none")...),
		"migration.includeUnsigned=false", "config.signature_provider_unavailable")
	out, err := renderArgs(t, with(append(zero, "--set", "migration.mode=dry-run", "--set", "signing.mode=none",
		"--set", "migration.includeUnsigned=false")...)...)
	if err != nil {
		t.Fatalf("a signing-off dry run with includeUnsigned=false refused: %v\n%s", err, out)
	}
	job := oneWorkload(t, out, "Job")
	jc := containerOf(t, job, migrateContainer)
	if want := []string{"sign-stored-rows", "--dry-run"}; !reflect.DeepEqual(jc.Args, want) {
		t.Errorf("a signing-off dry run passes %v; want %v", jc.Args, want)
	}
	if got := envValue(jc, "PODIUM_SIGN"); got != "none" {
		t.Errorf("a signing-off Job's PODIUM_SIGN is %q; want none", got)
	}
	if findMount(jc, "signing") != nil || findVolume(job, "signing") != nil {
		t.Errorf("a signing-off Job mounts a signing volume: %v", jc.VolumeMounts)
	}

	// The chart requires the digest for every run, including one without
	// --include-unsigned, for which the command itself would accept none.
	for what, sets := range map[string][]string{
		"a run without a plan digest":                         nil,
		"a run with a malformed plan digest":                  {"--set", "migration.planDigest=sha256:XYZ"},
		"a run with an uppercase plan digest":                 {"--set", "migration.planDigest=sha256:" + strings.Repeat("A", 64)},
		"a run with includeUnsigned=false and no plan digest": {"--set", "migration.includeUnsigned=false"},
	} {
		mustFail(t, what, with(append(append(zero, "--set", "migration.mode=run"), sets...)...),
			"migration.planDigest", "dry-run: plan digest")
	}

	for _, mode := range []string{"dry-run", "run"} {
		mustFail(t, mode+" without migration.previousImage",
			with(append(zero, "--set", "migration.mode="+mode, "--set", "migration.previousImage=")...),
			"migration.previousImage", "v0.4.0")
	}
	mustFail(t, "a migration on the previous image",
		with(append(zero, "--set", "migration.mode=dry-run", "--set", "image.repository=r", "--set", "image.tag=t",
			"--set", "migration.previousImage=r:t")...),
		"migration.previousImage", "r:t")
	appImage := "ghcr.io/lennylabs/podium:" + chartAppVersion(t)
	mustFail(t, "a migration on the AppVersion default image",
		with(append(zero, "--set", "migration.mode=dry-run", "--set", "migration.previousImage="+appImage)...),
		"migration.previousImage", appImage)

	for what, args := range map[string][]string{
		"a serving install":      {"--set", withSigningKey, "--set", "replicaCount=3"},
		"a zero-replica install": {"--set", withSigningKey, "--set", "replicaCount=0"},
		"a serving upgrade":      {"--is-upgrade", "--set", withSigningKey},
	} {
		if out, err := renderArgs(t, args...); err != nil {
			t.Errorf("%s refused: %v\n%s", what, err, out)
		}
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

// renderNotes renders the chart's NOTES through a client-side install dry run,
// which needs no cluster. `helm template` does not render NOTES.
func renderNotes(t *testing.T, args ...string) string {
	t.Helper()
	// Helm v3 checks that a cluster is reachable on every install, a dry run
	// included, and `helm template` does not render NOTES. Only Helm v4's
	// client-side dry run renders them without a cluster.
	if v, err := helmRun(t, "version", "--short"); err != nil || !strings.HasPrefix(strings.TrimSpace(v), "v4.") {
		t.Skipf("rendering NOTES without a cluster needs Helm v4; helm version --short printed %q", strings.TrimSpace(v))
	}
	out, err := helmRun(t, append([]string{"install", "t", chartDir, "--dry-run=client"}, args...)...)
	if err != nil {
		t.Fatalf("helm install --dry-run=client %v: %v\n%s", args, err, out)
	}
	i := strings.Index(out, "NOTES:")
	if i < 0 {
		t.Fatalf("rendered output carries no NOTES section:\n%s", out)
	}
	return out[i:]
}

// The dry-run and run NOTES follow the signing mode. With signing off the
// command signs no row and checks no signature, so the notes tell the
// operator neither to review signing nor to fix the signing Secret.
//
// Spec: §13.4
func TestChart_NotesFollowTheSigningMode(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, notes string, want, absent []string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(notes, w) {
				t.Errorf("the NOTES do not carry %q:\n%s", w, notes)
			}
		}
		for _, a := range absent {
			if strings.Contains(notes, a) {
				t.Errorf("the NOTES carry %q:\n%s", a, notes)
			}
		}
	}
	off := []string{"--set", "signing.mode=none", "--set", "migration.includeUnsigned=false"}
	on := []string{"--set", withSigningKey}
	migration := func(mode string, signing []string, extra ...string) []string {
		args := append([]string{"--set", "replicaCount=0", "--set", "migration.previousImage=" + previousImage,
			"--set", "migration.mode=" + mode}, signing...)
		return append(args, extra...)
	}

	t.Run("dry-run", func(t *testing.T) {
		t.Parallel()
		check(t, renderNotes(t, migration("dry-run", off)...),
			[]string{"signed_by=unchecked", "grep -E '^dry-run: plan digest ' dry-run.log", "migration.planDigest=<digest>"},
			[]string{"signature_unverified", "signing Secret"})
		check(t, renderNotes(t, migration("dry-run", on)...),
			[]string{"signed_by=unsigned and sign=true", "signature_unverified", "grep -E '^dry-run: plan digest ' dry-run.log"},
			[]string{"signed_by=unchecked"})
	})
	t.Run("run", func(t *testing.T) {
		t.Parallel()
		digest := []string{"--set", "migration.planDigest=" + zeroDigest}
		check(t, renderNotes(t, migration("run", off, digest...)...),
			[]string{"the Job signs no row", "--plan-digest=" + zeroDigest, "Exit status 3", "run.log", "exits at start"},
			[]string{"unsigned left", "materialize.signature_missing"})
		check(t, renderNotes(t, migration("run", on, append(digest, "--set", "migration.includeUnsigned=false")...)...),
			[]string{"rehash: N unsigned left", "materialize.signature_missing"},
			[]string{"the Job signs no row"})
	})
}

// A zero-replica render prints the stop step's wait command, including the
// phase selector that lets it return past an evicted pod. `helm template` does
// not render NOTES, and a client-side install dry run needs no cluster.
//
// Spec: §13.4
func TestChart_NotesStopStep(t *testing.T) {
	t.Parallel()
	notes := renderNotes(t, "--set", withSigningKey, "--set", "replicaCount=0")
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
