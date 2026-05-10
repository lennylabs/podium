package webhook_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
)

// Spec: §7.3.1 — GitHub webhooks signed with HMAC-SHA256 verify under
// the matching secret.
// Matrix: §6.10 (ingest.webhook_invalid)
func TestGitHub_VerifiesValidSha256(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "a-secret"
	sig, err := webhook.Sign("github", body, secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := (webhook.GitHub{}).Verify(body, sig, secret); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// Spec: §7.3.1 — wrong secret yields ErrInvalidSignature.
func TestGitHub_RejectsWrongSecret(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ref":"refs/heads/main"}`)
	sig, _ := webhook.Sign("github", body, "right")
	err := (webhook.GitHub{}).Verify(body, sig, "wrong")
	if !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("got %v, want ErrInvalidSignature", err)
	}
}

// Spec: §7.3.1 — body tampering invalidates the signature.
func TestGitHub_RejectsTamperedBody(t *testing.T) {
	t.Parallel()
	original := []byte(`{"ref":"refs/heads/main"}`)
	tampered := []byte(`{"ref":"refs/heads/evil"}`)
	sig, _ := webhook.Sign("github", original, "a-secret")
	err := (webhook.GitHub{}).Verify(tampered, sig, "a-secret")
	if !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("got %v, want ErrInvalidSignature", err)
	}
}

// Spec: §7.3.1 — sha1 fallback works for legacy webhooks.
func TestGitHub_AcceptsSha1Fallback(t *testing.T) {
	t.Parallel()
	// Hand-build a sha1 signature so we can test the fallback.
	body := []byte("hello")
	secret := "a-secret"
	// sha1 signature for ("hello", "a-secret"):
	// can be computed via the package's own helper... we don't expose
	// sha1 sign, so cross-check via Verify-on-correct-input:
	correct := computeHMACSHA1(body, secret)
	sig := "sha1=" + correct
	if err := (webhook.GitHub{}).Verify(body, sig, secret); err != nil {
		t.Errorf("Verify sha1: %v", err)
	}
}

// Spec: §7.3.1 — unsupported scheme rejected.
func TestGitHub_UnsupportedScheme(t *testing.T) {
	t.Parallel()
	err := (webhook.GitHub{}).Verify([]byte("body"), "md5=abcd", "secret")
	if !errors.Is(err, webhook.ErrUnsupportedScheme) {
		t.Errorf("got %v, want ErrUnsupportedScheme", err)
	}
}

// Spec: §7.3.1 — GitLab uses token equality.
func TestGitLab_TokenEquality(t *testing.T) {
	t.Parallel()
	if err := (webhook.GitLab{}).Verify(nil, "secret-token", "secret-token"); err != nil {
		t.Errorf("Verify: %v", err)
	}
	err := (webhook.GitLab{}).Verify(nil, "wrong", "secret-token")
	if !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("got %v, want ErrInvalidSignature", err)
	}
}

// Spec: §7.3.1 — Bitbucket uses HMAC-SHA256 without the sha256= prefix.
func TestBitbucket_VerifiesValid(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "a-secret"
	sig, _ := webhook.Sign("bitbucket", body, secret)
	if err := (webhook.Bitbucket{}).Verify(body, sig, secret); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// computeHMACSHA1 is a tiny test helper; the production code only
// signs with sha256.
func computeHMACSHA1(body []byte, secret string) string {
	// Local replication of HMAC-SHA1 so the test does not depend on
	// the production code's private functions.
	mac := hmacSHA1(secret, body)
	return mac
}
