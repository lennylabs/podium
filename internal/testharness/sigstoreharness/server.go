package sigstoreharness

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// ServerOpt adjusts the fake services FakeServer runs.
type ServerOpt func(*serverConfig)

// serverConfig collects the ServerOpt adjustments of one fake server.
type serverConfig struct {
	fulcioFail, rekorFail, tsaFail bool
	noBody, noProof, noCheckpoint  bool
	logIndexOmitted                bool
	tsaStatus                      int
	noToken                        bool
	counter                        *atomic.Int64
}

// WithFulcioFailure makes Fulcio answer 503.
func WithFulcioFailure() ServerOpt { return func(c *serverConfig) { c.fulcioFail = true } }

// WithRekorFailure makes Rekor answer 503.
func WithRekorFailure() ServerOpt { return func(c *serverConfig) { c.rekorFail = true } }

// WithTSAFailure makes the timestamp authority answer 503.
func WithTSAFailure() ServerOpt { return func(c *serverConfig) { c.tsaFail = true } }

// WithRekorBodyOmitted leaves canonicalizedBody out of the Rekor response.
func WithRekorBodyOmitted() ServerOpt { return func(c *serverConfig) { c.noBody = true } }

// WithRekorProofOmitted leaves inclusionProof out of the Rekor response.
func WithRekorProofOmitted() ServerOpt { return func(c *serverConfig) { c.noProof = true } }

// WithRekorCheckpointOmitted leaves the checkpoint out of the inclusion
// proof in the Rekor response.
func WithRekorCheckpointOmitted() ServerOpt { return func(c *serverConfig) { c.noCheckpoint = true } }

// WithRekorLogIndexOmitted places the posted entry at index 0 of a 3-leaf
// tree and leaves logIndex out of the response, as protojson does for a
// zero value.
func WithRekorLogIndexOmitted() ServerOpt { return func(c *serverConfig) { c.logIndexOmitted = true } }

// WithTimestampStatus makes the timestamp authority answer with PKIStatus
// status and no token. Status 2 is rejection.
func WithTimestampStatus(status int) ServerOpt {
	return func(c *serverConfig) { c.tsaStatus = status; c.noToken = true }
}

// WithTimestampTokenOmitted makes the timestamp authority answer status 0
// with no token.
func WithTimestampTokenOmitted() ServerOpt { return func(c *serverConfig) { c.noToken = true } }

// WithRequestCounter counts every request the server receives into c.
func WithRequestCounter(c *atomic.Int64) ServerOpt {
	return func(cfg *serverConfig) { cfg.counter = c }
}

// FakeServer serves Fulcio at /api/v2/signingCert, Rekor v2 at POST
// /api/v2/log/entries, and a timestamp authority at POST
// /api/v1/timestamp, all backed by the harness trust material. The
// server closes when the test ends.
//
// Spec: §4.7.9, §6.2.
func (h *Harness) FakeServer(t testing.TB, opts ...ServerOpt) *httptest.Server {
	t.Helper()
	var cfg serverConfig
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/signingCert", h.fulcioHandler(cfg))
	mux.HandleFunc("POST /api/v2/log/entries", h.rekorHandler(cfg))
	mux.HandleFunc("POST /api/v1/timestamp", h.tsaHandler(cfg))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.counter != nil {
			cfg.counter.Add(1)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fulcioHandler issues a default leaf for the posted public key and
// answers with the leaf, the intermediate, and the root, as Fulcio does.
func (h *Harness) fulcioHandler(cfg serverConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.fulcioFail {
			http.Error(w, "fulcio unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			PublicKeyRequest struct {
				PublicKey struct {
					Content string `json:"content"`
				} `json:"publicKey"`
			} `json:"publicKeyRequest"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		leaf, err := h.fulcioLeaf(req.PublicKeyRequest.PublicKey.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		chain := []string{pemChain([]*x509.Certificate{leaf}), pemChain([]*x509.Certificate{h.fulcioInter.cert}), pemChain([]*x509.Certificate{h.fulcioRoot.cert})}
		writeJSON(w, map[string]any{
			"signedCertificateEmbeddedSct": map[string]any{"chain": map[string]any{"certificates": chain}},
		})
	}
}

// fulcioLeaf issues a default leaf for a PEM public key.
func (h *Harness) fulcioLeaf(pubPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		return nil, fmt.Errorf("public key is not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	spec, err := defaultLeafConfig().spec(h.clock)
	if err != nil {
		return nil, err
	}
	return h.fulcioInter.issue(spec, pub)
}

// rekorRequest is the Rekor v2 hashedrekord v0.0.2 create-entry request.
type rekorRequest struct {
	HashedRekordRequestV002 struct {
		Digest    []byte `json:"digest"`
		Signature struct {
			Content  []byte `json:"content"`
			Verifier struct {
				X509Certificate rawCertDoc `json:"x509Certificate"`
				KeyDetails      string     `json:"keyDetails"`
			} `json:"verifier"`
		} `json:"signature"`
	} `json:"hashedRekordRequestV002"`
}

// rekorHandler logs the posted entry and answers with a protojson
// TransparencyLogEntry.
func (h *Harness) rekorHandler(cfg serverConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.rekorFail {
			http.Error(w, "rekor unavailable", http.StatusServiceUnavailable)
			return
		}
		var req rekorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := h.rekorEntry(cfg, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, resp)
	}
}

// transparencyLogEntry is the protojson TransparencyLogEntry. protojson
// writes int64 fields as decimal strings and omits zero values.
type transparencyLogEntry struct {
	LogIndex          string                 `json:"logIndex,omitempty"`
	LogID             logIDDoc               `json:"logId"`
	KindVersion       map[string]string      `json:"kindVersion"`
	InclusionProof    *inclusionProofMessage `json:"inclusionProof,omitempty"`
	CanonicalizedBody []byte                 `json:"canonicalizedBody,omitempty"`
}

type inclusionProofMessage struct {
	LogIndex   string             `json:"logIndex,omitempty"`
	RootHash   []byte             `json:"rootHash"`
	TreeSize   string             `json:"treeSize"`
	Hashes     [][]byte           `json:"hashes"`
	Checkpoint *checkpointMessage `json:"checkpoint,omitempty"`
}

type checkpointMessage struct {
	Envelope string `json:"envelope"`
}

// rekorEntry builds the response for one posted entry: index 5 of 7 by
// default, index 0 of 3 under WithRekorLogIndexOmitted.
func (h *Harness) rekorEntry(cfg serverConfig, req rekorRequest) (transparencyLogEntry, error) {
	in := req.HashedRekordRequestV002
	body, err := hashedRekordBody(entryFields{
		kind: defaultEntryKind, apiVersion: defaultEntryAPIVersion,
		digest: in.Digest, sig: in.Signature.Content,
		certDER: in.Signature.Verifier.X509Certificate.RawBytes, keyDetails: in.Signature.Verifier.KeyDetails,
	})
	if err != nil {
		return transparencyLogEntry{}, err
	}
	size, index := 7, 5
	if cfg.logIndexOmitted {
		size, index = 3, 0
	}
	inc, err := signedInclusion(body, size, index, h.logKey)
	if err != nil {
		return transparencyLogEntry{}, err
	}
	idx := strconv.Itoa(index)
	if cfg.logIndexOmitted {
		idx = ""
	}
	out := transparencyLogEntry{
		LogIndex:          idx,
		LogID:             logIDDoc{KeyID: []byte(checkpointOrigin)},
		KindVersion:       map[string]string{"kind": defaultEntryKind, "version": defaultEntryAPIVersion},
		CanonicalizedBody: body,
		InclusionProof: &inclusionProofMessage{
			LogIndex: idx, RootHash: inc.root, TreeSize: strconv.Itoa(size),
			Hashes: inc.hashes, Checkpoint: &checkpointMessage{Envelope: inc.checkpoint},
		},
	}
	if cfg.noBody {
		out.CanonicalizedBody = nil
	}
	if cfg.noCheckpoint {
		out.InclusionProof.Checkpoint = nil
	}
	if cfg.noProof {
		out.InclusionProof = nil
	}
	return out, nil
}

// tsaHandler stamps the posted request's imprint at the harness clock.
// The 500 branches cover encoding failures that do not occur for a token
// built from the harness signer and a parsed SHA-256 imprint.
func (h *Harness) tsaHandler(cfg serverConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.tsaFail {
			http.Error(w, "timestamp authority unavailable", http.StatusServiceUnavailable)
			return
		}
		der, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		imprint, err := parseTimeStampReq(der)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var token []byte
		if !cfg.noToken {
			if token, err = buildToken(h.defaultTSConfig(imprint)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		resp, err := timeStampResp(cfg.tsaStatus, token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}
}

// writeJSON writes v as a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
