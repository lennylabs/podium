package ingest

import (
	"context"
	"sync"
)

// CycleLocks serializes the ingest cycles of each layer within one registry
// process, so a cycle takes its snapshot only after the previous cycle of the
// layer has ended. Without it, a manual reingest and a webhook delivery of
// one layer can overlap, and the cycle holding the older snapshot can list
// the layer after the newer cycle stored an artifact and remove it. Cycles on
// other replicas are kept apart by the ingest mark the store checks when it
// tombstones (store.SoftDeleteArtifacts).
//
// mu guards locks. Each entry counts the cycles holding or waiting for it and
// is dropped when the count reaches zero, so the map holds only layers with a
// cycle in flight. The zero value is not usable; call NewCycleLocks.
//
// Spec: §7.3.1 (Removed artifacts)
type CycleLocks struct {
	mu    sync.Mutex
	locks map[[2]string]*cycleLock
}

// cycleLock is one layer's lock, a one-slot semaphore so a waiting cycle can
// stop at its context's end, and the number of cycles holding or waiting for
// it, which CycleLocks.mu guards.
type cycleLock struct {
	slot  chan struct{}
	users int
}

// NewCycleLocks returns an empty set of per-layer cycle locks.
func NewCycleLocks() *CycleLocks {
	return &CycleLocks{locks: map[[2]string]*cycleLock{}}
}

// lock waits until no other cycle of the layer holds its lock, takes it, and
// returns the function that releases it. It returns ctx's error, holding
// nothing, when ctx ends first. A nil receiver serializes nothing.
func (c *CycleLocks) lock(ctx context.Context, tenantID, layerID string) (unlock func(), err error) {
	if c == nil {
		return func() {}, nil
	}
	key := [2]string{tenantID, layerID}
	c.mu.Lock()
	l := c.locks[key]
	if l == nil {
		l = &cycleLock{slot: make(chan struct{}, 1)}
		c.locks[key] = l
	}
	l.users++
	c.mu.Unlock()

	select {
	case l.slot <- struct{}{}:
		return func() {
			<-l.slot
			c.release(key, l)
		}, nil
	case <-ctx.Done():
		c.release(key, l)
		return nil, ctx.Err()
	}
}

// release drops one user of l and forgets the entry once none remains.
func (c *CycleLocks) release(key [2]string, l *cycleLock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l.users--
	if l.users == 0 {
		delete(c.locks, key)
	}
}
