package notification_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/notification"
)

// Spec: §9 — Noop provider discards notifications.
func TestNoop_Discards(t *testing.T) {
	if err := (notification.Noop{}).Notify(context.Background(), notification.Notification{}); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// Spec: §9 — Webhook posts JSON and includes an HMAC-SHA256
// signature when Secret is configured.
func TestWebhook_PostsJSONWithHMAC(t *testing.T) {
	const secret = "shh"
	hits := atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var n notification.Notification
		if err := json.Unmarshal(body, &n); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		if n.Title != "ingest-failed" {
			t.Errorf("Title = %q", n.Title)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-Podium-Signature"); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		hits.Add(1)
	}))
	defer ts.Close()

	wh := notification.Webhook{URL: ts.URL, Secret: secret}
	err := wh.Notify(context.Background(), notification.Notification{
		Severity: notification.SeverityError,
		Title:    "ingest-failed",
		Body:     "git fetch failed for layer team-shared",
		Time:     time.Now(),
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
}

// Spec: §9 — Webhook surfaces non-2xx as an error so callers can
// retry or alert.
func TestWebhook_NonOKReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	err := notification.Webhook{URL: ts.URL}.Notify(context.Background(), notification.Notification{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want 500-bearing error", err)
	}
}

// Spec: §9 — MultiProvider fans out to every wrapped provider.
func TestMultiProvider_FansOut(t *testing.T) {
	count := atomic.Int64{}
	dummy := dummyProvider{onNotify: func() { count.Add(1) }}
	multi := notification.MultiProvider{Providers: []notification.Provider{dummy, dummy, dummy}}
	if err := multi.Notify(context.Background(), notification.Notification{}); err != nil {
		t.Errorf("err = %v", err)
	}
	if count.Load() != 3 {
		t.Errorf("count = %d, want 3", count.Load())
	}
}

type dummyProvider struct {
	onNotify func()
}

func (dummyProvider) ID() string { return "dummy" }
func (d dummyProvider) Notify(context.Context, notification.Notification) error {
	if d.onNotify != nil {
		d.onNotify()
	}
	return nil
}
