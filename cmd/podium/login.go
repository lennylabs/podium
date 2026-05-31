package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/sync"
)

// loginTimeout bounds the whole device-code flow. spec: §7.7 — login
// polls "until the user completes the flow or a 10-minute timeout
// elapses". Overridable in tests.
var loginTimeout = 10 * time.Minute

// loginCmd resolves the registry from the merged config, discovers the
// IdP from the registry (unless overridden), runs the §6.3 OAuth Device
// Code flow under a 10-minute deadline, and caches the access + refresh
// tokens in the OS keychain keyed by registry URL. spec: §7.7.
func loginCmd(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	setUsage(fs, "Authenticate against the resolved registry via the OAuth device-code flow.")
	registry := fs.String("registry", "", "registry URL (resolved from the merged config when unset)")
	issuer := fs.String("issuer", os.Getenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT"), "OAuth device endpoint (overrides registry discovery)")
	tokenURL := fs.String("token-url", os.Getenv("PODIUM_OAUTH_TOKEN_URL"), "OAuth token endpoint")
	clientID := fs.String("client-id", envDefault("PODIUM_OAUTH_CLIENT_ID", "podium-cli"), "OAuth client ID")
	audience := fs.String("audience", os.Getenv("PODIUM_OAUTH_AUDIENCE"), "audience claim for the issued token")
	scopes := fs.String("scopes", "openid profile email groups", "space-separated OAuth scopes")
	noBrowser := fs.Bool("no-browser", false, "don't auto-open the verification URL")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}

	// F-7.7.4: resolve the registry from --registry, PODIUM_REGISTRY,
	// then the merged sync.yaml.
	reg, err := resolveClientRegistry(*registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	// F-7.7.5: login is a no-op when the registry needs no auth (a
	// filesystem path or the standalone server). Print a notice and exit.
	if isNoAuthRegistry(reg) {
		fmt.Fprintf(os.Stderr, "%s requires no authentication; nothing to do.\n", reg)
		return 0
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// F-7.7.5: discover the IdP from the registry when --issuer is unset.
	deviceURL := *issuer
	tokURL := *tokenURL
	if deviceURL == "" {
		dev, tok, derr := discoverIdP(reg, client)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "error: could not discover the identity provider from %s: %v; pass --issuer\n", reg, derr)
			return 2
		}
		deviceURL = dev
		if tokURL == "" {
			tokURL = tok
		}
	}
	if tokURL == "" {
		tokURL = guessTokenURL(deviceURL)
	}

	flow := identity.DeviceCodeFlow{
		DeviceAuthURL: deviceURL,
		TokenURL:      tokURL,
		ClientID:      *clientID,
		Scopes:        splitOn(*scopes),
		Audience:      *audience,
		Client:        client,
	}

	// F-7.7.6: bound the whole flow to the 10-minute deadline.
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	auth, err := flow.Initiate(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device authorization: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Visit:", auth.VerificationURL)
	fmt.Fprintln(os.Stderr, "User code:", auth.UserCode)
	target := auth.VerificationURLComplete
	if target == "" {
		target = auth.VerificationURL
	}
	if auth.VerificationURLComplete != "" {
		fmt.Fprintln(os.Stderr, "Direct link:", auth.VerificationURLComplete)
	}
	if !*noBrowser {
		openBrowser(target)
	}

	tokens, err := flow.Poll(ctx, auth)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "login timed out after %s\n", loginTimeout)
		} else {
			fmt.Fprintf(os.Stderr, "token polling: %v\n", err)
		}
		return 1
	}

	store := identity.KeychainStore{Service: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
	if err := saveTokens(store, reg, tokens); err != nil {
		fmt.Fprintf(os.Stderr, "save token: %v\n", err)
		return 1
	}
	if id := decodeIdentity(tokens.IDToken); id != "" {
		fmt.Fprintln(os.Stderr, "Logged in as:", id)
	}
	fmt.Fprintln(os.Stderr, "Login successful; token saved to keychain.")
	return 0
}

// resolveClientRegistry resolves the registry for login from --registry,
// then PODIUM_REGISTRY, then the merged sync.yaml's defaults.registry.
// spec: §7.7 (F-7.7.4).
func resolveClientRegistry(flagVal string) (string, error) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return resolveClientRegistryAt(flagVal, cwd, home)
}

func resolveClientRegistryAt(flagVal, cwd, home string) (string, error) {
	reg := flagVal
	if reg == "" {
		reg = os.Getenv("PODIUM_REGISTRY")
	}
	if reg == "" {
		merged, _, err := sync.LoadMergedConfig(cwd, home)
		if err == nil && merged != nil {
			reg = merged.Defaults.Registry
		}
	}
	if reg == "" {
		return "", fmt.Errorf("no registry configured: pass --registry, set PODIUM_REGISTRY, or run `podium init`")
	}
	return reg, nil
}

// isNoAuthRegistry reports whether the resolved registry needs no
// authentication: a filesystem path (no http/https scheme) or the
// standalone server. spec: §7.7 (F-7.7.5).
func isNoAuthRegistry(reg string) bool {
	if !strings.HasPrefix(reg, "http://") && !strings.HasPrefix(reg, "https://") {
		return true
	}
	return reg == "http://127.0.0.1:8080" || reg == "http://localhost:8080"
}

// discoverIdP fetches the registry's RFC 8414 authorization-server
// metadata and returns its device-authorization and token endpoints.
// spec: §7.7 (F-7.7.5) — the issuer flags become optional overrides.
func discoverIdP(registry string, client *http.Client) (deviceURL, tokenURL string, err error) {
	u := strings.TrimRight(registry, "/") + "/.well-known/oauth-authorization-server"
	resp, err := client.Get(u)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("metadata endpoint returned HTTP %d", resp.StatusCode)
	}
	var meta struct {
		DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
		TokenEndpoint               string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", "", err
	}
	if meta.DeviceAuthorizationEndpoint == "" {
		return "", "", fmt.Errorf("metadata has no device_authorization_endpoint")
	}
	return meta.DeviceAuthorizationEndpoint, meta.TokenEndpoint, nil
}

// openBrowser best-effort opens url in the system browser. It never
// blocks and ignores launch failures. spec: §7.7 — login "attempts to
// open the URL in the system browser".
func openBrowser(url string) {
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	_ = cmd.Start()
}

// saveTokens persists the access token (under the registry label) and,
// when present, the refresh token (under identity.RefreshLabel). spec: §7.7
// (F-7.7.7) — cache the access + refresh tokens for silent renewal.
func saveTokens(store identity.TokenStore, registry string, tokens *identity.Tokens) error {
	if err := store.Save(registry, tokens.AccessToken); err != nil {
		return err
	}
	if tokens.RefreshToken != "" {
		if err := store.Save(identity.RefreshLabel(registry), tokens.RefreshToken); err != nil {
			return err
		}
	}
	return nil
}

// decodeIdentity extracts sub, email, and OIDC groups from an ID token's
// payload for the success message. It returns "" for an absent or
// malformed token. spec: §7.7 — print the resolved identity.
func decodeIdentity(idToken string) string {
	if idToken == "" {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Sub    string   `json:"sub"`
		Email  string   `json:"email"`
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	var b strings.Builder
	add := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(s)
	}
	if claims.Sub != "" {
		add("sub=" + claims.Sub)
	}
	if claims.Email != "" {
		add("email=" + claims.Email)
	}
	if len(claims.Groups) > 0 {
		add("groups=" + strings.Join(claims.Groups, ","))
	}
	return b.String()
}

// logoutCmd removes the cached tokens for the resolved registry from the
// keychain. No remote revocation is performed; the IdP retains authority
// over token lifecycle. The registry resolves from --registry, then
// PODIUM_REGISTRY, then the merged sync.yaml, mirroring `podium login` so a
// bare `podium logout` works after `podium init`. spec: §7.7 (F-7.7.14).
//
//	podium logout [--registry URL]
func logoutCmd(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	setUsage(fs, "Remove the cached token for the resolved registry.")
	registry := fs.String("registry", "", "registry URL (resolved from the merged config when unset)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	reg, err := resolveClientRegistry(*registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	store := identity.KeychainStore{Service: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium")}
	if err := store.Delete(reg); err != nil {
		fmt.Fprintf(os.Stderr, "delete token: %v\n", err)
		return 1
	}
	_ = store.Delete(identity.RefreshLabel(reg))
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
