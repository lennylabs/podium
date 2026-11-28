package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// vectorFile is the cross-language §4.7.10 vector file the Go suite under
// test/vectors generates.
const vectorFile = "../../test/vectors/delivery-record.json"

// responseVector is one case of the vector file's responses section. Fetched
// maps each presigned URL the body names to the base64 bytes the object store
// serves for it.
type responseVector struct {
	Name       string            `json:"name"`
	BodyBase64 string            `json:"body_base64"`
	Fetched    map[string]string `json:"fetched"`
	Outcome    string            `json:"outcome"`
}

// vectorObjects is an http.RoundTripper that answers each request whose URL
// a case's fetched map names with those bytes, and every other request with
// 404.
type vectorObjects map[string][]byte

func (v vectorObjects) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := v[r.URL.String()]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

// Spec: §4.7.10 — deliverFreshLoad reaches the declared outcome of every
// responses case of the cross-language vector file. Each case is a pinned
// load, so the §6.5 check skips it and the delivery check alone decides the
// outcome: an ok case materializes, and every other case is refused with its
// declared code. The policy is always over the vector file's public key, so
// the signature step runs on every case that reaches it.
func TestDeliveryVectors_MCP(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var file struct {
		PublicKey string           `json:"public_key"`
		Responses []responseVector `json:"responses"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if len(file.Responses) == 0 {
		t.Fatal("the vector file carries no responses cases")
	}
	verifier, err := sign.ResolveVerifier(sign.PolicyAlways, file.PublicKey, "")
	if err != nil {
		t.Fatalf("ResolveVerifier: %v", err)
	}
	for _, tc := range file.Responses {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			body := decodeVectorBytes(t, tc.BodyBase64)
			objects := vectorObjects{}
			for url, b64 := range tc.Fetched {
				objects[url] = decodeVectorBytes(t, b64)
			}
			dest := t.TempDir()
			s := newTestServer(t, &config{harness: "none", materializeRoot: dest, verifyPolicy: sign.PolicyAlways, verifier: verifier})
			s.http = &http.Client{Transport: objects}
			// The request names the vector's own ID and version, so the
			// §4.7.11 request binding passes and the case tests the
			// §4.7.10 procedure alone.
			id, ver := vectorRequest(body)
			out := s.deliverFreshLoad(body, "", map[string]any{"id": id, "version": ver, "destination": dest}, time.Now())
			got := errorMessageText(out)
			if tc.Outcome == "ok" {
				if got != "" {
					t.Fatalf("error = %q, want the case to verify", got)
				}
				if entries, _ := os.ReadDir(dest); len(entries) == 0 {
					t.Errorf("a verified case materialized nothing")
				}
				return
			}
			if !strings.HasPrefix(got, tc.Outcome) {
				t.Errorf("error = %q, want a leading %s", got, tc.Outcome)
			}
			if entries, _ := os.ReadDir(dest); len(entries) != 0 {
				t.Errorf("a refused case wrote to the destination: %v", entries)
			}
		})
	}
}

// decodeVectorBytes decodes one standard-base64 value of the vector file.
func decodeVectorBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode vector bytes: %v", err)
	}
	return b
}

// vectorRequest returns the id and version members of a vector body, the
// request the body answers. A body that does not decode yields an ID that
// the request binding refuses, which no ok case carries.
func vectorRequest(body []byte) (string, string) {
	m, err := version.DecodeJSONObject(body)
	if err != nil {
		return "acme/vector", ""
	}
	var id, ver string
	_ = json.Unmarshal(m["id"], &id)
	_ = json.Unmarshal(m["version"], &ver)
	return id, ver
}
