package serverboot

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/vector"
)

// overlapReadRetryPasses is the number of later outbox passes that retry an
// outbox-write computation whose stored-vector read the backend answered
// with no row. §7.3.1 fixes it at five, so it is not operator-tunable.
//
// Spec: §7.3.1
const overlapReadRetryPasses = 5

// overlapOutbox runs the §7.3.1 overlap computation after each §4.7 outbox
// pass and keeps, in memory, the computations to retry. A computation whose
// write carried a vector queries with it and never reads it back. One that
// has to read the stored vector, as on a self-embedding backend, can find no
// row right after the write on an eventually consistent backend such as
// Pinecone; it is then retried on each of the next overlapReadRetryPasses
// passes and left to the sweep after the last.
//
// The retry set is bounded: an entry is dropped when its computation runs
// without a missing read, when the outbox writes the artifact again, or
// after its last pass, so it never holds more than the misses of the last
// overlapReadRetryPasses passes. A restart loses it, and the startup pass
// then computes those artifacts.
type overlapOutbox struct {
	reg *core.Registry

	// mu is held for a whole pass. It guards retries, and it serializes two
	// passes so they never compute and record the same retry at once; the
	// drain worker runs its passes one at a time, so it never waits in
	// production.
	mu      sync.Mutex
	retries map[overlapRetryKey]overlapRetry
}

// overlapRetryKey names an artifact of a tenant. A retry is kept per
// artifact rather than per version, so a newer version replaces it.
type overlapRetryKey struct {
	tenant     string
	artifactID string
}

// overlapRetry is a computation to retry: the version whose stored vector
// was missing, and the passes left to retry it.
type overlapRetry struct {
	version    string
	passesLeft int
}

func newOverlapOutbox(reg *core.Registry) *overlapOutbox {
	return &overlapOutbox{reg: reg, retries: map[overlapRetryKey]overlapRetry{}}
}

// afterPass computes the artifacts one outbox pass wrote and the retries
// still due. Each tenant's computations go to one ComputeOverlapAtWrite
// call, which lists that tenant's served set once for the pass. A cancelled
// ctx stops it before the next tenant and keeps the remaining retries.
//
// Spec: §7.3.1
func (o *overlapOutbox) afterPass(ctx context.Context, written []outboxWrite) {
	o.mu.Lock()
	defer o.mu.Unlock()
	byTenant := o.passWrites(written)
	tenants := make([]string, 0, len(byTenant))
	for tenant := range byTenant {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	for _, tenant := range tenants {
		if ctx.Err() != nil {
			return
		}
		writes := byTenant[tenant]
		res, err := o.reg.ComputeOverlapAtWrite(core.ContextWithTenant(ctx, tenant), writes)
		if err != nil {
			log.Printf("%s: tenant %s after the outbox write of %d vectors: %v", lint.SemanticOverlapCode, tenant, len(writes), err)
		}
		o.settle(tenant, writes, res.Unreadable)
	}
}

// passWrites groups by tenant the computations of a pass: every row the
// pass wrote, with the vector it wrote, and every retry still due, which
// reads the stored vector. A row the pass wrote replaces the retry of its
// artifact.
func (o *overlapOutbox) passWrites(written []outboxWrite) map[string][]core.OverlapWrite {
	byTenant := map[string][]core.OverlapWrite{}
	for _, w := range written {
		delete(o.retries, overlapRetryKey{tenant: w.row.TenantID, artifactID: w.row.ArtifactID})
		byTenant[w.row.TenantID] = append(byTenant[w.row.TenantID], core.OverlapWrite{
			Key:    vector.RowKey{ArtifactID: w.row.ArtifactID, Version: w.row.Version},
			Vector: w.vec,
			Model:  w.model,
		})
	}
	for k, r := range o.retries {
		byTenant[k.tenant] = append(byTenant[k.tenant], core.OverlapWrite{Key: vector.RowKey{ArtifactID: k.artifactID, Version: r.version}})
	}
	return byTenant
}

// settle records the outcome of one tenant's computations. A computation
// whose stored vector was missing is retried on the next pass, with one pass
// fewer when it was already a retry; after its last pass it is dropped and
// logged, and the sweep computes it. Every other computation drops its retry:
// it was computed, failed for another reason the computation logged, or was
// skipped because its version is no longer served.
func (o *overlapOutbox) settle(tenant string, writes []core.OverlapWrite, unreadable []vector.RowKey) {
	missing := make(map[vector.RowKey]bool, len(unreadable))
	for _, k := range unreadable {
		missing[k] = true
	}
	for _, w := range writes {
		key := overlapRetryKey{tenant: tenant, artifactID: w.Key.ArtifactID}
		prev, retried := o.retries[key]
		retried = retried && prev.version == w.Key.Version
		if !missing[w.Key] {
			if retried {
				delete(o.retries, key)
			}
			continue
		}
		left := overlapReadRetryPasses
		if retried {
			left = prev.passesLeft - 1
		}
		if left <= 0 {
			delete(o.retries, key)
			log.Printf("%s: stored-vector read for %s@%s found no row after %d outbox passes; the sweep computes it",
				lint.SemanticOverlapCode, w.Key.ArtifactID, w.Key.Version, overlapReadRetryPasses)
			continue
		}
		o.retries[key] = overlapRetry{version: w.Key.Version, passesLeft: left}
	}
}
