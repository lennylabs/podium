package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"gopkg.in/yaml.v3"
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
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		fmt.Println(string(body))
		return
	}
	out := readArtifactShow{
		ID:          wire.ID,
		Version:     wire.Version,
		ContentHash: wire.ContentHash,
		Frontmatter: parseFrontmatterObject(wire.Frontmatter),
		Body:        wire.ManifestBody,
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
	fmt.Fprintln(os.Stdout, string(b))
}
