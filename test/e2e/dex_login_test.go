package e2e

// Live device-code login against the bundled Dex IdP.
//
// This is a live lane. It is off by default and self-skips when the bundled
// Dex is not reachable, so a plain `go test ./...` with no infrastructure stays
// green. Bring Dex up and run this test with the dedicated make target:
//
//	make test-auth-dex
//
// which runs `docker compose up -d dex` (issuer http://localhost:5556/dex) and
// then invokes this test. The test additionally honors an explicit opt-in env
// var (PODIUM_LIVE_DEX); setting it is not required when Dex is already up, but
// the CI lane sets it so the intent is legible alongside the other live lanes.
//
// What it exercises that the in-process stub (auth_oidc_test.go) does not: the
// real RFC 8628 device-authorization endpoint, real polling against a real
// token endpoint, and a real issued ID token whose sub/email come from Dex's
// static user. The stub-backed tests already cover prompt formatting, scope
// plumbing, audience plumbing, and the error-code exit mappings; this test does
// not repeat them. It asserts a single happy-path acquisition.
//
// Dex normally needs a browser to bind the user code, authenticate, and
// approve. The bundled config sets skipApprovalScreen, so approval is
// automatic; this test drives the user-code binding and the password form with
// plain HTTP POSTs (a cookie jar carries Dex's session across the redirect
// chain). `podium login` and the approval driver run concurrently: login polls
// the token endpoint while the driver completes the web side.
//
// Spec: §6.3 — `podium login` runs the OAuth device-code flow, prints the URL
// and code to stderr, and polls the IdP token endpoint until the user completes
// the flow. Spec: §6.3.1 — the IdP JWT carries {sub, email, ...} claims, which
// login decodes for the "Logged in as:" line.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// dexIssuer is the bundled Dex issuer reached from the host (docker-compose
// maps container port 5556 to the host). The device endpoint lives at
// <issuer>/device/code and the token endpoint at <issuer>/token.
const (
	dexIssuer         = "http://localhost:5556/dex"
	dexDeviceEndpoint = dexIssuer + "/device/code"
	dexTokenEndpoint  = dexIssuer + "/token"
	dexClientID       = "podium-registry"
	dexUserEmail      = "alice@acme.com"
	dexUserPassword   = "password"
)

// dexReachable reports whether the bundled Dex answers its OIDC discovery
// document. <issuer>/.well-known/openid-configuration is the natural probe: Dex
// always serves it once ready. A failed probe (connection refused, non-200)
// means the lane should skip rather than fail.
func dexReachable() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(dexIssuer + "/.well-known/openid-configuration")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// dexFormAction extracts the first <form ... action="..."> target from an HTML
// page and resolves it against base so a relative action becomes absolute. It
// returns an error rather than calling t.Fatalf, because the approval driver
// runs in a goroutine where FailNow is not allowed: it would silently kill the
// goroutine and leave the test hanging on its deadline instead of failing.
func dexFormAction(base, html string) (string, error) {
	m := regexp.MustCompile(`(?is)<form\b[^>]*\baction\s*=\s*["']([^"']+)["']`).FindStringSubmatch(html)
	if m == nil {
		return "", fmt.Errorf("no <form action=...> in Dex page (base %s)", base)
	}
	// Decode HTML entities Dex may emit in the query string (e.g. &amp;).
	action := strings.ReplaceAll(strings.TrimSpace(m[1]), "&amp;", "&")
	ref, err := url.Parse(action)
	if err != nil {
		return "", fmt.Errorf("parse form action %q: %w", action, err)
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base %q: %w", base, err)
	}
	return baseURL.ResolveReference(ref).String(), nil
}

// dexGet GETs u (following redirects) and returns the body and the final URL.
func dexGet(client *http.Client, u string) (body, finalURL string, err error) {
	resp, err := client.Get(u)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	return string(b), resp.Request.URL.String(), nil
}

// dexPost POSTs form to u (following redirects; a 303 becomes a GET) and returns
// the body and the final URL.
func dexPost(client *http.Client, u string, form url.Values) (body, finalURL string, err error) {
	resp, err := client.PostForm(u, form)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	return string(b), resp.Request.URL.String(), nil
}

// dexApprove drives the Dex web side of the device-code flow for the static
// user, given the verification_uri_complete that `podium login` surfaced. The
// sequence Dex v2.41 requires: GET the complete URI (Dex serves a code-confirm
// page), POST the user code to bind it (Dex redirects to the connector login
// form), then POST the static credentials. skipApprovalScreen makes the final
// approval automatic, so the credential POST completes the device request.
//
// It returns an error and never calls t.Fatalf, because it runs in a goroutine.
// A single http.Client carries a cookie jar so Dex's session survives the
// redirect chain across localhost:5556.
func dexApprove(completeURI string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("cookiejar: %w", err)
	}
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}

	cu, err := url.Parse(completeURI)
	if err != nil {
		return fmt.Errorf("parse complete uri %q: %w", completeURI, err)
	}
	userCode := cu.Query().Get("user_code")
	if userCode == "" {
		return fmt.Errorf("no user_code in verification uri %q", completeURI)
	}

	// Step 1: the complete URI serves the code-confirm page, whose form POSTs the
	// user code to /device/auth/verify_code.
	confirmPage, confirmURL, err := dexGet(client, completeURI)
	if err != nil {
		return fmt.Errorf("get code-confirm page: %w", err)
	}
	confirmAction, err := dexFormAction(confirmURL, confirmPage)
	if err != nil {
		return fmt.Errorf("code-confirm form: %w", err)
	}

	// Step 2: bind the user code. With a single password connector Dex
	// auto-selects it and 303-redirects (followed as GET) to the login form.
	loginPage, loginURL, err := dexPost(client, confirmAction, url.Values{"user_code": {userCode}})
	if err != nil {
		return fmt.Errorf("submit user code: %w", err)
	}
	loginAction, err := dexFormAction(loginURL, loginPage)
	if err != nil {
		return fmt.Errorf("login form (page %s): %w", loginURL, err)
	}

	// Step 3: submit the static credentials. Dex's local form posts `login` and
	// `password`; the form action carries the request state in its query string.
	// On success skipApprovalScreen auto-approves and the device request lands.
	afterPage, afterURL, err := dexPost(client, loginAction, url.Values{
		"login":    {dexUserEmail},
		"password": {dexUserPassword},
	})
	if err != nil {
		return fmt.Errorf("submit credentials: %w", err)
	}
	if strings.Contains(strings.ToLower(afterPage), "invalid") {
		return fmt.Errorf("Dex rejected the static credentials; check the bcrypt hash in deploy/compose/dex/config.yaml")
	}

	// Step 4 (defensive): a build with skipApprovalScreen off renders an approval
	// form here. The bundled config sets skipApprovalScreen, so this is normally
	// skipped; attempt it best-effort and let the token poll be the arbiter.
	low := strings.ToLower(afterPage)
	if strings.Contains(low, "approval") && strings.Contains(low, "<form") {
		if approveAction, ferr := dexFormAction(afterURL, afterPage); ferr == nil {
			af := url.Values{"approval": {"approve"}}
			if m := regexp.MustCompile(`(?is)name=["']req["'][^>]*value=["']([^"']+)["']`).FindStringSubmatch(afterPage); m != nil {
				af.Set("req", m[1])
			}
			_, _, _ = dexPost(client, approveAction, af)
		}
	}
	return nil
}

// dexDirectLinkRe matches the verification_uri_complete that `podium login`
// prints to stderr ("Direct link: <uri>"). login emits this line once it has
// the device-authorization response and before it begins polling, so a reader
// streaming stderr sees it while login is still running.
var dexDirectLinkRe = regexp.MustCompile(`Direct link:\s*(\S+)`)

// TestDexLogin_DeviceCodeFlow drives the real `podium login` device-code flow
// against the bundled Dex and asserts login acquires a token and prints the
// static user's sub and email.
//
// Spec: §6.3 (device-code flow, stderr prompt, poll-until-complete).
func TestDexLogin_DeviceCodeFlow(t *testing.T) {
	if !dexReachable() {
		t.Skip("bundled Dex not reachable at " + dexIssuer + "; run `make test-auth-dex` (or `docker compose up -d dex`)")
	}

	// Bound the whole flow well under any outer test timeout. The happy path
	// completes in a few seconds; this ceiling only guards against a wedged Dex.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// --registry must not be one of the no-auth short-circuits (a non-http path,
	// http://localhost:8080, or http://127.0.0.1:8080), or login exits 0 without
	// contacting Dex. A placeholder host gets past that guard; the issuer and
	// token URLs are passed explicitly, so the registry value is otherwise
	// unused. --token-url is required: Dex's device endpoint ends in
	// /device/code, which guessTokenURL does not rewrite.
	bin := cmdharness.Bin(t, "podium")
	cmd := exec.CommandContext(ctx, bin,
		"login",
		"--registry", "http://registry.acme.example",
		"--issuer", dexDeviceEndpoint,
		"--token-url", dexTokenEndpoint,
		"--client-id", dexClientID,
		"--scopes", "openid profile email groups",
	)
	// PODIUM_NO_BROWSER is pinned by mergeEnv so login never opens a browser.
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1")

	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start login: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	// Stream login's stderr: capture the full transcript for the final
	// assertions and, as the "Direct link:" line arrives, hand the
	// verification_uri_complete to the approval driver. The driver runs once.
	var transcript strings.Builder
	approveErr := make(chan error, 1)
	var approveStarted bool
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		transcript.WriteString(line)
		transcript.WriteByte('\n')
		if !approveStarted {
			if m := dexDirectLinkRe.FindStringSubmatch(line); m != nil {
				approveStarted = true
				completeURI := m[1]
				go func() {
					err := dexApprove(completeURI)
					approveErr <- err
					if err != nil {
						// Kill login so the stderr scanner unblocks and the
						// approval error surfaces immediately rather than waiting
						// out the deadline.
						cancel()
					}
				}()
			}
		}
	}
	// scanner ends at EOF when login closes stderr (on exit). A read error other
	// than EOF is reported but does not by itself fail the test; the wait below
	// is authoritative.
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Logf("stderr scan: %v", err)
	}

	if !approveStarted {
		t.Fatalf("login never printed a 'Direct link:' (verification_uri_complete); Dex may not have returned one.\ntranscript:\n%s", transcript.String())
	}
	// Surface an approval-driver failure with context before judging the exit.
	select {
	case err := <-approveErr:
		if err != nil {
			t.Fatalf("driving Dex approval failed: %v\nlogin transcript so far:\n%s", err, transcript.String())
		}
	case <-ctx.Done():
		t.Fatalf("Dex approval did not finish before the deadline\ntranscript:\n%s", transcript.String())
	}

	waitErr := cmd.Wait()
	out := transcript.String()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("login did not complete before the deadline\ntranscript:\n%s", out)
	}
	if waitErr != nil {
		t.Fatalf("podium login exited non-zero (%v); device-code acquisition against Dex failed.\ntranscript:\n%s", waitErr, out)
	}

	// A successful human-mode login prints the resolved identity and the success
	// line to stderr. Assert the observable acquisition and the static user's
	// claims as Dex issued them.
	if !strings.Contains(out, "Login successful") {
		t.Errorf("login did not report success.\ntranscript:\n%s", out)
	}
	if !strings.Contains(out, "Logged in as:") {
		t.Errorf("login did not print the resolved identity.\ntranscript:\n%s", out)
	}
	// The static user's email must appear in the decoded identity line.
	if !strings.Contains(out, "email="+dexUserEmail) {
		t.Errorf("expected the decoded identity to carry email=%s.\ntranscript:\n%s", dexUserEmail, out)
	}
	// A non-empty sub must be present (Dex emits it for the static userID). Match
	// the "sub=<value>" token on the identity line without pinning Dex's exact
	// subject encoding, which is an IdP-internal detail.
	if m := regexp.MustCompile(`sub=(\S+)`).FindStringSubmatch(out); m == nil || m[1] == "" {
		t.Errorf("expected a non-empty sub= in the decoded identity.\ntranscript:\n%s", out)
	}
}
