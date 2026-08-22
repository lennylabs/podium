// Package chart tests the Helm chart's values against what the registry
// accepts at startup and against what the templates actually render.
//
// The chart had three independent defects at once and no test: it selected an
// identity provider the registry refuses, an embedding provider whose
// credential it never supplied, and a bind address no template consumed. Each
// one alone stops a default install, and none is visible from reading a
// template, because two are in values.yaml and the third is the absence of a
// line.
package chart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const chartDir = "../../deploy/helm/podium"

// valuesConfig is the config: subtree of values.yaml, loaded as a generic tree
// so a key added without a template is still visible here.
func loadConfig(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}
	cfg, ok := doc["config"].(map[string]any)
	if !ok {
		t.Fatalf("values.yaml has no config: mapping")
	}
	return cfg
}

func templateSources(t *testing.T) string {
	t.Helper()
	var b strings.Builder
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
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// Spec: §6.3.3 — the registry verifies oidc-jwt, trusted-headers, and
// injected-session-token at request time. oauth-device-code is the client-side
// acquisition provider and the registry refuses it at startup with
// config.identity_provider_unverified, so a chart that ships it as the default
// cannot install.
func TestChart_IdentityProviderIsOneTheRegistryVerifies(t *testing.T) {
	t.Parallel()
	cfg := loadConfig(t)
	idp, ok := cfg["identityProvider"].(map[string]any)
	if !ok {
		t.Fatal("config.identityProvider is missing")
	}
	got, _ := idp["type"].(string)
	verified := map[string]bool{
		"oidc-jwt":               true,
		"trusted-headers":        true,
		"injected-session-token": true,
	}
	if !verified[got] {
		t.Errorf("config.identityProvider.type = %q, which the registry does not verify at request time; a default install refuses to start", got)
	}
}

// A registry that selects an embedding provider needs that provider's
// credential, which the chart cannot ship. The default therefore selects none,
// so a bare install boots and an operator turns hybrid search on deliberately.
// spec: §4.7 / §13.12.
func TestChart_EmbeddingDefaultNeedsNoCredential(t *testing.T) {
	t.Parallel()
	cfg := loadConfig(t)
	emb, ok := cfg["embeddingProvider"].(map[string]any)
	if !ok {
		t.Fatal("config.embeddingProvider is missing")
	}
	got, _ := emb["type"].(string)
	// These four are the providers that read a key from the environment.
	needsKey := map[string]bool{"openai": true, "voyage": true, "cohere": true}
	if needsKey[got] {
		t.Errorf("config.embeddingProvider.type = %q, which requires a credential the chart does not supply; a default install exits with `missing required configuration for the selected backend(s)`", got)
	}
}

// Every runtime setting values.yaml declares under config: is either rendered
// by a template or listed here as supplied through the secret that reaches the
// pod via envFrom. config.bind was neither: it was declared, documented, and
// silently ignored, so the registry kept its 127.0.0.1 default and the kubelet
// could not reach the pod. This test fails on the next such orphan.
func TestChart_EveryConfigValueIsRenderedOrDeclaredSecretSupplied(t *testing.T) {
	t.Parallel()
	// Keys the operator supplies through existingSecret rather than through the
	// chart's env block. Each is a credential or a value that varies per
	// deployment and has no business in a values file.
	secretSupplied := map[string]bool{
		"endpoint":                true,
		"store.dsn":               true,
		"objectStore.bucket":      true,
		"objectStore.region":      true,
		"objectStore.endpoint":    true,
		"embeddingProvider.model": true,
	}

	tmpl := templateSources(t)
	var orphans []string

	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for k, v := range node {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if child, ok := v.(map[string]any); ok {
				walk(path, child)
				continue
			}
			if secretSupplied[path] {
				continue
			}
			// A rendered value appears in a template as .Values.config.<path>.
			if !strings.Contains(tmpl, ".Values.config."+path) {
				orphans = append(orphans, path)
			}
		}
	}
	walk("", loadConfig(t))

	for _, o := range orphans {
		t.Errorf("config.%s is declared in values.yaml but no template renders it and it is not listed as secret-supplied; setting it has no effect", o)
	}
}

// loadValues returns the whole values.yaml tree, not only its config: subtree.
func loadValues(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}
	return doc
}

// The orphan check above walks config: only, so a top-level block could be
// declared, documented, and rendered by nothing without failing anything. Two
// were: an ingress: block and a serviceMonitor: block, each with an enabled
// flag that reported success and changed nothing, because the chart ships no
// template for either. This test covers the top level the same way.
//
// Spec: §13.1 — the chart is the standard-topology deployment surface, so a
// switch it offers has to reach the deployment.
func TestChart_EveryTopLevelBlockIsRendered(t *testing.T) {
	t.Parallel()
	// Blocks the templates consume through something other than a literal
	// .Values.<name> reference, or that Helm itself defines.
	consumedElsewhere := map[string]bool{
		"config":           true, // covered by the config-subtree orphan test
		"nameOverride":     true, // read by the _helpers.tpl name templates
		"fullnameOverride": true,
	}

	tmpl := templateSources(t)
	for k := range loadValues(t) {
		if consumedElsewhere[k] {
			continue
		}
		if !strings.Contains(tmpl, ".Values."+k) {
			t.Errorf("values.yaml declares %q and no template renders it; setting it has no effect", k)
		}
	}
}

// existingSecret is optional, and emptying it must drop the envFrom block
// rather than render a secretRef with no name. An unguarded block made
// `--set existingSecret=""` produce a manifest the API server rejects outright
// with `envFrom[0].secretRef.name: Required value`, so a deployment that needs
// no secret could not be installed at all.
//
// Spec: §13.1
func TestChart_EnvFromIsGuardedOnExistingSecret(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(chartDir, "templates", "deployment.yaml"))
	if err != nil {
		t.Fatalf("read deployment.yaml: %v", err)
	}
	src := string(raw)
	idx := strings.Index(src, "envFrom:")
	if idx < 0 {
		t.Fatal("deployment.yaml renders no envFrom block")
	}
	// The guard opens before the block and closes after it.
	before := src[:idx]
	if !strings.Contains(before, "if .Values.existingSecret") {
		t.Error("the envFrom block is not guarded on .Values.existingSecret; `--set existingSecret=\"\"` renders secretRef with an empty name and the API server rejects the Deployment")
	}
}

// Public mode refuses a non-loopback bind unless the bind is explicitly
// allowed, and the chart renders 0.0.0.0 so the kubelet can reach the probes.
// A chart that offered publicMode without allowPublicBind could therefore not
// express public mode at all: every such pod would exit with
// config.public_bind_refused.
//
// Spec: §13.10 — public mode, its bind restriction, and the override.
func TestChart_PublicModeCarriesItsBindOverride(t *testing.T) {
	t.Parallel()
	cfg := loadConfig(t)
	for _, k := range []string{"publicMode", "allowPublicBind"} {
		if _, ok := cfg[k]; !ok {
			t.Errorf("values.yaml declares no config.%s; public mode cannot be expressed through the chart without it", k)
		}
	}
	tmpl := templateSources(t)
	for _, env := range []string{"PODIUM_PUBLIC_MODE", "PODIUM_ALLOW_PUBLIC_BIND"} {
		if !strings.Contains(tmpl, env) {
			t.Errorf("no template renders %s, so the corresponding value cannot reach the pod", env)
		}
	}
}

// The identity block names the key the selected provider actually reads.
// oidc-jwt reads issuer; authorization_endpoint belongs to the device-code
// flow, which is not a provider the registry accepts, so a chart offering that
// key would document a configuration that cannot start. The spec's own §13.12
// example carried the same confusion and was corrected.
//
// Spec: §6.3.3 — oidc-jwt requires an https issuer and an audience.
func TestChart_IdentityBlockNamesTheKeysItsProviderReads(t *testing.T) {
	t.Parallel()
	cfg := loadConfig(t)
	idp, ok := cfg["identityProvider"].(map[string]any)
	if !ok {
		t.Fatal("values.yaml declares no config.identityProvider block")
	}
	if _, bad := idp["authorizationEndpoint"]; bad {
		t.Error("config.identityProvider.authorizationEndpoint is the device-code key; the registry accepts no device-code provider, so this key configures nothing the chart can select")
	}
	if _, ok := idp["issuer"]; !ok {
		t.Error("config.identityProvider declares no issuer, which is the key oidc-jwt reads (§6.3.3)")
	}
	// Both endpoint values default empty so a secret-supplied value wins: a
	// container env entry overrides the same key arriving through envFrom.
	for _, k := range []string{"issuer", "audience"} {
		if v, _ := idp[k].(string); v != "" {
			t.Errorf("config.identityProvider.%s defaults to %q; a non-empty default overrides a secret-supplied value through envFrom precedence and would replace a correct endpoint with a placeholder", k, v)
		}
	}
}
