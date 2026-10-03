package sigstoreharness

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"time"
)

// Sigstore trusted_root.json member values the harness writes.
const (
	trustedRootMediaType = "application/vnd.dev.sigstore.trustedroot+json;version=0.1"
	keyDetailsRSA        = "PKIX_RSA_PKCS1V15_2048_SHA256"
	rfc3339Millis        = "2006-01-02T15:04:05.000Z"
)

// trustedRootDoc is Sigstore's trusted_root.json, with its protojson
// member names.
type trustedRootDoc struct {
	MediaType              string         `json:"mediaType"`
	TLogs                  []tlogDoc      `json:"tlogs,omitempty"`
	CertificateAuthorities []authorityDoc `json:"certificateAuthorities,omitempty"`
	CTLogs                 []tlogDoc      `json:"ctlogs"`
	TimestampAuthorities   []authorityDoc `json:"timestampAuthorities,omitempty"`
}

type tlogDoc struct {
	BaseURL       string       `json:"baseUrl"`
	HashAlgorithm string       `json:"hashAlgorithm"`
	PublicKey     publicKeyDoc `json:"publicKey"`
	LogID         logIDDoc     `json:"logId"`
}

type publicKeyDoc struct {
	RawBytes   []byte      `json:"rawBytes"`
	KeyDetails string      `json:"keyDetails"`
	ValidFor   validForDoc `json:"validFor"`
}

type logIDDoc struct {
	KeyID []byte `json:"keyId"`
}

type authorityDoc struct {
	Subject   subjectDoc   `json:"subject"`
	URI       string       `json:"uri"`
	CertChain certChainDoc `json:"certChain"`
	ValidFor  validForDoc  `json:"validFor"`
}

type subjectDoc struct {
	Organization string `json:"organization"`
	CommonName   string `json:"commonName"`
}

type certChainDoc struct {
	Certificates []rawCertDoc `json:"certificates"`
}

type rawCertDoc struct {
	RawBytes []byte `json:"rawBytes"`
}

type validForDoc struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// Window is a validFor window. A nil bound leaves that side open.
type Window struct {
	Start *time.Time
	End   *time.Time
}

// doc writes the window as RFC 3339 with milliseconds and Z.
func (w Window) doc() validForDoc {
	var out validForDoc
	if w.Start != nil {
		out.Start = w.Start.UTC().Format(rfc3339Millis)
	}
	if w.End != nil {
		out.End = w.End.UTC().Format(rfc3339Millis)
	}
	return out
}

// RootOpt adjusts one trusted root built by TrustedRootJSON.
type RootOpt func(*rootConfig)

// rootConfig collects the RootOpt adjustments of one trusted root.
type rootConfig struct {
	noCAs, noTSAs, noTLogs bool
	edKey, unsupported     bool
	extraTLog, ctlogs      bool
	garbageCert, malformed bool
	foreignCA              bool
	caWindow, tsaWindow    Window
	logWindows             []Window
}

// WithoutCAs omits every certificate authority.
func WithoutCAs() RootOpt { return func(c *rootConfig) { c.noCAs = true } }

// WithoutTSAs omits every timestamp authority.
func WithoutTSAs() RootOpt { return func(c *rootConfig) { c.noTSAs = true } }

// WithoutTLogs omits the default P-256 log key. Keys other options add
// are still written.
func WithoutTLogs() RootOpt { return func(c *rootConfig) { c.noTLogs = true } }

// WithEd25519LogKey adds the Ed25519 log key beside the P-256 key.
func WithEd25519LogKey() RootOpt { return func(c *rootConfig) { c.edKey = true } }

// WithUnsupportedLogKey adds an RSA log key with an RSA keyDetails value,
// which the §4.7.9 verifier skips.
func WithUnsupportedLogKey() RootOpt { return func(c *rootConfig) { c.unsupported = true } }

// WithExtraTLog adds an unrelated P-256 log key.
func WithExtraTLog() RootOpt { return func(c *rootConfig) { c.extraTLog = true } }

// WithCTLogs adds a ctlogs entry whose rawBytes are not DER.
func WithCTLogs() RootOpt { return func(c *rootConfig) { c.ctlogs = true } }

// WithCAValidFor sets the certificate authority's validFor window.
func WithCAValidFor(start, end *time.Time) RootOpt {
	return func(c *rootConfig) { c.caWindow = Window{Start: start, End: end} }
}

// WithTSAValidFor sets the timestamp authority's validFor window.
func WithTSAValidFor(start, end *time.Time) RootOpt {
	return func(c *rootConfig) { c.tsaWindow = Window{Start: start, End: end} }
}

// WithLogValidFor lists the P-256 log key once per window.
func WithLogValidFor(windows ...Window) RootOpt {
	return func(c *rootConfig) { c.logWindows = windows }
}

// WithGarbageCert replaces the certificate authority's first certificate
// with bytes that are not DER.
func WithGarbageCert() RootOpt { return func(c *rootConfig) { c.garbageCert = true } }

// WithMalformedJSON truncates the document so it does not parse.
func WithMalformedJSON() RootOpt { return func(c *rootConfig) { c.malformed = true } }

// WithForeignCA replaces the certificate authorities with an unrelated
// root and intermediate. The timestamp authority and log key stay, so a
// default envelope fails only at the leaf's chain check.
func WithForeignCA() RootOpt { return func(c *rootConfig) { c.foreignCA = true } }

// TrustedRootJSON returns a Sigstore trusted_root.json with one
// certificate authority (the Fulcio intermediate then root), one
// timestamp authority (the signer then root), and the P-256 log key.
// Every window starts at the clock minus 1 hour and is open-ended unless
// an option sets it.
//
// Spec: §4.7.9, §6.2.
func (h *Harness) TrustedRootJSON(opts ...RootOpt) []byte {
	h.tb.Helper()
	start := h.clock.Add(-time.Hour)
	open := Window{Start: &start}
	cfg := rootConfig{caWindow: open, tsaWindow: open, logWindows: []Window{open}}
	for _, o := range opts {
		o(&cfg)
	}
	doc, err := h.trustedRoot(cfg, open)
	must(h.tb, err)
	out, err := json.MarshalIndent(doc, "", "  ")
	must(h.tb, err)
	if cfg.malformed {
		return out[:len(out)/2]
	}
	return out
}

// trustedRoot assembles the document for cfg.
func (h *Harness) trustedRoot(cfg rootConfig, open Window) (trustedRootDoc, error) {
	doc := trustedRootDoc{MediaType: trustedRootMediaType, CTLogs: []tlogDoc{}}
	tlogs, err := h.rootTLogs(cfg, open)
	if err != nil {
		return doc, err
	}
	doc.TLogs = tlogs
	if !cfg.noCAs {
		ca, err := h.rootCA(cfg)
		if err != nil {
			return doc, err
		}
		doc.CertificateAuthorities = []authorityDoc{ca}
	}
	if !cfg.noTSAs {
		doc.TimestampAuthorities = []authorityDoc{authorityEntry(
			"acme-tsa", "https://tsa.acme.test", cfg.tsaWindow, h.tsaLeaf.cert, h.tsaRoot.cert)}
	}
	if cfg.ctlogs {
		doc.CTLogs = []tlogDoc{{
			BaseURL: "https://ctlog.acme.test", HashAlgorithm: "SHA2_256",
			PublicKey: publicKeyDoc{RawBytes: []byte("acme ct log key, not DER"), KeyDetails: keyDetailsP256, ValidFor: open.doc()},
			LogID:     logIDDoc{KeyID: []byte("acme-ct")},
		}}
	}
	return doc, nil
}

// rootCA returns the certificate-authority entry: the harness Fulcio
// chain, or an unrelated one under WithForeignCA.
func (h *Harness) rootCA(cfg rootConfig) (authorityDoc, error) {
	root, inter := h.fulcioRoot, h.fulcioInter
	if cfg.foreignCA {
		var err error
		if root, inter, err = newFulcio("foreign-fulcio", h.clock); err != nil {
			return authorityDoc{}, err
		}
	}
	ca := authorityEntry("acme-fulcio", "https://fulcio.acme.test", cfg.caWindow, inter.cert, root.cert)
	if cfg.garbageCert {
		ca.CertChain.Certificates[0].RawBytes = []byte("acme garbage, not a certificate")
	}
	return ca, nil
}

// rootTLogs returns the tlogs entries for cfg.
func (h *Harness) rootTLogs(cfg rootConfig, open Window) ([]tlogDoc, error) {
	var out []tlogDoc
	if !cfg.noTLogs {
		for _, w := range cfg.logWindows {
			entry, err := tlogEntry(h.logKey.Public(), keyDetailsP256, w)
			if err != nil {
				return nil, err
			}
			out = append(out, entry)
		}
	}
	extra, err := h.extraTLogKeys(cfg)
	if err != nil {
		return nil, err
	}
	for _, k := range extra {
		entry, err := tlogEntry(k.pub, k.details, open)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// logKeyListing is one additional log key and its keyDetails value.
type logKeyListing struct {
	pub     crypto.PublicKey
	details string
}

// extraTLogKeys returns the log keys the options add.
func (h *Harness) extraTLogKeys(cfg rootConfig) ([]logKeyListing, error) {
	var out []logKeyListing
	if cfg.edKey {
		out = append(out, logKeyListing{h.edLogKey.Public(), keyDetailsEd25519})
	}
	if cfg.unsupported {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		out = append(out, logKeyListing{key.Public(), keyDetailsRSA})
	}
	if cfg.extraTLog {
		key, err := newECKey()
		if err != nil {
			return nil, err
		}
		out = append(out, logKeyListing{key.Public(), keyDetailsP256})
	}
	return out, nil
}

// tlogEntry writes one tlogs entry for pub.
func tlogEntry(pub crypto.PublicKey, details string, w Window) (tlogDoc, error) {
	raw, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return tlogDoc{}, err
	}
	keyID := sha256.Sum256(raw)
	return tlogDoc{
		BaseURL:       "https://" + checkpointOrigin,
		HashAlgorithm: "SHA2_256",
		PublicKey:     publicKeyDoc{RawBytes: raw, KeyDetails: details, ValidFor: w.doc()},
		LogID:         logIDDoc{KeyID: keyID[:]},
	}, nil
}

// authorityEntry writes one authority whose chain lists certs in order.
func authorityEntry(name, uri string, w Window, certs ...*x509.Certificate) authorityDoc {
	chain := make([]rawCertDoc, len(certs))
	for i, c := range certs {
		chain[i] = rawCertDoc{RawBytes: c.Raw}
	}
	return authorityDoc{
		Subject:   subjectDoc{Organization: "acme", CommonName: name},
		URI:       uri,
		CertChain: certChainDoc{Certificates: chain},
		ValidFor:  w.doc(),
	}
}
