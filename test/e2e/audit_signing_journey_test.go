package e2e

// Audit transparency-log signing, anchoring, verification, and PII redaction in
// one journey (gap G-AUTH-10).
//
// PII redaction is unit-tested in pkg/audit, anchoring and chain verification
// are integration-tested in audit_retention_reanchor_test.go, and
// discovery_search_test.go drives the redaction over the HTTP surface, but
// PODIUM_AUDIT_SIGNING_KEY_PATH is read only by serverboot and was never set in
// any test, so no single journey combined key-path signing, the anchor and
// verify intervals, redaction of a freeform query, and tamper detection. This
// boots one standalone server with all four enabled and walks the journey.
//
// The server is booted with a persisted Ed25519 signing key
// (PODIUM_AUDIT_SIGNING_KEY_PATH), a short anchor interval so the chain head is
// signed promptly, a short verify interval so an out-of-band edit is detected at
// runtime, and default-on PII redaction. A search_artifacts call carries an
// email in the freeform query. The journey then asserts:
//
//   - the exported audit log contains an Ed25519-signed audit.anchored event
//     whose envelope verifies against the public key read back from the key
//     file, over sha256:<chain head> (§8.6);
//   - the recorded search event redacts the email (§8.2);
//   - the on-disk hash chain verifies (§8.6); and
//   - a manually tampered entry breaks verification, which the running verify
//     scheduler also records as audit.gap_detected.
//
// Spec: §8.2 (free-text query redaction before audit, default-on), §8.6
// (transparency anchoring via the registry-managed key; periodic chain
// verification with automated gap detection), §4.7.9 (the registry-managed
// Ed25519 signing key). Gap G-AUTH-10.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// TestAuditSigningJourney_SignAnchorVerifyRedact boots a standalone server with
// audit signing, anchoring, verification, and PII redaction all enabled,
// searches with an email in the query, then asserts the chain is signed and
// verifiable, the query is redacted, and a tampered entry fails verification.
func TestAuditSigningJourney_SignAnchorVerifyRedact(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	auditPath := filepath.Join(home, "audit.log")
	keyPath := filepath.Join(home, "audit.key")

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_AUDIT_LOG_PATH=" + auditPath,
		"PODIUM_AUDIT_SIGNING_KEY_PATH=" + keyPath,
		// A 1-second anchor interval: the scheduler fires an immediate anchor on
		// boot (a no-op while the log is empty), then re-anchors the post-search
		// chain head within a tick.
		"PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=1",
		// A 1-second verify interval so the tampered entry is detected at runtime
		// and recorded as audit.gap_detected without waiting an hour.
		"PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS=1",
		// Default-on redaction is the default; set it explicitly so the journey
		// is self-describing.
		"PODIUM_PII_REDACTION=true",
	}, "serve", "--standalone", "--layer-path", writeRegistry(t, map[string]string{
		"finance/ledger/ARTIFACT.md": contextArtifact("quarterly ledger for finance"),
	}))

	// A freeform query carrying an email address (PII). The query also matches
	// the seeded artifact so the search resolves a result, but the redaction
	// applies regardless of the result set.
	const piiEmail = "carol@acme.com"
	query := "quarterly ledger owner " + piiEmail
	getJSON(t, srv.BaseURL+"/v1/search_artifacts?query="+url.QueryEscape(query), nil)

	// ---- The search query is redacted in the recorded event (§8.2) -----------
	if !brPollContains(auditPath, "[email-redacted]", 5*time.Second) {
		t.Fatalf("audit log never recorded the email redaction placeholder\nlog:\n%s", brReadOrEmpty(auditPath))
	}
	if body := brReadOrEmpty(auditPath); strings.Contains(body, piiEmail) {
		t.Errorf("audit log leaked the PII email %q despite redaction\nlog:\n%s", piiEmail, body)
	}

	// ---- An Ed25519-signed audit.anchored event covers the chain head (§8.6) -
	// The anchor scheduler signs sha256:<chain head> with the persisted key.
	// Poll until an anchored event lands after the search (the boot-time anchor
	// was a no-op on the empty log).
	if !brPollContains(auditPath, "audit.anchored", 8*time.Second) {
		t.Fatalf("audit log never recorded an audit.anchored event\nlog:\n%s", brReadOrEmpty(auditPath))
	}

	// Read the persisted public key back from the key file and verify the
	// anchored envelope against it. This proves the chain head is signed by the
	// registry-managed key the operator configured, not merely "anchored."
	pub := auditReadSigningPublicKey(t, keyPath)
	verifier := sign.RegistryManagedKey{PublicKey: pub}
	anchored := auditLastAnchored(t, auditPath)
	if anchored.Target == "" {
		t.Fatal("audit.anchored event has no chain-head target")
	}
	if err := verifier.Verify(context.Background(), "sha256:"+anchored.Target, anchored.Envelope); err != nil {
		t.Errorf("anchored envelope does not verify against the persisted signing key: %v", err)
	}
	// A tampered chain head must not verify under the same envelope, confirming
	// the signature binds to the specific head rather than verifying anything.
	if err := verifier.Verify(context.Background(), "sha256:"+flipLastHexNibble(anchored.Target), anchored.Envelope); err == nil {
		t.Error("anchored envelope verified against a forged chain head; the signature is not head-bound")
	}

	// ---- The on-disk hash chain verifies (§8.6) ------------------------------
	// Open a FileSink over the exported log and run the same Verify the runtime
	// scheduler runs. A clean chain returns nil.
	verifySink, err := audit.NewFileSink(auditPath)
	if err != nil {
		t.Fatalf("open audit log for verification: %v", err)
	}
	if err := verifySink.Verify(context.Background()); err != nil {
		t.Fatalf("freshly-exported audit chain failed verification: %v\nlog:\n%s", err, brReadOrEmpty(auditPath))
	}

	// ---- A manually tampered entry fails verification (§8.6) -----------------
	// Mutate one event's tamper-evident caller field while leaving its recorded
	// hash untouched, breaking that event's self-hash. Verify must then report
	// ErrChainBroken, and the running verify scheduler must record the gap.
	auditTamperOneCaller(t, auditPath)

	tamperedSink, err := audit.NewFileSink(auditPath)
	if err != nil {
		t.Fatalf("reopen tampered audit log: %v", err)
	}
	if err := tamperedSink.Verify(context.Background()); err == nil {
		t.Errorf("tampered audit chain verified clean; the edit was not detected\nlog:\n%s", brReadOrEmpty(auditPath))
	}
	// The runtime verify scheduler (1s interval) re-verifies and records the
	// break as audit.gap_detected, the §8.6 automated-detection backstop.
	if !brPollContains(auditPath, "audit.gap_detected", 8*time.Second) {
		t.Errorf("runtime verify scheduler did not record audit.gap_detected after the tamper\nlog:\n%s", brReadOrEmpty(auditPath))
	}
}

// auditReadSigningPublicKey parses the public key out of the two-line audit key
// file serverboot writes (a "public: <base64>" line), so the test can verify
// the anchored signature with the exact key the server signed with.
func auditReadSigningPublicKey(t *testing.T, keyPath string) ed25519.PublicKey {
	t.Helper()
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read audit signing key file: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "public:") {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "public:")))
		if err != nil {
			t.Fatalf("decode public key: %v", err)
		}
		if len(b) != ed25519.PublicKeySize {
			t.Fatalf("public key is %d bytes, want %d", len(b), ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(b)
	}
	t.Fatalf("audit signing key file has no public key line:\n%s", data)
	return nil
}

// anchoredEvent is the parsed shape of an audit.anchored event's fields the test
// needs: the chain-head target and the signature envelope in its context.
type anchoredEvent struct {
	Target   string
	Envelope string
}

// auditLastAnchored returns the most recent audit.anchored event in the log
// (its chain-head target and the signature envelope from the event context).
func auditLastAnchored(t *testing.T, auditPath string) anchoredEvent {
	t.Helper()
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var out anchoredEvent
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var je struct {
			Type    string            `json:"type"`
			Target  string            `json:"target"`
			Context map[string]string `json:"context"`
		}
		if err := json.Unmarshal([]byte(line), &je); err != nil {
			continue
		}
		if je.Type == "audit.anchored" {
			out = anchoredEvent{Target: je.Target, Envelope: je.Context["envelope"]}
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit.anchored event in the log:\n%s", data)
	}
	if out.Envelope == "" {
		t.Fatalf("audit.anchored event carries no signature envelope:\n%s", data)
	}
	return out
}

// auditTamperOneCaller rewrites the caller identity of the first
// artifacts.searched event in the log to a different value, leaving the
// recorded hash untouched so the event's self-hash no longer matches. This is
// the out-of-band edit §8.6 verification is built to catch.
func auditTamperOneCaller(t *testing.T, auditPath string) {
	t.Helper()
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log for tamper: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	tampered := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "" || !strings.Contains(line, `"type":"artifacts.searched"`) {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		caller, ok := raw["caller"]
		if !ok {
			// The event must carry a caller object to tamper; system:public
			// search still records caller.identity. Fall back to the target.
			raw["target"] = json.RawMessage(`"tampered-target"`)
		} else {
			// Flip a byte in the caller object's identity. Replacing the whole
			// object value with a syntactically valid but different identity
			// breaks the event's self-hash (caller participates in canonicalBody).
			_ = caller
			raw["caller"] = json.RawMessage(`{"identity":"tampered:attacker"}`)
		}
		out, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("re-marshal tampered event: %v", err)
		}
		lines[i] = string(out)
		tampered = true
		break
	}
	if !tampered {
		t.Fatalf("no artifacts.searched event to tamper in the log:\n%s", data)
	}
	if err := os.WriteFile(auditPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write tampered audit log: %v", err)
	}
}
