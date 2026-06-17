package server_test

import (
	"net/http"
	"os"
	"testing"
)

// TestMain disables HTTP keep-alives on the shared default transport for this
// package's tests.
//
// The tests run in parallel, each starting its own httptest.Server and issuing
// requests through http.DefaultClient. httptest.Server.Close() calls
// http.DefaultTransport.CloseIdleConnections(), which closes the pooled idle
// keep-alive connections that http.DefaultClient shares across every test. A
// test whose request reuses such a connection at the moment another test's
// cleanup closes it fails with "transport connection broken: http:
// CloseIdleConnections called".
//
// Disabling keep-alives keeps the idle pool empty, so no other test's Close()
// can break an in-flight request and no request reuses a connection a peer
// server has shut down. No test in this package depends on connection reuse.
func TestMain(m *testing.M) {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.DisableKeepAlives = true
	}
	os.Exit(m.Run())
}
