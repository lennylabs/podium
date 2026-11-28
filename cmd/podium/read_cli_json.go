package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// The read CLI's --json output follows the §7.6.1 documented schemas, which
// differ from the raw registry wire response: search and domain-search results
// both live under a "results" key, artifact frontmatter is a parsed object
// (not the raw ---fenced--- string), and an artifact's manifest text is keyed
// "body" (the wire calls it "manifest_body"). These mappers translate each
// raw response into the documented envelope so a `jq` pipeline written against
// the spec reads the keys it expects. On a decode failure each falls back to
// printing the raw body so output is never lost.

// readSearchResult is one §7.6.1 `podium search --json` result:
// {id, type, version, score, frontmatter}. The descriptive summary the
// §3.2 sketch labels `summary` lives inside the frontmatter object's
// `description` field.
type readSearchResult struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Version     string         `json:"version"`
	Score       float64        `json:"score"`
	Frontmatter map[string]any `json:"frontmatter"`
}

// readSearchEnvelope is the §7.6.1 `podium search --json` envelope.
type readSearchEnvelope struct {
	Query        string             `json:"query"`
	TotalMatched int                `json:"total_matched"`
	Results      []readSearchResult `json:"results"`
}

// emitSearchJSON maps the /v1/search_artifacts wire response to the §7.6.1
// `podium search --json` schema. The wire descriptor carries the frontmatter
// as a raw string; the documented schema delivers it as an object.
func emitSearchJSON(body []byte) {
	var wire struct {
		Query        string `json:"query"`
		TotalMatched int    `json:"total_matched"`
		Results      []struct {
			ID          string  `json:"id"`
			Type        string  `json:"type"`
			Version     string  `json:"version"`
			Score       float64 `json:"score"`
			Frontmatter string  `json:"frontmatter"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		fmt.Println(string(body))
		return
	}
	out := readSearchEnvelope{
		Query:        wire.Query,
		TotalMatched: wire.TotalMatched,
		Results:      []readSearchResult{},
	}
	for _, r := range wire.Results {
		out.Results = append(out.Results, readSearchResult{
			ID:          r.ID,
			Type:        r.Type,
			Version:     r.Version,
			Score:       r.Score,
			Frontmatter: parseFrontmatterObject(r.Frontmatter),
		})
	}
	printReadJSON(out, body)
}

// readDomainResult is one §7.6.1 `podium domain search --json` result:
// {path, name, description, keywords, score}.
type readDomainResult struct {
	Path        string   `json:"path"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Score       float64  `json:"score"`
}

// readDomainSearchEnvelope is the §7.6.1 `podium domain search --json`
// envelope. The wire response keys the entries "domains"; the documented
// schema (and the artifact-search envelope) uses "results".
type readDomainSearchEnvelope struct {
	Query        string             `json:"query"`
	TotalMatched int                `json:"total_matched"`
	Results      []readDomainResult `json:"results"`
}

// emitDomainSearchJSON maps the /v1/search_domains wire response (which serves
// the entries under "domains") to the §7.6.1 `podium domain search --json`
// schema, which serves them under "results".
func emitDomainSearchJSON(body []byte) {
	var wire struct {
		Query        string `json:"query"`
		TotalMatched int    `json:"total_matched"`
		Domains      []struct {
			Path        string   `json:"path"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Keywords    []string `json:"keywords"`
			Score       float64  `json:"score"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		fmt.Println(string(body))
		return
	}
	out := readDomainSearchEnvelope{
		Query:        wire.Query,
		TotalMatched: wire.TotalMatched,
		Results:      []readDomainResult{},
	}
	for _, d := range wire.Domains {
		out.Results = append(out.Results, readDomainResult{
			Path:        d.Path,
			Name:        d.Name,
			Description: d.Description,
			Keywords:    emptyIfNil(d.Keywords),
			Score:       d.Score,
		})
	}
	printReadJSON(out, body)
}

// readArtifactShow is the §7.6.1 `podium artifact show --json` envelope:
// {id, version, content_hash, frontmatter, body}. The wire response keys the
// manifest text "manifest_body" and delivers frontmatter as a raw string.
type readArtifactShow struct {
	ID          string         `json:"id"`
	Version     string         `json:"version"`
	ContentHash string         `json:"content_hash"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
	// Deprecated, ReplacedBy, and DeprecationWarning are the §7.2 deprecation
	// members, each omitted when the registry omits it (§7.6.1).
	Deprecated         bool   `json:"deprecated,omitempty"`
	ReplacedBy         string `json:"replaced_by,omitempty"`
	DeprecationWarning string `json:"deprecation_warning,omitempty"`
	// PackageDigest is the §7.2 package_digest member, the §4.7.6 package
	// digest the registry stores for the served version, omitted when the
	// registry omits it (§7.6.1).
	PackageDigest string `json:"package_digest,omitempty"`
	// Attestations and AttestationOutcome are the §7.2 attestation members,
	// each omitted when the registry omits it (§7.6.1). They are carried as
	// the registry served them.
	Attestations       json.RawMessage `json:"attestations,omitempty"`
	AttestationOutcome json.RawMessage `json:"attestation_outcome,omitempty"`
}

// packageDigestMember is the §7.2 package_digest member of a load_artifact
// response.
type packageDigestMember struct {
	PackageDigest string `json:"package_digest"`
}

// attestationMembers are the §7.2 attestation members of a load_artifact
// response.
type attestationMembers struct {
	Attestations       json.RawMessage `json:"attestations"`
	AttestationOutcome json.RawMessage `json:"attestation_outcome"`
}

// attestationLine is the part of an attestations element the default output
// prints.
type attestationLine struct {
	Digest        string `json:"digest"`
	PredicateType string `json:"predicate_type"`
	Verdict       string `json:"verdict"`
	Signer        *struct {
		Instance string `json:"instance"`
		Issuer   string `json:"issuer"`
		Identity string `json:"identity"`
	} `json:"signer"`
	AttestedAt string `json:"attested_at"`
}

// outcomeLine is the part of an attestation_outcome member the default
// output prints.
type outcomeLine struct {
	Outcome string `json:"outcome"`
	Failure *struct {
		Code        string `json:"code"`
		Scope       string `json:"scope"`
		Constraint  string `json:"constraint"`
		Requirement *int   `json:"requirement"`
	} `json:"failure"`
	EvaluatedAt string `json:"evaluated_at"`
}

// printAttestations prints the attestation members a load_artifact response
// carries to w, which is standard error in the default output: one line for
// the outcome and one for each link. The registry asserts both members, and
// the read CLI runs no check on them. Each string is printed between
// quotation marks with its control characters escaped, because the issuer
// and the identity come from a certificate.
//
// Spec: §7.6.1, §7.2 (Attestation members)
func printAttestations(w io.Writer, body []byte) {
	var m attestationMembers
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	var o outcomeLine
	if len(m.AttestationOutcome) > 0 && json.Unmarshal(m.AttestationOutcome, &o) == nil && o.Outcome != "" {
		line := "attestation_outcome: " + quoteReported(o.Outcome)
		if f := o.Failure; f != nil {
			line += " code=" + quoteReported(f.Code) + " scope=" + quoteReported(f.Scope)
			if f.Constraint != "" {
				line += " constraint=" + quoteReported(f.Constraint)
			}
			if f.Requirement != nil {
				line += fmt.Sprintf(" requirement=%d", *f.Requirement)
			}
		}
		if o.EvaluatedAt != "" {
			line += " evaluated_at=" + quoteReported(o.EvaluatedAt)
		}
		_, _ = fmt.Fprintln(w, line)
	}
	var links []attestationLine
	if len(m.Attestations) == 0 || json.Unmarshal(m.Attestations, &links) != nil {
		return
	}
	for _, l := range links {
		line := "attestation: " + quoteReported(l.Digest) + " " + quoteReported(l.Verdict) + " predicate_type=" + quoteReported(l.PredicateType)
		if s := l.Signer; s != nil {
			line += " instance=" + quoteReported(s.Instance) + " issuer=" + quoteReported(s.Issuer) + " identity=" + quoteReported(s.Identity)
		}
		if l.AttestedAt != "" {
			line += " attested_at=" + quoteReported(l.AttestedAt)
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// nullAsAbsent returns nil for an absent or null member, so the envelope
// omits it as the registry did.
func nullAsAbsent(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// printPackageDigest prints the package digest a load_artifact response
// carries to w, which is standard error in the default output, so standard
// output keeps the manifest document alone. Under --json the envelope carries
// the member and nothing is printed. A response without one prints nothing. The
// registry asserts the value, and the read CLI runs no check on it.
//
// Spec: §7.6.1, §7.2
func printPackageDigest(w io.Writer, body []byte) {
	var m packageDigestMember
	if err := json.Unmarshal(body, &m); err != nil || m.PackageDigest == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "package_digest: %s\n", m.PackageDigest)
}

// deprecationMembers are the §7.2 deprecation members of a load_artifact
// response.
type deprecationMembers struct {
	Deprecated         bool   `json:"deprecated"`
	ReplacedBy         string `json:"replaced_by"`
	DeprecationWarning string `json:"deprecation_warning"`
}

// warnDeprecation prints the §4.7.4 deprecation warning a load_artifact
// response carries to w, so it reaches the user on standard error whatever
// standard output carries. A response without one prints nothing.
//
// Spec: §7.6.1, §4.7.4
func warnDeprecation(w io.Writer, body []byte) {
	var m deprecationMembers
	if err := json.Unmarshal(body, &m); err != nil || m.DeprecationWarning == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "warning: %s\n", m.DeprecationWarning)
}

// emitArtifactShowJSON maps the /v1/load_artifact wire response to the §7.6.1
// `podium artifact show --json` schema: manifest_body becomes body and the
// frontmatter string becomes an object.
func emitArtifactShowJSON(body []byte) {
	var wire struct {
		ID           string `json:"id"`
		Version      string `json:"version"`
		ContentHash  string `json:"content_hash"`
		ManifestBody string `json:"manifest_body"`
		Frontmatter  string `json:"frontmatter"`
		deprecationMembers
		packageDigestMember
		attestationMembers
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		fmt.Println(string(body))
		return
	}
	out := readArtifactShow{
		ID:                 wire.ID,
		Version:            wire.Version,
		ContentHash:        wire.ContentHash,
		Frontmatter:        parseFrontmatterObject(wire.Frontmatter),
		Body:               wire.ManifestBody,
		Deprecated:         wire.Deprecated,
		ReplacedBy:         wire.ReplacedBy,
		DeprecationWarning: wire.DeprecationWarning,
		PackageDigest:      wire.PackageDigest,
		Attestations:       nullAsAbsent(wire.Attestations),
		AttestationOutcome: nullAsAbsent(wire.AttestationOutcome),
	}
	printReadJSON(out, body)
}

// parseFrontmatterObject decodes the wire frontmatter string into the object
// the §7.6.1 schema documents ({ ... }). The wire string is the raw
// ARTIFACT.md frontmatter, normally fenced by --- lines; the fences are
// stripped before the YAML body is decoded. An empty or unparseable
// frontmatter yields an empty object so the key is always present and a `jq`
// path into it never errors.
func parseFrontmatterObject(fm string) map[string]any {
	if strings.TrimSpace(fm) == "" {
		return map[string]any{}
	}
	src := []byte(fm)
	if inner, _, err := manifest.SplitFrontmatter(src); err == nil {
		src = inner
	}
	var obj map[string]any
	if err := yaml.Unmarshal(src, &obj); err != nil || obj == nil {
		return map[string]any{}
	}
	return obj
}

// printReadJSON emits the mapped envelope as indented JSON. On a marshal
// failure it falls back to the raw wire body so output is never lost.
func printReadJSON(v any, raw []byte) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Println(string(raw))
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, string(b))
}
