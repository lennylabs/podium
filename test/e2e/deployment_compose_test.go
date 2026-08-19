package e2e

// Structural conformance tests for the §13.1.1 evaluation docker-compose
// stack. These parse docker-compose.yml and the bundled Dex config without
// requiring Docker, so they run in plain-process CI. They assert that the
// compose file declares the full standard topology the spec lists
// (registry, postgres, minio, dex, bootstrap) and wires the registry
// against the local services.
//
// spec: §13.1.1 — covers the registry service, the dex
// service, bootstrap responsibilities, and the
// one-command full-stack evaluation topology).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// composeService models the fields of a compose service these tests read.
type composeService struct {
	Image       string                       `yaml:"image"`
	Build       any                          `yaml:"build"`
	Environment map[string]string            `yaml:"environment"`
	Entrypoint  any                          `yaml:"entrypoint"`
	Command     any                          `yaml:"command"`
	Volumes     []string                     `yaml:"volumes"`
	Ports       []string                     `yaml:"ports"`
	DependsOn   map[string]composeDependency `yaml:"depends_on"`
}

// commandString flattens a compose command/entrypoint, which YAML may
// encode as a scalar string or a sequence of strings, into one string.
func commandString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

type composeDependency struct {
	Condition string `yaml:"condition"`
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

func loadComposeFile(t *testing.T) composeFile {
	t.Helper()
	path := filepath.Join(repoRoot(t), "docker-compose.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cf
}

// TestOrg_1_ComposeStackServices asserts the compose file declares every
// service §13.1.1 lists. The pre-remediation file declared only postgres,
// minio, and minio-init.
func TestDeployCompose_ComposeStackServices(t *testing.T) {
	t.Parallel()
	cf := loadComposeFile(t)
	for _, name := range []string{"registry", "postgres", "minio", "dex", "bootstrap"} {
		if _, ok := cf.Services[name]; !ok {
			t.Errorf("docker-compose.yml is missing the %q service required by §13.1.1", name)
		}
	}
}

// TestOrg_1_RegistryServiceWiring asserts the registry service is built and
// configured against the local Postgres, MinIO, and Dex services.
func TestDeployCompose_RegistryServiceWiring(t *testing.T) {
	t.Parallel()
	cf := loadComposeFile(t)
	reg, ok := cf.Services["registry"]
	if !ok {
		t.Fatal("no registry service")
	}
	if reg.Build == nil && reg.Image == "" {
		t.Error("registry service has neither build nor image; it cannot start the registry binary")
	}
	env := reg.Environment
	checks := map[string]func(string) bool{
		"PODIUM_REGISTRY_STORE":   func(v string) bool { return v == "postgres" },
		"PODIUM_POSTGRES_DSN":     func(v string) bool { return strings.Contains(v, "postgres:5432") },
		"PODIUM_OBJECT_STORE":     func(v string) bool { return v == "s3" },
		"PODIUM_S3_ENDPOINT":      func(v string) bool { return strings.Contains(v, "minio:9000") },
		"PODIUM_S3_BUCKET":        func(v string) bool { return v == "podium" },
		"PODIUM_BOOTSTRAP_ADMINS": func(v string) bool { return v != "" },
		"PODIUM_BIND":             func(v string) bool { return strings.HasPrefix(v, "0.0.0.0:") },
	}
	for key, ok := range checks {
		v, present := env[key]
		if !present {
			t.Errorf("registry env missing %s", key)
			continue
		}
		if !ok(v) {
			t.Errorf("registry env %s=%q failed its wiring check", key, v)
		}
	}
	// Spec: §13.1.1 — the evaluation stack selects no identity provider, so
	// it authenticates no caller. Selecting injected-session-token over the
	// empty key set the stack ships would abort the boot with
	// config.runtime_keys_unavailable (§6.3.2).
	if v, present := env["PODIUM_IDENTITY_PROVIDER"]; present {
		t.Errorf("registry env sets PODIUM_IDENTITY_PROVIDER=%q; the evaluation stack selects no provider", v)
	}
	// Spec: §13.1.1 — the pin fixes the visibility persisted into a layer's
	// declaration so the declaration is correct once an identity provider is
	// configured. It changes nothing a caller observes on this stack, where
	// every caller resolves as anonymous-public.
	if v := env["PODIUM_DEFAULT_LAYER_VISIBILITY"]; v != "private" {
		t.Errorf("PODIUM_DEFAULT_LAYER_VISIBILITY=%q, want \"private\"", v)
	}
	// Spec: §13.1.1 — the listener is published on the host loopback
	// interface, so an unauthenticated registry is not reachable off-host.
	if len(reg.Ports) != 1 || !strings.HasPrefix(reg.Ports[0], "127.0.0.1:") {
		t.Errorf("registry ports = %v, want a single 127.0.0.1-bound publish", reg.Ports)
	}
	// The device-code authorization endpoint must point at the bundled Dex.
	if v := env["PODIUM_OAUTH_AUTHORIZATION_ENDPOINT"]; !strings.Contains(v, "5556") {
		t.Errorf("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT=%q does not point at the bundled Dex (port 5556)", v)
	}
	// The registry must come up after Postgres is healthy and the bucket
	// bootstrap has completed.
	if dep, ok := reg.DependsOn["postgres"]; !ok || dep.Condition != "service_healthy" {
		t.Errorf("registry should depend_on postgres with service_healthy, got %+v", reg.DependsOn["postgres"])
	}
	if dep, ok := reg.DependsOn["bootstrap"]; !ok || dep.Condition != "service_completed_successfully" {
		t.Errorf("registry should depend_on bootstrap with service_completed_successfully, got %+v", reg.DependsOn["bootstrap"])
	}
}

// TestDeployCompose_InjectedTokenServicesSeedRuntimeKeys asserts the paired
// runtime-key invariant over every service in the compose file: a service
// that selects injected-session-token also names a PODIUM_RUNTIME_KEYS_PATH
// and mounts a volume that supplies it. A service that selects the provider
// over a key set no volume supplies aborts its boot with
// config.runtime_keys_unavailable, and the compose file has no compiled
// call site that would catch it. The invariant holds vacuously while no
// service names the provider.
//
// Spec: §6.3.2 — the trusted runtime signing-key set is operator-supplied
// configuration read at startup.
// Spec: §13.12 — PODIUM_RUNTIME_KEYS_PATH names the seed file.
func TestDeployCompose_InjectedTokenServicesSeedRuntimeKeys(t *testing.T) {
	t.Parallel()
	cf := loadComposeFile(t)
	for name, svc := range cf.Services {
		if svc.Environment["PODIUM_IDENTITY_PROVIDER"] != "injected-session-token" {
			continue
		}
		keysPath := svc.Environment["PODIUM_RUNTIME_KEYS_PATH"]
		if keysPath == "" {
			t.Errorf("service %q selects injected-session-token and sets no PODIUM_RUNTIME_KEYS_PATH; its boot aborts with config.runtime_keys_unavailable", name)
			continue
		}
		if !composeSuppliesPath(svc.Volumes, keysPath) {
			t.Errorf("service %q sets PODIUM_RUNTIME_KEYS_PATH=%q but mounts no volume supplying it; volumes=%v", name, keysPath, svc.Volumes)
		}
	}
}

// composeSuppliesPath reports whether one of the compose volume entries
// mounts the container-side path, either exactly or as a directory holding
// it. A compose volume entry is `source:target[:options]`, so the
// container-side path is the second colon-separated field.
func composeSuppliesPath(volumes []string, target string) bool {
	for _, v := range volumes {
		fields := strings.Split(v, ":")
		if len(fields) < 2 {
			continue
		}
		mount := fields[1]
		if mount == target || strings.HasPrefix(target, strings.TrimSuffix(mount, "/")+"/") {
			return true
		}
	}
	return false
}

// TestDeployCompose_ComposeSuppliesPath exercises the matcher the invariant
// above runs vacuously today, so the arms it depends on are pinned before a
// service reinstates injected-session-token.
func TestDeployCompose_ComposeSuppliesPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		volumes []string
		target  string
		want    bool
	}{
		{"exact file mount", []string{"./deploy/keys.json:/etc/podium/keys.json:ro"}, "/etc/podium/keys.json", true},
		{"directory prefix", []string{"./deploy/keys:/etc/podium"}, "/etc/podium/keys.json", true},
		{"directory prefix with slash", []string{"./deploy/keys:/etc/podium/"}, "/etc/podium/keys.json", true},
		{"unrelated mount", []string{"./deploy/dex:/etc/dex:ro"}, "/etc/podium/keys.json", false},
		{"named volume without target", []string{"podium_data"}, "/etc/podium/keys.json", false},
		{"no volumes", nil, "/etc/podium/keys.json", false},
		{"sibling path is not a prefix", []string{"./a:/etc/podium-keys"}, "/etc/podium-keys.json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := composeSuppliesPath(tc.volumes, tc.target); got != tc.want {
				t.Errorf("composeSuppliesPath(%v, %q) = %v, want %v", tc.volumes, tc.target, got, tc.want)
			}
		})
	}
}

// TestOrg_1_DexService asserts the Dex IdP service exists, mounts the
// bundled config, and that the config defines a device-code client and a
// static user.
func TestDeployCompose_DexService(t *testing.T) {
	t.Parallel()
	cf := loadComposeFile(t)
	dex, ok := cf.Services["dex"]
	if !ok {
		t.Fatal("no dex service")
	}
	if !strings.Contains(dex.Image, "dex") {
		t.Errorf("dex service image %q does not look like a Dex image", dex.Image)
	}
	mountsConfig := false
	for _, v := range dex.Volumes {
		if strings.Contains(v, "dex/config.yaml") {
			mountsConfig = true
		}
	}
	if !mountsConfig {
		t.Errorf("dex service does not mount deploy/compose/dex/config.yaml; volumes=%v", dex.Volumes)
	}

	cfgPath := filepath.Join(repoRoot(t), "deploy", "compose", "dex", "config.yaml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read dex config %s: %v", cfgPath, err)
	}
	body := string(raw)
	for _, want := range []string{"issuer:", "staticClients", "podium-registry", "staticPasswords", "alice@acme.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("dex config missing %q (the device-code client / static user §13.1.1 requires)", want)
		}
	}
}

// TestOrg_1_BootstrapService asserts the one-shot bootstrap container creates
// the MinIO bucket and runs after MinIO is healthy.
func TestDeployCompose_BootstrapService(t *testing.T) {
	t.Parallel()
	cf := loadComposeFile(t)
	bs, ok := cf.Services["bootstrap"]
	if !ok {
		t.Fatal("no bootstrap service")
	}
	if dep, ok := bs.DependsOn["minio"]; !ok || dep.Condition != "service_healthy" {
		t.Errorf("bootstrap should depend_on minio with service_healthy, got %+v", bs.DependsOn["minio"])
	}
	joined := commandString(bs.Entrypoint) + "\n" + commandString(bs.Command)
	if !strings.Contains(joined, "mc mb") {
		t.Errorf("bootstrap entrypoint does not create the MinIO bucket (no `mc mb`): %q", joined)
	}
}
