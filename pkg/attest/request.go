package attest

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// Request is what a consumer asked a registry for: the requested ID as the
// consumer sent it, and the pin as it sent it, empty for latest.
type Request struct {
	ID  string
	Pin string
}

// CheckRequestedID applies the §4.7.11 well-formed rule to a requested ID.
// A consumer calls it before it sends the request, and an ID that breaks
// the rule is a materialize.request_mismatch Failure with the constraint
// id_form. The rule runs under every mode, none included, because the
// adapters join an ID to a directory with path.Join, and an ID such as
// GIT~1, which Windows resolves to .git, would otherwise place output in
// the repository's Git directory.
//
// Spec: §4.7.11 (Well-formed requests and records)
func CheckRequestedID(id string) error {
	if why := pathFormViolation(id, true); why != "" {
		return fail(CodeRequestMismatch, ConstraintIDForm, "the requested ID "+why, nil)
	}
	return nil
}

// CheckResourcePath applies the §4.7.11 well-formed rule to one resource
// path of a served record, on the platform of the consumer. A path that
// breaks it is a materialize.request_mismatch Failure with the constraint
// resource_path.
//
// Spec: §4.7.11 (Well-formed requests and records)
func CheckResourcePath(p string) error {
	if why := pathFormViolation(p, false); why != "" {
		return fail(CodeRequestMismatch, ConstraintResourcePath, "a resource path "+why, nil)
	}
	return nil
}

// CheckRecordPaths applies CheckResourcePath to every resource path of a
// served record, in ascending path order so the refusal names the same path
// on every run.
//
// Spec: §4.7.11 (Well-formed requests and records)
func CheckRecordPaths(rec version.DeliveryRecord) error {
	for _, p := range sortedKeys(rec.Resources) {
		if err := CheckResourcePath(p); err != nil {
			return err
		}
	}
	return nil
}

// pathFormViolation returns why p breaks the well-formed rule, or "" when
// it meets it. An ID also refuses a segment that begins with a dot, which
// no discovered package has; a resource path may hold one, such as
// .github/workflow.yml.
func pathFormViolation(p string, isID bool) string {
	switch {
	case p == "":
		return "is empty"
	case !utf8.ValidString(p):
		return "is not valid UTF-8"
	case strings.HasPrefix(p, "/"):
		return "begins with /"
	case strings.ContainsRune(p, '\\'):
		return "holds a backslash"
	case strings.ContainsFunc(p, isControl):
		return "holds a control character"
	case !isID && filepath.VolumeName(p) != "":
		return "begins with a volume name"
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "":
			return "holds an empty segment"
		case seg == "." || seg == "..":
			return fmt.Sprintf("holds the segment %q", seg)
		case isID && strings.HasPrefix(seg, "."):
			return "holds a segment that begins with ."
		case !IsPortableName(seg):
			return fmt.Sprintf("holds the segment %q, which is not a portable name", seg)
		}
	}
	return ""
}

// isControl reports whether r is a control character of the well-formed
// rule: U+0000 to U+001F and U+007F to U+009F.
func isControl(r rune) bool {
	return r <= 0x1F || (r >= 0x7F && r <= 0x9F)
}

// BindRequest applies the §4.7.11 request binding to a record that the
// §4.7.10 procedure verified: the served ID equals the requested ID byte for
// byte, and the served version or content hash meets the pin as the
// registry's own pin rule reads it. A failure is a
// materialize.request_mismatch Failure with the constraint id, version, or
// content_hash, which no mode reduces to a warning.
//
// Spec: §4.7.11 (Request binding)
func BindRequest(req Request, rec version.DeliveryRecord) error {
	if rec.ID != req.ID {
		return fail(CodeRequestMismatch, ConstraintID, "the served ID differs from the requested ID", nil)
	}
	return bindPin(req.Pin, rec)
}

// bindPin compares the served version or content hash with pin. A pin is
// parsed by version.ParsePin, the parser the registry resolves pins with,
// so the binding refuses no answer the registry's own rule selects.
func bindPin(pin string, rec version.DeliveryRecord) error {
	refuse := func(detail string) error {
		return fail(CodeRequestMismatch, ConstraintVersion, detail, nil)
	}
	p, err := version.ParsePin(pin)
	if err != nil {
		return refuse("the pin names no version a registry can serve")
	}
	switch p.Kind {
	case version.PinLatest:
		return nil
	case version.PinContentHash:
		if rec.ContentHash != "sha256:"+strings.ToLower(p.Hash) {
			return fail(CodeRequestMismatch, ConstraintContentHash, "the served content hash differs from the content pin", nil)
		}
		return nil
	case version.PinExact:
		if p.Auto {
			if rec.Version != pin {
				return refuse("the served version differs from the registry-assigned pin")
			}
			return nil
		}
		return bindNumbers(rec.Version, p, 3, refuse)
	case version.PinMinor:
		return bindNumbers(rec.Version, p, 2, refuse)
	case version.PinMajor:
		return bindNumbers(rec.Version, p, 1, refuse)
	}
	// ParsePin returns no other kind; a new kind binds nothing until the
	// binding states it, so it is refused rather than admitted.
	return refuse("the pin is of an unknown kind")
}

// bindNumbers requires served to have the major.minor.patch form and its
// first n numbers to equal those of p by value. A pin with a negative
// number names no version.
func bindNumbers(served string, p version.Pin, n int, refuse func(string) error) error {
	want := []int{p.Major, p.Minor, p.Patch}[:n]
	for _, v := range want {
		if v < 0 {
			return refuse("the pin holds a negative number and names no version")
		}
	}
	got, ok := servedNumbers(served)
	if !ok {
		return refuse("the served version does not have the major.minor.patch form")
	}
	for i, v := range want {
		if got[i] != fmt.Sprint(v) {
			return refuse("the served version does not meet the pin")
		}
	}
	return nil
}

// servedNumbers splits a served version of the major.minor.patch form into
// its three numbers, each with its leading zeros removed so that numbers
// compare by value as decimal text, whatever their size.
func servedNumbers(v string) ([3]string, bool) {
	var out [3]string
	if manifest.ValidateVersion(v) != nil {
		return out, false
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		trimmed := strings.TrimLeft(part, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		out[i] = trimmed
	}
	return out, true
}

// BindBatch applies the §4.7.11 rule for batch entries to the entries of a
// §7.6.2 batch response: requested lists the IDs of the request, an ID once
// for each time the request lists it, and pins maps an ID to its
// version_pins value. It returns one error for each entry, nil for an entry
// that is not ok and for an ok entry that answers the request. An ok entry
// whose id the request does not list, and each ok entry whose id occurs in
// more ok entries than the request lists it, fail with the constraint
// batch_entry; every other ok entry is bound by BindRequest under its ID's
// pin. A requested ID that no entry answers is withheld, which the caller
// reports.
//
// Spec: §4.7.11 (Batch entries), §7.6.2
func BindBatch(requested []string, pins map[string]string, entries []version.BatchEntry) []error {
	want := map[string]int{}
	for _, id := range requested {
		want[id]++
	}
	got := map[string]int{}
	for _, e := range entries {
		if e.Status == "ok" && e.Err == nil {
			got[e.Served.Record.ID]++
		}
	}
	out := make([]error, len(entries))
	for i, e := range entries {
		if e.Status != "ok" || e.Err != nil {
			continue
		}
		id := e.Served.Record.ID
		if want[id] == 0 || got[id] > want[id] {
			out[i] = fail(CodeRequestMismatch, ConstraintBatchEntry, "the entry answers no requested ID, or repeats one", nil)
			continue
		}
		out[i] = BindRequest(Request{ID: id, Pin: pins[id]}, e.Served.Record)
	}
	return out
}
