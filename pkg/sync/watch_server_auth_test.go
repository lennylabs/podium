package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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
