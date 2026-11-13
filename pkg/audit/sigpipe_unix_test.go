//go:build !windows

package audit

import (
	"context"
	"io"
	"os/signal"
	"syscall"
	"testing"
)

// Open ignores SIGPIPE when the list names stdout, so a write to a standard
// output whose reader has gone fails with EPIPE instead of killing the
// process. Ignoring SIGPIPE is process-wide, so the test asserts only the
// state after Open.
//
// Spec: §8.3
func TestOpen_StdoutIgnoresSIGPIPE(t *testing.T) {
	s, err := Open(context.Background(), "stdout", OpenOptions{Stdout: io.Discard})
	if err != nil {
		t.Fatalf("Open(stdout): %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if !signal.Ignored(syscall.SIGPIPE) {
		t.Fatalf("SIGPIPE is not ignored after Open(stdout)")
	}
}
