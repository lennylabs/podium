# Proposal 0032: Correct seven spec statements that contradict the spec, the code, or the deployed project

- Issue: (to be filed)
- Status: Approved (2026-10-01). Signed off as staged. OQ-1: the overlay and filesystem sync are to follow extends-or-reject, as separate follow-up work outside this proposal. OQ-2: no public hosting commitment; SPEC-6 and SPEC-7 stand. OQ-3: list the runtime-capability variables in §6.2 in a separate follow-up proposal.
- Date: 2026-10-01

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §4.6 (D8): the layer-list sentence "Higher-precedence layers override lower on collisions" defers to the §4.6 collision merge semantics (SPEC-1). The concepts page, its ASCII fallback, and the layer-composition diagram stop describing shadowing (DOC-1, DOC-2).
- §4.3 and §7.6 (D15): the `extends:` example uses the valid pin `@1.2.x`, and the `dependents_of` example passes the bare artifact ID (SPEC-2, SPEC-3).
- §4.1 and §7.2 (D19): the inline cutoff is stated as "at or below" and "above", with 256 KB defined as 262144 bytes (SPEC-4, SPEC-5). Two boundary tests pin the exact-cutoff case at ingest and at delivery (TEST-1, TEST-2).
- §1.6 and §9.2 (D20): the claims that a reference registry and a community plugin registry are hosted at the project's public URL are replaced by what exists (SPEC-6, SPEC-7, DOC-3). The end-to-end test that requires the deleted docs sentence is rewritten to pin its absence (TEST-4).
- §4.4.1 and §6.9 (D21): the `materialize.runtime_unavailable` refusal states its host opt-in condition, the override, and that `podium sync` does not check (SPEC-8, SPEC-9). The bundled-resources page, the frontmatter reference, the hooks page, and both authoring tutorials state the same condition (DOC-4).
- §13.2.1, §4.7.5, and §9.1 (D23, D12): "freeze toggles" leaves the read-only write examples and "freeze-window toggles" leaves the audited admin actions (SPEC-10, SPEC-11, DOC-5). Manual-validation scenario S45, which greps the runbook and HTTP API lists, follows (MV-1). The `NotificationProvider` default becomes opt-in, and the code comments that cite the old default follow (SPEC-12, CODE-1, TEST-3).

**Fixed decisions.**

- No error code, environment variable, endpoint, meta-tool, SPI, or behavior is added or changed. Production code changes are comments only.
- D8 removes the spec's internal contradiction only. The overlay and filesystem-sync shadowing in the code is OQ-1 and is not resolved here.
- D8 adds no test, because `TestIngest_CrossLayerCollisionRejected` and `TestWalk_CollisionWithoutExtendsFails` already pin the §4.6 rule.
- The D8 docs describe precedence only. The diagram column and the ASCII fallback list precedence without win or fallback semantics, so they hold whichever way OQ-1 is answered.
- D15 fixes the two examples differently: `@1.2.x` for `extends:`, and the bare ID for `dependents_of`, because the reverse index is keyed by the pin-stripped ID.
- D19 uses the wording "at or below" and "above", matching §7.6.2 and `docs/consuming/handling-artifact-responses.md`. §6.6 and §4.7.6 lines that say only "above" stay unchanged.
- D20 states only what exists. SPEC-6 names the §11 example artifact registry and no fixture path, and §9.2 keeps its Go-module distribution sentence. Whether to host a public registry or plugin index is OQ-2.
- D21 describes the condition by mechanism and names no environment variable. Whether §6.2 lists those variables is OQ-3.
- D23 keeps the §13.2.1 list as non-bounding examples and drops only the non-existent item. §4.7.5 gains no replacement item. §4.7.2 and §1.4 lines saying admins manage freeze windows stay unchanged.
- D12 replaces only the §9.1 Default cell. No list of built-ins is added to the spec.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **SPEC-1 must not claim that precedence orders the field merge.** The §4.6 field-semantics table is defined between child and parent. An earlier draft added "Precedence orders the layers for `extends:` resolution and for the field merge"; it is dropped on purpose and must not be reintroduced.
- **The diagram's right-hand column contradicts the footer as long as it says "wins" or "fallback".** DOC-2 rewrites the column now. Leaving it unchanged replaces one contradiction with another.
- **TEST-2 must seed an object-held ref directly.** At exactly the cutoff, ingest keeps `Inline` set, and the delivery path serves any ref with `Inline` set inline before it compares sizes. A delivery test driven through ingest passes against a `>=` comparison and so tests nothing.
- **TEST-2 asserts on `LoadArtifactResponse.Resources` and `LoadArtifactResponse.LargeResources`.** The `inline` and `presigned_url` fields belong to the batch-load envelope.
- **The overlay and sync code paths shadow on collision today.** `cmd/podium-mcp/main.go` (`loadArtifact`), `pkg/overlay/overlay.go`, and `pkg/sync/sync.go` use `CollisionPolicyHighestWins` or an overlay short-circuit. Do not change them in this proposal, and do not add a sentence documenting the MCP overlay shadowing, because either act decides OQ-1.
- **DOC-4, DOC-5, and MV-1 cover every restatement found by grep.** Each `docs/` statement of the runtime-requirement rule that omits the enforcement opt-in gains it, whether it states the refusal unconditionally or says unconditionally that a host with no capabilities proceeds. Each read-only write list drops "freeze toggles", and S45 changes with the lists it greps. Leaving any one of them unchanged leaves two contradictory statements of the same rule.
- **DOC-3 breaks an existing end-to-end test.** `TestPluginSPI_CommunityPluginRegistryDocGap` requires the sentence DOC-3 deletes and runs on every platform, so DOC-3 and its replacement, TEST-4, land in the same step.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §4.6 layer-list sentence defers to the collision merge semantics.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §4.3 `extends:` example uses `@1.2.x`.
      Levels: —. Depends on: —
- [ ] **S3 · spec** — SPEC-3. The §7.6 `dependents_of` example passes the bare artifact ID.
      Levels: —. Depends on: —
- [ ] **S4 · spec** — SPEC-4. The §4.1 inline cutoff states "at or below" and "above" and defines 262144 bytes.
      Levels: —. Depends on: —
- [ ] **S5 · spec** — SPEC-5. The §7.2 inline sentence says "At or below the inline cutoff".
      Levels: —. Depends on: —
- [ ] **S6 · spec** — SPEC-6. The §1.6 public-registry bullet states that the reference registry ships in the repository as the §11 example artifact registry.
      Levels: —. Depends on: —
- [ ] **S7 · spec** — SPEC-7. The §9.2 community-plugin-registry sentence is deleted.
      Levels: —. Depends on: —
- [ ] **S8 · spec** — SPEC-8. The §4.4.1 runtime-requirement paragraph states the opt-in condition, the override, and the `podium sync` scope.
      Levels: —. Depends on: —
- [ ] **S9 · spec** — SPEC-9. The §6.9 "Runtime requirement unsatisfiable" row states the opt-in condition.
      Levels: —. Depends on: S8
- [ ] **S10 · spec** — SPEC-10. The §13.2.1 write-endpoint examples drop "freeze toggles".
      Levels: —. Depends on: —
- [ ] **S11 · spec** — SPEC-11. The §4.7.5 audited admin actions drop "freeze-window toggles".
      Levels: —. Depends on: —
- [ ] **S12 · spec** — SPEC-12. The §9.1 `NotificationProvider` Default cell becomes opt-in.
      Levels: —. Depends on: —
- [ ] **S13 · code** — CODE-1. The `openNotifier` "multi" comment, the provider-list comment in the boot sequence, and the `SMTP` type comment stop citing the old default.
      Levels: unit. Depends on: S12
- [ ] **S14 · test** — TEST-1. Ingest boundary test at `InlineCutoff` and `InlineCutoff+1`.
      Levels: unit. Depends on: S4, S5
- [ ] **S15 · test** — TEST-2. Delivery boundary test at `InlineCutoff` and `InlineCutoff+1` over `GET /v1/load_artifact`.
      Levels: integration. Depends on: S4, S5
- [ ] **S16 · test** — TEST-3. `wiring_helpers_test.go` comment corrected and `TestOpenNotifier_NoopAndUnset` annotated with `// Spec: §9.1`.
      Levels: unit. Depends on: S12
- [ ] **S17 · docs** — DOC-1, DOC-2. The concepts-page prose and ASCII fallback, and the layer-composition SVG, describe precedence without shadowing. Bundled because the fallback mirrors the SVG and one reviewer checks both together.
      Levels: —. Depends on: S1
- [ ] **S18 · docs** — DOC-3, TEST-4. The `docs/deployment/extending.md` community-plugin-registry sentence is deleted, and `TestPluginSPI_CommunityPluginRegistryDocGap` is replaced by `TestPluginSPI_PluginDistributionGoModulesOnly`. Bundled because the existing test fails as soon as the sentence is deleted.
      Levels: e2e. Depends on: S7
- [ ] **S19 · docs** — DOC-4. `docs/authoring/bundled-resources.md`, `docs/authoring/your-first-skill.md`, and `docs/authoring/your-first-agent.md` state the enforcement opt-in beside the advertising condition, and `docs/authoring/frontmatter-reference.md` and `docs/authoring/hooks.md` state the refusal with its condition.
      Levels: —. Depends on: S8
- [ ] **S20 · docs** — DOC-5, MV-1. The read-only restatements drop "freeze toggles", and manual-validation scenario S45 expects the corrected lists. Bundled because S45 step 2 greps two of the restated documents and fails against either half landed alone.
      Levels: manual. Depends on: S10

**Ordering constraints.** The spec steps touch independent sentences and can land in any order, except that S9 restates S8. Each code, test, and docs step follows the spec step whose text it restates or cites.

## Current state and the gap

HEAD equals `main` (7682e037). Each defect below is open, and each cited spec line still carries the defective wording. The code agrees with itself on D12, D15, D19, D21, and D23, so those need wording corrections only. On D8 the code splits, so this proposal removes only the spec's internal contradiction.

### D8. Layer collisions

§4.6 "The layer list" says "Higher-precedence layers override lower on collisions." The same section's "Merge semantics for collisions" says "A collision is rejected at ingest **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter" and "Silent shadowing is never permitted." §6.4 says the workspace overlay's "merge semantics are identical to registry-side layers."

Registry ingest rejects the collision with `ingest.collision` and names `extends:` (`pkg/registry/ingest/ingest.go`). The filesystem walk rejects it under `CollisionPolicyError` (`pkg/registry/filesystem/walk.go`). `TestIngest_CrossLayerCollisionRejected` (`pkg/registry/ingest/extends_conformance_test.go`) and `TestWalk_CollisionWithoutExtendsFails` (`pkg/registry/filesystem/walk_test.go`, annotated `// Spec: §4.6` and `// Matrix: §6.10 (ingest.collision)`) pin that rule.

Two code paths shadow without an `extends:` check. The MCP server's `loadArtifact` returns the overlay record for a matching ID without consulting the registry (`cmd/podium-mcp/main.go`, `cmd/podium-mcp/load_domain.go`). Filesystem `podium sync` and the overlay reader use `CollisionPolicyHighestWins` (`pkg/sync/sync.go`, `pkg/overlay/overlay.go`, `pkg/registry/filesystem/walk.go`). OQ-1 owns that divergence.

`docs/getting-started/concepts.md` (lines 130-134), its ASCII fallback (lines 143-148), and the headline and right-hand column of `docs/assets/diagrams/layer-composition.svg` (lines 137-142) repeat the override wording. The diagram footer (line 148) states the rejection rule, so the diagram contradicts itself. `docs/authoring/extends.md` and `docs/deployment/layers.md` already state the rejection rule.

### D15. Pin examples

The §4.3 example `extends: finance/ap/pay-invoice@1.2` is not a valid pin. `version.ParsePin` (`pkg/version/version.go`) returns `ErrInvalidPin` for a two-part pin whose second part is not `x`, `pkg/version/version_test.go` lists `"1.2"` as invalid, and the §4.7.6 pin grammar does not allow it.

The §7.6 example `client.dependents_of("finance/ap/pay-invoice@1.2")` passes a pin where the method takes an artifact ID. Ingest stores the `extends` edge target with the pin stripped (`stripPin` in `pkg/registry/ingest/ingest.go`). The store matches `e.To == artifactID` exactly (`pkg/store/memory.go`), and neither `handleDependents` (`pkg/registry/server/server.go`) nor the Python SDK (`sdks/podium-py/podium/client.py`) strips a pin, so a pinned argument returns no edges. `docs/consuming/custom-via-sdk.md` line 148 already uses the bare ID, and `docs/authoring/frontmatter-reference.md` line 205 already uses `@1.2.x`.

### D19. Inline-cutoff boundary

Every routing comparison uses `> objectstore.InlineCutoff` or `<= objectstore.InlineCutoff`, with `InlineCutoff = 256 * 1024` (`pkg/objectstore/objectstore.go`): ingest (`pkg/registry/ingest/ingest.go`), delivery (`attachResources` in `pkg/registry/server/server.go`), and the manifest body (`pkg/registry/server/server.go`, `pkg/registry/core/core.go`). A resource of exactly 262144 bytes travels inline. §4.1 says "below this ... above, presigned URL" and §7.2 says "Below the inline cutoff", so the exact-cutoff case is unstated. §7.6.2 already says "at or below the §4.1 inline cutoff", as does `docs/consuming/handling-artifact-responses.md` line 51.

No test exercises `InlineCutoff` or `InlineCutoff+1` through the ingest or delivery decision. Existing tests use `InlineCutoff+N` with N of at least 1 and usually far larger, or seed the object store directly.

### D20. Hosted registries

§1.6 says "A reference registry with curated example artifacts is hosted at the project's public URL." §9.2 says "A community plugin registry is hosted at the project's public URL." No hosted registry or plugin index exists, and nothing in the repository names one. `README.md` links to the GitHub repository, the docs site, Codecov, Discord, and the Scoop bucket. The curated example registry exists as the repository fixture `testdata/registries/reference`, which §11 ("Example artifact registry") and §10 Phase 19 require. `docs/deployment/extending.md` line 51 repeats the plugin claim.

### D21. Runtime-requirement refusal

§4.4.1 says "Hosts that cannot satisfy a requirement reject the artifact at load time with `materialize.runtime_unavailable`" with no condition, and the §6.9 "Runtime requirement unsatisfiable" row repeats it. `enforceRuntimePolicy` in `cmd/podium-mcp/main.go` returns early unless `runtimeGateActive()` is true. The gate is active only when the host advertises `PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, or `PODIUM_HOST_PACKAGES`, or sets `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`. `PODIUM_IGNORE_RUNTIME_REQUIREMENTS` bypasses an active gate and logs a warning. The only call site is the MCP `load_artifact` path, so `podium sync` never checks requirements. `TestRuntimePolicy_InactiveWhenUnconfigured` and `TestRuntimePolicy_EnforceFlagFailsClosed` (`cmd/podium-mcp/runtime_policy_test.go`, citing §4.4.1) pin the gate. `docs/authoring/bundled-resources.md` line 104, `docs/authoring/your-first-skill.md` line 98, and `docs/authoring/your-first-agent.md` line 87 state the advertising condition and omit the enforcement opt-in, so each says unconditionally that a host advertising no capabilities proceeds. An opted-in host with no capabilities refuses (`runtimeGateActive` returns true on `s.cfg.enforceRuntime`, `cmd/podium-mcp/main.go:2288-2289`). The `runtime_requirements` row of `docs/authoring/frontmatter-reference.md` (line 158) says "The host refuses to materialize when a requirement isn't satisfied" with no condition. `docs/authoring/hooks.md` line 143, after the `system_packages: [jq]` example, says "The harness refuses to materialize when a system package isn't available" with no condition.

### D23. Freeze toggles

§13.2.1 gives "freeze toggles" as an example of a write endpoint that read-only mode rejects. No such endpoint exists. Freeze windows come only from the `registry.yaml` `freeze_windows:` key, read at boot (`internal/serverboot/yaml_config.go`; §4.7.2 "Freeze windows"). The admin routes are re-embed, grants, show-effective, tenants, and erase (`pkg/registry/server/server.go`, `pkg/registry/server/layers.go`). `rejectIfReadOnly` (`pkg/registry/server/readonly.go`) gates the actual write set. The only freeze-related write is break-glass reingest, which the reingest handler already gates.

§4.7.5 lists "freeze-window toggles" among audited admin actions. `pkg/audit/audit.go` has no such event; the only freeze event is `freeze.break_glass` (§8.1). `docs/reference/http-api.md` line 846, `docs/reference/error-codes.md` line 155, `docs/deployment/operator-guide.md` line 163, and `deploy/runbook.md` line 18 repeat "freeze toggles". Manual-validation scenario S45 (`test/manual-validation.md:4675`) greps the http-api and runbook lists, and its step 2 Expect (lines 4720-4721) requires both to name freeze toggles. Step 3 counts "the named five" categories (lines 4752 and 4755), and a paragraph (lines 4768-4774) leaves the freeze question open with "Add the assertion when that is settled."

### D12. NotificationProvider default

The §9.1 table gives the `NotificationProvider` default as "Email + webhook". `openNotifier` (`internal/serverboot/serverboot.go`) returns nil when `PODIUM_NOTIFICATION_PROVIDER` is unset or `noop`, so no notifier is wired, and `TestOpenNotifier_NoopAndUnset` (`internal/serverboot/wiring_helpers_test.go`) pins the nil result. The webhook and email built-ins return nil with a warning until their URL or SMTP host and sender are configured, so neither can be an unconfigured default. `docs/deployment/extending.md` line 40 is already correct. Three code comments repeat the old default: in `openNotifier`'s `"multi"` arm, on the `SMTP` type in `pkg/notification/notification.go`, and above `TestOpenNotifier_MultiIncludesEmail`. The boot-sequence comment that lists the providers omits `email`.

## Decisions

- **Corrections only.** Every spec edit corrects existing text. Where the code is consistent, the spec moves to match it, because the code behavior is the one the tests pin and the docs already describe.
- **D8 defers to the existing rule.** SPEC-1 replaces only the first sentence of the layer-list paragraph with a pointer to "Merge semantics for collisions", which already applies to every layer including the overlay (§6.4). It takes no position on the overlay and sync divergence.
- **D8 docs commit to a precedence-only description.** A column that lists precedence without win or fallback semantics holds whichever way OQ-1 is answered.
- **D15 examples follow their contracts.** `extends:` takes a pin, so the example becomes `@1.2.x`, which matches §4.7.6 and `ParsePin`. `dependents_of` takes an artifact ID, so the example drops the pin. The `dependents_of` contract stays unchanged.
- **D19 reuses existing wording and adds a test.** "At or below" and "above" are the §7.6.2 terms. The boundary tests exist because the spec states the exact-cutoff case for the first time and no test reaches it.
- **D20 states what exists.** The example registry is the §11 fixture. The spec does not cite test fixtures by path, so SPEC-6 names §11 and no path. §9.2 keeps its Go-module distribution sentence.
- **D21 follows the `sandbox_profile` precedent.** The adjacent §4.4.1 sandbox paragraph describes host configuration without naming environment variables, so SPEC-8 describes the gate by mechanism.
- **D23 removes only the false item.** The §13.2.1 list stays a non-bounding example list, and each page keeps its own conjunction.
- **D12 uses the table's opt-in form.** The §9.1 `MaterializationHook` row already reads "(none; opt-in, declared in client config)", and the `LocalAuditSink` and `SignatureProvider` rows already name their environment variables, so naming `PODIUM_NOTIFICATION_PROVIDER` adds no surface.
- **Diagram edits follow `.claude/rules/doc-diagram-style.md`.** The shared style block is reused, the SVG is rendered in both themes standalone and inlined, and the ASCII fallback changes with the SVG.

## Spec amendment: §4.6 layer-list collision sentence

**SPEC-1.** Anchor: `spec/04-artifact-model.md`, §4.6, the paragraph after the "Composition order (lowest to highest precedence)" list, which begins "Higher-precedence layers override lower on collisions." (line 589 at the time of writing).

Replace only the first sentence:

> Higher-precedence layers override lower on collisions.

with:

> A canonical-ID collision between layers follows the merge semantics for collisions below: the collision is rejected unless the higher-precedence artifact declares `extends:` on the lower-precedence one.

The rest of the paragraph, from "Resolution of layers 1 and 2 happens at the registry" through "layer 3 is merged in by the MCP server before returning results.", is unchanged. No sentence about precedence ordering the field merge is added.

## Spec amendment: §4.3 extends example pin

**SPEC-2.** Anchor: `spec/04-artifact-model.md`, §4.3, the type-specific fields YAML example, under the comment `# Inheritance — explicitly extend another artifact's manifest (cross-layer merge)` (line 232 at the time of writing).

Replace:

```yaml
extends: finance/ap/pay-invoice@1.2
```

with:

```yaml
extends: finance/ap/pay-invoice@1.2.x
```

## Spec amendment: §7.6 dependents_of example argument

**SPEC-3.** Anchor: `spec/07-external-integration.md`, §7.6, the SDK example under the comment `# Cross-type dependency walks (for impact analysis in custom tooling)` (line 609 at the time of writing).

Replace:

```python
deps = client.dependents_of("finance/ap/pay-invoice@1.2")
```

with:

```python
deps = client.dependents_of("finance/ap/pay-invoice")
```

## Spec amendment: §4.1 inline cutoff

**SPEC-4.** Anchor: `spec/04-artifact-model.md`, §4.1, the first bullet under "**Three size thresholds with distinct roles:**" (line 76 at the time of writing).

Replace:

> - **Inline cutoff (256 KB)**: below this, resource bytes are returned in the `load_artifact` response body; above, presigned URL. A resource the registry holds inline on the manifest record is returned inline at any size (§7.2).

with:

> - **Inline cutoff (256 KB, 262144 bytes)**: a resource at or below this size is returned in the `load_artifact` response body; a resource above it is returned as a presigned URL. A resource the registry holds inline on the manifest record is returned inline at any size (§7.2).

The per-file and per-package soft-cap bullets are unchanged.

## Spec amendment: §7.2 inline sentence

**SPEC-5.** Anchor: `spec/07-external-integration.md`, §7.2, the one-sentence paragraph after the "**Data plane (object storage).**" paragraph (line 32 at the time of writing).

Replace:

> Below the inline cutoff, resources are returned inline. This avoids round-trips for small fixtures.

with:

> At or below the inline cutoff, resources are returned inline. This avoids round-trips for small fixtures.

## Spec amendment: §1.6 reference registry

**SPEC-6.** Anchor: `spec/01-overview.md`, §1.6 Project Model, the bullet that begins "**Public registry.**" (line 121 at the time of writing).

Replace:

> - **Public registry.** A reference registry with curated example artifacts is hosted at the project's public URL.

with:

> - **Reference registry.** A multi-layer example registry with curated artifacts across every first-class type ships in the repository as the §11 example artifact registry.

## Spec amendment: §9.2 community plugin registry

**SPEC-7.** Anchor: `spec/09-extensibility.md`, §9.2 Plugin Distribution, the second paragraph (line 34 at the time of writing).

Delete the paragraph:

> A community plugin registry is hosted at the project's public URL.

The first paragraph ("Plugins ship as Go modules importable into a registry build. ...") is unchanged and becomes the whole of §9.2.

## Spec amendment: §4.4.1 runtime-requirement refusal

**SPEC-8.** Anchor: `spec/04-artifact-model.md`, §4.4.1 Execution Model Contract, the paragraph after the `runtime_requirements:` YAML example (line 365 at the time of writing).

Replace:

> Adapters surface these requirements to the host where supported. Hosts that cannot satisfy a requirement reject the artifact at load time with `materialize.runtime_unavailable`.

with:

> Adapters surface these requirements to the host where supported. Once a host advertises its runtime capabilities to the Podium MCP server, or opts into enforcement, the MCP server refuses a `load_artifact` whose requirements the advertised capabilities do not satisfy with `materialize.runtime_unavailable`. A host that advertises no capabilities and does not opt in receives the requirements without a refusal. An explicit host override bypasses the refusal and logs a warning. `podium sync` does not evaluate runtime requirements.

The `sandbox_profile:` paragraph that follows is unchanged.

## Spec amendment: §6.9 runtime-requirement failure row

**SPEC-9.** Anchor: `spec/06-mcp-server.md`, §6.9, the failure-mode table row whose first cell is "Runtime requirement unsatisfiable" (line 426 at the time of writing).

Replace the Behavior cell:

> Fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.

with:

> When the host advertises runtime capabilities or opts into enforcement (§4.4.1), fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.

The first cell is unchanged. Re-pad the row to the table's column widths where the source aligns them.

## Spec amendment: §13.2.1 read-only write examples

**SPEC-10.** Anchor: `spec/13-deployment.md`, §13.2.1 Read-Only Mode, first paragraph (line 41 at the time of writing).

Replace the sentence:

> Ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management are named examples and do not bound the rule.

with:

> Ingest webhooks, layer admin operations, admin grants, and tenant management are named examples and do not bound the rule.

The rest of the paragraph, including the SCIM exemption and the per-endpoint classification sentence, is unchanged.

## Spec amendment: §4.7.5 audited admin actions

**SPEC-11.** Anchor: `spec/04-artifact-model.md`, §4.7.5 Audit (line 860 at the time of writing).

Replace the parenthetical:

> admin actions (layer-list edits, freeze-window toggles, admin grants)

with:

> admin actions (layer-list edits, admin grants)

The rest of the paragraph is unchanged. §4.7.2 "Freeze windows" and §1.4, which say that admins manage freeze windows, are unchanged.

## Spec amendment: §9.1 NotificationProvider default

**SPEC-12.** Anchor: `spec/09-extensibility.md`, §9.1 Pluggable Interfaces table, the row whose first cell is `` `NotificationProvider` `` (line 25 at the time of writing).

Replace the Default cell:

> Email + webhook

with:

> (none; opt-in via `PODIUM_NOTIFICATION_PROVIDER`)

The Purpose cell ("Delivery for ingest-failure and operational notifications") is unchanged. No list of built-in providers is added. Re-pad the cell to the table's column width.

## Proposed solution

The proposal makes no production behavior change. The code changes are comment edits and the test changes listed under Testing.

**CODE-1.** The code changes are comment-only edits.

- `internal/serverboot/serverboot.go`, `openNotifier`, the comment in the `"multi"` arm. Replace

  ```go
  		// §9.1 default "Email + webhook": "multi" combines the log
  		// provider with the webhook and email providers when each is
  		// configured. Useful for "alert + record" deployments.
  ```

  with

  ```go
  		// §9.1 "multi" combines the log provider with the webhook and
  		// email providers when each is configured. Useful for
  		// "alert + record" deployments.
  ```

- `internal/serverboot/serverboot.go`, the comment above `notifier := openNotifier()` in the boot sequence. Replace `(one of "noop", "log", "webhook", or "multi")` with `(one of "noop", "log", "webhook", "email" (or "smtp"), or "multi")`. The rest of the comment is unchanged.
- `pkg/notification/notification.go`, the `SMTP` type comment. Replace `the email half of the §9.1 NotificationProvider "Email + webhook" default` with `the email delivery of the §9.1 NotificationProvider`, and rewrap the comment.

Run `gofmt`, `go build ./...`, and `make lint`.

**TEST-1, TEST-2, TEST-3, TEST-4.** See Testing.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Two registry layers contribute the same canonical ID and the higher one declares no `extends:` | Ingest rejects with `ingest.collision`; `podium lint` reports the same collision | §4.6 "Merge semantics for collisions" and SPEC-1; `docs/deployment/layers.md` "Merge behavior worth knowing", `docs/authoring/extends.md` |
| Filesystem-source `podium sync` materializes a catalog with a same-ID collision | The highest-precedence copy is materialized; no error (accepted and deferred to OQ-1) | No spec text states it; §4.6 and §6.4 currently say the collision is rejected. `docs/deployment/layers.md` "Merge behavior worth knowing" states the sync behavior |
| The workspace overlay carries an ID that a registry layer also serves | The MCP server serves the overlay copy without consulting the registry (accepted and deferred to OQ-1) | No spec or docs sentence is staged, because any sentence would decide OQ-1. §6.4 "merge semantics are identical to registry-side layers" stays the governing text until OQ-1 is answered |
| An `extends:` reference with a two-part pin such as `@1.2` | Ingest of the child fails with a pin parse error | §4.7.6 pin grammar; SPEC-2 corrects the example; `docs/authoring/frontmatter-reference.md` |
| `dependents_of` called with a pinned ID | Returns no edges, because edges are keyed by the pin-stripped ID | §7.6 example after SPEC-3; `docs/consuming/custom-via-sdk.md` uses the bare ID. No sentence states the empty result, because the contract takes an artifact ID |
| A bundled resource of exactly 262144 bytes with an object store configured | Stored in object storage and kept inline on the record; served in the `load_artifact` body | SPEC-4, SPEC-5, §7.6.2; `docs/consuming/handling-artifact-responses.md` |
| A bundled resource of 262145 bytes with an object store configured | Served as a presigned URL in `large_resources` | SPEC-4; `docs/consuming/handling-artifact-responses.md` |
| A resource above the cutoff held inline on the record (ingested with no object store) | Served inline at any size | §4.1 (SPEC-4, unchanged sentence), §7.2; `docs/consuming/handling-artifact-responses.md` |
| A host advertises no runtime capabilities and does not opt in | `load_artifact` returns the artifact and its requirements; no refusal | SPEC-8; `docs/authoring/bundled-resources.md`, `docs/authoring/your-first-skill.md`, `docs/authoring/your-first-agent.md`, `docs/authoring/hooks.md` (DOC-4) |
| A host advertises capabilities that do not satisfy a requirement | `load_artifact` fails with `materialize.runtime_unavailable`, naming the requirement | SPEC-8, SPEC-9; `docs/authoring/bundled-resources.md`, `docs/reference/error-codes.md` |
| A host opts into enforcement and advertises no capabilities | Any declared requirement is unsatisfied, so `load_artifact` fails with `materialize.runtime_unavailable` | SPEC-8, SPEC-9; `docs/authoring/bundled-resources.md` (DOC-4) |
| A host sets the override while the gate is active | The load proceeds and the MCP server logs a warning | SPEC-8. No docs page names the override (OQ-3) |
| `podium sync` materializes an artifact with unsatisfiable requirements | The artifact is written; requirements are not evaluated | SPEC-8; `docs/authoring/bundled-resources.md`, `docs/authoring/frontmatter-reference.md`, `docs/authoring/hooks.md` (DOC-4) |
| An operator wants to change a freeze window during read-only mode | No endpoint exists; the window changes by editing `registry.yaml` and restarting | §4.7.2 "Freeze windows"; `docs/deployment/progressive-adoption.md`, `docs/deployment/clustered.md` |
| Break-glass reingest during read-only mode | Rejected with `registry.read_only` | §13.2.1 (SPEC-10, non-bounding examples); `docs/reference/error-codes.md` |
| `PODIUM_NOTIFICATION_PROVIDER` unset or `noop` | No notifier is wired; no notifications are sent | SPEC-12; `docs/deployment/extending.md` |
| `PODIUM_NOTIFICATION_PROVIDER=webhook` with no webhook URL, or `email` with no SMTP host and sender | A warning is logged and no notifier is wired | SPEC-12 (opt-in); `docs/deployment/extending.md` states that each built-in needs its configuration. The warning itself is not documented, which is a non-goal |
| A reader looks for a hosted reference registry or plugin index | None exists; the example registry ships in the repository | SPEC-6, SPEC-7 (OQ-2); `docs/deployment/extending.md` after DOC-3 |

## Testing

**TEST-1 · unit, `pkg/registry/ingest/resources_test.go`.** Lands in S14.

- Add `TestIngest_InlineCutoffBoundary`, annotated `// Spec: §4.1, §7.2`, as a table over sizes `{objectstore.InlineCutoff, objectstore.InlineCutoff + 1}`.
- For each size, ingest a skill with one bundled resource of that size, with `ResourcePut: rec.put` from `newPutRecorder()`, following `TestIngest_PersistsResourcesWithObjectStore`.
- Assert that `rec.objects[hashKey(body)]` holds the bytes in both cases.
- At `InlineCutoff`, assert that the stored `ResourceRef.Inline` equals the bytes. At `InlineCutoff+1`, assert that `Inline` is nil and `Size` equals the length.
- The `InlineCutoff` row fails if the ingest comparison becomes `>=`, and the `InlineCutoff+1` row fails if it becomes `> InlineCutoff+1` or the inline bytes are kept.

**TEST-2 · integration, `pkg/registry/server/resource_delivery_test.go`.** Lands in S15.

- Add `TestResourceDelivery_InlineCutoffBoundary`, annotated `// Spec: §4.1, §7.2`, as a table over sizes `{objectstore.InlineCutoff, objectstore.InlineCutoff + 1}`.
- For each size, seed a sealed `store.ManifestRecord` with one object-held ref: `Inline` nil, `Size` set, and `ContentHash` matching bytes `Put` into `objectstore.NewMemory()`. Follow the `core.New(...).WithAdmission(nil, objects, objectstore.DefaultReadTimeout)` and `server.WithObjectStore` pattern of `TestResourceDelivery_InlineRefAboveCutoffServedInlineWithObjectStore`.
- `GET /v1/load_artifact` and decode `server.LoadArtifactResponse`.
- At `InlineCutoff`, assert that the path is in `Resources` with the exact bytes and absent from `LargeResources`. At `InlineCutoff+1`, assert that it is in `LargeResources` and absent from `Resources`.
- Do not drive this test through ingest. The `Inline`-set short circuit in `attachResources` would serve the boundary resource inline before the size comparison runs.

**TEST-3 · unit, `internal/serverboot/wiring_helpers_test.go`.** Lands in S16.

- Replace the comment above `TestOpenNotifier_MultiIncludesEmail` with `// Spec: §9.1 — the "multi" built-in includes the email provider when SMTP is configured alongside the webhook.`
- Add `// Spec: §9.1 — an unset PODIUM_NOTIFICATION_PROVIDER or "noop" wires no notifier.` above `TestOpenNotifier_NoopAndUnset`, which then pins the corrected default. The test body is unchanged.

**TEST-4 · e2e, `test/e2e/plugin_spi_test.go`.** Lands in S18 with DOC-3.

- `TestPluginSPI_CommunityPluginRegistryDocGap` (line 1224 at the time of writing) fails once DOC-3 lands, because it asserts that `docs/deployment/extending.md` contains "community plugin registry", and line 51 is the only occurrence. The test has no skip and the file has no build tag, so it runs on every platform. Its comment also records a "doc-accuracy gap" that DOC-3 removes.
- Replace it with `TestPluginSPI_PluginDistributionGoModulesOnly`, annotated `// Spec: §9.2`, which reads the same file through `repoRoot(t)` and `readFile(t, ...)`.
- Assert that the content contains neither "community plugin registry" nor "hosted at the project's public URL".
- Assert that the content still contains the "Plugin distribution" heading and the sentence "Plugins ship as Go modules importable into a registry build." (`docs/deployment/extending.md:49`).
- Drop the `t.Logf` gap message.

No test is added for D8, D15, D21, or D23. D8 and D21 are pinned by `TestIngest_CrossLayerCollisionRejected`, `TestWalk_CollisionWithoutExtendsFails`, and the §4.4.1 runtime-policy tests, which cover the unconfigured, advertised, enforce-only, and override branches. D15 and D23 change example and list text with no behavior behind it. D20 also changes text with no behavior behind it, and its only test change is TEST-4.

Run `go test ./pkg/registry/ingest/ ./pkg/registry/server/ ./internal/serverboot/`, `go test -run TestPluginSPI_PluginDistributionGoModulesOnly ./test/e2e/`, and `make coverage-gate`.

## Manual validation

No new manual validation scenario is staged. The proposal changes no served bytes, materialized output, CLI or HTTP response, audit stream, or startup behavior. It does change the operator-facing write-set lists in `deploy/runbook.md` and `docs/reference/http-api.md` (DOC-5), which existing scenario S45 reads, so S45 changes with them.

**MV-1.** `test/manual-validation.md`, scenario S45 "The runbook's read-only write set matches what the registry rejects" (line 4675). Lands in S20 with DOC-5.

- Step 2 Expect (lines 4720-4721). Replace `Both enumerate ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management.` with `Both enumerate ingest webhooks, layer admin operations, admin grants, and tenant management, and neither names freeze toggles.` Rewrap. The rest of the Expect paragraph is unchanged.
- Step 3 (line 4752). Replace `the named five "do not bound the rule"` with `the named categories "do not bound the rule"`.
- Step 3 (line 4755). Replace `**Two of the five named categories cannot reach` with `**Two of the named categories cannot reach`.
- Step 3, the paragraph beginning `**Freeze toggles are not asserted here.**` (lines 4768-4774). Replace it with:

  > **No freeze endpoint is in the write set.** Freeze windows are configuration-only: they come from the `registry.yaml` `freeze_windows:` key (`internal/serverboot/yaml_config.go`, §4.7.2), are enforced during ingest, and are bypassed with `podium layer reingest --break-glass`. §13.2.1 does not name freeze toggles, and the break-glass reingest write is covered by the `POST /v1/layers/reingest` probe above.

  Rewrap to the file's line width.

The step 3 probe list already includes `POST /v1/layers/reingest`, which `rejectIfReadOnly` gates (`pkg/registry/server/layers.go`, the `reingest` handler), so MV-1 adds no request and no new assertion beyond the corrected list.

## Documentation changes

**DOC-1.** `docs/getting-started/concepts.md`.

(a) Lines 130-134. Replace:

> When a caller asks for an artifact, Podium composes the caller's **effective view** from every visible layer, in precedence order. Higher-precedence layers override lower on collisions; `extends:` lets a higher artifact inherit and refine a lower one without forking.

with:

> When a caller asks for an artifact, Podium composes the caller's **effective view** from every visible layer, in precedence order. Two layers cannot contribute the same canonical ID unless the higher-precedence artifact declares `extends:` on the lower one. In that case the higher artifact inherits and refines the lower one without forking (see [extends](../authoring/extends.md)).

Rewrap to the page's line width.

(b) The ASCII fallback (lines 143-148). Replace the right-hand text `Higher layers override on collision.` with `Precedence orders the composed view.`, and replace the four rows with:

```
overlay   -> highest
alice     -> if visible
finance   -> if alice is a member
org       -> lowest, visible to anyone
```

Keep the left-hand column, the `|` separator, and the alignment. The `->` arrows are safe inside the HTML comment because they do not contain `-->`. Add no statement about overlay or sync collision behavior to this page.

**DOC-2.** `docs/assets/diagrams/layer-composition.svg`.

- Line 137: replace the text `Higher layers override on collision.` with `Precedence orders the composed view.`, keeping `class="dg-name dg-t-l"`. If it overflows the panel, use the next size modifier down.
- Lines 139-142: replace the four `dg-mono` rows with `overlay  → highest`, `alice    → if visible`, `finance  → if alice is a member`, and `org      → lowest, visible to anyone`. Equivalent rows are acceptable provided no row says "wins" or "if no overlay match".
- The footer lines 148-150 are unchanged.
- Render in both themes, standalone and inlined, and run the `.claude/rules/doc-diagram-style.md` hazard sweep.

**DOC-3.** `docs/deployment/extending.md` line 51. Delete the paragraph "A community plugin registry is hosted at the project's public URL." The "Plugin distribution" section keeps its Go-module paragraph. TEST-4 replaces the end-to-end test that requires the deleted sentence, in the same step.

**DOC-4.** `docs/authoring/bundled-resources.md` line 104. Replace:

> A host that advertises its runtime capabilities to the Podium MCP server refuses a `load_artifact` it cannot satisfy with `materialize.runtime_unavailable`. A host that advertises no capabilities receives the requirement and proceeds, and `podium sync` materializes the artifact without checking it.

with:

> A host that advertises its runtime capabilities to the Podium MCP server, or opts into enforcement, refuses a `load_artifact` it cannot satisfy with `materialize.runtime_unavailable`. A host that advertises no capabilities and does not opt in receives the requirement and proceeds, and `podium sync` materializes the artifact without checking it.

`docs/authoring/frontmatter-reference.md` line 158, the `runtime_requirements` row. Replace the Description cell:

> Map of runtime versions and system packages the bundled scripts depend on. The host refuses to materialize when a requirement isn't satisfied.

with:

> Map of runtime versions and system packages the bundled scripts depend on. A Podium MCP server whose host advertises its runtime capabilities or opts into enforcement refuses a `load_artifact` whose requirements those capabilities do not satisfy, with `materialize.runtime_unavailable`. `podium sync` does not check requirements. See [Bundled resources](bundled-resources).

The link follows the row-level form the table already uses (`See [Hints](hints)`). The `sandbox_profile` row is unchanged.

`docs/authoring/your-first-skill.md` line 98. Replace `A host that advertises no capabilities receives the requirement and proceeds, and` with `A host that advertises no capabilities and does not opt into enforcement receives the requirement and proceeds, and`. The rest of the paragraph is unchanged.

`docs/authoring/your-first-agent.md` line 87. Replace `A host that advertises no capabilities receives the requirement and proceeds.` with `A host that advertises no capabilities and does not opt into enforcement receives the requirement and proceeds.` The rest of the paragraph is unchanged.

`docs/authoring/hooks.md` line 143, the sentence after the `runtime_requirements:` example. Replace:

> The harness refuses to materialize when a system package isn't available.

with:

> A Podium MCP server whose host advertises its runtime capabilities or opts into enforcement refuses a `load_artifact` when a declared system package is unavailable, with `materialize.runtime_unavailable`. `podium sync` does not check requirements. See [Bundled resources](bundled-resources).

The link uses the relative form the page's sibling links use.

The tutorials keep the advertising sentence that precedes each correction, because it is true as written; the correction removes only the unconditional claim about a host with no capabilities.

**DOC-5.** Read-only write examples. Remove "freeze toggles" from each list and keep the rest of each sentence:

- `docs/reference/http-api.md` line 846: `(ingest webhooks, layer admin operations, admin grants, and tenant management)`.
- `docs/deployment/operator-guide.md` line 163: `(ingest webhooks, layer admin operations, admin grants, and tenant management)`.
- `deploy/runbook.md` lines 17-19: `(ingest webhooks, layer admin operations, admin grants, and tenant management)`, rewrapped.
- `docs/reference/error-codes.md` line 155: `(ingest, layer admin, admin grants, tenant management)`.

No other docs page changes. `docs/authoring/artifact-types.md` line 58 says the host "can refuse", which is consistent with the conditional rule. `docs/deployment/extending.md` line 40, `docs/consuming/custom-via-sdk.md`, the `extends:` example in `docs/authoring/frontmatter-reference.md` (line 205), and `docs/consuming/handling-artifact-responses.md` already match the corrected spec.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-01, automated)

- **The frontmatter reference stated the runtime refusal unconditionally and was outside DOC-4.** `docs/authoring/frontmatter-reference.md:158` said the host refuses whenever a requirement is unsatisfied. DOC-4 now replaces that Description cell with the conditional form and a link to Bundled resources; the D21 current state, the S19 step, the Summary, and the "No other docs page changes" sentence name the page.
- **Manual-validation scenario S45 asserted the freeze-toggles list that DOC-5 and SPEC-10 remove.** MV-1 now stages the S45 edits: the step 2 Expect lists the corrected categories, the step 3 counts become count-free, and the open-question paragraph about freeze toggles is replaced with the configuration-only statement. The Manual validation section, the D23 current state, the S20 step, and the Summary record it.
- **The tutorials said a no-capability host proceeds, contradicting SPEC-8 and DOC-4 for the enforcement opt-in.** DOC-4 now adds "and does not opt into enforcement" to `docs/authoring/your-first-skill.md:98` and `docs/authoring/your-first-agent.md:87`. The Watch-out bullet and the DOC-4 note that kept the tutorials unchanged are removed.
- **The hooks page stated the runtime refusal unconditionally and was outside DOC-4.** `docs/authoring/hooks.md:143` said "The harness refuses to materialize when a system package isn't available" with no condition, so the Watch-out claim of grep-complete coverage was false. DOC-4 now replaces that sentence with the conditional form and a link to Bundled resources. The S19 step, the Summary, the D21 current state, and the no-capability and `podium sync` edge-case rows name the page, the Watch-out bullet now scopes its claim to unconditional refusals, and the "No other docs page changes" sentence records that `docs/authoring/artifact-types.md:58` ("can refuse") already agrees.

### Pass 2 (2026-10-01, automated)

- **DOC-3 deleted the sentence that `TestPluginSPI_CommunityPluginRegistryDocGap` requires, and no test change was staged.** `test/e2e/plugin_spi_test.go:1224-1236` asserts that `docs/deployment/extending.md` contains "community plugin registry", which occurs only at line 51. TEST-4 now replaces it with `TestPluginSPI_PluginDistributionGoModulesOnly` (`// Spec: §9.2`), which asserts the hosting claim is absent and the Go-module paragraph remains. S18 bundles DOC-3 with TEST-4, and the Summary, the Proposed solution, the DOC-3 entry, and the Testing sentence on D20 record it.

## Open questions

**OQ-1. D8: overlay and filesystem-sync collisions.** §4.6 and §6.4 say a collision is rejected unless the higher artifact declares `extends:`, and that the overlay's merge semantics are identical to the registry's. The MCP overlay path (`cmd/podium-mcp/main.go`, `cmd/podium-mcp/load_domain.go`) and filesystem-source `podium sync` (`pkg/sync/sync.go`, `pkg/overlay/overlay.go`) let the higher layer shadow the lower one, while a server ingest of the same layers rejects them, so the deployment modes diverge. Should the overlay and filesystem sync follow extends-or-reject, which needs a follow-up code proposal? Or should the spec allow overlay shadowing explicitly, which needs a spec amendment that names the exception and accepts the split with §13.11.3 and the §11 equivalence test? The answer also decides whether the diagram's precedence column gains win semantics.

**OQ-2. D20: hosting.** Should the project commit to hosting the reference registry, a community plugin index, or both at a public URL? If yes, SPEC-6 and SPEC-7 become explicitly planned statements, with a URL once one exists. If no, SPEC-6 and SPEC-7 stand.

**OQ-3. D21: configuration surface.** Should §6.2 list the host runtime-capability variables (`PODIUM_HOST_PYTHON`, `PODIUM_HOST_NODE`, and `PODIUM_HOST_PACKAGES`) and the enforcement and override flags (`PODIUM_ENFORCE_RUNTIME_REQUIREMENTS` and `PODIUM_IGNORE_RUNTIME_REQUIREMENTS`)? They exist in `cmd/podium-mcp/main.go` with no spec or docs entry. SPEC-8 describes them by mechanism, following the §4.4.1 `sandbox_profile` precedent.

## Non-goals

- Changing how the MCP workspace overlay or filesystem-source `podium sync` handles collisions (`CollisionPolicyHighestWins` and the `loadArtifact` overlay short-circuit). OQ-1 owns this.
- Changing the inline cutoff value or the routing comparison in `pkg/objectstore`, `pkg/registry/ingest`, `pkg/registry/server`, or `pkg/registry/core`.
- Adding a freeze-window HTTP or CLI toggle, or an audit event for one.
- Adding the runtime-capability and enforcement variables to the §6.2 configuration table. OQ-3 owns this.
- Making `podium sync` evaluate runtime requirements.
- Changing what `openNotifier` does when unset, or documenting the warning it logs for an unconfigured webhook or email provider.
- Standing up a hosted public registry or plugin index. OQ-2 owns this.
- Changing the `dependents_of` contract so it accepts a pinned ID.
- Editing §4.7.2 "Freeze windows" or §1.4, which accurately say that admins manage freeze windows through configuration.
- Editing the §6.6 and §4.7.6 lines whose "above the inline cutoff" wording already matches the code.
