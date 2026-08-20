package projection_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/projection"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7 "Artifact embeddings" — the projection is name, description,
// when_to_use (joined with newlines), and tags (joined with spaces), built
// from the record's frontmatter columns. Empty parts and empty list
// entries are dropped so the input carries no stray separator, and the
// prose body is excluded.
func TestArtifact_Projection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rec  store.ManifestRecord
		want string
	}{
		{
			name: "name and description only",
			rec:  store.ManifestRecord{Name: "Pay Invoice", Description: "settles an AP invoice", Body: []byte("ignored prose")},
			want: "Pay Invoice\nsettles an AP invoice",
		},
		{
			name: "when_to_use joined with newlines",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"first", "second"}},
			want: "n\nd\nfirst\nsecond",
		},
		{
			name: "tags joined with spaces",
			rec:  store.ManifestRecord{Name: "n", Description: "d", Tags: []string{"finance", "ap"}},
			want: "n\nd\nfinance ap",
		},
		{
			name: "all components present",
			rec: store.ManifestRecord{
				Name:        "run-variance-analysis",
				Description: "flag unusual variance",
				WhenToUse:   []string{"after month-end close", "before board review"},
				Tags:        []string{"finance", "variance"},
				Body:        []byte("SECRET BODY PROSE that must not be embedded"),
			},
			want: "run-variance-analysis\nflag unusual variance\nafter month-end close\nbefore board review\nfinance variance",
		},
		{
			name: "empty name skipped, no leading separator",
			rec:  store.ManifestRecord{Description: "only desc"},
			want: "only desc",
		},
		{
			name: "empty description leaves no blank line",
			rec:  store.ManifestRecord{ArtifactID: "finance/x", Name: "x", Tags: []string{"t"}},
			want: "x\nt",
		},
		{
			name: "interior empty when_to_use entries dropped",
			rec:  store.ManifestRecord{Name: "n", Description: "d", WhenToUse: []string{"", "kept", ""}},
			want: "n\nd\nkept",
		},
		{
			name: "interior empty tag entries dropped",
			rec:  store.ManifestRecord{Name: "n", Description: "d", Tags: []string{"", "finance", ""}},
			want: "n\nd\nfinance",
		},
		{
			name: "fully empty record yields empty string",
			rec:  store.ManifestRecord{Body: []byte("body is never embedded")},
			want: "",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := projection.Artifact(c.rec)
			if got != c.want {
				t.Fatalf("Artifact() = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "SECRET BODY") {
				t.Errorf("projection must not embed the prose body; got %q", got)
			}
			if c.rec.ArtifactID != "" && strings.Contains(got, c.rec.ArtifactID) {
				t.Errorf("artifact id must not appear in the projection; got %q", got)
			}
		})
	}
}

// Spec: §4.7 "Artifact embeddings" and §4.7 "Dual-write semantics for
// external vector backends" — the ingest write path (embedAndStore and the
// outbox enqueue in commitManifest) and `podium admin reembed`
// (Registry.ReembedOne) must embed identical text for one record, or a
// managed backend and a collocated one index the same artifact
// differently. Both call sites take this function, so the guarantee is
// that the projection is a pure function of the record.
//
// The assertion is over one store.ManifestRecord value rather than over a
// store round-trip on purpose: neither SQL store persists Name or
// WhenToUse, so a record read back from SQLite or Postgres projects less
// text than the one ingest holds. That divergence predates this change,
// and collapsing the two copies does not close it.
func TestProjection_IngestAndReembedAgree(t *testing.T) {
	t.Parallel()
	rec := store.ManifestRecord{
		TenantID:    "acme",
		ArtifactID:  "finance/reconcile",
		Version:     "1.0.0",
		Name:        "reconcile-invoices",
		Description: "matches vendor payments against purchase orders",
		WhenToUse:   []string{"at month-end close", "", "before an audit"},
		Tags:        []string{"finance", "", "ap"},
		Body:        []byte("prose that is never embedded"),
	}

	// The text ingest embeds inline, the text ingest enqueues on the
	// outbox row, and the text reembed upserts.
	inline := projection.Artifact(rec)
	outbox := projection.Artifact(rec)
	reembed := projection.Artifact(rec)

	if inline != outbox {
		t.Errorf("ingest inline text %q != outbox text %q", inline, outbox)
	}
	if inline != reembed {
		t.Errorf("ingest text %q != reembed text %q", inline, reembed)
	}
	const want = "reconcile-invoices\nmatches vendor payments against purchase orders\nat month-end close\nbefore an audit\nfinance ap"
	if inline != want {
		t.Errorf("Artifact() = %q, want %q", inline, want)
	}
}
