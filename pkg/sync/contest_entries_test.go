package sync

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/version"
)

// genResponse is a sealed load_artifact response of any type, signed by the
// harness default signer when h is not nil.
func genResponse(t *testing.T, h *sh.Harness, id, typ, fm, skillRaw string, resources map[string]string, bundles map[string][]byte) map[string]any {
	t.Helper()
	resp := map[string]any{
		"id": id, "version": "1.0.0", "type": typ, "layer": "team",
		"manifest_body": "body\n", "frontmatter": fm,
		"artifact_revision": "2025-01-01T00:00:00.000000Z",
	}
	if typ == "skill" {
		resp["manifest_body"] = "Run the linter.\n"
	}
	if skillRaw != "" {
		resp["skill_raw"] = skillRaw
	}
	if len(resources) > 0 {
		resp["resources"] = resources
		resp["resources_base64"] = false
	}
	seal(t, resp, nil)
	if h == nil {
		return resp
	}
	raw, _ := json.Marshal(resp)
	served, err := version.ParseLoadResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	life, _ := attest.ReadLifecycle(served.Members)
	cands, err := attest.PackageDigestCandidates(served.Record, life)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := attest.ExpectedSubjectName(id, served.Record)
	var subs []sh.Subject
	for _, c := range cands {
		subs = append(subs, sh.Subject{Name: name, SHA256: strings.TrimPrefix(c, "sha256:")})
	}
	b := h.DSSEBundle(sh.Statement(t, attestPredicate, subs...), sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(h.Clock().Add(time.Minute))))
	d := attest.AttestationDigest(b)
	bundles[d] = b
	resp["attestations"] = []map[string]string{{"digest": d, "verdict": "verified"}}
	return resp
}

// mcpServerDoc is the manifest of an mcp-server whose entry name is db.
func mcpServerDoc(server string) string {
	return "---\ntype: mcp-server\nname: db\nversion: 1.0.0\ndescription: d\nserver_identifier: " + server + "\n---\n\nbody\n"
}

// namedEntryStub lists the attested finance/db and ops/x, which declares
// the same entry name db.
func namedEntryStub(t *testing.T, h *sh.Harness) *httptest.Server {
	t.Helper()
	bundles := map[string][]byte{}
	good := genResponse(t, h, "finance/db", "mcp-server", mcpServerDoc("npx:@acme/good"), "", nil, bundles)
	evil := genResponse(t, nil, "ops/x", "mcp-server", mcpServerDoc("npx:@evil/pwn"), "", nil, bundles)
	return attestedStub(t, []string{"finance/db", "ops/x"}, func(_ string, call int) map[string]any {
		if call == 0 {
			return good
		}
		return evil
	}, bundles)
}

// Spec: §7.5 (Output paths of distinct IDs) — ops/x, which resolves to none,
// declares name: db and would merge into the mcpServers.db entry that the
// attested finance/db adds to .mcp.json (claude-code) and the
// [mcp_servers.db] table it injects into .codex/config.toml (codex). Both IDs
// are refused with materialize.output_conflict (entry) under every mode, and
// neither server reaches the file.
func TestRun_NamedEntryContest(t *testing.T) {
	t.Parallel()
	for adapterID, file := range map[string]string{"claude-code": ".mcp.json", "codex": ".codex/config.toml"} {
		t.Run(adapterID, func(t *testing.T) {
			t.Parallel()
			h := sh.New(t)
			srv := namedEntryStub(t, h)
			_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
			target := t.TempDir()
			res, err := Run(Options{RegistryPath: srv.URL, Delivery: neverDelivery, Target: target, AdapterID: adapterID,
				HTTPClient: srv.Client(), Attestation: f})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Dropped) != 2 || len(res.Artifacts) != 0 {
				t.Fatalf("run = artifacts %+v dropped %+v", res.Artifacts, res.Dropped)
			}
			for _, d := range res.Dropped {
				if d.Code != attest.CodeOutputConflict || d.Details["constraint"] != attest.ConstraintEntry {
					t.Fatalf("drop = %+v", d)
				}
			}
			if b, _ := os.ReadFile(filepath.Join(target, filepath.FromSlash(file))); strings.Contains(string(b), "@evil/pwn") {
				t.Fatalf("the unattested server reached %s: %s", file, b)
			}
		})
	}
}

// Spec: §7.8 (Author attestation) — two IDs that add one mcpServers entry to
// a plugin's MCP configuration fail the render before it writes.
func TestRender_NamedEntryContestFailsTheTarget(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	srv := namedEntryStub(t, h)
	_, f := testAttestation(t, h, "", signerRule("require", sh.DefaultSAN, "finance/**"))
	workdir := t.TempDir()
	_, err := Render(context.Background(), RenderOptions{OutputID: "acme", Registry: srv.URL, Workdir: workdir,
		Harnesses: []string{"claude-code"}, Plugins: []PluginFilter{{Name: "pack", Include: []string{"**"}}},
		Attestation: f, Delivery: neverDelivery, HTTPClient: srv.Client()})
	if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeOutputConflict || fl.Constraint != attest.ConstraintEntry {
		t.Fatalf("Render = %v, want output_conflict (entry)", err)
	}
	if entries, _ := os.ReadDir(workdir); len(entries) != 0 {
		t.Fatalf("the failed render wrote %v", entries)
	}
}

// Spec: §7.5 (Output paths of distinct IDs) — the named entries of a JSON
// merge come from its x-podium index and those of a TOML inject from its
// table headers; an array of tables and a markdown inject claim nothing.
func TestNamedEntries(t *testing.T) {
	t.Parallel()
	got := namedEntries([]adapter.File{
		{Path: ".mcp.json", Op: adapter.OpMergeJSON, Content: []byte(`{"mcpServers":{"db":{}},"x-podium":{"a":["mcpServers","db"]}}`)},
		{Path: ".codex/config.toml", Op: adapter.OpInject, Content: []byte("[mcp_servers.db]\ncommand = \"x\"\n[[hooks.Stop]]\n")},
		{Path: "AGENTS.md", Op: adapter.OpInject, Content: []byte("[not a table]\n")},
		{Path: "a.md"},
	})
	want := []string{".mcp.json" + entrySep + "mcpServers.db", ".codex/config.toml" + entrySep + "mcp_servers.db"}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %q", got)
	}
	f := conflictFailure("a", outputContest{path: ".mcp.json" + entrySep + "mcpServers.db", other: "b"})
	if f.Constraint != attest.ConstraintEntry || !strings.Contains(f.Error(), "mcpServers.db") {
		t.Fatalf("failure = %v", f)
	}
}
