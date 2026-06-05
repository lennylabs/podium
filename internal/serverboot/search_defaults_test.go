package serverboot

import (
	"os"
	"testing"
)

// clearSearchEnv removes every env var that influences the §9.1 search /
// embedding defaults so each case observes the zero-config default for the
// selected deployment mode. The vars are truly unset (not set-empty) so
// LoadConfig sees them as absent, distinguishing "unset" (apply default)
// from "explicitly empty" (§13.12 disabled).
func clearSearchEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PODIUM_VECTOR_BACKEND", "PODIUM_EMBEDDING_PROVIDER",
		"PODIUM_NO_EMBEDDINGS", "PODIUM_REGISTRY_STORE",
	} {
		orig, had := os.LookupEnv(k)
		if had {
			t.Cleanup(func() { os.Setenv(k, orig) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
}

// Spec: §9.1 / §13.10 — a zero-config standalone deployment defaults
// to the sqlite-vec backend with the ollama embedder so hybrid search is on
// out of the box, matching the §9.1 table's standalone default column.
func TestLoadConfig_StandaloneDefaultsHybrid(t *testing.T) {
	clearSearchEnv(t)
	c := LoadConfig()
	if c.vectorBackend != "sqlite-vec" {
		t.Errorf("vectorBackend = %q, want sqlite-vec (standalone default)", c.vectorBackend)
	}
	if c.embeddingProvider != "ollama" {
		t.Errorf("embeddingProvider = %q, want ollama (standalone default)", c.embeddingProvider)
	}
}

// Spec: §9.1 / §13 — a standard (Postgres) deployment defaults to
// pgvector with the openai embedder.
func TestLoadConfig_StandardDefaultsHybrid(t *testing.T) {
	clearSearchEnv(t)
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	c := LoadConfig()
	if c.vectorBackend != "pgvector" {
		t.Errorf("vectorBackend = %q, want pgvector (standard default)", c.vectorBackend)
	}
	if c.embeddingProvider != "openai" {
		t.Errorf("embeddingProvider = %q, want openai (standard default)", c.embeddingProvider)
	}
}

// Spec: §13.10 — PODIUM_NO_EMBEDDINGS forces the BM25-only fallback
// the spec frames as --no-embeddings, overriding the per-mode defaults.
func TestLoadConfig_NoEmbeddingsForcesBM25Only(t *testing.T) {
	clearSearchEnv(t)
	t.Setenv("PODIUM_NO_EMBEDDINGS", "true")
	c := LoadConfig()
	if c.vectorBackend != "none" || c.embeddingProvider != "none" {
		t.Errorf("with PODIUM_NO_EMBEDDINGS: vector=%q embed=%q, want none/none", c.vectorBackend, c.embeddingProvider)
	}
}

// Spec: §13.12 — an explicitly empty PODIUM_EMBEDDING_PROVIDER
// disables embedding generation rather than triggering the mode default, so
// an operator can opt into BM25-only without PODIUM_NO_EMBEDDINGS.
func TestLoadConfig_ExplicitEmptyDisables(t *testing.T) {
	clearSearchEnv(t)
	t.Setenv("PODIUM_VECTOR_BACKEND", "")
	t.Setenv("PODIUM_EMBEDDING_PROVIDER", "")
	c := LoadConfig()
	if c.vectorBackend != "" || c.embeddingProvider != "" {
		t.Errorf("explicit empty: vector=%q embed=%q, want both empty (disabled)", c.vectorBackend, c.embeddingProvider)
	}
}

// Spec: §9.1 / §4.7 — embeddingProviderExplicit records whether the
// operator chose a provider (env set, even to empty) versus inheriting the
// per-mode default, so a self-embedding backend can tell an override from a
// default.
func TestLoadConfig_EmbeddingProviderExplicitFlag(t *testing.T) {
	t.Run("defaulted is not explicit", func(t *testing.T) {
		clearSearchEnv(t)
		c := LoadConfig()
		if c.embeddingProviderExplicit {
			t.Errorf("embeddingProviderExplicit = true, want false (mode default applied)")
		}
	})
	t.Run("explicit env is explicit", func(t *testing.T) {
		clearSearchEnv(t)
		t.Setenv("PODIUM_EMBEDDING_PROVIDER", "openai")
		c := LoadConfig()
		if !c.embeddingProviderExplicit {
			t.Errorf("embeddingProviderExplicit = false, want true (explicit env)")
		}
	})
	t.Run("explicit empty is explicit", func(t *testing.T) {
		clearSearchEnv(t)
		t.Setenv("PODIUM_EMBEDDING_PROVIDER", "")
		c := LoadConfig()
		if !c.embeddingProviderExplicit {
			t.Errorf("embeddingProviderExplicit = false, want true (explicit empty disables)")
		}
	})
}

// Spec: §9.1 — explicit env values keep precedence over the mode
// defaults so an operator can run a managed backend in either mode.
func TestLoadConfig_ExplicitBackendsWin(t *testing.T) {
	clearSearchEnv(t)
	t.Setenv("PODIUM_VECTOR_BACKEND", "pinecone")
	t.Setenv("PODIUM_EMBEDDING_PROVIDER", "voyage")
	c := LoadConfig()
	if c.vectorBackend != "pinecone" {
		t.Errorf("vectorBackend = %q, want pinecone (explicit env)", c.vectorBackend)
	}
	if c.embeddingProvider != "voyage" {
		t.Errorf("embeddingProvider = %q, want voyage (explicit env)", c.embeddingProvider)
	}
}
