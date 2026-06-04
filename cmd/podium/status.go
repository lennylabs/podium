package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/sync"
)

// statusCmd prints a one-screen summary of the current Podium
// client setup: configured registry, identity, cache directory,
// and whether the registry is reachable.
//
//	podium status
//
// Useful as a first diagnostic when something doesn't work.
func statusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	setUsage(fs, "Print a diagnostic summary of the current Podium client setup.")
	registryFlag := fs.String("registry", "", "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	// §7.5.2: resolve the registry and harness the way `podium sync` and
	// `podium config show` do — the flag, then the PODIUM_* env var, then the
	// merged sync.yaml — so this diagnostic reflects what a sync would actually
	// use, not only the process environment. A relative filesystem registry
	// resolves against the workspace.
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	merged, workspace, _ := sync.LoadMergedConfig(cwd, home)
	registry := *registryFlag
	if registry == "" {
		registry = os.Getenv("PODIUM_REGISTRY")
	}
	if registry == "" && merged != nil {
		registry = merged.Defaults.Registry
	}
	if registry != "" {
		ws := workspace
		if ws == "" {
			ws = cwd
		}
		registry = sync.ResolveRegistryPath(ws, registry)
	}
	harness := os.Getenv("PODIUM_HARNESS")
	if harness == "" && merged != nil {
		harness = merged.Defaults.Harness
	}
	if harness == "" {
		harness = "none"
	}
	fmt.Printf("registry:           %s\n", orMissing(registry))
	fmt.Printf("harness:            %s\n", harness)
	fmt.Printf("cache dir:          %s\n", envOr("PODIUM_CACHE_DIR", "~/.podium/cache/"))
	fmt.Printf("cache mode:         %s\n", envOr("PODIUM_CACHE_MODE", "always-revalidate"))
	fmt.Printf("overlay path:       %s\n", envOr("PODIUM_OVERLAY_PATH", "(disabled)"))
	fmt.Printf("identity provider:  %s\n", envOr("PODIUM_IDENTITY_PROVIDER", "(none)"))
	fmt.Printf("session token:      %s\n", maskedToken(os.Getenv("PODIUM_SESSION_TOKEN")))
	fmt.Printf("tenant:             %s\n", envOr("PODIUM_TENANT_ID", "(unset)"))

	// Reachability, scope preview, and the keychain token only apply to a
	// server-source registry (an HTTP URL). A filesystem-source registry has no
	// /healthz endpoint, so probing it would print a spurious UNREACHABLE.
	serverURL := registry != "" && sync.IsServerSource(registry)
	if registry != "" && !serverURL {
		fmt.Printf("source:             filesystem (no server to reach)\n")
	}
	if serverURL {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, registry+"/healthz", nil)
		resp, err := http.DefaultClient.Do(req)
		switch {
		case err != nil:
			fmt.Printf("reachability:       UNREACHABLE (%v)\n", err)
		case resp.StatusCode == http.StatusOK:
			fmt.Printf("reachability:       OK\n")
			// §13.2.2: surface the registry's mode so operators
			// can see public_mode without inspecting startup config.
			if mode := decodeHealthMode(resp); mode != "" {
				fmt.Printf("registry mode:      %s\n", mode)
			}
		default:
			fmt.Printf("reachability:       HTTP %d\n", resp.StatusCode)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}

		// §3.5: podium status surfaces the same scope-preview aggregate
		// counts the MCP server, SDK, and podium sync expose, for human
		// inspection of "what could this identity have loaded?".
		fmt.Printf("scope preview:\n")
		switch preview, err := fetchScopePreview(registry); {
		case err == nil:
			printScopePreview(os.Stdout, preview)
		case isScopePreviewDisabled(err):
			fmt.Printf("  (disabled by tenant config expose_scope_preview)\n")
		default:
			fmt.Printf("  (unavailable: %v)\n", err)
		}

		store := identity.KeychainStore{Service: envOr("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
		if _, err := store.Load(registry); err == nil {
			fmt.Printf("keychain token:     present\n")
		} else {
			fmt.Printf("keychain token:     not found (run `podium login`)\n")
		}
	}
	return 0
}

func orMissing(s string) string {
	if s == "" {
		return "(unset; set PODIUM_REGISTRY or pass --registry)"
	}
	return s
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// decodeHealthMode extracts the `mode` field from a /healthz JSON
// response. Returns "" when the body can't be decoded so the
// status command falls back gracefully.
func decodeHealthMode(resp *http.Response) string {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return body.Mode
}

// maskedToken returns the first eight chars of a JWT followed by
// `…`, so the token's presence is visible without leaking the full
// secret to a screenshot or shared terminal.
func maskedToken(tok string) string {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "(unset)"
	}
	if len(tok) <= 12 {
		return "(set)"
	}
	return tok[:8] + "…"
}
