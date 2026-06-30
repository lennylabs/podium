package e2e

// Proves the notification delivery sink and override seam.
//
// Each test drives the real standalone binary and the reusable primitives in
// notification_sink_helpers_test.go:
//
//   - WebhookDeliversSignedEvent: a CLI reingest fires §7.3.2 artifact.published
//     to a registered receiver; the sink records a signature-valid delivery
//     whose body carries the §7.3.2 schema and the published artifact id.
//   - FilterOmitsNonMatchingEvents: a receiver filtered to an event type the
//     reingest does not emit records nothing, while an all-events receiver on
//     the same server records the same reingest. This isolates the §7.3.2 event
//     filter from "no delivery happened at all."
//   - AutoDisablesAfterMaxFailures: with PODIUM_WEBHOOK_MAX_FAILURES=2 and a
//     receiver pointed at a sink that fails every delivery, two reingests drive
//     the failure counter to the cap and the receiver auto-disables (§7.3.2).
//   - SSEDeliversChangeEvent: a bounded /v1/events reader subscribed to
//     artifact.published receives the event a reingest fires (§7.6).
//   - WebhookNotificationProviderRecorder: the same sink is a valid §9.1 webhook
//     NotificationProvider endpoint (it records the signed operational body),
//     and the standalone server boots cleanly with
//     PODIUM_NOTIFICATION_PROVIDER=webhook pointed at it.
//
// Spec: §7.3.2, §7.6, §9.1.

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/notification"
)

// niArtifact is the canonical id every primitive test publishes; one place
// keeps the delivered-body assertions self-documenting.
const niArtifact = "ops/notify-probe"

// publishProbe registers a runtime local-source layer and publishes one version
// of niArtifact into it, firing the §7.3.2/§7.6 ingest events for that version.
// It returns the republish handle so a test can publish a further version to
// fire a second cycle (the auto-disable path needs two).
func publishProbe(t *testing.T, srv *serverProc, layerID, version string) *republishLayer {
	t.Helper()
	rl := newRepublishLayer(t, srv, layerID)
	rl.publishVersion(t, versionSpec{
		ID:          niArtifact,
		Version:     version,
		Description: "notification delivery probe for vendor payment events here today",
	})
	return rl
}

// TestNotificationSink_WebhookDeliversSignedEvent proves the core path: a
// registry event produces a delivered, signature-valid notification carrying
// the §7.3.2 body schema.
func TestNotificationSink_WebhookDeliversSignedEvent(t *testing.T) {
	t.Parallel()
	requireSubprocessTLSTrust(t)
	const secret = "wh-secret-signed"
	sink := newNotificationSink(t, withSinkTLS(), withSinkSecret(secret))
	srv := startWebhookAdminServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}), withSink(t, sink))
	registerWebhook(t, srv, sink.URL(), secret, "artifact.published")

	publishProbe(t, srv, "notify-layer", "1.0.0")

	if !sink.waitForDelivery(1, 5*time.Second) {
		t.Fatalf("no webhook delivery recorded within deadline\nserver log:\n%s", srv.log())
	}
	d, ok := sink.firstMatching("artifact.published")
	if !ok {
		t.Fatalf("no artifact.published delivery recorded; got %d deliveries: %+v", sink.count(), sink.all())
	}
	if !d.SigValid {
		t.Errorf("delivered body failed HMAC verification against the receiver secret")
	}
	// §7.3.2 body schema: every key present, actor an object.
	for _, k := range []string{"event", "trace_id", "timestamp", "actor", "data"} {
		if _, present := d.Body[k]; !present {
			t.Errorf("delivered body missing %q key: %+v", k, d.Body)
		}
	}
	if _, ok := d.Body["actor"].(map[string]any); !ok {
		t.Errorf("delivered actor is not an object: %v", d.Body["actor"])
	}
	data, _ := d.Body["data"].(map[string]any)
	if data["id"] != niArtifact {
		t.Errorf("delivered data.id = %v, want %s", data["id"], niArtifact)
	}
	if data["version"] != "1.0.0" {
		t.Errorf("delivered data.version = %v, want 1.0.0", data["version"])
	}
}

// TestNotificationSink_FilterOmitsNonMatchingEvents proves the §7.3.2 event
// filter: a receiver filtered to an event type the reingest never emits sees
// nothing, while an all-events receiver on the same server sees the reingest.
func TestNotificationSink_FilterOmitsNonMatchingEvents(t *testing.T) {
	t.Parallel()
	requireSubprocessTLSTrust(t)
	// One receiver filtered to layer.history_rewritten, which a clean local-source
	// reingest never fires; one receiver with no filter (all events).
	const filteredSecret = "wh-secret-filtered"
	const allSecret = "wh-secret-all"
	filtered := newNotificationSink(t, withSinkTLS(), withSinkSecret(filteredSecret))
	all := newNotificationSink(t, withSinkTLS(), withSinkSecret(allSecret))
	srv := startWebhookAdminServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}), withSink(t, filtered), withSink(t, all))
	registerWebhook(t, srv, filtered.URL(), filteredSecret, "layer.history_rewritten")
	registerWebhook(t, srv, all.URL(), allSecret) // no filter

	publishProbe(t, srv, "filter-layer", "1.0.0")

	// The all-events receiver must record at least the artifact.published and
	// layer.ingested events the reingest fires.
	if !all.waitForDelivery(1, 5*time.Second) {
		t.Fatalf("all-events receiver recorded nothing for the reingest\nserver log:\n%s", srv.log())
	}
	if _, ok := all.firstMatching("artifact.published"); !ok {
		t.Errorf("all-events receiver missing artifact.published; got: %+v", all.all())
	}
	// Give any stray delivery to the filtered receiver time to arrive, then
	// assert it stayed empty: the filter excluded every fired event type.
	time.Sleep(500 * time.Millisecond)
	if n := filtered.count(); n != 0 {
		t.Errorf("filtered receiver recorded %d deliveries, want 0 (filter mismatch): %+v", n, filtered.all())
	}
}

// TestNotificationSink_AutoDisablesAfterMaxFailures proves the override seam:
// PODIUM_WEBHOOK_MAX_FAILURES lowers the §7.3.2 auto-disable threshold so a
// receiver pointed at a failing sink disables after a small number of induced
// failures. Filtering to layer.ingested makes exactly one delivery fire per
// reingest, so two reingests are exactly two consecutive failures.
func TestNotificationSink_AutoDisablesAfterMaxFailures(t *testing.T) {
	t.Parallel()
	requireSubprocessTLSTrust(t)
	const secret = "wh-secret-autodisable"
	sink := newNotificationSink(t, withSinkTLS(), withSinkSecret(secret), withSinkFailEvery())
	srv := startWebhookAdminServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}), withMaxFailures(2), withSink(t, sink))
	rec := registerWebhook(t, srv, sink.URL(), secret, "layer.ingested")

	rl := publishProbe(t, srv, "autodisable-layer", "1.0.0")
	// One layer.ingested event -> one consecutive delivery failure (each event is
	// a single Deliver call; the retry within it still counts as one failure for
	// the receiver). The cap is 2, so the receiver is not yet disabled.
	if !sink.waitForDelivery(1, 5*time.Second) {
		t.Fatalf("first failing delivery not attempted\nserver log:\n%s", srv.log())
	}
	got, reached := waitForWebhookFailureCount(t, srv, rec.ID, 1, 5*time.Second)
	if !reached {
		t.Fatalf("failure_count did not reach 1 after one failing reingest (got %d)\nserver log:\n%s",
			got.FailureCount, srv.log())
	}
	if got.Disabled {
		t.Fatalf("receiver disabled after one failure; want disable only at the cap of 2")
	}

	// Second reingest -> second consecutive failure -> reaches the cap -> disable.
	rl.publishVersion(t, versionSpec{
		ID:          niArtifact,
		Version:     "2.0.0",
		Description: "notification delivery probe for vendor payment events here today",
	})
	final, disabled := waitForWebhookDisabled(t, srv, rec.ID, 5*time.Second)
	if !disabled {
		t.Fatalf("receiver not auto-disabled after reaching MaxFailures=2 (failure_count=%d)\nserver log:\n%s",
			final.FailureCount, srv.log())
	}
}

// TestNotificationSink_SSEDeliversChangeEvent proves the bounded SSE client: a
// subscriber on the §7.6 /v1/events stream filtered to artifact.published
// receives the event a reingest fires, with the published id in the body.
func TestNotificationSink_SSEDeliversChangeEvent(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}))
	// openSSE returns only after the server has registered the subscription and
	// flushed the response headers, so an event fired after this point cannot be
	// missed by the subscriber. newRepublishLayer only registers the layer;
	// publishVersion fires the artifact.published event.
	sub := openSSE(t, srv, "artifact.published")
	rl := newRepublishLayer(t, srv, "sse-layer")
	rl.publishVersion(t, versionSpec{
		ID:          niArtifact,
		Version:     "1.0.0",
		Description: "notification delivery probe for vendor payment events here today",
	})

	ev, ok := sub.waitForEvent(t, "artifact.published", 5*time.Second)
	if !ok {
		t.Fatalf("no artifact.published event on the SSE stream within deadline\nserver log:\n%s", srv.log())
	}
	if ev.Data["id"] != niArtifact {
		t.Errorf("SSE event data.id = %v, want %s", ev.Data["id"], niArtifact)
	}
	if ev.Timestamp == "" {
		t.Errorf("SSE event missing timestamp: %+v", ev)
	}
}

// TestNotificationSink_WebhookNotificationProviderRecorder proves the recorder
// is a valid §9.1 webhook NotificationProvider endpoint: the provider POSTs a
// signed operational body and the sink records it with a verified signature.
// This is the recorder half of the operator-notification path; the standalone
// ingest-failure trigger that fires such a notification is a downstream gap.
func TestNotificationSink_WebhookNotificationProviderRecorder(t *testing.T) {
	t.Parallel()
	const secret = "notify-provider-secret"
	sink := newNotificationSink(t, withSinkSecret(secret))

	prov := notification.Webhook{URL: sink.URL(), Secret: secret}
	if id := prov.ID(); id != "webhook" {
		t.Fatalf("provider ID = %q, want webhook", id)
	}
	err := prov.Notify(context.Background(), notification.Notification{
		Severity: notification.SeverityError,
		Title:    "ingest failure",
		Body:     "layer prod failed to ingest",
		Tags:     map[string]string{"layer": "prod"},
		Time:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NotificationProvider.Notify: %v", err)
	}
	if !sink.waitForDelivery(1, 2*time.Second) {
		t.Fatalf("notification not recorded by the sink")
	}
	d := sink.all()[0]
	if !d.SigValid {
		t.Errorf("operational notification body failed HMAC verification")
	}
	if d.Body["title"] != "ingest failure" {
		t.Errorf("recorded notification title = %v, want 'ingest failure'", d.Body["title"])
	}
	if d.Body["severity"] != string(notification.SeverityError) {
		t.Errorf("recorded notification severity = %v, want error", d.Body["severity"])
	}
}

// TestNotificationSink_StandaloneBootsWithWebhookNotifier proves the standalone
// server accepts the §9.1 webhook NotificationProvider env wiring pointed at the
// in-test sink and serves normally (the provider is installed at boot; an
// operational notification would deliver to the sink). It asserts the boot path
// is healthy with the provider configured, complementing the direct-provider
// proof above.
func TestNotificationSink_StandaloneBootsWithWebhookNotifier(t *testing.T) {
	t.Parallel()
	const secret = "boot-notifier-secret"
	sink := newNotificationSink(t, withSinkSecret(secret))
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_NOTIFICATION_PROVIDER=webhook",
		"PODIUM_NOTIFICATION_WEBHOOK_URL=" + sink.URL(),
		"PODIUM_NOTIFICATION_WEBHOOK_SECRET=" + secret,
	}, "serve", "--standalone", "--layer-path", writeRegistry(t, map[string]string{
		"seed/ARTIFACT.md": smallteamLowArtifact("seed"),
	}))
	// The server is up (startServerArgs blocks on /healthz). A reingest exercises
	// the live path; the notifier is wired even though no operational
	// notification fires on a clean ingest.
	publishProbe(t, srv, "notifier-boot-layer", "1.0.0")
	var health struct {
		Mode string `json:"mode"`
	}
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health.Mode != "ready" && health.Mode != "standalone" {
		t.Errorf("healthz mode=%q, want ready or standalone", health.Mode)
	}
}
