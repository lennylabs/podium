package serverboot

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/store"
)

// declaredLocalTree writes a single-artifact local layer rooted at a fresh
// temp dir and returns the absolute path.
func declaredLocalTree(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	testharness.WriteTree(t, dir, testharness.WriteTreeOption{
		Path:    name + "/ARTIFACT.md",
		Content: artifactBody,
	})
	return dir
}

// Spec: §4.6 — the declarative `layers:` list seeds a LayerConfig
// per entry, ingests local sources at startup, and feeds the in-memory layer
// list with the declared visibility.
func TestBootstrapDeclaredLayers_LocalIngestsAndSeeds(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	path := declaredLocalTree(t, "policy")
	cfg := &Config{
		identityProvider: "oidc",
		declaredLayers: []yamlLayerEntry{{
			ID:         "org-defaults",
			Source:     yamlLayerSource{Local: &yamlLocalSource{Path: path}},
			Visibility: yamlLayerVisibility{Organization: true},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	if layers[0].ID != "org-defaults" || layers[0].Precedence != 1 {
		t.Errorf("layer = %+v, want id=org-defaults precedence=1", layers[0])
	}
	if !layers[0].Visibility.Organization || layers[0].Visibility.Public {
		t.Errorf("Visibility = %+v, want organization-only", layers[0].Visibility)
	}

	lc, err := st.GetLayerConfig(context.Background(), "default", "org-defaults")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.SourceType != "local" || lc.LocalPath != path {
		t.Errorf("LayerConfig source = %q/%q, want local/%q", lc.SourceType, lc.LocalPath, path)
	}
	if !lc.Organization || lc.Public {
		t.Errorf("LayerConfig visibility = %+v, want organization-only", lc)
	}

	mans, err := st.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(mans) == 0 {
		t.Errorf("declared local layer produced no manifests")
	}
}

// Spec: §4.6 / §13.10 — a git source is seeded as a config row but
// not cloned at startup (a clone is unbounded network I/O that must not block
// boot); the §7.3.1 reingest/webhook path pulls it later.
func TestBootstrapDeclaredLayers_GitSeedsWithoutClone(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID:         "team-finance",
			Source:     yamlLayerSource{Git: &yamlGitSource{Repo: "git@github.com:acme/x.git", Ref: "main", Root: "artifacts/"}},
			Visibility: yamlLayerVisibility{Groups: []string{"acme-finance"}},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.SourceType != "git" || lc.Repo != "git@github.com:acme/x.git" || lc.Ref != "main" || lc.Root != "artifacts/" {
		t.Errorf("git LayerConfig = %+v", lc)
	}
	if len(lc.Groups) != 1 || lc.Groups[0] != "acme-finance" {
		t.Errorf("git LayerConfig groups = %v, want [acme-finance]", lc.Groups)
	}
	mans, err := st.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(mans) != 0 {
		t.Errorf("git layer was ingested at boot (%d manifests); ingest must defer to reingest", len(mans))
	}
}

// Spec: §4.6 — a declared layer with an empty visibility block falls
// back to the deployment default (IdP + private → no public grant).
func TestBootstrapDeclaredLayers_EmptyVisibilityUsesDefault(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	path := declaredLocalTree(t, "policy")
	cfg := &Config{
		identityProvider:       "oidc",
		defaultLayerVisibility: "private",
		declaredLayers: []yamlLayerEntry{{
			ID:     "no-vis",
			Source: yamlLayerSource{Local: &yamlLocalSource{Path: path}},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if layers[0].Visibility.Public {
		t.Errorf("empty visibility under IdP+private must not be public: %+v", layers[0].Visibility)
	}
}

// Spec: §4.6 — multiple entries take orders 1..N in config order
// (lowest precedence first).
func TestBootstrapDeclaredLayers_OrdersByListPosition(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		identityProvider: "",
		declaredLayers: []yamlLayerEntry{
			{ID: "first", Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "a")}}},
			{ID: "second", Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "b")}}},
		},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 2 || layers[0].ID != "first" || layers[0].Precedence != 1 ||
		layers[1].ID != "second" || layers[1].Precedence != 2 {
		t.Errorf("layers = %+v, want first@1 then second@2", layers)
	}
}

// Spec: §4.6 — invalid declared entries abort startup so the
// operator notices the misconfiguration.
func TestBootstrapDeclaredLayers_ValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		entry yamlLayerEntry
	}{
		{"missing id", yamlLayerEntry{Source: yamlLayerSource{Local: &yamlLocalSource{Path: "/x"}}}},
		{"no source", yamlLayerEntry{ID: "x"}},
		{"both sources", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Git: &yamlGitSource{Repo: "r"}, Local: &yamlLocalSource{Path: "/x"}}}},
		{"git no repo", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Git: &yamlGitSource{Ref: "main"}}}},
		{"local no path", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Local: &yamlLocalSource{}}}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			st := newMemoryStoreWithTenant(t)
			cfg := &Config{declaredLayers: []yamlLayerEntry{c.entry}}
			if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err == nil {
				t.Errorf("err = nil, want a validation error for %s", c.name)
			}
		})
	}
}

// Spec: §4.6 — readYAMLConfig parses the full layers: list with
// source (git/local) and visibility blocks per the §4.6 config schema.
func TestReadYAMLConfig_ParsesLayersList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte(`registry:
  layers:
    - id: org-defaults
      source:
        git:
          repo: git@github.com:acme/org.git
          ref: main
          root: artifacts/
      visibility:
        organization: true
    - id: dev-finance
      source:
        local:
          path: /var/podium/dev/finance
      visibility:
        users: [alice@acme.com]
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	if y == nil || len(y.Layers) != 2 {
		t.Fatalf("Layers = %+v, want 2 entries", y)
	}
	if y.Layers[0].ID != "org-defaults" || y.Layers[0].Source.Git == nil ||
		y.Layers[0].Source.Git.Repo != "git@github.com:acme/org.git" || y.Layers[0].Source.Git.Root != "artifacts/" {
		t.Errorf("layer[0] = %+v", y.Layers[0])
	}
	if !y.Layers[0].Visibility.Organization {
		t.Errorf("layer[0] visibility = %+v, want organization", y.Layers[0].Visibility)
	}
	if y.Layers[1].Source.Local == nil || y.Layers[1].Source.Local.Path != "/var/podium/dev/finance" {
		t.Errorf("layer[1] source = %+v", y.Layers[1].Source)
	}
	if len(y.Layers[1].Visibility.Users) != 1 || y.Layers[1].Visibility.Users[0] != "alice@acme.com" {
		t.Errorf("layer[1] users = %v", y.Layers[1].Visibility.Users)
	}
}

// Spec: §4.6 — applyYAML carries the parsed layers: list onto Config
// so Run() can seed them.
func TestApplyYAML_CarriesDeclaredLayers(t *testing.T) {
	c := &Config{}
	applyYAML(c, &yamlConfig{Layers: []yamlLayerEntry{
		{ID: "a", Source: yamlLayerSource{Local: &yamlLocalSource{Path: "/x"}}},
	}})
	if len(c.declaredLayers) != 1 || c.declaredLayers[0].ID != "a" {
		t.Errorf("declaredLayers = %+v, want 1 entry id=a", c.declaredLayers)
	}
}

// Spec: §7.3.1 — the declared `source.git.git_provider` key is the setter for
// a declared layer's git provider, and it is seeded onto the stored config so
// the inbound webhook path verifies the delivery under that provider's
// signature scheme.
func TestBootstrapDeclaredLayers_GitProviderSeeded(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID: "team-finance",
			Source: yamlLayerSource{Git: &yamlGitSource{
				Repo: "git@gitlab.com:acme/x.git", Ref: "main", GitProvider: "gitlab",
			}},
		}},
	}
	if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.GitProvider != "gitlab" {
		t.Errorf("GitProvider = %q, want gitlab", lc.GitProvider)
	}
}

// Spec: §7.3.1 (Declared repository credentials) — the boot log line for a
// declared git layer names the layer and the repo host with the URL userinfo
// removed, a second line steers the URL credential to the source.git keys, and
// the stored row keeps the full repo the clone reads (X9-0.7.0). The test
// replaces the process-wide log writer, so it does not run in parallel.
func TestBootstrapDeclaredLayers_GitRepoCredentialNotLogged(t *testing.T) {
	const repo = "https://alice-user:s3cr3tpw@git.acme.com/acme/finance.git"
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID:     "team-finance",
			Source: yamlLayerSource{Git: &yamlGitSource{Repo: repo, Ref: "main"}},
		}},
	}
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	out := buf.String()
	const want = "seeded declared git layer team-finance (repo=https://git.acme.com/acme/finance.git ref=main)"
	if !strings.Contains(out, want) {
		t.Errorf("boot log missing %q, got: %q", want, out)
	}
	// Spec: §7.3.1 (Declared repository credentials). A declaration that
	// sets neither key is seeded as declared, and each start steers its URL
	// credential to the keys.
	const steer = "declared git layer team-finance carries URL userinfo in source.git.repo; declare its credential in source.git.username and source.git.token"
	if !strings.Contains(out, steer) {
		t.Errorf("boot log missing %q, got: %q", steer, out)
	}
	for _, part := range []string{"alice-user", "s3cr3tpw"} {
		if strings.Contains(out, part) {
			t.Errorf("boot log carries the credential part %q: %q", part, out)
		}
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got := lc.CloneRepo(); got != repo {
		t.Errorf("stored CloneRepo = %q, want the full declared value %q", got, repo)
	}
	if lc.Repo != "https://git.acme.com/acme/finance.git" {
		t.Errorf("stored Repo = %q, want the reported value", lc.Repo)
	}
}

// declaredTokenNode builds the yaml.Node a parsed `token:` scalar yields.
func declaredTokenNode(value string) yaml.Node {
	return yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

// Spec: §7.3.1 (Declared repository credentials) — a declaration that sets
// source.git.username and source.git.token is seeded with the credential
// joined into its repo, the store holds it apart from the reported value, and
// no log line carries either part or the URL-userinfo steer (X8-0.7.0). The
// test replaces the process-wide log writer, so it does not run in parallel.
func TestBootstrapDeclaredLayers_GitRepoCredentialKeys(t *testing.T) {
	const bare = "https://git.acme.com/acme/finance.git"
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID: "team-finance",
			Source: yamlLayerSource{Git: &yamlGitSource{
				Repo: bare, Ref: "main", Username: "alice-user", Token: declaredTokenNode("s3cr3tpw"),
			}},
		}},
	}
	logs := captureLog(t)
	if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	out := logs.String()
	const want = "seeded declared git layer team-finance (repo=https://git.acme.com/acme/finance.git ref=main)"
	if !strings.Contains(out, want) {
		t.Errorf("boot log missing %q, got: %q", want, out)
	}
	for _, part := range []string{"alice-user", "s3cr3tpw", "carries URL userinfo"} {
		if strings.Contains(out, part) {
			t.Errorf("boot log carries %q: %q", part, out)
		}
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got, want := lc.CloneRepo(), "https://alice-user:s3cr3tpw@git.acme.com/acme/finance.git"; got != want {
		t.Errorf("stored CloneRepo = %q, want %q", got, want)
	}
	if lc.Repo != bare {
		t.Errorf("stored Repo = %q, want the bare value %q", lc.Repo, bare)
	}
}

// Spec: §7.3.1 (Declared repository credentials) — startup aborts only for a
// declaration that sets a credential key, and each refusal names the layer
// and the key and carries no part of the credential. A declaration that sets
// neither key is seeded with its source.git.repo byte for byte, and the
// URL-userinfo steer applies to an http or https repo with userinfo alone.
func TestBootstrapDeclaredLayers_RepoCredentialRefusals(t *testing.T) {
	tok := declaredTokenNode("s3cr3tpw")
	refused := []struct {
		name string
		git  yamlGitSource
		want string
	}{
		{"userinfo with token (X9-0.7.0)", yamlGitSource{Repo: "https://alice-user:s3cr3tpw@h/x.git", Token: tok}, "remove the userinfo from source.git.repo"},
		{"empty userinfo with token", yamlGitSource{Repo: "https://@h/x.git", Token: tok}, "remove the userinfo from source.git.repo"},
		{"redacted class with token (X10-0.7.0)", yamlGitSource{Repo: "https://h/x.git@s3cr3tpw", Token: tok}, "takes no credential"},
		{"unparseable ssh with username (X10-0.7.0)", yamlGitSource{Repo: "ssh://alice-user@h:org/x.git", Username: "alice-user"}, "takes no credential"},
		{"ssh login name with token (X10-0.7.0)", yamlGitSource{Repo: "ssh://git@h/x.git", Token: tok}, "takes no credential"},
		{"username without token", yamlGitSource{Repo: "https://h/x.git", Username: "alice-user"}, "without source.git.token"},
		{"empty token", yamlGitSource{Repo: "https://h/x.git", Token: declaredTokenNode("")}, "empty source.git.token"},
		{"mapping token", yamlGitSource{Repo: "https://h/x.git", Token: yaml.Node{Kind: yaml.MappingNode}}, "source.git.token that is not a string"},
		{"username with colon", yamlGitSource{Repo: "https://h/x.git", Username: "alice-user:x", Token: tok}, "invalid source.git.username"},
		{"token with newline", yamlGitSource{Repo: "https://h/x.git", Token: declaredTokenNode("s3cr3tpw\n")}, "invalid source.git.token"},
	}
	for _, c := range refused {
		t.Run("refused/"+c.name, func(t *testing.T) {
			st := newMemoryStoreWithTenant(t)
			git := c.git
			cfg := &Config{declaredLayers: []yamlLayerEntry{{ID: "team-x", Source: yamlLayerSource{Git: &git}}}}
			_, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
			if err == nil {
				t.Fatalf("err = nil, want a refusal carrying %q", c.want)
			}
			msg := err.Error()
			for _, frag := range []string{"team-x", c.want} {
				if !strings.Contains(msg, frag) {
					t.Errorf("error %q does not carry %q", msg, frag)
				}
			}
			for _, part := range []string{"s3cr3tpw", "alice-user"} {
				if strings.Contains(msg, part) {
					t.Errorf("error %q carries the credential part %q", msg, part)
				}
			}
			if _, err := st.GetLayerConfig(context.Background(), "default", "team-x"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetLayerConfig err = %v, want ErrNotFound for a refused declaration", err)
			}
		})
	}

	admitted := []struct {
		name          string
		git           yamlGitSource
		urlCredential bool
	}{
		{"https without userinfo", yamlGitSource{Repo: "https://git.acme.com/acme/x.git"}, false},
		{"scp-like", yamlGitSource{Repo: "git@github.com:acme/x.git"}, false},
		{"https credential (X9-0.7.0)", yamlGitSource{Repo: "https://alice-user:s3cr3tpw@h/x.git"}, true},
		{"https token-only userinfo (X9-0.7.0)", yamlGitSource{Repo: "https://ghp_tok3n@h/x.git"}, true},
		{"https empty userinfo", yamlGitSource{Repo: "https://@h/x.git"}, true},
		{"https encoded colon in username", yamlGitSource{Repo: "https://a%3Ab:s3cr3tpw@h/x.git"}, true},
		{"https credential with empty username key", yamlGitSource{Repo: "https://alice-user:s3cr3tpw@h/x.git", Username: ""}, true},
		{"redacted class (X10-0.7.0)", yamlGitSource{Repo: "https://h/x.git@s3cr3tpw"}, false},
		{"unparseable ssh (X10-0.7.0)", yamlGitSource{Repo: "ssh://alice-user@h:org/x.git"}, false},
		{"ssh login name (X10-0.7.0)", yamlGitSource{Repo: "ssh://git@h/x.git"}, false},
	}
	for _, c := range admitted {
		t.Run("admitted/"+c.name, func(t *testing.T) {
			git := c.git
			entry := yamlLayerEntry{ID: "team-x", Source: yamlLayerSource{Git: &git}}
			lc, _, err := layerConfigFromEntry("default", entry, 1, &Config{})
			if err != nil {
				t.Fatalf("layerConfigFromEntry: %v", err)
			}
			if lc.Repo != c.git.Repo {
				t.Errorf("Repo = %q, want the declared value %q byte for byte", lc.Repo, c.git.Repo)
			}
			if got := declaredURLCredential(entry.Source.Git); got != c.urlCredential {
				t.Errorf("declaredURLCredential = %v, want %v", got, c.urlCredential)
			}
		})
	}
}

// Spec: §7.3.1 (Declared repository credentials) — readYAMLConfig parses the
// source.git.username and source.git.token keys. A token written as an unset
// ${ENV_VAR} expands to nothing and parses as a present null, which an absent
// key does not, and bootstrapDeclaredLayers refuses it as an empty token.
func TestReadYAMLConfig_ParsesGitCredentialKeys(t *testing.T) {
	t.Setenv("PODIUM_TEST_GIT_TOKEN", "s3cr3tpw")
	t.Setenv("PODIUM_TEST_UNSET_VAR", "")
	if err := os.Unsetenv("PODIUM_TEST_UNSET_VAR"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	path := filepath.Join(t.TempDir(), "registry.yaml")
	body := []byte(`registry:
  layers:
    - id: team-set
      source:
        git:
          repo: https://git.acme.com/acme/set.git
          username: alice-user
          token: "${PODIUM_TEST_GIT_TOKEN}"
    - id: team-unset
      source:
        git:
          repo: https://git.acme.com/acme/unset.git
          token: ${PODIUM_TEST_UNSET_VAR}
    - id: team-absent
      source:
        git:
          repo: https://git.acme.com/acme/absent.git
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	if y == nil || len(y.Layers) != 3 {
		t.Fatalf("Layers = %+v, want 3 entries", y)
	}
	set, unset, absent := y.Layers[0].Source.Git, y.Layers[1].Source.Git, y.Layers[2].Source.Git
	if set == nil || unset == nil || absent == nil {
		t.Fatalf("every entry must parse a git source: %+v", y.Layers)
	}
	if set.Username != "alice-user" || set.Token.Kind != yaml.ScalarNode || set.Token.Value != "s3cr3tpw" {
		t.Errorf("set entry: username %q, token kind %v value %q; want alice-user, scalar, s3cr3tpw",
			set.Username, set.Token.Kind, set.Token.Value)
	}
	if unset.Token.Kind == 0 || unset.Token.ShortTag() != "!!null" {
		t.Errorf("unset entry: token kind %v tag %q; want a present !!null", unset.Token.Kind, unset.Token.ShortTag())
	}
	if absent.Token.Kind != 0 {
		t.Errorf("absent entry: token kind %v, want 0", absent.Token.Kind)
	}

	st := newMemoryStoreWithTenant(t)
	cfg := &Config{declaredLayers: []yamlLayerEntry{y.Layers[1]}}
	_, err = bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err == nil {
		t.Fatalf("err = nil, want the empty-token refusal for a null token")
	}
	for _, want := range []string{"team-unset", "empty source.git.token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

// Spec: §7.3.1 (Declared repository credentials) — the declaration is
// authoritative for the credential. Each start re-seeds the declared token
// over a row holding another userinfo and over the bare row a 0.6.0 start
// writes for the declaration (X8-0.7.0), a declaration without the keys seeds
// the bare repo, and a declaration carrying the credential in source.git.repo
// seeds that value (X9-0.7.0). The reported Repo stays bare throughout.
func TestBootstrapDeclaredLayers_ReseedsDeclaredCredential(t *testing.T) {
	const (
		bare     = "https://git.acme.com/acme/finance.git"
		declared = "https://alice-user:s3cr3tpw@git.acme.com/acme/finance.git"
	)
	ctx := context.Background()
	st := newMemoryStoreWithTenant(t)
	keyed := yamlGitSource{Repo: bare, Ref: "main", Username: "alice-user", Token: declaredTokenNode("s3cr3tpw")}
	seed := func(t *testing.T, git yamlGitSource) store.LayerConfig {
		t.Helper()
		cfg := &Config{declaredLayers: []yamlLayerEntry{{ID: "team-finance", Source: yamlLayerSource{Git: &git}}}}
		if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
			t.Fatalf("bootstrapDeclaredLayers: %v", err)
		}
		lc, err := st.GetLayerConfig(ctx, "default", "team-finance")
		if err != nil {
			t.Fatalf("GetLayerConfig: %v", err)
		}
		if lc.Repo != bare {
			t.Errorf("stored Repo = %q, want the bare value %q", lc.Repo, bare)
		}
		return lc
	}
	overwrite := func(t *testing.T, lc store.LayerConfig, repo string) {
		t.Helper()
		lc.Repo, lc.RegisteredRepo = repo, ""
		if err := st.PutLayerConfig(ctx, lc); err != nil {
			t.Fatalf("PutLayerConfig: %v", err)
		}
	}

	lc := seed(t, keyed)
	if got := lc.CloneRepo(); got != declared {
		t.Fatalf("first seed CloneRepo = %q, want %q", got, declared)
	}

	overwrite(t, lc, "https://bob-user:0ldt0ken@git.acme.com/acme/finance.git")
	if got := seed(t, keyed).CloneRepo(); got != declared {
		t.Errorf("re-seed over another userinfo: CloneRepo = %q, want %q", got, declared)
	}

	overwrite(t, lc, bare)
	if got := seed(t, keyed).CloneRepo(); got != declared {
		t.Errorf("re-seed over the 0.6.0 bare row: CloneRepo = %q, want %q", got, declared)
	}

	if got := seed(t, yamlGitSource{Repo: bare, Ref: "main"}).CloneRepo(); got != bare {
		t.Errorf("declaration without keys: CloneRepo = %q, want the bare %q", got, bare)
	}

	if got := seed(t, yamlGitSource{Repo: declared, Ref: "main"}).CloneRepo(); got != declared {
		t.Errorf("declaration with a URL credential: CloneRepo = %q, want %q", got, declared)
	}
}

// Spec: §7.3.1 — an entry that omits the key seeds the empty string, which
// resolves to github at the point of use, so an existing registry.yaml keeps
// its behaviour with no edit.
func TestBootstrapDeclaredLayers_GitProviderOmittedSeedsEmpty(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID:     "vendor",
			Source: yamlLayerSource{Git: &yamlGitSource{Repo: "git@github.com:acme/vendor.git"}},
		}},
	}
	if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{}); err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "vendor")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.GitProvider != "" {
		t.Errorf("GitProvider = %q, want the empty string", lc.GitProvider)
	}
}

// Spec: §7.3.1 — a declared value the provider registry does not carry aborts
// the boot, with an error naming the layer and the value.
func TestBootstrapDeclaredLayers_UnknownGitProviderAborts(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID: "team-finance",
			Source: yamlLayerSource{Git: &yamlGitSource{
				Repo: "git@example.com:acme/x.git", GitProvider: "acme-forge-that-is-not-registered",
			}},
		}},
	}
	_, err := bootstrapDeclaredLayers(st, "default", cfg, nil, nil, false, collocatedVectorIngest{})
	if err == nil {
		t.Fatalf("err = nil, want a refusal for an unregistered git_provider")
	}
	for _, want := range []string{"team-finance", "acme-forge-that-is-not-registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// Spec: §7.3.1 — readYAMLConfig parses the `source.git.git_provider` key.
func TestReadYAMLConfig_ParsesGitProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.yaml")
	body := []byte(`registry:
  layers:
    - id: team-finance
      source:
        git:
          repo: git@gitlab.com:acme/finance.git
          ref: main
          git_provider: gitlab
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	if y == nil || len(y.Layers) != 1 || y.Layers[0].Source.Git == nil {
		t.Fatalf("Layers = %+v, want one git entry", y)
	}
	if got := y.Layers[0].Source.Git.GitProvider; got != "gitlab" {
		t.Errorf("git_provider = %q, want gitlab", got)
	}
}
