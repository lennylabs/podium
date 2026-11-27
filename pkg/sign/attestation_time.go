package sign

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

// oidRSASSAPSS is the PKCS #1 RSASSA-PSS signature identifier.
var oidRSASSAPSS = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}

// timeEvidence is what the timestamp condition and the signed entry
// timestamp yield: the verified times and the TypeScript-limit mark.
type timeEvidence struct {
	times []time.Time
	// rsaNonSHA256 is set when a verified timestamp carries an RSA
	// signature with a digest other than SHA-256.
	rsaNonSHA256 bool
}

// checkTimestampCondition applies the §4.7.11 timestamp condition in
// Podium code before sigstore-go verifies the bundle: each RFC 3161
// timestamp verifies under an authority of the instance, no two come from
// one authority, each embeds at most one certificate, and the bundle
// carries at most one log entry and none where the instance requires no
// transparency log. sigstore-go reports one timestamp per authority, the
// first listed, and passes over one that does not verify, so the earliest
// time is not available from its result. The token signatures are
// verified by the library's timestamp authority verifier.
//
// Spec: §4.7.11.
func checkTimestampCondition(b *bundle.Bundle, tm *root.TrustedRoot, sig []byte, requireLog bool) (timeEvidence, error) {
	entries, err := b.TlogEntries()
	if err != nil {
		return timeEvidence{}, refuse(ReasonParse, "a log entry does not parse", err)
	}
	if len(entries) > 1 {
		return timeEvidence{}, refuse(ReasonTimestampCondition, "the bundle carries more than one log entry", nil)
	}
	if len(entries) == 1 && !requireLog {
		return timeEvidence{}, refuse(ReasonTimestampCondition, "the bundle carries a log entry and the instance requires no transparency log", nil)
	}
	tokens, err := b.Timestamps()
	if err != nil {
		return timeEvidence{}, refuse(ReasonParse, "the timestamps do not parse", err)
	}
	var (
		ev   timeEvidence
		seen = map[string]bool{}
	)
	for _, resp := range tokens {
		parsed, err := parseResponseToken(resp)
		if err != nil {
			return timeEvidence{}, refuse(ReasonTimestampCondition, "a timestamp does not parse", err)
		}
		if len(parsed.embedded) > 1 {
			return timeEvidence{}, refuse(ReasonTimestampCondition, "a timestamp embeds more than one certificate", nil)
		}
		ts, err := verifyUnderAuthorities(tm, resp, sig)
		if err != nil {
			return timeEvidence{}, refuse(ReasonTimestampCondition, "a timestamp verifies under no authority of the instance", err)
		}
		// Entries that share a URI, or that carry none, are one authority.
		if seen[ts.URI] {
			return timeEvidence{}, refuse(ReasonTimestampCondition, "two timestamps come from one authority", nil)
		}
		seen[ts.URI] = true
		ev.times = append(ev.times, ts.Time)
		ev.rsaNonSHA256 = ev.rsaNonSHA256 || rsaWithoutSHA256(parsed.signer)
	}
	return ev, nil
}

// parseResponseToken decodes a bundle's DER TimeStampResp and the token it
// carries, for the count of embedded certificates and the signer's
// algorithms. The library verifies the response itself.
func parseResponseToken(der []byte) (parsedToken, error) {
	var resp timeStampResp
	if rest, err := asn1.Unmarshal(der, &resp); err != nil || len(rest) > 0 {
		return parsedToken{}, fmt.Errorf("timestamp response does not parse: %v", errOrTrailing(err))
	}
	return parseToken(resp.TimeStampToken.FullBytes)
}

// verifyUnderAuthorities verifies token over sig under each timestamp
// authority of the root in turn and returns the first that verifies it.
func verifyUnderAuthorities(tm *root.TrustedRoot, token, sig []byte) (*root.Timestamp, error) {
	var errs []error
	for _, tsa := range tm.TimestampingAuthorities() {
		ts, err := tsa.Verify(token, sig)
		if err == nil {
			return ts, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(append(errs, errors.New("no authority verified the timestamp"))...)
}

// rsaWithoutSHA256 reports whether a SignerInfo carries an RSA signature
// with a digest other than SHA-256, which the TypeScript verifier refuses.
func rsaWithoutSHA256(si cmsSignerInfo) bool {
	alg := si.SignatureAlgorithm.Algorithm
	rsa := alg.Equal(oidRSAEncryption) || alg.Equal(oidSHA256WithRSA) || alg.Equal(oidSHA384WithRSA) ||
		alg.Equal(oidSHA512WithRSA) || alg.Equal(oidRSASSAPSS)
	return rsa && !si.DigestAlgorithm.Algorithm.Equal(oidSHA256)
}

// signedEntryTime returns the integrated time of the bundle's log entry
// when the entry carries a signed entry timestamp that verifies under a
// log key of the root. The inclusion proof of a Rekor v1 entry does not
// cover the integrated time, so an entry without a verifying promise
// yields no time.
//
// Spec: §4.7.11.
func signedEntryTime(b *bundle.Bundle, tm *root.TrustedRoot) (time.Time, bool) {
	entries, err := b.TlogEntries()
	if err != nil || len(entries) != 1 || !entries[0].HasInclusionPromise() {
		return time.Time{}, false
	}
	if err := tlog.VerifySET(entries[0], tm.RekorLogs()); err != nil {
		return time.Time{}, false
	}
	return entries[0].IntegratedTime(), true
}

// sortedTimes returns times in ascending order.
func sortedTimes(times []time.Time) []time.Time {
	out := slices.Clone(times)
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}
