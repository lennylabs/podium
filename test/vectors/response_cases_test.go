package vectors_test

import (
	"strings"

	"github.com/lennylabs/podium/pkg/version"
)

// objectURL is the presigned URL a case's object store serves path at.
func objectURL(path string) string { return "https://objects.acme.com/presigned/" + path }

// okResponseCases are the responses cases every consumer verifies. Each
// declares the record its body frames.
func okResponseCases() []responseCase {
	return []responseCase{
		{name: "spec record with SKILL.md", spec: withInline(version.DeliveryRecord{
			ID: "team/review", Version: "1.2.0", Type: "skill",
			ContentHash: "sha256:abc", Sensitivity: "internal",
			ArtifactRevision: "2025-01-01T00:00:00.000000Z",
			Frontmatter:      "---\nname: review\n---\n",
			ManifestBody:     "body\n",
			SkillRaw:         "---\nname: review\n---\nskill\n",
		}, map[string]string{"ref.md": "reference\n", "a.md": "first\n"})},
		// The one ok case at the epoch: the registry held no ingest time.
		{name: "spec record without SKILL.md", spec: withInline(version.DeliveryRecord{
			ID: "team/rule", Version: "0.1.0", Type: "rule",
			ContentHash: "sha256:def", Sensitivity: "public",
			ArtifactRevision: epoch,
			Frontmatter:      "---\nname: rule\n---\n",
		}, map[string]string{"data/a.txt": "a\n"})},
		// U+FF5E sorts before U+1F600 in UTF-8 byte order and after it in
		// UTF-16 code-unit order.
		{name: "resource paths whose UTF-8 and UTF-16 orders differ", spec: withInline(rule("paths"),
			map[string]string{"\uff5e.md": "tilde\n", "\U0001F600.md": "grin\n"})},
		{name: "non-ASCII frontmatter", spec: loadSpec{rec: func() version.DeliveryRecord {
			r := rule("accents")
			r.Frontmatter = "---\nname: caf\u00e9 \u2713\n---\nna\u00efve \U0001F600\n"
			r.ManifestBody = "na\u00efve \U0001F600\n"
			return r
		}()}},
		{name: "sensitivity empty", spec: loadSpec{rec: withSensitivity(rule("sens-empty"), "")}},
		{name: "sensitivity absent", spec: loadSpec{rec: withSensitivity(rule("sens-absent"), "")},
			edit: func(o object) object { return o.without("sensitivity") }},
		{name: "sensitivity null", spec: loadSpec{rec: withSensitivity(rule("sens-null"), "")},
			edit: func(o object) object { return o.set("sensitivity", "null") }},
		{name: "skill with an empty SKILL.md", spec: loadSpec{rec: func() version.DeliveryRecord {
			r := rule("empty-skill")
			r.Type = "skill"
			return r
		}()}, edit: func(o object) object { return o.set("skill_raw", `""`) }},
		{name: "no resources", spec: loadSpec{rec: rule("bare")}},
		{name: "canonical resources_base64", spec: base64Spec()},
		{name: "manifest_body_url document that is not UTF-8", spec: manifestSpec("rule", "big-rule",
			"---\nname: big-rule\n---\nbin\xffary body\n", "bin\xffary body\n", true)},
		{name: "manifest_body_url with an empty content_hash", spec: manifestSpec("skill", "big-skill",
			"---\nname: big-skill\n---\nskill body\n", "skill body\n", false)},
		{name: "resource named __proto__", spec: withInline(rule("proto"),
			map[string]string{"__proto__": "prototype\n", "a.md": "a\n"})},
		// The raw body writes U+1F600 as two \u escapes and holds no raw
		// UTF-8 bytes of it; the record frames F0 9F 98 80.
		{name: "frontmatter written as a surrogate-pair escape", spec: loadSpec{rec: func() version.DeliveryRecord {
			r := rule("escape")
			r.Frontmatter = "---\nname: \U0001F600\n---\n"
			r.ManifestBody = ""
			return r
		}()}, edit: func(o object) object { return o.set("frontmatter", `"---\nname: \ud83d\ude00\n---\n"`) }},
		{name: "repeated frontmatter frames the later member", spec: loadSpec{rec: rule("repeat")},
			edit: func(o object) object { return o.before("frontmatter", `"decoy"`) }},
		{name: "FRONTMATTER beside frontmatter is ignored", spec: loadSpec{rec: rule("case-variant")},
			edit: func(o object) object { return o.plus("FRONTMATTER", `"decoy"`) }},
		{name: "mistyped case variants are ignored", spec: loadSpec{rec: rule("case-mistyped")},
			edit: func(o object) object { return o.plus("FRONTMATTER", "5").plus("SENSITIVITY", "5") }},
		{name: "mistyped earlier sensitivity frames the later member", spec: loadSpec{rec: rule("sens-repeat")},
			edit: func(o object) object { return o.before("sensitivity", "5") }},
		{name: "unknown member nested 60 levels", spec: loadSpec{rec: rule("nest-60")},
			edit: func(o object) object { return o.plus("x", nest(60)) }},
		{name: "body nested exactly 64 levels", spec: loadSpec{rec: rule("nest-64")},
			edit: func(o object) object { return o.plus("x", nest(63)) }},
		{name: "unknown member holding a 5000-digit integer", spec: loadSpec{rec: rule("big-int")},
			edit: func(o object) object { return o.plus("x", bigInt) }},
		{name: "mistyped extends_pin is ignored", spec: loadSpec{rec: rule("extends-pin")},
			edit: func(o object) object { return o.plus("extends_pin", "5") }},
		// No consumer's delivery check reads the format of the ingest time.
		{name: "artifact_revision outside the canonical form", spec: loadSpec{rec: func() version.DeliveryRecord {
			r := rule("odd-revision")
			r.ArtifactRevision = "2025-01-01T00:00:00Z"
			return r
		}()}},
		// A runner that fetches the url member finds no body and fails.
		{name: "large link with an unknown url member", spec: withLinks(rule("link-url"), map[string]link{
			"big.bin": {url: objectURL("big.bin"), hash: digest("large body\n"), body: "large body\n",
				extra: object{}.set("url", str("https://objects.acme.com/elsewhere"))},
		})},
	}
}

// withSensitivity returns rec with its sensitivity replaced.
func withSensitivity(rec version.DeliveryRecord, s string) version.DeliveryRecord {
	rec.Sensitivity = s
	return rec
}

// base64Spec serves two binary resources under resources_base64.
func base64Spec() loadSpec {
	bodies := map[string]string{"bin/a.dat": "\x00\xfb\xff\x10", "bin/b.dat": "plain"}
	s := withInline(rule("base64"), bodies)
	s.b64 = true
	s.inline = map[string]string{}
	for path, body := range bodies {
		s.inline[path] = b64([]byte(body))
	}
	return s
}

// manifestSpec serves the manifest document doc by manifest_body_url. body is
// the manifest body the case declares the document derives to. A skill's
// document fills SKILL.md and every other type's fills ARTIFACT.md.
func manifestSpec(typ, name, doc, body string, withHash bool) loadSpec {
	rec := rule(name)
	rec.Type = typ
	rec.ManifestBody = body
	if typ == "skill" {
		rec.SkillRaw = doc
	} else {
		rec.Frontmatter = doc
	}
	l := link{url: objectURL(name + ".md"), body: doc}
	if withHash {
		l.hash = digest(doc)
	}
	return loadSpec{rec: rec, manifest: &l}
}

// refusedResponseCases are the responses cases every consumer refuses with
// materialize.content_hash_mismatch, grouped by the §4.7.10 step each
// exercises. The grouping documents intent; no runner reports a step.
func refusedResponseCases() []responseCase {
	var cases []responseCase
	for _, group := range [][]responseCase{
		jsonRuleCases(), memberCases(), base64ResourceCases(), bodyCases(), compareCases(),
	} {
		for _, c := range group {
			c.refuse = true
			cases = append(cases, c)
		}
	}
	return cases
}

// jsonRuleCases exercise step 1.
func jsonRuleCases() []responseCase {
	edit := func(name string, f func(object) object) responseCase {
		return responseCase{name: name, spec: loadSpec{rec: rule("step1")}, edit: f}
	}
	return []responseCase{
		edit("raw 0xFF byte inside a string", func(o object) object { return o.set("frontmatter", "\"bad \xff byte\"") }),
		{name: "leading byte order mark", spec: loadSpec{rec: rule("step1")},
			raw: func(b []byte) []byte { return append([]byte("\xef\xbb\xbf"), b...) }},
		edit("NaN in an unknown member", func(o object) object { return o.plus("x", "NaN") }),
		edit("body nested 65 levels", func(o object) object { return o.plus("x", nest(64)) }),
		{name: "top-level array", spec: loadSpec{rec: rule("step1")},
			raw: func(b []byte) []byte { return []byte("[" + string(b) + "]") }},
		edit("frontmatter holding an unpaired high surrogate", func(o object) object { return o.set("frontmatter", `"\ud800"`) }),
		edit("resource path holding an unpaired low surrogate", func(o object) object {
			return o.set("resources", object{}.plusRawKey(`"\udc00.md"`, `"x"`).String())
		}),
		edit("high surrogate followed by a letter", func(o object) object { return o.set("frontmatter", `"\ud800A"`) }),
		edit("repeated unknown member whose earlier value holds a surrogate", func(o object) object {
			return o.plus("x", `"\ud800"`).plus("x", `"ok"`)
		}),
		edit("repeated unknown member whose earlier value nests 65 levels", func(o object) object {
			return o.plus("x", nest(64)).plus("x", "1")
		}),
	}
}

// memberCases exercise step 2.
func memberCases() []responseCase {
	edit := func(name string, f func(object) object) responseCase {
		return responseCase{name: name, spec: withInline(rule("step2"), map[string]string{"a.md": "a\n"}), edit: f}
	}
	return []responseCase{
		edit("numeric sensitivity", func(o object) object { return o.set("sensitivity", "5") }),
		edit("numeric artifact_revision", func(o object) object { return o.set("artifact_revision", "5") }),
		edit("string resources_base64", func(o object) object { return o.set("resources_base64", `"true"`) }),
		edit("numeric resources", func(o object) object { return o.set("resources", "5") }),
		{name: "link with an empty presigned_url", spec: withLinks(rule("step2"), map[string]link{
			"big.bin": {hash: digest("large\n"), body: "large\n"},
		})},
	}
}

// base64ResourceCases exercise step 3 on a resource whose canonical encoding
// is "+/8=", the encoding of FB FF.
func base64ResourceCases() []responseCase {
	edit := func(name, value string) responseCase {
		s := withInline(rule("step3"), map[string]string{"a.bin": "\xfb\xff"})
		s.b64, s.inline = true, map[string]string{"a.bin": "+/8="}
		return responseCase{name: name, spec: s, edit: func(o object) object {
			return o.set("resources", object{}.set("a.bin", value).String())
		}}
	}
	return []responseCase{
		edit("resource base64 with an escaped line break", `"+/\r\n8="`),
		edit("resource base64 without padding", `"+/8"`),
		edit("resource base64 in the URL-safe alphabet", `"-_8="`),
		edit("resource base64 with a non-zero pad bit", `"+/9="`),
	}
}

// bodyCases exercise steps 4 and 6.
func bodyCases() []responseCase {
	both := withInline(rule("step4"), map[string]string{"a.md": "a\n"})
	both.links = map[string]link{"a.md": {url: objectURL("a.md"), hash: digest("a\n"), body: "a\n"}}
	return []responseCase{
		{name: "path in both resources and large_resources", spec: both},
		{name: "fetched large body that fails its content_hash", spec: withLinks(rule("step6"), map[string]link{
			"big.bin": {url: objectURL("big.bin"), hash: digest("large body\n"), body: "tampered body\n"},
		})},
		{name: "manifest_body_url document that fails its content_hash", spec: func() loadSpec {
			s := manifestSpec("rule", "step6-doc", "---\nname: step6-doc\n---\nbody\n", "body\n", true)
			s.manifest.body = "---\nname: step6-doc\n---\ntampered\n"
			return s
		}()},
	}
}

// compareCases exercise step 7.
func compareCases() []responseCase {
	authentic := withLinks(rule("step7-link"), map[string]link{
		"big.bin": {url: objectURL("big.bin"), body: "large body\n"},
	})
	// The registry frames the true digest; the link serves an empty hash.
	authentic.rec.Resources["big.bin"] = digest("large body\n")
	noRevision := rule("step7-epoch")
	noRevision.ArtifactRevision = epoch
	return []responseCase{
		{name: "empty delivery_hash", spec: loadSpec{rec: rule("step7")},
			edit: func(o object) object { return o.set("delivery_hash", `""`) }},
		{name: "uppercase-hex delivery_hash", spec: loadSpec{rec: rule("step7")},
			edit: func(o object) object {
				return o.set("delivery_hash", str("sha256:"+strings.ToUpper(strings.TrimPrefix(version.DeliveryHash(rule("step7")), "sha256:"))))
			}},
		{name: "artifact_revision changed after hashing", spec: loadSpec{rec: rule("step7")},
			edit: func(o object) object { return o.set("artifact_revision", str("2025-06-01T12:34:56.789013Z")) }},
		// A consumer frames an absent revision as empty and never
		// substitutes the epoch.
		{name: "absent artifact_revision under a hash that frames the epoch", spec: loadSpec{rec: noRevision},
			edit: func(o object) object { return o.without("artifact_revision") }},
		{name: "large link with an empty content_hash and an authentic body", spec: authentic},
	}
}
