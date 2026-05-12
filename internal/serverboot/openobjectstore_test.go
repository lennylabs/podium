package serverboot

import (
	"strings"
	"testing"
)

func TestOpenObjectStore_NoneReturnsNil(t *testing.T) {
	t.Parallel()
	got, err := openObjectStore(&Config{objectStore: "none"})
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestOpenObjectStore_S3RequiresBucket(t *testing.T) {
	t.Parallel()
	_, err := openObjectStore(&Config{objectStore: "s3"})
	if err == nil || !strings.Contains(err.Error(), "S3_BUCKET") {
		t.Errorf("err = %v, want PODIUM_S3_BUCKET error", err)
	}
}

func TestOpenObjectStore_S3UsesDefaultEndpoint(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		objectStore: "s3",
		s3Bucket:    "my-bucket",
		s3Region:    "us-east-1",
	}
	got, err := openObjectStore(cfg)
	if err != nil {
		t.Fatalf("openObjectStore: %v", err)
	}
	if got == nil {
		t.Fatal("nil store")
	}
	if cfg.s3Endpoint == "" {
		t.Errorf("default endpoint not applied; cfg.s3Endpoint = %q", cfg.s3Endpoint)
	}
}

func TestIsTrue(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"true", "TRUE", "1", "yes", "on", " true ", "YES"} {
		if !isTrue(in) {
			t.Errorf("isTrue(%q) = false", in)
		}
	}
	for _, in := range []string{"false", "0", "no", "off", "", "maybe"} {
		if isTrue(in) {
			t.Errorf("isTrue(%q) = true", in)
		}
	}
}

func TestEnvDefault_BothCases(t *testing.T) {
	const key = "PODIUM_TEST_SBOOT_ENVDEFAULT_XYZZY"
	t.Setenv(key, "")
	if got := envDefault(key, "fb"); got != "fb" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv(key, "from-env")
	if got := envDefault(key, "fb"); got != "from-env" {
		t.Errorf("set: %q", got)
	}
}
