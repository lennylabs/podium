package serverboot

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// The credentials the redaction test configures and then has a backend quote.
const (
	redactTestDSN      = "postgres://podium:dsn-pass-7731@db.internal:5432/podium?sslmode=disable"
	redactTestKVDSN    = "host=db.internal user=podium password=kv-pass-4410 dbname=podium"
	redactTestAPIKey   = "sk-live-key-90210"
	redactTestURLToken = "url-token-5150"
	redactTestURLUser  = "url-user-8008"
	redactTestQuery    = "query-token-6060"
)

// leakyVector is a vector backend whose writes fail with an error that quotes
// a configured DSN in both forms, a URL with userinfo and a query token, and
// an API key value, as a custom backend that wraps its configuration or a
// backend that echoes its request can.
type leakyVector struct{ *vector.Memory }

func (leakyVector) Put(context.Context, string, string, string, []float32) error {
	return errors.New("vector: backend unreachable: open " + redactTestDSN + " (" + redactTestKVDSN + "): " +
		`Put "https://` + redactTestURLUser + ":" + redactTestURLToken + "@weaviate.example/v1/objects?api_key=" + redactTestQuery +
		`": dial tcp: connection refused; upstream said: invalid key ` + redactTestAPIKey)
}

// Spec: §4.7 — an outbox row records the error of its last failed attempt
// with no credential: the full DSN, the DSN password in URL and key/value
// form, URL userinfo, URL query strings, and configured API keys are removed
// before the row stores the text and before the log line carries it. The
// drain worker started by startVectorOutboxWorker applies it, and the rest of
// the error stays readable. The test captures the standard logger, so it does
// not run in parallel.
func TestVectorOutbox_ErrorTextCarriesNoCredential(t *testing.T) {
	logBuf := &syncBuffer{}
	prev := log.Writer()
	log.SetOutput(logBuf)
	t.Cleanup(func() { log.SetOutput(prev) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec := store.ManifestRecord{TenantID: "default", ArtifactID: "fin/a", Version: "1.0.0", ContentHash: "sha256:a", Type: "agent", Description: "d"}
	pending := store.VectorPending{TenantID: "default", ArtifactID: "fin/a", Version: "1.0.0", Text: "d", EnqueuedAt: time.Now().UTC(), NextRetryAt: time.Now().UTC()}
	if err := st.PutManifestWithVectorPending(ctx, rec, pending); err != nil {
		t.Fatalf("PutManifestWithVectorPending: %v", err)
	}
	cfg := &Config{
		postgresDSN: redactTestDSN, pgvectorDSN: redactTestKVDSN, openaiAPIKey: redactTestAPIKey,
		vectorOutboxInterval: 3600, vectorOutboxBatch: 10,
	}
	startVectorOutboxWorker(ctx, cfg, st, leakyVector{vector.NewMemory(4)}, fakeEmbedder{dim: 4}, nil, nil, "default", nil)

	const drainLine = "vector outbox: drain fin/a@1.0.0 failed"
	var lastError string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (lastError == "" || !strings.Contains(logBuf.String(), drainLine)) {
		rows, err := st.ListVectorPending(ctx, 10, time.Now().Add(48*time.Hour))
		if err != nil {
			t.Fatalf("ListVectorPending: %v", err)
		}
		if len(rows) == 1 && rows[0].Attempts > 0 {
			lastError = rows[0].LastError
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if lastError == "" {
		t.Fatal("the drain worker recorded no failed attempt")
	}
	logged := logBuf.String()
	for _, secret := range []string{redactTestDSN, "dsn-pass-7731", "kv-pass-4410", redactTestAPIKey, redactTestURLToken, redactTestURLUser, redactTestQuery, "sslmode"} {
		if strings.Contains(lastError, secret) {
			t.Errorf("last_error carries %q: %s", secret, lastError)
		}
		if strings.Contains(logged, secret) {
			t.Errorf("the log carries %q: %s", secret, logged)
		}
	}
	if !strings.Contains(lastError, "connection refused") || !strings.Contains(lastError, "weaviate.example/v1/objects") {
		t.Errorf("last_error lost the readable part of the error: %s", lastError)
	}
	if !strings.Contains(logged, drainLine) {
		t.Errorf("the drain failure was not logged: %s", logged)
	}
}

// Spec: §4.7 — the redaction patterns cover a credential the registry was
// not configured with: URL userinfo, including a password that contains '@',
// a URL query string, and a key/value password, quoted or not. A zero
// redactor applies the patterns alone.
func TestOutboxRedactor_Patterns(t *testing.T) {
	t.Parallel()
	var zero outboxRedactor
	in := errors.New(`Post "https://bob:p@ss@host.example/path?token=abc&x=1": EOF; dsn password='quoted secret' and PASSWORD=bare`)
	got := zero.text(in)
	for _, leaked := range []string{"bob", "p@ss", "token=abc", "quoted secret", "bare"} {
		if strings.Contains(got, leaked) {
			t.Errorf("redacted text carries %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, `Post "https://host.example/path": EOF`) {
		t.Errorf("redacted text = %s, want the URL kept without userinfo and query", got)
	}
	r := newOutboxRedactor("", "  ", "postgres://u:pw-1@h/db?password=pw-2", "abcd", "abcdef")
	if len(r.secrets) != 5 || r.secrets[len(r.secrets)-1] != "abcd" {
		t.Errorf("secrets = %q, want the DSN, both of its passwords, and both values, longest first", r.secrets)
	}
	if got := r.text(errors.New("value abcdef here")); got != "value [redacted] here" {
		t.Errorf("text = %q, want the longer value removed whole", got)
	}
}
