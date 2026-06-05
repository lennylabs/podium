package serverboot

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// hermeticEnv points PODIUM_CONFIG_FILE at a missing file and HOME at a temp
// dir so LoadConfig does not pick up a developer's ~/.podium/registry.yaml.
func hermeticEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("HOME", t.TempDir())
}

// Spec: §13.12 — PODIUM_PINECONE_NAMESPACE defaults to "default";
// an explicit env value wins.
func TestLoadConfig_PineconeNamespaceDefault(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PODIUM_PINECONE_NAMESPACE", "")
	if got := LoadConfig().pineconeNS; got != "default" {
		t.Errorf("pineconeNS (unset) = %q, want default", got)
	}
	t.Setenv("PODIUM_PINECONE_NAMESPACE", "tenant-acme")
	if got := LoadConfig().pineconeNS; got != "tenant-acme" {
		t.Errorf("pineconeNS (env) = %q, want tenant-acme", got)
	}
}

// Spec: §13.12 — PODIUM_S3_REGION has no implicit default, so an
// unset region resolves empty and is later named by validate() for s3.
func TestLoadConfig_S3RegionNoImplicitDefault(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PODIUM_S3_REGION", "")
	if got := LoadConfig().s3Region; got != "" {
		t.Errorf("s3Region (unset) = %q, want empty (no us-east-1 default)", got)
	}
	t.Setenv("PODIUM_S3_REGION", "eu-west-1")
	if got := LoadConfig().s3Region; got != "eu-west-1" {
		t.Errorf("s3Region (env) = %q, want eu-west-1", got)
	}
}

// Spec: §13.12 — PODIUM_S3_FORCE_PATH_STYLE flows into the config.
func TestLoadConfig_S3ForcePathStyle(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PODIUM_S3_FORCE_PATH_STYLE", "")
	if LoadConfig().s3ForcePathStyle {
		t.Error("s3ForcePathStyle (unset) = true, want false")
	}
	t.Setenv("PODIUM_S3_FORCE_PATH_STYLE", "true")
	if !LoadConfig().s3ForcePathStyle {
		t.Error("s3ForcePathStyle (true) = false, want true")
	}
}

// Spec: §13.12 — the vector collection has no implicit default, so
// an unset weaviate/qdrant collection resolves empty (named by validate()).
func TestLoadConfig_VectorCollectionNoDefault(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PODIUM_WEAVIATE_COLLECTION", "")
	t.Setenv("PODIUM_QDRANT_COLLECTION", "")
	c := LoadConfig()
	if c.weaviateColl != "" {
		t.Errorf("weaviateColl (unset) = %q, want empty (no PodiumArtifacts default)", c.weaviateColl)
	}
	if c.qdrantColl != "" {
		t.Errorf("qdrantColl (unset) = %q, want empty (no podium_artifacts default)", c.qdrantColl)
	}
}

// Spec: §13.12 — vectorSelfEmbeds is true only for a cloud backend
// configured with an inference model / vectorizer.
func TestVectorSelfEmbeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"pinecone + model", Config{vectorBackend: "pinecone", vectorInferenceModel: "m"}, true},
		{"weaviate + vectorizer", Config{vectorBackend: "weaviate-cloud", vectorInferenceModel: "text2vec"}, true},
		{"qdrant + model", Config{vectorBackend: "qdrant-cloud", vectorInferenceModel: "bge"}, true},
		{"pinecone no model", Config{vectorBackend: "pinecone"}, false},
		{"pgvector + stray model ignored", Config{vectorBackend: "pgvector", vectorInferenceModel: "m"}, false},
		{"unset backend + model", Config{vectorInferenceModel: "m"}, false},
	}
	for _, tc := range cases {
		if got := tc.cfg.vectorSelfEmbeds(); got != tc.want {
			t.Errorf("%s: vectorSelfEmbeds() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Spec: §13.12 — openVectorAndEmbedder returns a configured vector
// backend with a nil embedder when the backend self-embeds, so vector search
// is wired without a separate embedding provider.
func TestOpenVectorAndEmbedder_SelfEmbedding(t *testing.T) {
	t.Parallel()
	c := &Config{
		vectorBackend:        "pinecone",
		pineconeKey:          "k",
		pineconeHost:         "https://h.example.com",
		vectorInferenceModel: "multilingual-e5-large",
		// No embedding provider configured.
	}
	v, e, err := openVectorAndEmbedder(c)
	if err != nil {
		t.Fatalf("openVectorAndEmbedder: %v", err)
	}
	if v == nil {
		t.Fatal("vector backend nil; self-embedding must still wire vector search")
	}
	if e != nil {
		t.Errorf("embedder = %v, want nil (backend self-embeds)", e)
	}
	if !vector.SelfEmbeds(v) {
		t.Errorf("opened backend does not report SelfEmbeds()")
	}
}
