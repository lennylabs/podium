package vectors_test

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// responseVector is one load_artifact body of the responses section. Fetched
// maps each presigned URL the body names to the bytes the object store
// serves. ServedHash is the delivery hash an ok case verifies to.
type responseVector struct {
	Name       string            `json:"name"`
	BodyBase64 string            `json:"body_base64"`
	Fetched    map[string]string `json:"fetched,omitempty"`
	Outcome    string            `json:"outcome"`
	ServedHash string            `json:"served_hash,omitempty"`
}

// revision is the ingest time most cases frame. It differs from every
// sensitivity value, so a framer that drops or reorders it fails.
const revision = "2025-06-01T12:34:56.789012Z"

// epoch is the ingest time a registry frames when it holds none.
const epoch = "1970-01-01T00:00:00.000000Z"

// link is one large-resource or manifest_body_url link of a load_artifact
// body and the bytes its presigned URL serves.
type link struct {
	url   string
	hash  string
	body  string
	extra object
}

// loadSpec is the wire form of one load_artifact body. rec is the record the
// body frames, and the body's delivery_hash is version.DeliveryHash(rec).
type loadSpec struct {
	rec      version.DeliveryRecord
	inline   map[string]string
	b64      bool
	links    map[string]link
	manifest *link
}

// responseCase is one responses case. An ok case declares its record in
// spec.rec; edit and raw alter the encoded body after the hash is computed.
type responseCase struct {
	name   string
	spec   loadSpec
	edit   func(object) object
	raw    func([]byte) []byte
	refuse bool
}

// rule returns a rule record with no resources, the base most cases edit.
func rule(name string) version.DeliveryRecord {
	return version.DeliveryRecord{
		ID:               "acme/" + name,
		Version:          "1.0.0",
		Type:             "rule",
		ContentHash:      digest("stored package " + name),
		Sensitivity:      "internal",
		ArtifactRevision: revision,
		Frontmatter:      "---\nname: " + name + "\n---\nBody of " + name + ".\n",
		ManifestBody:     "Body of " + name + ".\n",
	}
}

// withInline returns a spec whose resources member serves bodies as text,
// with the record's resource map holding each body's digest.
func withInline(rec version.DeliveryRecord, bodies map[string]string) loadSpec {
	rec.Resources = map[string]string{}
	for path, body := range bodies {
		rec.Resources[path] = digest(body)
	}
	return loadSpec{rec: rec, inline: bodies}
}

// withLinks returns a spec whose large_resources member serves links, with
// the record's resource map holding each link's content_hash.
func withLinks(rec version.DeliveryRecord, links map[string]link) loadSpec {
	rec.Resources = map[string]string{}
	for path, l := range links {
		rec.Resources[path] = l.hash
	}
	return loadSpec{rec: rec, links: links}
}

// object encodes the spec as the registry serves it, before any case edit.
func (s loadSpec) object(t testing.TB) object {
	r := s.rec
	o := object{}.
		set("id", str(r.ID)).
		set("version", str(r.Version)).
		set("type", str(r.Type)).
		set("content_hash", str(r.ContentHash)).
		set("sensitivity", str(r.Sensitivity)).
		set("artifact_revision", str(r.ArtifactRevision))
	o = s.manifestMembers(o)
	if s.inline != nil {
		if s.b64 {
			o = o.set("resources_base64", "true")
		}
		o = o.set("resources", stringMap(s.inline))
	}
	if s.links != nil {
		links := object{}
		for _, path := range sortedKeys(s.links) {
			links = links.set(path, s.links[path].object())
		}
		o = o.set("large_resources", links.String())
	}
	if s.manifest != nil {
		o = o.set("manifest_body_url", s.manifest.object())
	}
	hash := version.DeliveryHash(r)
	return o.set("delivery_hash", str(hash)).set("delivery_signature", str(signHash(t, hash)))
}

// manifestMembers adds the manifest-document members. A body that carries
// manifest_body_url serves the document by URL, so it omits the member the
// document fills and the manifest body.
func (s loadSpec) manifestMembers(o object) object {
	r := s.rec
	if s.manifest == nil {
		o = o.set("frontmatter", str(r.Frontmatter)).set("manifest_body", str(r.ManifestBody))
		if r.SkillRaw != "" {
			o = o.set("skill_raw", str(r.SkillRaw))
		}
		return o
	}
	if r.Type == "skill" {
		o = o.set("frontmatter", str(r.Frontmatter))
	}
	return o
}

// object encodes a link with the size and content_type members the registry
// serves beside the two the procedure reads, followed by the case's extras.
func (l link) object() string {
	o := object{}.
		set("presigned_url", str(l.url)).
		set("content_hash", str(l.hash)).
		set("size", strconv.Itoa(len(l.body))).
		set("content_type", str("application/octet-stream"))
	return append(o, l.extra...).String()
}

// fetched returns what the object store serves for the spec's links.
func (s loadSpec) fetched() map[string]string {
	out := map[string]string{}
	all := make([]link, 0, len(s.links)+1)
	for _, l := range s.links {
		all = append(all, l)
	}
	if s.manifest != nil {
		all = append(all, *s.manifest)
	}
	for _, l := range all {
		if l.url != "" {
			out[l.url] = b64([]byte(l.body))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// responseVectors builds the responses section and checks each case.
func responseVectors(t *testing.T) []responseVector {
	cases := append(okResponseCases(), refusedResponseCases()...)
	out := make([]responseVector, 0, len(cases))
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, c.vector(t))
		names = append(names, c.name)
	}
	checkUniqueNames(t, "responses", names)
	return sortByName(out, func(v responseVector) string { return v.Name })
}

// vector encodes the case and asserts that Go reaches its declared outcome.
func (c responseCase) vector(t *testing.T) responseVector {
	o := c.spec.object(t)
	if c.edit != nil {
		o = c.edit(o)
	}
	body := []byte(o.String())
	if c.raw != nil {
		body = c.raw(body)
	}
	v := responseVector{Name: c.name, BodyBase64: b64(body), Fetched: c.spec.fetched(), Outcome: codeMismatch}
	rec, hash, err := verifyResponse(body, v.Fetched)
	switch {
	case c.refuse && err == nil:
		t.Errorf("responses/%s: Go accepted a body the case declares refused", c.name)
	case !c.refuse && err != nil:
		t.Errorf("responses/%s: Go refused a body the case declares ok: %v", c.name, err)
	case !c.refuse:
		checkRecord(t, "responses/"+c.name, rec, c.spec.rec)
		v.Outcome, v.ServedHash = outcomeOK, hash
	}
	return v
}

// verifyResponse runs §4.7.10 steps 1 to 7 on a load_artifact body, fetching
// each link from fetched, and returns the verified record and its hash.
func verifyResponse(body []byte, fetched map[string]string) (version.DeliveryRecord, string, error) {
	s, err := version.ParseLoadResponse(body)
	if err != nil {
		return version.DeliveryRecord{}, "", err
	}
	if s.ManifestLink != nil {
		if err := placeManifest(&s, fetched); err != nil {
			return version.DeliveryRecord{}, "", err
		}
	}
	for _, path := range sortedKeys(s.Links) {
		l := s.Links[path]
		doc, err := fetch(fetched, l.URL)
		if err != nil {
			return version.DeliveryRecord{}, "", err
		}
		if err := version.CheckLinked(doc, l.ContentHash); err != nil {
			return version.DeliveryRecord{}, "", fmt.Errorf("resource %q: %w", path, err)
		}
	}
	got := version.DeliveryHash(s.Record)
	if s.Hash == "" || got != s.Hash {
		return version.DeliveryRecord{}, "", fmt.Errorf("delivery hash %s does not match served %q", got, s.Hash)
	}
	return s.Record, got, nil
}

// placeManifest fetches, checks, and places the manifest_body_url document.
func placeManifest(s *version.Served, fetched map[string]string) error {
	doc, err := fetch(fetched, s.ManifestLink.URL)
	if err != nil {
		return err
	}
	if err := version.CheckLinked(doc, s.ManifestLink.ContentHash); err != nil {
		return fmt.Errorf("manifest document: %w", err)
	}
	body, err := manifest.ManifestBodyOf(doc)
	if err != nil {
		return err
	}
	s.PlaceManifestDocument(doc, body)
	return nil
}

// errNotFetched signals a link whose URL the case's object store does not
// serve, which a consumer that fetched a different member than presigned_url
// would reach.
var errNotFetched = errors.New("object store serves no such URL")

// fetch returns the bytes the case's object store serves at url.
func fetch(fetched map[string]string, url string) ([]byte, error) {
	enc, ok := fetched[url]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errNotFetched, url)
	}
	return version.DecodeBase64(enc)
}
