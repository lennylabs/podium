package sign_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §4.7.11 — a trusted root file holds one root. The JSON Lines output
// of `gh attestation trusted-root`, one root per line, is refused rather
// than read by choosing a line, and the line kept alone loads. The digest
// of a root is the SHA-256 digest of the bytes as read.
func TestParseAttestationRoot_OneRootPerDocument(t *testing.T) {
	t.Parallel()
	pg, err := os.ReadFile("testdata/attestation/public-good.trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	gh, err := os.ReadFile("testdata/attestation/github.trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	ghLine := append(compact(t, gh), '\n')
	lines := append(append(compact(t, pg), '\n'), ghLine...)
	_, err = sign.ParseAttestationRoot(lines)
	if !errors.Is(err, sign.ErrAttestationRootInvalid) || !strings.Contains(err.Error(), "holds 2 trusted roots") {
		t.Fatalf("two lines: %v, want a refusal naming two roots", err)
	}
	r, err := sign.ParseAttestationRoot(ghLine)
	if err != nil {
		t.Fatalf("the GitHub line alone: %v", err)
	}
	sum := sha256.Sum256(ghLine)
	if r.Digest() != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("Digest = %s, want the SHA-256 of the bytes read", r.Digest())
	}
	if tlog, ctlog := r.ListsLogs(); tlog || ctlog {
		t.Fatalf("GitHub root ListsLogs = %v, %v, want neither", tlog, ctlog)
	}
	if err := sign.CheckPublicGoodRoot(r); !errors.Is(err, sign.ErrAttestationRootInvalid) {
		t.Fatalf("CheckPublicGoodRoot(GitHub root) = %v, want a refusal", err)
	}
	var zero sign.AttestationRoot
	if tlog, ctlog := zero.ListsLogs(); tlog || ctlog {
		t.Fatal("a zero root lists logs")
	}
}

// Spec: §4.7.11 — a public-good root that lists a transparency log and no
// certificate transparency log is refused.
func TestCheckPublicGoodRoot_NeedsCertificateTransparency(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	err := sign.CheckPublicGoodRoot(mustRoot(t, h.TrustedRootJSON()))
	if !errors.Is(err, sign.ErrAttestationRootInvalid) || !strings.Contains(err.Error(), "certificate transparency") {
		t.Fatalf("CheckPublicGoodRoot = %v, want the certificate transparency refusal", err)
	}
}

// Spec: §4.7.11 — the status of a root read from a file reports the file
// origin, the digest, the read time, and the SHA-1 authorities.
func TestStatusOf(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	r := mustRoot(t, h.TrustedRootJSON(sh.WithTimestampAuthorities(sh.TSASHA1)))
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	st := sign.StatusOf(r, at)
	if st.Origin != sign.RootFromFile || st.Digest != r.Digest() || !st.Time.Equal(at) || len(st.SHA1TimestampAuthorities) != 1 {
		t.Fatalf("StatusOf = %+v", st)
	}
}
