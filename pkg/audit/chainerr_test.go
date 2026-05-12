package audit

import (
	"errors"
	"strings"
	"testing"
)

func TestItoa(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		0:   "0",
		1:   "1",
		42:  "42",
		999: "999",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestChainErr_FormatsAndUnwraps(t *testing.T) {
	t.Parallel()
	base := errors.New("hash mismatch")
	err := chainErr{base: base, idx: 7, msg: "prev_hash drift"}
	msg := err.Error()
	if !strings.Contains(msg, "chain broken at index 7") {
		t.Errorf("msg = %q", msg)
	}
	if !strings.Contains(msg, "prev_hash drift") {
		t.Errorf("msg = %q", msg)
	}
	if !errors.Is(err, base) {
		t.Errorf("Unwrap did not return base")
	}
}

func TestFmtErr(t *testing.T) {
	t.Parallel()
	err := fmtErr("got %d items", 3)
	if !strings.Contains(err.Error(), "3 items") {
		t.Errorf("got %v", err)
	}
}

func TestFileSink_Path(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/audit.log"
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if got := sink.Path(); got != path {
		t.Errorf("Path = %q, want %q", got, path)
	}
}
