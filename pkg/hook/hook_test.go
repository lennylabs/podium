package hook

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

type uppercaser struct{}

func (uppercaser) ID() string { return "uppercaser" }
func (uppercaser) Apply(_ context.Context, _ map[string]any, f File) (Result, error) {
	f.Content = []byte(strings.ToUpper(string(f.Content)))
	return Result{File: f}, nil
}

type dropper struct{}

func (dropper) ID() string { return "dropper" }
func (dropper) Apply(_ context.Context, _ map[string]any, f File) (Result, error) {
	if strings.HasSuffix(f.Path, ".tmp") {
		f.Drop = true
	}
	return Result{File: f}, nil
}

// Spec: §6.6 step 4 / §9.1 — hooks chain in declared order; the second
// hook receives the first's output.
func TestRun_HooksChain(t *testing.T) {
	t.Parallel()
	in := []adapter.File{
		{Path: "x.txt", Content: []byte("hello")},
		{Path: "y.tmp", Content: []byte("temp")},
	}
	out, warnings, err := Run(context.Background(), []Hook{uppercaser{}, dropper{}}, nil, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(out) != 1 || out[0].Path != "x.txt" || string(out[0].Content) != "HELLO" {
		t.Errorf("got %+v", out)
	}
}

// cancelObserver aborts when its context is already cancelled, modeling the
// §9.3 "Cancellable" constraint: long-running work checks for cancellation.
type cancelObserver struct{}

func (cancelObserver) ID() string { return "cancel-observer" }
func (cancelObserver) Apply(ctx context.Context, _ map[string]any, f File) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{File: f}, nil
}

// Spec: §9.3 "Cancellable" — Run threads the caller's context to each hook's
// Apply, so a cancelled context propagates and aborts the chain. This is the
// behavioral counterpart to the static signature guard in test/conformance.
func TestRun_PropagatesContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := []adapter.File{{Path: "x.txt", Content: []byte("hello")}}
	_, _, err := Run(ctx, []Hook{cancelObserver{}}, nil, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with cancelled context: got err=%v, want context.Canceled", err)
	}
}

// Spec: §6.6 — with no hooks configured, files pass through unchanged.
func TestRun_NoHooksIsNoop(t *testing.T) {
	t.Parallel()
	in := []adapter.File{{Path: "x", Content: []byte("y")}}
	out, _, err := Run(context.Background(), nil, nil, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 1 || string(out[0].Content) != "y" {
		t.Errorf("noop hook chain modified output")
	}
}
