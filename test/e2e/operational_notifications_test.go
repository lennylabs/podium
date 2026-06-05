package e2e

// Operational notifications through the wired §9.1 NotificationProvider.
//
// The §9.1 NotificationProvider delivers operational notifications (ingest
// failure, embedding-provider outage, transparency-anchor failure, layer
// auto-disable). serverboot selects the provider from
// PODIUM_NOTIFICATION_PROVIDER and wires it into the registry, but no test
// drove a registry-side event through that wired path: the prior coverage
// proved the provider records a body (the recorder primitive) and that the
// server boots with the provider configured, without firing a real registry
// event.
//
// This file closes that journey. A standalone server boots with
// PODIUM_NOTIFICATION_PROVIDER=webhook pointed at the in-test notificationSink
// plus a secret. A reingest of a layer whose only artifact fails lint at error
// severity is an ingest-failure (§7.3.1 ingest.lint_failed), so the reingest
// path fires a §9.1 operational notification to the configured provider. The
// sink records exactly one delivery whose HMAC verifies against the secret and
// whose body carries the error severity and a title naming the failed layer.
//
// Spec: §9 / §9.1 (the NotificationProvider delivers ingest-failure and
// operational notifications; the webhook provider POSTs a signed JSON body with
// severity, title, body, and tags), §7.3.1 (a reingest whose snapshot fails
// lint returns ingest.lint_failed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/notification"
)

// startServerNotifier boots a standalone server over registry with the §9.1
// webhook NotificationProvider pointed at url and signing with secret. The
// returned server has the provider installed at boot, so a registry-side
// operational event delivers a signed body to the sink.
func startServerNotifier(t *testing.T, registry, url, secret string) *serverProc {
	t.Helper()
	return startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_NOTIFICATION_PROVIDER=webhook",
		"PODIUM_NOTIFICATION_WEBHOOK_URL=" + url,
		"PODIUM_NOTIFICATION_WEBHOOK_SECRET=" + secret,
	}, "serve", "--standalone", "--layer-path", registry)
}

// TestOps_IngestFailureFiresOperationalNotification proves the wired §9.1
// path: a reingest that fails lint fires one signature-valid operational
// notification carrying the error severity and a title naming the layer.
func TestOps_IngestFailureFiresOperationalNotification(t *testing.T) {
	t.Parallel()
	const secret = "ops-notify-ingest-failure"
	sink := newNotificationSink(t, withSinkSecret(secret))
	srv := startServerNotifier(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}), sink.URL(), secret)

	// Register a clean local-source layer (register does not ingest), then write a
	// malformed artifact (no `type`) as the layer's only artifact. The reingest
	// snapshot fails lint at error severity with nothing accepted, so the runner
	// returns ingest.lint_failed and the reingest path fires the §9.1 notifier.
	rl := newRepublishLayer(t, srv, "ingest-fail-layer")
	badDir := filepath.Join(rl.dir, "broken")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", badDir, err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "ARTIFACT.md"), []byte("---\nversion: 1.0.0\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write malformed ARTIFACT.md: %v", err)
	}

	// Trigger the reingest directly (publishVersion would fatal on the expected
	// non-zero exit). The malformed snapshot must be rejected.
	ri := runPodium(t, "", nil, "layer", "reingest", "--registry", srv.BaseURL, "ingest-fail-layer")
	if ri.Exit == 0 {
		t.Fatalf("reingest of a malformed layer succeeded (exit=0); expected ingest.lint_failed\nstdout: %s\nstderr: %s",
			ri.Stdout, ri.Stderr)
	}

	// The §9.1 webhook delivery is fired from the failure path; wait for the
	// asynchronous POST.
	if !sink.waitForDelivery(1, 5*time.Second) {
		t.Fatalf("no operational notification delivered for the ingest failure\nreingest stderr: %s\nserver log:\n%s",
			ri.Stderr, srv.log())
	}
	deliveries := sink.all()
	if len(deliveries) != 1 {
		t.Errorf("recorded %d deliveries, want exactly 1: %+v", len(deliveries), deliveries)
	}
	d := deliveries[0]
	if !d.SigValid {
		t.Errorf("operational notification body failed HMAC verification against the provider secret")
	}
	// §9.1 body schema: severity, title, body. The notifier classifies an
	// ingest-failure at error severity and names the failing layer in the title.
	if d.Body["severity"] != string(notification.SeverityError) {
		t.Errorf("notification severity = %v, want %q", d.Body["severity"], notification.SeverityError)
	}
	title, _ := d.Body["title"].(string)
	if title == "" {
		t.Errorf("notification missing a title: %+v", d.Body)
	}
	if !strings.Contains(title, "ingest-fail-layer") {
		t.Errorf("notification title %q does not name the failed layer", title)
	}
	if _, present := d.Body["body"]; !present {
		t.Errorf("notification missing a body field: %+v", d.Body)
	}
	// The layer tag lets a receiver route on the layer id.
	if tags, ok := d.Body["tags"].(map[string]any); ok {
		if tags["layer"] != "ingest-fail-layer" {
			t.Errorf("notification tags.layer = %v, want ingest-fail-layer", tags["layer"])
		}
	} else {
		t.Errorf("notification missing tags object: %+v", d.Body)
	}
}

// TestOps_CleanReingestFiresNoOperationalNotification is the negative control:
// a successful reingest is not an ingest-failure, so the §9.1 provider receives
// nothing. This isolates the failure trigger from "any reingest delivers."
func TestOps_CleanReingestFiresNoOperationalNotification(t *testing.T) {
	t.Parallel()
	const secret = "ops-notify-clean"
	sink := newNotificationSink(t, withSinkSecret(secret))
	srv := startServerNotifier(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}), sink.URL(), secret)

	// A clean publish: register and reingest a valid artifact. publishVersion
	// fatals on a non-zero exit, so a green reingest is asserted by construction.
	rl := newRepublishLayer(t, srv, "clean-layer")
	rl.publishVersion(t, versionSpec{
		ID:          "ops/clean-probe",
		Version:     "1.0.0",
		Description: "a clean operational-notification negative control artifact here",
	})

	// Give any stray operational notification time to arrive, then assert none
	// did: a successful ingest fires no §9.1 NotificationProvider delivery.
	time.Sleep(700 * time.Millisecond)
	if n := sink.count(); n != 0 {
		t.Errorf("clean reingest delivered %d operational notifications, want 0: %+v", n, sink.all())
	}
}
