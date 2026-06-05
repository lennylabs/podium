package objectstore_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
)

// Spec: §13.12 — PODIUM_S3_ENDPOINT is a URL; its scheme selects
// TLS. https (and any non-http scheme) enables it, http disables it, a bare
// host defaults to TLS on, and an empty value resolves to AWS S3 over TLS.
func TestParseS3Endpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantHost string
		wantSSL  bool
	}{
		{"", "s3.amazonaws.com", true},
		{"s3.amazonaws.com", "s3.amazonaws.com", true},
		{"minio.example.com:9000", "minio.example.com:9000", true},
		{"https://minio.example.com", "minio.example.com", true},
		{"http://localhost:9000", "localhost:9000", false},
		{"https://play.min.io", "play.min.io", true},
		{"  https://trimmed.example.com  ", "trimmed.example.com", true},
	}
	for _, tc := range cases {
		host, ssl := objectstore.ParseS3Endpoint(tc.in)
		if host != tc.wantHost || ssl != tc.wantSSL {
			t.Errorf("ParseS3Endpoint(%q) = (%q, %v), want (%q, %v)", tc.in, host, ssl, tc.wantHost, tc.wantSSL)
		}
	}
}

// Spec: §13.12 — NewS3 accepts the force-path-style flag and
// constructs a client (the BucketLookup wiring is exercised here; the
// behavioral effect needs a live DNS endpoint). Construction is lazy, so no
// network call happens and the test never blocks.
func TestNewS3_ForcePathStyleConstructs(t *testing.T) {
	t.Parallel()
	for _, force := range []bool{false, true} {
		s, err := objectstore.NewS3(objectstore.S3Config{
			Endpoint:       "minio.example.com:9000",
			Bucket:         "b",
			Region:         "us-east-1",
			UseSSL:         false,
			ForcePathStyle: force,
		})
		if err != nil {
			t.Fatalf("NewS3(force=%v): %v", force, err)
		}
		if s == nil || s.Client == nil {
			t.Fatalf("NewS3(force=%v) returned nil client", force)
		}
	}
}

// Spec: §13.12 — NewS3 with no static credentials constructs
// successfully (the AWS credential chain is installed lazily); the constructor
// must not block on credential resolution.
func TestNewS3_NoCredentialsConstructs(t *testing.T) {
	t.Parallel()
	s, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: "s3.amazonaws.com",
		Bucket:   "b",
		Region:   "us-east-1",
		UseSSL:   true,
	})
	if err != nil {
		t.Fatalf("NewS3 (no creds): %v", err)
	}
	if s == nil {
		t.Fatal("nil store")
	}
}
