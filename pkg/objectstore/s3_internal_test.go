package objectstore

import "testing"

// Spec: §13.12 — when both static keys are set the client signs
// with them and ignores the ambient AWS_* environment; when both are unset it
// falls back to the AWS credential chain, whose first provider (EnvAWS) reads
// AWS_ACCESS_KEY_ID. Setting the AWS_* env lets the test prove the chain is
// consulted without reaching the network (EnvAWS short-circuits before IAM).
func TestS3Credentials_StaticVsChain(t *testing.T) {
	t.Run("static keys win over the environment", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
		v, err := s3Credentials(S3Config{AccessKeyID: "cfg-key", SecretAccessKey: "cfg-secret"}).Get()
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if v.AccessKeyID != "cfg-key" {
			t.Errorf("AccessKeyID = %q, want cfg-key (static must ignore AWS_* env)", v.AccessKeyID)
		}
	})

	t.Run("chain consulted when static keys unset", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
		v, err := s3Credentials(S3Config{}).Get()
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if v.AccessKeyID != "env-key" {
			t.Errorf("AccessKeyID = %q, want env-key (chain → EnvAWS / IAM fallback)", v.AccessKeyID)
		}
	})

	t.Run("partial static key does not fall through to the chain", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
		// Only the access key is set: this is a misconfiguration, but it must
		// take the static path rather than silently picking up the ambient
		// AWS_* environment (which would mask the operator's mistake).
		v, err := s3Credentials(S3Config{AccessKeyID: "cfg-key"}).Get()
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if v.AccessKeyID == "env-key" {
			t.Errorf("partial static key fell through to the chain (got env-key); want the static path")
		}
	})
}
