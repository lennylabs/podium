package e2e

// End-to-end coverage for the §13.12 server-side config-file format through the
// real `podium` binary: the top-level `registry:` block (F-13.12.2), ${ENV_VAR}
// interpolation (F-13.12.1), the section-relative store/object keys and the
// vector/identity sub-keys (F-13.12.4, F-13.12.5), the refuse-to-start contract
// for a selected backend missing required values (F-13.12.10), and the
// standalone public default for endpoint-registered layers (F-13.12.15).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// §13.12 (F-13.12.1, F-13.12.2, F-13.12.4, F-13.12.5): a registry.yaml written
// as the documented config-file example resolves end-to-end. `config show`
// reflects the nested keys, the interpolated value, and registry.yaml as the
// source. If the registry: nesting failed to parse, none of these would
// appear.
func TestRegistryConfig_SpecExampleNestedInterpolation(t *testing.T) {
	t.Parallel()
	cfgDir := t.TempDir()
	cfgFile := filepath.Join(cfgDir, "registry.yaml")
	body := "" +
		"registry:\n" +
		"  store:\n" +
		"    type: postgres\n" +
		"    dsn: ${PODIUM_TEST_PG_DSN}\n" +
		"  object_store:\n" +
		"    type: s3\n" +
		"    bucket: acme-podium\n" +
		"    region: us-west-2\n" +
		"  vector_backend:\n" +
		"    type: pinecone\n" +
		"    api_key: ${PODIUM_TEST_PCN_KEY}\n" +
		"    index: acme-prod\n" +
		"    namespace: ${PODIUM_TEST_NS}\n" +
		"  identity_provider:\n" +
		"    type: oauth-device-code\n" +
		"    audience: https://podium.acme.com\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	// Backend selectors left empty so registry.yaml is the source; only the
	// interpolation variables carry values.
	env := []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_TEST_PG_DSN=postgres://alice:pw@db/podium",
		"PODIUM_TEST_PCN_KEY=pcn-secret",
		"PODIUM_TEST_NS=tenant-acme",
		"PODIUM_REGISTRY_STORE=", "PODIUM_OBJECT_STORE=", "PODIUM_VECTOR_BACKEND=",
		"PODIUM_S3_BUCKET=", "PODIUM_S3_REGION=", "PODIUM_PINECONE_INDEX=",
		"PODIUM_PINECONE_NAMESPACE=", "PODIUM_IDENTITY_PROVIDER=", "PODIUM_OAUTH_AUDIENCE=",
	}
	// spec §7.7 (F-7.7.2): the backend selectors are §13.12 server settings,
	// surfaced by `config show --server`.
	res := runPodium(t, "", env, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	out := res.Stdout
	for _, want := range []string{
		"postgres",                // store.type
		"acme-podium",             // object_store.bucket (F-13.12.5)
		"us-west-2",               // object_store.region
		"pinecone",                // vector_backend.type
		"acme-prod",               // vector_backend.index (F-13.12.4)
		"tenant-acme",             // namespace, ${PODIUM_TEST_NS} interpolated (F-13.12.1)
		"oauth-device-code",       // identity_provider.type (F-13.12.4)
		"https://podium.acme.com", // identity_provider.audience
		"registry.yaml",           // source attribution
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config show output missing %q:\n%s", want, out)
		}
	}
	// The interpolated secret DSN must be redacted, never printed literally.
	if strings.Contains(out, "postgres://alice:pw@db/podium") {
		t.Errorf("config show leaked the interpolated DSN secret:\n%s", out)
	}
}

// §13.12 line 347 (F-13.12.10): selecting the s3 object store without its
// required bucket refuses startup, naming the missing key.
func TestRegistryConfig_S3MissingBucketRefusesStart(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"a/ARTIFACT.md": contextArtifact("a")})
	bind := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	res := runPodium(t, "", []string{
		"HOME=" + t.TempDir(),
		"PODIUM_OBJECT_STORE=s3",
		"PODIUM_S3_BUCKET=",
	}, "serve", "--standalone", "--layer-path", reg, "--bind", bind)
	if res.Exit == 0 {
		t.Fatalf("expected non-zero exit (refuse to start)\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout+res.Stderr, "PODIUM_S3_BUCKET") {
		t.Errorf("startup error should name PODIUM_S3_BUCKET; got:\n%s\n%s", res.Stdout, res.Stderr)
	}
}

// §13.10 / §13.12 (F-13.12.15): a standalone deployment (no identity provider)
// defaults an endpoint-registered admin layer to public when no explicit
// visibility is supplied.
func TestRegistryConfig_StandalonePublicDefault(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "") // standalone, no identity provider, empty registry

	st, body := postJSON(t, srv.BaseURL+"/v1/layers", map[string]any{
		"id":          "team-shared",
		"source_type": "local",
		"local_path":  "/tmp/x",
	})
	if st != 201 {
		t.Fatalf("POST /v1/layers = %d, want 201\nbody: %s", st, body)
	}

	// store.LayerConfig has no JSON tags, so fields serialize under Go names.
	var resp struct {
		Layers []struct {
			ID     string `json:"ID"`
			Public bool   `json:"Public"`
		} `json:"layers"`
	}
	getJSON(t, srv.BaseURL+"/v1/layers", &resp)
	found := false
	for _, l := range resp.Layers {
		if l.ID == "team-shared" {
			found = true
			if !l.Public {
				t.Errorf("standalone-registered layer Public = false, want true (§13.10 standalone default)")
			}
		}
	}
	if !found {
		t.Errorf("team-shared layer not listed: %+v", resp.Layers)
	}
}
