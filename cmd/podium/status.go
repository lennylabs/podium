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
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Printf("registry:           %s\n", orMissing(*registry))
	fmt.Printf("harness:            %s\n", envOr("PODIUM_HARNESS", "none"))
	fmt.Printf("cache dir:          %s\n", envOr("PODIUM_CACHE_DIR", "~/.podium/cache/"))
	fmt.Printf("cache mode:         %s\n", envOr("PODIUM_CACHE_MODE", "always-revalidate"))
	fmt.Printf("overlay path:       %s\n", envOr("PODIUM_OVERLAY_PATH", "(disabled)"))
	fmt.Printf("identity provider:  %s\n", envOr("PODIUM_IDENTITY_PROVIDER", "(none)"))
	fmt.Printf("session token:      %s\n", maskedToken(os.Getenv("PODIUM_SESSION_TOKEN")))
	fmt.Printf("tenant:             %s\n", envOr("PODIUM_TENANT_ID", "(unset)"))

	if *registry != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, *registry+"/healthz", nil)
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
	}

	store := identity.KeychainStore{Service: envOr("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
	if *registry != "" {
		if _, err := store.Load(*registry); err == nil {
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
