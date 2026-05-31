package integration

import (
	"net/http"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// Spec: §12 (F-12.0.8) — "ETag caching of immutable artifact versions." The
// standalone registry server publishes the content hash as the load_artifact
// ETag and answers a matching If-None-Match with 304 Not Modified, end to end.
func TestRegistry_LoadArtifactETagRoundTrip(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path:    "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: x\n---\n\nbody\n",
		},
	)

	resp, err := http.Get(h.URL + "/v1/load_artifact?id=x")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("load_artifact response carried no ETag")
	}

	req, _ := http.NewRequest(http.MethodGet, h.URL+"/v1/load_artifact?id=x", nil)
	req.Header.Set("If-None-Match", etag)
	cond, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer cond.Body.Close()
	if cond.StatusCode != http.StatusNotModified {
		t.Errorf("conditional GET status = %d, want 304", cond.StatusCode)
	}
}
