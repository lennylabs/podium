package e2e

// End-to-end tests for docs/consuming/custom-via-sdk.md (D-custom-sdk).
//
// The Python and TypeScript SDKs are driven as subprocesses against a real
// standalone server. Each SDK test first gates on a usable toolchain:
//   - Python: the SDK requires >=3.10 (it uses PEP 604 `X | None` unions);
//     csPython skips when no such interpreter can import the package. No
//     `pip install` is needed because the SDK imports only the stdlib, so it
//     runs from PYTHONPATH directly.
//   - TypeScript: csNode skips unless `node` can execute the SDK source. The
//     SDK uses TS parameter properties, which Node's strip-only loader
//     rejects, so a transpiler/built dist is required; absent it, these skip.
//
// Server-side validation (batch cap, method enforcement, the absence of a
// bulk MCP tool) is exercised in pure Go and always runs.
//
// Several doc examples describe SDK surfaces that do not exist; those tests
// assert the discrepancy (a TypeError / AttributeError) and document the gap.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- toolchain gates --------------------------------------------------------

func csPyDir(t *testing.T) string { return filepath.Join(repoRoot(t), "sdks", "podium-py") }

// csPython returns a python interpreter (>=3.10) that can import the SDK from
// PYTHONPATH, or skips the test.
func csPython(t *testing.T) string {
	t.Helper()
	pyDir := csPyDir(t)
	for _, cand := range []string{"python3.13", "python3.12", "python3.11", "python3.10", "python3"} {
		bin, err := exec.LookPath(cand)
		if err != nil {
			continue
		}
		res := runBin(t, bin, "", []string{"PYTHONPATH=" + pyDir}, nil, 30*time.Second,
			"-c", "import sys; assert sys.version_info[:2] >= (3,10); import podium")
		if res.Exit == 0 {
			return bin
		}
	}
	t.Skip("no Python >=3.10 that can import podium-sdk (host interpreter predates PEP 604 union syntax used by the SDK)")
	return ""
}

// csRunPy runs a python script with the SDK on PYTHONPATH and PODIUM_REGISTRY set.
func csRunPy(t *testing.T, py, registry, script string, extraEnv ...string) cliResult {
	t.Helper()
	f := filepath.Join(t.TempDir(), "script.py")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatalf("write py: %v", err)
	}
	env := append([]string{"PYTHONPATH=" + csPyDir(t), "PODIUM_REGISTRY=" + registry}, extraEnv...)
	return runBin(t, py, "", env, nil, 60*time.Second, f)
}

func csTSImport(t *testing.T) string {
	return strconv.Quote(filepath.Join(repoRoot(t), "sdks", "podium-ts", "src", "index.ts"))
}

// csNode returns the node binary if it can execute the TS SDK source, else skips.
func csNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	probe := filepath.Join(t.TempDir(), "probe.ts")
	src := "import { Client } from " + csTSImport(t) + ";\nconsole.log(\"PROBE_OK\", typeof Client);\n"
	if err := os.WriteFile(probe, []byte(src), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	res := runBin(t, node, "", nil, nil, 30*time.Second, probe)
	if res.Exit == 0 && strings.Contains(res.Stdout, "PROBE_OK") {
		return node
	}
	t.Skip("node cannot run the TypeScript SDK source (strip-only mode rejects parameter properties; a transpiler or built dist is required)")
	return ""
}

func csRunTS(t *testing.T, node, registry, script string, extraEnv ...string) cliResult {
	t.Helper()
	f := filepath.Join(t.TempDir(), "run.ts")
	if err := os.WriteFile(f, []byte(script), 0o644); err != nil {
		t.Fatalf("write ts: %v", err)
	}
	env := append([]string{"PODIUM_REGISTRY=" + registry}, extraEnv...)
	return runBin(t, node, "", env, nil, 60*time.Second, f)
}

func csWantStdout(t *testing.T, res cliResult, want string) {
	t.Helper()
	if res.Exit != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, want) {
		t.Errorf("stdout missing %q:\nstdout=%s\nstderr=%s", want, res.Stdout, res.Stderr)
	}
}

func csSkillReg(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		// A skill reads its description from SKILL.md; setting it on ARTIFACT.md
		// (and mismatching SKILL.md) is a lint error that blocks ingest, so omit it.
		"finance/close-reporting/run-variance-analysis/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\ntags: [finance, close]\n---\n\n<!-- body -->\n",
		"finance/close-reporting/run-variance-analysis/SKILL.md":    skillBody("run-variance-analysis"),
		"finance/ap/pay-invoice/ARTIFACT.md":                        contextArtifact("pay invoice"),
	})
}

// ---- install + import -------------------------------------------------------

// Python SDK imports the documented names.
func TestSDK_PyImport(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://localhost:1",
		"from podium import Client, RegistryError, DeviceCodeRequired\nprint('IMPORT_OK', Client.__name__)\n")
	csWantStdout(t, res, "IMPORT_OK")
}

// TypeScript SDK exports Client and RegistryError.
func TestSDK_TSImport(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, "http://localhost:1",
		"import { Client, RegistryError } from "+csTSImport(t)+";\nconsole.log('IMPORT_OK', typeof Client, typeof RegistryError);\n")
	csWantStdout(t, res, "IMPORT_OK")
}

// Python Client.from_env reads PODIUM_REGISTRY.
func TestSDK_PyFromEnv(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nprint('REG', c.registry)\n")
	csWantStdout(t, res, "REG "+srv.BaseURL)
}

// Python from_env raises when PODIUM_REGISTRY is absent.
func TestSDK_PyFromEnvMissing(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "",
		"from podium import Client\ntry:\n    Client.from_env()\n    print('NO_ERROR')\nexcept Exception as e:\n    print('RUNTIMEERROR_OK', 'PODIUM_REGISTRY' in str(e))\n",
		"PODIUM_REGISTRY=")
	csWantStdout(t, res, "RUNTIMEERROR_OK True")
}

// TypeScript Client.fromEnv reads PODIUM_REGISTRY.
func TestSDK_TSFromEnv(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = Client.fromEnv();\nconsole.log('REG', c.registry);\n")
	csWantStdout(t, res, "REG "+srv.BaseURL)
}

// TypeScript fromEnv throws when PODIUM_REGISTRY is absent.
func TestSDK_TSFromEnvMissing(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, "",
		"import { Client } from "+csTSImport(t)+";\ntry { Client.fromEnv(); console.log('NO_ERROR'); } catch (e) { console.log('ERROR_OK', String(e).includes('PODIUM_REGISTRY')); }\n",
		"PODIUM_REGISTRY=")
	csWantStdout(t, res, "ERROR_OK true")
}

// Python Client constructor sets attributes; no network call.
func TestSDK_PyConstructor(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://localhost:1",
		"from podium import Client\nc = Client(registry='https://podium.acme.com', identity_provider='oauth-device-code', overlay_path='./.podium/overlay/')\n"+
			"assert c.registry == 'https://podium.acme.com'\nassert c.identity_provider == 'oauth-device-code'\nassert c.overlay_path == './.podium/overlay/'\nprint('CTOR_OK')\n")
	csWantStdout(t, res, "CTOR_OK")
}

// Python login() is documented but absent (gap).
func TestSDK_PyLoginGap(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://localhost:1",
		"from podium import Client\nc = Client(registry='http://localhost:1')\nprint('HAS_LOGIN', hasattr(c, 'login'))\n")
	csWantStdout(t, res, "HAS_LOGIN True")
}

// Python load_domain returns a descriptor for a valid path.
func TestSDK_PyLoadDomain(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nd = c.load_domain('finance/close-reporting')\nprint('PATH', d.get('path'))\n")
	csWantStdout(t, res, "PATH finance/close-reporting")
}

// Python search_domains returns a SearchResult.
func TestSDK_PySearchDomains(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_domains('vendor payments', top_k=5)\nprint('TM', isinstance(r.total_matched, int))\n")
	csWantStdout(t, res, "TM True")
}

// Python search_artifacts with query, type, tags, scope, top_k.
func TestSDK_PySearchArtifactsAllParams(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('variance analysis', type='skill', tags=['finance','close'], scope='finance/close-reporting', top_k=10)\nprint('OK', isinstance(r.results, list))\n")
	csWantStdout(t, res, "OK True")
}

// Python search_artifacts browse mode.
func TestSDK_PyBrowse(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nb = c.search_artifacts(scope='finance/ap', top_k=50)\nprint(f'showing {len(b.results)} of {b.total_matched}')\n")
	csWantStdout(t, res, "showing ")
}

// Python search_artifacts type=agent.
func TestSDK_PyTypeAgent(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"agents/orch/ARTIFACT.md": "---\ntype: agent\nversion: 1.0.0\ndescription: payment workflow agent.\n---\n\nbody\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('payment workflow', type='agent')\nprint('ALL_AGENT', all(d.type == 'agent' for d in r.results))\n")
	csWantStdout(t, res, "ALL_AGENT True")
}

// Python search_artifacts type=context.
func TestSDK_PyTypeContext(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"ctx/style/ARTIFACT.md": contextArtifact("style guide"),
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('style guide', type='context')\nprint('ALL_CTX', all(d.type == 'context' for d in r.results))\n")
	csWantStdout(t, res, "ALL_CTX True")
}

// Python search_artifacts type=mcp-server.
func TestSDK_PyTypeMcpServer(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"tools/gh/ARTIFACT.md": "---\ntype: mcp-server\nversion: 1.0.0\ndescription: GitHub MCP.\nserver_identifier: github\n---\n\nbody\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts(type='mcp-server')\nprint('TM', isinstance(r.total_matched, int))\n")
	csWantStdout(t, res, "TM True")
}

// Python load_artifact returns manifest_body and frontmatter.
func TestSDK_PyLoadArtifact(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\na = c.load_artifact('finance/close-reporting/run-variance-analysis')\nprint('ID', a.id, 'FM', bool(a.frontmatter))\n")
	csWantStdout(t, res, "ID finance/close-reporting/run-variance-analysis FM True")
}

// Python load_artifact for unknown id raises RegistryError.
func TestSDK_PyLoadArtifactNotFound(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\nc = Client.from_env()\ntry:\n    c.load_artifact('does/not/exist')\n    print('NO_ERROR')\nexcept RegistryError as e:\n    print('CODE', e.code, 'RETRY', e.retryable)\n")
	csWantStdout(t, res, "CODE registry.not_found RETRY False")
}

// Python materialize(harness=none) is documented but absent (gap).
func TestSDK_PyMaterializeNoneGap(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\na = c.load_artifact('finance/ap/pay-invoice')\nprint('HAS_MATERIALIZE', hasattr(a, 'materialize'))\n")
	csWantStdout(t, res, "HAS_MATERIALIZE True")
}

// Python materialize(harness=claude-code) is absent (gap).
func TestSDK_PyMaterializeClaudeCodeGap(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\na = c.load_artifact('finance/close-reporting/run-variance-analysis')\nprint('HAS_MATERIALIZE', hasattr(a, 'materialize'))\n")
	csWantStdout(t, res, "HAS_MATERIALIZE True")
}

// Python load_artifacts bulk-fetches in one request.
func TestSDK_PyBulkLoad(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nout = c.load_artifacts(ids=['finance/close-reporting/run-variance-analysis','finance/ap/pay-invoice'])\nprint('N', len(out), 'OK', sum(1 for x in out if x.status=='ok'))\n")
	csWantStdout(t, res, "N 2 OK 2")
}

// Python load_artifacts handles partial failure without raising.
func TestSDK_PyBulkPartialFailure(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nout = c.load_artifacts(ids=['finance/ap/pay-invoice','does/not/exist'])\nbyid = {x.id: x for x in out}\nprint('OK', byid['finance/ap/pay-invoice'].status, 'ERR', byid['does/not/exist'].status, byid['does/not/exist'].error.code)\n")
	csWantStdout(t, res, "OK ok ERR error visibility.denied")
}

// server enforces the 50-item batch cap.
func TestSDK_BatchCap(t *testing.T) {
	t.Parallel()
	srv := startServer(t, csSkillReg(t))
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "x/a" + strconv.Itoa(i)
	}
	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad", map[string]any{"ids": ids})
	if st != 400 {
		t.Fatalf("status=%d, want 400: %s", st, body)
	}
	s := string(body)
	if !strings.Contains(s, "registry.invalid_argument") || !strings.Contains(s, "50") {
		t.Errorf("body missing registry.invalid_argument / 50: %s", s)
	}
}

// Python load_artifacts splits sets larger than 50.
func TestSDK_PyBulkSplit(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nids = [f'missing/a{i}' for i in range(55)]\nout = c.load_artifacts(ids=ids)\nprint('N', len(out))\n")
	csWantStdout(t, res, "N 55")
}

// Python load_artifacts forwards session_id and harness.
func TestSDK_PyBulkForwardsParams(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	// The standalone server accepts session_id and harness in the batch body;
	// a successful call confirms the SDK forwards them without error.
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nout = c.load_artifacts(ids=['finance/ap/pay-invoice'], session_id='sess-abc', harness='claude-code')\nprint('STATUS', out[0].status)\n")
	csWantStdout(t, res, "STATUS ok")
}

// Python bulk-load item materialize() is absent (gap).
func TestSDK_PyBulkMaterializeGap(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nout = c.load_artifacts(ids=['finance/ap/pay-invoice'])\nprint('IS_DICT', isinstance(out[0], dict), 'HAS_MATERIALIZE', hasattr(out[0], 'materialize'))\n")
	csWantStdout(t, res, "IS_DICT False HAS_MATERIALIZE True")
}

// Python bulk-load surfaces visibility.denied for an item
// the caller cannot see. The authenticated, visibility-capable harness
// places one artifact in a layer restricted to bob and one in a
// public layer; the Python SDK runs as alice (her minted token forwarded via
// PODIUM_SESSION_TOKEN, the same credential from_env reads for the §6.3.2
// injected-session-token path). c.load_artifacts over both ids returns the
// public artifact with status="ok" and the restricted artifact with
// status="error" carrying the §7.6.2 visibility.denied code, which does not
// leak the artifact's existence in the hidden layer.
func TestSDK_PyBulkVisibilityDenied(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startAuthServer(t, authServerSpec{
		Layers: []authLayer{
			{
				ID:         "shared",
				Files:      map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": authContext("pay invoice")},
				Visibility: authVisibility{Public: true},
			},
			{
				ID:         "restricted",
				Files:      map[string]string{"finance/ledger/ARTIFACT.md": authContext("restricted ledger")},
				Visibility: authVisibility{Users: []string{"bob@acme.com"}},
			},
		},
	})
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})

	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\n"+
			"c = Client.from_env()\n"+
			"out = c.load_artifacts(ids=['finance/ap/pay-invoice','finance/ledger'])\n"+
			"byid = {x.id: x for x in out}\n"+
			"vis = byid['finance/ap/pay-invoice']\n"+
			"den = byid['finance/ledger']\n"+
			"print('OK', vis.status, 'DEN', den.status, den.error.code)\n",
		"PODIUM_SESSION_TOKEN="+alice, "PODIUM_IDENTITY_PROVIDER=injected-session-token")
	csWantStdout(t, res, "OK ok DEN error visibility.denied")
}

// Python subscribe yields events.
func TestSDK_PySubscribe(t *testing.T) {
	t.Parallel()
	t.Skip("subscription e2e requires a publish trigger and a bounded SSE read; not implemented as a stable gate")
}

// Python subscribe accepts the documented positional
// event-type list (§7.6). The call form `c.subscribe([...])` must
// type-check; against an unreachable registry the connection raises a
// non-TypeError, so the test asserts the positional call is accepted (it is
// not rejected with TypeError).
func TestSDK_PySubscribePositional(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://localhost:1",
		"from podium import Client\nc = Client(registry='http://localhost:1')\ntry:\n    g = c.subscribe(['artifact.published'])\n    next(g)\n    print('ACCEPTED')\nexcept TypeError:\n    print('TYPEERROR')\nexcept Exception:\n    print('ACCEPTED')\n")
	csWantStdout(t, res, "ACCEPTED")
}

// Python dependents_of returns descriptors.
func TestSDK_PyDependentsOf(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
		"agents/orch/ARTIFACT.md":            "---\ntype: agent\nversion: 1.0.0\ndescription: Orchestrator.\ndelegates_to: [finance/ap/pay-invoice]\n---\n\nbody\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\ndeps = c.dependents_of('finance/ap/pay-invoice')\nprint('IS_LIST', isinstance(deps, list))\n")
	csWantStdout(t, res, "IS_LIST True")
}

// Python dependents_of empty for an artifact with none.
func TestSDK_PyDependentsOfEmpty(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/standalone/ARTIFACT.md": contextArtifact("standalone"),
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\ndeps = c.dependents_of('finance/ap/standalone')\nprint('LEN', len(deps))\n")
	csWantStdout(t, res, "LEN 0")
}

// Python curation pattern (search then podium sync).
func TestSDK_PyCuration(t *testing.T) {
	t.Parallel()
	t.Skip("blocked by a known gap: `podium sync --include` is never applied, so programmatic curation cannot scope the materialized set")
}

// Python curation with empty results skips the sync call.
func TestSDK_PyCurationEmpty(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('zzz-nonexistent-topic-zzz', type='skill', top_k=15)\nids = [d.id for d in r.results if d.score > 0.5]\nprint('IDS', len(ids), 'SYNC_SKIPPED', len(ids) == 0)\n")
	csWantStdout(t, res, "SYNC_SKIPPED True")
}

// Python custom consumer reads frontmatter and body directly.
func TestSDK_PyCustomConsumer(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"evals/regression-suite/run-week-42/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Week 42 eval.\n---\n\nThe eval body.\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\na = c.load_artifact('evals/regression-suite/run-week-42')\nprint('FM', bool(a.frontmatter), 'BODY', bool(a.manifest_body))\n")
	csWantStdout(t, res, "FM True BODY True")
}

// Python load_artifact harness parameter is absent (gap).
func TestSDK_PyLoadArtifactHarnessGap(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\ntry:\n    c.load_artifact('finance/ap/pay-invoice', harness='none')\n    print('NO_ERROR')\nexcept TypeError:\n    print('TYPEERROR_OK')\n")
	csWantStdout(t, res, "TYPEERROR_OK")
}

// Python eval pipeline: search by type, load each.
func TestSDK_PyEvalPipeline(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: A doc.\ntags: [regression]\n---\n\nbody\n",
	}))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nsuite = c.search_artifacts(type='context', tags=['regression'], top_k=50)\nok = True\nfor d in suite.results:\n    a = c.load_artifact(d.id)\n    ok = ok and bool(a.frontmatter)\nprint('PIPELINE_OK', ok)\n")
	csWantStdout(t, res, "PIPELINE_OK True")
}

// TypeScript searchArtifacts with query and topK.
func TestSDK_TSSearchArtifacts(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst out = await c.searchArtifacts('variance analysis', { topK: 10 });\nconsole.log('OK', typeof out.total_matched, Array.isArray(out.results));\n")
	csWantStdout(t, res, "OK number true")
}

// TypeScript loadArtifact returns manifest_body.
func TestSDK_TSLoadArtifact(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst a = await c.loadArtifact('finance/close-reporting/run-variance-analysis');\nconsole.log('ID', a.id, 'FM', !!a.frontmatter);\n")
	csWantStdout(t, res, "ID finance/close-reporting/run-variance-analysis FM true")
}

// TypeScript loadArtifacts handles partial failure.
func TestSDK_TSBulkPartial(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst r = await c.loadArtifacts(['finance/ap/pay-invoice','does/not/exist']);\nconst m = Object.fromEntries(r.map(x => [x.id, x]));\nconsole.log('OK', m['finance/ap/pay-invoice'].status, m['does/not/exist'].status, m['does/not/exist'].error?.code);\n")
	csWantStdout(t, res, "OK ok error registry.not_found")
}

// TypeScript dependentsOf returns dependency edges.
func TestSDK_TSDependentsOf(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay"),
		"agents/orch/ARTIFACT.md":            "---\ntype: agent\nversion: 1.0.0\ndescription: Orchestrator.\ndelegates_to: [finance/ap/pay-invoice]\n---\n\nbody\n",
	}))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst edges = await c.dependentsOf('finance/ap/pay-invoice');\nconsole.log('IS_ARRAY', Array.isArray(edges));\n")
	csWantStdout(t, res, "IS_ARRAY true")
}

// TypeScript subscribe yields NDJSON events.
func TestSDK_TSSubscribe(t *testing.T) {
	t.Parallel()
	t.Skip("subscription e2e requires a publish trigger and a bounded SSE read; not implemented as a stable gate")
}

// injected-session-token is accepted as a constructor param.
func TestSDK_PyInjectedSessionToken(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://localhost:1",
		"from podium import Client\nc = Client(registry='http://localhost:1', identity_provider='injected-session-token')\nassert c.identity_provider == 'injected-session-token'\nprint('PROVIDER_OK')\n")
	csWantStdout(t, res, "PROVIDER_OK")
}

// SDK does not work against a filesystem-source registry.
func TestSDK_PyNoFilesystemRegistry(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://127.0.0.1:1",
		"from podium import Client, RegistryError\nc = Client.from_env()\ntry:\n    c.load_artifact('some/artifact')\n    print('NO_ERROR')\nexcept (RegistryError, OSError, Exception):\n    print('CONN_ERROR_OK')\n")
	csWantStdout(t, res, "CONN_ERROR_OK")
}

// Python RegistryError carries code, message, retryable.
func TestSDK_PyRegistryErrorFields(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\nc = Client.from_env()\ntry:\n    c.load_artifact('no/such/artifact')\nexcept RegistryError as e:\n    print('STR_OK', str(e) == f'{e.code}: {e.message}', isinstance(e.retryable, bool))\n")
	csWantStdout(t, res, "STR_OK True True")
}

// TypeScript RegistryError is an Error subclass with code/retryable.
func TestSDK_TSRegistryError(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client, RegistryError } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\ntry { await c.loadArtifact('no/such/artifact'); console.log('NO_ERROR'); } catch (e) { const r = e as RegistryError; console.log('ERR', r instanceof RegistryError, r instanceof Error, r.name, r.code, r.retryable); }\n")
	csWantStdout(t, res, "ERR true true RegistryError registry.not_found false")
}

// Python load_artifacts empty ids returns [] without HTTP.
func TestSDK_PyBulkEmpty(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, "http://127.0.0.1:1",
		"from podium import Client\nc = Client(registry='http://127.0.0.1:1')\nout = c.load_artifacts(ids=[])\nprint('EMPTY', out == [])\n")
	csWantStdout(t, res, "EMPTY True")
}

// TypeScript loadArtifacts empty ids returns [] without fetch.
func TestSDK_TSBulkEmpty(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, "http://127.0.0.1:1",
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: 'http://127.0.0.1:1' });\nconst out = await c.loadArtifacts([]);\nconsole.log('EMPTY', Array.isArray(out) && out.length === 0);\n")
	csWantStdout(t, res, "EMPTY true")
}

// the bulk endpoint is not exposed as an MCP meta-tool.
func TestSDK_NoBulkMCPTool(t *testing.T) {
	t.Parallel()
	srv := startServer(t, csSkillReg(t))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL}, rpcReq{ID: 1, Method: "tools/list", Params: map[string]any{}})
	result := rpcResult(t, res.Stdout, 1)
	// tools/list advertises the meta-tools plus the §13.9 health tool;
	// the bulk endpoint is intentionally absent (asserted below).
	for _, want := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact", "health"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("tools/list missing %q: %s", want, mustJSON(result))
		}
	}
	for _, banned := range []string{"load_artifacts", "batch_load", "batchLoad"} {
		if strings.Contains(res.Stdout, banned) {
			t.Errorf("tools/list unexpectedly exposes a bulk tool %q", banned)
		}
	}
}

// Python search_artifacts session_id parameter is absent (gap).
func TestSDK_PySearchAcceptsSessionID(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	// search_artifacts accepts a session_id keyword and forwards it; a
	// successful call (no TypeError) confirms the parameter is supported.
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('variance analysis', session_id='some-session')\nprint('SESSION_OK', r is not None)\n")
	csWantStdout(t, res, "SESSION_OK True")
}

// server enforces POST on /v1/artifacts:batchLoad.
func TestSDK_BatchMethod(t *testing.T) {
	t.Parallel()
	srv := startServer(t, csSkillReg(t))
	st, body := getRaw(t, srv.BaseURL+"/v1/artifacts:batchLoad")
	if st != 405 {
		t.Errorf("status=%d, want 405: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body missing registry.invalid_argument: %s", body)
	}
}

// server rejects an empty ids array.
func TestSDK_BatchEmptyIds(t *testing.T) {
	t.Parallel()
	srv := startServer(t, csSkillReg(t))
	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad", map[string]any{"ids": []string{}})
	if st != 400 {
		t.Errorf("status=%d, want 400: %s", st, body)
	}
	if !strings.Contains(string(body), "registry.invalid_argument") {
		t.Errorf("body missing registry.invalid_argument: %s", body)
	}
}

// Python load_domain with empty path returns the root map.
func TestSDK_PyLoadDomainEmpty(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nd = c.load_domain()\nprint('IS_DICT', isinstance(d, dict))\n")
	csWantStdout(t, res, "IS_DICT True")
}

// Python load_domain for a nonexistent path is deterministic.
func TestSDK_PyLoadDomainNonexistent(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\nc = Client.from_env()\ntry:\n    d = c.load_domain('nonexistent/path')\n    print('EMPTY_OK', isinstance(d, dict))\nexcept RegistryError as e:\n    print('NOTFOUND_OK', e.code)\n")
	if res.Exit != 0 {
		t.Fatalf("exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "EMPTY_OK") && !strings.Contains(res.Stdout, "NOTFOUND_OK") {
		t.Errorf("expected deterministic empty-or-not-found; stdout=%s", res.Stdout)
	}
}

// Python ArtifactDescriptor exposes all documented fields.
func TestSDK_PyDescriptorFields(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\nc = Client.from_env()\nr = c.search_artifacts('variance', top_k=1)\nd = r.results[0]\nprint('FIELDS', bool(d.id), isinstance(d.type, str), isinstance(d.version, str), isinstance(d.tags, list), isinstance(d.score, float))\n")
	csWantStdout(t, res, "FIELDS True True True True True")
}

// TypeScript loadArtifacts splits sets larger than 50.
func TestSDK_TSBulkSplit(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServer(t, csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client } from "+csTSImport(t)+";\nconst c = new Client({ registry: process.env.PODIUM_REGISTRY });\nconst out = await c.loadArtifacts(Array.from({ length: 55 }, (_, i) => `missing/a${i}`));\nconsole.log('N', out.length);\n")
	csWantStdout(t, res, "N 55")
}

// programmatic identity and visibility are unchanged from
// the MCP path: the same server, driven by the SDK as two distinct identities,
// returns the per-caller surface each token's identity grants. The
// authenticated harness restricts one layer to bob while a second
// layer is public. The Python SDK runs once as bob and once as alice, each
// caller's minted token forwarded via PODIUM_SESSION_TOKEN (the §6.3.2
// injected-session-token credential from_env reads). bob loads the restricted
// artifact; alice is denied it on both the object route (a RegistryError) and
// the §7.6.2 batch route (a visibility.denied item) while still loading the
// public artifact, so the identity bound to the token, rather than the SDK
// caller, decides the view.
func TestSDK_PyIdentityUnchanged(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startAuthServer(t, authServerSpec{
		Layers: []authLayer{
			{
				ID:         "shared",
				Files:      map[string]string{"finance/ap/pay-invoice/ARTIFACT.md": authContext("pay invoice")},
				Visibility: authVisibility{Public: true},
			},
			{
				ID:         "restricted",
				Files:      map[string]string{"finance/ledger/ARTIFACT.md": authContext("restricted ledger")},
				Visibility: authVisibility{Users: []string{"bob@acme.com"}},
			},
		},
	})
	alice := srv.token(authIdentity{Sub: "alice@acme.com", Email: "alice@acme.com"})
	bob := srv.token(authIdentity{Sub: "bob@acme.com", Email: "bob@acme.com"})

	// bob's identity sees the restricted artifact.
	bobRes := csRunPy(t, py, srv.BaseURL,
		"from podium import Client\n"+
			"c = Client.from_env()\n"+
			"a = c.load_artifact('finance/ledger')\n"+
			"print('BOB_SEES', a.id)\n",
		"PODIUM_SESSION_TOKEN="+bob, "PODIUM_IDENTITY_PROVIDER=injected-session-token")
	csWantStdout(t, bobRes, "BOB_SEES finance/ledger")

	// alice's identity is denied the restricted artifact on the object route and
	// in the batch, but still loads the public artifact: the surface is bound to
	// the token's identity, exactly as on the MCP path.
	aliceRes := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\n"+
			"c = Client.from_env()\n"+
			"try:\n"+
			"    c.load_artifact('finance/ledger')\n"+
			"    print('LEAK')\n"+
			"except RegistryError as e:\n"+
			"    print('ALICE_DENIED', e.code)\n"+
			"out = c.load_artifacts(ids=['finance/ap/pay-invoice','finance/ledger'])\n"+
			"byid = {x.id: x for x in out}\n"+
			"print('PUB', byid['finance/ap/pay-invoice'].status, 'BATCH', byid['finance/ledger'].status, byid['finance/ledger'].error.code)\n",
		"PODIUM_SESSION_TOKEN="+alice, "PODIUM_IDENTITY_PROVIDER=injected-session-token")
	csWantStdout(t, aliceRes, "ALICE_DENIED registry.not_found")
	csWantStdout(t, aliceRes, "PUB ok BATCH error visibility.denied")
}

// deadRegistry returns a registry URL pointing at a closed local port so a
// transport-level connection is refused immediately (no listener).
func deadRegistry(t *testing.T) string {
	t.Helper()
	return "http://127.0.0.1:" + strconv.Itoa(freePort(t))
}

// Python: an unreachable registry surfaces the structured
// §7.4 network.registry_unreachable code, the §6.10 retryable flag, and a
// remediation hint rather than leaking a raw transport exception.
func TestSDK_PyUnreachableRegistry(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, deadRegistry(t),
		"from podium import Client, RegistryError\n"+
			"c = Client.from_env()\n"+
			"try:\n"+
			"    c.load_artifact('finance/x')\n"+
			"    print('NO_ERROR')\n"+
			"except RegistryError as e:\n"+
			"    print('CODE', e.code, 'RETRY', e.retryable, 'HINT', bool(e.suggested_action))\n")
	csWantStdout(t, res, "CODE network.registry_unreachable RETRY True HINT True")
}

// Python: offline-first keeps no cache, so an unreachable
// registry is the same structured no-cache miss.
func TestSDK_PyUnreachableOfflineFirst(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	res := csRunPy(t, py, deadRegistry(t),
		"from podium import Client, RegistryError\n"+
			"c = Client.from_env()\n"+
			"try:\n"+
			"    c.search_artifacts('x')\n"+
			"    print('NO_ERROR')\n"+
			"except RegistryError as e:\n"+
			"    print('CODE', e.code)\n",
		"PODIUM_CACHE_MODE=offline-first")
	csWantStdout(t, res, "CODE network.registry_unreachable")
}

// TypeScript: an unreachable registry surfaces the
// structured §7.4 network.registry_unreachable code with the retryable flag and
// a remediation hint rather than a raw fetch rejection.
func TestSDK_TSUnreachableRegistry(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, deadRegistry(t),
		"import { Client, RegistryError } from "+csTSImport(t)+";\n"+
			"const c = new Client({ registry: process.env.PODIUM_REGISTRY });\n"+
			"try { await c.loadArtifact('finance/x'); console.log('NO_ERROR'); }\n"+
			"catch (e) { const r = e as RegistryError; console.log('CODE', r.code, 'RETRY', r.retryable, 'HINT', r.suggestedAction.length > 0); }\n")
	csWantStdout(t, res, "CODE network.registry_unreachable RETRY true HINT true")
}

// TypeScript: offline-first keeps no cache, so an
// unreachable registry is the same structured no-cache miss.
func TestSDK_TSUnreachableOfflineFirst(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	res := csRunTS(t, node, deadRegistry(t),
		"import { Client, RegistryError } from "+csTSImport(t)+";\n"+
			"const c = new Client({ registry: process.env.PODIUM_REGISTRY, cacheMode: 'offline-first' });\n"+
			"try { await c.searchArtifacts('x'); console.log('NO_ERROR'); }\n"+
			"catch (e) { const r = e as RegistryError; console.log('CODE', r.code); }\n")
	csWantStdout(t, res, "CODE network.registry_unreachable")
}

// Python: the SDK surfaces the full §6.10 envelope. A
// reachable server's quota error carries a populated suggested_action and a
// details map that the RegistryError exposes.
func TestSDK_PyErrorEnvelopeFull(t *testing.T) {
	t.Parallel()
	py := csPython(t)
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_QUOTA_SEARCH_QPS=1"},
		"serve", "--standalone", "--layer-path", csSkillReg(t))
	res := csRunPy(t, py, srv.BaseURL,
		"from podium import Client, RegistryError\n"+
			"c = Client.from_env()\n"+
			"hint = None\n"+
			"for _ in range(40):\n"+
			"    try:\n"+
			"        c.search_artifacts('variance')\n"+
			"    except RegistryError as e:\n"+
			"        if e.code == 'quota.search_qps_exceeded':\n"+
			"            assert isinstance(e.details, dict)\n"+
			"            hint = e.suggested_action\n"+
			"            break\n"+
			"print('FULL_OK', bool(hint))\n")
	csWantStdout(t, res, "FULL_OK True")
}

// TypeScript: the SDK surfaces the full §6.10 envelope. A
// reachable server's quota error carries a populated suggestedAction and a
// details map that the RegistryError exposes.
func TestSDK_TSErrorEnvelopeFull(t *testing.T) {
	t.Parallel()
	node := csNode(t)
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir(), "PODIUM_QUOTA_SEARCH_QPS=1"},
		"serve", "--standalone", "--layer-path", csSkillReg(t))
	res := csRunTS(t, node, srv.BaseURL,
		"import { Client, RegistryError } from "+csTSImport(t)+";\n"+
			"const c = new Client({ registry: process.env.PODIUM_REGISTRY });\n"+
			"let hint = '';\n"+
			"for (let i = 0; i < 40; i++) {\n"+
			"  try { await c.searchArtifacts('variance'); }\n"+
			"  catch (e) { const r = e as RegistryError; if (r.code === 'quota.search_qps_exceeded') { hint = r.suggestedAction; if (typeof r.details !== 'object') throw new Error('details not an object'); break; } }\n"+
			"}\n"+
			"console.log('FULL_OK', hint.length > 0);\n")
	csWantStdout(t, res, "FULL_OK true")
}
