package objectstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/objectstore"
)

// stallingProvider never answers a Get until release closes, and ignores its
// context the way Filesystem.Get does.
type stallingProvider struct {
	*objectstore.Memory
	release chan struct{}
}

func (s *stallingProvider) Get(ctx context.Context, key string) ([]byte, error) {
	<-s.release
	return s.Memory.Get(ctx, key)
}

// Spec: §13.12 — PODIUM_MIGRATION_OBJECT_READ_TIMEOUT bounds each object read,
// and a read the provider does not abandon on its context is still abandoned
// at the deadline.
func TestGetWithDeadline_AbandonsAStalledRead(t *testing.T) {
	t.Parallel()
	p := &stallingProvider{Memory: objectstore.NewMemory(), release: make(chan struct{})}
	t.Cleanup(func() { close(p.release) })
	start := time.Now()
	_, err := objectstore.GetWithDeadline(context.Background(), p, "k", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("GetWithDeadline returned after %v, want the 20ms deadline", elapsed)
	}
}

// Spec: §13.12 — a read inside the deadline returns the provider's answer,
// including its not-found error.
func TestGetWithDeadline_ReturnsTheProvidersAnswer(t *testing.T) {
	t.Parallel()
	m := objectstore.NewMemory()
	if err := m.Put(context.Background(), "k", []byte("body"), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	body, err := objectstore.GetWithDeadline(context.Background(), m, "k", objectstore.DefaultReadTimeout)
	if err != nil || string(body) != "body" {
		t.Fatalf("GetWithDeadline = %q, %v; want body", body, err)
	}
	if _, err := objectstore.GetWithDeadline(context.Background(), m, "absent", objectstore.DefaultReadTimeout); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("absent key err = %v, want ErrNotFound", err)
	}
}
