# Proposal 0045: Remove the unread harness field from batchLoad and load_artifacts, and drop the nonexistent `reembed --all` flag from §4.7

- Issue: (to be filed)
- Status: Implemented (2026-10-03). Approved (2026-10-03) on the user's behalf under the overnight authorization and signed off as staged: a retired harness key is ignored, following the server's lenient JSON-decoding convention.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.6.2: the bulk-fetch example drops `harness="claude-code"`, the batchLoad wire format drops `harness?`, and one sentence states that the request selects no harness (SPEC-1). §7.6 loses the sentence that carved `load_artifacts` out of the `materialize()` harness rule (SPEC-2).
- §4.7: the "Model versioning and re-embedding" paragraph names the flagless `podium admin reembed` as the full pass and `--since` or `--only-missing` as scoped passes, in place of the nonexistent `--all` flag (SPEC-3).
- Registry server: `BatchLoadRequest` in `pkg/registry/server/batch_load.go` loses its unread `Harness` field (CODE-1).
- SDKs: Python `Client.load_artifacts` and TypeScript `Client.loadArtifacts` lose the `harness` argument and stop sending the key (CODE-2, CODE-3), with the unit and e2e tests updated to match (TEST-2, TEST-3, TEST-4).
- Stale `reembed --all` references: four Go comments and one e2e skip message (CODE-4, TEST-5).
- Docs and changelog: the HTTP API reference body example, the consumer SDK bulk-load example, and the `[Unreleased]` `Removed` and `Documentation` entries (DOC-1, DOC-2, CHANGELOG-1).

**Fixed decisions.**

- The `harness` field is removed from the batchLoad wire format, `BatchLoadRequest`, and both SDKs. Server-side adaptation is rejected.
- The change adds no shim, no deprecation period, and no dual path. The SDK argument disappears outright.
- A batchLoad body that still carries `harness` is ignored, because the server's plain JSON decoder ignores unknown keys. It is not rejected.
- The change adds no spec rule for unknown request-body fields and no §6.10 error code.
- No server test pins the ignore behavior (TEST-1 is dropped; see Non-goals).
- The MCP `load_artifact` harness argument (§6.7) is unchanged.
- The rest of the §7.6 `materialize()` paragraph stays byte-identical; only its last sentence is deleted.
- In §4.7 only the parenthetical about `--all` changes. Every other sentence of the paragraph stays byte-identical.
- `podium admin reembed` gains no `--all` flag, and the CLI behavior is unchanged.
- The lowercase `// spec: §4.7` tag at `cmd/podium/admin.go:205` becomes `// Spec: §4.7` because CODE-4 rewrites that comment. Lowercase tags on untouched lines stay.

**Watch out for.**

- **SPEC-1 and SPEC-2 land in one commit.** If SPEC-1 lands alone, §7.6 refers to a `load_artifacts` argument that no longer exists. If SPEC-2 lands alone, the §7.6.2 argument it excluded comes under the `materialize()` rule.
- **TEST-4 fails the moment CODE-2 lands.** `TestSDK_PyBulkForwardsParams` in `test/e2e/sdk_clients_test.go` passes `harness='claude-code'`. Without TEST-4 the script raises `TypeError`, prints no `STATUS ok`, and the test fails. CODE-2 and TEST-4 share step S4.
- **TEST-3 fails until CODE-3 lands.** Its exact-body assertion rejects the `harness` key the current client still sends. CODE-3 and TEST-3 share step S5.
- **The MCP `load_artifact` harness argument looks like the same defect and is out of scope.** The MCP server runs the adapter locally, so that override has an implementation. Do not touch `cmd/podium-mcp`, §6.7, or `docs/consuming/browsing-the-catalog.md`. The other `"harness": "claude-code"` uses under `test/e2e/` (`discovery_search_test.go`, `bundled_resources_test.go`) are MCP tool calls and stay.
- **SDK `materialize(harness=...)` is a different argument.** It stays and accepts only `none` (proposal 0044). Do not touch `LoadedArtifact.materialize`, `BatchResult.materialize`, or their tests.
- **`test/e2e/vector_backend_config_test.go` already asserts that `--all` exits 2.** It agrees with SPEC-3 and stays unchanged.
- **The TypeScript compile error covers only an object literal.** Vitest does not type-check (`sdks/podium-ts/package.json` runs `vitest run`), and an options variable typed elsewhere or a plain JavaScript caller compiles. The runtime guarantee is the allowlisted body, which TEST-3 pins.
- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1, SPEC-2. §7.6.2 drops `harness` from the example and the wire format, and §7.6 deletes its `load_artifacts` carve-out. Bundled because both edit `spec/07-external-integration.md` and must land together.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-3. §4.7 replaces the `--all` parenthetical with the flagless full pass and the scoped passes.
      Levels: —. Depends on: —
- [x] **S3 · code** — CODE-1. `BatchLoadRequest` loses `Harness`, and its doc comment states that the request selects no harness.
      Levels: unit, e2e. Depends on: S1
- [x] **S4 · code** — CODE-2, TEST-2, TEST-4. Python `load_artifacts` loses `harness`; the unit test pins the body keys and the e2e test drops the argument. Bundled because TEST-4 fails as soon as CODE-2 lands.
      Levels: unit, e2e. Depends on: S1
- [x] **S5 · code** — CODE-3, TEST-3. TypeScript `loadArtifacts` loses `harness`; the unit test pins the body allowlist. Bundled because TEST-3 fails until CODE-3 lands.
      Levels: unit. Depends on: S1
- [x] **S6 · code** — CODE-4. The four stale `reembed --all` comments name the flagless full pass, and the `cmd/podium/admin.go` tag becomes `// Spec: §4.7`.
      Levels: —. Depends on: S2
- [x] **S7 · test** — TEST-5. The `TestCLI_AdminReembed` skip message drops the `--all` clause.
      Levels: e2e. Depends on: S2
- [x] **S8 · docs** — DOC-1. The HTTP API reference body example drops `harness`.
      Levels: —. Depends on: S3
- [x] **S9 · docs** — DOC-2. The consumer SDK bulk-load example drops `harness` and its false comment.
      Levels: —. Depends on: S4
- [x] **S10 · docs** — CHANGELOG-1. The `[Unreleased]` `Removed` and `Documentation` entries.
      Levels: —. Depends on: S3, S4, S5, S6

**Ordering constraints.** The batchLoad chain (S1, S3, S4, S5, S8, S9) and the reembed chain (S2, S6, S7) are independent and may proceed in parallel. S3, S4, and S5 are independent of each other. S10 follows both chains because its entries describe the SDK, server, and §4.7 changes together.

## Current state and the gap

Proposal 0044 deferred two items, its OQ-1 and OQ-2. This proposal resolves both.

### The batchLoad harness field

§7.6.2 still lists a harness override on bulk load. The `load_artifacts` example passes `harness="claude-code"` with the comment `# optional per-call adapter override` (`spec/07-external-integration.md:689`), and the wire format lists `harness?` in the `POST /v1/artifacts:batchLoad` body (`spec/07-external-integration.md:699`). §7.6 excludes this argument from the 0044 canonical-layout rule by name: "This rule does not cover the `harness` argument of `load_artifacts` (§7.6.2)" (`spec/07-external-integration.md:616`).

The server decodes the field into `BatchLoadRequest.Harness` (`pkg/registry/server/batch_load.go:25`) and never reads it. `handleBatchLoad` (`batch_load.go:80-107`) reads only `req.IDs`, `req.VersionPins`, and `req.SessionID`. `loadOneForBatch` (`batch_load.go:109-113`) passes only `Version` and `SessionID` to `core.LoadArtifactOptions`, which has no harness field. The registry server does not import `pkg/adapter`. §2.2, as amended by 0044, says the SDKs run no harness adapter and that adapters run at delivery time, in the MCP server and in `podium sync`. Nothing on the SDK path can consume a harness-specific response, because SDK `materialize()` accepts only `none` (§7.6). The field is a promise with no implementation and no consumer.

Both SDKs expose the argument and send it. Python `load_artifacts` takes `harness: str = ""` and sets `body["harness"]` (`sdks/podium-py/podium/client.py:1448`, `:1470-1471`). TypeScript `loadArtifacts` takes `opts.harness` and sets `body.harness` (`sdks/podium-ts/src/index.ts:1377`, `:1390`). The docs repeat the promise in two places. `docs/reference/http-api.md:261` shows `"harness": "claude-code"` in the batchLoad body. `docs/consuming/custom-via-sdk.md:114` passes `harness="claude-code"` with a comment saying the value is "recorded on the request". The comment is false: the server neither records, logs, nor echoes the field. The e2e test `TestSDK_PyBulkForwardsParams` (`test/e2e/sdk_clients_test.go:369-379`) pins the forwarding.

The server decodes JSON bodies leniently. `handleBatchLoad` uses a plain `json.NewDecoder(r.Body).Decode` (`batch_load.go:87`). No non-test code under `pkg/`, `cmd/`, or `internal/` calls `DisallowUnknownFields`. The presence-decoding handlers (`pkg/registry/server/tenants.go:194-224` and `pkg/registry/server/layers.go:655-675`) read only the keys they know and do not reject extra ones. The spec has no unknown-field rule for request bodies.

### The `reembed --all` flag

The §4.7 "Model versioning and re-embedding" paragraph says the re-embed runs via `podium admin reembed` (`--all` or `--since <timestamp>`) (`spec/04-artifact-model.md:770`). The CLI defines only `--registry`, `--artifact`, `--version`, `--only-missing`, and `--since` (`cmd/podium/admin.go:174-181`). A run with no flags is the tenant-wide pass (`cmd/podium/admin.go:203-212`, `docs/reference/cli.md:576-593`). `test/e2e/vector_backend_config_test.go:579-590` already asserts that `--all` exits 2. In `spec/` and `docs/`, line 770 is the only mention of `--all` for reembed. Stale references remain in four Go comments (`cmd/podium/admin.go:205`, `pkg/registry/core/reembed.go:36`, `pkg/vector/vector.go:19`, and `pkg/vector/pgvector.go:29`) and in one e2e skip message (`test/e2e/cli_reference_test.go:1751`).

## Decisions

- **Remove the field.** The `harness` field leaves the §7.6.2 batchLoad request, `BatchLoadRequest`, and both SDKs' `load_artifacts` and `loadArtifacts`. Implementing server-side adaptation was considered and rejected. It contradicts §2.2, under which adapters run at delivery time in the MCP server and `podium sync`, and the SDK `materialize()` that would receive its output accepts only `none`.
- **No shim.** Under the pre-1.0 policy there is no deprecation period and no dual path. Python callers that still pass `harness=` get a `TypeError` from the keyword-only signature. TypeScript callers that pass it in an options object literal get a compile error on the unknown key.
- **A retired `harness` key is ignored rather than rejected.** This follows the server's existing convention: no handler uses `DisallowUnknownFields` or rejects unknown or retired JSON keys (`pkg/registry/server/batch_load.go:87`, `pkg/registry/server/tenants.go:194-224`, `pkg/registry/server/layers.go:655-675`). Rejecting the key would need a field-specific check that no other handler has. Ignoring it also keeps older SDK releases working against a new registry, because those releases still send the field and it never had an effect. The CHANGELOG entry states this as the existing decoder behavior.
- **No spec rule for unknown request-body fields.** The spec has none today, and a batchLoad-only rule would create a one-off normative surface. The §7.6.2 wire format lists the accepted fields, and that list is the contract.
- **No new §6.10 error code.** The ignore rule needs none, and `registry.invalid_argument` stays unused for this case.
- **Delete the §7.6 carve-out sentence.** The argument it excluded no longer exists. The §7.6 `materialize()` rule itself is unchanged.
- **Leave the MCP `load_artifact` harness argument alone.** The MCP server runs the adapter locally (§6.7), so that override has an implementation.
- **Replace only the §4.7 parenthetical.** The new wording names the flagless invocation as the full tenant-wide pass and `--since` or `--only-missing` as scoped passes. This matches the CLI and the paragraph's own later "full pass" and "scoped with `--since` or `--only-missing`" sentences. The rest of the paragraph that 0044 converged stays byte-identical.
- **Fix every stale `reembed --all` reference outside `spec/` in the same change.** These are Go comments and one test skip message, so the fix changes no behavior. The existing test that asserts `--all` exits 2 already agrees and stays as it is.
- **Normalize one spec tag in passing.** The lowercase `// spec: §4.7` tag at `cmd/podium/admin.go:205` becomes `// Spec: §4.7` while that comment is rewritten, per `.claude/rules/code-best-practices.md`. Lowercase tags in untouched lines stay.

## Spec amendment: §7.6.2 Bulk Fetch

**SPEC-1.** `spec/07-external-integration.md`, §7.6.2 "Bulk Fetch". Two anchors, landed in the same commit as SPEC-2.

**Anchor 1, the `load_artifacts` example** (lines 681-691 at the time of writing). Delete the line:

```python
    harness="claude-code",        # optional per-call adapter override
```

so that the call reads:

> ```python
> artifacts = client.load_artifacts(
>     ids=[
>         "finance/close-reporting/run-variance-analysis",
>         "finance/close-reporting/policy-doc",
>         "finance/ap/pay-invoice",
>     ],
>     session_id=session_id,        # honors the same `latest`-resolution semantics as load_artifact
> )
> ```

The `for result in artifacts:` loop that follows is unchanged.

**Anchor 2, the wire-format paragraph** (line 699 at the time of writing). Replace:

> **Wire format.** `POST /v1/artifacts:batchLoad` with body `{ids: [...], session_id?, harness?, version_pins?: {<id>: <semver>}}`. Response is an array of per-item envelopes:

with:

> **Wire format.** `POST /v1/artifacts:batchLoad` with body `{ids: [...], session_id?, version_pins?: {<id>: <semver>}}`. The request selects no harness, and each `ok` item carries the canonical manifest and resources (§7.6). Response is an array of per-item envelopes:

The JSON response block that follows the colon is unchanged.

## Spec amendment: §7.6 SDK materialize and the canonical layout

**SPEC-2.** `spec/07-external-integration.md`, §7.6 "Language SDKs", the paragraph that begins "`materialize()` writes the artifact under `<to>/<id>/` in the canonical layout" (line 616 at the time of writing). Delete its final sentence:

> This rule does not cover the `harness` argument of `load_artifacts` (§7.6.2).

The paragraph then ends with "On a §7.6.2 bulk-load item the argument is checked before the item's status, so an `error` item called with an invalid `harness` raises the argument error rather than the item's registry error." Every other sentence of the paragraph stays byte-identical.

## Spec amendment: §4.7 Model versioning and re-embedding

**SPEC-3.** `spec/04-artifact-model.md`, §4.7, the paragraph that begins "**Model versioning and re-embedding.**" (line 770 at the time of writing). Replace:

> the registry triggers a background re-embed via `podium admin reembed` (`--all` or `--since <timestamp>`).

with:

> the registry triggers a background re-embed via `podium admin reembed`, which with no flags runs a full pass over the tenant and with `--since <timestamp>` or `--only-missing` runs a scoped pass.

Every other sentence of the paragraph stays byte-identical, including "Once a full pass completes, the previous model's rows are purged from a collocated store." and "A pass scoped with `--since` or `--only-missing` purges nothing."

## Proposed solution

### CODE-1. Server: remove `BatchLoadRequest.Harness`

`pkg/registry/server/batch_load.go`.

- Delete the ``Harness string `json:"harness,omitempty"` `` field from `BatchLoadRequest` and run `gofmt` to realign the remaining fields.
- Replace the doc comment ("BatchLoadRequest is the wire shape of POST /v1/artifacts:batchLoad.") with:

  ```go
  // BatchLoadRequest is the §7.6.2 request body of POST
  // /v1/artifacts:batchLoad. The request selects no harness: the
  // registry runs no harness adapter (§2.2). Like the other JSON
  // handlers, the decoder ignores keys outside this struct, so a body
  // from an older SDK that still sends `harness` loads normally.
  //
  // Spec: §7.6.2
  ```

- Leave `handleBatchLoad` and `loadOneForBatch` unchanged. Neither reads the field.

### CODE-2. Python SDK: remove the `harness` parameter from `load_artifacts`

`sdks/podium-py/podium/client.py`, `Client.load_artifacts` (lines 1443-1471 at the time of writing).

- Remove `harness: str = "",` from the keyword-only signature.
- Remove the two lines `if harness:` and `body["harness"] = harness`.
- Keep `session_id` and `version_pins` unchanged. Add no `**kwargs`, so a stale `harness=` raises `TypeError` before any request is sent.

### CODE-3. TypeScript SDK: remove the `harness` option from `loadArtifacts`

`sdks/podium-ts/src/index.ts`, `Client.loadArtifacts` (lines 1369-1390 at the time of writing).

- Remove `harness?: string;` from the `opts` type.
- Remove `if (opts.harness) body.harness = opts.harness;`.
- Add one sentence to the method comment: "The request selects no harness (§7.6.2)."
- The body is built from an explicit allowlist (`ids`, `session_id`, `version_pins`), so a plain JavaScript caller that passes `harness` through a cast has the key dropped and never sent.

### CODE-4. Stale `reembed --all` comments

These are comment-only edits, and none changes behavior.

- `cmd/podium/admin.go:205-206`. Replace:

  ```go
  // spec: §4.7 — `--all` is the no-flag default; `--since` and
  // `--only-missing` scope a tenant-wide pass and compose.
  ```

  with:

  ```go
  // Spec: §4.7 — with no flags the command runs a full tenant-wide pass; `--since` and
  // `--only-missing` scope that pass and compose.
  ```

- `pkg/registry/core/reembed.go:36`. Replace `the §4.7 "podium admin reembed" default (--all).` with ``the full pass that a flagless `podium admin reembed` runs (§4.7).``
- `pkg/vector/vector.go:19`. Replace `` `podium admin reembed --all` `` with ``a full `podium admin reembed` pass (no flags)``.
- `pkg/vector/pgvector.go:29`. Replace ``a `podium admin reembed --all` `` with ``a full `podium admin reembed` pass``.

Run `gofmt` and `make lint` after the edits.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Python caller passes `harness=` to `load_artifacts` | `TypeError` before any request is sent | §7.6.2 example and wire format list no `harness` (SPEC-1); `CHANGELOG.md` (CHANGELOG-1) |
| TypeScript caller passes `harness` in an options object literal | Compile error on the unknown key | §7.6.2 (SPEC-1); CHANGELOG-1 |
| TypeScript caller passes `harness` through a cast, a typed variable, or plain JavaScript | Compiles; `loadArtifacts` drops the key and the request carries no `harness` (accepted) | §7.6.2 "The request selects no harness" (SPEC-1); CHANGELOG-1 "`loadArtifacts` never sends the key" |
| Older SDK release sends `harness` in the batchLoad body | The registry ignores the key and loads the items as before (accepted; existing decoder behavior, not pinned by a test) | §7.6.2 wire format lists the accepted fields (SPEC-1); no `docs/` page states it by decision; CHANGELOG-1 states it; the CODE-1 doc comment records it |
| Raw HTTP client sends `harness` in the batchLoad body | Same as the previous row | Same as the previous row; `docs/reference/http-api.md` lists the accepted body fields (DOC-1) |
| `ok` batch item | Canonical manifest and resources, as today | §7.6.2 "each `ok` item carries the canonical manifest and resources (§7.6)" (SPEC-1) |
| `materialize()` on a batch item with `harness` other than `none` | Argument error, as today | §7.6 (unchanged by SPEC-2); `docs/consuming/custom-via-sdk.md` (existing text) |
| MCP `load_artifact` with a `harness` argument | Adapter runs in the MCP server, as today | §6.7 (unchanged); `docs/consuming/browsing-the-catalog.md` (unchanged) |
| `podium admin reembed` with no flags | Full tenant-wide pass, as today | §4.7 "with no flags runs a full pass over the tenant" (SPEC-3); `docs/reference/cli.md` (existing text) |
| `podium admin reembed --since <timestamp>` or `--only-missing` | Scoped pass that purges nothing, as today | §4.7 "with `--since <timestamp>` or `--only-missing` runs a scoped pass" and "A pass scoped with `--since` or `--only-missing` purges nothing" (SPEC-3); `docs/reference/cli.md` (existing text) |
| `podium admin reembed --all` | Exit 2 with a flag-parse error, as today | §4.7 names no `--all` (SPEC-3); `docs/reference/cli.md` lists the accepted flags (existing text) |

## Testing

No new behavior is added, so no new test function is added. CODE-1 and CODE-4 only delete code or edit comments and create no new line under the 85% bar. The changes below update existing tests so they pin the narrowed contract.

**TEST-2 · unit, `sdks/podium-py/tests/test_client.py`.** Lands in S4 with CODE-2. Extend `test_load_artifacts_returns_envelopes` (lines 449-465 at the time of writing):

- Change the call to `client.load_artifacts(["a", "b"], session_id="s")`.
- Next to the existing `assert body["ids"] == ["a", "b"]`, add `assert set(body) == {"ids", "session_id"} and body["session_id"] == "s"`.
- Extend the existing `# Spec: §7.6.2` comment block with "the body carries ids and session_id and selects no harness."

This pins `session_id` forwarding, which no current unit test covers for `load_artifacts`, and checks that no `harness` key is sent. Add no `TypeError` assertion: Python's keyword-only signature produces that error, and no SDK code path runs for it.

**TEST-3 · unit, `sdks/podium-ts/src/index.test.ts`.** Lands in S5 with CODE-3. Edit the existing case "loadArtifacts returns per-item envelopes" (lines 392-413 at the time of writing) rather than adding a new `it`:

- Change the call to `c.loadArtifacts(["a", "b"], { sessionID: "s", harness: "claude-code" } as unknown as { sessionID: string })`.
- Replace `expect(JSON.parse(body).ids).toEqual(["a", "b"])` with `expect(JSON.parse(body)).toEqual({ ids: ["a", "b"], session_id: "s" })`.
- Extend the existing `// Spec: §7.6.2` comment with "the body carries ids and session_id and no harness key, even when a caller passes one through a cast."

This pins the allowlist after CODE-3, covers the `session_id` forwarding line, and adds no new test case. Run `npm test` (vitest) in `sdks/podium-ts`.

**TEST-4 · e2e, `test/e2e/sdk_clients_test.go`.** Lands in S4 with CODE-2.

- Rename `TestSDK_PyBulkForwardsParams` to `TestSDK_PyBulkForwardsSessionID`.
- Replace the leading comment with `// Spec: §7.6.2 — Python load_artifacts forwards session_id to POST /v1/artifacts:batchLoad.`
- Change the inner comment to say that the standalone server accepts `session_id` in the batch body and that a successful call confirms the SDK forwards it without error.
- Change the script call to `c.load_artifacts(ids=['finance/ap/pay-invoice'], session_id='sess-abc')`. Keep `print('STATUS', out[0].status)` and the single `csWantStdout(t, res, "STATUS ok")` assertion.
- Add no `try`/`except TypeError` leg. TEST-2 and the signature cover that path.

The other batchLoad e2e tests (`TestSDK_PyBulkSplit`, `TestSDK_PyBulkMaterializeGap`, `TestSDK_PyBulkVisibilityDenied`, the batch-cap test, and the `http_api` and delivery batchLoad cases) pass no harness and stay unchanged. Several e2e suites skip on macOS, so confirm that the run did not skip `TestSDK_PyBulkForwardsSessionID` before reporting it green.

**TEST-5 · e2e, `test/e2e/cli_reference_test.go:1751`.** Lands in S7. Change the `t.Skip` message of `TestCLI_AdminReembed` to `"requires a configured vector backend; standalone has no embedder so reembed returns registry.unavailable"`, removing " The doc's --all flag is also not implemented". The test stays skipped.

**Existing coverage that stays.** `TestBatchLoad_ReturnsPerItemEnvelopes` (`pkg/registry/server/batch_load_test.go:44-74`) covers the handler path after CODE-1. `test/e2e/vector_backend_config_test.go:579-590` asserts that `--all` exits 2 and covers SPEC-3's flag set. Run `make speccov-drift` after S1 and S2 to confirm §7.6.2 and §4.7 keep their citations.

## Documentation changes

**DOC-1.** `docs/reference/http-api.md`, the `POST /v1/artifacts:batchLoad` body example (lines 252-264 at the time of writing). Lands in S8. Delete the line `  "harness": "claude-code",` so that `"session_id": "..."` is followed directly by `"version_pins"` and the JSON stays valid. Add no prose sentence. The field list in the example is the contract, the response paragraph already describes the per-item payload, and CHANGELOG-1 carries the migration note. `tools/doccov/manifest.yaml` has no entry for this page.

**DOC-2.** `docs/consuming/custom-via-sdk.md`, the "Bulk fetch" example (lines 106-121 at the time of writing). Lands in S9. Delete the line:

```python
    harness="claude-code",        # recorded on the request; the response and materialize() are canonical
```

so that `session_id=session_id,` is the last argument. The surrounding prose names no harness argument for `load_artifacts`, so no other edit is needed.

**CHANGELOG-1.** `CHANGELOG.md`, under `## [Unreleased]`. Lands in S10.

Under `### Removed`, append:

> - **The `harness` argument of `load_artifacts` and `loadArtifacts`, and the `harness` field of `POST /v1/artifacts:batchLoad`** (§7.6.2): the registry never read the field and runs no harness adapter (§2.2), so every `ok` item already carried the canonical manifest. Remove the argument from calls. Python raises `TypeError` when `harness=` is passed. TypeScript rejects `harness` in an options object literal at compile time, and `loadArtifacts` never sends the key. The registry decodes the request body as it decodes every other JSON body, so a `harness` key that an older SDK still sends is ignored.

Under `### Documentation`, append:

> - §4.7 names the flagless `podium admin reembed` as the full re-embed pass, in place of an `--all` flag the CLI never had.

## Non-goals

- Implementing harness adaptation on the registry or in the SDKs for bulk load.
- Changing the MCP `load_artifact` harness argument (§6.7, `docs/consuming/browsing-the-catalog.md:158`), which the MCP server implements locally.
- Changing the §7.6 `materialize()` harness rule that 0044 introduced, beyond deleting its `load_artifacts` carve-out sentence.
- Adopting strict JSON decoding (`DisallowUnknownFields`) on batchLoad or any other handler, or adding a spec-wide unknown-field rule.
- Adding a §6.10 error code or a `registry.invalid_argument` rejection for the retired key.
- Adding an `--all` flag to `podium admin reembed`, or changing any reembed CLI or endpoint behavior.
- Reworking the rest of the §4.7 model-versioning paragraph that 0044 converged, including the missing automatic re-embed trigger.
- Editing `test/e2e/vector_backend_config_test.go:579-590`, which already asserts that `--all` exits 2.
- A manual-validation scenario. The unit and e2e tests cover the changes, and `test/manual-validation.md` has no `load_artifacts` scenario to update.
- **TEST-1, a server unit test that a batchLoad body carrying `harness` is ignored (dropped).** The behavior it would pin is the default of the Go standard library decoder rather than Podium logic: `handleBatchLoad` decodes with a plain `json.NewDecoder(r.Body).Decode(&req)` (`pkg/registry/server/batch_load.go:87`), and `DisallowUnknownFields` appears nowhere under `pkg/`, `cmd/`, or `internal/`. After CODE-1 the handler has no harness-related line to cover, and `TestBatchLoad_ReturnsPerItemEnvelopes` (`pkg/registry/server/batch_load_test.go:44-74`) already covers the handler path with the same fixture. A `// Spec: §7.6.2` tag on such a test would cite a rule the section does not state, because SPEC-1 only removes the field and this proposal adds no unknown-field rule; `speccov-drift` would count it as coverage of something unwritten. `.claude/rules/spec-driven-development.md` says a test that encodes no spec requirement is not the test to write. The test would also make lenient decoding a backward-compatibility contract for one retired key on one endpoint, which the pre-1.0 no-shim rule in `.claude/rules/code-best-practices.md` excludes, and would make a later move to strict decoding look like a regression. CHANGELOG-1 and the CODE-1 doc comment describe the leniency as existing decoder behavior rather than a pinned guarantee.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.
