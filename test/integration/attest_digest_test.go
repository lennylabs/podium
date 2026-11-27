package integration

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// corpusDir returns the committed attestation corpus layer.
func corpusDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "registries", "attest-corpus")
}

// corpusPackage is one package of the corpus with its pinned subject name,
// resolution, and package digest. omit names the root files the §4.7.6 entry
// rule leaves out, which is a zero-length skill document. lintFails marks a
// resolved package that ingest lint refuses, so no stored row exists to admit:
// a version outside major.minor.patch, and an overlay value that differs
// from its metadata value.
type corpusPackage struct {
	id        string
	subject   string
	resolved  bool
	lintFails bool
	fields    manifest.ResolvedFields
	omit      []string
	digest    string
}

// corpusPackages pins every package of the corpus. A change to discovery,
// to the resolution, or to the serialization moves a value here and fails
// the build, as §4.7.6 *Format stability* requires.
var corpusPackages = []corpusPackage{
	{id: "finance/ap/pay-invoice", subject: "finance/ap/pay-invoice@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityMedium},
		digest: "sha256:9efa9ef128d5dcf99a69afa8b45d29a0291c8b7edd0fa666666e72f7cd5b2d14"},
	{id: "finance/close/run-variance", subject: "finance/close/run-variance@2.1.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeSkill, Sensitivity: manifest.SensitivityHigh, Deprecated: true, ReplacedBy: "finance/close/run-variance-v2"},
		digest: "sha256:2c89d87eb98cd86ed6b2f50d6e0c96b9629b0f165494bb2014746428d19c6335"},
	{id: "forms/anchor", subject: "forms/anchor@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityHigh},
		digest: "sha256:13b511546eef066336ad70441dd51bc3592460aeb17d5a79efe967adf79afdaa"},
	{id: "forms/block", subject: "forms/block@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityHigh, ReplacedBy: "forms/ quoted"},
		digest: "sha256:64a36decd4084d316b40f8d39af973daaa74619a4fffea9e78a434d29118831e"},
	{id: "forms/flow", subject: "forms/flow@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityMedium},
		digest: "sha256:3f9990b39ee5334f0eb679a93149ac7c034337155c543219c3d40c3a17a76beb"},
	{id: "forms/merge", subject: "forms/merge@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityMedium, Deprecated: true, ReplacedBy: "forms/anchor"},
		digest: "sha256:6457b8164ce5ee0d489a9f0ea3e5b67f70b4af87c9e8e760c696ab2da137456e"},
	{id: "forms/nested", subject: "forms/nested@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityHigh},
		digest: "sha256:dafc01c38c40cb58378d49d6b96a96ca6ed87da9e13918ff62a67b17270b4bda"},
	{id: "forms/quoted", subject: "forms/quoted@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityLow, Deprecated: true},
		digest: "sha256:48f2406357333a68f1cb27c141e294ec55b1f67b8b244fcde20863cb80eef239"},
	{id: "forms/tags", subject: "forms/tags@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityHigh, Deprecated: true},
		digest: "sha256:96881e2fbe8ec04d2e4f7086637a7308a8f04066a81eb54f09a07ad670a9717d"},
	{id: "forms/version-other", subject: "forms/version-other", resolved: true, lintFails: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent},
		digest: "sha256:cefff6d8a72cbcf1f81271ba4512c2c7fa9452dd6e46e757f7e039c25d19ffce"},
	{id: "forms/yes", subject: "forms/yes@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Deprecated: true},
		digest: "sha256:21206286dc273073b206aff2f7937a0c1adc3f7f09d024fbe0f20b33fffd12cc"},
	{id: "forms/binary", subject: "forms/binary",
		digest: "sha256:ba90a346ae2c11b6abf2d1c899451915ba91395e10e78d81c2bdc680414d70f3"},
	{id: "forms/binary-utf8", subject: "forms/binary-utf8@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Sensitivity: manifest.SensitivityHigh},
		digest: "sha256:87ff4f0c086b3afd4d0b94462443503acb731e082ce65725cb59bb6c2e888efb"},
	{id: "forms/bool-on", subject: "forms/bool-on@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Deprecated: true},
		digest: "sha256:383434de34a249f41fbc5c15f99adc482148bae60494d458b4026f04ab59a6a5"},
	{id: "forms/bool-y", subject: "forms/bool-y@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, Deprecated: true},
		digest: "sha256:7454b14c921ac5079581f18860355d8d6ba58ecbc37500b69d7b65e7c94fb457"},
	{id: "forms/bool-off", subject: "forms/bool-off@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeAgent, ReplacedBy: "forms/bool-on"},
		digest: "sha256:24dea51169d0c80fa1b0a254a70872fa491d42afb246685418ece22b2d544ad7"},
	{id: "skills/meta-binary", subject: "skills/meta-binary",
		digest: "sha256:0651b240cd100585790ba0c4ab373c2b03a9b4924fad7536d296e9f88c0c751b"},
	{id: "rules/dot-nested", subject: "rules/dot-nested@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeRule},
		digest: "sha256:af731c168da0562bfd2d7baae8abb2c74088b96eb2c04cb2fa4e7fb64b18f71d"},
	{id: "rules/broken", subject: "rules/broken",
		digest: "sha256:f2a8ad5f7cd03ceea8737a76d774529b898a556545a74371a2e9c154e94968bf"},
	{id: "rules/zero-skill", subject: "rules/zero-skill@1.0.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeRule},
		digest: "sha256:c09194675a7b4225f85fda28287a3ce8e3ee235aeb43e504e2624e6601bdb975"},
	{id: "skills/broken-doc", subject: "skills/broken-doc",
		digest: "sha256:9627875c98d656042b0fd36dde185dae0cf56a004b78df574297688e7044f0ab"},
	{id: "skills/empty-doc", subject: "skills/empty-doc", omit: []string{"SKILL.md"},
		digest: "sha256:39568aa721350c0f2e3ab15cf92ef00a6b84d9bfba837b094cf3205859a8e956"},
	{id: "skills/meta-yes", subject: "skills/meta-yes", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeSkill},
		digest: "sha256:62091a6e78342bcf7cc8157aece14f005b09e766666a909522a19f800c368207"},
	{id: "skills/missing-doc", subject: "skills/missing-doc",
		digest: "sha256:4c878e0ca054cc44ff129bf2b330ed6dcbff67f5f365b4e3ff19e52848ffa2c5"},
	{id: "skills/stated-empty", subject: "skills/stated-empty", resolved: true, lintFails: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeSkill},
		digest: "sha256:6254c4080aa2a24b9f590571bbe2d8a67a235a36d960021012940fc6772272f3"},
	{id: "skills/plain-meta", subject: "skills/plain-meta@1.2.0", resolved: true,
		fields: manifest.ResolvedFields{Type: manifest.TypeSkill, Sensitivity: manifest.SensitivityMedium, ReplacedBy: "skills/next"},
		digest: "sha256:0fc2cc23f65343b506b5bd3ae60f18d5c2612b9fa98f84fac583dcb400f3fc58"},
}

// corpusLayerFiles are the §4.7.6 layer files of the corpus: the root
// DOMAIN.md, a DOMAIN.md that does not parse, one inside a package, and the
// .layer-config, without the DOMAIN.md under .hidden/.
var corpusLayerFiles = []string{
	".layer-config",
	"DOMAIN.md",
	"finance/DOMAIN.md",
	"finance/ap/DOMAIN.md",
	"finance/ap/pay-invoice/DOMAIN.md",
}

// corpusLayerDigest pins the layer digest of the corpus.
const corpusLayerDigest = "sha256:fc40aa4b2a232d2df0ac80916d779443fbf32c54d8b141634d8f7f97c04ee25e"

// packageEntries reads the entries of the package at dir independently of
// the walks: every file below it, apart from the root files the case omits.
// The corpus holds no nested package and no version-control data.
func packageEntries(t *testing.T, dir string, omit []string) map[string]attest.Sum {
	t.Helper()
	entries := map[string]attest.Sum{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		entries[filepath.ToSlash(rel)] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read package %s: %v", dir, err)
	}
	for _, o := range omit {
		delete(entries, o)
	}
	return entries
}

func sortedSubjects(l attest.Layer) []attest.Subject {
	out := append([]attest.Subject(nil), l.Subjects...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Spec: §11 (Package and layer digest tests), §4.7.6, §2.2 — registry
// ingest's walk of a snapshot and the filesystem-source walk of the same
// directory list the same packages under the same subject names and package
// digests, list the same layer files, and give the same layer digest, which
// is pinned. Each package digest is the one pkg/attest computes over the
// pinned resolution and the package's files read independently of the walks,
// so the digest frames the pinned fields. The listed packages include the
// ones ingest refuses, and an unresolved package is listed under its bare ID.
func TestAttestDigest_WalksAgreeOnTheCorpus(t *testing.T) {
	t.Parallel()
	dir := corpusDir(t)
	fsLayer, err := filesystem.LayerContent(filesystem.Layer{ID: "corpus", Path: dir})
	if err != nil {
		t.Fatalf("filesystem.LayerContent: %v", err)
	}
	ingLayer, err := ingest.LayerContent(os.DirFS(dir))
	if err != nil {
		t.Fatalf("ingest.LayerContent: %v", err)
	}
	if !reflect.DeepEqual(sortedSubjects(fsLayer), sortedSubjects(ingLayer)) {
		t.Fatalf("the walks list different subjects:\nfilesystem %+v\ningest     %+v", sortedSubjects(fsLayer), sortedSubjects(ingLayer))
	}
	if !reflect.DeepEqual(fsLayer.Files, ingLayer.Files) {
		t.Fatalf("the walks list different layer files:\nfilesystem %v\ningest     %v", fsLayer.Files, ingLayer.Files)
	}
	var files []string
	for p := range fsLayer.Files {
		files = append(files, p)
	}
	sort.Strings(files)
	if !reflect.DeepEqual(files, corpusLayerFiles) {
		t.Errorf("layer files = %q, want %q", files, corpusLayerFiles)
	}
	fsDigest, ingDigest := attest.LayerDigest(fsLayer), attest.LayerDigest(ingLayer)
	if fsDigest != ingDigest || fsDigest != corpusLayerDigest {
		t.Errorf("layer digest = %s (filesystem), %s (ingest), want the pinned %s", fsDigest, ingDigest, corpusLayerDigest)
	}

	got := map[string]string{}
	for _, s := range fsLayer.Subjects {
		got[s.Name] = s.Digest
	}
	if len(got) != len(corpusPackages) {
		t.Errorf("walk listed %d subjects, the corpus pins %d: %v", len(got), len(corpusPackages), got)
	}
	for _, c := range corpusPackages {
		digest, ok := got[c.subject]
		if !ok {
			t.Errorf("%s: no subject %q in %v", c.id, c.subject, got)
			continue
		}
		want := attest.PackageDigest(attest.Package{
			Resolved: c.resolved,
			Fields:   c.fields,
			Entries:  packageEntries(t, filepath.Join(dir, filepath.FromSlash(c.id)), c.omit),
		})
		if digest != want || digest != c.digest {
			t.Errorf("%s: package digest = %s, pkg/attest over the pinned fields gives %s, pinned %s", c.id, digest, want, c.digest)
		}
	}
}

// Spec: §11 (Package and layer digest tests), §4.7.6, §4.3.4, §13.4 — the
// resolved fields of every corpus package are the pinned values in the
// filesystem-source walk, in the rows registry ingest stores, and in what
// §13.4 stored-row admission serves on a load. Every unresolved package is
// refused per artifact with manifest_unparseable by both walks, a type: skill
// with no SKILL.md among them.
func TestAttestDigest_ResolutionCorpusAgreesAcrossReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := corpusDir(t)

	reg, err := filesystem.Open(dir)
	if err != nil {
		t.Fatalf("filesystem.Open: %v", err)
	}
	refused := map[string]string{}
	records, err := reg.Walk(filesystem.WalkOptions{OnDrop: func(d layer.Drop) {
		refused[d.ArtifactID] = d.Details["constraint"].(string)
	}})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	walked := map[string]manifest.ResolvedFields{}
	for _, r := range records {
		walked[r.ID] = manifest.ResolvedFields{Type: r.Artifact.Type, Sensitivity: r.Artifact.Sensitivity, Deprecated: r.Artifact.Deprecated, ReplacedBy: r.Artifact.ReplacedBy}
	}

	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{TenantID: "t", LayerID: "corpus", Files: os.DirFS(dir)})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	lintFailed := map[string]bool{}
	for _, d := range res.LintFailures {
		lintFailed[d.ArtifactID] = true
	}
	rejected := map[string]string{}
	for _, r := range res.Rejected {
		rejected[r.ArtifactID], _ = r.Details["constraint"].(string)
	}
	rows, err := st.ListManifests(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	stored := map[string]manifest.ResolvedFields{}
	for _, r := range rows {
		stored[r.ArtifactID] = manifest.ResolvedFields{Type: manifest.ArtifactType(r.Type), Sensitivity: manifest.Sensitivity(r.Sensitivity), Deprecated: r.Deprecated, ReplacedBy: r.ReplacedBy}
	}
	admit := core.New(st, "t", []layer.Layer{{ID: "corpus", Precedence: 1, Visibility: layer.Visibility{Public: true}}})

	for _, c := range corpusPackages {
		if !c.resolved {
			if refused[c.id] != layer.ManifestUnparseableConstraint || rejected[c.id] != layer.ManifestUnparseableConstraint {
				t.Errorf("%s: refusal = %q (filesystem), %q (ingest), want manifest_unparseable from both", c.id, refused[c.id], rejected[c.id])
			}
			continue
		}
		if walked[c.id] != c.fields {
			t.Errorf("%s: filesystem walk resolves %+v, want %+v", c.id, walked[c.id], c.fields)
		}
		if c.lintFails {
			if _, ok := stored[c.id]; ok || !lintFailed[c.id] {
				t.Errorf("%s: stored %v, lint failed %v, want a lint refusal and no row", c.id, ok, lintFailed[c.id])
			}
			continue
		}
		if stored[c.id] != c.fields {
			t.Errorf("%s: ingest stores %+v, want %+v", c.id, stored[c.id], c.fields)
		}
		loaded, err := admit.LoadArtifact(ctx, layer.Identity{IsPublic: true}, c.id, core.LoadArtifactOptions{})
		if err != nil {
			t.Errorf("%s: LoadArtifact: %v", c.id, err)
			continue
		}
		served := manifest.ResolvedFields{Type: manifest.ArtifactType(loaded.Type), Sensitivity: manifest.Sensitivity(loaded.Sensitivity), Deprecated: loaded.Deprecated, ReplacedBy: loaded.ReplacedBy}
		if served != c.fields {
			t.Errorf("%s: admission serves %+v, want %+v", c.id, served, c.fields)
		}
	}
	if len(refused) != len(rejected) {
		t.Errorf("refusals differ: filesystem %v, ingest %v", refused, rejected)
	}
	for id := range rejected {
		if !strings.Contains(strings.Join(corpusIDs(), " "), id) {
			t.Errorf("ingest refused %s, which the corpus does not pin", id)
		}
	}
}

func corpusIDs() []string {
	ids := make([]string, 0, len(corpusPackages))
	for _, c := range corpusPackages {
		ids = append(ids, c.id)
	}
	return ids
}

// emptyLayerDigest is the layer digest of a layer with no package and no
// layer file.
const emptyLayerDigest = "sha256:cc667fbb3308a3092591e40ca80c83aec76dbe57b802f23f8938d8dec5db3d76"

// Spec: §11 (Package and layer digest tests), §4.7.6, §2.2 — both walks
// agree on the layers of the reference registry, which follow the spec's
// example layouts, on an empty layer, whose digest is pinned, and on a layer
// read through a root subpath of a larger tree, whose layer subject name
// carries the root with its leading and trailing / characters removed.
func TestAttestDigest_WalksAgreeOnEveryLayout(t *testing.T) {
	t.Parallel()
	ref := referenceRegistryPath(t)
	entries, err := os.ReadDir(ref)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	dirs := map[string]string{"empty": t.TempDir()}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs["reference/"+e.Name()] = filepath.Join(ref, e.Name())
		}
	}
	for name, dir := range dirs {
		fsLayer, err := filesystem.LayerContent(filesystem.Layer{ID: name, Path: dir})
		if err != nil {
			t.Fatalf("%s: filesystem.LayerContent: %v", name, err)
		}
		ingLayer, err := ingest.LayerContent(os.DirFS(dir))
		if err != nil {
			t.Fatalf("%s: ingest.LayerContent: %v", name, err)
		}
		if !reflect.DeepEqual(sortedSubjects(fsLayer), sortedSubjects(ingLayer)) || !reflect.DeepEqual(fsLayer.Files, ingLayer.Files) {
			t.Errorf("%s: the walks differ:\nfilesystem %+v\ningest     %+v", name, fsLayer, ingLayer)
		}
		if name != "empty" && len(fsLayer.Subjects) == 0 {
			t.Errorf("%s: no subjects listed", name)
		}
	}
	empty, err := ingest.LayerContent(os.DirFS(dirs["empty"]))
	if err != nil {
		t.Fatalf("ingest.LayerContent(empty): %v", err)
	}
	if got := attest.LayerDigest(empty); got != emptyLayerDigest {
		t.Errorf("empty layer digest = %s, want the pinned %s", got, emptyLayerDigest)
	}

	tree := filepath.Dir(corpusDir(t))
	sub, err := fs.Sub(os.DirFS(tree), "attest-corpus")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	rooted, err := ingest.LayerContent(sub)
	if err != nil {
		t.Fatalf("ingest.LayerContent(root subpath): %v", err)
	}
	if got := attest.LayerDigest(rooted); got != corpusLayerDigest {
		t.Errorf("layer digest through the root subpath = %s, want the corpus digest %s", got, corpusLayerDigest)
	}
	if name, err := attest.LayerSubjectName("/attest-corpus/"); err != nil || name != ".podium/layer/attest-corpus" {
		t.Errorf("LayerSubjectName = %q, %v, want .podium/layer/attest-corpus", name, err)
	}
}

// Spec: §4.7.6, §11 (Package and layer digest tests) — a symbolic link to a
// file inside a package is read as its target's bytes, and a symbolic link to
// a directory inside a package fails both walks, so the layer has no digest.
func TestAttestDigest_SymbolicLinksInsideAPackage(t *testing.T) {
	t.Parallel()
	const manifestMD = "---\ntype: rule\nversion: 1.0.0\ndescription: Apply the rule.\nrule_mode: always\n---\n\nBody.\n"
	build := func(t *testing.T, target, link string) string {
		t.Helper()
		root := t.TempDir()
		for p, b := range map[string]string{"team/pkg/ARTIFACT.md": manifestMD, "shared/refs/common.md": "Shared.\n"} {
			full := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(full, []byte(b), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}
		if err := os.Symlink(target, filepath.Join(root, "team", "pkg", link)); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		return root
	}

	fileLink := build(t, "../../shared/refs/common.md", "common.md")
	fsLayer, err := filesystem.LayerContent(filesystem.Layer{ID: "L", Path: fileLink})
	if err != nil {
		t.Fatalf("filesystem.LayerContent with a file link: %v", err)
	}
	ingLayer, err := ingest.LayerContent(os.DirFS(fileLink))
	if err != nil {
		t.Fatalf("ingest.LayerContent with a file link: %v", err)
	}
	want := attest.PackageDigest(attest.Package{
		Resolved: true,
		Fields:   manifest.ResolvedFields{Type: manifest.TypeRule},
		Entries: map[string]attest.Sum{
			"ARTIFACT.md": sha256.Sum256([]byte(manifestMD)),
			"common.md":   sha256.Sum256([]byte("Shared.\n")),
		},
	})
	if fsLayer.Subjects[0].Digest != want || ingLayer.Subjects[0].Digest != want {
		t.Errorf("file link digests = %s (filesystem), %s (ingest), want the target's bytes %s", fsLayer.Subjects[0].Digest, ingLayer.Subjects[0].Digest, want)
	}

	dirLink := build(t, "../../shared/refs", "refs")
	if _, err := filesystem.LayerContent(filesystem.Layer{ID: "L", Path: dirLink}); err == nil {
		t.Errorf("filesystem.LayerContent with a directory link = nil, want a failed walk")
	}
	if _, err := ingest.LayerContent(os.DirFS(dirLink)); err == nil {
		t.Errorf("ingest.LayerContent with a directory link = nil, want a failed walk")
	}
}

// Spec: §4.7.6, §11 (Package and layer digest tests) — a layer path that is
// a symbolic link to the corpus is walked as the directory it names, so the
// filesystem-source walk, the ingest walk over the same path, and Walk agree
// with the corpus itself.
func TestAttestDigest_SymlinkedLayerRoot(t *testing.T) {
	t.Parallel()
	link := filepath.Join(t.TempDir(), "corpus-link")
	if err := os.Symlink(corpusDir(t), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	fsLayer, err := filesystem.LayerContent(filesystem.Layer{ID: "corpus", Path: link})
	if err != nil {
		t.Fatalf("filesystem.LayerContent: %v", err)
	}
	ingLayer, err := ingest.LayerContent(os.DirFS(link))
	if err != nil {
		t.Fatalf("ingest.LayerContent: %v", err)
	}
	if got := attest.LayerDigest(fsLayer); got != corpusLayerDigest || attest.LayerDigest(ingLayer) != corpusLayerDigest {
		t.Errorf("layer digest through the link = %s (filesystem), %s (ingest), want %s", got, attest.LayerDigest(ingLayer), corpusLayerDigest)
	}
	reg := &filesystem.Registry{Layers: []filesystem.Layer{{ID: "corpus", Path: link}}}
	records, err := reg.Walk(filesystem.WalkOptions{OnDrop: func(layer.Drop) {}})
	if err != nil || len(records) == 0 {
		t.Errorf("Walk through the link = %d records, %v, want the corpus records", len(records), err)
	}
	direct := &filesystem.Registry{Layers: []filesystem.Layer{{ID: "corpus", Path: corpusDir(t)}}}
	want, err := direct.WalkDomains()
	if err != nil || len(want) == 0 {
		t.Fatalf("WalkDomains of the corpus = %d, %v", len(want), err)
	}
	linked, err := reg.WalkDomains()
	if err != nil || len(linked) != len(want) {
		t.Errorf("WalkDomains through the link = %d domains, %v, want the corpus's %d", len(linked), err, len(want))
	}
	for i := range linked {
		if i < len(want) && linked[i].Path != want[i].Path {
			t.Errorf("domain %d through the link = %q, want %q", i, linked[i].Path, want[i].Path)
		}
	}
	gone := &filesystem.Registry{Layers: []filesystem.Layer{{ID: "gone", Path: filepath.Join(t.TempDir(), "absent")}}}
	if _, err := gone.WalkDomains(); err == nil {
		t.Errorf("WalkDomains of an absent path = nil, want the resolve error")
	}
	if _, err := filesystem.LayerContent(filesystem.Layer{ID: "gone", Path: filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Errorf("LayerContent of an absent path = nil, want the resolve error")
	}
}
