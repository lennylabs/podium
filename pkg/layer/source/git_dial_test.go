package source

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// Spec: §7.3.1, §13.12 — the deadline dial covers every ssh remote, ssh://
// and scp-like, and leaves the clone's proxy options empty for http, https,
// git://, and file remotes. A released token no longer resolves to a dialer.
func TestSnapshotProxyOptions_RoutesSSHRemotes(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{"ssh://git@git.acme.com/acme/layer.git", "git@git.acme.com:acme/layer.git"} {
		opts, release := snapshotProxyOptions(context.Background(), repo)
		u, err := url.Parse(opts.URL)
		if err != nil || u.Scheme != deadlineDialScheme {
			t.Fatalf("%s: proxy URL = %q (%v), want the %s scheme", repo, opts.URL, err, deadlineDialScheme)
		}
		if _, err := proxy.FromURL(u, proxy.Direct); err != nil {
			t.Errorf("%s: FromURL before release = %v, want a dialer", repo, err)
		}
		release()
		if _, err := proxy.FromURL(u, proxy.Direct); err == nil || !strings.Contains(err.Error(), "no snapshot") {
			t.Errorf("%s: FromURL after release = %v, want a refusal", repo, err)
		}
	}
	for _, repo := range []string{"https://git.acme.com/acme/layer.git", "http://git.acme.com/acme/layer.git",
		"git://git.acme.com/acme/layer.git", "file:///srv/acme/layer", "://not a url"} {
		opts, release := snapshotProxyOptions(context.Background(), repo)
		release()
		if opts.URL != "" {
			t.Errorf("%s: proxy URL = %q, want none", repo, opts.URL)
		}
	}
}

// Spec: §7.3.1, §13.12 — a connection the deadline dialer opens carries the
// snapshot deadline and is closed when the snapshot context ends.
func TestDeadlineDialer_TiesTheConnectionToTheSnapshot(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	conn, err := deadlineDialer{ctx: ctx}.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	cancel()
	if _, err := conn.Read(make([]byte, 1)); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Read after the snapshot ended = %v, want a closed connection", err)
	}

	expired, cancelExpired := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelExpired()
	<-expired.Done()
	if _, err := (deadlineDialer{ctx: expired}).DialContext(context.Background(), "tcp", ln.Addr().String()); err == nil {
		t.Errorf("DialContext under an expired snapshot context succeeded, want an error")
	}
}
