package lint

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// urlRecord is an agent whose body links each of hrefs.
func urlRecord(id string, hrefs ...string) filesystem.ArtifactRecord {
	var b strings.Builder
	b.WriteString("---\ntype: agent\n---\n")
	for i, h := range hrefs {
		fmt.Fprintf(&b, "See [link %d](%s).\n", i, h)
	}
	return filesystem.ArtifactRecord{ID: id, Artifact: &manifest.Artifact{Type: manifest.TypeAgent}, ArtifactBytes: []byte(b.String())}
}

// Spec: §4.4 — a URL that several references name is probed once, every
// reference to a failing URL draws its own diagnostic under its artifact,
// and the diagnostics keep the order of a serial check, a bundled-file
// failure included.
func TestProseReference_ProbesEachURLOnce(t *testing.T) {
	t.Parallel()
	var heads sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := heads.LoadOrStore(r.URL.Path, new(atomic.Int32))
		n.(*atomic.Int32).Add(1)
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ok, gone := srv.URL+"/ok", srv.URL+"/gone"
	rule := ruleProseReferenceResolution{HTTPClient: srv.Client(), Concurrency: 4}
	diags := rule.Check(context.Background(), nil, []filesystem.ArtifactRecord{
		urlRecord("a/one", ok, gone, "./missing.md"),
		urlRecord("a/two", gone, ok),
		urlRecord("a/three", ok),
	})
	var got []string
	for _, d := range diags {
		got = append(got, d.ArtifactID+" "+d.Message)
	}
	want := []string{
		fmt.Sprintf("a/one prose URL %q HEAD returned HTTP 404", gone),
		"a/one prose reference \"./missing.md\" does not resolve to a bundled file or a known artifact",
		fmt.Sprintf("a/two prose URL %q HEAD returned HTTP 404", gone),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, path := range []string{"/ok", "/gone"} {
		n, _ := heads.Load(path)
		if n == nil || n.(*atomic.Int32).Load() != 1 {
			t.Errorf("%s probed %v times, want once", path, n)
		}
	}
}

// Spec: §4.4, §13.12 — the run never holds more than Concurrency probes in
// flight, and reaches that many when there is enough work.
func TestProseReference_BoundsConcurrentProbes(t *testing.T) {
	t.Parallel()
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}))
	defer srv.Close()
	var hrefs []string
	for i := range 12 {
		hrefs = append(hrefs, fmt.Sprintf("%s/p%d", srv.URL, i))
	}
	rule := ruleProseReferenceResolution{HTTPClient: srv.Client(), Concurrency: 3}
	if diags := rule.Check(context.Background(), nil, []filesystem.ArtifactRecord{urlRecord("a/x", hrefs...)}); len(diags) != 0 {
		t.Fatalf("diagnostics = %v, want none", diags)
	}
	if p := peak.Load(); p != 3 {
		t.Errorf("peak in-flight probes = %d, want 3", p)
	}
}

// Spec: §4.4 — a probe that misses its deadline fails, and a cancelled run
// probes nothing and reports no URL failure.
func TestProseReference_DeadlineAndCancellation(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	rule := ruleProseReferenceResolution{HTTPClient: srv.Client(), Concurrency: 2, timeout: 50 * time.Millisecond}
	diags := rule.Check(context.Background(), nil, []filesystem.ArtifactRecord{urlRecord("a/slow", srv.URL+"/slow")})
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "HEAD failed") {
		t.Errorf("slow probe diagnostics = %v, want one HEAD failure", diags)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := rule.probeURLs(ctx, []string{srv.URL + "/a", srv.URL + "/b"}); len(got) != 0 {
		t.Errorf("cancelled run failures = %v, want none", got)
	}
}

// Spec: §4.4 — a run above 20 distinct URLs logs its start, its progress no
// more than once per 10 seconds, and its closing counts; a smaller run logs
// nothing.
func TestProseReference_ProgressLogs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/bad") {
			w.WriteHeader(http.StatusGone)
		}
	}))
	defer srv.Close()
	hrefs := func(n int) []string {
		out := []string{srv.URL + "/x/bad"}
		for i := range n - 1 {
			out = append(out, fmt.Sprintf("%s/u%d", srv.URL, i))
		}
		return out
	}
	var mu sync.Mutex
	var clock time.Time
	tick := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(4 * time.Second)
		return clock
	}
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	rule := ruleProseReferenceResolution{HTTPClient: srv.Client(), Concurrency: 1, Logf: logf, now: tick}
	rule.probeURLs(context.Background(), hrefs(25))
	progress := 0
	for _, l := range lines {
		if strings.Contains(l, "checked") && strings.Contains(l, " of 25 URLs") {
			progress++
		}
	}
	// One tick per probe at 4 seconds each puts a progress line every third
	// probe; the clock reading that starts the run is the first tick.
	if len(lines) < 3 || lines[0] != "lint.prose_reference: checking 25 URLs, 1 at a time" ||
		!strings.HasPrefix(lines[len(lines)-1], "lint.prose_reference: checked 25 URLs in ") ||
		!strings.HasSuffix(lines[len(lines)-1], ", 1 failed") || progress != 8 {
		t.Errorf("progress lines (%d progress):\n%s", progress, strings.Join(lines, "\n"))
	}

	lines = nil
	rule.probeURLs(context.Background(), hrefs(20))
	if len(lines) != 0 {
		t.Errorf("a 20-URL run logged %q", lines)
	}
}
