package source

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.6 source types — Local exposes the filesystem path as an
// fs.FS rooted at the configured path.
func TestLocal_SnapshotExposesFilesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	testharness.WriteTree(t, dir,
		testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: "---\ntype: context\nversion: 1.0.0\n---\nbody\n"},
	)
	snap, err := Local{}.Snapshot(context.Background(), LayerConfig{Path: dir})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	data, err := fs.ReadFile(snap.Files, "x/ARTIFACT.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) == "" {
		t.Errorf("empty manifest body")
	}
}

// Spec: §4.6 — Local without path: fails with ErrInvalidConfig.
func TestLocal_RequiresPath(t *testing.T) {
	t.Parallel()
	_, err := Local{}.Snapshot(context.Background(), LayerConfig{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v, want ErrInvalidConfig", err)
	}
}

// Spec: §6.10 namespace — missing path returns ErrSourceUnreachable
// (maps to ingest.source_unreachable).
// Matrix: §6.10 (ingest.source_unreachable)
func TestLocal_MissingPathFailsSourceUnreachable(t *testing.T) {
	t.Parallel()
	_, err := Local{}.Snapshot(context.Background(), LayerConfig{Path: "/nonexistent/path/qqqq"})
	if !errors.Is(err, ErrSourceUnreachable) {
		t.Fatalf("got %v, want ErrSourceUnreachable", err)
	}
}

// Spec: §7.3.1 Ingestion triggers — Git source declares webhook trigger.
func TestGit_DeclaresWebhookTrigger(t *testing.T) {
	t.Parallel()
	if (Git{}).Trigger() != TriggerWebhook {
		t.Errorf("Git.Trigger = %s, want %s", (Git{}).Trigger(), TriggerWebhook)
	}
}

// Spec: §4.6 — Git source requires repo and ref.
func TestGit_RequiresRepoAndRef(t *testing.T) {
	t.Parallel()
	_, err := Git{}.Snapshot(context.Background(), LayerConfig{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing repo: got %v, want ErrInvalidConfig", err)
	}
	_, err = Git{}.Snapshot(context.Background(), LayerConfig{Repo: "git@github.com:x/y"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing ref: got %v, want ErrInvalidConfig", err)
	}
}
