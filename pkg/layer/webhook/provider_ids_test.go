package webhook_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/layer/webhook"
)

func TestProviderIDs(t *testing.T) {
	t.Parallel()
	cases := map[string]webhook.Provider{
		"github":    webhook.GitHub{},
		"gitlab":    webhook.GitLab{},
		"bitbucket": webhook.Bitbucket{},
	}
	for want, p := range cases {
		if got := p.ID(); got != want {
			t.Errorf("%T.ID() = %q, want %q", p, got, want)
		}
	}
}

func TestGitLab_VerifyTokenEquality(t *testing.T) {
	t.Parallel()
	gl := webhook.GitLab{}
	if err := gl.Verify(nil, "secret-token", "secret-token"); err != nil {
		t.Errorf("matching token: %v", err)
	}
	if err := gl.Verify(nil, "wrong", "right"); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("mismatched token: %v", err)
	}
	if err := gl.Verify(nil, "", "right"); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("empty signature: %v", err)
	}
}

func TestBitbucket_VerifyHMACSHA256(t *testing.T) {
	t.Parallel()
	const secret = "shh"
	body := []byte("payload")
	sig, err := webhook.Sign("bitbucket", body, secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bb := webhook.Bitbucket{}
	if err := bb.Verify(body, sig, secret); err != nil {
		t.Errorf("matching: %v", err)
	}
	if err := bb.Verify(body, "deadbeef", secret); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("mismatched: %v", err)
	}
	if err := bb.Verify(body, "", secret); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Errorf("empty sig: %v", err)
	}
}
