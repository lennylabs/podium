package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// initWithClient builds an initialize request carrying the given clientInfo
// version and the current protocol version, and returns the handler response.
func initWithClient(srv *mcpServer, clientVersion string) rpcResponse {
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": protocolVersion,
		"clientInfo":      map[string]any{"name": "test-host", "version": clientVersion},
	})
	return srv.handle(rpcRequest{JSONRPC: "2.0", Method: "initialize", Params: params})
}

// Spec: §6.9 "Binary version mismatch with host caller" — a host caller whose
// version is below the configured floor is refused with mcp.client_too_old so
// the host's CLI can prompt an update.
func TestHandle_InitializeRefusesOldClient(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{minClientVersion: "1.2.0"}}
	resp := initWithClient(srv, "1.1.9")
	if resp.Error == nil {
		t.Fatalf("expected refusal for old host caller, got %+v", resp.Result)
	}
	if !strings.Contains(resp.Error.Message, "mcp.client_too_old") {
		t.Errorf("message = %q, want mcp.client_too_old", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "1.1.9") || !strings.Contains(resp.Error.Message, "1.2.0") {
		t.Errorf("message should name both versions: %q", resp.Error.Message)
	}
}

// Spec: §6.9 — a host caller at or above the floor proceeds normally.
func TestHandle_InitializeAcceptsCurrentClient(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.2.0", "1.2.1", "2.0.0"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			srv := &mcpServer{cfg: &config{minClientVersion: "1.2.0"}}
			resp := initWithClient(srv, v)
			if resp.Error != nil {
				t.Errorf("client %q at/above floor refused: %v", v, resp.Error)
			}
		})
	}
}

// Spec: §6.9 — the check is opt-in: with no floor configured no host caller is
// refused, since host version strings are not portably comparable.
func TestHandle_InitializeNoFloorNeverRefuses(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{}} // no minClientVersion
	resp := initWithClient(srv, "0.0.1")
	if resp.Error != nil {
		t.Errorf("no floor must not refuse any client: %v", resp.Error)
	}
}

// Spec: §6.9 — an absent or unparsable host version cannot establish a
// mismatch, so the binary serves rather than locking the host out.
func TestHandle_InitializeUnparsableClientNotRefused(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "nightly", "v2"} {
		v := v
		t.Run("version="+v, func(t *testing.T) {
			t.Parallel()
			srv := &mcpServer{cfg: &config{minClientVersion: "1.2.0"}}
			resp := initWithClient(srv, v)
			if resp.Error != nil {
				t.Errorf("unparsable/absent client version %q must not be refused: %v", v, resp.Error)
			}
		})
	}
}

// Spec: §6.9 — clientVersionRefusal is the unit under the handler: it returns
// (msg, true) only when a configured floor is provably above a parsable host
// version.
func TestClientVersionRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		floor, client string
		wantRefuse    bool
	}{
		{"", "1.0.0", false},        // no floor
		{"1.2.0", "", false},        // no client version
		{"1.2.0", "1.1.0", true},    // below floor
		{"1.2.0", "1.2.0", false},   // equal
		{"1.2.0", "1.3.0", false},   // above
		{"1.2.0", "garbage", false}, // unparsable client
		{"garbage", "1.0.0", false}, // unparsable floor
	}
	for _, tc := range cases {
		s := &mcpServer{cfg: &config{minClientVersion: tc.floor}}
		_, refuse := s.clientVersionRefusal(tc.client)
		if refuse != tc.wantRefuse {
			t.Errorf("clientVersionRefusal(floor=%q, client=%q) refuse=%v, want %v", tc.floor, tc.client, refuse, tc.wantRefuse)
		}
	}
}
