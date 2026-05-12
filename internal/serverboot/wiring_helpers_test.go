package serverboot

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/notification"
	"github.com/lennylabs/podium/pkg/scim"
)

func TestBuildSCIMHandler_NoTokensDisablesSCIM(t *testing.T) {
	t.Setenv("PODIUM_SCIM_TOKENS", "")
	if h := buildSCIMHandler(scim.NewMemory()); h != nil {
		t.Errorf("expected nil handler, got %+v", h)
	}
}

func TestBuildSCIMHandler_WithTokens(t *testing.T) {
	t.Setenv("PODIUM_SCIM_TOKENS", "tok1, tok2, ,tok3")
	h := buildSCIMHandler(scim.NewMemory())
	if h == nil {
		t.Fatal("expected handler, got nil")
	}
	if !h.Tokens["tok1"] || !h.Tokens["tok2"] || !h.Tokens["tok3"] {
		t.Errorf("tokens missing entries: %+v", h.Tokens)
	}
}

func TestBuildSCIMHandler_OnlyWhitespaceTokens(t *testing.T) {
	t.Setenv("PODIUM_SCIM_TOKENS", "  ,  ,")
	if h := buildSCIMHandler(scim.NewMemory()); h != nil {
		t.Errorf("expected nil handler for empty tokens, got %+v", h)
	}
}

func TestOpenNotifier_NoopAndUnset(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "")
	if p := openNotifier(); p != nil {
		t.Errorf("unset → %T, want nil", p)
	}
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "noop")
	if p := openNotifier(); p != nil {
		t.Errorf("noop → %T, want nil", p)
	}
}

func TestOpenNotifier_Log(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "log")
	p := openNotifier()
	if p == nil || p.ID() != "log" {
		t.Errorf("got %v", p)
	}
}

func TestOpenNotifier_WebhookRequiresURL(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "webhook")
	t.Setenv("PODIUM_NOTIFICATION_WEBHOOK_URL", "")
	if p := openNotifier(); p != nil {
		t.Errorf("expected nil, got %v", p)
	}
	t.Setenv("PODIUM_NOTIFICATION_WEBHOOK_URL", "http://example/webhook")
	t.Setenv("PODIUM_NOTIFICATION_WEBHOOK_SECRET", "shh")
	p := openNotifier()
	if p == nil || p.ID() != "webhook" {
		t.Errorf("got %v", p)
	}
}

func TestOpenNotifier_MultiWithURL(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "multi")
	t.Setenv("PODIUM_NOTIFICATION_WEBHOOK_URL", "http://example/")
	p := openNotifier()
	if p == nil || p.ID() != "multi" {
		t.Errorf("got %v", p)
	}
}

func TestOpenNotifier_MultiWithoutURL(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "multi")
	t.Setenv("PODIUM_NOTIFICATION_WEBHOOK_URL", "")
	p := openNotifier()
	if p == nil {
		t.Errorf("multi returned nil")
	}
}

func TestOpenNotifier_UnknownProvider(t *testing.T) {
	t.Setenv("PODIUM_NOTIFICATION_PROVIDER", "bogus")
	if p := openNotifier(); p != nil {
		t.Errorf("expected nil for unknown, got %v", p)
	}
}

// adaptNotifier wraps a notification.Provider as a NotificationFunc.
// The wrapper swallows errors so the registry keeps running on
// notifier outage.
func TestAdaptNotifier_SwallowsErrors(t *testing.T) {
	t.Parallel()
	hit := 0
	wrap := adaptNotifier(stubProvider{onNotify: func() { hit++ }})
	wrap(context.Background(), "warning", "t", "b", nil)
	if hit != 1 {
		t.Errorf("hit = %d", hit)
	}
}

type stubProvider struct{ onNotify func() }

func (stubProvider) ID() string { return "stub" }
func (s stubProvider) Notify(context.Context, notification.Notification) error {
	if s.onNotify != nil {
		s.onNotify()
	}
	return nil
}
