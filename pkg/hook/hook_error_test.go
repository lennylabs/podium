package hook_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/hook"
)

// failingHook always returns an error so we can exercise the
// chain's abort path.
type failingHook struct{ id string }

func (f failingHook) ID() string { return f.id }
func (f failingHook) Apply(map[string]any, hook.File) (hook.Result, error) {
	return hook.Result{}, errors.New("boom")
}

// passthrough is a successful no-op hook used to verify that a
// later failure aborts the chain (warnings from earlier hooks
// also drop).
type passthrough struct{ id string }

func (p passthrough) ID() string { return p.id }
func (p passthrough) Apply(_ map[string]any, f hook.File) (hook.Result, error) {
	return hook.Result{File: f, Warnings: []string{p.id + ":ok"}}, nil
}

// Spec: §9.1 — when a MaterializationHook returns a non-nil
// error, the chain aborts and the error is wrapped with the
// failing hook's ID for operator diagnostics.
func TestRun_AbortsAndWrapsHookError(t *testing.T) {
	t.Parallel()
	hooks := []hook.Hook{passthrough{id: "ok"}, failingHook{id: "fail"}}
	files := []adapter.File{{Path: "a.md", Content: []byte("x")}}
	out, warnings, err := hook.Run(hooks, nil, files)
	if err == nil {
		t.Fatalf("err = nil, want abort")
	}
	if !strings.Contains(err.Error(), "fail") {
		t.Errorf("err = %v, want hook ID in message", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want underlying error wrapped", err)
	}
	if out != nil {
		t.Errorf("out = %+v, want nil on abort", out)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want empty on abort", warnings)
	}
}

// Spec: §9.1 — empty hook chain is a clean no-op: the input
// files pass through, no warnings are produced.
func TestRun_EmptyHookListIsNoop(t *testing.T) {
	t.Parallel()
	files := []adapter.File{{Path: "a.md", Content: []byte("x")}}
	out, warnings, err := hook.Run(nil, nil, files)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(out) != 1 || string(out[0].Content) != "x" {
		t.Errorf("out = %+v, want passthrough", out)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want empty", warnings)
	}
}
