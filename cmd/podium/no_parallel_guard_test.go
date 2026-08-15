package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// Spec: n/a — a guard on this package's test suite rather than on product
// behavior.
//
// The capture helpers in this package (captureStderr, captureStdout) swap the
// process-wide os.Stderr and os.Stdout, because the commands under test write
// their diagnostics straight to those variables. A test running in parallel
// with a capture either loses its output into the capturing test's pipe or
// reads the variable while the swap writes it, which is a data race and shows
// up in CI as an intermittent failure in an unrelated test. The suite runs in
// about two seconds serially, so the parallelism is not worth the flake.
//
// This guard fails when t.Parallel() reappears, because the failure it causes
// is intermittent and lands on whichever test loses the race rather than on
// the one that introduced it. `go test -race ./cmd/podium/` reproduces the
// underlying races when the rule is broken.
func TestNoParallelTests(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "no_parallel_guard_test.go" {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(line) == "t.Parallel()" {
				offenders = append(offenders, name+":"+strconv.Itoa(i+1))
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("t.Parallel() is not allowed in this package because the capture helpers swap os.Stderr and os.Stdout process-wide; found at:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
