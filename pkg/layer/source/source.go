// Package source defines the LayerSourceProvider SPI (spec §9.1, §4.6
// "Source types"), plus the built-in local and git providers.
//
// A provider is responsible for snapshotting a layer at a stable
// reference, exposing the artifact tree as a filesystem-like view, and
// declaring its trigger model (webhook / poll / push).
package source

import (
	"context"
	"errors"
	"io/fs"
	"time"
)

// TriggerModel declares how the registry should ingest from a source.
type TriggerModel string

// TriggerModel values per §7.3.1 Ingestion triggers.
const (
	TriggerWebhook TriggerModel = "webhook"
	TriggerPoll    TriggerModel = "poll"
	TriggerPush    TriggerModel = "push"
	TriggerManual  TriggerModel = "manual"
)

// Snapshot is one consistent view of a source at a stable reference.
type Snapshot struct {
	// Reference is the source-specific identifier (commit SHA for git,
	// timestamp for local, version ID for object stores).
	Reference string
	// Files exposes the artifact tree as a read-only filesystem.
	Files fs.FS
	// CreatedAt is the snapshot's creation time.
	CreatedAt time.Time
}

// Provider is the SPI implementations satisfy. Methods take a
// context.Context first per §9.3.
type Provider interface {
	// ID returns the provider identifier (e.g., "local", "git").
	ID() string
	// Trigger declares how the registry ingests from this provider.
	Trigger() TriggerModel
	// Snapshot returns the current snapshot at the layer's configured
	// reference.
	Snapshot(ctx context.Context, layerConfig LayerConfig) (*Snapshot, error)
}

// LayerConfig captures the per-layer source options. Implementations
// look at the fields relevant to their type and ignore the rest.
type LayerConfig struct {
	// Generic
	ID         string
	Visibility Visibility

	// Git source
	Repo string
	Ref  string
	Root string

	// Local source
	Path string
}

// Visibility declares who can see a layer (§4.6).
type Visibility struct {
	Public       bool
	Organization bool
	Groups       []string
	Users        []string
}

// Errors returned by providers.
var (
	// ErrSourceUnreachable wraps a transient fetch failure. Maps to
	// ingest.source_unreachable in §6.10.
	ErrSourceUnreachable = errors.New("source: unreachable")
	// ErrInvalidConfig signals an invalid layer config (e.g., git source
	// missing repo:).
	ErrInvalidConfig = errors.New("source: invalid_config")
)
