package objectstore_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/objectstore"
)

// liveS3 reads PODIUM_S3_* env vars and returns a configured S3
// Provider, or skips when the environment is unconfigured. The
// recommended free target for nightly CI is play.min.io with the
// publicly-known credentials (kept stable by MinIO for testing):
//
//	PODIUM_S3_ENDPOINT=play.min.io
//	PODIUM_S3_BUCKET=podium-ci
//	PODIUM_S3_REGION=us-east-1
//	PODIUM_S3_USE_SSL=true
//	PODIUM_S3_ACCESS_KEY_ID=Q3AM3UQ867SPQQA43P2F
//	PODIUM_S3_SECRET_ACCESS_KEY=zuf+tfteSlswRu7BJ86wekitnifILbZam1KYY3TG
//
// Local devs running MinIO themselves point Endpoint at
// localhost:9000 with USE_SSL=false. Production smokes use AWS S3
// with a CI service account.
func liveS3(t *testing.T) *objectstore.S3 {
	t.Helper()
	endpoint := os.Getenv("PODIUM_S3_ENDPOINT")
	bucket := os.Getenv("PODIUM_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("PODIUM_S3_ENDPOINT/BUCKET unset; skipping live S3 smoke")
	}
	cfg := objectstore.S3Config{
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          envOr("PODIUM_S3_REGION", "us-east-1"),
		AccessKeyID:     os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		UseSSL:          os.Getenv("PODIUM_S3_USE_SSL") != "false",
	}
	s3, err := objectstore.NewS3(cfg)
	if err != nil {
		t.Skipf("NewS3: %v", err)
	}
	return s3
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// uniqueKey returns a random key prefix so concurrent CI jobs don't
// collide on the shared test bucket.
func uniqueKey(t *testing.T, suffix string) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "podium-test/" + hex.EncodeToString(buf) + "/" + suffix
}

// Spec: §13.10 — S3 backend round-trip end-to-end against a live
// S3-compatible endpoint. Gated on PODIUM_S3_* env vars; default
// CI runs skip the test.
func TestS3_LivePutGetRoundTrip(t *testing.T) {
	s := liveS3(t)
	ctx := context.Background()
	key := uniqueKey(t, "round-trip")
	body := []byte("podium live S3 smoke " + key)
	if err := s.Put(ctx, key, body, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = s.Delete(ctx, key) })

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("Get returned %q, want %q", got, body)
	}
}

// Spec: §6.2 — S3.Presign returns a Signature V4 URL the consumer
// can follow to fetch the body without sending credentials. Tests
// use a real http.Client so the signature is exercised end-to-end.
func TestS3_LivePresignRoundTrip(t *testing.T) {
	s := liveS3(t)
	ctx := context.Background()
	key := uniqueKey(t, "presign")
	body := []byte("presigned content")
	if err := s.Put(ctx, key, body, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { _ = s.Delete(ctx, key) })

	url, err := s.Presign(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("Presign: %v", err)
	}
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Errorf("Presign URL missing AWS signature: %s", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET presigned: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned GET status = %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(body) {
		t.Errorf("presigned GET returned %q, want %q", got, body)
	}
}

// Spec: §6.10 — S3.Get on a missing key returns ErrNotFound.
func TestS3_LiveGetMissingReturnsErrNotFound(t *testing.T) {
	s := liveS3(t)
	_, err := s.Get(context.Background(), uniqueKey(t, "definitely-missing"))
	if !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}
