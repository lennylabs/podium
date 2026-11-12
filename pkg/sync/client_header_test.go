package sync

import (
	"net/url"
	"testing"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/audit"
)

// TestClientHeaderValue pins the X-Podium-Client value podium sync sends: kind
// sync, name podium, the build version, and the harness it is given. An empty
// harness (a marketplace render) omits the member rather than sending it empty.
//
// Spec: §7.2.2
func TestClientHeaderValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		harness string
	}{
		{name: "named harness", harness: "claude-code"},
		{name: "none harness", harness: "none"},
		{name: "marketplace render", harness: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := ClientHeaderValue(tc.harness)
			got := audit.ParseClientHeader(v)
			want := audit.CallerClient{Kind: "sync", Name: "podium", Version: buildinfo.Version, Harness: tc.harness}
			if got == nil || *got != want {
				t.Fatalf("ClientHeaderValue(%q) parsed to %+v, want %+v", tc.harness, got, want)
			}
			q, err := url.ParseQuery(v)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", v, err)
			}
			if _, ok := q["harness"]; ok != (tc.harness != "") {
				t.Errorf("harness member present = %v in %q, want %v", ok, v, tc.harness != "")
			}
		})
	}
}
