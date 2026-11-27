package sign

import (
	"context"
	"encoding/asn1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// Spec: §4.7.9 — the timestamp condition reads a token's embedded
// certificates and its signer from a token with exactly one signer, and
// refuses a token that does not parse as a CMS SignedData over a TSTInfo.
// sigstore-go verifies the token's signature, imprint, and chain.
func TestParseToken(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	sig := []byte("acme signature")
	cases := []struct {
		name  string
		token []byte
		want  string // empty means parsed
	}{
		{"default", h.TimestampToken(sig), ""},
		{"no embedded certificate", h.TimestampToken(sig, sigstoreharness.WithoutEmbeddedCerts()), ""},
		{"two signers", h.TimestampToken(sig, sigstoreharness.WithTwoSigners()), "timestamp carries 2 signers; want 1"},
		{"content type", h.TimestampToken(sig, sigstoreharness.WithTSTContentType(asn1.ObjectIdentifier{1, 2, 3})), "is not TSTInfo"},
		{"not DER", []byte("acme"), "timestamp does not parse"},
		{"trailing bytes", append(h.TimestampToken(sig), 0x00), "timestamp does not parse"},
		{"not signed data", contentInfoDER(t, oidTSTInfo), "is not signed data"},
		{"signed data does not parse", contentInfoDER(t, oidSignedData), "signed data"},
	}
	for _, c := range cases {
		tok, err := parseToken(c.token)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: (%v, %v), want %q", c.name, tok, err, c.want)
		}
	}
}

// contentInfoDER encodes a ContentInfo of contentType whose content is a
// NULL.
func contentInfoDER(t *testing.T, contentType asn1.ObjectIdentifier) []byte {
	t.Helper()
	b, err := asn1.Marshal(struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue
	}{contentType, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: []byte{0x05, 0x00}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// Spec: §4.7.9 — requestTimestamp refuses a response that is not a
// TimeStampResp.
func TestRequestTimestamp_UndecodableResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("acme, not DER"))
	}))
	t.Cleanup(srv.Close)
	s := SigstoreKeyless{TSAURL: srv.URL, Client: srv.Client()}
	if _, err := s.requestTimestamp(context.Background(), []byte("sig")); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode response", err)
	}
}

// Spec: §4.7.9 — requestTimestamp returns the TimeStampResp a bundle
// carries, without bytes that follow it in the response body.
func TestRequestTimestamp_ReturnsTheResponse(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	srv := h.FakeServer(t)
	s := SigstoreKeyless{TSAURL: srv.URL + "/api/v1/timestamp", Client: srv.Client()}
	resp, err := s.requestTimestamp(context.Background(), []byte("sig"))
	if err != nil {
		t.Fatalf("requestTimestamp: %v", err)
	}
	var tsr timeStampResp
	if rest, err := asn1.Unmarshal(resp, &tsr); err != nil || len(rest) != 0 || tsr.Status.Status != 0 {
		t.Fatalf("response = %x (%v)", resp, err)
	}
	if _, err := parseToken(tsr.TimeStampToken.FullBytes); err != nil {
		t.Fatalf("token: %v", err)
	}
}

// Spec: §4.7.9 — an unreachable timestamp authority fails the request.
func TestRequestTimestamp_Unreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	s := SigstoreKeyless{TSAURL: srv.URL, Client: srv.Client()}
	if _, err := s.requestTimestamp(context.Background(), []byte("sig")); err == nil {
		t.Fatal("request to a closed server succeeded")
	}
}
