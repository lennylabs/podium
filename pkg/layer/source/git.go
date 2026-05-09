package source

import (
	"context"
	"errors"
	"fmt"
)

// Git is the built-in git source provider stub (§4.6 source types).
// Phase 6 will swap this for a real go-git or shell-out implementation;
// the SPI surface is correct now so tests can drive against it.
type Git struct{}

// ID returns "git".
func (Git) ID() string { return "git" }

// Trigger returns TriggerWebhook; ingest fires on the configured
// provider's webhook (§7.3.1).
func (Git) Trigger() TriggerModel { return TriggerWebhook }

// Snapshot is a stub: it validates the layer config and returns a
// not-implemented sentinel.
func (Git) Snapshot(_ context.Context, cfg LayerConfig) (*Snapshot, error) {
	if cfg.Repo == "" {
		return nil, fmt.Errorf("%w: git source requires repo", ErrInvalidConfig)
	}
	if cfg.Ref == "" {
		return nil, fmt.Errorf("%w: git source requires ref", ErrInvalidConfig)
	}
	return nil, errors.New("git source: phase 6 implementation pending")
}
