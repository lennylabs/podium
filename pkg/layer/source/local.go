package source

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Local is the built-in filesystem source provider (§4.6 source types).
type Local struct{}

// ID returns "local".
func (Local) ID() string { return "local" }

// Trigger returns TriggerManual; local sources re-scan on demand via
// `podium layer reingest <id>` (§7.3.1).
func (Local) Trigger() TriggerModel { return TriggerManual }

// Snapshot opens the layer's configured filesystem path and returns a
// Snapshot exposing it as an fs.FS.
func (Local) Snapshot(_ context.Context, cfg LayerConfig) (*Snapshot, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("%w: local source requires path", ErrInvalidConfig)
	}
	// spec: §6.10 — a path that cannot be stat'd is unreachable whatever the
	// reason. A permission failure is the same condition as a missing
	// directory, so it carries ingest.source_unreachable rather than falling
	// through unclassified to registry.unavailable.
	if _, err := os.Stat(cfg.Path); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrSourceUnreachable, cfg.Path, err)
	}
	return &Snapshot{
		Reference: cfg.Path,
		Files:     os.DirFS(cfg.Path),
		CreatedAt: time.Now().UTC(),
	}, nil
}
