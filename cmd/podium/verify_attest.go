package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sync"
)

// verifyAttestation is the §4.7.11 part of `podium verify <artifact>`. It
// decodes the record the registry served for the artifact, applies the
// well-formed rule and the request binding, and runs the attestation check
// under the consumer trust policy that podium sync loads, reporting the
// result on w apart from the delivery signature result. It returns the exit
// status of this part: 0 when the binding passes and the check does not
// refuse the record, a failure under warn included, and 1 otherwise.
//
// Spec: §4.7.9, §4.7.11 (Request binding, Attestation check), §7.5 (Author
// attestation)
func verifyAttestation(w io.Writer, att *sync.Attestation, registry, artifact string, body []byte) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	token := readCLIToken(registry)
	served, err := sync.DecodeServed(ctx, doJSONClient, token, body)
	res := attest.Result{}
	if att.Policy != nil {
		res.Resolution = att.Policy.Resolve(artifact)
	}
	mode := res.Resolution.Mode
	if mode == "" {
		mode = attest.ModeNone
	}
	if err != nil {
		// The record does not decode, so neither the binding nor the check
		// can read it; the delivery part keeps its own result.
		return reportUndecodable(w, mode, err)
	}
	if err := sync.BindServed(artifact, served.Record); err != nil {
		_, _ = fmt.Fprintf(w, "request binding: refused: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(w, "request binding: ok")
	checker := (&sync.Attestation{Policy: att.Policy, Limits: att.Limits}).Checker(attest.HTTPBundleSource{
		BaseURL: registry, Client: doJSONClient, MaxBytes: att.Limits.MaxBytes,
		Prepare: func(r *http.Request) {
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			r.Header.Set(audit.ClientHeader, cliClientHeader())
		},
	})
	res, err = checker.Verify(ctx, attest.Delivery{Request: attest.Request{ID: artifact}, Served: served})
	if err != nil {
		_, _ = fmt.Fprintf(w, "attestation: mode %s\nattestation: refused: %v\n", mode, err)
		return 1
	}
	reportAttestationResult(w, mode, res)
	return 0
}

// reportUndecodable reports a record that the §4.7.10 decoding refuses: a
// refusal of the check under require, a warning under warn, and no check
// under none.
func reportUndecodable(w io.Writer, mode attest.Mode, err error) int {
	_, _ = fmt.Fprintf(w, "attestation: mode %s\n", mode)
	switch mode {
	case attest.ModeRequire:
		_, _ = fmt.Fprintf(w, "attestation: refused: %v\n", err)
		return 1
	case attest.ModeWarn:
		_, _ = fmt.Fprintf(w, "attestation: warning: %v\n", err)
	default:
		_, _ = fmt.Fprintln(w, "attestation: not checked: the ID resolves to none")
	}
	return 0
}

// reportAttestationResult reports the outcome of a check that did not
// refuse the record: the mode, the warnings under warn, the package digest
// that podium sync records, and the signer and attested time of each bundle
// that met a requirement.
func reportAttestationResult(w io.Writer, mode attest.Mode, res attest.Result) {
	_, _ = fmt.Fprintf(w, "attestation: mode %s\n", mode)
	if !res.Checked {
		_, _ = fmt.Fprintln(w, "attestation: not checked: the ID resolves to none")
		return
	}
	for _, f := range res.Warnings {
		_, _ = fmt.Fprintf(w, "attestation: warning: %s\n", f.WarningText(res.Resolution.ID))
	}
	if len(res.Warnings) == 0 {
		_, _ = fmt.Fprintln(w, "attestation: ok")
	}
	if res.PackageDigest != "" {
		_, _ = fmt.Fprintf(w, "package_digest: %s\n", res.PackageDigest)
	}
	for i, a := range res.Accepted {
		at := "none"
		if !a.AttestedTime.IsZero() {
			at = a.AttestedTime.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "requirement %d: bundle %s signer instance=%q issuer=%q identity=%q attested_time=%s\n",
			i+1, a.Digest, a.Instance, a.Issuer, a.Identity, at)
	}
}
