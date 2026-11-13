package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

// writeRecorder records each Write call's bytes separately.
type writeRecorder struct {
	mu     sync.Mutex
	writes [][]byte
}

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (w *writeRecorder) snapshot() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.writes...)
}

// A stdout destination opened through Open writes each record as one JSON
// line in a single Write call, and each line equals the file destination's
// line for the same record byte for byte.
//
// Spec: §8.3
func TestOpen_StdoutWritesOneLinePerRecord(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	w := &writeRecorder{}
	s := openFileStream(t, path+",stdout", OpenOptions{Stdout: w})
	appendN(t, s, 2)
	writes := w.snapshot()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileLines := bytes.SplitAfter(data, []byte("\n"))
	if len(writes) != 2 || len(fileLines) != 3 {
		t.Fatalf("got %d writes and %d file lines, want 2 and 2", len(writes), len(fileLines)-1)
	}
	for i, got := range writes {
		if !bytes.Equal(got, fileLines[i]) {
			t.Errorf("write %d = %q, want the file line %q", i, got, fileLines[i])
		}
	}
}

// A stream whose only destination is stdout chains its records against its
// own head, so each line's prev_hash is the previous line's hash.
//
// Spec: §8.3, §8.6
func TestOpen_StdoutAloneChainsItsLines(t *testing.T) {
	t.Parallel()
	w := &writeRecorder{}
	s, err := Open(context.Background(), "stdout", OpenOptions{Stdout: w})
	if err != nil {
		t.Fatalf("Open(stdout): %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if s.File() != nil {
		t.Fatalf("a stdout-only stream has a file destination")
	}
	appendN(t, s, 3)
	writes := w.snapshot()
	if len(writes) != 3 {
		t.Fatalf("stdout received %d writes, want 3", len(writes))
	}
	prev := ""
	for i, line := range writes {
		var je jsonEvent
		if err := json.Unmarshal(line, &je); err != nil {
			t.Fatalf("line %d %q: %v", i, line, err)
		}
		if je.PrevHash != prev || je.Hash == "" {
			t.Fatalf("line %d prev_hash = %q, want %q (hash %q)", i, je.PrevHash, prev, je.Hash)
		}
		prev = je.Hash
	}
}

// A stdout destination returns its writer's error unchanged, so the stream
// can count it as write_error.
//
// Spec: §8.3
func TestStdoutDest_ReturnsWriteError(t *testing.T) {
	t.Parallel()
	d := &stdoutDest{w: failWriter{err: syscall.EPIPE}}
	if err := d.write(record{line: []byte(`{}`)}); !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("write = %v, want EPIPE", err)
	}
}

// TestStdoutDest_LeavesSharedLineUnchanged pins that writing a record's line
// with its newline never writes into the line's backing array, which the
// other destinations of the stream read after the stdout write.
//
// Spec: §8.3
func TestStdoutDest_LeavesSharedLineUnchanged(t *testing.T) {
	t.Parallel()
	backing := []byte(`{"a":1}XXXX`)
	line := backing[:7]
	w := &writeRecorder{}
	d := &stdoutDest{w: w}
	if err := d.write(record{line: line}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := string(backing); got != `{"a":1}XXXX` {
		t.Fatalf("backing array = %q, want it unchanged", got)
	}
	if len(w.writes) != 1 || string(w.writes[0]) != "{\"a\":1}\n" {
		t.Fatalf("writes = %q, want one line with its newline", w.writes)
	}
}
