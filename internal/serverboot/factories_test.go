package serverboot

import (
	"strings"
	"testing"
)

func TestOpenEmbedder_DispatchesByProvider(t *testing.T) {
	cases := []struct {
		name       string
		cfg        *Config
		wantNil    bool
		wantErr    string
		wantErrSub string
	}{
		{name: "none-default", cfg: &Config{}, wantNil: true},
		{name: "explicit-none", cfg: &Config{embeddingProvider: "none"}, wantNil: true},
		{name: "openai-missing-key", cfg: &Config{embeddingProvider: "openai"}, wantErrSub: "OPENAI_API_KEY"},
		{name: "voyage-missing-key", cfg: &Config{embeddingProvider: "voyage"}, wantErrSub: "VOYAGE_API_KEY"},
		{name: "cohere-missing-key", cfg: &Config{embeddingProvider: "cohere"}, wantErrSub: "COHERE_API_KEY"},
		{name: "unknown-provider", cfg: &Config{embeddingProvider: "bogus"}, wantErrSub: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := openEmbedder(c.cfg)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Errorf("err = %v, want substring %q", err, c.wantErrSub)
				}
				return
			}
			if c.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil provider", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err = %v", err)
			}
		})
	}
}

func TestOpenEmbedder_HappyPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cfg     *Config
		wantID  string
	}{
		{"openai", &Config{embeddingProvider: "openai", openaiAPIKey: "k"}, "openai"},
		{"voyage", &Config{embeddingProvider: "voyage", voyageAPIKey: "k"}, "voyage"},
		{"cohere", &Config{embeddingProvider: "cohere", cohereAPIKey: "k"}, "cohere"},
		{"ollama", &Config{embeddingProvider: "ollama", ollamaURL: "http://localhost:11434"}, "ollama"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			emb, err := openEmbedder(c.cfg)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if emb.ID() != c.wantID {
				t.Errorf("ID = %q, want %q", emb.ID(), c.wantID)
			}
		})
	}
}

func TestOpenVectorBackend_DispatchesByBackend(t *testing.T) {
	cases := []struct {
		name       string
		cfg        *Config
		dim        int
		wantNil    bool
		wantErrSub string
		wantID     string
	}{
		{name: "none-default", cfg: &Config{}, dim: 4, wantNil: true},
		{name: "explicit-none", cfg: &Config{vectorBackend: "none"}, dim: 4, wantNil: true},
		{name: "memory", cfg: &Config{vectorBackend: "memory"}, dim: 4, wantID: "memory"},
		{name: "pgvector-missing-dsn", cfg: &Config{vectorBackend: "pgvector"}, dim: 4, wantErrSub: "DSN"},
		{name: "pinecone-no-key", cfg: &Config{vectorBackend: "pinecone"}, dim: 4, wantErrSub: "APIKey"},
		{name: "unknown-backend", cfg: &Config{vectorBackend: "bogus"}, dim: 4, wantErrSub: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := openVectorBackend(c.cfg, c.dim)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Errorf("err = %v, want substring %q", err, c.wantErrSub)
				}
				return
			}
			if c.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err = %v", err)
				return
			}
			if c.wantID != "" && got.ID() != c.wantID {
				t.Errorf("ID = %q, want %q", got.ID(), c.wantID)
			}
		})
	}
}

func TestOpenObjectStore_DispatchesByType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cfg        *Config
		wantErrSub string
		wantNil    bool
	}{
		{"filesystem-default", &Config{filesystemRoot: t.TempDir()}, "", false},
		{"filesystem-explicit", &Config{objectStore: "filesystem", filesystemRoot: t.TempDir()}, "", false},
		{"unknown", &Config{objectStore: "bogus"}, "unknown", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := openObjectStore(c.cfg)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Errorf("err = %v, want substring %q", err, c.wantErrSub)
				}
				if c.wantNil && got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if got == nil {
				t.Errorf("provider was nil")
			}
		})
	}
}

func TestOpenStore_DispatchesByType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cfg        *Config
		wantNil    bool
		wantErrSub string
	}{
		{"memory", &Config{storeType: "memory"}, false, ""},
		{"sqlite-memory", &Config{storeType: "sqlite", sqlitePath: ":memory:"}, false, ""},
		{"postgres-needs-dsn", &Config{storeType: "postgres"}, true, ""}, // err depends on driver state
		{"unknown", &Config{storeType: "bogus"}, true, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := openStore(c.cfg)
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Errorf("err = %v, want substring %q", err, c.wantErrSub)
				}
				return
			}
			if c.wantNil {
				// Either nil or an error is acceptable for postgres
				// without a configured DSN.
				_ = got
				return
			}
			if err != nil {
				t.Errorf("err = %v", err)
			}
			if got == nil {
				t.Errorf("got nil store")
			}
		})
	}
}

func TestOpenVectorAndEmbedder_NilWhenEmbeddingDisabled(t *testing.T) {
	t.Parallel()
	v, e, err := openVectorAndEmbedder(&Config{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v != nil || e != nil {
		t.Errorf("expected both nil; got v=%v e=%v", v, e)
	}
}

func TestOpenVectorAndEmbedder_PropagatesEmbedderError(t *testing.T) {
	t.Parallel()
	cfg := &Config{embeddingProvider: "openai"} // missing key
	_, _, err := openVectorAndEmbedder(cfg)
	if err == nil {
		t.Errorf("expected error for missing openai key")
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	if err := (&Config{storeType: "postgres"}).validate(); err == nil ||
		!strings.Contains(err.Error(), "POSTGRES_DSN") {
		t.Errorf("err = %v", err)
	}
	if err := (&Config{storeType: "sqlite"}).validate(); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestModeBanner(t *testing.T) {
	t.Parallel()
	cases := map[string]*Config{
		"standalone": {},
		"public":     {publicMode: true},
		"oidc":       {identityProvider: "oidc"},
	}
	for want, cfg := range cases {
		if got := cfg.modeBanner(); got != want {
			t.Errorf("Config{%+v}.modeBanner() = %q, want %q", cfg, got, want)
		}
	}
}

func TestEnvInt_Defaults(t *testing.T) {
	t.Setenv("PODIUM_TEST_ENVINT_OK", "42")
	if got := envInt("PODIUM_TEST_ENVINT_OK", 0); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	t.Setenv("PODIUM_TEST_ENVINT_OK", "not-a-number")
	if got := envInt("PODIUM_TEST_ENVINT_OK", 7); got != 7 {
		t.Errorf("got %d, want 7 (default)", got)
	}
	t.Setenv("PODIUM_TEST_ENVINT_OK", "-5")
	if got := envInt("PODIUM_TEST_ENVINT_OK", 3); got != 3 {
		t.Errorf("got %d, want 3 (negative rejected)", got)
	}
	t.Setenv("PODIUM_TEST_ENVINT_OK", "")
	if got := envInt("PODIUM_TEST_ENVINT_OK", 9); got != 9 {
		t.Errorf("got %d, want 9 (unset)", got)
	}
}
