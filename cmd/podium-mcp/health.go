package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// healthProbeTimeout bounds the §13.9 health tool's registry ping so a
// hung or unreachable registry cannot block the MCP call indefinitely.
const healthProbeTimeout = 5 * time.Second

// healthResult is the §13.9 MCP health tool payload. It reports registry
// connectivity, the observed registry mode (ready / read_only / public /
// unreachable), the resolution cache size, and the timestamp of the last
// successful registry call. §13.10 names this tool as one of the surfaces
// that report public mode, so `public` joins the mode vocabulary.
type healthResult struct {
	Registry           string `json:"registry"`
	Connected          bool   `json:"connected"`
	Mode               string `json:"mode"`
	CacheSize          int    `json:"cache_size"`
	LastSuccessfulCall string `json:"last_successful_call,omitempty"`
}

// healthTool answers the §13.9 `health` MCP tool. It pings the
// configured registry, maps the registry's readiness state into the
// tool's ready / read_only / public / unreachable vocabulary, and reports
// the resolution cache size plus the last successful registry call
// timestamp. §13.2.1 names this tool as the surface that reports
// `mode: read_only` when the registry has flipped to read-only mode, and
// §13.10 names it as one of the surfaces that report `mode: public`.
func (s *mcpServer) healthTool() any {
	res := healthResult{
		Registry:  s.cfg.registry,
		CacheSize: s.resolutions.Len(),
	}
	res.Connected, res.Mode = s.probeRegistryMode()
	if t, ok := s.lastSuccessTime(); ok {
		res.LastSuccessfulCall = t.UTC().Format(time.RFC3339)
	}
	return res
}

// probeRegistryMode determines the §13.9 / §13.10 health mode. It reads
// the readiness state from /readyz (ready / read_only / unreachable) and,
// when the registry answered, checks /healthz for the public-mode signal.
// Public mode is a /healthz-only signal: the registry intentionally omits
// it from /readyz, whose vocabulary the spec restricts to ready /
// read_only / not_ready (pkg/registry/server/server.go readyMode). Because
// the registry's modeBanner ranks public above read_only and ready, a
// reachable registry reporting public on /healthz surfaces `public`
// regardless of its readiness state, so downstream tooling can detect
// public mode per §13.10 without inspecting startup config.
func (s *mcpServer) probeRegistryMode() (connected bool, mode string) {
	connected, mode = s.probeReadyMode()
	if !connected {
		// The registry did not answer at all; it cannot be in public mode.
		return connected, mode
	}
	if s.probeHealthzPublic() {
		return true, "public"
	}
	return connected, mode
}

// probeReadyMode pings <registry>/readyz and maps the result into the
// §13.9 readiness vocabulary. The registry's /readyz reports ready /
// read_only / not_ready; this maps not_ready (and any error status or
// unreadable body) to "unreachable" so the readiness mode is always one of
// ready / read_only / unreachable. The connected return reports whether
// the registry answered on the wire at all, which stays true even for a
// not_ready 503 so an operator can tell "answered but not ready" apart
// from "did not answer".
func (s *mcpServer) probeReadyMode() (connected bool, mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.registry+"/readyz", nil)
	if err != nil {
		return false, "unreachable"
	}
	if tok := s.currentToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false, "unreachable"
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// A successful readiness call counts toward last-successful-call.
		s.recordSuccess(time.Now())
		var parsed struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(body, &parsed)
		if parsed.Mode == "read_only" {
			return true, "read_only"
		}
		// ready, or an unlabeled 2xx: the registry is serving reads.
		return true, "ready"
	}
	// The registry answered but is not ready (503 not_ready) or returned
	// an error status: reachable on the wire, yet unusable for fresh
	// reads, which the tool reports as unreachable.
	return true, "unreachable"
}

// probeHealthzPublic reports whether <registry>/healthz announces
// `mode: public` (§13.10). /healthz is the liveness endpoint and the only
// surface carrying the public-mode banner; a non-2xx response, a transport
// failure, or any other mode value reads as not-public. This is a separate
// round-trip from probeReadyMode because /readyz never carries the public
// signal.
func (s *mcpServer) probeHealthzPublic() bool {
	ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.registry+"/healthz", nil)
	if err != nil {
		return false
	}
	if tok := s.currentToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var parsed struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(body, &parsed)
	return parsed.Mode == "public"
}
