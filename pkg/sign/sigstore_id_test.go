package sign_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

func TestSigstoreKeyless_ID(t *testing.T) {
	t.Parallel()
	if got := (sign.SigstoreKeyless{}).ID(); got != "sigstore-keyless" {
		t.Errorf("ID = %q", got)
	}
}
