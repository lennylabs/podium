# Proposal 0044: Narrow the SDK materialize() harness promise to the canonical layout and scope model versioning to the built-in vector stores

- Issue: (to be filed)
- Status: Approved (2026-10-03), decided on the user's behalf under the overnight authorization. Signed off as staged. OQ-1 (the unread batchLoad harness field) and OQ-2 (the nonexistent `reembed --all` token in §4.7) go to a separate follow-up proposal, so this converged text is not reopened.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §2.2: the HarnessAdapter bullet stops promising a harness parameter on SDK `materialize()` and states that the SDKs run no adapter, write the canonical layout, and leave harness-native output to `podium sync` and the MCP server (SPEC-1).
- §7.6: the SDK example drops `harness="claude-code"`, and a new paragraph states the canonical-layout rule and the `harness` argument contract of `materialize()`, scoped so it does not cover `load_artifacts` (SPEC-2).
- §4.7: the "Model versioning and re-embedding" paragraph scopes per-row model tagging, query-time model filtering, and the stale-row purge to the collocated stores, and states the fresh-index requirement for a model change on a managed backend (SPEC-3).
- Python and TypeScript SDKs: `materialize()` on a loaded artifact and on a batch item rejects any `harness` other than `none` before any fetch or write, and before the batch item's status check (CODE-1, CODE-2, TEST-1, TEST-2).
- Docs and changelog: the consumer SDK page, the vector-backends switching paragraph, one operator-guide pitfall bullet, and the `[Unreleased]` entries (DOC-1, DOC-3, DOC-4, CHANGELOG-1). Manual-validation scenario S82 covers the SDK rejection (MV-1).

**Fixed decisions.**

- D4 is resolved by narrowing the spec. Porting the Go adapters to the SDKs and rendering harness layouts on the registry are both rejected.
- The SDK `harness` argument stays on `materialize()` and accepts only `none` or omitted. It is not removed.
- The rejection is a plain argument error: `ValueError` in Python, `Error` in TypeScript. It is not a §6.10 code and not `MaterializeError`.
- The message contains the literal substring `canonical layout only`. Both test suites match on it.
- The check runs first in all four entry points, before any presigned fetch, any write, and `BatchResult`'s status check.
- §7.6 is the single normative location of the `harness` argument rule. §2.2 states only that the SDKs run no adapter and points at §7.6.
- The `harness` field on `load_artifacts` / `POST /v1/artifacts:batchLoad` is not changed (OQ-1).
- D18 is resolved by narrowing §4.7 and documenting the operator procedure. It changes no code and adds no `ModelVersioned` implementation for Pinecone, Weaviate, or Qdrant, no detection, no warning, and no new persisted state.
- §4.7 names the collocated stores, pgvector and sqlite-vec, and omits the undocumented `memory` backend.
- A model change on a managed backend uses a fresh index or collection followed by a full re-embed in every tenant. A new Pinecone namespace prefix does not count as a fresh index.
- SPEC-3 states the contract. DOC-3 carries the operator detail: the query behavior during the pass and the outcome of reusing the old index.
- The managed-backend procedure names the flagless `podium admin reembed`. The `--all` token in the unchanged second sentence of the §4.7 paragraph stays (OQ-2).
- The §4.7 sentence that says the registry triggers the re-embed stays byte-identical. The missing automatic trigger is a separate gap.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **SPEC-1 and SPEC-2 must land before CODE-1 and CODE-2.** Both SDKs cite §2.2's "The SDKs accept a harness parameter on `materialize()`" to justify the current behavior (`sdks/podium-ts/src/index.ts` quotes it in the `MaterializeOptions` comment). Code that rejects the argument contradicts the spec until the spec edits land.
- **The existing tests break the moment the check lands.** `sdks/podium-py/tests/test_client.py` (`test_materialize_context_writes_artifact_md`) and `sdks/podium-ts/src/index.test.ts` ("writes ARTIFACT.md for a context and no SKILL.md") pass `harness: "claude-code"`. Each code change lands with its test change in one commit (steps S4 and S5).
- **A decorator breaks the Python `Spec:` annotation.** `tools/internal/specparser` reads the contiguous comment block immediately above `def test_*`. In the parametrized test, put `@pytest.mark.parametrize(...)` first and the `# Spec: §2.2 / §7.6` comment between the decorator and `def`. In TypeScript the annotation sits directly above each `it(` call; a comment above `describe` attaches to no test.
- **The TypeScript suite runs on vitest** (`sdks/podium-ts/package.json`, `"test": "vitest run"`) rather than the Node test runner that `.claude/rules/test-coverage.md` names.
- **Narrowing `MaterializeOptions.harness` to `"none"` makes the rejection tests fail to type-check** unless they cast, as TEST-2 stages. The runtime check is still required for plain JavaScript callers.
- **`load_artifacts(harness=...)` looks like the same defect and is out of scope.** Do not touch `Client.load_artifacts`, `loadArtifacts`, the batchLoad wire field, or `pkg/registry/server/batch_load.go`. The `docs/consuming/custom-via-sdk.md` comment at the bulk-load example ("recorded on the request ...") also stays.
- **`pkg/vector/vector.go` is not edited.** The `ModelVersioned` doc comment already states the contract SPEC-3 adopts (see Non-goals, CODE-3).
- **A full pass reports per-item failures with exit 0.** `podium admin reembed` exits non-zero only on an HTTP error (`cmd/podium/admin.go`); per-item failures appear in the result's `failed` list (`pkg/registry/core/reembed.go`). DOC-3 tells the operator to check that list before deleting the old index.
- **A fresh index is a new index or collection, and every tenant re-embeds into it.** A new Pinecone namespace prefix shares the old index's dimension and hosted model, so SPEC-3, DOC-3, and DOC-4 name only an index or collection. Repointing affects every tenant, a pass covers only the caller's tenant, and DOC-3 keeps the old index until every tenant's pass reports an empty `failed` list.
- **The BM25-only fallback is not reported to callers.** `SearchResult.Degraded` is set in `pkg/registry/core/core.go` and serialized nowhere. DOC-3 says the search response does not report it, matching the vector-backends "Operational notes".
- **DOC-3 adds no heading.** DOC-4 links to the existing `#switching-backends-on-a-running-deployment` anchor. The site build rejects a link to a missing anchor.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-2. §7.6 corrects the `materialize()` example and adds the canonical-layout and `harness` argument paragraph.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-1. §2.2 replaces the SDK harness-parameter sentence with the canonical-layout statement, which points at the §7.6 rule that S1 adds.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §4.7 scopes model versioning to the collocated stores and states the managed-backend fresh-index requirement.
      Levels: —. Depends on: —
- [ ] **S4 · code** — CODE-1, TEST-1. The Python helper and its calls land with the updated and new Python tests. Bundled because the existing `harness="claude-code"` test fails as soon as the check lands.
      Levels: unit. Depends on: S1, S2
- [ ] **S5 · code** — CODE-2, TEST-2. The TypeScript helper, type narrowing, and comments land with the updated and new vitest cases. Bundled for the same reason as S4.
      Levels: unit. Depends on: S1, S2
- [ ] **S6 · docs** — DOC-1. The consumer SDK page states the `none`-only rule and the argument error.
      Levels: —. Depends on: S4, S5
- [ ] **S7 · docs** — DOC-3. The vector-backends switching paragraph gains the managed-backend procedure and the query behavior during the pass.
      Levels: —. Depends on: S3
- [ ] **S8 · docs** — DOC-4. One operator-guide pitfall bullet links to the DOC-3 procedure.
      Levels: —. Depends on: S7
- [ ] **S9 · docs** — CHANGELOG-1. The `[Unreleased]` `Changed` and `Documentation` entries.
      Levels: —. Depends on: S4, S5, S7, S8
- [ ] **S10 · docs** — MV-1. Manual-validation scenario S82 for the Python SDK rejection.
      Levels: manual. Depends on: S4

**Ordering constraints.** The D4 chain (S1, S2, S4, S5, S6, S10) and the D18 chain (S3, S7, S8) are independent and may proceed in parallel. S9 follows both chains, because its `Documentation` entry names the operator-guide link that S8 adds. SPEC-2 lands before SPEC-1 because the new §2.2 sentence refers to the §7.6 rule, while the SPEC-2 paragraph cites only the existing §2.2 statement that the SDKs do not share the Go module. S4 and S5 are independent of each other. DOC-1 lands after both code steps because its text is false until both SDKs raise.

## Current state and the gap

The spec makes two promises the product does not implement. This proposal narrows the spec to what ships. Both decisions were made before this proposal and are not reopened here.

### D4: SDK materialize() and the harness adapter

§7.6 shows `artifact.materialize(to="./artifacts/", harness="claude-code")  # respects the harness adapter`, and the HarnessAdapter bullet in §2.2 ends with "The SDKs accept a harness parameter on `materialize()`." Neither SDK runs a harness adapter, and both write only the canonical layout.

- Python declares `harness: str = "none"` on `LoadedArtifact.materialize` and `BatchResult.materialize` (`sdks/podium-py/podium/client.py`) and calls `_materialize_canonical` without it. The `LoadedArtifact` docstring says `harness` "is recorded for forward compatibility", but nothing stores the value.
- TypeScript declares `MaterializeOptions.harness` (`sdks/podium-ts/src/index.ts`) with the same comment, and neither `LoadedArtifact.materialize` nor `BatchResult.materialize` reads it. The `LoadedArtifact` class comment says the object exposes `materialize(to, { harness })`.

A caller that passes `harness="claude-code"` therefore gets canonical files and no error. The adapters live in the single Go module that §2.2 describes, and the SDKs "are independent HTTP clients ... and do not share this module". Porting the adapters to Python and TypeScript would create two more implementations of every harness layout with nothing shared to keep them in agreement. Proposal 0027 rejected the same duplication for the delivery serialization. The SDK unit tests pin the silent acceptance: `sdks/podium-py/tests/test_client.py` and `sdks/podium-ts/src/index.test.ts` each call `materialize` with harness `claude-code`. `docs/consuming/custom-via-sdk.md` describes the argument as forward compatibility.

### D18: Model versioning on managed vector backends

The "Model versioning and re-embedding" paragraph of §4.7 states without qualification that the vector store records `(model_id, dimensions)` per artifact, that query results are restricted to the configured model during a re-embed, and that stale rows are purged afterwards. The code provides this only through the optional `vector.ModelVersioned` capability (`pkg/vector/vector.go`). Only memory (`pkg/vector/memory.go`), sqlite-vec (`pkg/vector/sqlitevec.go`), and pgvector (`pkg/vector/pgvector.go`) implement it.

- Pinecone stores only `artifact_id` and `version` (`pkg/vector/pinecone.go`). Qdrant stores `tenant_id`, `artifact_id`, and `version` (`pkg/vector/qdrant.go`). Weaviate stores `tenantId`, `artifactId`, `version`, and `content` (`pkg/vector/weaviate.go`).
- The registry falls back to plain `Query` and plain `Put` for these backends (`pkg/registry/core/core.go`) and runs the purge only on a `ModelVersioned` backend (`pkg/registry/core/reembed.go`).

When an operator switches to a model with the same dimension and re-embeds into the same managed index, queries during the pass score vectors from both models together, and nothing purges the old rows afterwards. When the new model has a different dimension, the managed index rejects the mismatched writes and queries, and search degrades to BM25 only. The registry cannot detect a model change on a managed backend cheaply. Managed rows carry no model tag, and no previous model id is persisted anywhere the registry reads at startup. The model id appears only in the transient `embedding.reembed_in_progress` audit event.

`docs/deployment/vector-backends.md` already says that managed backends carry no model tag. It limits the fresh-index requirement to dimension-changing switches, gives no procedure, and does not say what queries return during the pass. `docs/deployment/operator-guide.md` has no re-embed content.

## Decisions

- **D4 narrows the spec.** SDK `materialize()` writes the canonical layout, which is the output of the `none` adapter. Harness-native layouts come from `podium sync` or the MCP server. Porting the Go adapters is rejected for the reason §2.2 and proposal 0027 give: it would create duplicate implementations with no shared library to keep them in agreement.
- **Server-side harness rendering is rejected for this change.** The registry embeds the adapters, and the batchLoad wire already carries a `harness` field. Using it would still need a new response format carrying per-harness file paths and contents for the SDKs to write. That is a new capability, and the problem statement fixes the outcome as a narrowing.
- **The `harness` argument stays and accepts only `none`.** Removing it was the alternative. Keeping it with a runtime check is preferred for three reasons. (a) In TypeScript, removing an option-bag key is enforced only at compile time, so plain JavaScript callers would keep passing `{ harness: "claude-code" }` and have it ignored, which is the defect being fixed. A runtime check behaves the same in both SDKs. (b) The check follows the existing client convention for an invalid argument value: Python raises `ValueError` for a bad `cache_mode`, and TypeScript throws `Error` for a bad `cacheMode` (`sdks/podium-py/podium/client.py`, `sdks/podium-ts/src/index.ts`). (c) It keeps the documented `harness="none"` examples in `docs/consuming/custom-via-sdk.md` valid. The check is not a compatibility shim. Callers that passed another value now get an error, and that pre-1.0 break lands in a MINOR bump.
- **The error is a plain argument error.** The check is local to the client and nothing goes over the wire, so it carries no §6.10 code. `MaterializeError` is not reused, because it means the §6.6 sandbox refused a write.
- **The check covers all four entry points and runs first.** `LoadedArtifact.materialize` and `BatchResult.materialize` in each SDK validate before any presigned fetch, any write, and `BatchResult`'s status check, so an invalid argument is reported as an argument error whatever the item's status. SPEC-2 states this ordering so TEST-1 and TEST-2 pin a spec requirement.
- **The rule has one normative location.** §7.6 states the `harness` argument contract. §2.2 states only the architectural fact (no adapter in the SDK, canonical output, sync or MCP for harness-native files) and points at §7.6. The SPEC-1 and SPEC-2 review notes proposed two slightly different §2.2 sentences; SPEC-1's text is staged because it keeps the sync and MCP pointers the HarnessAdapter bullet needs, and neither text repeats the argument rule.
- **The wire-level `harness` on `load_artifacts` is unchanged.** The server decodes `BatchLoadRequest.Harness` (`pkg/registry/server/batch_load.go`) and does not use it. That is a separate unimplemented promise, raised as OQ-1. The SPEC-2 paragraph names `materialize()`'s argument explicitly so it does not read as covering `load_artifacts`.
- **D18 narrows §4.7.** Per-row model tagging, query-time model filtering, and the stale-row purge are properties of the collocated stores, pgvector and sqlite-vec. For Pinecone, Weaviate Cloud, and Qdrant Cloud, the operator switches models by pointing the registry at a fresh index or collection and running a full re-embed in every tenant. A fresh Pinecone namespace prefix is excluded, because every namespace lives in the one configured index and shares its dimension and hosted model (`pkg/vector/pinecone.go:18-21`, `:137-144`). A full re-embed is required in every tenant because the index setting is instance-wide while a pass covers only the caller's tenant (`spec/04-artifact-model.md:770`, `pkg/registry/core/reembed.go:68`). Building model versioning for the managed backends is rejected because it cannot be verified without live managed-backend credentials.
- **The spec text says "collocated" and omits the memory store.** The spec's backend vocabulary (§9.1, §13.12) lists pgvector, sqlite-vec, pinecone, weaviate-cloud, and qdrant-cloud, and §4.7 already calls pgvector and sqlite-vec collocated. The `memory` vector backend is undocumented and non-durable (`internal/serverboot/backend_config_test.go`), so a model-switch procedure for it has no meaning.
- **No startup or reembed warning is staged.** Detection is unreliable: managed rows carry no model tag, no previous model id is persisted in `pkg/store` or `internal/`, and the model id appears only in the transient `embedding.reembed_in_progress` event. A warning would need new persisted state, which is a new feature.
- **The procedure names the flagless `podium admin reembed`.** The CLI defines no `--all` flag (`cmd/podium/admin.go`), and `docs/reference/cli.md` documents the flagless form as the tenant-wide pass. The `--all` token in §4.7's second sentence is raised as OQ-2 and is not silently corrected.
- **The §4.7 trigger sentence stays byte-identical.** Rewriting "the registry triggers a background re-embed" to "the operator re-embeds" would silently resolve the separate automatic-trigger gap and leave the spec contradicting `docs/reference/cli.md`. That gap is a non-goal.
- **The spec states the contract; the docs state the operator detail.** SPEC-3 states which stores filter and purge and that a managed-backend switch uses a fresh index or collection followed by a full pass in every tenant. The partial-recall behavior during the pass and the outcome of reusing the old index live in DOC-3. During a pass into a fresh index, search fuses BM25 with vector ranks via RRF (`pkg/registry/core/core.go`); BM25 covers every artifact, the vector half covers only the artifacts already written to the new index, and results never mix two models.

## Spec amendment: §2.2 HarnessAdapter

**SPEC-1.** `spec/02-architecture.md`, §2.2, the **HarnessAdapter** bullet under "Pluggable interfaces shared across the consumers:" (line 103 at the time of writing). Replace the final sentence of the bullet:

> The SDKs accept a harness parameter on `materialize()`.

with:

> The language SDKs run no harness adapter. Their `materialize()` writes the canonical layout that the `none` adapter produces, and harness-native layouts come from `podium sync` (§7.5) or the MCP server (§6.7). §7.6 defines the SDK `harness` argument.

The rest of the bullet, from "translates canonical artifacts into harness-native format" through "See §6.7 for the full roster with documentation links.", is unchanged. The **Shared library code (Go)** paragraph that follows is unchanged.

## Spec amendment: §7.6 SDK materialize and the canonical layout

**SPEC-2.** `spec/07-external-integration.md`, §7.6 "Language SDKs". Two anchors, landed in one commit.

(a) The `materialize` line of the Python example (line 604 at the time of writing). Replace:

```python
artifact.materialize(to="./artifacts/", harness="claude-code")  # respects the harness adapter
```

with:

```python
artifact.materialize(to="./artifacts/")  # canonical layout: ARTIFACT.md, SKILL.md for a skill, and bundled resources
```

(b) Insert a new paragraph immediately after the closing fence of that example block and before the paragraph that begins "Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path":

> `materialize()` writes the artifact under `<to>/<id>/` in the canonical layout, which is the output of the `none` harness adapter. The SDKs do not embed the harness adapters (§2.2). A consumer that needs harness-native files runs `podium sync --harness <name>` (§7.5) or loads through the MCP server (§6.7). The `harness` argument of `materialize()` accepts only `none`, and omitting it is equivalent to `none`. Any other value raises an argument error before any resource is fetched or any file is written. On a §7.6.2 bulk-load item the argument is checked before the item's status, so an `error` item called with an invalid `harness` raises the argument error rather than the item's registry error. This rule does not cover the `harness` argument of `load_artifacts` (§7.6.2).

The §7.6.2 bulk-fetch example, including `harness="claude-code",        # optional per-call adapter override` and the wire format line, is unchanged (OQ-1).

## Spec amendment: §4.7 Model versioning and re-embedding

**SPEC-3.** `spec/04-artifact-model.md`, §4.7 "Embedding generation", the paragraph that opens with "**Model versioning and re-embedding.**" (line 770 at the time of writing). Replace the first, third, and fourth sentences. The second sentence and every sentence from "The re-embed runs over the caller's tenant and is authorized by the per-tenant `admin` role (§4.7.2)." to the end of the paragraph stay byte-identical.

Current text of the replaced span, from the start of the paragraph through "stale-dimension rows are purged.":

> **Model versioning and re-embedding.** The vector store records `(model_id, dimensions)` per artifact. When the configured embedding model changes (operator switches `EmbeddingProvider`, switches the self-embedding backend's hosted model, or upgrades to a new version of the same model), the registry triggers a background re-embed via `podium admin reembed` (`--all` or `--since <timestamp>`). During re-embedding, the vector store may transiently contain mixed dimensions; query-time the registry restricts results to vectors matching the currently-configured model and emits `embedding.reembed_in_progress` events for progress monitoring. Once re-embedding completes, stale-dimension rows are purged.

Replacement text:

> **Model versioning and re-embedding.** The collocated vector stores (pgvector and sqlite-vec) record `(model_id, dimensions)` per row. When the configured embedding model changes (operator switches `EmbeddingProvider`, switches the self-embedding backend's hosted model, or upgrades to a new version of the same model), the registry triggers a background re-embed via `podium admin reembed` (`--all` or `--since <timestamp>`). During a re-embed the registry emits `embedding.reembed_in_progress` events for progress monitoring. A collocated store may transiently contain vectors from two models during the pass, and at query time the registry restricts its results to vectors that match the currently-configured model. Once a full pass completes, the previous model's rows are purged from a collocated store. A pass scoped with `--since` or `--only-missing` purges nothing. The managed backends (Pinecone, Weaviate Cloud, and Qdrant Cloud) record no model per row, and the registry neither filters their results by model nor purges their rows. Every tenant shares the configured managed index or collection, so a model change on a managed backend requires pointing the registry at a fresh index or collection and then running a full re-embed in every tenant.

The paragraph then continues unchanged with "The re-embed runs over the caller's tenant and is authorized by the per-tenant `admin` role (§4.7.2)."

## Proposed solution

### CODE-1. Python SDK: reject a non-`none` harness on both materialize methods

`sdks/podium-py/podium/client.py`.

- Add a module-level helper next to `_materialize_canonical`:

  ```python
  def _require_canonical_harness(harness: str) -> None:
      # spec: §2.2 / §7.6 — the SDK embeds no harness adapter and writes the
      # canonical layout only; podium sync or the MCP server writes
      # harness-native files.
      if harness != "none":
          raise ValueError(
              f"materialize() writes the canonical layout only; harness must be 'none', got {harness!r}. "
              "Use `podium sync --harness <name>` for harness-native files."
          )
  ```

- Call `_require_canonical_harness(harness)` as the first statement of `LoadedArtifact.materialize`, and as the first statement of `BatchResult.materialize`, before `if self.status != "ok":`. Keep both signatures (`harness: str = "none"`).
- Rewrite the `LoadedArtifact.materialize` docstring. Its first paragraph cites `materialize(to=...)` instead of `materialize(to=..., harness=...)`. Replace the paragraph that begins "The ``harness`` parameter is accepted per §2.2." with: "``harness`` accepts only ``\"none\"`` (§2.2, §7.6). The SDK does not embed the harness adapters, so any other value raises ValueError before a file is written; run ``podium sync --harness <name>`` for harness-native files."
- Add one sentence to the `BatchResult.materialize` docstring: "``harness`` accepts only ``\"none\"`` and is checked before ``status`` (§7.6), so an invalid value raises ValueError on an ``error`` item too."
- Format with the SDK's configured tooling.

### CODE-2. TypeScript SDK: reject a non-`none` harness on both materialize methods

`sdks/podium-ts/src/index.ts`.

- Narrow `MaterializeOptions.harness` to `harness?: "none";`. Replace the comment above it with: "Spec §2.2 / §7.6. The SDK embeds no harness adapter and writes the canonical layout, which is the output of the `none` adapter. Any other value throws before a file is written; run `podium sync --harness <name>` for harness-native files."
- Add a module-private helper:

  ```ts
  // Spec §2.2 / §7.6: the type narrowing protects TypeScript callers only, so
  // a plain JavaScript caller passing another harness is rejected at runtime.
  function requireCanonicalHarness(harness: unknown): void {
    if (harness !== undefined && harness !== "none") {
      throw new Error(
        `materialize() writes the canonical layout only; harness must be "none", got ${JSON.stringify(harness)}. ` +
          "Use `podium sync --harness <name>` for harness-native files.",
      );
    }
  }
  ```

- Call `requireCanonicalHarness(opts.harness)` as the first statement of `LoadedArtifact.materialize` and of `BatchResult.materialize`, before `if (this.status !== "ok")`.
- Replace the `LoadedArtifact` class comment ("Spec §7.6 / §2.2 — the loaded-artifact object exposes materialize(to, { harness }) ...") with: "Spec §7.6 / §2.2 — the loaded-artifact object exposes materialize(to), which writes the canonical layout; resources are inline bytes; largeResources are §7.2 presigned references fetched on demand."
- The `harness` option of `loadArtifacts` is unchanged.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `harness` omitted (Python default, TypeScript `undefined`) | Canonical layout written, as today | §7.6 "omitting it is equivalent to `none`" (SPEC-2); `docs/consuming/custom-via-sdk.md` (DOC-1) |
| `harness="none"` | Canonical layout written | §7.6 (SPEC-2); DOC-1 and the existing `harness="none"` examples |
| `harness` set to an adapter name, `""`, `"NONE"`, or Python `None` | Argument error; the comparison is exact, so every value other than the string `none` is rejected | §7.6 "Any other value raises an argument error" (SPEC-2); DOC-1 |
| Rejected call on an artifact with presigned resources | No fetch and no file; the destination is untouched | §7.6 "before any resource is fetched or any file is written" (SPEC-2); DOC-1 |
| Batch `error` item with an invalid `harness` | Argument error rather than the item's `RegistryError` | §7.6 "On a §7.6.2 bulk-load item the argument is checked before the item's status" (SPEC-2) |
| Batch `error` item with `none` or omitted | Item's `RegistryError` subclass, as today | §7.6.2 (unchanged) |
| Caller that relied on the silent acceptance of an adapter name | Now raises (accepted pre-1.0 break) | §7.6 (SPEC-2); `CHANGELOG.md` (CHANGELOG-1) |
| `load_artifacts(harness="claude-code")` | Accepted, sent on the wire, and ignored by the server (deferred) | §7.6 "This rule does not cover the `harness` argument of `load_artifacts`" (SPEC-2); existing comment in `docs/consuming/custom-via-sdk.md`; OQ-1 |
| Model change on pgvector or sqlite-vec, full pass | Queries during the pass return only current-model vectors; the previous model's rows are purged afterwards | §4.7 (SPEC-3); `docs/deployment/vector-backends.md` (existing text, kept by DOC-3) |
| Model change on pgvector or sqlite-vec, `--since` or `--only-missing` pass | Nothing is purged | §4.7 "A pass scoped with `--since` or `--only-missing` purges nothing" (SPEC-3); vector-backends page (existing) |
| Managed backend, fresh index, pass in progress | BM25 covers the whole catalog; vector matches cover only the artifacts already re-embedded; no mixed-model scoring | §4.7 fresh-index sentence (SPEC-3); DOC-3 |
| Managed backend, same index reused, same dimension | Vectors from both models scored together; old rows never purged (accepted outside the procedure) | §4.7 "the registry neither filters their results by model nor purges their rows" (SPEC-3); DOC-3 |
| Managed backend, fresh index, multi-tenant registry, a tenant whose pass has not run | That tenant's search returns BM25 matches only, and the search response does not report it; the old index is kept until every tenant's pass reports an empty `failed` list | §4.7 "a full re-embed in every tenant" (SPEC-3); DOC-3 |
| Managed backend, new Pinecone namespace prefix in the same index | Not a fresh index: the namespace shares the index's dimension and hosted model, so a dimension or hosted-model change fails as in the next row | §4.7 "a fresh index or collection" (SPEC-3); DOC-3 |
| Managed backend, same index reused, dimension changed | The index rejects the new vectors; search runs BM25 only, and the search response does not report the fallback | §4.7 (SPEC-3); DOC-3; existing dimension text in `docs/deployment/vector-backends.md` "Self-embedding and storage-only modes" and its "Operational notes" |
| Managed backend, model changed and no re-embed run | No detection and no warning; queries embed with the new model against old vectors (accepted, deferred) | §4.7 "requires pointing the registry at a fresh index or collection and then running a full re-embed in every tenant" (SPEC-3); DOC-3 and DOC-4 state the required operator steps |
| Managed backend, pass returns with per-item failures | The command exits 0 and lists the failures; those artifacts have no vector in the new index until a re-run | DOC-3 "check that the result's `failed` list is empty ... re-run" |

## Testing

All SDK tests are unit tests in the SDK suites: the behavior is local to the client and reaches no server. Each test carries its `Spec:` annotation directly above the test line.

**TEST-1 · unit, `sdks/podium-py/tests/test_client.py`.** Lands in S4 with CODE-1.

1. **Update `test_materialize_context_writes_artifact_md`.** Change the call to `art.materialize(str(tmp_path), harness="none")`. Replace the annotation above it with `# Spec: §7.6 / §2.2 — materialize writes the canonical layout; harness accepts only "none".` This pins the explicit-`none` path.
2. **Add `test_materialize_rejects_non_none_harness`.** Parametrize over two callables: a `LoadedArtifact` with a `large_resources` entry, and an `ok` `BatchResult` with a presigned resource. Place `@pytest.mark.parametrize(...)` first and `# Spec: §2.2 / §7.6 — a harness other than "none" raises before any fetch or write.` between the decorator and `def`. Each case calls `materialize(str(tmp_path), harness="claude-code", fetch=<a function that raises AssertionError>)` inside `pytest.raises(ValueError, match="canonical layout only")`, then asserts `list(tmp_path.iterdir()) == []`. The raising `fetch` proves the check precedes the presigned fetch; the empty directory proves no write.
3. **Extend `test_batch_result_materialize_ok_and_error`.** After the existing `pytest.raises(RegistryError)` assertion, assert that `bad.materialize(str(tmp_path), harness="claude-code")` raises `ValueError` and not `RegistryError` (`ValueError` is not a `RegistryError` subclass, so `pytest.raises(ValueError)` suffices). Add `§7.6` to the annotation above the test and a comment citing the §7.6 ordering sentence.

The omitted-argument default stays covered by the existing skill and resource tests. No separate test is added for it.

**TEST-2 · unit, `sdks/podium-ts/src/index.test.ts`.** Lands in S5 with CODE-2.

1. **Update the context test.** Change the call to `art.materialize(dir, { harness: "none" })`. Rewrite the comment above `describe("LoadedArtifact.materialize", ...)` to `// Spec: §2.2 / §7.6 — materialize writes the canonical layout; harness accepts only "none" and any other value throws before a file is written.`, and add `// Spec: §7.6` directly above the updated `it(` call so the annotation attaches to a test.
2. **Imports.** Add `readdir` to the `node:fs/promises` import, and `type MaterializeOptions` to the `./index.js` import.
3. **Add three cases inside the `describe`, each with `// Spec: §2.2 / §7.6` directly above `it(`.**
   - (a) `LoadedArtifact` with a large resource: `await expect(art.materialize(dir, { harness: "claude-code", fetcher: <a fetcher that throws> } as unknown as MaterializeOptions)).rejects.toThrow(/canonical layout only/)`, then `expect(await readdir(dir)).toEqual([])`.
   - (b) An `ok` `BatchResult` with a presigned resource and the same throwing fetcher: the same two assertions.
   - (c) An `error` `BatchResult` with the same cast harness: assert `rejects.toThrow(/canonical layout only/)` and `rejects.not.toBeInstanceOf(RegistryError)`. This pins that validation runs before the status check.
4. Run `npm test` (vitest) in `sdks/podium-ts`. When a coverage provider is configured, confirm that `requireCanonicalHarness` and both call sites are covered.

The omitted-argument default stays covered by the existing skill test.

D18 changes no code, so it adds no test. SPEC-3 keeps the behavioral claims for the collocated stores that the existing re-embed tests already cite under §4.7. Run `make speccov-drift` after S3 to confirm §4.7 keeps its citations.

## Manual validation

**MV-1.** Add scenario S82 to `test/manual-validation.md` after S81, following the existing conventions. Lands in S10.

~~~~markdown
## S82: The Python SDK rejects a harness other than `none` on materialize

**Goal.** Validate that `materialize()` on a loaded artifact and on a batch
item writes the canonical layout for `harness="none"` and raises `ValueError`
for any other value without writing a file.

**Covers.** The §7.6 `harness` argument contract and the §2.2 statement that
the SDKs run no harness adapter.

**Why by hand.** The SDK unit tests assert the exception type and an empty
directory. A person reads the error message a caller sees in a terminal and
confirms that it names the canonical layout and points at `podium sync`.

**Prerequisites.** `python3` 3.10 or later.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then put the
   SDK on the path and write the script.

   ```bash
   export PYTHONPATH="$REAL_HOME/projects/podium/sdks/podium-py"
   cat > "$WORK/mat.py" <<'PY'
   import os, sys
   from podium import BatchResult, LoadedArtifact, RegistryError
   art = LoadedArtifact(id="a/b", type="context", version="1.0.0",
                        manifest_body="x\n", frontmatter="---\ntype: context\n---\n")
   bad = BatchResult(id="x/y", status="error",
                     error=RegistryError("visibility.denied", "no"))
   out = os.path.join(os.environ["WORK"], "out")
   os.makedirs(out, exist_ok=True)
   mode = sys.argv[1]
   try:
       if mode == "none":
           print("WROTE", art.materialize(out, harness="none"))
       elif mode == "claude":
           art.materialize(out, harness="claude-code")
       else:
           bad.materialize(out, harness="claude-code")
   except Exception as e:
       print("RAISED", type(e).__name__, e)
   print("FILES", sorted(os.listdir(out)))
   PY
   ```

   **Expect.** The script file exists.

2. Materialize with `harness="claude-code"`.

   ```bash
   python3 "$WORK/mat.py" claude
   ```

   **Expect.** `RAISED ValueError materialize() writes the canonical layout
   only; harness must be 'none', got 'claude-code'. Use `podium sync --harness
   <name>` for harness-native files.` followed by `FILES []`. A `FILES` line
   listing `a` is the shipped behavior this scenario exists to catch.

3. Materialize an error batch item with `harness="claude-code"`.

   ```bash
   python3 "$WORK/mat.py" batch-error
   ```

   **Expect.** `RAISED ValueError` with the same message, and `FILES []`.
   `RAISED VisibilityDenied` or any other registry error means the status
   check ran first, which is a defect.

4. Materialize with `harness="none"`.

   ```bash
   python3 "$WORK/mat.py" none
   cat "$WORK/out/a/b/ARTIFACT.md"
   ```

   **Expect.** A `WROTE` line naming `.../out/a/b/ARTIFACT.md`, `FILES ['a']`,
   and the file prints `---`, `type: context`, `---`.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.

---
~~~~

The D18 changes are documentation only and alter nothing an operator observes, so they stage no scenario.

## Documentation changes

**DOC-1.** `docs/consuming/custom-via-sdk.md`, the paragraph that begins "`materialize()` writes the canonical layout under `<to>/<id>/`" (line 94 at the time of writing). Lands in S6, after both SDKs raise. Keep the first sentence. Replace the rest of the paragraph:

> The SDK is an independent HTTP client that does not embed the harness adapters, so `materialize()` accepts a `harness` argument for forward compatibility and writes the canonical layout whatever its value. Run `podium sync --harness <name>` when the consumer needs harness-native files.

with:

> The SDK is an independent HTTP client that does not embed the harness adapters, so `materialize()` writes only the canonical layout, the output of the `none` adapter, and its `harness` argument accepts only `none`. Any other value raises an argument error (`ValueError` in Python, `Error` in TypeScript) before a file is written. Run `podium sync --harness <name>`, or load through the MCP server, when the consumer needs harness-native files.

The bulk-load comment at line 114 and the `harness="none"` examples at lines 206 and 217 are unchanged.

**DOC-3.** `docs/deployment/vector-backends.md`, "Switching backends on a running deployment", the paragraph that begins "The registry emits `embedding.reembed_in_progress` events" (line 206 at the time of writing). Lands in S7. Edit in place, with no new heading. Keep every sentence up to and including "a pass scoped with `--only-missing` or `--since` purges nothing." Replace the final sentence:

> Pinecone, Weaviate Cloud, and Qdrant Cloud carry no model tag, so a model change that alters the vector dimension needs a new index or collection sized to the new model rather than an in-place re-embed.

with:

> Pinecone, Weaviate Cloud, and Qdrant Cloud carry no model tag, so the registry neither filters their query results by model nor purges the previous model's vectors. Any embedding-model change on one of them, whether or not it alters the dimension, needs a fresh index or collection (`PODIUM_PINECONE_INDEX`, `PODIUM_WEAVIATE_COLLECTION`, or `PODIUM_QDRANT_COLLECTION`) created for the new model's dimension and, on a self-embedding deployment, for its hosted model. When `PODIUM_PINECONE_HOST` is set explicitly, point it at the new index as well. A new `PODIUM_PINECONE_NAMESPACE` prefix does not substitute for a new index, because every namespace lives in the same index and shares its dimension and hosted model. Every tenant shares the configured index or collection, so repointing the registry affects every tenant at once, while a re-embed pass covers only the caller's tenant. Point the registry at the fresh index, restart the registry, and have an identity that holds the `admin` role in each provisioned tenant run `podium admin reembed` with no scoping flag for that tenant. The command returns when the pass ends. Check that the `failed` list in its result is empty, and re-run the command when it is not. Delete the old index only after every tenant's pass reports an empty `failed` list, because until then it is the only populated index to roll back to. Until a tenant's pass completes, that tenant's search fuses BM25 over the whole catalog with vector matches from only the artifacts already re-embedded, and a tenant whose pass has not started gets BM25 matches only. Re-embedding into the existing managed index scores vectors from both models together when the dimension is unchanged. When the dimension changes, the index rejects the new vectors and search runs BM25 only. The search response reports none of these conditions (see [Operational notes](#operational-notes)).

**DOC-4.** `docs/deployment/operator-guide.md`, "Common operational pitfalls". Lands in S8. Insert one bullet immediately after the **pgvector index bloat** bullet:

> - **Embedding-model change.** After changing `PODIUM_EMBEDDING_PROVIDER`, the embedding model, or a self-embedding backend's hosted model, run `podium admin reembed` with no scoping flag in every tenant. On Pinecone, Weaviate, or Qdrant, first point the registry at a fresh index or collection, because those backends record no model per row and every tenant shares the index. The procedure is in [Switching backends on a running deployment](vector-backends#switching-backends-on-a-running-deployment).

The link targets the existing heading, because DOC-3 adds none. Do not restate the purge rules or the query behavior during the pass; DOC-3 owns them.

**CHANGELOG-1.** `CHANGELOG.md`, under `## [Unreleased]`. Lands in S9.

Under `### Changed`:

> - **SDK `materialize()` rejects a harness other than `none`** (§2.2, §7.6): `podium-py` and `podium-ts` write only the canonical layout, and `materialize()` on a loaded artifact or a batch item now raises an argument error (`ValueError` in Python, `Error` in TypeScript) before writing when `harness` is any value other than `none`. Previously the argument was accepted and ignored. Run `podium sync --harness <name>` for harness-native files.

Under `### Documentation`:

> - **Embedding-model switch on managed vector backends** (§4.7): per-row model versioning, query-time model filtering, and the stale-row purge apply to the collocated stores, pgvector and sqlite-vec. The vector-backends page gives the fresh-index procedure for Pinecone, Weaviate, and Qdrant and describes query results during the re-embed, and the operator guide links to it.

## Open questions

**OQ-1. The `harness` argument of `load_artifacts`.** The §7.6.2 bulk-fetch example calls the `harness` argument on `load_artifacts` an "optional per-call adapter override", and the batchLoad wire carries `harness?`. The server decodes the field into `BatchLoadRequest.Harness` and never reads it (`pkg/registry/server/batch_load.go`). This is the same class of unimplemented promise as D4. Should this proposal also narrow §7.6.2, either by removing the field from the wire and both SDKs or by redefining it as recorded only, or should it go to a separate proposal as this draft assumes?

**OQ-2. The `--all` token in §4.7.** The §4.7 paragraph names `podium admin reembed` (`--all` or `--since <timestamp>`), but the CLI has no `--all` flag (`cmd/podium/admin.go`), and the flagless command is the tenant-wide pass (`docs/reference/cli.md`). Should SPEC-3 also replace `--all` with "a tenant-wide pass (no scoping flag)" while it rewrites that paragraph, or should the token be left for a separate fix? The draft leaves the second sentence byte-identical.

## Non-goals

- Porting the Go harness adapters to `podium-py` or `podium-ts`.
- Rendering harness-native layouts on the registry and returning them to the SDKs.
- Changing the wire-level `harness` field on `POST /v1/artifacts:batchLoad` or the `harness` option on `load_artifacts` / `loadArtifacts` (OQ-1).
- Implementing `vector.ModelVersioned` for Pinecone, Weaviate, or Qdrant.
- A startup or reembed warning on a managed-backend model change, and any new persisted last-model state such a warning would need.
- Adding an `--all` flag to `podium admin reembed` (OQ-2).
- Making the registry trigger a re-embed automatically on a model change. §4.7 and `docs/reference/cli.md` describe an automatic trigger, and no such trigger exists in `internal/serverboot` or `pkg/registry/core`. That gap is separate from D18.
- Changing the MCP server's `load_artifact` harness override (`cmd/podium-mcp/descriptions.go`).
- **DOC-2, SDK READMEs (dropped).** The consumer SDK page is the documented home for per-method SDK behavior, and DOC-1 rewrites it. Neither README makes a false promise about `harness`: the TypeScript README is a usage snippet, and the Python README lists only `Client` methods, while `materialize()` is a method of `LoadedArtifact` and `BatchResult`. A README edit would add two more copies of the rule to keep in sync with the code. The CODE-1 and CODE-2 error messages and docstrings already name `podium sync --harness <name>` to every caller that passes another value.
- **CODE-3, `vector.ModelVersioned` doc comment (dropped).** The comment in `pkg/vector/vector.go` already cites §4.7, names memory, sqlite-vec, and pgvector as implementers, and says a managed backend that re-indexes per model need not implement it and the registry falls back to plain `Put` and `Query`. That matches `pkg/registry/core/core.go` and `pkg/registry/core/reembed.go` and the narrowed §4.7. Rewriting it would change a correct comment outside the scope of the change and hard-code a list of backend names that goes stale when a backend is added.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-03, automated)

- **A fresh Pinecone namespace cannot be sized for a new model.** A namespace is a partition of the one configured index (`pkg/vector/pinecone.go:18-21`, `namespaceFor` at `:137-144`), so it shares the index's dimension and hosted model. The namespace option was dropped rather than restricted, because a fresh index or collection is correct for every model change and a conditional branch would be one more predicate for three sections to keep in agreement. SPEC-3's last sentence, DOC-3, DOC-4, Decisions (D18 bullet), and the edge-case table now name only a fresh index or collection; DOC-3 adds the `PODIUM_PINECONE_HOST` note and states that a new namespace prefix is no substitute; a new edge-case row and a Watch-out bullet record it.
- **One re-embed covers only the caller's tenant.** The index setting is instance-wide (`pkg/vector/pinecone.go:18-19`, `pkg/vector/qdrant.go:17-18`), while `Reembed` lists the caller's tenant only (`pkg/registry/core/reembed.go:68`, `spec/04-artifact-model.md:770`) and the operator role confers no per-tenant admin rights (`spec/04-artifact-model.md:794`). SPEC-3 now requires a full re-embed in every tenant. DOC-3 has a tenant admin run the flagless pass in each provisioned tenant, states BM25-only search for a tenant whose pass has not started, and deletes the old index only after every tenant's pass reports an empty `failed` list. DOC-4, the Decisions bullets, the edge-case table (new multi-tenant row, corrected quote), and the Watch-out list match.
- **DOC-3 claimed the BM25-only fallback is reported as degraded.** `SearchResult.Degraded` is read nowhere outside `pkg/registry/core` (`core.go:1176`, `domain_search.go:47` are comments), and `docs/deployment/vector-backends.md:223` says the response carries no degraded field. DOC-3 now ends with "The search response reports none of these conditions" and links to Operational notes; the edge-case row says the response does not report the fallback; a Watch-out bullet records it.
- **The re-embed tenant citation pointed at the wrong line.** `pkg/registry/core/reembed.go:67` is the vector-not-configured guard. The Decisions D18 bullet and the tenant bullet above now cite `pkg/registry/core/reembed.go:68`, the `ListManifests(ctx, r.tenantFor(ctx))` call.
- **The degraded-field citation pointed at the wrong line.** `docs/deployment/vector-backends.md:222` is the managed-service costs bullet. The DOC-3 bullet above now cites `docs/deployment/vector-backends.md:223`, which states that the response bodies carry no degraded field.
