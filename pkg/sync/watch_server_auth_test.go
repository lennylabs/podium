package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/audit"
)

// spec: §6.3.2 — /v1/events is not exempt from the registry's identity
// verification, so the watch subscription carries the caller credential the
// same way the one-shot sync fetch does. Without it a registry with an identity
// provider configured answers 401 and the watcher reconnects forever, which
// presents as a watch that never triggers rather than as an auth failure.
//
// The one-shot path was already covered by TestRun_ServerSource_ForwardsBearerToken;
// nothing covered the subscription, which is why the omission survived.
func TestWatch_ServerSource_SubscriptionCarriesBearerToken(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var authSeen string
	gotRequest := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authSeen = r.Header.Get("Authorization")
		mu.Unlock()
		select {
		case gotRequest <- struct{}{}:
		default:
		}
		// Hold the stream open until the test cancels, the way a real
		// subscription behaves.
		<-r.Context().Done()
	})
	// The initial sync runs before the subscription opens; serve it an empty
	// view so the watcher reaches the subscription.
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	events, err := Watch(ctx, WatchOptions{
		Sync: Options{
			RegistryPath: srv.URL,
			Delivery:     neverDelivery,
			Target:       t.TempDir(),
			AdapterID:    "none",
			Token:        "runtime-issued-jwt",
		},
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	go func() {
		for range events {
		}
	}()

	select {
	case <-gotRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher never opened the /v1/events subscription")
	}

	mu.Lock()
	got := authSeen
	mu.Unlock()
	if want := "Bearer runtime-issued-jwt"; got != want {
		t.Errorf("subscription Authorization = %q, want %q", got, want)
	}
}

// Spec: §7.2.2, §7.5 — the /v1/events subscription of a server-source watch
// sends the same X-Podium-Client value as the sync it reruns. Run defaults an
// empty AdapterID to none on its own copy of the options, which the
// subscription never sees, so runServerWatch applies the default itself; the
// no-harness case pins that through the public Watch entry point.
func TestWatch_ServerSource_SubscriptionSendsClientHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, adapterID, wantHarness string
	}{
		{name: "named harness", adapterID: "claude-code", wantHarness: "claude-code"},
		{name: "no harness", adapterID: "", wantHarness: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := watchSubscriptionClientHeader(t, tc.adapterID)
			want := audit.CallerClient{Kind: "sync", Name: "podium", Version: buildinfo.Version, Harness: tc.wantHarness}
			if parsed := audit.ParseClientHeader(got); parsed == nil || *parsed != want {
				t.Errorf("subscription X-Podium-Client %q parsed to %+v, want %+v", got, parsed, want)
			}
		})
	}
}

// watchSubscriptionClientHeader starts a server-source Watch with adapterID
// and returns the X-Podium-Client header its first /v1/events subscription
// carried.
func watchSubscriptionClientHeader(t *testing.T, adapterID string) string {
	t.Helper()
	seen := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get(audit.ClientHeader):
		default:
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1/sync/manifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events, err := Watch(ctx, WatchOptions{
		Sync: Options{
			RegistryPath: srv.URL,
			Delivery:     neverDelivery,
			Target:       t.TempDir(),
			AdapterID:    adapterID,
		},
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	go func() {
		for range events {
		}
	}()

	select {
	case got := <-seen:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher never opened the /v1/events subscription")
		return ""
	}
}
