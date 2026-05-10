package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
)

// loginCmd runs the §6.3 OAuth Device Code flow and persists the
// resulting token to the OS keychain (or the configured TokenStore).
//
//	podium login [--registry URL] [--issuer URL] [--client-id ID] [--audience URI]
//
// The flow:
//
//  1. POST to <issuer>/oauth2/device with client_id + scopes.
//  2. Print the user code + verification URL to stderr.
//  3. Poll <issuer>/oauth2/token until the user completes the flow.
//  4. Save the resulting access token under PODIUM_TOKEN_KEYCHAIN_NAME
//     (default `podium`) keyed by the registry URL.
func loginCmd(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	setUsage(fs, "Run the OAuth Device Code flow and persist the token to the keychain.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL (used as the keychain entry label)")
	issuer := fs.String("issuer", os.Getenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT"), "OAuth issuer URL (or its device endpoint)")
	tokenURL := fs.String("token-url", os.Getenv("PODIUM_OAUTH_TOKEN_URL"), "OAuth token endpoint (defaults to <issuer>/token)")
	clientID := fs.String("client-id", envDefault("PODIUM_OAUTH_CLIENT_ID", "podium-cli"), "OAuth client ID")
	audience := fs.String("audience", os.Getenv("PODIUM_OAUTH_AUDIENCE"), "audience claim for the issued token")
	scopes := fs.String("scopes", "openid profile email groups", "space-separated OAuth scopes")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	if *issuer == "" {
		fmt.Fprintln(os.Stderr, "error: --issuer or PODIUM_OAUTH_AUTHORIZATION_ENDPOINT is required")
		return 2
	}
	deviceURL := *issuer
	tokURL := *tokenURL
	if tokURL == "" {
		tokURL = guessTokenURL(*issuer)
	}

	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: deviceURL,
		TokenURL:      tokURL,
		ClientID:      *clientID,
		Scopes:        splitOn(*scopes),
		Audience:      *audience,
		Client:        &http.Client{Timeout: 30 * time.Second},
	}
	ctx := context.Background()
	auth, err := flow.Initiate(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device authorization: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Visit:", auth.VerificationURL)
	fmt.Fprintln(os.Stderr, "User code:", auth.UserCode)
	if auth.VerificationURLComplete != "" {
		fmt.Fprintln(os.Stderr, "Direct link:", auth.VerificationURLComplete)
	}
	tokens, err := flow.Poll(ctx, auth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "token polling: %v\n", err)
		return 1
	}
	store := identity.KeychainStore{Service: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
	if err := store.Save(*registry, tokens.AccessToken); err != nil {
		fmt.Fprintf(os.Stderr, "save token: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Login successful; token saved to keychain.")
	return 0
}

// logoutCmd removes the token entry for the configured registry from
// the keychain. No remote revocation is performed; the IdP retains
// authority over token lifecycle.
//
//	podium logout [--registry URL]
func logoutCmd(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	setUsage(fs, "Remove the cached token for the configured registry.")
	registry := fs.String("registry", os.Getenv("PODIUM_REGISTRY"), "registry URL (keychain entry label)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *registry == "" {
		fmt.Fprintln(os.Stderr, "error: --registry is required")
		return 2
	}
	store := identity.KeychainStore{Service: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
	if err := store.Delete(*registry); err != nil {
		fmt.Fprintf(os.Stderr, "delete token: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Logout successful.")
	return 0
}

// guessTokenURL synthesizes a token URL from a device-auth URL when
// --token-url isn't supplied. Most IdPs expose `/token` next to
// `/device`.
func guessTokenURL(deviceURL string) string {
	for _, suffix := range []string{"/device", "/oauth2/device", "/v1/oauth/device"} {
		if len(deviceURL) > len(suffix) && deviceURL[len(deviceURL)-len(suffix):] == suffix {
			return deviceURL[:len(deviceURL)-len(suffix)] + replaceSuffix(suffix, "/device", "/token")
		}
	}
	return deviceURL + "/token"
}

func replaceSuffix(s, oldSuffix, newSuffix string) string {
	if len(s) >= len(oldSuffix) && s[len(s)-len(oldSuffix):] == oldSuffix {
		return s[:len(s)-len(oldSuffix)] + newSuffix
	}
	return s
}

func splitOn(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ' ' || r == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
