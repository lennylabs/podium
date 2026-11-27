package sign

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// Spec: §4.7.11 — the refresh fetcher answers as go-tuf's default fetcher
// does: a body longer than the role's limit is a length mismatch, another
// status is an HTTP error that carries it, and a URL that does not parse
// fails before any request.
func TestContextFetcher_Limits(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	t.Cleanup(srv.Close)
	f := &contextFetcher{ctx: context.Background(), client: srv.Client()}

	if got, err := f.DownloadFile(srv.URL+"/ok", 10, 0); err != nil || string(got) != "0123456789" {
		t.Fatalf("DownloadFile within the limit = %q, %v", got, err)
	}
	var mismatch *metadata.ErrDownloadLengthMismatch
	if _, err := f.DownloadFile(srv.URL+"/ok", 9, 0); !errors.As(err, &mismatch) {
		t.Fatalf("DownloadFile over the limit = %v, want a length mismatch", err)
	}
	var status *metadata.ErrDownloadHTTP
	if _, err := f.DownloadFile(srv.URL+"/missing", 10, 0); !errors.As(err, &status) || status.StatusCode != http.StatusNotFound {
		t.Fatalf("DownloadFile of a missing file = %v, want an HTTP 404 error", err)
	}
	if _, err := f.DownloadFile("http://[::1:bad", 10, 0); err == nil {
		t.Fatal("DownloadFile of a malformed URL succeeded")
	}
}

// Spec: §13.12 — a copy that cannot be written keeps the refreshed root in
// memory: writeFileAtomic into a missing directory fails and leaves no
// temporary file behind.
func TestWriteFileAtomic_Failure(t *testing.T) {
	t.Parallel()
	if err := writeFileAtomic(t.TempDir()+"/absent", rootCopyFile, []byte("{}")); err == nil {
		t.Fatal("writeFileAtomic into a missing directory succeeded")
	}
}
