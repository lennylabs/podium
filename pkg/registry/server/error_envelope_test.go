package server

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

func TestErrorEnvelopeFor_MapsCoreErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not-found", core.ErrNotFound, "registry.not_found"},
		{"wrapped-not-found", &wrapErr{err: core.ErrNotFound}, "registry.not_found"},
		{"unavailable", core.ErrUnavailable, "registry.unavailable"},
		{"invalid-argument", core.ErrInvalidArgument, "registry.invalid_argument"},
		{"unknown", errors.New("mystery"), "registry.unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := errorEnvelopeFor(c.err)
			if env == nil || env.Code != c.want {
				t.Errorf("got %+v, want code=%s", env, c.want)
			}
		})
	}
}

func TestErrorEnvelopeFor_UnavailableMarksRetryable(t *testing.T) {
	t.Parallel()
	env := errorEnvelopeFor(core.ErrUnavailable)
	if !env.Retryable {
		t.Errorf("Retryable = false; want true for ErrUnavailable")
	}
}

type wrapErr struct {
	err error
}

func (w *wrapErr) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapErr) Unwrap() error { return w.err }
