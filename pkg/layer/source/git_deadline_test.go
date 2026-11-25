package source_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
)

// stalledRemote starts an HTTP git remote whose handler stalls every request
// until the client goes away or the test ends. handle runs first and can
// write part of a response before the stall.
func stalledRemote(t *testing.T, handle func(http.ResponseWriter, *http.Request, <-chan struct{})) string {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, stop)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	// Cleanups run last-in first-out: the handlers return before Close waits
	// for them.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	return srv.URL + "/acme/layer.git"
}

// hangingHandshake accepts the connection and never answers the smart-HTTP
// reference advertisement.
func hangingHandshake(http.ResponseWriter, *http.Request, <-chan struct{}) {}

// tricklingAdvertisement answers the reference advertisement with a status
// and headers, then sends one byte of an oversized pkt-line every 20ms, so
// the clone keeps receiving data and never completes.
func tricklingAdvertisement(w http.ResponseWriter, r *http.Request, stop <-chan struct{}) {
	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	_, _ = w.Write([]byte("001e# service=git-upload-pack\n0000fff0"))
	flusher.Flush()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-stop:
			return
		case <-tick.C:
			if _, err := w.Write([]byte("a")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Spec: §7.3.1 (Source unreachable), §13.12 — a Git snapshot runs under the
// PODIUM_GIT_FETCH_TIMEOUT deadline: a remote that never answers the
// handshake, or that trickles data, fails the snapshot as
// ingest.source_unreachable once the deadline passes, and the error names the
// setting.
// Matrix: §6.10 (ingest.source_unreachable)
func TestGit_SnapshotDeadline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request, <-chan struct{})
	}{
		{"hanging handshake", hangingHandshake},
		{"trickling remote", tricklingAdvertisement},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := stalledRemote(t, tc.handle)
			start := time.Now()
			_, err := source.Git{FetchTimeout: 300 * time.Millisecond}.Snapshot(context.Background(), source.LayerConfig{
				Repo: repo, Ref: "main",
			})
			elapsed := time.Since(start)
			if !errors.Is(err, source.ErrSourceUnreachable) {
				t.Fatalf("Snapshot = %v, want ErrSourceUnreachable", err)
			}
			if !strings.Contains(err.Error(), "PODIUM_GIT_FETCH_TIMEOUT (300ms)") {
				t.Errorf("error %q does not name PODIUM_GIT_FETCH_TIMEOUT and its value", err)
			}
			if elapsed > 10*time.Second {
				t.Errorf("Snapshot returned after %v, want shortly after the 300ms deadline", elapsed)
			}
		})
	}
}

// Spec: §7.3.1 (Source unreachable) — a snapshot that ends because the
// caller's context ended, before the PODIUM_GIT_FETCH_TIMEOUT deadline,
// fails as ingest.source_unreachable and does not blame the setting. A zero
// FetchTimeout selects the default deadline rather than none.
func TestGit_SnapshotCallerDeadlineDoesNotNameTheSetting(t *testing.T) {
	t.Parallel()
	repo := stalledRemote(t, hangingHandshake)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := source.Git{}.Snapshot(ctx, source.LayerConfig{Repo: repo, Ref: "main"})
	if !errors.Is(err, source.ErrSourceUnreachable) {
		t.Fatalf("Snapshot = %v, want ErrSourceUnreachable", err)
	}
	if strings.Contains(err.Error(), source.FetchTimeoutSetting) {
		t.Errorf("error %q names %s, but the caller's deadline ended the snapshot", err, source.FetchTimeoutSetting)
	}
}
