package sync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §7.1 / §7.5.2 dispatch — "a URL routes to a Podium server, a
// filesystem path routes to local filesystem." podium sync materializes
// only the filesystem source, so Run rejects an http(s):// registry with
// the canonical config.server_source_unsupported error instead of handing
// the URL to filesystem.Open (which collapses it into a bogus path under
// the working directory and reports a misleading "registry path does not
// exist"). This is the regression the dispatch fix removes.
func TestRun_ServerSourceURLRejected(t *testing.T) {
	t.Parallel()
	for _, url := range []string{
		"https://podium.acme.com",
		"http://127.0.0.1:8080",
		"https://podium.acme.com/sub/path",
	} {
		url := url
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			_, err := Run(Options{RegistryPath: url, Target: t.TempDir(), AdapterID: "none"})
			if !errors.Is(err, ErrServerSourceUnsupported) {
				t.Fatalf("Run(%q) error = %v, want ErrServerSourceUnsupported", url, err)
			}
			// The pre-fix failure mode was a mangled filesystem path; the
			// canonical error must not mention filesystem.Open's message.
			if strings.Contains(err.Error(), "registry path does not exist") {
				t.Fatalf("Run(%q) leaked the mangled filesystem error: %v", url, err)
			}
		})
	}
}

// Spec: §7.1 / §7.5.2 — the server-source guard fires before the missing
// target check, so even a dry-run against a URL reports the dispatch error
// rather than appearing to succeed.
func TestRun_ServerSourceURLRejectedOnDryRun(t *testing.T) {
	t.Parallel()
	_, err := Run(Options{RegistryPath: "https://podium.acme.com", DryRun: true})
	if !errors.Is(err, ErrServerSourceUnsupported) {
		t.Fatalf("dry-run Run error = %v, want ErrServerSourceUnsupported", err)
	}
}

// Spec: §7.5.2 — a filesystem path (and a file:// URI) is a filesystem
// source, not a server source, so the dispatch guard must not intercept
// it. A bare relative/absolute path proceeds to filesystem composition; a
// nonexistent one surfaces filesystem.Open's error, which is explicitly
// NOT the server-source error.
func TestRun_FilesystemSourceNotRejected(t *testing.T) {
	t.Parallel()

	// A real filesystem registry materializes normally.
	registry := t.TempDir()
	testharness.WriteTree(t, registry,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "team/glossary/ARTIFACT.md", Content: contextArtifactSrc},
	)
	if _, err := Run(Options{RegistryPath: registry, Target: t.TempDir(), AdapterID: "none"}); err != nil {
		t.Fatalf("filesystem Run: %v", err)
	}

	// A file:// URI and a nonexistent path are filesystem sources: they
	// must not trip the server-source guard.
	for _, fsSrc := range []string{
		"file:///tmp/does-not-exist-podium",
		"/tmp/does-not-exist-podium-xyz",
		"./relative-missing",
	} {
		_, err := Run(Options{RegistryPath: fsSrc, Target: t.TempDir(), AdapterID: "none"})
		if errors.Is(err, ErrServerSourceUnsupported) {
			t.Fatalf("Run(%q) wrongly classified a filesystem source as a server source", fsSrc)
		}
	}
}

// Spec: §7.1 / §7.5.2 — `podium sync --watch` against a server-source URL
// fails fast with the same canonical error, synchronously, instead of
// spawning a poller that can never stat the URL as a directory.
func TestWatch_ServerSourceURLRejected(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := Watch(ctx, WatchOptions{Sync: Options{
		RegistryPath: "https://podium.acme.com",
		Target:       t.TempDir(),
		AdapterID:    "none",
	}})
	if !errors.Is(err, ErrServerSourceUnsupported) {
		t.Fatalf("Watch error = %v, want ErrServerSourceUnsupported", err)
	}
	if ch != nil {
		t.Fatalf("Watch returned a channel %v alongside the error; the watcher goroutine must not start", ch)
	}
}

// isServerSource is the §7.1 / §7.5.2 scheme classifier: only http:// and
// https:// route to a server; bare paths and file:// URIs are filesystem
// sources.
func TestIsServerSource(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://podium.acme.com": true,
		"http://localhost:8080":   true,
		"HTTPS://podium.acme.com": false, // strict, lower-case scheme only
		"file:///srv/registry":    false,
		"/srv/registry":           false,
		"./.podium/registry":      false,
		"registry":                false,
		"":                        false,
	}
	for in, want := range cases {
		if got := isServerSource(in); got != want {
			t.Errorf("isServerSource(%q) = %v, want %v", in, got, want)
		}
	}
}
