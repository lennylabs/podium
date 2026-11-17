package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/version"
)

const ctxArtifactInternal = "---\n" +
	"type: agent\n" +
	"version: 1.0.0\n" +
	"description: d\n" +
	"sensitivity: low\n" +
	"---\n\nbody\n"

// TestLoadOne_RootLevelRejected covers
// spec: §4.2 — loadOne rejects a root-level ARTIFACT.md (empty canonical ID)
// the same way the filesystem-source registry does, so both ingest paths
// share the invariant that an artifact has an addressable canonical home.
func TestLoadOne_RootLevelRejected(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
	}
	_, err := loadOne(fsys, "ARTIFACT.md", "layer")
	if err == nil {
		t.Fatalf("loadOne accepted a root-level ARTIFACT.md (empty canonical ID)")
	}
	if !strings.Contains(err.Error(), "subdirectory") {
		t.Errorf("error %q missing 'subdirectory'", err)
	}
	// The canonical-ID refusal is a content fault, so the reingest endpoint
	// answers ingest.lint_failed rather than a retryable registry.unavailable.
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Errorf("error %v does not wrap ErrInvalidArtifact", err)
	}
}

// TestLoadOne_AtSegmentRejected covers
// spec: §4.2 — "@" is reserved for the @version/@sha256 reference suffix, so a
// directory name containing "@" is an invalid canonical-ID segment.
func TestLoadOne_AtSegmentRejected(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"finance/pay@v2/ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
	}
	_, err := loadOne(fsys, "finance/pay@v2/ARTIFACT.md", "layer")
	if err == nil {
		t.Fatalf("loadOne accepted '@' in a canonical-ID segment")
	}
	if !strings.Contains(err.Error(), "@") {
		t.Errorf("error %q missing '@'", err)
	}
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Errorf("error %v does not wrap ErrInvalidArtifact", err)
	}
}

// TestLoadOne_NestedArtifactNotCaptured covers
// spec: §4.2/§4.4 — the ingest resource walk stops at a nested artifact
// boundary, so a child artifact's files are not captured as the parent's
// bundled resources.
func TestLoadOne_NestedArtifactNotCaptured(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"outer/ARTIFACT.md":       &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
		"outer/notes.md":          &fstest.MapFile{Data: []byte("outer notes")},
		"outer/inner/ARTIFACT.md": &fstest.MapFile{Data: []byte(ctxArtifactInternal)},
		"outer/inner/data.txt":    &fstest.MapFile{Data: []byte("inner data")},
	}
	rec, err := loadOne(fsys, "outer/ARTIFACT.md", "layer")
	if err != nil {
		t.Fatalf("loadOne: %v", err)
	}
	if _, ok := rec.Resources["notes.md"]; !ok {
		t.Errorf("outer missing its own resource notes.md (got %v)", mapKeys(rec.Resources))
	}
	for k := range rec.Resources {
		if strings.HasPrefix(k, "inner/") {
			t.Errorf("outer captured nested-artifact file %q as a resource", k)
		}
	}
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLoadOne_MalformedFrontmatterIsRefusedPerArtifact covers
// spec: §4.1 — a manifest the parser cannot decode is refused on its own, so
// loadOne returns no error, which would stop the walk of the whole layer.
// It returns a record that carries the unparseable reason, naming the file
// and the parse error and no layer, and that Ingest refuses before any rule
// reads the parsed fields. This covers the ARTIFACT.md that does not parse
// and the type: skill package whose SKILL.md does not parse.
func TestLoadOne_MalformedFrontmatterIsRefusedPerArtifact(t *testing.T) {
	t.Parallel()
	const malformed = "---\n" +
		"type: agent\n" +
		"version: 1.0.0\n" +
		"description: d\n" +
		"weird: [unclosed\n" +
		"---\n\nbody\n"
	const nestedMetadata = "---\nname: s\ndescription: d\nmetadata:\n  author:\n    name: alice\n---\n\nbody\n"
	cases := []struct {
		name, manifestPath, wantFile string
		fsys                         fstest.MapFS
	}{
		{"ARTIFACT.md", "x/y/ARTIFACT.md", "x/y/ARTIFACT.md: ", fstest.MapFS{
			"x/y/ARTIFACT.md": &fstest.MapFile{Data: []byte(malformed)},
		}},
		{"SKILL.md of a skill package", "x/s/ARTIFACT.md", "x/s/SKILL.md: ", fstest.MapFS{
			"x/s/ARTIFACT.md": &fstest.MapFile{Data: []byte(skillArtifactInternal)},
			"x/s/SKILL.md":    &fstest.MapFile{Data: []byte(nestedMetadata)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec, err := loadOne(tc.fsys, tc.manifestPath, "layer")
			if err != nil {
				t.Fatalf("loadOne = %v, want a per-artifact refusal and no error", err)
			}
			constraint, reason, ok := rec.Refusal()
			if !ok || constraint != layer.ManifestUnparseableConstraint {
				t.Fatalf("Refusal() = %q, %q, %v, want manifest_unparseable", constraint, reason, ok)
			}
			if !strings.HasPrefix(reason, tc.wantFile+"manifest: invalid YAML frontmatter") {
				t.Errorf("reason %q does not name the file %q and the parse error", reason, tc.wantFile)
			}
			if strings.Contains(reason, "layer") {
				t.Errorf("reason %q names the layer", reason)
			}
		})
	}
}

const skillArtifactInternal = "---\n" +
	"type: skill\n" +
	"version: 1.0.0\n" +
	"---\n\n"

const skillBodyInternal = "---\n" +
	"name: outer\n" +
	"description: Outer skill\n" +
	"---\n\nBody for outer.\n"

// resourceSetTree is the tree of the parity case that mixes a CRLF body, a
// dot-prefixed file, a dot-prefixed directory with no manifest, a SKILL.md
// below the package root, a nested package, and a dot-prefixed nested
// package.
var resourceSetTree = []testharness.WriteTreeOption{
	{Path: "outer/ARTIFACT.md", Content: skillArtifactInternal},
	{Path: "outer/SKILL.md", Content: skillBodyInternal},
	{Path: "outer/notes.md", Content: "line one\r\nline two\r\n"},
	{Path: "outer/.hidden-note", Content: "hidden note body\n"},
	{Path: "outer/.tooling/config.json", Content: "{\"tool\":\"config\"}\n"},
	{Path: "outer/references/SKILL.md", Content: "reference skill body\n"},
	{Path: "outer/inner/ARTIFACT.md", Content: ctxArtifactInternal},
	{Path: "outer/inner/data.txt", Content: "inner data\n"},
	{Path: "outer/.nested/ARTIFACT.md", Content: ctxArtifactInternal},
	{Path: "outer/.nested/note.txt", Content: "nested note\n"},
}

const plainSkillInternal = "---\n" +
	"name: plain\n" +
	"description: Plain skill\n" +
	"---\n\nBody for plain.\n"

const versionedPlainSkillInternal = "---\n" +
	"name: versioned\n" +
	"description: Versioned plain skill\n" +
	"metadata:\n" +
	"  version: 2.1.0\n" +
	"---\n\nBody for versioned.\n"

const agentArtifactInternal = "---\n" +
	"type: agent\n" +
	"version: 1.0.0\n" +
	"description: d\n" +
	"---\n\nagent body\n"

// nestedMetadataSkillInternal is a SKILL.md whose metadata holds a mapping,
// which §4.3.4 does not allow, so it does not parse.
const nestedMetadataSkillInternal = "---\n" +
	"name: nested\n" +
	"description: Nested metadata\n" +
	"metadata:\n" +
	"  author:\n" +
	"    name: alice\n" +
	"---\n\nBody for nested.\n"

// invalidYAMLArtifactInternal is an ARTIFACT.md whose frontmatter is not
// valid YAML.
const invalidYAMLArtifactInternal = "---\ntype: agent\nweird: [unclosed\n---\n\nbody\n"

// skillReason returns the unparseable-manifest reason for the SKILL.md src at
// file, or the empty string when src parses, which no refusal matches.
func skillReason(file, src string) string {
	if _, err := manifest.ParseSkill([]byte(src)); err != nil {
		return layer.UnparseableReason(file, err)
	}
	return ""
}

// artifactReason returns the unparseable-manifest reason for the ARTIFACT.md
// src at file, or the empty string when src parses.
func artifactReason(file, src string) string {
	if _, err := manifest.ParseArtifact([]byte(src)); err != nil {
		return layer.UnparseableReason(file, err)
	}
	return ""
}

// walkParityPackage is one package a walk parity case expects: its canonical
// ID, its resource set, and its resolved version.
type walkParityPackage struct {
	id              string
	resources       map[string]string
	version         string
	versionAssigned bool
}

// walkParityCase is one on-disk tree both layer walks are run over.
type walkParityCase struct {
	name string
	tree []testharness.WriteTreeOption
	// links creates symbolic links under the temporary directory after the
	// tree is written.
	links func(t *testing.T, dir string)
	// layerRoot is the subdirectory of the temporary directory both walks
	// treat as the layer root; "" is the temporary directory itself.
	layerRoot string
	want      []walkParityPackage
	// wantErr lists substrings both walks' errors carry. A case that sets it
	// also requires the ingest error to wrap ErrInvalidArtifact.
	wantErr []string
	// wantRefused maps the ID of each package both modes refuse under the
	// §4.1 unparseable-manifest rule to the reason both give it.
	wantRefused map[string]string
}

func walkParityCases() []walkParityCase {
	return []walkParityCase{
		{
			name: "SKILL.md alone",
			tree: []testharness.WriteTreeOption{
				{Path: "plain/SKILL.md", Content: plainSkillInternal},
				{Path: "plain/notes.md", Content: "notes\n"},
			},
			want: []walkParityPackage{{id: "plain", resources: map[string]string{"notes.md": "notes\n"}, versionAssigned: true}},
		},
		{
			name: "SKILL.md alone with metadata.version",
			tree: []testharness.WriteTreeOption{{Path: "versioned/SKILL.md", Content: versionedPlainSkillInternal}},
			want: []walkParityPackage{{id: "versioned", resources: map[string]string{}, version: "2.1.0"}},
		},
		{
			name: "ARTIFACT.md alone",
			tree: []testharness.WriteTreeOption{{Path: "ctx/ARTIFACT.md", Content: ctxArtifactInternal}},
			want: []walkParityPackage{{id: "ctx", resources: map[string]string{}, version: "1.0.0"}},
		},
		{
			name: "both files",
			tree: []testharness.WriteTreeOption{
				{Path: "both/ARTIFACT.md", Content: skillArtifactInternal},
				{Path: "both/SKILL.md", Content: skillBodyInternal},
			},
			want: []walkParityPackage{{id: "both", resources: map[string]string{}, version: "1.0.0"}},
		},
		{
			name: "plain skill with a nested SKILL.md",
			tree: []testharness.WriteTreeOption{
				{Path: "plain/SKILL.md", Content: plainSkillInternal},
				{Path: "plain/references/SKILL.md", Content: versionedPlainSkillInternal},
			},
			want: []walkParityPackage{{
				id:              "plain",
				resources:       map[string]string{"references/SKILL.md": versionedPlainSkillInternal},
				versionAssigned: true,
			}},
		},
		{
			// The tree mixes a CRLF body, a dot-prefixed file, a dot-prefixed
			// directory with no manifest, a frontmatter-less SKILL.md below
			// the package root, a nested package, and a dot-prefixed nested
			// package.
			name: "ARTIFACT.md package with a nested frontmatter-less SKILL.md",
			tree: resourceSetTree,
			want: []walkParityPackage{
				{id: "outer", version: "1.0.0", resources: map[string]string{
					"notes.md":             "line one\r\nline two\r\n",
					".hidden-note":         "hidden note body\n",
					".tooling/config.json": "{\"tool\":\"config\"}\n",
					"references/SKILL.md":  "reference skill body\n",
				}},
				{id: "outer/inner", version: "1.0.0", resources: map[string]string{"data.txt": "inner data\n"}},
			},
		},
		{
			name: "SKILL.md under a manifest-less directory",
			tree: []testharness.WriteTreeOption{{Path: "group/x/SKILL.md", Content: plainSkillInternal}},
			want: []walkParityPackage{{id: "group/x", resources: map[string]string{}, versionAssigned: true}},
		},
		{
			name: "plain skill containing a nested ARTIFACT.md package",
			tree: []testharness.WriteTreeOption{
				{Path: "plain/SKILL.md", Content: plainSkillInternal},
				{Path: "plain/notes.md", Content: "notes\n"},
				{Path: "plain/inner/ARTIFACT.md", Content: ctxArtifactInternal},
				{Path: "plain/inner/data.txt", Content: "inner data\n"},
			},
			want: []walkParityPackage{
				{id: "plain", resources: map[string]string{"notes.md": "notes\n"}, versionAssigned: true},
				{id: "plain/inner", resources: map[string]string{"data.txt": "inner data\n"}, version: "1.0.0"},
			},
		},
		{
			name: "dot directory below the layer root",
			tree: []testharness.WriteTreeOption{{Path: ".claude/skills/x/SKILL.md", Content: plainSkillInternal}},
		},
		{
			name:      "dot directory as the layer root",
			tree:      []testharness.WriteTreeOption{{Path: ".claude/skills/x/SKILL.md", Content: plainSkillInternal}},
			layerRoot: ".claude",
			want:      []walkParityPackage{{id: "skills/x", resources: map[string]string{}, versionAssigned: true}},
		},
		{
			name:    "root-level SKILL.md",
			tree:    []testharness.WriteTreeOption{{Path: "SKILL.md", Content: plainSkillInternal}},
			wantErr: []string{"subdirectory", "SKILL.md"},
		},
		{
			name: "SKILL.md symlinks",
			tree: []testharness.WriteTreeOption{
				{Path: "shared/skill.txt", Content: plainSkillInternal},
				{Path: "ydir/notes.md", Content: "notes\n"},
			},
			links: func(t *testing.T, dir string) {
				t.Helper()
				for _, l := range []struct{ target, link string }{
					{filepath.Join("..", "shared", "skill.txt"), filepath.Join(dir, "x", "SKILL.md")},
					{filepath.Join("..", "ydir"), filepath.Join(dir, "y", "SKILL.md")},
				} {
					if err := os.MkdirAll(filepath.Dir(l.link), 0o755); err != nil {
						t.Fatalf("MkdirAll: %v", err)
					}
					if err := os.Symlink(l.target, l.link); err != nil {
						t.Fatalf("Symlink: %v", err)
					}
				}
			},
			want: []walkParityPackage{{id: "x", resources: map[string]string{}, versionAssigned: true}},
		},
		{
			name:        "frontmatter-less SKILL.md outside any package",
			tree:        []testharness.WriteTreeOption{{Path: "loose/SKILL.md", Content: "no frontmatter here\n"}},
			wantRefused: map[string]string{"loose": "loose/SKILL.md: manifest: missing YAML frontmatter"},
		},
		{
			name: "SKILL.md with nested metadata beside a valid skill",
			tree: []testharness.WriteTreeOption{
				{Path: "bad/SKILL.md", Content: nestedMetadataSkillInternal},
				{Path: "good/SKILL.md", Content: plainSkillInternal},
			},
			want:        []walkParityPackage{{id: "good", resources: map[string]string{}, versionAssigned: true}},
			wantRefused: map[string]string{"bad": skillReason("bad/SKILL.md", nestedMetadataSkillInternal)},
		},
		{
			name: "ARTIFACT.md with invalid YAML beside a valid artifact",
			tree: []testharness.WriteTreeOption{
				{Path: "bad/ARTIFACT.md", Content: invalidYAMLArtifactInternal},
				{Path: "ctx/ARTIFACT.md", Content: ctxArtifactInternal},
			},
			want:        []walkParityPackage{{id: "ctx", resources: map[string]string{}, version: "1.0.0"}},
			wantRefused: map[string]string{"bad": artifactReason("bad/ARTIFACT.md", invalidYAMLArtifactInternal)},
		},
		{
			// The refused package still claims its directory under §4.2, so the
			// SKILL.md below it is a bundled file rather than a plain skill.
			name: "unparseable ARTIFACT.md above a SKILL.md",
			tree: []testharness.WriteTreeOption{
				{Path: "bad/ARTIFACT.md", Content: "no frontmatter\n"},
				{Path: "bad/refs/SKILL.md", Content: plainSkillInternal},
			},
			wantRefused: map[string]string{"bad": "bad/ARTIFACT.md: manifest: missing YAML frontmatter"},
		},
		{
			name: "skill ARTIFACT.md whose SKILL.md does not parse",
			tree: []testharness.WriteTreeOption{
				{Path: "both/ARTIFACT.md", Content: skillArtifactInternal},
				{Path: "both/SKILL.md", Content: nestedMetadataSkillInternal},
			},
			wantRefused: map[string]string{"both": skillReason("both/SKILL.md", nestedMetadataSkillInternal)},
		},
		{
			name: "agent ARTIFACT.md beside its root SKILL.md",
			tree: []testharness.WriteTreeOption{
				{Path: "bot/ARTIFACT.md", Content: agentArtifactInternal},
				{Path: "bot/SKILL.md", Content: plainSkillInternal},
			},
			want: []walkParityPackage{{id: "bot", resources: map[string]string{"SKILL.md": plainSkillInternal}, version: "1.0.0"}},
		},
	}
}

// TestWalkLayer_MatchesTheFilesystemWalk covers
// spec: §4.2, §4.4, §4.7.6 — package discovery and the digest's resource set
// each have two implementations, walkLayer here and walkLayer in
// pkg/registry/filesystem, and §4.7.6 requires every party to compute one
// digest for a package. Each case runs both walks over one tree and compares
// the package IDs, the resource sets, the resolved versions, and the digests,
// so a filter, a rename, or a discovery rule added to one walk and not the
// other fails here rather than surfacing as a content_hash disagreement
// between a filesystem-source consumer and the registry.
func TestWalkLayer_MatchesTheFilesystemWalk(t *testing.T) {
	t.Parallel()
	for _, tc := range walkParityCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			testharness.WriteTree(t, dir, tc.tree...)
			if tc.links != nil {
				tc.links(t, dir)
			}
			root := filepath.Join(dir, tc.layerRoot)
			ingestRecs, ingestErr := walkLayer(os.DirFS(root), "L")
			fsRecs, fsDrops, fsErr := filesystemWalk(t, root)
			if len(tc.wantErr) > 0 {
				assertWalkErrors(t, ingestErr, fsErr, tc.wantErr)
				return
			}
			if ingestErr != nil || fsErr != nil {
				t.Fatalf("walk errors: ingest %v, filesystem %v", ingestErr, fsErr)
			}
			kept, refused := refuseInvalidArtifacts(ingestRecs)
			assertRefusalParity(t, tc.wantRefused, refused, fsDrops)
			assertWalkParity(t, tc.want, kept, fsRecs)
		})
	}
}

// filesystemWalk walks root as a single-layer filesystem registry under the
// default collision policy and returns the kept records and the drops the
// walk reported.
func filesystemWalk(t *testing.T, root string) ([]filesystem.ArtifactRecord, []layer.Drop, error) {
	t.Helper()
	reg, err := filesystem.Open(root)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	var drops []layer.Drop
	recs, err := reg.Walk(filesystem.WalkOptions{OnDrop: func(d layer.Drop) { drops = append(drops, d) }})
	return recs, drops, err
}

// assertRefusalParity checks that ingest's rejections and the filesystem
// walk's drops each name exactly the packages in want, with
// ingest.invalid_artifact, the wanted reason, and details that are exactly
// {"constraint": "manifest_unparseable"}.
//
// Spec: §4.1, §13.11.3
func assertRefusalParity(t *testing.T, want map[string]string, refused []RejectedArtifact, drops []layer.Drop) {
	t.Helper()
	if len(refused) != len(want) || len(drops) != len(want) {
		t.Fatalf("refusals: ingest %+v, filesystem %+v, want %v", refused, drops, want)
	}
	wantDetails := map[string]any{"constraint": layer.ManifestUnparseableConstraint}
	for i, r := range refused {
		d := drops[i]
		if want[r.ArtifactID] != r.Reason || r.ArtifactID != d.ArtifactID || r.Reason != d.Reason {
			t.Errorf("refusal %d: ingest %s %q, filesystem %s %q, want reason %q",
				i, r.ArtifactID, r.Reason, d.ArtifactID, d.Reason, want[r.ArtifactID])
		}
		if r.Code != layer.InvalidArtifactCode || d.Code != layer.InvalidArtifactCode {
			t.Errorf("refusal %s codes: ingest %s, filesystem %s", r.ArtifactID, r.Code, d.Code)
		}
		if !reflect.DeepEqual(r.Details, wantDetails) || !reflect.DeepEqual(d.Details, wantDetails) {
			t.Errorf("refusal %s details: ingest %v, filesystem %v, want %v", r.ArtifactID, r.Details, d.Details, wantDetails)
		}
	}
}

func assertWalkErrors(t *testing.T, ingestErr, fsErr error, substrings []string) {
	t.Helper()
	if ingestErr == nil || fsErr == nil {
		t.Fatalf("want both walks to fail: ingest %v, filesystem %v", ingestErr, fsErr)
	}
	for _, sub := range substrings {
		if !strings.Contains(ingestErr.Error(), sub) {
			t.Errorf("ingest error %q missing %q", ingestErr, sub)
		}
		if !strings.Contains(fsErr.Error(), sub) {
			t.Errorf("filesystem error %q missing %q", fsErr, sub)
		}
	}
	if !errors.Is(ingestErr, ErrInvalidArtifact) {
		t.Errorf("ingest error %v does not wrap ErrInvalidArtifact", ingestErr)
	}
}

func assertWalkParity(t *testing.T, want []walkParityPackage, ingestRecs, fsRecs []filesystem.ArtifactRecord) {
	t.Helper()
	if len(ingestRecs) != len(want) || len(fsRecs) != len(want) {
		t.Fatalf("package IDs: ingest %v, filesystem %v, want %d packages",
			recordIDs(ingestRecs), recordIDs(fsRecs), len(want))
	}
	for i, w := range want {
		ing, fsr := ingestRecs[i], fsRecs[i]
		if ing.ID != w.id || fsr.ID != w.id {
			t.Errorf("package %d: ingest %q, filesystem %q, want %q", i, ing.ID, fsr.ID, w.id)
			continue
		}
		assertResourceSet(t, "ingest walk "+w.id, ing.Resources, w.resources)
		assertResourceSet(t, "filesystem walk "+w.id, fsr.Resources, w.resources)
		// A filesystem source reports 0.0.0-auto.0 for an assigned version
		// (§13.11.3), while ingest leaves it empty until it derives N from
		// the stored rows (§7.3.1).
		fsVersion := w.version
		if w.versionAssigned {
			fsVersion = manifest.AutoVersion(0)
		}
		if ing.Artifact.Version != w.version || fsr.Artifact.Version != fsVersion {
			t.Errorf("%s version: ingest %q, filesystem %q, want %q and %q",
				w.id, ing.Artifact.Version, fsr.Artifact.Version, w.version, fsVersion)
		}
		if ing.VersionAssigned != w.versionAssigned || fsr.VersionAssigned != w.versionAssigned {
			t.Errorf("%s VersionAssigned: ingest %v, filesystem %v, want %v",
				w.id, ing.VersionAssigned, fsr.VersionAssigned, w.versionAssigned)
		}
		fsHash := version.CanonicalContentHash(fsr.AuthoredBytes, fsr.SkillBytes, fsr.Resources)
		if got := contentHashOf(ing); got != fsHash {
			t.Errorf("%s: ingest content hash %s, filesystem walk %s: the two walks disagree on the §4.7.6 digest", w.id, got, fsHash)
		}
	}
}

func recordIDs(recs []filesystem.ArtifactRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

// TestWalkLayer_DigestInputs covers
// spec: §4.7.6 — the digest of a package with both files is taken over the
// authored ARTIFACT.md, SKILL.md, and resources, and the digest of a skill
// with no ARTIFACT.md frames a zero-length first value.
func TestWalkLayer_DigestInputs(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"both/ARTIFACT.md": &fstest.MapFile{Data: []byte(skillArtifactInternal)},
		"both/SKILL.md":    &fstest.MapFile{Data: []byte(skillBodyInternal)},
		"both/notes.md":    &fstest.MapFile{Data: []byte("notes\n")},
		"plain/SKILL.md":   &fstest.MapFile{Data: []byte(plainSkillInternal)},
		"plain/notes.md":   &fstest.MapFile{Data: []byte("notes\n")},
	}
	recs, err := walkLayer(fsys, "L")
	if err != nil {
		t.Fatalf("walkLayer: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("walkLayer IDs = %v, want both and plain", recordIDs(recs))
	}
	resources := map[string][]byte{"notes.md": []byte("notes\n")}
	wantBoth := version.CanonicalContentHash([]byte(skillArtifactInternal), []byte(skillBodyInternal), resources)
	if got := contentHashOf(recs[0]); got != wantBoth {
		t.Errorf("both-files digest %s, want %s", got, wantBoth)
	}
	plain := recs[1]
	if plain.ArtifactBytes != nil || plain.AuthoredBytes != nil {
		t.Errorf("plain skill ARTIFACT.md bytes = %q / %q, want nil", plain.ArtifactBytes, plain.AuthoredBytes)
	}
	if plain.Artifact.Type != manifest.TypeSkill {
		t.Errorf("plain skill type = %q, want skill", plain.Artifact.Type)
	}
	wantPlain := version.CanonicalContentHash([]byte{}, []byte(plainSkillInternal), resources)
	if got := contentHashOf(plain); got != wantPlain {
		t.Errorf("plain-skill digest %s, want %s over a zero-length first value", got, wantPlain)
	}
}

func assertResourceSet(t *testing.T, who string, got map[string][]byte, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s resource keys = %v, want %d keys", who, mapKeys(got), len(want))
	}
	for k, v := range want {
		body, ok := got[k]
		if !ok {
			t.Errorf("%s missing resource %q (got %v)", who, k, mapKeys(got))
			continue
		}
		if string(body) != v {
			t.Errorf("%s resource %q = %q, want %q", who, k, body, v)
		}
	}
}
