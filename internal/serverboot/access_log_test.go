package serverboot

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// Spec: §7.1 — the access log is on by default so a fresh deployment has a
// timing surface; PODIUM_ACCESS_LOG=false (or 0/off/no) silences it, and an
// explicit truthy value keeps it on.
func TestAccessLogEnabled(t *testing.T) {
	cases := map[string]bool{
		"":      true, // unset → default on
		"true":  true,
		"1":     true,
		"on":    true,
		"false": false,
		"0":     false,
		"off":   false,
		"no":    false,
	}
	for val, want := range cases {
		// An empty value reads the same as unset (os.Getenv returns "" for
		// both), exercising envBoolPtr's empty → nil → default-on path.
		t.Setenv("PODIUM_ACCESS_LOG", val)
		if got := accessLogEnabled(); got != want {
			t.Errorf("PODIUM_ACCESS_LOG=%q: accessLogEnabled() = %v, want %v", val, got, want)
		}
	}
}

// Spec: §7.1 — the default observer emits one structured key=value line per
// request, keyed by operation name, with the elapsed time in milliseconds
// at microsecond precision so an operator can compare against the SLO
// budgets.
func TestAccessLogObserver_Format(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(orig)
		log.SetFlags(flags)
	})

	accessLogObserver()("load_artifact", 200, 12500*time.Microsecond)

	line := strings.TrimSpace(buf.String())
	for _, want := range []string{
		"op=load_artifact",
		"status=200",
		"duration_ms=12.500",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("access log line %q missing %q", line, want)
		}
	}
}
