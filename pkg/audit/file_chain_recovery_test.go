package audit_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
)

// Spec: §8.3 — when an existing audit log is reopened, the new
// FileSink picks up the prior chain head so the next event's
// PrevHash equals the last logged Hash. Without this, the chain
// would break across server restarts and Verify would fail.
func TestFileSink_ChainContinuesAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("first NewFileSink: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := sink.Append(context.Background(), audit.Event{Type: audit.EventType("noop")}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Reopen and append again.
	sink2, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("reopen NewFileSink: %v", err)
	}
	if err := sink2.Append(context.Background(), audit.Event{Type: audit.EventType("noop")}); err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}

	// Verify the entire chain — would fail if the second sink
	// reset lastHash.
	if err := sink2.Verify(context.Background()); err != nil {
		t.Errorf("Verify after reopen: %v", err)
	}
}
