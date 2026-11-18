package serverboot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// The §13.12 usage settings' defaults and bounds.
const (
	defaultUsageFlushInterval = time.Minute
	minUsageFlushInterval     = time.Second
	defaultUsageRetentionDays = 365
	// usageRetentionEvery is how often the §8.7 retention pass runs after
	// the one at startup. §8.7 fixes it at 24 hours.
	usageRetentionEvery = 24 * time.Hour
	// usageFinalFlushTimeout bounds the flush at a graceful shutdown, so a
	// store that hangs does not hold the process open.
	usageFinalFlushTimeout = 10 * time.Second
)

// usageConfig is the §8.7 configuration from PODIUM_USAGE_FLUSH_INTERVAL and
// PODIUM_USAGE_RETENTION_DAYS. err holds the first value that does not parse
// or is out of range; validate refuses the start with it.
type usageConfig struct {
	flushInterval time.Duration
	retentionDays int
	err           error
}

// usageConfigFromEnv reads the §13.12 usage settings through getenv. An unset
// or blank value takes its default.
//
// Spec: §8.7, §13.12
func usageConfigFromEnv(getenv func(string) string) usageConfig {
	c := usageConfig{flushInterval: defaultUsageFlushInterval, retentionDays: defaultUsageRetentionDays}
	if raw := strings.TrimSpace(getenv("PODIUM_USAGE_FLUSH_INTERVAL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < minUsageFlushInterval {
			c.err = fmt.Errorf("config.invalid: PODIUM_USAGE_FLUSH_INTERVAL must be a duration of at least 1s, got %q", raw)
			return c
		}
		c.flushInterval = d
	}
	if raw := strings.TrimSpace(getenv("PODIUM_USAGE_RETENTION_DAYS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			c.err = fmt.Errorf("config.invalid: PODIUM_USAGE_RETENTION_DAYS must be a positive integer, got %q", raw)
			return c
		}
		c.retentionDays = n
	}
	return c
}

// startUsageCounts runs the §8.7 flush loop and retention pass until ctx is
// cancelled. The returned function flushes once more, for the graceful
// shutdown, under its own deadline because ctx is already cancelled then.
//
// Spec: §8.7
func startUsageCounts(ctx context.Context, cfg usageConfig, st store.Store, counter *core.UsageCounter, c clock.Clock) (finalFlush func()) {
	go usageLoop(ctx, cfg.flushInterval, func(ctx context.Context) {
		if err := counter.Flush(ctx, st); err != nil {
			log.Printf("usage: flush failed; the counts stay buffered for the next flush: %v", err)
		}
	})
	go usageLoop(ctx, usageRetentionEvery, func(ctx context.Context) { runUsageRetention(ctx, st, cfg.retentionDays, c) })
	runUsageRetention(ctx, st, cfg.retentionDays, c)
	return func() {
		fctx, cancel := context.WithTimeout(context.Background(), usageFinalFlushTimeout)
		defer cancel()
		if err := counter.Flush(fctx, st); err != nil {
			log.Printf("usage: final flush failed; the counts since the last flush are lost: %v", err)
		}
	}
}

// usageLoop runs fn every interval until ctx is cancelled.
func usageLoop(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

// runUsageRetention deletes the rows of every day before the retained window,
// the last retentionDays UTC days counting today.
func runUsageRetention(ctx context.Context, st store.Store, retentionDays int, c clock.Clock) {
	today := c.Now().UTC().Truncate(24 * time.Hour)
	cutoff := today.AddDate(0, 0, 1-retentionDays).Format(store.UsageDayLayout)
	n, err := st.DeleteUsageBefore(ctx, cutoff)
	if err != nil {
		log.Printf("usage: retention pass failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("usage: retention pass removed %d rows before %s", n, cutoff)
	}
}
