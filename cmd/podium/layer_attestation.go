package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// attestationNextSteps is the next step `podium layer show` prints under a
// layer's attestation status, keyed by the status and the constraint of the
// details. A status with no constraint of its own is keyed with "".
//
// Spec: §4.7.11 (Attestation record), §7.3.1
var attestationNextSteps = map[[2]string]string{
	{"ingest.attestation_missing", "no_bundle"}:          "Check that the tracked ref is the signed ref and that the last run of the signing workflow succeeded.",
	{"ingest.attestation_missing", "no_accepted_bundle"}: "The listed bundles were refused. Compare each certificate's identity, source repository, and source ref with the layer's signers.",
	{"ingest.attestation_missing", "incomplete"}:         "The attestation directory on the tracked ref lacks a statement of the covering signer, and the listed artifacts have no subject from that signer. Compare the tip of the tracked ref with the signed ref of the repository, and run the signing workflow again. A recipe written by hand signs every file that `podium attest` writes.",
	{"ingest.attestation_missing", "no_layer_subject"}:   "The accepted bundles carry no layer subject for this layer's root. Check the --root the workflow passes to `podium attest`. For a local layer, check the layer's trust_layer_root.",
	{"ingest.attestation_mismatch", "layer_digest"}:      "The tracked ref holds content that no bundle covers. Check the last run of the signing workflow, and compare the listed subjects when the podium version in CI differs from the registry's. When no subject is listed, a DOMAIN.md or the .layer-config file differs from the signed state. When a listed subject carries fields, compare them with the output of `podium attest check` run with the registry's release of podium.",
	{"ingest.attestation_stale", "mark"}:                 "The newest bundle on the tracked ref is older than the last accepted one. Run the signing workflow on the current tip of the main branch. After a change of the layer's source, reset the mark with `podium layer reingest <id> --reset-attestation-mark`.",
	{"ingest.attestation_stale", "not_descendant"}:       "The signed commit does not descend from the last accepted one. After a re-run of an old workflow run, run the workflow on the current tip of the main branch. After a rewrite of the main branch, or a change of the layer's source, reset the mark with `podium layer reingest <id> --reset-attestation-mark --to-commit <sha>`, naming the new tip.",
	{"ingest.attestation_stale", "not_in_history"}:       "The signed commit is not in the history of the tracked ref. A recipe that publishes to a separate ref gives each published commit the source commit as a parent.",
	{"ingest.source_unreachable", "fetch_deadline"}:      "The Git fetch for this layer reached PODIUM_GIT_FETCH_TIMEOUT at the reported depth before the history decided an answer. A cycle needs that much history after a rewritten history, after a recreated signed ref, and for a signed commit that does not descend from the last accepted one. Raise PODIUM_GIT_FETCH_TIMEOUT until a cycle reports the underlying result, or reset the mark when the cause is known.",
	{"ingest.attestation_invalid", "subject_name"}:       "A package directory on the tracked ref has a name with a control character, a white space character, or a backslash, and no statement can name it. Rename the listed directories.",
	{"ingest.attestation_invalid", "limits"}:             "The attestation directory on the tracked ref exceeds the limits. Run the signing workflow with recover, which rebuilds the directory from that run's bundles.",
	{"ingest.attestation_policy_invalid", ""}:            "The layer's trust policy does not meet the registry default, or names an instance the registry no longer configures. Update the policy with `podium layer update`.",
	{"ingest.attestation_root_stale", ""}:                "The registry's trusted root for the public-good instance is past its maximum age. Check the registry's access to the Sigstore TUF repository, or set PODIUM_ATTESTATION_TRUSTED_ROOT_FILE.",
}

// layerRecordView is the part of a layer object the attestation notes read.
type layerRecordView struct {
	ID                 string         `json:"id"`
	TrustMode          string         `json:"trust_mode"`
	AttestationStatus  string         `json:"attestation_status"`
	AttestationDetails map[string]any `json:"attestation_details"`
	AttestationDropped int            `json:"attestation_dropped"`
}

// writeAttestationNotes writes to w what `podium layer show` states about a
// layer's attestation record beside the layer object: the next step under a
// status other than ok, the count of the artifacts the last cycle dropped,
// and, for a layer in mode none, that no cycle updates the record. The layer
// object stays the only output on standard output, so a script reads it as
// JSON.
//
// Spec: §4.7.11 (Attestation record), §7.3.1
func writeAttestationNotes(w io.Writer, raw json.RawMessage) {
	var l layerRecordView
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	if l.TrustMode == "none" && l.AttestationStatus != "" {
		_, _ = fmt.Fprintln(w, "note: the layer's trust mode is none, and no cycle updates its attestation record in that mode.")
	}
	if l.AttestationStatus != "" && l.AttestationStatus != "ok" {
		constraint, _ := l.AttestationDetails["constraint"].(string)
		key := [2]string{l.AttestationStatus, constraint}
		if _, ok := attestationNextSteps[key]; !ok {
			key[1] = ""
		}
		if step, ok := attestationNextSteps[key]; ok {
			_, _ = fmt.Fprintf(w, "attestation status %s%s\nnext step: %s\n", quoteReported(l.AttestationStatus), constraintSuffix(constraint), step)
		}
	}
	if l.AttestationDropped > 0 {
		_, _ = fmt.Fprintf(w, "the last cycle dropped %d artifacts; `podium layer reingest %s` lists each with its code.\n", l.AttestationDropped, l.ID)
	}
}

// constraintSuffix renders a constraint after the status.
func constraintSuffix(c string) string {
	if c == "" {
		return ""
	}
	return " (constraint " + quoteReported(c) + ")"
}

// quoteReported prints a string the registry reports between quotation marks,
// with each control character written as a \u escape.
//
// Spec: §4.7.11 (Reported strings)
func quoteReported(s string) string {
	return `"` + string(escapeControls([]byte(s))) + `"`
}

// escapeControls writes each character of the categories Cc, Cf, Zl, and Zp
// in b as a \u escape, and each byte that is not valid UTF-8 as \ufffd. Inside
// a JSON string the escape is the same character, so a layer object stays
// valid JSON, and a bidirectional control or a line break a registry sends
// cannot reorder or split what the terminal shows.
//
// Spec: §4.7.11 (Reported strings)
func escapeControls(b []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		switch {
		case r == utf8.RuneError && size == 1:
			out.WriteString(`\ufffd`)
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp):
			writeEscape(&out, r)
		default:
			out.Write(b[:size])
		}
		b = b[size:]
	}
	return out.Bytes()
}

// writeEscape writes r as a JSON \u escape, as a surrogate pair above
// U+FFFF.
func writeEscape(out *bytes.Buffer, r rune) {
	if r > 0xFFFF {
		hi, lo := utf16.EncodeRune(r)
		_, _ = fmt.Fprintf(out, `\u%04x\u%04x`, hi, lo)
		return
	}
	_, _ = fmt.Fprintf(out, `\u%04x`, r)
}
