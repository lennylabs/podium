package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §2.1 / §6.4 step 2 — workspaceFromURI maps an MCP root URI to a
// filesystem path. Roots are file:// URIs; a bare absolute path is
// tolerated, and anything else yields no workspace. F-2.1.2.
func TestWorkspaceFromURI(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"file:///Users/alice/ws", "/Users/alice/ws"},
		{"file://localhost/Users/alice/ws", "/Users/alice/ws"},
		{"/Users/alice/ws", "/Users/alice/ws"},
		{"", ""},
		{"https://example.com/x", ""},
		{"relative/path", ""},
	}
	for _, c := range cases {
		if got := workspaceFromURI(c.in); got != c.want {
			t.Errorf("workspaceFromURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Spec: §6.4 step 2 — the bridge asks for roots/list only when no
// PODIUM_OVERLAY_PATH is set (step 1 wins) and the host advertised the
// roots capability, and it asks at most once. F-2.1.2.
func TestRequestRootsIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("explicit overlay path: no request", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		s := &mcpServer{cfg: &config{overlayPath: "/explicit"}, hostSupportsRoots: true}
		s.out = json.NewEncoder(&buf)
		s.requestRootsIfNeeded()
		if buf.Len() != 0 {
			t.Errorf("sent a request despite explicit overlay path: %s", buf.String())
		}
		if s.rootsRequested {
			t.Errorf("rootsRequested set despite explicit overlay path")
		}
	})

	t.Run("host lacks roots capability: no request", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		s := &mcpServer{cfg: &config{}, hostSupportsRoots: false}
		s.out = json.NewEncoder(&buf)
		s.requestRootsIfNeeded()
		if buf.Len() != 0 {
			t.Errorf("sent a request despite no roots capability: %s", buf.String())
		}
	})

	t.Run("roots-capable host, no overlay path: one request", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		s := &mcpServer{cfg: &config{}, hostSupportsRoots: true}
		s.out = json.NewEncoder(&buf)
		s.requestRootsIfNeeded()
		s.requestRootsIfNeeded() // idempotent
		out := buf.String()
		if strings.Count(out, "roots/list") != 1 {
			t.Errorf("want exactly one roots/list request, got: %s", out)
		}
		if !strings.Contains(out, rootsRequestID) {
			t.Errorf("request missing id %q: %s", rootsRequestID, out)
		}
	})
}

// Spec: §6.4 step 2 — applyRootsResponse resolves <workspace>/.podium/overlay
// from the host's roots/list reply and loads its records. F-2.1.2.
func TestApplyRootsResponse_ResolvesOverlay(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/draft/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\n---\n\ndraft body\n",
	})
	s := newTestServer(t, &config{})
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rootsRequestID,
		"result": map[string]any{
			"roots": []map[string]any{{"uri": "file://" + ws, "name": "ws"}},
		},
	})
	if !s.applyRootsResponse(line) {
		t.Fatalf("applyRootsResponse returned false for the roots reply")
	}
	if len(s.overlay) == 0 {
		t.Errorf("overlay not populated from roots reply")
	}
	if s.cfg.overlayPath != filepath.Join(ws, ".podium", "overlay") {
		t.Errorf("overlayPath = %q, want %q", s.cfg.overlayPath, filepath.Join(ws, ".podium", "overlay"))
	}
	if rec := s.overlayMatch("draft"); rec == nil {
		t.Errorf("overlay does not contain the 'draft' record")
	}
}

// Spec: §6.4 step 2 — only the bridge's own roots/list reply is consumed;
// requests, unrelated responses, and malformed lines pass through and leave
// the overlay untouched. F-2.1.2.
func TestApplyRootsResponse_IgnoresOtherMessages(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{})

	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if s.applyRootsResponse(req) {
		t.Errorf("consumed a request as a roots reply")
	}
	other, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "other", "result": map[string]any{}})
	if s.applyRootsResponse(other) {
		t.Errorf("consumed an unrelated response")
	}
	if s.applyRootsResponse([]byte("not json")) {
		t.Errorf("consumed malformed input")
	}
	if len(s.overlay) != 0 || s.cfg.overlayPath != "" {
		t.Errorf("overlay state mutated: overlay=%v path=%q", s.overlay, s.cfg.overlayPath)
	}
}

// Spec: §6.4 step 2 — a root without a .podium/overlay directory consumes
// the reply (so it is not re-dispatched) but leaves the overlay disabled.
// F-2.1.2.
func TestApplyRootsResponse_NoOverlayDir(t *testing.T) {
	t.Parallel()
	ws := t.TempDir() // no .podium/overlay underneath
	s := newTestServer(t, &config{})
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rootsRequestID,
		"result":  map[string]any{"roots": []map[string]any{{"uri": "file://" + ws}}},
	})
	if !s.applyRootsResponse(line) {
		t.Errorf("should consume the roots reply even when no overlay dir exists")
	}
	if len(s.overlay) != 0 {
		t.Errorf("overlay should remain empty, got %v", s.overlay)
	}
}

// Spec: §2.1 / §6.4 step 2 — end to end across the serve loop: a roots-capable
// host triggers a roots/list request, and the host's reply resolves the
// workspace overlay so a later read finds the draft. F-2.1.2.
func TestServe_RootsListResolvesWorkspaceOverlay(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/draft/ARTIFACT.md",
		Content: "---\ntype: context\nversion: 0.1.0\n---\n\ndraft body\n",
	})
	s := newTestServer(t, &config{})

	initReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"roots": map[string]any{"listChanged": true}},
		},
	})
	rootsReply, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": rootsRequestID,
		"result": map[string]any{"roots": []map[string]any{{"uri": "file://" + ws}}},
	})
	in := bytes.NewReader(append(append(initReq, '\n'), append(rootsReply, '\n')...))
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(out.String(), `"roots/list"`) {
		t.Errorf("server did not emit a roots/list request:\n%s", out.String())
	}
	if len(s.overlay) == 0 {
		t.Errorf("overlay not resolved from the roots reply")
	}
	if rec := s.overlayMatch("draft"); rec == nil {
		t.Errorf("expected overlay to contain the 'draft' record")
	}
}

// Spec: §6.4 step 2 — when no host roots capability is advertised, the serve
// loop sends no roots/list request and the overlay stays disabled. F-2.1.2.
func TestServe_NoRootsCapabilityNoRequest(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{})
	initReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": protocolVersion},
	})
	in := bytes.NewReader(append(initReq, '\n'))
	var out bytes.Buffer
	if err := s.serve(in, &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if strings.Contains(out.String(), "roots/list") {
		t.Errorf("server emitted roots/list without a host roots capability:\n%s", out.String())
	}
	if len(s.overlay) != 0 {
		t.Errorf("overlay unexpectedly populated: %v", s.overlay)
	}
}
