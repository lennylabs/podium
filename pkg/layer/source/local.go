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
	if _, err := os.Stat(cfg.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSourceUnreachable, cfg.Path)
		}
		return nil, err
	}
	return &Snapshot{
		Reference: cfg.Path,
		Files:     os.DirFS(cfg.Path),
		CreatedAt: time.Now().UTC(),
	}, nil
}
