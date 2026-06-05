package serverboot

import (
	"bytes"
	"strings"
	"testing"
)

// Spec: §13.2.2 / §13.10 — the startup banner shows the public-mode
// warning, copied verbatim from §13.10 (the ⚠ glyph and both lines),
// when public mode is engaged.
func TestEmitStartupBanner_PublicMode(t *testing.T) {
	var buf bytes.Buffer
	emitStartupBanner(&buf, true)
	out := buf.String()

	wantLine1 := "⚠  PUBLIC MODE: all artifacts visible to all callers without authentication."
	wantLine2 := "   Bound to 127.0.0.1 by default; pass --allow-public-bind to bind a non-loopback address."
	if !strings.Contains(out, wantLine1) {
		t.Errorf("banner missing line 1.\ngot: %q\nwant substring: %q", out, wantLine1)
	}
	if !strings.Contains(out, wantLine2) {
		t.Errorf("banner missing line 2.\ngot: %q\nwant substring: %q", out, wantLine2)
	}
}

// Spec: §13.2.2 — the public-mode warning is absent when public mode is
// off; a standard or standalone deployment gets no warning banner.
func TestEmitStartupBanner_NonPublicMode(t *testing.T) {
	var buf bytes.Buffer
	emitStartupBanner(&buf, false)
	if buf.Len() != 0 {
		t.Errorf("non-public mode emitted a banner: %q", buf.String())
	}
}
