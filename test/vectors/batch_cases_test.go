package vectors_test

import (
	"bytes"
)

// batchCases are the batch bodies. A refused entry sits between two ok
// entries, so each case pins that the refusal stays with its entry.
func batchCases() []batchCase {
	first := okEntry(rule("batch-first"), inlineRef("a.md", "a\n"))
	last := okEntry(rule("batch-last"))
	around := func(name string, e entrySpec) batchCase {
		e.refuse = true
		return batchCase{name: name, entries: []entrySpec{first, e, last}}
	}
	return []batchCase{
		{name: "text, base64, and presigned references", entries: []entrySpec{okEntry(rule("batch-mixed"),
			inlineRef("a.md", "text\n"), base64Ref("b.bin", "\x00\xfb\xff"), linkRef("c.bin", digest("large\n")))}},
		around("path in two references", okEntry(rule("batch-dup"), inlineRef("a.md", "a\n"), inlineRef("a.md", "a\n"))),
		around("non-canonical inline base64", okEntry(rule("batch-b64"), ref{"a.bin", digest("\x00\xff"),
			object{}.set("path", str("a.bin")).set("content_hash", str(digest("\x00\xff"))).
				set("inline", str("AP8")).set("inline_base64", "true")})),
		around("inline body that fails its content_hash", okEntry(rule("batch-tamper"), ref{"a.md", digest("a\n"),
			object{}.set("path", str("a.md")).set("content_hash", str(digest("a\n"))).set("inline", str("tampered\n"))})),
		around("numeric sensitivity", func() entrySpec {
			e := okEntry(rule("batch-sens"))
			e.edit = func(o object) object { return o.set("sensitivity", "5") }
			return e
		}()),
		around("reference without a path", okEntry(rule("batch-nopath"), ref{"", digest("a\n"),
			object{}.set("content_hash", str(digest("a\n"))).set("inline", str("a\n"))})),
		{name: "entry without sensitivity", entries: []entrySpec{first,
			okEntry(withSensitivity(rule("batch-nosens"), ""))}},
		around("absent artifact_revision under a hash that frames the epoch", func() entrySpec {
			r := rule("batch-epoch")
			r.ArtifactRevision = epoch
			e := okEntry(r)
			e.edit = func(o object) object { return o.without("artifact_revision") }
			return e
		}()),
		// The registry frames the true digest; the reference serves an empty
		// content_hash, which the record then frames.
		around("inline reference with an empty content_hash and an authentic body", okEntry(rule("batch-empty-hash"),
			ref{"a.md", digest("a\n"), object{}.set("path", str("a.md")).set("content_hash", `""`).set("inline", str("a\n"))})),
		{name: "zero-byte inline reference without inline", entries: []entrySpec{first, okEntry(rule("batch-empty"),
			ref{"empty.txt", digest(""), object{}.set("path", str("empty.txt")).set("content_hash", str(digest("")))})}},
		// A non-empty presigned_url makes a link reference, whose inline
		// value is ignored after its type check.
		{name: "presigned reference beside a mismatched inline", entries: []entrySpec{first, okEntry(rule("batch-link-inline"),
			ref{"big.bin", digest("large\n"), linkRef("big.bin", digest("large\n")).obj.set("inline", str("not the body"))})}},
		{name: "body that is not UTF-8", entries: []entrySpec{first, last}, refuse: true,
			raw: func(b []byte) []byte { return bytes.Replace(b, []byte("Body of"), []byte("Body\xff of"), 1) }},
	}
}
