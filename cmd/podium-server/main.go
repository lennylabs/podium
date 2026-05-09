// Command podium-server runs the Podium registry as a long-lived
// HTTP server. The standalone deployment (§13.10) bundles all
// dependencies (SQLite + filesystem object store + ONNX embeddings);
// the standard deployment (§13.1) wires Postgres + S3-compatible
// object storage + an OIDC IdP via env vars per §13.12.
//
// The binary reads its configuration from PODIUM_REGISTRY_STORE,
// PODIUM_OBJECT_STORE, PODIUM_VECTOR_BACKEND, PODIUM_EMBEDDING_PROVIDER,
// PODIUM_IDENTITY_PROVIDER, plus the per-backend env vars from §13.12.
// Default behavior matches §13.10's zero-flag standalone bootstrap:
// SQLite + filesystem object store + embedded-onnx + no auth bound on
// 127.0.0.1:8080.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		return err
	}

	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Standalone bootstrap: ensure the default tenant exists so
	// initial requests don't fail on missing-tenant lookups (§13.10
	// auto-bootstrap).
	const tenantID = "default"
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID})

	registry := core.New(st, tenantID, []layer.Layer{})
	srv := server.New(registry,
		bootstrapOptions(cfg)...,
	)

	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())

	httpServer := &http.Server{
		Addr:              cfg.bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("podium-server listening on %s (mode=%s)", cfg.bind, cfg.modeBanner())
	return httpServer.ListenAndServe()
}

type config struct {
	bind             string
	publicMode       bool
	identityProvider string
	storeType        string
	sqlitePath       string
	postgresDSN      string
}

func loadConfig() *config {
	c := &config{
		bind:             envDefault("PODIUM_BIND", "127.0.0.1:8080"),
		publicMode:       isTrue(os.Getenv("PODIUM_PUBLIC_MODE")),
		identityProvider: os.Getenv("PODIUM_IDENTITY_PROVIDER"),
		storeType:        envDefault("PODIUM_REGISTRY_STORE", "sqlite"),
		sqlitePath:       os.Getenv("PODIUM_SQLITE_PATH"),
		postgresDSN:      os.Getenv("PODIUM_POSTGRES_DSN"),
	}
	if c.sqlitePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.sqlitePath = filepath.Join(home, ".podium", "standalone", "podium.db")
		}
	}
	return c
}

func (c *config) validate() error {
	startup := server.StartupConfig{
		PublicMode:       c.publicMode,
		IdentityProvider: c.identityProvider,
	}
	if err := startup.Validate(); err != nil {
		return err
	}
	if c.storeType == "postgres" && c.postgresDSN == "" {
		return fmt.Errorf("PODIUM_POSTGRES_DSN is required when PODIUM_REGISTRY_STORE=postgres")
	}
	return nil
}

func (c *config) modeBanner() string {
	if c.publicMode {
		return "public"
	}
	if c.identityProvider != "" {
		return c.identityProvider
	}
	return "standalone"
}

func openStore(c *config) (store.Store, error) {
	switch c.storeType {
	case "sqlite":
		dir := filepath.Dir(c.sqlitePath)
		_ = os.MkdirAll(dir, 0o755)
		return store.OpenSQLite(c.sqlitePath)
	case "memory":
		return store.NewMemory(), nil
	case "postgres":
		return store.OpenPostgres(c.postgresDSN)
	}
	return nil, fmt.Errorf("unknown PODIUM_REGISTRY_STORE: %s", c.storeType)
}

func bootstrapOptions(c *config) []server.Option {
	out := []server.Option{}
	if c.publicMode {
		out = append(out, server.WithPublicMode())
	}
	return out
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
