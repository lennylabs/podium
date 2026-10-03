package sigstoreharness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// post sends body to url and returns the status and the response body.
func post(t *testing.T, url, contentType string, body []byte) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, out
}

// Spec: §4.7.9, §6.2. The fake Fulcio issues a default leaf for the
// posted key under the harness intermediate and answers with the leaf,
// the intermediate, and the root.
func TestFakeServer_Fulcio(t *testing.T) {
	t.Parallel()
	h := New(t)
	srv := h.FakeServer(t)
	key, err := newECKey()
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkixBytes(t, key.Public())})
	req, _ := json.Marshal(map[string]any{"publicKeyRequest": map[string]any{"publicKey": map[string]any{"algorithm": "ECDSA", "content": string(pubPEM)}}})
	status, body := post(t, srv.URL+"/api/v2/signingCert", "application/json", req)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	var resp struct {
		SignedCertificateEmbeddedSct struct {
			Chain struct {
				Certificates []string `json:"certificates"`
			} `json:"chain"`
		} `json:"signedCertificateEmbeddedSct"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	chain := resp.SignedCertificateEmbeddedSct.Chain.Certificates
	if len(chain) != 3 {
		t.Fatalf("chain = %d certificates", len(chain))
	}
	leaf := decodePEMChain(t, chain[0])[0]
	if !leaf.PublicKey.(*ecdsa.PublicKey).Equal(key.Public()) || leaf.EmailAddresses[0] != DefaultSAN {
		t.Fatal("leaf does not carry the posted key and the default SAN")
	}
	if err := leaf.CheckSignatureFrom(h.FulcioIntermediate()); err != nil {
		t.Fatalf("leaf issuer: %v", err)
	}
	for name, bad := range map[string][]byte{
		"not JSON": []byte("acme"),
		"not PEM":  []byte(`{"publicKeyRequest":{"publicKey":{"content":"acme"}}}`),
		"bad key":  []byte(`{"publicKeyRequest":{"publicKey":{"content":"-----BEGIN PUBLIC KEY-----\nYWNtZQ==\n-----END PUBLIC KEY-----\n"}}}`),
	} {
		if status, _ := post(t, srv.URL+"/api/v2/signingCert", "application/json", bad); status != http.StatusBadRequest {
			t.Fatalf("%s: status %d", name, status)
		}
	}
}

// rekorRequestBody encodes a hashedrekord v0.0.2 create-entry request.
func rekorRequestBody(t *testing.T, digest, sig, cert []byte) []byte {
	t.Helper()
	req, err := json.Marshal(map[string]any{"hashedRekordRequestV002": map[string]any{
		"digest": digest,
		"signature": map[string]any{
			"content":  sig,
			"verifier": map[string]any{"x509Certificate": map[string]any{"rawBytes": cert}, "keyDetails": keyDetailsP256},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// Spec: §4.7.9. The fake Rekor v2 answers a create-entry request with a
// protojson TransparencyLogEntry whose logIndex is a decimal string, whose
// canonicalized body binds the request, and whose inclusion proof and
// checkpoint verify.
func TestFakeServer_Rekor(t *testing.T) {
	t.Parallel()
	h := New(t)
	_, digest := testContentHash("rekor")
	sig, cert := []byte("acme sig"), h.FulcioIntermediate().Raw
	req := rekorRequestBody(t, digest, sig, cert)

	cases := []rekorCase{
		{name: "default", index: 5, size: 7, indexKey: true, body: true, proof: true, cpOK: true},
		{name: "log index omitted", opts: []ServerOpt{WithRekorLogIndexOmitted()}, index: 0, size: 3, body: true, proof: true, cpOK: true},
		{name: "body omitted", opts: []ServerOpt{WithRekorBodyOmitted()}, index: 5, size: 7, indexKey: true, proof: true, cpOK: true},
		{name: "proof omitted", opts: []ServerOpt{WithRekorProofOmitted()}, index: 5, size: 7, indexKey: true, body: true},
		{name: "checkpoint omitted", opts: []ServerOpt{WithRekorCheckpointOmitted()}, index: 5, size: 7, indexKey: true, body: true, proof: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := h.FakeServer(t, tc.opts...)
			status, raw := post(t, srv.URL+"/api/v2/log/entries", "application/json", req)
			if status != http.StatusOK {
				t.Fatalf("status %d: %s", status, raw)
			}
			tc.assert(t, h, raw, digest, sig, cert)
		})
	}
	srv := h.FakeServer(t, WithRekorFailure())
	if status, _ := post(t, srv.URL+"/api/v2/log/entries", "application/json", req); status != http.StatusServiceUnavailable {
		t.Fatalf("failure: status %d", status)
	}
	if status, _ := post(t, h.FakeServer(t).URL+"/api/v2/log/entries", "application/json", []byte("acme")); status != http.StatusBadRequest {
		t.Fatalf("bad request: status %d", status)
	}
}

// rekorCase is one fake Rekor configuration and the response members it
// expects to be present.
type rekorCase struct {
	name              string
	opts              []ServerOpt
	index, size       int64
	indexKey          bool
	body, proof, cpOK bool
}

// assert checks one Rekor response against the case.
func (c rekorCase) assert(t *testing.T, h *Harness, raw []byte, digest, sig, cert []byte) {
	t.Helper()
	indexKey, hasBody, hasProof, hasCP, index, size := c.indexKey, c.body, c.proof, c.cpOK, c.index, c.size
	var generic map[string]any
	var resp transparencyLogEntry
	if json.Unmarshal(raw, &generic) != nil || json.Unmarshal(raw, &resp) != nil {
		t.Fatalf("response: %s", raw)
	}
	if _, ok := generic["logIndex"]; ok != indexKey {
		t.Fatalf("logIndex present = %v, want %v", ok, indexKey)
	}
	if indexKey && generic["logIndex"] != "5" {
		t.Fatalf("logIndex = %#v, want the string \"5\"", generic["logIndex"])
	}
	if (resp.CanonicalizedBody != nil) != hasBody || (resp.InclusionProof != nil) != hasProof {
		t.Fatalf("body %v proof %v", resp.CanonicalizedBody != nil, resp.InclusionProof != nil)
	}
	if !hasBody || !hasProof {
		return
	}
	in := inspection{bodyRaw: resp.CanonicalizedBody, hashes: resp.InclusionProof.Hashes}
	if err := json.Unmarshal(resp.CanonicalizedBody, &in.body); err != nil {
		t.Fatal(err)
	}
	equalBytes(t, "digest", in.body.Spec.V002.Data.Digest, digest)
	equalBytes(t, "signature", in.body.Spec.V002.Signature.Content, sig)
	equalBytes(t, "cert", in.body.Spec.V002.Signature.Verifier.X509Certificate.RawBytes, cert)
	root, ok := in.proofRoot(index, size)
	if !ok || !bytes.Equal(root, resp.InclusionProof.RootHash) {
		t.Fatal("inclusion proof does not recompute the root hash")
	}
	if (resp.InclusionProof.Checkpoint != nil) != hasCP {
		t.Fatalf("checkpoint present = %v", resp.InclusionProof.Checkpoint != nil)
	}
	if hasCP {
		n, ok := parseNote(resp.InclusionProof.Checkpoint.Envelope)
		if !ok || !n.verifiesUnder(h.LogPublicKey()) || !bytes.Equal(n.root, root) || n.size != size {
			t.Fatal("checkpoint does not verify or does not match the proof")
		}
	}
}

// timeStampRespMsg is the decoded TimeStampResp.
type timeStampRespMsg struct {
	Status struct {
		Status int
	}
	Token asn1.RawValue `asn1:"optional"`
}

// Spec: §4.7.9. The fake timestamp authority stamps the posted imprint at
// the harness clock, answers with the configured status, and refuses a
// request that is not a SHA-256 TimeStampReq.
func TestFakeServer_TimestampAuthority(t *testing.T) {
	t.Parallel()
	h := New(t)
	imprint := sha256.Sum256([]byte("acme sig"))
	req := timeStampRequest(t, oidSHA256, imprint[:])
	cases := []struct {
		name     string
		opts     []ServerOpt
		status   int
		hasToken bool
	}{
		{name: "default", hasToken: true},
		{name: "rejection", opts: []ServerOpt{WithTimestampStatus(2)}, status: 2},
		{name: "token omitted", opts: []ServerOpt{WithTimestampTokenOmitted()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := post(t, h.FakeServer(t, tc.opts...).URL+"/api/v1/timestamp", "application/timestamp-query", req)
			if code != http.StatusOK {
				t.Fatalf("status %d: %s", code, raw)
			}
			var resp timeStampRespMsg
			if _, err := asn1.Unmarshal(raw, &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Status.Status != tc.status || (len(resp.Token.FullBytes) > 0) != tc.hasToken {
				t.Fatalf("status %d token %v", resp.Status.Status, len(resp.Token.FullBytes) > 0)
			}
			if !tc.hasToken {
				return
			}
			tok := parseToken(t, resp.Token.FullBytes)
			equalBytes(t, "imprint", tok.tst.MessageImprint.HashedMessage, imprint[:])
			if !tok.tst.GenTime.Equal(h.Clock()) || tok.attrsVerifyUnder(h.TSALeaf()) != nil {
				t.Fatal("token not stamped at the clock by the harness signer")
			}
		})
	}
	srv := h.FakeServer(t, WithTSAFailure())
	if code, _ := post(t, srv.URL+"/api/v1/timestamp", "application/timestamp-query", req); code != http.StatusServiceUnavailable {
		t.Fatalf("failure: status %d", code)
	}
	if code, _ := post(t, h.FakeServer(t).URL+"/api/v1/timestamp", "application/timestamp-query", []byte("acme")); code != http.StatusBadRequest {
		t.Fatalf("bad request: status %d", code)
	}
}

// Spec: §4.7.9, §6.2. The request counter counts every request, including
// a failed service and an unknown path, and Fulcio fails under
// WithFulcioFailure.
func TestFakeServer_CounterAndFulcioFailure(t *testing.T) {
	t.Parallel()
	h := New(t)
	var n atomic.Int64
	srv := h.FakeServer(t, WithRequestCounter(&n), WithFulcioFailure())
	if code, body := post(t, srv.URL+"/api/v2/signingCert", "application/json", []byte("{}")); code != http.StatusServiceUnavailable || !strings.Contains(string(body), "fulcio") {
		t.Fatalf("status %d: %s", code, body)
	}
	if code, _ := post(t, srv.URL+"/api/v1/log/entries", "application/json", []byte("{}")); code != http.StatusNotFound {
		t.Fatalf("Rekor v1 path: status %d, want 404", code)
	}
	if n.Load() != 2 {
		t.Fatalf("counter = %d, want 2", n.Load())
	}
}

// Spec: §4.7.9. A TimestampToken built by the fake authority and one
// built directly agree on the structure an independent verifier reads.
func TestFakeServer_TokenMatchesTimestampToken(t *testing.T) {
	t.Parallel()
	h := New(t)
	sig := []byte("acme sig")
	direct := parseToken(t, h.TimestampToken(sig))
	imprint := sha256.Sum256(sig)
	_, raw := post(t, h.FakeServer(t).URL+"/api/v1/timestamp", "application/timestamp-query", timeStampRequest(t, oidSHA256, imprint[:]))
	var resp timeStampRespMsg
	if _, err := asn1.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	served := parseToken(t, resp.Token.FullBytes)
	if !bytes.Equal(direct.tst.MessageImprint.HashedMessage, served.tst.MessageImprint.HashedMessage) ||
		!direct.tst.GenTime.Equal(served.tst.GenTime) {
		t.Fatal("served token differs from TimestampToken")
	}
	if !direct.embedded[0].Equal(served.embedded[0]) {
		t.Fatal("served token embeds a different signer")
	}
}
