package main

import (
	"math"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// localSearchResult is one MCP-side BM25 hit against the
// workspace overlay. Score is in BM25 units (positive, larger
// means a stronger match).
type localSearchResult struct {
	ID          string
	Type        string
	Version     string
	Description string
	Tags        []string
	Score       float64
}

// localSearch runs §6.4.1 BM25 over the workspace-overlay records
// that match the optional type / scope filters. Returns up to
// topK results sorted by score descending; alphabetical fallback
// for empty queries so the order stays deterministic.
//
// The implementation mirrors the registry's BM25 (Robertson +
// Spärck-Jones, k1=1.5, b=0.75) so the fused ranks via RRF are
// comparable across the two streams.
func localSearch(records []filesystem.ArtifactRecord, query, typeFilter, scope string, tags []string, topK int) []localSearchResult {
	if topK <= 0 {
		topK = 10
	}
	docs := make([][]string, 0, len(records))
	keep := make([]filesystem.ArtifactRecord, 0, len(records))
	for _, rec := range records {
		if rec.Artifact == nil {
			continue
		}
		if typeFilter != "" && string(rec.Artifact.Type) != typeFilter {
			continue
		}
		if scope != "" && !strings.HasPrefix(rec.ID, scope) {
			continue
		}
		if !overlayTagsMatch(rec.Artifact.Tags, tags) {
			continue
		}
		docs = append(docs, overlayTokens(rec))
		keep = append(keep, rec)
	}
	if len(keep) == 0 {
		return nil
	}
	if query == "" {
		out := make([]localSearchResult, 0, len(keep))
		for _, rec := range keep {
			out = append(out, descriptorFor(rec))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		if len(out) > topK {
			out = out[:topK]
		}
		return out
	}
	scores := bm25Scores(docs, strings.ToLower(query))
	out := make([]localSearchResult, 0, len(keep))
	for i, rec := range keep {
		if scores[i] <= 0 {
			continue
		}
		entry := descriptorFor(rec)
		entry.Score = scores[i]
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

func descriptorFor(rec filesystem.ArtifactRecord) localSearchResult {
	out := localSearchResult{
		ID: rec.ID,
	}
	if rec.Artifact != nil {
		out.Type = string(rec.Artifact.Type)
		out.Version = rec.Artifact.Version
		out.Description = rec.Artifact.Description
		out.Tags = rec.Artifact.Tags
	}
	return out
}

// overlayTokens returns the BM25 doc bag for an overlay record:
// id segments, description, tag list, and the manifest body.
func overlayTokens(rec filesystem.ArtifactRecord) []string {
	parts := []string{strings.ReplaceAll(rec.ID, "/", " ")}
	if rec.Artifact != nil {
		parts = append(parts, rec.Artifact.Name)
		parts = append(parts, rec.Artifact.Description)
		parts = append(parts, strings.Join(rec.Artifact.Tags, " "))
		for _, w := range rec.Artifact.WhenToUse {
			parts = append(parts, w)
		}
	}
	parts = append(parts, string(rec.SkillBytes))
	parts = append(parts, manifestBodyOf(rec))
	return tokenizeOverlay(strings.ToLower(strings.Join(parts, " ")))
}

// manifestBodyOf returns ARTIFACT.md body bytes minus the YAML
// frontmatter so BM25 sees prose, not duplicated structured fields.
func manifestBodyOf(rec filesystem.ArtifactRecord) string {
	if len(rec.ArtifactBytes) == 0 {
		return ""
	}
	src := string(rec.ArtifactBytes)
	if !strings.HasPrefix(src, "---") {
		return src
	}
	idx := strings.Index(src[3:], "\n---")
	if idx < 0 {
		return src
	}
	return src[3+idx+len("\n---"):]
}

// tokenizeOverlay strips punctuation and splits on whitespace.
func tokenizeOverlay(s string) []string {
	out := []string{}
	cur := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// bm25Scores returns one score per doc for query. Standard BM25
// with k1=1.5, b=0.75; documents with no query term hits return 0.
func bm25Scores(docs [][]string, query string) []float64 {
	const (
		k1 = 1.5
		b  = 0.75
	)
	N := len(docs)
	if N == 0 {
		return nil
	}
	totalLen := 0
	for _, doc := range docs {
		totalLen += len(doc)
	}
	avgLen := float64(totalLen) / float64(N)

	df := map[string]int{}
	for _, doc := range docs {
		seen := map[string]bool{}
		for _, t := range doc {
			if seen[t] {
				continue
			}
			seen[t] = true
			df[t]++
		}
	}

	queryTerms := tokenizeOverlay(query)
	scores := make([]float64, N)
	for i, doc := range docs {
		tf := map[string]int{}
		for _, t := range doc {
			tf[t]++
		}
		score := 0.0
		for _, qt := range queryTerms {
			f := float64(tf[qt])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (float64(N)-float64(df[qt])+0.5)/(float64(df[qt])+0.5))
			docLen := float64(len(doc))
			norm := f * (k1 + 1) / (f + k1*(1-b+b*docLen/avgLen))
			score += idf * norm
		}
		scores[i] = score
	}
	return scores
}

// overlayTagsMatch is a small helper duplicated from the
// registry's tagsMatch. Returns true when every required tag
// appears in the artifact's tag list.
func overlayTagsMatch(have []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, r := range required {
		if !set[r] {
			return false
		}
	}
	return true
}
