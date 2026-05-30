package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"
)

// tokenWatchInterval is the cadence at which the session-token file is
// stat-polled for changes. The per-call fresh read in currentToken() is the
// authoritative rotation path (§6.3.2.1); the watch surfaces a rotation
// promptly and independently of request traffic.
const tokenWatchInterval = time.Second

// startTokenWatch installs the §6.3.2.1 rotation mechanisms the contract
// names beyond the per-call fresh read: a SIGHUP forced re-read and a watch
// on PODIUM_SESSION_TOKEN_FILE. It returns a stop function that the serve
// loop defers so the watcher goroutine and signal registration are released
// when the bridge exits.
func (s *mcpServer) startTokenWatch() func() {
	return startTokenWatch(s.cfg.sessionTokenFile, tokenWatchInterval, func(reason string) {
		// Re-read the token so a rotation is observed immediately and
		// surfaced to the operator. currentToken() remains the authoritative
		// per-call read; this keeps the contract's two explicit mechanisms
		// wired without changing request behavior.
		_ = s.currentToken()
		fmt.Fprintf(os.Stderr, "podium-mcp: session token re-read (%s)\n", reason)
	})
}

// startTokenWatch wires the real SIGHUP channel and the file ticker into
// watchLoop and returns a stop function. file may be empty (no file watch);
// the SIGHUP handler is always installed where the platform supports it.
func startTokenWatch(file string, interval time.Duration, reload func(reason string)) func() {
	sig := make(chan os.Signal, 1)
	if hs := hangupSignals(); len(hs) > 0 {
		signal.Notify(sig, hs...)
	}
	done := make(chan struct{})
	go watchLoop(file, interval, sig, reload, done)
	return func() {
		signal.Stop(sig)
		close(done)
	}
}

// watchLoop fires reload on a SIGHUP-channel send and on a detected mtime
// change of file. It is the testable core of the watcher: tests drive it
// with a controlled signal channel and a temp file. The loop exits when
// done is closed.
func watchLoop(file string, interval time.Duration, sighup <-chan os.Signal, reload func(reason string), done <-chan struct{}) {
	var lastMod time.Time
	if file != "" {
		if fi, err := os.Stat(file); err == nil {
			lastMod = fi.ModTime()
		}
	}
	var tick <-chan time.Time
	if file != "" && interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-sighup:
			reload("SIGHUP")
		case <-tick:
			fi, err := os.Stat(file)
			if err != nil {
				continue
			}
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				reload("file changed")
			}
		case <-done:
			return
		}
	}
}
