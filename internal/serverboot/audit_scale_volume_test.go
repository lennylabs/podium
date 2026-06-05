package serverboot

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
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/sign"
)

// audit sampling and retention purging under high event volume with
// signature integrity. This drives the real serverboot audit wiring — the
// PODIUM_AUDIT_SAMPLE_RATES sampler, the §4.7.8 audit-volume meter, the §8.3
// file sink, the §8.6 anchor signer, and the §8.4 retention sweep — through the
// same composition Run() builds (auditEmitterFor + auditVolumeEmitter +
// audit.Anchor + audit.Enforce), at a volume the parse-only and single-event
// unit tests do not exercise.
//
// It asserts:
//   - high-frequency domain.loaded and domains.searched events are recorded at
//     approximately their configured sampling fraction;
//   - ingest (layer.ingested / artifact.published) and admin.granted events are
//     never sampled out;
//   - PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY caps total writes (the meter the
//     reingest path consults flips to refusing once the daily budget is spent);
//   - the §8.4 retention sweep purges aged events while the post-sweep chain
//     still verifies and the re-anchored head's Ed25519 signature checks out.

// auditScaleEvent is a counted recorded-event snapshot read back from the log.
type auditScaleCounts map[string]int

// countEventsByType reads the JSON-Lines audit log and tallies events by type.
func countEventsByType(t *testing.T, path string) auditScaleCounts {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log %s: %v", path, err)
	}
	counts := auditScaleCounts{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var je struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &je); err != nil {
			t.Fatalf("parse audit line: %v\nline=%s", err, line)
		}
		counts[je.Type]++
	}
	return counts
}

// TestAuditScale_SamplingAlwaysRecordedRetentionAndVolume is the
// journey over the real serverboot audit emitter composition.
func TestAuditScale_SamplingAlwaysRecordedRetentionAndVolume(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(logPath)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}

	// The exact §8.4 sampler the parser produces from the env var, here over
	// the two high-frequency read events at a 20% keep rate.
	const sampleRate = 0.20
	rates := parseAuditSampleRates("domain.loaded=0.2,domains.searched=0.2")
	if rates[audit.EventDomainLoaded] != sampleRate || rates[audit.EventDomainsSearched] != sampleRate {
		t.Fatalf("sample rate parse = %v, want both at %v", rates, sampleRate)
	}
	sampler := audit.NewSampler(rates)

	// The §4.7.8 audit-volume meter with a daily cap, wired the way Run() does:
	// every emitted event is recorded against the tenant budget, and a write
	// operation consults Allow() before proceeding.
	const tenant = "default"
	const volumeCap = 5000
	meter := server.NewAuditVolumeMeter(volumeCap)

	// Build the production emitter composition: sampler-aware file emitter,
	// wrapped by the volume recorder. No PII scrubber needed for this volume.
	base := auditEmitterFor(sink, audit.NewPIIScrubber(), sampler)
	emit := auditVolumeEmitter(meter, tenant, base)
	ctx := context.Background()

	// ---- 1. high-frequency sampled events ---------------------------------
	const perType = 4000
	for i := 0; i < perType; i++ {
		emit(ctx, core.AuditEvent{Type: "domain.loaded", Caller: "alice@acme.com", Target: "finance"})
		emit(ctx, core.AuditEvent{Type: "domains.searched", Caller: "alice@acme.com", Context: map[string]string{"query": "vendor"}})
	}

	// ---- 2. ingest + admin-grant events are never sampled -----------------
	// These types carry no configured rate, so the sampler always keeps them.
	const ingestN, grantN = 25, 15
	for i := 0; i < ingestN; i++ {
		emit(ctx, core.AuditEvent{Type: "layer.ingested", Caller: "system:ingest", Target: "team/finance"})
		emit(ctx, core.AuditEvent{Type: "artifact.published", Caller: "alice@acme.com", Target: "skill/pay"})
	}
	for i := 0; i < grantN; i++ {
		emit(ctx, core.AuditEvent{Type: "admin.granted", Caller: "admin@acme.com", Target: "bob@acme.com"})
	}

	counts := countEventsByType(t, logPath)

	// The recorded fraction of each sampled type is within tolerance of 0.20.
	// math/rand drives the keep decision, so allow a generous band around the
	// rate (binomial std-dev at n=4000, p=0.2 is ~25 events ~ 0.6%, so +/-5%
	// is many sigma and still catches a broken rate like keep-all or drop-all).
	for _, ty := range []string{"domain.loaded", "domains.searched"} {
		frac := float64(counts[ty]) / float64(perType)
		if frac < sampleRate-0.05 || frac > sampleRate+0.05 {
			t.Errorf("%s recorded fraction = %.3f (%d/%d), want within 0.05 of %.2f",
				ty, frac, counts[ty], perType, sampleRate)
		}
		// Sampling must actually reduce volume: not all kept, not all dropped.
		if counts[ty] == 0 || counts[ty] == perType {
			t.Errorf("%s sampling degenerate: recorded %d of %d", ty, counts[ty], perType)
		}
	}

	// Ingest and admin-grant events are recorded in full (never sampled out).
	if counts["layer.ingested"] != ingestN {
		t.Errorf("layer.ingested recorded %d, want all %d (never sampled)", counts["layer.ingested"], ingestN)
	}
	if counts["artifact.published"] != ingestN {
		t.Errorf("artifact.published recorded %d, want all %d (never sampled)", counts["artifact.published"], ingestN)
	}
	if counts["admin.granted"] != grantN {
		t.Errorf("admin.granted recorded %d, want all %d (never sampled)", counts["admin.granted"], grantN)
	}

	// ---- 3. the volume quota caps total writes ----------------------------
	// The meter recorded one tick per emit() above (sampled-out reads still
	// count: auditVolumeEmitter.Record runs before the sampler drop, matching
	// Run()'s wrapping order). The total emit() count far exceeds volumeCap, so
	// the budget is spent and the write gate the reingest path consults refuses.
	totalEmits := perType*2 + ingestN*2 + grantN
	if totalEmits <= volumeCap {
		t.Fatalf("test misconfigured: %d emits <= cap %d, budget would not be spent", totalEmits, volumeCap)
	}
	if meter.Allow(tenant) {
		t.Errorf("audit-volume budget of %d not enforced after %d recorded events; Allow() still true", volumeCap, totalEmits)
	}
	// A different tenant with no recorded events is still allowed: the cap is
	// per-tenant, not global.
	if !meter.Allow("globex") {
		t.Errorf("per-tenant audit-volume budget leaked across tenants; globex refused with no recorded events")
	}

	// ---- 4. retention purge with signatures still verifying ---------------
	// Anchor the current head with an Ed25519 signer, then append a set of
	// deliberately over-age events, run the §8.4 retention sweep, and re-anchor.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer := sign.RegistryManagedKey{PrivateKey: priv, PublicKey: pub, KeyID: keyIDFor(pub)}

	now := time.Now().UTC()
	// 50 over-age artifact.loaded events (older than the 1-year metadata window)
	// plus a fresh one that must survive.
	for i := 0; i < 50; i++ {
		if err := sink.Append(ctx, audit.Event{
			Type: audit.EventArtifactLoaded, Timestamp: now.Add(-400 * 24 * time.Hour), Caller: "alice@acme.com",
		}); err != nil {
			t.Fatalf("append aged event: %v", err)
		}
	}
	if err := sink.Append(ctx, audit.Event{Type: audit.EventArtifactLoaded, Timestamp: now.Add(-time.Hour), Caller: "alice@acme.com"}); err != nil {
		t.Fatalf("append fresh event: %v", err)
	}

	// Anchor the pre-retention head.
	if _, err := audit.Anchor(ctx, sink, signer); err != nil {
		t.Fatalf("initial Anchor: %v", err)
	}
	if err := sink.Verify(ctx); err != nil {
		t.Fatalf("chain invalid before retention: %v", err)
	}

	// Run the retention sweep with the production policy table (1-year metadata
	// window), then re-anchor the moved head the way Run() wires it.
	policies := defaultRetentionPolicies(365 * 24 * time.Hour)
	dropped, err := audit.Enforce(ctx, sink, now, policies, audit.DefaultQueryRetention())
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if dropped < 50 {
		t.Errorf("retention dropped %d events, want >= 50 aged artifact.loaded events", dropped)
	}
	if _, err := audit.Anchor(ctx, sink, signer); err != nil {
		t.Fatalf("re-anchor after retention: %v", err)
	}

	// The chain still verifies after the purge + re-anchor.
	if err := sink.Verify(ctx); err != nil {
		t.Errorf("chain invalid after retention + re-anchor: %v", err)
	}

	// The aged events are gone: only the single fresh artifact.loaded survives.
	post := countEventsByType(t, logPath)
	if post["artifact.loaded"] != 1 {
		t.Errorf("artifact.loaded after retention = %d, want 1 (50 aged purged, 1 fresh kept)", post["artifact.loaded"])
	}
	// The retention boundary marker and the re-anchor are recorded.
	if post["audit.retention_enforced"] == 0 {
		t.Errorf("no audit.retention_enforced marker after a purge")
	}
	if post["audit.anchored"] < 2 {
		t.Errorf("expected at least 2 audit.anchored events (initial + re-anchor), got %d", post["audit.anchored"])
	}
	// The sampled read events survive retention: they are well within the
	// 1-year window, so the purge does not touch them.
	if post["domain.loaded"] != counts["domain.loaded"] {
		t.Errorf("retention purged in-window domain.loaded events: %d -> %d", counts["domain.loaded"], post["domain.loaded"])
	}

	// The re-anchored head's signature verifies against the public key. Read the
	// last audit.anchored event, extract its signed envelope and the chain head
	// it covers, and verify the Ed25519 signature.
	envelope, head := lastAnchor(t, logPath)
	if envelope == "" || head == "" {
		t.Fatalf("no signed anchor envelope found after re-anchor")
	}
	if err := signer.Verify(ctx, "sha256:"+head, envelope); err != nil {
		t.Errorf("re-anchored head signature does not verify: %v", err)
	}
	// A tampered head fails verification, proving the signature is load-bearing.
	if err := signer.Verify(ctx, "sha256:"+head+"deadbeef", envelope); err == nil {
		t.Errorf("signature verified against a tampered head; integrity check is not effective")
	}
}

// lastAnchor returns the signed envelope and covered chain head from the most
// recent audit.anchored event in the log.
func lastAnchor(t *testing.T, path string) (envelope, head string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var je struct {
			Type    string            `json:"type"`
			Target  string            `json:"target"`
			Context map[string]string `json:"context"`
		}
		if err := json.Unmarshal([]byte(line), &je); err != nil {
			continue
		}
		if je.Type == string(audit.EventAuditAnchored) {
			envelope = je.Context["envelope"]
			head = je.Target
		}
	}
	return envelope, head
}
