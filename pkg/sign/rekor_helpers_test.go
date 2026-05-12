package sign

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitContentHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantAlg  string
		wantHex  string
		wantErr  bool
	}{
		{"sha256:" + hex.EncodeToString([]byte("abc")), "sha256", "616263", false},
		{"sha512:" + hex.EncodeToString([]byte("def")), "sha512", "646566", false},
		{"no-colon", "", "", true},
		{":alone", "", "", true},
		{"sha256:", "", "", true},
		{"sha256:not-hex", "", "", true},
	}
	for _, c := range cases {
		alg, hexOut, err := splitContentHash(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("splitContentHash(%q): err = %v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if alg != c.wantAlg || hexOut != c.wantHex {
			t.Errorf("splitContentHash(%q) = (%q, %q), want (%q, %q)",
				c.in, alg, hexOut, c.wantAlg, c.wantHex)
		}
	}
}

func TestFetchRekor_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	s := SigstoreKeyless{RekorURL: srv.URL, Client: srv.Client()}
	err := s.fetchRekor(context.Background(), 42)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchRekor_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"x"}`))
	}))
	defer srv.Close()
	s := SigstoreKeyless{RekorURL: srv.URL, Client: srv.Client()}
	err := s.fetchRekor(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchRekor_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := SigstoreKeyless{RekorURL: srv.URL, Client: srv.Client()}
	if err := s.fetchRekor(context.Background(), 1); err != nil {
		t.Errorf("err = %v", err)
	}
}

