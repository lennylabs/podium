package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

const confinedContextArtifact = "---\ntype: context\nversion: 1.0.0\ndescription: Pay an invoice\nsensitivity: low\n---\n\nbody\n"

// writeConfinementTree builds a layer directory holding one artifact and, when
// escaping is true, one bundled resource symlinked to a file above the root.
// It returns the parent and the layer root.
func writeConfinementTree(t *testing.T, escaping bool) (parent, root string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "layer")
	dir := filepath.Join(root, "finance", "pay-invoice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ARTIFACT.md"), []byte(confinedContextArtifact), 0o644); err != nil {
		t.Fatalf("WriteFile ARTIFACT.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("outside secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if escaping {
		if err := os.Symlink(filepath.Join("..", "..", "..", "outside.txt"), filepath.Join(dir, "leak.txt")); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
	}
	return parent, root
}

// confinedReingestRunner wires the ingest pipeline against the built-in local
// source provider, mirroring what serverboot installs.
func confinedReingestRunner(st store.Store) server.ReingestRunner {
	return func(ctx context.Context, lc store.LayerConfig, _ *server.BreakGlass) (*ingest.Result, error) {
		return ingest.SourceIngestWithOptions(ctx, st, source.Local{}, lc, ingest.SourceIngestOptions{})
	}
}

// Spec: §6.10 — a reingest the confinement refuses is answered
// 502 ingest.source_unreachable with retryable false, rather than falling
// through unclassified to a 500 registry.unavailable the client would retry.
// The body names no host path.
// Matrix: §6.10 (ingest.source_unreachable)
func TestReingest_EscapingResourceEnvelope(t *testing.T) {
	t.Parallel()
	parent, root := writeConfinementTree(t, true)
	st := newLayerWriteStore(t)
	seedLayer(t, st, store.LayerConfig{ID: "own", SourceType: "local", LocalPath: root})
	base := newLayerWriteServer(t, st, true, aliceID, confinedReingestRunner(st))

	resp, body := mustPost(t, base, "/v1/layers/reingest?id=own", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("reingest status = %d, want %d: %s", resp.StatusCode, http.StatusBadGateway, body)
	}
	if code := errCode(t, body); code != "ingest.source_unreachable" {
		t.Errorf("code = %q, want ingest.source_unreachable", code)
	}
	var env struct {
		Retryable bool `json:"retryable"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	if env.Retryable {
		t.Errorf("retryable = true, want false: %s", body)
	}
	if strings.Contains(string(body), parent) || strings.Contains(string(body), root) {
		t.Errorf("envelope leaks a host path: %s", body)
	}
}

// Spec: §7.3.1 — the filesystem bootstrap builds its layer tree through the
// same confinement constructor, so a bootstrapped layer holding an escaping
// symbolic link fails to construct rather than reading the target through.
// This is the only level at which server.NewFromFilesystem is reached; its
// sole non-test caller in the module is internal/testharness/registryharness,
// so no podium serve invocation exercises it.
func TestNewFromFilesystem_IngestIsConfined(t *testing.T) {
	t.Parallel()

	t.Run("escaping", func(t *testing.T) {
		t.Parallel()
		_, root := writeConfinementTree(t, true)
		srv, err := server.NewFromFilesystem(root)
		if err == nil {
			t.Fatalf("NewFromFilesystem accepted a tree holding an escaping symbolic link")
		}
		if srv != nil {
			t.Errorf("refused bootstrap returned a server")
		}
		if !errors.Is(err, source.ErrSourceUnreachable) {
			t.Errorf("error %q is not source.ErrSourceUnreachable", err)
		}
	})

	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		_, root := writeConfinementTree(t, false)
		srv, err := server.NewFromFilesystem(root)
		if err != nil {
			t.Fatalf("NewFromFilesystem: %v", err)
		}
		if srv == nil {
			t.Fatalf("NewFromFilesystem returned no server")
		}
		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		body := mustGet(t, ts.URL, "/v1/catalog")
		if !strings.Contains(string(body), "finance/pay-invoice") {
			t.Errorf("bootstrapped registry does not serve the in-root artifact: %s", body)
		}
	})
}
