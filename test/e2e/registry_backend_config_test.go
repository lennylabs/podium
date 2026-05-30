package e2e

// End-to-end coverage through the real `podium` binary for the §13.12
// storage/vector backend config conformance fixes: the S3 region requirement
// (F-13.12.9), the force-path-style flag (F-13.12.8), and the Pinecone
// namespace default (F-13.12.11). Self-embedding (F-13.12.6) is exercised by
// the vector-backends suite (tests 3, 10, 15).

import (
	"encoding/json"
	"testing"
)

// rcShowSetting runs `podium config show --json` and returns the named setting
// row. It pins HOME so the resolution is hermetic.
func rcShowSetting(t *testing.T, env []string, name string) (value, source string, found bool) {
	t.Helper()
	env = append([]string{"HOME=" + t.TempDir()}, env...)
	res := runPodium(t, "", env, "config", "show", "--json")
	if res.Exit != 0 {
		t.Fatalf("config show --json exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	var parsed struct {
		Settings []struct {
			Name   string `json:"Name"`
			Value  string `json:"Value"`
			Source string `json:"Source"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		t.Fatalf("parse config show json: %v\n%s", err, res.Stdout)
	}
	for _, s := range parsed.Settings {
		if s.Name == name {
			return s.Value, s.Source, true
		}
	}
	return "", "", false
}

// §13.12 (F-13.12.9): selecting the s3 object store without its required region
// refuses startup, naming the missing key. The bucket is supplied so the error
// isolates the region.
func TestRegistryConfig_S3MissingRegionRefusesStart(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "PODIUM_S3_REGION",
		"PODIUM_OBJECT_STORE=s3",
		"PODIUM_S3_BUCKET=acme-podium",
		"PODIUM_S3_REGION=",
	)
}

// §13.12 (F-13.12.12): selecting weaviate-cloud without its required collection
// refuses startup, naming the missing key.
func TestRegistryConfig_WeaviateMissingCollectionRefusesStart(t *testing.T) {
	t.Parallel()
	reg := vbReg(t)
	vbExpectRefuseToStart(t, reg, "PODIUM_WEAVIATE_COLLECTION",
		"PODIUM_VECTOR_BACKEND=weaviate-cloud",
		"PODIUM_WEAVIATE_URL=https://w.example.com",
		"PODIUM_WEAVIATE_API_KEY=wv-test",
		"PODIUM_WEAVIATE_COLLECTION=",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
	)
}

// §13.12 (F-13.12.8): PODIUM_S3_FORCE_PATH_STYLE is surfaced in config show and
// reflects the env value.
func TestRegistryConfig_S3ForcePathStyleConfigShow(t *testing.T) {
	t.Parallel()
	v, _, found := rcShowSetting(t, []string{"PODIUM_S3_FORCE_PATH_STYLE=true"}, "object_store.s3_force_path_style")
	if !found {
		t.Fatal("object_store.s3_force_path_style missing from config show")
	}
	if v != "true" {
		t.Errorf("s3_force_path_style = %q, want true", v)
	}
}

// §13.12 (F-13.12.11): the Pinecone namespace defaults to "default" when unset.
func TestRegistryConfig_PineconeNamespaceDefaultConfigShow(t *testing.T) {
	t.Parallel()
	v, _, found := rcShowSetting(t, []string{"PODIUM_PINECONE_NAMESPACE="}, "vector_backend.namespace")
	if !found {
		t.Fatal("vector_backend.namespace missing from config show")
	}
	if v != "default" {
		t.Errorf("namespace (unset) = %q, want default", v)
	}
}
