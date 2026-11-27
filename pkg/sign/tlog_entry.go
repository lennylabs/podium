package sign

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"

	"github.com/lennylabs/podium/pkg/version"
)

// rekorV1OriginPattern is the origin line of a Rekor v1 checkpoint,
// "<host> - <tree ID>". sigstore-go takes the Rekor v1 path for any origin
// that ends in " - <digits>"; every origin this pattern admits also takes
// that path.
var rekorV1OriginPattern = regexp.MustCompile(`^[^\s]+ - [0-9]+$`)

// errLogEntryForm is wrapped by every refusal of checkLogEntryForm.
var errLogEntryForm = errors.New("log entry form")

// checkLogEntryForm binds a Sigstore bundle's log entry to the path that
// sigstore-go's verify.VerifyTlogEntry takes for it, before that call runs.
//
// The library reads the entry's kind and version from the bundle's
// kindVersion label, which lies outside the signed body, and copies the
// label into the entry, so its own label check cannot fail. It then picks
// its path from two independent facts: whether the body parses as a
// Rekor v2 body, and whether the checkpoint origin has the Rekor v1 form.
// A Rekor v2 body under a Rekor v1 checkpoint takes the v1 inclusion path
// over the raw body and skips both the v1 field comparison with the bundle
// and the v2 rebuild from the bundle, so such an entry can record another
// digest and signature and still verify.
//
// The check reads the kind and the version from the canonicalized body
// and refuses a label that differs. A Rekor v1 body must lead to a Rekor
// v1 checkpoint, where the library compares the body with the bundle. A
// Rekor v2 body must be a hashedrekord 0.0.2 body and lead to a checkpoint
// whose origin is the host of the log's baseUrl, where the library
// rebuilds the body from the bundle. Every other combination is refused.
// The library still verifies the proof, the checkpoint signature, and any
// signed entry timestamp afterwards. logs maps the hexadecimal log ID to
// each transparency log of the trusted root.
//
// Spec: §4.7.11.
func checkLogEntryForm(entry *tlog.Entry, logs map[string]*root.TransparencyLog) error {
	tle := entry.TransparencyLogEntry()
	kind, apiVersion, err := bodyKindVersion(tle.GetCanonicalizedBody())
	if err != nil {
		return err
	}
	if label := tle.GetKindVersion(); label.GetKind() != kind || label.GetVersion() != apiVersion {
		return fmt.Errorf("%w: the kindVersion label differs from the signed body", errLogEntryForm)
	}
	envelope := tle.GetInclusionProof().GetCheckpoint().GetEnvelope()
	if envelope == "" {
		return fmt.Errorf("%w: the entry carries no checkpoint", errLogEntryForm)
	}
	origin, _, _ := strings.Cut(envelope, "\n")
	if !entry.IsRekorV2() {
		if !rekorV1OriginPattern.MatchString(origin) {
			return fmt.Errorf("%w: a Rekor v1 body under a checkpoint of another form", errLogEntryForm)
		}
		return nil
	}
	if kind != "hashedrekord" || apiVersion != "0.0.2" {
		return fmt.Errorf("%w: a Rekor v2 body other than hashedrekord 0.0.2", errLogEntryForm)
	}
	log, ok := logs[hex.EncodeToString(tle.GetLogId().GetKeyId())]
	if !ok {
		return fmt.Errorf("%w: the entry names no log of the trusted root", errLogEntryForm)
	}
	u, err := url.Parse(log.BaseURL)
	if err != nil || u.Hostname() == "" || origin != u.Hostname() {
		return fmt.Errorf("%w: a Rekor v2 body under a checkpoint whose origin is not the log's host", errLogEntryForm)
	}
	return nil
}

// bodyKindVersion reads the kind and apiVersion members of a
// canonicalized log entry body by exact name.
func bodyKindVersion(body []byte) (kind, apiVersion string, err error) {
	members, err := version.DecodeJSONObject(body)
	if err != nil {
		return "", "", fmt.Errorf("%w: the body does not parse: %w", errLogEntryForm, err)
	}
	kind, okKind := jsonString(members["kind"])
	apiVersion, okVersion := jsonString(members["apiVersion"])
	if !okKind || !okVersion {
		return "", "", fmt.Errorf("%w: the body names no kind or apiVersion", errLogEntryForm)
	}
	return kind, apiVersion, nil
}

// errLogKeyWindow is wrapped by a refusal of checkLogKeyWindow for a
// verified time outside the log key's window.
var errLogKeyWindow = errors.New("log key validity window")

// checkLogKeyWindow refuses an entry whose log's key validity window
// excludes one of times, wrapping errLogKeyWindow, and an entry whose log
// the root does not list, wrapping errLogEntryForm. times are the verified
// times the bundle relies on. The window is the validFor of the log's
// entry in the trusted root; for a log ID the root lists more than once,
// sigstore-go's map keeps the last listing, and that is the window
// checked. A zero bound leaves its side
// open. sigstore-go checks the window only in tlog.VerifySET, against the
// integrated time, and neither inclusion-proof path checks it, so an entry
// with no signed entry timestamp beside an RFC 3161 timestamp needs this
// check.
//
// Spec: §4.7.11.
func checkLogKeyWindow(entry *tlog.Entry, logs map[string]*root.TransparencyLog, times []time.Time) error {
	log, ok := logs[hex.EncodeToString(entry.TransparencyLogEntry().GetLogId().GetKeyId())]
	if !ok {
		return fmt.Errorf("%w: the entry names no log of the trusted root", errLogEntryForm)
	}
	for _, t := range times {
		if !log.ValidityPeriodStart.IsZero() && t.Before(log.ValidityPeriodStart) {
			return fmt.Errorf("%w: a verified time precedes the log key's window", errLogKeyWindow)
		}
		if !log.ValidityPeriodEnd.IsZero() && t.After(log.ValidityPeriodEnd) {
			return fmt.Errorf("%w: a verified time follows the log key's window", errLogKeyWindow)
		}
	}
	return nil
}
