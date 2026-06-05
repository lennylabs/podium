package serverboot

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// startStoreRetentionScheduler runs the §8.4 store-level retention sweeps
// on a cadence: it purges deprecated artifact versions older than the
// deprecated-version window ("90 days after the deprecation flag is set")
// and hard-deletes soft-deleted layers and their artifacts once they pass
// the owner-unregistered recovery window ("30 days (artifacts
// soft-deleted, recoverable via admin)").
//
// The scheduler is best-effort: a sweep failure logs a warning and the
// next tick retries. Disabled when the interval is non-positive.
func startStoreRetentionScheduler(ctx context.Context, cfg *Config, st store.Store) {
	if st == nil {
		return
	}
	interval := time.Duration(cfg.storeRetentionInterval) * time.Second
	if interval <= 0 {
		return
	}
	deprecatedWindow := time.Duration(cfg.deprecatedRetentionDays) * 24 * time.Hour
	layerWindow := time.Duration(cfg.layerRecoveryDays) * 24 * time.Hour
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		// An immediate pass, then one per tick, until ctx is cancelled.
		for {
			runStoreRetentionOnce(ctx, st, deprecatedWindow, layerWindow)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	log.Printf("store retention scheduler running (interval=%ds, deprecated=%dd, layer recovery=%dd)",
		cfg.storeRetentionInterval, cfg.deprecatedRetentionDays, cfg.layerRecoveryDays)
}

func runStoreRetentionOnce(ctx context.Context, st store.Store, deprecatedWindow, layerWindow time.Duration) {
	now := time.Now().UTC()
	if deprecatedWindow > 0 {
		if n, err := st.PurgeDeprecatedManifests(ctx, now.Add(-deprecatedWindow)); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("store retention: purge deprecated versions: %v", err)
			}
		} else if n > 0 {
			log.Printf("store retention: purged %d deprecated artifact version(s)", n)
		}
	}
	if layerWindow > 0 {
		if n, err := st.PurgeExpiredLayerDeletions(ctx, now.Add(-layerWindow)); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("store retention: purge expired layer deletions: %v", err)
			}
		} else if n > 0 {
			log.Printf("store retention: purged %d expired soft-deleted layer(s)", n)
		}
	}
}
