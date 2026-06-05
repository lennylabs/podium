package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §8.4/§8.6 — a retention pass that drops events rebuilds
// the hash chain, which would otherwise leave a previously anchored head
// stale. Enforce records an audit.retention_enforced marker naming the
// superseded head, and an immediate re-anchor pins the new head. This
// exercises the full retention→re-anchor coordination end to end.
func TestAuditRetention_ReanchorsNewHeadAfterDrop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer := sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub, KeyID: "test-key"}

	ctx := context.Background()
	now := time.Now().UTC()
	// One over-age event and one fresh event.
	for _, ts := range []time.Time{now.Add(-400 * 24 * time.Hour), now.Add(-time.Hour)} {
		if err := sink.Append(ctx, audit.Event{
			Type: audit.EventArtifactLoaded, Timestamp: ts, Caller: "alice@acme.com",
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Anchor the original head, then capture it.
	if _, err := audit.Anchor(ctx, sink, signer); err != nil {
		t.Fatalf("initial Anchor: %v", err)
	}
	headBeforeRetention := lastEventHash(t, path)

	// Retention drops the over-age event; the reAnchor closure pins the
	// new head the way serverboot's scheduler wires it.
	reAnchored := false
	reAnchor := func() {
		reAnchored = true
		if _, err := audit.Anchor(ctx, sink, signer); err != nil {
			t.Errorf("re-anchor: %v", err)
		}
	}
	dropped, err := audit.Enforce(ctx, sink, now,
		[]audit.Policy{{Type: audit.EventArtifactLoaded, MaxAge: 365 * 24 * time.Hour}}, nil)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped > 0 {
		reAnchor()
	}
	if !reAnchored {
		t.Fatalf("expected a re-anchor after dropping %d event(s)", dropped)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	got := string(body)
	// The retention marker names the superseded head.
	if !strings.Contains(got, "audit.retention_enforced") {
		t.Errorf("missing retention marker:\n%s", got)
	}
	if !strings.Contains(got, headBeforeRetention) {
		t.Errorf("marker does not record superseded head %q", headBeforeRetention)
	}
	// A fresh audit.anchored event covers the new (post-retention) head.
	if !strings.Contains(got, "audit.anchored") {
		t.Errorf("expected an audit.anchored event after re-anchor:\n%s", got)
	}
	newHead := lastEventHash(t, path)
	if newHead == headBeforeRetention {
		t.Errorf("chain head did not move after retention drop")
	}
	if err := sink.Verify(ctx); err != nil {
		t.Errorf("chain invalid after retention + re-anchor: %v", err)
	}
}

// lastEventHash returns the Hash of the last event line in the
// JSON-Lines log, the chain head a prior anchor would have pinned.
func lastEventHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var head string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var je struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal([]byte(line), &je); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		head = je.Hash
	}
	if head == "" {
		t.Fatalf("no chain head in %s", path)
	}
	return head
}
