package sign_test

import (
	"context"
	"errors"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// staleSource is a root source that reports a status and cannot verify.
type staleSource struct{ st sign.AttestationRootStatus }

func (s staleSource) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, sign.ErrAttestationRootStale
}
func (s staleSource) Status() sign.AttestationRootStatus { return s.st }

// unreadableSource is a root source with no status that cannot be read.
type unreadableSource struct{}

func (unreadableSource) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, errors.New("unreadable")
}

// Spec: §4.7.11 (Root record) — the status of the root an instance uses: a
// file root reports its read time, a root a caller built reports its digest
// with an unknown time, a root past its maximum age reports its status and
// cannot verify, and an instance with no root or an unreadable one fails.
func TestRootStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := mustRoot(t, sh.New(t).TrustedRootJSON())
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	file := sign.FileAttestationRoot{Root: r, ReadAt: at}
	if got, _ := file.AttestationRoot(ctx); got != r {
		t.Error("a file root returned another root")
	}
	st, usable, err := sign.RootStatus(ctx, sign.AttestationInstance{Name: "f", Root: file})
	if err != nil || !usable || st.Digest != r.Digest() || !st.Time.Equal(at) {
		t.Errorf("file: %+v %v %v", st, usable, err)
	}
	st, usable, err = sign.RootStatus(ctx, sign.AttestationInstance{Name: "b", Root: r})
	if err != nil || !usable || st.Digest != r.Digest() || !st.Time.IsZero() {
		t.Errorf("built: %+v %v %v", st, usable, err)
	}
	stale := staleSource{st: sign.AttestationRootStatus{Digest: "sha256:old", Time: at}}
	if st, usable, err = sign.RootStatus(ctx, sign.AttestationInstance{Name: "s", Root: stale}); err != nil || usable || st.Digest != "sha256:old" {
		t.Errorf("stale: %+v %v %v", st, usable, err)
	}
	if _, _, err = sign.RootStatus(ctx, sign.AttestationInstance{Name: "u", Root: unreadableSource{}}); err == nil {
		t.Error("an unreadable root reported a status")
	}
	if _, _, err = sign.RootStatus(ctx, sign.AttestationInstance{Name: "n"}); err == nil {
		t.Error("an instance with no root reported a status")
	}
}
