package main

import (
	"encoding/json"
	"testing"
)

// spec: §7.6.1 — `podium search --json` emits each result as
// {id, type, version, score, frontmatter} with frontmatter as a parsed object,
// not the raw ---fenced--- string. Regression for the descriptor-name gap.
func TestReadCLI_SearchJSONSchema(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/search_artifacts": map[string]any{
			"query":         "variance",
			"total_matched": 3,
			"results": []map[string]any{
				{
					"id":          "team/variance-helper",
					"type":        "skill",
					"version":     "1.0.0",
					"score":       0.83,
					"description": "compute variance",
					"frontmatter": "---\ntype: skill\nname: variance-helper\ndescription: compute variance\n---\n",
				},
			},
		},
	})
	out := captureStdout(t, func() {
		if rc := searchCmd([]string{"--registry", ts.URL, "--json", "variance"}); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	var got struct {
		Query        string `json:"query"`
		TotalMatched int    `json:"total_matched"`
		Results      []struct {
			ID          string         `json:"id"`
			Type        string         `json:"type"`
			Version     string         `json:"version"`
			Score       float64        `json:"score"`
			Frontmatter map[string]any `json:"frontmatter"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json output not valid: %v\n%s", err, out)
	}
	if got.TotalMatched != 3 {
		t.Errorf("total_matched = %d, want 3", got.TotalMatched)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(got.Results))
	}
	r := got.Results[0]
	if r.ID != "team/variance-helper" || r.Type != "skill" || r.Version != "1.0.0" {
		t.Errorf("scalar fields wrong: %+v", r)
	}
	if r.Score != 0.83 {
		t.Errorf("score = %v, want 0.83", r.Score)
	}
	// Frontmatter is an object, with the description (the §3.2 `summary` role)
	// reachable as .frontmatter.description.
	if r.Frontmatter["description"] != "compute variance" {
		t.Errorf("frontmatter.description = %v, want %q", r.Frontmatter["description"], "compute variance")
	}
	if r.Frontmatter["name"] != "variance-helper" {
		t.Errorf("frontmatter.name = %v", r.Frontmatter["name"])
	}
}

// spec: §7.6.1 — `podium domain search --json` keys the ranked domains under
// "results", matching the artifact-search envelope. The wire response keys
// them "domains" (F-7.6.1).
func TestReadCLI_DomainSearchJSONUsesResultsKey(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/search_domains": map[string]any{
			"query":         "payments",
			"total_matched": 2,
			"domains": []map[string]any{
				{
					"path":        "finance/ap",
					"name":        "Accounts Payable",
					"description": "vendor payments",
					"keywords":    []string{"invoice", "vendor"},
					"score":       0.87,
				},
			},
		},
	})
	out := captureStdout(t, func() {
		if rc := domainSearch([]string{"--registry", ts.URL, "--json", "payments"}); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json output not valid: %v\n%s", err, out)
	}
	if _, ok := raw["results"]; !ok {
		t.Errorf("missing documented `results` key: %s", out)
	}
	if _, ok := raw["domains"]; ok {
		t.Errorf("emitted wire `domains` key instead of `results`: %s", out)
	}
	var got struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			Path        string   `json:"path"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Keywords    []string `json:"keywords"`
			Score       float64  `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TotalMatched != 2 || len(got.Results) != 1 {
		t.Fatalf("envelope wrong: total=%d results=%d", got.TotalMatched, len(got.Results))
	}
	d := got.Results[0]
	if d.Path != "finance/ap" || d.Name != "Accounts Payable" || d.Description != "vendor payments" {
		t.Errorf("domain fields wrong: %+v", d)
	}
	if len(d.Keywords) != 2 || d.Score != 0.87 {
		t.Errorf("keywords/score wrong: %+v", d)
	}
}

// spec: §7.6.1 — `podium artifact show --json` emits {id, version,
// content_hash, frontmatter, body}. The wire response keys the manifest text
// "manifest_body" and delivers frontmatter as a string (F-7.6.2).
func TestReadCLI_ArtifactShowJSONSchema(t *testing.T) {
	ts := newRegistryStub(t, map[string]any{
		"/v1/load_artifact": map[string]any{
			"id":            "team/x",
			"type":          "skill",
			"version":       "1.2.0",
			"content_hash":  "sha256:abc",
			"manifest_body": "Skill body content.",
			"frontmatter":   "---\ntype: skill\nname: x\n---\n",
		},
	})
	out := captureStdout(t, func() {
		if rc := artifactShow([]string{"--registry", ts.URL, "--json", "team/x"}); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("json output not valid: %v\n%s", err, out)
	}
	if _, ok := raw["manifest_body"]; ok {
		t.Errorf("emitted wire `manifest_body` instead of documented `body`: %s", out)
	}
	var got struct {
		ID          string         `json:"id"`
		Version     string         `json:"version"`
		ContentHash string         `json:"content_hash"`
		Frontmatter map[string]any `json:"frontmatter"`
		Body        string         `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "team/x" || got.Version != "1.2.0" || got.ContentHash != "sha256:abc" {
		t.Errorf("scalar fields wrong: %+v", got)
	}
	if got.Body != "Skill body content." {
		t.Errorf("body = %q, want the manifest_body content", got.Body)
	}
	if got.Frontmatter["type"] != "skill" || got.Frontmatter["name"] != "x" {
		t.Errorf("frontmatter object wrong: %+v", got.Frontmatter)
	}
}

// spec: §7.6.1 — an empty or absent frontmatter string yields an empty object
// so the documented key is always present and a `jq` path into it never errors.
func TestParseFrontmatterObject_EmptyAndMalformed(t *testing.T) {
	for _, in := range []string{"", "   ", "not: [valid", "no frontmatter fences here"} {
		obj := parseFrontmatterObject(in)
		if obj == nil {
			t.Errorf("parseFrontmatterObject(%q) = nil, want non-nil map", in)
		}
	}
	// A fenced block decodes its inner YAML.
	obj := parseFrontmatterObject("---\ntype: rule\nversion: 2.0.0\n---\n")
	if obj["type"] != "rule" || obj["version"] != "2.0.0" {
		t.Errorf("fenced frontmatter not decoded: %+v", obj)
	}
	// A bare YAML body (no fences) still decodes.
	bare := parseFrontmatterObject("type: hook\n")
	if bare["type"] != "hook" {
		t.Errorf("bare frontmatter not decoded: %+v", bare)
	}
}
