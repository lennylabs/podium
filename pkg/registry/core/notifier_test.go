package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

func TestRegistry_NotifyForwards(t *testing.T) {
	t.Parallel()
	got := 0
	notifier := func(_ context.Context, severity, title, body string, _ map[string]string) {
		got++
		if severity != "warning" || title != "t" || body != "b" {
			t.Errorf("notifier got %s/%s/%s", severity, title, body)
		}
	}
	r := core.New(store.NewMemory(), "default", nil).WithNotifier(notifier)
	r.Notify(context.Background(), "warning", "t", "b", nil)
	if got != 1 {
		t.Errorf("notifier called %d times, want 1", got)
	}
}

func TestRegistry_NotifyNoOpWithoutWiring(t *testing.T) {
	t.Parallel()
	r := core.New(store.NewMemory(), "default", nil)
	r.Notify(context.Background(), "warning", "t", "b", nil)
}

func TestRegistry_VectorStoreAndEmbedderNilByDefault(t *testing.T) {
	t.Parallel()
	r := core.New(store.NewMemory(), "default", nil)
	if r.VectorStore() != nil {
		t.Errorf("VectorStore not nil")
	}
	if r.Embedder() != nil {
		t.Errorf("Embedder not nil")
	}
}
