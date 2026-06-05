package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// serverWatchReconnectDelay is the pause before re-opening a dropped
// event stream. Bounded so a flapping connection does not busy-loop.
const serverWatchReconnectDelay = 500 * time.Millisecond

// runServerWatch is the server-source watcher goroutine (§7.5.4). It runs an
// initial sync, then subscribes to the registry's §7.5 change-event stream
// (`artifact.published`, `artifact.deprecated`, `layer.config_changed`) and
// reruns the sync on every event. It owns the events channel and closes it
// on exit. Reruns are debounced so a burst of events coalesces into one
// sync.
func runServerWatch(ctx context.Context, opts WatchOptions, events chan<- WatchEvent) {
	defer close(events)

	emit := func(res *Result, err error) {
		select {
		case events <- WatchEvent{Result: res, Err: err}:
		case <-ctx.Done():
		}
	}

	// Initial sync materializes the current effective view.
	res, err := Run(opts.Sync)
	emit(res, err)

	trigger := make(chan struct{}, 1)
	go streamServerEvents(ctx, opts.Sync.RegistryPath, opts.Sync.HTTPClient, trigger)

	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-trigger:
			if !ok {
				// The stream goroutine exited; it only does so when ctx is
				// done, so stop here rather than rerun on a closed channel.
				return
			}
			if timer == nil {
				timer = time.NewTimer(opts.Debounce)
				timerC = timer.C
			} else {
				timer.Reset(opts.Debounce)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			res, err := Run(opts.Sync)
			emit(res, err)
		}
	}
}

// streamServerEvents subscribes to /v1/events for the §7.5.4 change events
// and signals trigger on each one. It reconnects after a dropped stream
// until ctx is canceled, then closes trigger. The §7.5 event types are
// requested server-side so every delivered line (apart from heartbeats) is
// relevant to the watcher.
func streamServerEvents(ctx context.Context, registry string, client *http.Client, trigger chan<- struct{}) {
	defer close(trigger)

	// A streaming subscription must not carry an overall client timeout, or
	// the long-lived connection would be cut mid-stream. Cancellation runs
	// through the request context instead.
	stream := &http.Client{}
	if client != nil {
		stream = &http.Client{Transport: client.Transport}
	}
	base := strings.TrimRight(registry, "/")
	url := base + "/v1/events?type=artifact.published&type=artifact.deprecated&type=layer.config_changed"

	for ctx.Err() == nil {
		readServerEventStream(ctx, stream, url, trigger)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(serverWatchReconnectDelay):
		}
	}
}

// readServerEventStream opens one /v1/events connection and signals trigger
// for every non-heartbeat NDJSON line until the stream ends or ctx is done.
func readServerEventStream(ctx context.Context, client *http.Client, url string, trigger chan<- struct{}) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Event string `json:"event"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Event == "" || ev.Event == "_heartbeat" {
			continue
		}
		// Coalesce: a full trigger buffer already has a pending rerun.
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
}
