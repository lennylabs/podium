// Package projection builds the canonical §4.7 text projections that the
// registry embeds. It is the single implementation shared by the ingest
// write path and the `podium admin reembed` path, so a managed vector
// backend and a collocated one index identical text for the same record.
package projection

import (
	"strings"

	"github.com/lennylabs/podium/pkg/store"
)

// Artifact is the canonical §4.7 embedding-input projection for an
// artifact, built from the stored record's frontmatter fields only: name,
// description, when_to_use (joined with newlines), and tags (joined with
// spaces). The record's columns are already §4.6-resolved at ingest, so a
// child that declares extends: is indexed under the description and tags
// its parent supplies.
//
// The prose body is deliberately excluded ("The prose body is not
// embedded"): it is noisy for retrieval and risks busting embedding-model
// context limits. Authors influence recall through description and
// when_to_use.
//
// An empty part contributes no line, and an empty entry inside WhenToUse
// or Tags is dropped, so the projection never carries a stray separator
// that shifts the embedding for a record holding a blank list entry.
//
// Spec: §4.7 "Artifact embeddings".
func Artifact(mr store.ManifestRecord) string {
	parts := []string{mr.Name, mr.Description}
	if len(mr.WhenToUse) > 0 {
		parts = append(parts, joinNonEmpty(mr.WhenToUse, "\n"))
	}
	if len(mr.Tags) > 0 {
		parts = append(parts, joinNonEmpty(mr.Tags, " "))
	}
	return joinNonEmpty(parts, "\n")
}

// joinNonEmpty joins the non-empty parts with sep.
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
