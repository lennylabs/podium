package sigstoreharness

import "encoding/json"

// hashedRekordEntry is the canonicalized hashedrekord v0.0.2 entry body
// Rekor v2 stores, with member names from the protobuf-specs rekor/v2
// hashedrekord message. The fields are declared in lexical order, so
// encoding/json writes the sorted-key form Rekor canonicalizes to.
type hashedRekordEntry struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Spec       hashedRekordSpec `json:"spec"`
}

type hashedRekordSpec struct {
	V002 hashedRekordV002 `json:"hashedRekordV002"`
}

type hashedRekordV002 struct {
	Data      hashedRekordData      `json:"data"`
	Signature hashedRekordSignature `json:"signature"`
}

type hashedRekordData struct {
	Algorithm string `json:"algorithm"`
	Digest    []byte `json:"digest"`
}

type hashedRekordSignature struct {
	Content  []byte               `json:"content"`
	Verifier hashedRekordVerifier `json:"verifier"`
}

type hashedRekordVerifier struct {
	KeyDetails      string     `json:"keyDetails"`
	X509Certificate rawCertDoc `json:"x509Certificate"`
}

// entryFields are the values one hashedrekord body binds.
type entryFields struct {
	kind       string
	apiVersion string
	digest     []byte
	sig        []byte
	certDER    []byte
	keyDetails string
}

// defaultEntryKind and defaultEntryAPIVersion name the entry type the
// §4.7.9 verifier accepts.
const (
	defaultEntryKind       = "hashedrekord"
	defaultEntryAPIVersion = "0.0.2"
)

// hashedRekordBody encodes the canonicalized entry body.
//
// Spec: §4.7.9.
func hashedRekordBody(f entryFields) ([]byte, error) {
	return json.Marshal(hashedRekordEntry{
		APIVersion: f.apiVersion,
		Kind:       f.kind,
		Spec: hashedRekordSpec{V002: hashedRekordV002{
			Data: hashedRekordData{Algorithm: "SHA2_256", Digest: f.digest},
			Signature: hashedRekordSignature{
				Content: f.sig,
				Verifier: hashedRekordVerifier{
					KeyDetails:      f.keyDetails,
					X509Certificate: rawCertDoc{RawBytes: f.certDER},
				},
			},
		}},
	})
}
