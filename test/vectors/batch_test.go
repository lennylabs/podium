package vectors_test

import (
	"fmt"
	"testing"

	"github.com/lennylabs/podium/pkg/version"
)

// batchVector is one §7.6.2 batch body. Outcome refuses the whole call; when
// it is ok, Entries holds each entry's outcome in body order.
type batchVector struct {
	Name       string        `json:"name"`
	BodyBase64 string        `json:"body_base64"`
	Outcome    string        `json:"outcome"`
	Entries    []entryVector `json:"entries,omitempty"`
}

// entryVector is the outcome of one batch entry. ServedHash is the delivery
// hash an ok entry verifies to.
type entryVector struct {
	Outcome    string `json:"outcome"`
	ServedHash string `json:"served_hash,omitempty"`
}

// entrySpec is the wire form of one ok batch entry. rec is the record the
// entry frames; refs are its reference objects; edit alters the encoded
// entry after the hash is computed.
type entrySpec struct {
	rec    version.DeliveryRecord
	refs   []object
	edit   func(object) object
	refuse bool
}

// batchCase is one batch body. raw, when set, replaces the encoded body.
type batchCase struct {
	name    string
	entries []entrySpec
	raw     func([]byte) []byte
	refuse  bool
}

// ref is one batch reference: its path, the content hash the entry frames for
// it, and its encoded object.
type ref struct {
	path, hash string
	obj        object
}

// inlineRef is a text inline reference carrying its body's digest.
func inlineRef(path, body string) ref {
	return ref{path, digest(body), object{}.set("path", str(path)).
		set("content_hash", str(digest(body))).set("inline", str(body))}
}

// base64Ref is an inline reference carrying its body as base64.
func base64Ref(path, body string) ref {
	return ref{path, digest(body), object{}.set("path", str(path)).set("content_hash", str(digest(body))).
		set("inline", str(b64([]byte(body)))).set("inline_base64", "true")}
}

// linkRef is a presigned reference.
func linkRef(path, hash string) ref {
	return ref{path, hash, object{}.set("path", str(path)).
		set("presigned_url", str(objectURL(path))).set("content_hash", str(hash))}
}

// okEntry returns an entry whose record frames each reference's content hash.
func okEntry(rec version.DeliveryRecord, refs ...ref) entrySpec {
	rec.Resources = map[string]string{}
	objs := make([]object, len(refs))
	for i, r := range refs {
		rec.Resources[r.path] = r.hash
		objs[i] = r.obj
	}
	return entrySpec{rec: rec, refs: objs}
}

// object encodes the entry as the registry serves it: each empty string
// member other than id, delivery_hash, and artifact_revision is absent.
func (e entrySpec) object(t testing.TB) object {
	r := e.rec
	o := object{}.set("id", str(r.ID)).set("status", str("ok"))
	for _, m := range []struct{ name, value string }{
		{"type", r.Type}, {"version", r.Version}, {"content_hash", r.ContentHash},
		{"sensitivity", r.Sensitivity}, {"manifest_body", r.ManifestBody},
		{"frontmatter", r.Frontmatter}, {"skill_raw", r.SkillRaw},
	} {
		if m.value != "" {
			o = o.set(m.name, str(m.value))
		}
	}
	if len(e.refs) > 0 {
		refs := make([]string, len(e.refs))
		for i, obj := range e.refs {
			refs[i] = obj.String()
		}
		o = o.set("resources", array(refs...))
	}
	hash := version.DeliveryHash(r)
	o = o.set("delivery_hash", str(hash)).set("delivery_signature", str(signHash(t, hash))).
		set("artifact_revision", str(r.ArtifactRevision))
	if e.edit != nil {
		o = e.edit(o)
	}
	return o
}

// batchVectors builds the batch section and checks each case.
func batchVectors(t *testing.T) []batchVector {
	cases := batchCases()
	out := make([]batchVector, 0, len(cases))
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, c.vector(t))
		names = append(names, c.name)
	}
	checkUniqueNames(t, "batch", names)
	return sortByName(out, func(v batchVector) string { return v.Name })
}

// vector encodes the case and asserts that Go reaches the declared outcome of
// the call and of each entry.
func (c batchCase) vector(t *testing.T) batchVector {
	parts := make([]string, len(c.entries))
	for i, e := range c.entries {
		parts[i] = e.object(t).String()
	}
	body := []byte(array(parts...))
	if c.raw != nil {
		body = c.raw(body)
	}
	v := batchVector{Name: c.name, BodyBase64: b64(body), Outcome: codeMismatch}
	entries, err := version.ParseBatchResponse(body)
	if c.refuse {
		if err == nil {
			t.Errorf("batch/%s: Go accepted a body the case declares refused", c.name)
		}
		return v
	}
	if err != nil {
		t.Errorf("batch/%s: Go refused the whole body: %v", c.name, err)
		return v
	}
	if len(entries) != len(c.entries) {
		t.Errorf("batch/%s: Go decoded %d entries, want %d", c.name, len(entries), len(c.entries))
		return v
	}
	v.Outcome = outcomeOK
	for i, e := range c.entries {
		v.Entries = append(v.Entries, e.verify(t, fmt.Sprintf("batch/%s/entry %d", c.name, i), entries[i]))
	}
	return v
}

// verify applies the step 7 comparison to a decoded entry and asserts the
// entry's declared outcome.
func (e entrySpec) verify(t *testing.T, where string, got version.BatchEntry) entryVector {
	err := got.Err
	if err == nil {
		if hash := version.DeliveryHash(got.Served.Record); got.Served.Hash == "" || hash != got.Served.Hash {
			err = fmt.Errorf("delivery hash %s does not match served %q", hash, got.Served.Hash)
		}
	}
	switch {
	case e.refuse && err == nil:
		t.Errorf("%s: Go accepted an entry the case declares refused", where)
	case !e.refuse && err != nil:
		t.Errorf("%s: Go refused an entry the case declares ok: %v", where, err)
	case !e.refuse:
		checkRecord(t, where, got.Served.Record, e.rec)
		return entryVector{Outcome: outcomeOK, ServedHash: got.Served.Hash}
	}
	return entryVector{Outcome: codeMismatch}
}
