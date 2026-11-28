package attest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/sign"
)

// Spec: §4.7.11 (Trusted roots of a consumer, Policy load) — the public-good
// instance is built only for a file whose signer names it, and a failure to
// build it refuses the file.
func TestLoadPolicyFileWith_BuildsPublicGoodOnDemand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyOnly := filepath.Join(dir, "none.json")
	pg := filepath.Join(dir, "pg.json")
	if err := os.WriteFile(keyOnly, []byte(`{"version":1,"rules":[{"ids":["**"],"mode":"none"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pg, []byte(`{"version":1,"rules":[{"ids":["**"],"mode":"require","requirements":[{"signers":[`+
		`{"issuer":"https://accounts.acme.com","identity":"alice@acme.com","instance":"public-good"}]}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	build := func() (sign.AttestationInstance, error) {
		calls++
		return sign.AttestationInstance{}, errors.New("no root")
	}
	if _, err := attest.LoadPolicyFileWith(keyOnly, build, time.Now()); err != nil || calls != 0 {
		t.Fatalf("a file naming no public-good signer: %v, %d builds", err, calls)
	}
	_, err := attest.LoadPolicyFileWith(pg, build, time.Now())
	if f, ok := attest.FailureOf(err); !ok || f.Code != attest.CodePolicyUnavailable || !strings.Contains(err.Error(), "no root") || calls != 1 {
		t.Fatalf("a failed build: %v, %d builds", err, calls)
	}
}
