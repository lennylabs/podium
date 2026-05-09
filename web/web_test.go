package web_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/lennylabs/podium/web"
)

// Spec: §13.10 — the embedded UI assets ship inside the binary so
// a single distribution covers the standalone deployment.
func TestAssets_HasIndex(t *testing.T) {
	assets := web.Assets()
	got, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "<title>Podium</title>") {
		t.Errorf("index.html missing title; got first 200 bytes: %.200s", got)
	}
}

// Spec: §13.10 — the SPA references app.js and style.css; both
// must be in the embedded set.
func TestAssets_HasJSAndCSS(t *testing.T) {
	assets := web.Assets()
	for _, path := range []string{"app.js", "style.css"} {
		if _, err := fs.ReadFile(assets, path); err != nil {
			t.Errorf("missing %s: %v", path, err)
		}
	}
}
