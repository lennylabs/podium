package store

import (
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/spi"
)

// UsageLoads is one artifact's §8.7 loads under one layer, summed over a range
// of days. The §3.3 learn-from-usage reranking reads it.
type UsageLoads struct {
	Layer      string
	ArtifactID string
	Loads      int64
}

// ErrInvalidUsageCount is returned by AddUsageCounts when a count names no
// day, layer, artifact, client kind, or harness, carries a day that is not a
// YYYY-MM-DD date, or carries a negative counter.
var ErrInvalidUsageCount = &spi.Error{Code: "registry.invalid_argument", Message: "store: invalid usage count"}

// UsageDayLayout is the layout of UsageCount.Day: the UTC calendar date.
const UsageDayLayout = "2006-01-02"

// UsageCount is one §8.7 row of daily usage totals, keyed by tenant, UTC day,
// layer, artifact canonical ID, client kind, and harness. Loads counts resolved
// load_artifact calls from a client whose kind is not sync, Syncs counts the
// same calls from a sync client, and SearchHits counts searches with a query
// that returned the artifact among their first 10 results.
//
// Spec: §8.7
type UsageCount struct {
	TenantID   string
	Day        string
	Layer      string
	ArtifactID string
	Client     string
	// Harness is the §7.2.2 harness the request reported, or "none".
	Harness    string
	Loads      int64
	Syncs      int64
	SearchHits int64
}

// usageKey is the identity of a UsageCount row within one tenant.
type usageKey struct {
	day, layer, artifact, kind, harness string
}

func (c UsageCount) key() usageKey {
	return usageKey{day: c.Day, layer: c.Layer, artifact: c.ArtifactID, kind: c.Client, harness: c.Harness}
}

// normalizeUsageCounts validates counts and merges the ones that share a key,
// so a backend adds each key once per call. It returns the merged rows with
// TenantID set to tenantID, ordered by key, and drops a row whose counters are
// all zero. Every backend applies it, so the backends store the same rows.
func normalizeUsageCounts(tenantID string, counts []UsageCount) ([]UsageCount, error) {
	merged := map[usageKey]UsageCount{}
	for _, c := range counts {
		if err := validateUsageCount(c); err != nil {
			return nil, err
		}
		if c.Loads == 0 && c.Syncs == 0 && c.SearchHits == 0 {
			continue
		}
		k := c.key()
		m, ok := merged[k]
		if !ok {
			m = UsageCount{TenantID: tenantID, Day: c.Day, Layer: c.Layer, ArtifactID: c.ArtifactID, Client: c.Client, Harness: c.Harness}
		}
		m.Loads += c.Loads
		m.Syncs += c.Syncs
		m.SearchHits += c.SearchHits
		merged[k] = m
	}
	out := make([]UsageCount, 0, len(merged))
	for _, m := range merged {
		out = append(out, m)
	}
	sortUsageCounts(out)
	return out, nil
}

// validateUsageCount reports a count no correct recorder produces.
func validateUsageCount(c UsageCount) error {
	if c.Layer == "" || c.ArtifactID == "" || c.Client == "" || c.Harness == "" {
		return fmt.Errorf("%w: a count names no layer, artifact, client kind, or harness", ErrInvalidUsageCount)
	}
	if _, err := time.Parse(UsageDayLayout, c.Day); err != nil {
		return fmt.Errorf("%w: day %q is not a YYYY-MM-DD date", ErrInvalidUsageCount, c.Day)
	}
	if c.Loads < 0 || c.Syncs < 0 || c.SearchHits < 0 {
		return fmt.Errorf("%w: a counter is negative", ErrInvalidUsageCount)
	}
	return nil
}

// sortUsageCounts orders rows by day, layer, artifact, client kind, and harness, the
// order ListUsageCounts returns on every backend.
func sortUsageCounts(rows []UsageCount) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		if a.ArtifactID != b.ArtifactID {
			return a.ArtifactID < b.ArtifactID
		}
		if a.Client != b.Client {
			return a.Client < b.Client
		}
		return a.Harness < b.Harness
	})
}
