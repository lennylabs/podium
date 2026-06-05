package serverboot

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §4.7.2 — the vector outbox drain worker honors the server lifecycle
// context. Cancelling the context stops the goroutine instead of leaking it, so
// a graceful shutdown does not leave the worker draining against a torn-down
// store. goleak fails the test if the goroutine outlives the cancellation, which
// is what the pre-fix context.Background() loop did.
func TestStartVectorOutboxWorker_StopsOnContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	st := store.NewMemory()
	vec := vector.NewMemory(8)
	ctx, cancel := context.WithCancel(context.Background())
	// A long interval parks the goroutine on the ticker after its immediate pass.
	// The ctx.Done() case in the select must still unblock the worker and return.
	startVectorOutboxWorker(ctx, &Config{vectorOutboxInterval: 3600}, st, vec, fakeEmbedder{dim: 8}, nil, nil, "t")
	cancel()
}
