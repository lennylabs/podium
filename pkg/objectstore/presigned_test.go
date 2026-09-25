package objectstore_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
)

// Spec: §13.12 — a URL is SigV4 presigned when its query carries a non-empty
// X-Amz-Signature; the /objects route, an empty signature, and a URL that
// fails to parse are not.
func TestPresignedSigV4(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]bool{
		"https://bucket.s3.example/key?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=abc": true,
		"https://registry.example/objects/abc":                                               false,
		"https://bucket.s3.example/key?X-Amz-Signature=":                                     false,
		"http://[::1%zz/objects/abc":                                                         false,
	} {
		if got := objectstore.PresignedSigV4(raw); got != want {
			t.Errorf("PresignedSigV4(%q) = %v, want %v", raw, got, want)
		}
	}
}
