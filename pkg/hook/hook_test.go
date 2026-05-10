package hook

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

type uppercaser struct{}

func (uppercaser) ID() string { return "uppercaser" }
func (uppercaser) Apply(_ map[string]any, f File) (Result, error) {
	f.Content = []byte(strings.ToUpper(string(f.Content)))
	return Result{File: f}, nil
}

type dropper struct{}

func (dropper) ID() string { return "dropper" }
func (dropper) Apply(_ map[string]any, f File) (Result, error) {
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
	out, warnings, err := Run([]Hook{uppercaser{}, dropper{}}, nil, in)
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

// Spec: §6.6 — with no hooks configured, files pass through unchanged.
func TestRun_NoHooksIsNoop(t *testing.T) {
	t.Parallel()
	in := []adapter.File{{Path: "x", Content: []byte("y")}}
	out, _, err := Run(nil, nil, in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 1 || string(out[0].Content) != "y" {
		t.Errorf("noop hook chain modified output")
	}
}
