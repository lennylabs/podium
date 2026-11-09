# Proposal 0057: Require an extends: child to restate every executable field a parent in another layer sets

- Issue: (to be filed)
- Status: Applied to spec (2026-11-09). Verified on 2026-11-09 after 10 adversarial review rounds (20 findings fixed).
- Date: 2026-11-09

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is backward-incompatible: ingest and a filesystem-source `podium sync` refuse an `extends:` child that ingests or syncs today.

## Summary

**What changes.**

- §4.6 defines the executable fields and a rule that a child whose pinned parent comes from another layer restates every executable field that parent sets, or ingest rejects it with `ingest.invalid_artifact` and `details.constraint: "executable_inheritance"`. §7.3.1 lists the new drop and the reingest `details` member, with the envelope scope SPEC-2 edit 2 states. §13.11.3 makes a filesystem-source `podium sync` drop a refused child, recompose the layers without it, and cascade the drop, as SPEC-3 states (SPEC-1, SPEC-2, SPEC-3).
- `pkg/manifest` gains `InheritedExecutableFields`, the one comparison both modes call. `pkg/layer` gains `ExecutableInheritance`, a general `Drop` record, `InvalidArtifactCode`, and the shared reason helpers. `pkg/registry/core` exports its chain walk as `ExtendsChain` and its fold as `ResolvedArtifact` (CODE-1, CODE-2, CODE-3).
- `pkg/registry/ingest` runs the check on the insert path, after the idempotency switch, and carries `RejectedArtifact.Details`. `pkg/registry/server` serializes `details` on the reingest `rejected` entry (CODE-4, CODE-5).
- `pkg/registry/filesystem` gains a per-record drop path in `extends:` resolution, settled by `composeExtends`, and `pkg/sync` and `cmd/podium` carry `[]layer.Drop` everywhere they carried `[]layer.Collision` (CODE-6, CODE-7).
- Unit, integration, and end-to-end tests pin the rule in both modes and their equivalence (TEST-1 to TEST-6). Scenario S91 joins `test/manual-validation.md` (MV-1), and scenario S39's cross-layer overlay restates the undeclared key it used to inherit (MV-2). `docs/authoring/extends.md`, the frontmatter pages, the reference pages, and `CHANGELOG.md` follow (DOC-1, DOC-2, DOC-3).

**Fixed decisions.**

- D-1: the executable set, defined in SPEC-1 edit 2.
- D-2: the §4.6 fields outside the set.
- D-3: what "the parent sets" means.
- D-4: what "restate" means, and the equality check CODE-1 implements.
- D-5: the immediate cross-layer edge, and the parent-selection divergence SPEC-3 states.
- D-6: the server checks new inserts alone.
- D-7: the error code and the `details` field-path forms.
- D-8: withholding in the reason, `details`, the field paths, and the chain-walk abort.
- D-9: the filesystem-source drop, recomposition, and cascade.
- D-10: no new surface, and `[]layer.Drop` in place of `[]layer.Collision`.
- D-11: the unchanged `TypeProvider` SPI.
- D-12: the shared comparison, reasons, constants, and chain walk.
- D-13: the delivery-signature statement.
- D-14: spec-first ordering and the CHANGELOG entry.

**Watch out for.**

- **Server lint refuses a hook child that omits `hook_action` before this rule runs.** The ingest linter runs `ruleTypeRequiredFields` on the authored, pre-merge record (`pkg/lint/required_fields.go:45-51`; `pkg/registry/ingest/ingest.go:584-587`), so a `type: hook` child without `hook_action` lands in `LintFailures` and never reaches CODE-4. A server-side test or scenario that uses a hook example pins lint and nothing about this rule. Use `server_identifier`, an `mcpServers` entry, or an undeclared key in every server-side case. The filesystem source runs no lint, so a hook example does reach CODE-6 there.
- **Place the server check after the idempotency switch.** The `extends:` block runs before the switch (`pkg/registry/ingest/ingest.go:665` against `:767-782`), and every cycle re-walks the whole snapshot. A check beside the existing `extends:` rejections drops an unchanged stored child on every reingest. The check goes in the `errors.Is(err, store.ErrNotFound)` arm, before signing.
- **The chain walk's error contract changes in CODE-3.** `(*Registry).resolveExtendsChain` formats the store error with `%v` under `core.ErrNotFound` (`pkg/registry/core/core.go:1830-1832`), so `errors.Is(err, store.ErrNotFound)` is false for every chain failure. CODE-4's abort-versus-reject branch depends on CODE-3 wrapping with two `%w` verbs. Do not extract the body verbatim.
- **Existing fixtures inherit executable fields across layers.** TEST-6 and MV-2 update the known ones in a step that lands before any rule code, and each update passes before and after the rule. The known set is listed in TEST-6 and MV-2. When CODE-4 or CODE-6 turns up another failing fixture, fix it in that step under TEST-6's constraint, and never weaken an assertion to make it pass. A hand-run scenario in `test/manual-validation.md` fails no automated run, so MV-2 names it explicitly.
- **A same-layer fixture must order the parent first.** Server ingest walks one layer and processes its records sorted by canonical ID (`pkg/registry/ingest/ingest.go:1024`), and it resolves a pin only against rows already stored (`:1519-1521`). A fixture that moves a child into its parent's layer, or adds a within-layer child, gives the parent an ID that sorts before the child's (TEST-6).
- **The filesystem resolver rewrites records in place.** `resolveExtends` replaces `deduped[i].Artifact` and `ArtifactBytes` with merged values once it processes a record (`pkg/registry/filesystem/extends.go:65-76`). CODE-1 takes the child's authored bytes, and CODE-6 validates every record before the rewrite pass. A check that reads a processed record sees inherited values as child-authored and passes silently.
- **CODE-6 and CODE-7 land in one commit.** Renaming `WalkOptions.OnCollision` to `OnDrop` breaks every `pkg/sync` caller and the test files that CODE-6 and CODE-7 list under their compile-required test edits, until those edits land.
- **The two modes do not always select the same parent.** D-5 gives the evidence and SPEC-3 states the divergence; every equivalence test uses a fixture where both modes select the same parent.
- **The same-ID cascade reason differs between modes.** A server rejects an overlay whose only lower record was never stored with the reason `extends: self-extends cycle: ...`, and the filesystem cascade uses the shared unresolved-parent reason. The codes match. Tests compare codes across modes and compare reasons only within a mode.
- **`mcpServers: []` and an undeclared key set to `[]` behave differently.** A decoded empty slice cannot be told apart from an absent key, and `mergeMCPServers` keeps every parent entry, so the child must still restate each entry. An undeclared key set to `[]` is a value other than an empty scalar and counts as restated. TEST-1 pins both side by side.
- **TEST-4's equivalence fixture must not use `hook_action`.** It would pass on the server through lint with CODE-4 absent.
- **Parts of `test/e2e` skip silently on macOS.** Run the end-to-end steps with `-v` and grep the output for `SKIP` before claiming they passed.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3. The §4.6 rule, the §7.3.1 drop and `details` sentence, and the §13.11.3 filesystem drop land in one commit, because each cites the others and §4.6 would contradict §13.11.3 alone.
      Levels: unit. Depends on: —
- [ ] **S2 · test** — TEST-6, MV-2. Existing fixtures and scenario S39 stop relying on cross-layer inheritance of an executable field. This test step precedes the code steps on purpose: each update passes before and after the rule, so every later commit stays green.
      Levels: unit, integration, e2e, manual. Depends on: S1
- [ ] **S3 · code** — CODE-1. `manifest.InheritedExecutableFields` and `ErrUnreadableChain`.
      Levels: unit. Depends on: S1
- [ ] **S4 · code** — CODE-2. `layer.ExecutableInheritance`, `layer.Drop`, `Collision.Drop`, `InvalidArtifactCode`, and the shared reasons, with ingest's literals replaced.
      Levels: unit. Depends on: S1
- [ ] **S5 · code** — CODE-3. `core.ExtendsChain` and `core.ResolvedArtifact`, with the two-`%w` error contract and its unit test.
      Levels: unit. Depends on: S1
- [ ] **S6 · code** — CODE-4, CODE-5. The server check on the insert path and the `details` member on the wire. Bundled because `Details` has no reader until the response serializes it, and one reviewer checks the field end to end.
      Levels: unit, integration, e2e. Depends on: S2, S3, S4, S5
- [ ] **S7 · code** — CODE-6, CODE-7. The filesystem drop path and the `layer.Drop` plumbing through `pkg/sync` and `cmd/podium`. Bundled because the `OnDrop` rename does not compile without the callers it changes.
      Levels: unit, integration, e2e. Depends on: S2, S3, S4
- [ ] **S8 · test** — TEST-1. Unit table for `InheritedExecutableFields`.
      Levels: unit. Depends on: S3
- [ ] **S9 · test** — TEST-2. Ingest integration cases for the server rule.
      Levels: integration. Depends on: S6
- [ ] **S10 · test** — TEST-3. Filesystem resolver cases for drop, overlay fallback, collision re-evaluation, cascade, and drop-report order.
      Levels: unit. Depends on: S7
- [ ] **S11 · test** — TEST-4. HTTP `details` on the reingest response, the error-envelope scope of the `rejected` array, and cross-mode equivalence of the drop.
      Levels: integration, materialization. Depends on: S6, S7
- [ ] **S12 · test** — TEST-5. End-to-end CLI reporting for `podium layer reingest` and `podium sync`.
      Levels: e2e. Depends on: S6, S7
- [ ] **S13 · test** — MV-1. Scenario S91 in `test/manual-validation.md`.
      Levels: manual. Depends on: S6, S7
- [ ] **S14 · docs** — DOC-1. `docs/authoring/extends.md` and the two frontmatter pages.
      Levels: —. Depends on: S1
- [ ] **S15 · docs** — DOC-2. `docs/reference/error-codes.md`, `docs/reference/cli.md`, and `docs/reference/http-api.md`.
      Levels: —. Depends on: S1
- [ ] **S16 · docs** — DOC-3. The CHANGELOG bullet.
      Levels: —. Depends on: S1–S15

**Ordering constraints.** S1 lands first and passes `make coverage-gate` before any later step (D-14). S2 lands before S6 and S7. S3, S4, and S5 are independent of one another. S6 and S7 are independent of each other once their dependencies land. The test steps S8 to S13 and the docs steps S14 and S15 are independent of one another. S16 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

### Inheritance today

Under the §4.6 omitted-field rule, a child that declares `extends:` and omits a frontmatter field, or sets an empty scalar, inherits the parent's value. The rule covers every frontmatter field and names `hook_action` and `server_identifier`. `mcpServers` deep-merges by `name` (§4.6 field-semantics table).

The parent can come from any layer of the tenant. Ingest resolves the pin across every layer and excludes only the child's own row (`pkg/registry/ingest/ingest.go:1469-1515`). Under §4.6 *Hidden parents*, the registry merges the parent server-side and serves the merged frontmatter, and it withholds the parent's canonical ID, version, layer, content hash, and signature. The stored content hash and the ingest signature frame only the child's pre-merge `ARTIFACT.md` (§4.7.6, §4.7.9). The §4.7.10 delivery signature covers the merged `ARTIFACT.md`, so the inherited bytes are registry-signed, but it signs them under the child's identity and excludes the contributing layer. A shell snippet or a server command written in one layer can therefore run under another layer's artifact name. Nothing served or signed attributes it to its author, and the child's author never restated it.

### The code that carries it

The shared merge implements this inheritance in every deployment mode. `MergeExtends` starts from the parent (`pkg/manifest/merge.go:29`) and keeps the parent's `HookAction` and `ServerIdentifier` when the child's is empty (`:104-109`). `mergeMCPServers` keeps every parent-only server (`:278-281`). For a name both sides declare, `mergeMCPServer` fills each of `Transport`, `Command`, and `Args` the child leaves empty, and it unions `Env` key by key (`:295-317`). A child that restates an entry by name alone therefore still runs the parent's command.

The adapters write these values into harness configuration, and the harness runs them; the §6.7 sandbox contract forbids an adapter to spawn a subprocess itself (`spec/06-mcp-server.md:359`). A `hook` artifact's `hook_action` becomes the harness hook command (`pkg/adapter/layout.go:262-267`, `:285-299`). An `mcp-server` artifact's `server_identifier` becomes the harness MCP server command (`pkg/adapter/layout.go:154-162`, `:188-225`; `pkg/adapter/claudecode.go:100-101`). The Claude Code adapter writes an `agent` or `command` manifest, merged frontmatter included, verbatim into `.claude/agents/<n>.md` and `.claude/commands/<n>.md`, and it writes an extension type's manifest verbatim under `.claude/podium/<id>/` (`pkg/adapter/claudecode.go:76-91`, `:102-110`). Every frontmatter key the `Artifact` struct does not declare is inherited whole by `undeclaredKeysOf` (`pkg/manifest/merge_serialize.go:301-326`), so such a key reaches harness-native files unchanged.

### What blocks the decided rule

The maintainer has decided the fix: at ingest, a child whose parent comes from a different layer must restate every executable field that parent sets, or ingest rejects it with `ingest.invalid_artifact` and `details.constraint: "executable_inheritance"`. The filesystem source applies the same rule. Four gaps in today's structure must be closed for the rule to work as stated.

1. **No per-artifact ingest rejection carries `details`.** `RejectedArtifact` has only `ArtifactID`, `Reason`, and `Code` (`pkg/registry/ingest/ingest.go:414-418`). The reingest response serializes only those three as a `map[string]string` (`pkg/registry/server/layers.go:2004-2010`). The filesystem drop report is typed to `[]layer.Collision` and hard-codes `ingest.collision` (`cmd/podium/layer.go:615-619`, `cmd/podium/main.go:577`).
2. **The `extends:` block runs before the idempotency check** (`pkg/registry/ingest/ingest.go:665` against `:767-776`), and every cycle re-walks the whole snapshot (`:520`). A check beside the existing `extends:` rejections would drop an unchanged stored child on every reingest.
3. **A stored parent record holds only its authored bytes** (`pkg/registry/ingest/ingest.go:1241`), and the ingest fold carries four indexed columns (`:1418`). A check that reads the immediate parent's authored keys misses the values that parent inherited, as in the chain A0 (layer L1, sets `hook_action`) to A (L1, omits it) to B (L2).
4. **The filesystem resolver has no per-record drop path.** Every `mergeRecord` error aborts the whole walk (`pkg/registry/filesystem/extends.go:59-63`). The filesystem source keeps no ingest state (`pkg/sync/sync.go:567-583`), so nothing there can be grandfathered.

## Decisions

- **D-1. Executable set.** The executable fields are `hook_action`, `server_identifier`, `mcpServers`, and every undeclared key. An undeclared key is an `ARTIFACT.md` frontmatter key that §4.3 does not define as an `ARTIFACT.md` field (the universal, caller-interpreted, type-specific, and external-resources fields, `spec/04-artifact-model.md:137-252`), other than `source` (§4.4.2), `audit_redact`, and `lint_suppress`. Every extension-type field is an undeclared key, and so is a field §4.3 defines for `SKILL.md` alone (`compatibility`, `metadata`, and `allowed-tools`, `spec/04-artifact-model.md:266-268`) when it appears in `ARTIFACT.md`. The §4.3.4 field-allocation table also marks `name`, `description`, and `license` "Omitted" from a skill's `ARTIFACT.md` (`spec/04-artifact-model.md:263-265`), but they are universal fields (`:139-155`), lint admits them in a skill's `ARTIFACT.md` with a warning (`pkg/lint/skill_rules.go:54-56`, `:89-90`), and the `Artifact` struct declares them (`pkg/manifest/types.go:101`, `:103`, `:107`), so they are defined `ARTIFACT.md` fields and never undeclared keys. The spec text names both groups so that its predicate does not depend on reading the allocation table's "Omitted" column. The keys that are not undeclared are exactly `declaredKeys` (`pkg/manifest/merge_serialize.go:22-35`), which reflects the `Artifact` struct alone: the §4.3 `ARTIFACT.md` keys, `source`, `audit_redact`, and `lint_suppress` (`pkg/manifest/types.go:100-159`; `:115`, `:121`, `:127`). The §4.3.4 `SKILL.md` keys live on the separate `Skill` struct (`pkg/manifest/types.go:218-224`), so `declaredKeys` excludes them and `undeclaredKeysOf` restores them (`pkg/manifest/merge_serialize.go:301-326`). The spec text names them so that the spec predicate and the code predicate are the same set, and CODE-1 keeps `declaredKeys` unchanged. `allowed-tools` matters on its own: the Claude Code adapter writes a `command` or `agent` manifest verbatim into `.claude/commands/<n>.md` or `.claude/agents/<n>.md` (`pkg/adapter/claudecode.go:76-91`), where the key pre-approves tools. `server_identifier` is included because every adapter with an MCP surface derives the launched command or URL from it (`pkg/adapter/layout.go:154-162`, `:188-225`; `docs/authoring/artifact-types.md:221`), so an `mcp-server` child that omits it launches its parent's server. Undeclared keys are included fail-closed (`.claude/rules/code-best-practices.md` Errors), because the Claude Code adapter copies the merged frontmatter of `agent`, `command`, and extension-type manifests verbatim into harness-native files (`pkg/adapter/claudecode.go:76-91`, `:102-110`), and Podium cannot tell whether the harness or the host acts on a key it does not define.
- **D-2. Excluded fields.** `hook_event` selects when a restated action runs and supplies nothing to run. `runtime_requirements` is a gate input: the MCP server compares it with the host's advertised capabilities (§4.4.1; `pkg/materialize/atomic.go:47`), and adapters only describe it (`pkg/adapter/compatibility.go:45`). `sandbox_profile` constrains execution and supplies nothing to run. A child that omits it inherits a constraint no weaker than the low-sensitivity default (§4.4.1), and a tighter inherited profile at worst leads to a refusal. The case where a parent loosens a profile the child declares comes from the single ranking in `sandboxRank` (`pkg/manifest/merge.go:231-252`), which merges a child's `read-only-fs` with a parent's `network-isolated` or `seccomp-strict` into the parent's value. Restatement cannot fix that, because the most-restrictive merge picks the parent's value whatever the child restates, so it is a non-goal (Q-2). `external_resources` supplies pointer records whose bytes the registry neither stores nor serves, and no built-in consumer fetches them (`docs/authoring/bundled-resources.md:77`). Third-party hosts are documented to fetch and verify each entry (`docs/consuming/handling-artifact-responses.md:160`, `:279`), and the bytes run only when an executable field or the child's own bundled content runs them. `requiresApproval`, `delegates_to`, `input`, `output`, and the remaining fields named in §4.6 supply nothing a harness runs.
- **D-3. What the parent sets.** The parent's set fields are its chain-resolved values: the values the parent's own `extends:` chain yields under the shared `MergeExtends` fold, the same fold the read path applies (`pkg/registry/core/core.go:1819-1897`). For undeclared keys, they are the keys `undeclaredKeysOf` restores from the parent chain's authored blocks. A value the parent inherited from its own ancestors counts as set by the parent, which closes the A0 (L1) to A (L1) to B (L2) gap.
- **D-4. Restate.** For `hook_action` and `server_identifier`, the child's own frontmatter sets the key to a non-empty value. For `mcpServers`, for each entry name in the parent's resolved list, the child's own list carries an entry under that name, and that entry sets each of `transport`, `command`, and `args` the parent's entry sets and each `env` key the parent's entry sets. For an undeclared key, the child's own frontmatter sets the key to a value other than an empty scalar, matching `isEmptyValue` (`pkg/manifest/merge_serialize.go:350`). The restated value may equal the parent's or differ from it. Under each of these definitions the merged value of the field equals the value the child authored, so CODE-1 implements the check as that equality over the output of `MergeExtends` and `undeclaredKeysOf`, and reports each missing member.
- **D-5. Cross-layer edge.** The rule compares the child's layer with the layer of the parent version the child pins: `req.LayerID` against the pinned row's `Layer` on the server (`pkg/registry/ingest/ingest.go:666`, `:708`; one row per tenant, ID, and version, `pkg/store/sqlite.go:101`), and `rec.Layer.ID` against `parentRec.Layer.ID` in the filesystem resolver. Only the immediate edge is tested. Inheritance within one layer is unchanged, including a same-ID `extends:` of an earlier version in the same layer (`pkg/registry/ingest/ingest.go:1507`). A §4.6 same-ID overlay crosses layers when its pin resolves to a version a lower-precedence layer contributes, so such an overlay restates each executable field that version sets. On the server, `resolveExtendsPin` excludes only the child's identical row (same ID, version, and layer, `pkg/registry/ingest/ingest.go:1507`), so an earlier version of the overlay stored from the overlay's own layer stays a candidate, and an unpinned reference selects the most recently ingested candidate (`:1567-1581`; `pkg/version/version.go:160`). When the reference selects that earlier same-layer version, the server tests a within-layer edge and accepts the overlay without a restatement. The filesystem resolver always takes the next-lower layer's record for a same-ID overlay (`pkg/registry/filesystem/extends.go:101-108`), so a filesystem sync tests the cross-layer edge and can drop the same overlay. This is an instance of the parent-selection divergence SPEC-3 states, and the alternative of resolving a same-ID pin only among lower-layer rows would change §4.7.6 pin selection and needs its own spec edit. DOC-1 and DOC-3 state the overlay consequence in this scoped form.
- **D-6. Only new ingests.** On the server, the check runs on the insert path: after the collision check and after the idempotency switch classifies the version as not stored (the `errors.Is(err, store.ErrNotFound)` arm, `pkg/registry/ingest/ingest.go:782`), before signing. An unchanged re-ingest of a stored version is therefore classified unchanged (`:767-772`), and a same-version byte change stays an `ingest.immutable_violation` conflict (`:773-780`). The check is evaluated against the pin the new version receives in the same pass, which is the pin stored with it (§4.7.6). A stored version is never re-evaluated when its parent later gains an executable field. A same-layer child of a stored version that inherited a foreign value keeps inheriting it, because its own edge lies within one layer. The spec states this as an accepted consequence, and no per-value layer provenance is added.
- **D-7. Error surface.** The code is the existing `ingest.invalid_artifact`, so §6.10 gains no code and the matrix axis is unchanged (`tools/matrix/matrices.go:103-111`). The per-artifact rejection gains a `details` object, `{"constraint": "executable_inheritance", "fields": [...]}`, carried on `RejectedArtifact.Details` and on the reingest response's `rejected` entry, and present only when the rejecting rule defines it. Field paths use the forms `hook_action`, `server_identifier`, `mcpServers[<name>]` (no entry under that name), `mcpServers[<name>].transport`, `mcpServers[<name>].command`, `mcpServers[<name>].args`, `mcpServers[<name>].env.<KEY>`, and the bare undeclared key. They are sorted byte-wise and de-duplicated. The rejected artifact is named by the entry's existing `artifact_id`. The CLI line format `rejected: <id> (<code>): <reason>` is unchanged, and the reason lists the same fields.
- **D-8. Withholding.** Neither the reason nor `details` names the parent's canonical ID, version, layer, content hash, or signature. Naming `mcpServers` entry names, `env` keys, and undeclared key names is permitted, because §4.6 serves the parent's frontmatter values by design, but only on the terms the read path serves them. §4.6 *Withheld* fails a read whose merged block carries a key or a value standing as a reference to a chain parent (`spec/04-artifact-model.md:685`; `pkg/manifest/merge_serialize.go:132-138`, `:415-437`). A field path built from a parent-authored name skips that test, so a chain B (L2) to A (L1) to A0 whose author wrote an undeclared key or an entry named A0's canonical ID would print A0's ID on B's rejection, while every read of B withholds it. CODE-1 therefore applies the same reference test to every name component it would emit, over the parent set `pinnedParents` builds from the chain's pins and the child's own `extends:` less the child's ID (`pkg/manifest/merge_serialize.go:376-393`). When a component discloses a chain parent, CODE-1 returns `ErrUnreadableChain`, and the caller emits the rejection with `layer.UnreadableExecutableFieldsReason` (CODE-2), which names no field and no parent. Such a child would fail every read anyway, so the rejection loses no servable artifact. The rejection reveals that the parent comes from a layer other than the child's. SPEC-1 amends the §4.6 resolution-disclosure paragraph to cover exactly that, and the rejection names no layer. As `collisionRejection` does (`pkg/registry/ingest/ingest.go:1449-1456`), the server logs the tenant, the child's layer, the pin, and the parent's layer with `log.Printf` so an operator can trace the rejection. The same withholding covers a store fault during the chain walk. `core.ExtendsChain` walks the pinned parent's own chain, so its error can name an ancestor beyond the reference the author wrote (CODE-3 wraps `rec.ExtendsPin` of the record whose parent read failed), and the reingest handler writes any runner error's text into the `registry.unavailable` envelope (`pkg/registry/server/layers.go:1931-1935`, `:2057-2058`). CODE-4 therefore logs the walk error and aborts with a `*chainReadError` whose message names the child alone and which unwraps to the walk error, as core keeps the same walk error in the server log because it can name an ancestor (`pkg/registry/core/core.go:1353-1368`). This matches §4.6 *Withheld*: a failure to resolve the chain reports the artifact the caller requested and not the parent it could not reach (`spec/04-artifact-model.md:685`).
- **D-9. Filesystem source.** `podium sync` applies the rule on every run, because the filesystem source keeps no stored state. A child the rule refuses is dropped and reported with `ingest.invalid_artifact`, and it follows the exit-status, `--dry-run`, `--config`, and `podium sync override` terms §13.11.3 already sets for a collision drop. A dropped artifact takes no part in `extends:` resolution or in the collision rule. After each drop, sync recomposes the layers from the walked records without every artifact dropped under this rule or by the cascade, and re-applies the collision rule to what remains. A fresh server never stores a refused row, and its collision check reads only stored rows of other layers (`pkg/registry/ingest/ingest.go:567-573`, `:751-764`), so a higher-precedence record without `extends:` that collides only with a refused record is accepted there. Re-applying the collision rule keeps that record in sync too and retracts its collision drop, which is never reported. For example, with `P` in L1, `x` in L2 declaring `extends: P` and refused, and `x` in L3 declaring no `extends:`, both modes serve L3's `x` and report only L2's drop. When the dropped child overlays its own canonical ID and a lower-precedence record under that ID remains, that record resolves references to the ID, and the rule applies afresh to each reference that crosses a layer to it. When a reference no longer resolves because of a drop under this rule, the referencing artifact is dropped with `ingest.invalid_artifact`, and the drop repeats until no further artifact is dropped. A fresh server rejects such a descendant with the same code because its pin does not resolve (`pkg/registry/ingest/ingest.go:666-674`), so §11 equivalence holds for the code. Every other `extends.unresolved` and `extends.type_mismatch` cause keeps failing the walk.
- **D-10. No new surface.** The change adds no environment variable, flag, `registry.yaml` key, opt-out, §6.10 code, matrix cell, meta-tool, or SPI method. The rule is spec-fixed, so the §13.12 operator-tunable requirement does not apply. Pre-1.0 policy applies: no grandfathering flag, no migration report, and no dual code path. The `[]layer.Collision` drop list is replaced by a general `layer.Drop` everywhere it flows, and every caller is updated.
- **D-11. TypeProvider.** The `TypeProvider` SPI stays unchanged (`pkg/typeprovider/typeprovider.go:39-43`). D-1's fail-closed treatment of undeclared keys covers extension-type fields through the existing `declaredKeys` and `undeclaredKeysOf` seam. A later proposal that implements the §9.1 field-merge-semantics responsibility can let a provider declare a field non-executable. Until then, every extension-type field is executable for this rule.
- **D-12. Shared implementation (§2.2).** The field comparison is one pure function in `pkg/manifest`. The rejection text, constraint, and code constants live in `pkg/layer` beside `Collision`. Server ingest and the filesystem resolver both call them, so wherever the two modes select the same parent record, they reject the same children with the same code and give byte-identical reasons for the same CODE-1 outcome: `ExecutableInheritance.Reason()` when CODE-1 returns fields, and `layer.UnreadableExecutableFieldsReason` when it returns an error. The server's store-side arms for a missing stored ancestor and a stored cycle keep the reason `extends: parent chain does not resolve`, because the filesystem source has no stored chain. Server ingest reuses core's chain walk through an exported store-level helper and does not copy it.
- **D-13. Signature statement.** The spec text does not call inherited executable bytes unsigned. It states that the §4.7.10 delivery signature covers the merged document under the child's identity, and that under this rule no executable value in that document reaches it across the edge to a parent in another layer unless the child authored it.
- **D-14. Ordering and release.** All spec edits (SPEC-1 to SPEC-3) land in one commit, verified with `make coverage-gate`, before any code. TEST-6 follows, because its fixture updates pass before and after the rule. The code steps follow in the order CODE-1, CODE-2, CODE-3, then CODE-4 with CODE-5, then CODE-6 with CODE-7, then tests, docs, and the CHANGELOG last, all in one pull request. The change is backward-incompatible and is recorded under `[Unreleased]` `### Changed`. Release numbering is settled at release prep.

## Spec amendment: §4.6 Layers and Visibility, executable fields across layers

**SPEC-1.** `spec/04-artifact-model.md`, §4.6. Four edits, all in one commit with SPEC-2 and SPEC-3 (D-14).

**Rationale.** No spec surface limits cross-layer inheritance of executable fields. The omitted-field rule grants unconditional inheritance of `hook_action`, `server_identifier`, and undeclared keys, and the code follows it. The decided rule needs a normative home next to the rule it limits, with precise definitions of "executable", "sets", and "restate" (D-1 to D-5), the stored-version terms (D-6), and the withholding terms (D-8). D-1 records why the undeclared-key definition equals the set the code leaves out of `declaredKeys`. The details paragraph applies the *Withheld* reference test to the field paths, because those paths are built from parent-authored names (D-8). The resolution-disclosure paragraph currently states that the disclosure does not extend to the layer that contributes the parent, and the rejection reveals whether that layer is the author's own, so the paragraph is amended rather than cited. The *Served* paragraph promises unconditional inheritance of every key, so it gains a pointer to the rule.

**Edit 1, omitted fields.** Anchor: the paragraph that begins "**Omitted fields.**", directly above "Extension types register their own field semantics via `TypeProvider`." Replace its second sentence.

Current text:

> This holds for every frontmatter field, including the fields in the table above.

Replacement:

> This holds for every frontmatter field, including the fields in the table above, subject to the executable-field rule below when the parent comes from a layer other than the child's.

The rest of the paragraph is unchanged.

**Edit 2, new paragraphs after the TypeProvider sentence.** Anchor: directly after the line "Extension types register their own field semantics via `TypeProvider`." and before the heading `## 4.7 Registry as a Service`. Insert:

> **Executable fields across layers.** The executable fields are `hook_action`, `server_identifier`, `mcpServers`, and every undeclared key. An undeclared key is an `ARTIFACT.md` frontmatter key that §4.3 does not define as an `ARTIFACT.md` field (a universal, caller-interpreted, type-specific, or external-resources field), other than `source` (§4.4.2), `audit_redact`, and `lint_suppress`. Every extension-type field is an undeclared key, and so is a field that §4.3 defines for `SKILL.md` alone (`compatibility`, `metadata`, and `allowed-tools`) when it appears in `ARTIFACT.md`. The universal fields that §4.3.4 places in a skill's `SKILL.md` (`name`, `description`, and `license`) are `ARTIFACT.md` fields, so they are not undeclared keys when they appear in a skill's `ARTIFACT.md`. A harness adapter writes `hook_action` into the harness's hook configuration as the command the harness runs, and derives from `server_identifier` the MCP server command or URL the harness launches (§6.7); a harness launches each `mcpServers` entry; and an adapter can write an undeclared key verbatim into a harness-native file, where the registry cannot determine whether the harness or the host acts on it. An extension-type field is an executable field whatever its `TypeProvider` declares (§9.1).
>
> When the parent version a child pins (§4.7.6) comes from a layer other than the child's, ingest rejects the child with `ingest.invalid_artifact` (§6.10) unless the child restates every executable field the parent sets. The parent sets a field when the field carries a value in the parent's resolved manifest, which is the result of this section's merge over the parent's own `extends:` chain, so a value the parent inherited from its ancestors counts as one it sets. The child restates:
>
> - `hook_action` or `server_identifier` when its own frontmatter sets the key to a non-empty value;
> - `mcpServers` when, for each entry name in the parent's resolved list, its own list carries an entry under that name, and that entry sets each of `transport`, `command`, and `args` that the parent's entry sets and each `env` key that the parent's entry sets;
> - an undeclared key when its own frontmatter sets the key to a value other than an empty scalar.
>
> The restated value may equal the parent's or differ from it. Under each of these conditions the merged value of the field is the value the child authored. A child cannot remove an `mcpServers` entry, an `env` key, or an undeclared key that the parent sets, so it restates each one. The remaining fields are outside the rule. `hook_event` selects when a restated action runs and supplies nothing to run. `runtime_requirements` is an input to the gate that compares it with the host's capabilities (§4.4.1). `sandbox_profile` constrains execution and supplies nothing to run. `external_resources` supplies pointer records whose bytes the registry neither stores nor serves (§4.3). `requiresApproval`, `delegates_to`, `input`, `output`, and the other fields this section names supply nothing a harness runs.
>
> The rule compares the child's layer with the layer that contributes the pinned parent version, and it tests that edge alone. Inheritance between a child and a parent in the same layer is unchanged. A same-ID overlay whose pin resolves to a version that a lower-precedence layer contributes crosses layers, so it restates each executable field that version sets. When the reference selects an earlier version of the overlay stored from the overlay's own layer, the edge lies within that layer.
>
> The rejection carries `details.constraint: "executable_inheritance"` and `details.fields`, which lists each field the child must restate, sorted byte-wise without duplicates. A field is written as `hook_action` or `server_identifier`; as `mcpServers[<name>]` for a parent entry the child's list does not carry; as `mcpServers[<name>].transport`, `mcpServers[<name>].command`, `mcpServers[<name>].args`, or `mcpServers[<name>].env.<KEY>` for a member the child's entry does not set; and as the bare key name for an undeclared key. The reason lists the same fields. Neither the reason nor `details` names the parent's canonical ID, version, layer, content hash, or signature. When the registry cannot read the parent's chain, because a stored ancestor is missing or the stored chain returns to a version it already walked, it rejects the child with `ingest.invalid_artifact` and a reason that names no parent. The registry rejects the child the same way, naming no field, when an `mcpServers` entry name, an `env` key, or an undeclared key that a field path would carry stands as a reference to a member of the parent's chain under the test the *Withheld* paragraph applies, because such a path would surface an ID that every read of the child withholds.
>
> The rule applies to the version being ingested and never invalidates a version already stored: a stored version keeps its pin and keeps serving, and a cycle that presents it with unchanged content classifies it unchanged (§7.3.1). A child in the same layer as such a stored version keeps inheriting the executable values that version inherited from another layer, because the child's own edge lies within one layer. The §4.7.10 delivery signature covers the merged document under the child's identity. Under this rule, no executable value in that document reaches it across the edge to a parent in another layer unless the child authored it. A filesystem-source `podium sync` applies the rule on every run (§13.11.3).

**Edit 3, the *Served* paragraph of Hidden parents.** Anchor: the paragraph that begins "*Served.* The parent's own frontmatter values reach the requester through the merge, by design." Insert one sentence directly after the sentence that ends "and the list-valued fields carry the parent's entries alongside the child's."

> The executable-field rule below limits which executable keys a child may leave to a parent in another layer.

**Edit 4, resolution disclosure.** Anchor: the paragraph that begins "Resolution of an `extends:` reference crosses the visibility boundary by construction". Replace its last sentence.

Current text:

> The disclosure is scoped to the reference the author wrote and does not extend to the layer that contributes the parent.

Replacement:

> The disclosure is scoped to the reference the author wrote. It extends to the layer that contributes the parent only as far as the executable-field rule below reveals whether that layer is the author's own, and it never names that layer.

## Spec amendment: §7.3.1 Authoring and Ingestion, ingest outcome

**SPEC-2.** `spec/07-external-integration.md`, §7.3.1, the paragraph that begins "**Ingest outcome.**". Two edits.

**Rationale.** §7.3.1 lists every ingest drop and is the only home for the reingest response's per-artifact entries outside the code. The new drop must be listed, the `details` object SPEC-1 promises needs a wire statement, and §13.11.3 (SPEC-3) cites the code an unresolved `extends:` pin carries, which no section states today. §4.6 owns the `details` members, so this section states only that the object appears.

**Edit 1, drop list.**

Current text:

> a cross-layer collision (`ingest.collision`) and an `extends:` chain that crosses types (§4.6), an unresolved `extends` pin, a manifest record the cycle cannot build,

Replacement:

> a cross-layer collision (`ingest.collision`) and an `extends:` chain that crosses types (§4.6), a child that does not restate an executable field its parent in another layer sets (`ingest.invalid_artifact`, §4.6), an unresolved `extends` pin (`ingest.invalid_artifact`), a manifest record the cycle cannot build,

**Edit 2, wire sentence.** Anchor: directly after the sentence "A non-blocking advisory (§3.3) and an artifact whose embedding call failed (§4.7) are not drops: the artifact is stored and served." and before "`podium layer reingest <id>` exits non-zero". Insert:

> When the registry answers a completed cycle without a §6.10 error envelope, the `POST /v1/layers/reingest` response reports each artifact the cycle rejected for a reason other than a lint failure or a same-version content change with its identifier, its §6.10 code, and its reason, and with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field rule does. A cycle that classifies no artifact as accepted or unchanged is answered with the `ingest.immutable_violation` envelope when it has a same-version content change, and with the `ingest.lint_failed` envelope when it has a lint failure and no such change. Neither envelope reports the cycle's other rejections or their `details`.

The CLI sentences that follow are unchanged. The existing sentence "A cycle the registry answers with a §6.10 error reports that error envelope instead and exits non-zero" already covers the CLI side of the two envelope cases.

The envelope conditions are the handler's and the pipeline's today: the reingest handler writes the `ingest.immutable_violation` 409 envelope, whose details carry only `conflicts`, when `len(res.Conflicts) > 0 && res.Accepted == 0 && res.Idempotent == 0` (`pkg/registry/server/layers.go:1988-1997`), and `Ingest` returns `ErrLintFailed` when `len(res.LintFailures) > 0 && res.Accepted == 0 && res.Idempotent == 0 && len(res.Conflicts) == 0` (`pkg/registry/ingest/ingest.go:922-923`), which `writeReingestError` maps to the `ingest.lint_failed` 422 envelope (`pkg/registry/server/layers.go:2045-2046`). Neither condition reads `res.Rejected`, so a rejection in the same cycle, an executable-inheritance rejection included, appears in neither envelope. The proposal scopes the wire sentence to that behavior and does not carry `rejected` into the envelopes: the omission applies to every rejection kind today, the CLI already exits non-zero on either envelope, and carrying `rejected` into an error envelope would add a wire member to two §6.10 envelopes for a case the author resolves by fixing the lint failure or bumping the version and reingesting, after which the next 200 response names the refused child with its `details`.

## Spec amendment: §13.11.3 What's Available, executable inheritance

**SPEC-3.** `spec/13-deployment.md`, §13.11.3. Insert a new paragraph directly after the paragraph that begins "**Layer collisions.**" (it ends "The workspace overlay is merged after this composition and follows §6.4.") and before "What's **not available** in filesystem source:".

**Rationale.** The filesystem source re-walks and re-resolves on every run (`pkg/sync/sync.go:567-583`), so the server's insert-only scope grandfathers nothing there. Today every resolver error aborts the walk (`pkg/registry/filesystem/extends.go:59-63`). A walk failure would materialize nothing where a fresh server materializes everything except the refused child, which contradicts the equivalence §13.11.3 promises, so the refusal is a drop. The cascade is stated as one general rule so that every reference a drop strands is covered, including a higher same-ID overlay whose only lower record was dropped. The collision rule is re-applied after a drop because the existing collision paragraph promises that the artifact sync keeps is the one a server started fresh on the same directory serves (`spec/13-deployment.md:339`), and a server never stores a refused artifact, so its collision check, which reads only stored rows of other layers (`pkg/registry/ingest/ingest.go:567-573`, `:751-764`), accepts a higher-precedence artifact that collided only with the refused one. The parent-selection sentence records that the filesystem resolver and the server can test the rule on different edges.

Insert:

> **Executable inheritance.** Sync applies the §4.6 executable-field rule to each artifact whose `extends:` parent comes from a layer subdirectory other than its own. The parent is the artifact sync resolves the reference to: the highest-precedence artifact under a different ID, or, for a same-ID overlay, the next-lower-precedence artifact under the same ID. That artifact can differ from the parent version a server pins (§4.7.6), and where it does, the two modes test the rule on different edges. The filesystem source keeps no ingest state, so the rule applies on every run. Sync drops an artifact the rule refuses and names it on standard error with its identifier, the code `ingest.invalid_artifact`, and the reason. After such a drop, sync composes the layers again without the dropped artifact. It re-applies the collision rule to the remaining artifacts, so a higher-precedence artifact that collided only with a dropped artifact is kept, as a server that never stored the dropped artifact keeps it, and sync does not report that collision. It then resolves `extends:` again. A reference to the dropped artifact's ID then resolves to the next-lower-precedence artifact under that ID when one remains, and the rule applies to that reference when it crosses a layer. When no artifact under the ID remains to resolve the reference, sync drops the referencing artifact with `ingest.invalid_artifact`, the code §7.3.1 gives an unresolved `extends` pin. Sync repeats these steps until no further artifact is dropped. Exit status, `--dry-run`, `--config` targets, and `podium sync override` treat these drops as they treat a collision drop.

## Proposed solution

### CODE-1. `pkg/manifest/executable.go`: the shared comparison

New file `pkg/manifest/executable.go`, beside `MergeExtends`, reusing the package-private `declaredKeys`, `undeclaredKeysOf`, `isEmptyValue`, `chainMappings`, `mappingOf`, `pinnedParents`, and `parentIDs.discloses` in `pkg/manifest/merge_serialize.go`.

```go
// ErrUnreadableChain reports that a parent chain block or the child block
// cannot be decoded for the §4.6 executable-field comparison, or that a field
// path the comparison would emit names a chain parent §4.6 withholds.
var ErrUnreadableChain = errors.New("manifest: executable fields: unreadable block")

// InheritedExecutableFields returns the §4.6 executable fields whose merged
// value would not be the value the child authored, as sorted, de-duplicated
// field paths. ...
//
// Spec: §4.6
func InheritedExecutableFields(childID string, parent Artifact, parentChain []MergedBlock, childBlock []byte) ([]string, error)
```

- **Inputs.** `childID` is the child's canonical ID, used only to build the parent set of step (5). `parent` is the parent's chain-resolved `Artifact`. `parentChain` is the parent's chain of authored blocks, root first and parent last. `childBlock` is the child's authored `ARTIFACT.md`. The function parses the child with `ParseArtifact(childBlock)` and decodes `childBlock` with `mappingOf`, so the typed and undeclared halves read the same authored bytes, and a caller cannot pass an already-merged child by mistake.
- **Body.** (1) `merged := MergeExtends(parent, child)`. (2) Append `hook_action` when `merged.HookAction != child.HookAction`, and `server_identifier` likewise. (3) For each entry `m` in `merged.MCPServers`, find the child's entry `c` by name. With none, append `mcpServers[<name>]`. Otherwise append `mcpServers[<name>].transport`, `.command`, or `.args` for each member where `m` differs from `c`, and `mcpServers[<name>].env.<KEY>` for each key in `m.Env` that `c.Env` lacks. (4) Run `undeclaredKeysOf` over `chainMappings(parentChain)` followed by the child's mapping, and append each key whose winning value node is not a node from the child's own mapping. A parent value for which `isEmptyValue` is true sets nothing, and a parent `[]` or `{}` counts as set, which matches what `SerializeMerged` serves. An undeclared key is any key outside `declaredKeys`, which stays unchanged; D-1 records why that set equals the one SPEC-1 defines. (5) Build the parent set with `pinnedParents(childID, pins)`, where `pins` is the `Extends` of every `parentChain` block followed by the child's own `Extends`, which is the set `parentsOf` builds for the read path (`pkg/manifest/merge_serialize.go:376-393`). For each path from steps (3) and (4), test its name components with `parentIDs.discloses`: the `mcpServers` entry name, the `env` key, and the undeclared key. When any component discloses a parent, return `ErrUnreadableChain` and no paths (D-8). (6) Return the paths sorted byte-wise and de-duplicated.
- **Doc comment.** It states that the result is empty exactly when the executable values of `MergeExtends(parent, child)` and of the restored undeclared keys equal the child-authored values, and that a caller turns any returned error into a per-artifact `ingest.invalid_artifact` rejection or drop with `layer.UnreadableExecutableFieldsReason` and does not abort the run.
- **Errors.** Every decode failure returns `fmt.Errorf("manifest: executable fields: %w", ErrUnreadableChain)` with the cause in the message. A disclosing name component returns `fmt.Errorf("manifest: executable fields: field path names a chain parent: %w", ErrUnreadableChain)`, whose message carries neither the component nor any parent ID, because the caller can log it. It does not wrap `ErrUnhidableParent`, so `errors.Is(err, ErrUnhidableParent)` stays specific to the hidden-parent rule.

The function is pure and uses no clock, randomness, or I/O.

### CODE-2. `pkg/layer/executable.go` and `pkg/layer/collision.go`: shared text, constants, and `Drop`

New file `pkg/layer/executable.go`:

```go
// InvalidArtifactCode is the §6.10 code ingest returns for an artifact it
// cannot accept as authored, and the code a filesystem-source composition
// reports for an artifact it drops under the §4.6 executable-field rule or
// because its extends: reference no longer resolves.
//
// Spec: §4.6, §13.11.3
const InvalidArtifactCode = "ingest.invalid_artifact"

// ExecutableInheritanceConstraint is the details.constraint value of a §4.6
// executable-field rejection.
const ExecutableInheritanceConstraint = "executable_inheritance"

// ExecutableInheritance describes a child that leaves executable fields to a
// parent in another layer.
//
// Spec: §4.6
type ExecutableInheritance struct {
	ArtifactID string
	Fields     []string // sorted, de-duplicated field paths
}

func (e *ExecutableInheritance) Reason() string
func (e *ExecutableInheritance) Error() string // returns Reason()
func (e *ExecutableInheritance) Details() map[string]any

// UnreadableExecutableFieldsReason is the reason both modes give a child
// whose §4.6 executable-field comparison returns an error, including a field
// path that would name a chain parent. It names no field and no parent.
//
// Spec: §4.6
const UnreadableExecutableFieldsReason = "extends: executable fields could not be read"

// UnresolvedParentReason is the reason both modes give an artifact whose
// extends: reference names an ID no stored or kept record provides.
func UnresolvedParentReason(parentID string) string
```

- `Reason()` returns exactly `extends: a parent in another layer sets executable fields this artifact does not restate: <f1>, <f2>; restate each one in this artifact`, with the fields joined by `", "`. It names neither the parent's ID nor any layer. For a same-ID overlay the only ID in the text is the child's own.
- `Details()` returns `map[string]any{"constraint": ExecutableInheritanceConstraint, "fields": e.Fields}`. Only the server path emits it. It stays on the shared type so that one type describes the rejection, and the filesystem path uses the code and the reason.
- `UnresolvedParentReason(id)` returns `extends: no parent artifact "<id>" ingested yet`. In `pkg/registry/ingest/ingest.go`, `resolveExtendsPin` builds its `no parent artifact %q ingested yet` error from the same text (strip the `extends: ` prefix, or have the helper return the suffix and both callers prefix it). The server's reason for that case stays byte-identical to today's.

In `pkg/layer/collision.go`, add:

```go
// Drop is one artifact a filesystem-source composition dropped, with the
// §6.10 code and reason it reports.
//
// Spec: §4.6, §13.11.3
type Drop struct {
	ArtifactID string
	Code       string
	Reason     string
}

// Drop returns the drop record for c.
func (c Collision) Drop() Drop // Drop{c.ArtifactID, CollisionCode, c.Reason()}
```

In `pkg/registry/ingest/ingest.go`, define `ErrInvalidArtifact = errors.New(layer.InvalidArtifactCode)` and replace the literal `Code: "ingest.invalid_artifact"` values (about `:619`, `:671`, `:682`, and `:726`) with `layer.InvalidArtifactCode` in the same commit, following how `layer.CollisionCode` is used.

### CODE-3. `pkg/registry/core/core.go`: export the chain walk and the fold

- **`ExtendsChain`.** `func ExtendsChain(ctx context.Context, st store.Store, tenantID string, rec store.ManifestRecord) ([]store.ManifestRecord, error)` extracts `(*Registry).resolveExtendsChain` and keeps its seen-map cycle guard internally. It wraps a parent read failure with both sentinels: `fmt.Errorf("%w: parent %s: %w", ErrNotFound, rec.ExtendsPin, err)` (`go.mod` pins Go 1.26.3, which accepts multiple `%w`). A cycle stays `fmt.Errorf("%w: extends cycle at %s", ErrInvalidArgument, key)`. `(*Registry).resolveExtendsChain` becomes a call to `ExtendsChain(ctx, r.store, r.TenantFor(ctx), rec)`, so the callers at `pkg/registry/core/admit.go:356`, `core.go:1365`, and `core.go:2092` still match `core.ErrNotFound` through `sentinelOf`, their log lines, and `mergedDocumentKeyMatches`.
- **`ResolvedArtifact`.** `func ResolvedArtifact(chain []store.ManifestRecord) (manifest.Artifact, []manifest.MergedBlock)` folds `parsedArtifact` over the chain with `manifest.MergeExtends` and returns the authored blocks as `MergedBlock{Frontmatter, Extends: ExtendsPin}`, root first. `mergeChain` calls it for both the merged `Artifact` and the blocks. The identity, body, resources, and `SkillRaw` assignments stay in `mergeChain` after the fold, and their outcome is unchanged because they are last-wins over the chain. The doc comment states that `ResolvedArtifact` inherits `parsedArtifact`'s stored-column fallback for a block that does not parse, so CODE-1 is the place that fails closed on such a block.
- **Doc comments.** Both exports carry `// Spec: §4.6`. `ExtendsChain`'s comment states the error contract: a missing parent matches `core.ErrNotFound` and `store.ErrNotFound`, any other store failure matches `core.ErrNotFound` and the store's own error and not `store.ErrNotFound`, and a cycle matches `core.ErrInvalidArgument`.
- **Unit test in this step.** Add `TestExtendsChain_ErrorContract` to `pkg/registry/core` with `// Spec: §4.6`. A fake store that returns a non-NotFound error for a grandparent yields an error that matches `core.ErrNotFound` and the injected error and does not match `store.ErrNotFound`. A missing grandparent matches both `core.ErrNotFound` and `store.ErrNotFound`. A two-row stored cycle matches `core.ErrInvalidArgument`.

### CODE-4. `pkg/registry/ingest/ingest.go`: the check on the insert path

- **`Details`.** `RejectedArtifact` gains `Details map[string]any`, documented as present only when the rejecting rule defines one (§7.3.1).
- **Hoisted parent row.** The `store.ManifestRecord` the `extends:` block loads at about `:708` is kept in a variable that outlives the block, so the insert path can read its `Layer` without a second read.
- **Placement.** In the `case errors.Is(err, store.ErrNotFound):` arm of the idempotency switch (about `:782`), before the `req.Signer` block, and only when `mr.ExtendsPin != ""`, call:

  ```go
  func executableInheritance(ctx context.Context, st store.Store, tenantID, childLayer string,
      rec filesystem.ArtifactRecord, mr store.ManifestRecord, parent store.ManifestRecord) (*RejectedArtifact, error)
  ```

  It returns nil, nil when `parent.Layer == childLayer`. Otherwise it calls `core.ExtendsChain(ctx, st, tenantID, parent)` and branches:
  - `errors.Is(err, store.ErrNotFound)` or `errors.Is(err, core.ErrInvalidArgument)`: a rejection with `Code: layer.InvalidArtifactCode` and `Reason: "extends: parent chain does not resolve"`, which names no parent. These store-side arms are the only users of this reason; the filesystem source has no stored chain.
  - Any other error: `executableInheritance` logs it with `log.Printf("ingest: tenant=%s layer=%s artifact=%s extends_pin=%s: resolve extends chain: %v", ...)` and returns `&chainReadError{artifactID: rec.ID, cause: err}`, and `Ingest` aborts by returning that error unwrapped, as the parent-load abort at about `:711-713` aborts. The walk error can name an ancestor beyond the reference the author wrote, and the reingest handler writes a runner error's text into the `registry.unavailable` envelope (`pkg/registry/server/layers.go:1931-1935`, `:2057-2058`), so the returned message names the child alone (D-8).
  - Success: `p, blocks := core.ResolvedArtifact(chain)`, then `manifest.InheritedExecutableFields(rec.ID, p, blocks, rec.ArtifactBytes)`. An error, including the `ErrUnreadableChain` a disclosing field path returns (CODE-1 step (5)), is a rejection with `Code: layer.InvalidArtifactCode`, `Reason: layer.UnreadableExecutableFieldsReason`, and nil `Details`, the same reason CODE-6 gives (D-12), and the log line names no field path. A non-empty result builds `ie := &layer.ExecutableInheritance{ArtifactID: rec.ID, Fields: fields}` and a rejection with `Code: layer.InvalidArtifactCode`, `Reason: ie.Reason()`, and `Details: ie.Details()`.
- **`chainReadError`.** An unexported type in `pkg/registry/ingest/ingest.go`, beside `invalidArtifactError` (about `:1098-1106`), which it follows:

  ```go
  // chainReadError aborts an ingest cycle when a store read fails while the
  // §4.6 executable-field check walks a cross-layer parent's chain. The cause
  // can name an ancestor beyond the reference the author wrote, so the message
  // names the child alone and the cause stays in the server log and the chain.
  //
  // Spec: §4.6 hidden parents (withheld)
  type chainReadError struct {
      artifactID string
      cause      error
  }

  func (e *chainReadError) Error() string {
      return "ingest: resolve extends chain for " + e.artifactID + ": store read failed"
  }
  func (e *chainReadError) Unwrap() error { return e.cause }
  ```

  Only the abort arm above constructs it, and nothing else sets or reads its fields. `errors.Is` still reaches the store error and `core.ErrNotFound` through `Unwrap`, and neither is a sentinel `writeReingestError` maps, so the handler answers 500 `registry.unavailable` with the child-only message, as it answers a parent-load abort today. `notifyIngestFailure` sends the same `err.Error()` (`pkg/registry/server/layers.go:2072-2079`). Were the abort to wrap the walk error with `%w` in a format string instead, the ancestor's ID and version would reach the envelope, and TEST-2 (i) fails on that text.
- **Log line.** On an executable-inheritance rejection, `log.Printf("ingest: tenant=%s layer=%s artifact=%s extends_pin=%s parent_layer=%s rejected: executable_inheritance fields=%s", ...)`, following `collisionRejection` (about `:1449-1456`). The log is for operators and is never returned to the caller.
- **Flow.** A rejection appends to `res.Rejected` and `continue`s, so the artifact is neither signed nor stored.
- **Fixture obligation.** When this step turns up an existing fixture beyond TEST-6's list that relies on cross-layer inheritance of an executable field, fix it here under TEST-6's constraint.

`rec.ArtifactBytes` is the authored block: server ingest walks one layer without resolving `extends:`.

### CODE-5. `pkg/registry/server/layers.go`: `details` on the wire

At about `:2004-2010`, change `rejected := make([]map[string]string, ...)` to `[]map[string]any`. Build each entry with `artifact_id`, `code`, and `reason`, and set `entry["details"] = rj.Details` only when `len(rj.Details) > 0`. Update the comment above the loop to name the executable-field drop. The `ingest.immutable_violation` 409 branch above the loop (about `:1988-1997`) and the `ingest.lint_failed` 422 mapping in `writeReingestError` (about `:2045-2046`) stay unchanged and carry no `rejected` array, as SPEC-2 edit 2 states. `web/ui` reads `artifact_id`, `code`, and `reason`, and the SDKs do not read `rejected`, so neither changes.

### CODE-6. `pkg/registry/filesystem`: a per-record drop path

The change targets `pkg/registry/filesystem/extends.go` (the new `composeExtends`, `resolveExtends`, `mergeRecord`, `authoredChain`) and `pkg/registry/filesystem/walk.go` (`Walk`, `dedupe`, `WalkOptions`).

- **Rename.** `WalkOptions.OnCollision func(layer.Collision)` becomes `OnDrop func(layer.Drop)`. The guard at about `walk.go:53-54` becomes `filesystem: CollisionPolicyDrop requires OnDrop`.
- **`dedupe` returns its collision drops.** `dedupe(all []ArtifactRecord, opts WalkOptions) (deduped, kept []ArtifactRecord, err error)` (`walk.go:90`) becomes `dedupe(all []ArtifactRecord, opts WalkOptions) (deduped, kept []ArtifactRecord, collisions []layer.Drop, err error)`. Under `CollisionPolicyDrop` it appends `layer.Collision{...}.Drop()` to `collisions` where it calls `opts.OnCollision` today (`walk.go:114-118`), and it calls no callback, so a caller can discard the collisions of a composition it later repeats. Its only caller is `Walk` today, and after this change `Walk` and `composeExtends` call it. Without `ResolveExtends`, `Walk` calls `dedupe` once and passes each collision to `opts.OnDrop` in order before it returns.
- **Typed error.** `mergeRecord` keeps its signature. When the edge from `rec` to `parentRec` crosses layers (`parentRec.Layer.ID != rec.Layer.ID`) and `manifest.InheritedExecutableFields(rec.ID, *parentMerged, authoredChain(parentRec, parentIdx, ...), rec.ArtifactBytes)` returns fields, `mergeRecord` returns an unexported `*refusedRecordError` that records the failing record's ID and layer ID and wraps `*layer.ExecutableInheritance` (its `Unwrap` returns the inner error). A non-nil error from `InheritedExecutableFields` also returns a `*refusedRecordError`, whose drop uses `layer.InvalidArtifactCode` and `layer.UnreadableExecutableFieldsReason`, the same reason CODE-4 gives (D-12). An ancestor's violation bubbles up through its descendants unchanged.
- **Rewrite pass keeps its signature.** `resolveExtends(deduped, all []ArtifactRecord) error` (`pkg/registry/filesystem/extends.go:39`) is unchanged and stays the rewrite pass, which folds and re-serializes each settled record in place. It runs only on the set `composeExtends` settles, so CODE-1 always reads authored bytes in the validation pass.
- **`composeExtends`.** `Walk` with `ResolveExtends` set returns `composeExtends(all, opts)`, a new unexported `func composeExtends(all []ArtifactRecord, opts WalkOptions) ([]ArtifactRecord, error)` in `extends.go`. It settles the composition, reports the drops, then calls `resolveExtends(deduped, kept)` on the settled set and returns `deduped`. Its state is local to one `Walk` call. Each refused or cascaded record yields `layer.Drop{ArtifactID, Code: layer.InvalidArtifactCode, Reason}`, with the reason its `*refusedRecordError` or the cascade bullet gives.

  **IMPLEMENTOR'S CHOICE:** the algorithm that settles the filesystem composition. The kept set and the reported drops equal those obtained by repeatedly re-running `dedupe` and the validation pass (`mergeRecord` on each `deduped` record that declares `extends:`, rewriting no record) with every refused or cascaded record removed, until no new refusal occurs. Only the settled composition's collision drops reach `opts.OnDrop`, in the order the settled `dedupe` returns them, and they are reported before the refusal drops. The refusal drops follow, sorted byte-wise by canonical ID and, for one ID, by layer from lowest to highest precedence, whatever iteration refused each one. The order is specified because `podium sync` prints one `rejected:` line per drop in `OnDrop` order (`cmd/podium/layer.go:615-619`), and a fixpoint that iterates a map of refused IDs would otherwise print a different order on each run. Validation reads authored bytes before any rewrite pass. A `dedupe` error, and any validation error other than a `*refusedRecordError` or a cascade case, fails the walk. TEST-3 (c), (c3), (c4), (c5), and (h) and the TEST-4 (b) subcases pin the result, and TEST-3 (c), (c3), and (c5) pin the report order.
- **Cascade.** In validation, an `extends.unresolved` error whose parent ID, or whose own ID for a same-ID overlay, belongs to a record already refused or cascaded in this walk is a refusal of the referencing record with `layer.InvalidArtifactCode` and `layer.UnresolvedParentReason(parentID)`. Every other resolver error still fails the walk.
- **No `OnDrop`.** With `opts.OnDrop` nil (unset on the `CollisionPolicyHighestWins` and `CollisionPolicyError` paths), a refused record fails the walk with the `*refusedRecordError`, and `errors.As(err, new(*layer.ExecutableInheritance))` succeeds.
- **Doc comment.** **IMPLEMENTOR'S CHOICE:** the wording of `resolveExtends`'s doc comment. It names the record the filesystem resolver selects as the parent (the highest-precedence record for a different-ID parent, and the next-lower record for a same-ID overlay), cites §13.11.3 for the divergence from the version a server pins, and does not enumerate the causes D-5 and SPEC-3 already give.
- **Compile-required test edits in this step.** `pkg/registry/filesystem/walk_test.go`: rename `TestWalk_DropPolicyRequiresOnCollision` to `TestWalk_DropPolicyRequiresOnDrop` with the new expected message, convert `wantDrops` to `[]layer.Drop` with `Code: "ingest.collision"` and `Reason: layer.Collision{ArtifactID: ...}.Reason()`, and replace the `ExistingLayer` assertion with the existing kept-layer assertion (`x.Layer.ID`). In the same file, `TestWalk_DropPolicyDroppedRecordIsNotAParent` (about `:467-478`) declares `drops` as `[]layer.Drop`, passes `OnDrop`, and compares against `[]layer.Drop{{ArtifactID: "x", Code: layer.CollisionCode, Reason: layer.Collision{ArtifactID: "x"}.Reason()}}`. `Drop` carries no layer, so the dropped copy stays pinned by the test's existing assertions that the merged record's tags exclude `l2-tag` and its bytes exclude `from-l2`. `pkg/registry/filesystem/extends_test.go` needs no edit, because `resolveExtends` keeps its signature.
- **Fixture obligation.** As in CODE-4.

### CODE-7. `pkg/sync` and `cmd/podium`: carry `layer.Drop`

1. In `pkg/sync`, replace every `[]layer.Collision` drop list with `[]layer.Drop`: `Result.Dropped`, `resolveRecords`, and `filesystemRecords` in `sync.go`; `RenderResult.Dropped` in `render.go`; `FetchRecords` in `records.go`; and `OverrideResult.Dropped` and the local `dropped` in `override.go`. In `filesystemRecords`, set `OnDrop: func(d layer.Drop) { dropped = append(dropped, d) }`.
2. `cmd/podium/layer.go` `reportDropped` takes `[]layer.Drop` and calls `printRejected(w, d.ArtifactID, d.Code, d.Reason)`. Its callers in `cmd/podium/main.go` and `cmd/podium/interactive.go` pass the field through and need no edit.
3. `cmd/podium/main.go` `errDropped` becomes `errors.New("dropped at least one artifact")`, because each per-artifact `rejected:` line on standard error already carries its code. The comment above it names both the §4.6 collision rule and the executable-field rule.
4. The doc comments that say "dropped under the §4.6 collision rule" also name the executable-field rule: `pkg/sync/sync.go` (about `:169`, `:485`, `:563`), `pkg/sync/render.go` (about `:65`), `pkg/sync/records.go` (about `:86-88`), `pkg/sync/effective_view.go` (about `:45-48`), and `cmd/podium/layer.go` (about `:610-614`).
5. Compile-required test edits in this step: `pkg/sync/sync_collision_test.go` `assertOneCollision` (about `:70-87`) and `test/integration/sync_equivalence_test.go` (about `:342-344`) compare against `layer.Collision{...}.Drop()`, or assert `ArtifactID`, `Code == layer.CollisionCode`, and the reason substrings, and check the dropped layer through the kept record or the lock.

The exit-status logic is unchanged.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| A child in layer L2 pins a parent in L1 that sets `server_identifier`, and the child omits it | Ingest rejects the child with `ingest.invalid_artifact`, `details.fields` `["server_identifier"]`, and a reason naming no parent; the child is not stored | §4.6 "ingest rejects the child with `ingest.invalid_artifact` (§6.10) unless the child restates every executable field the parent sets" (SPEC-1); `docs/authoring/extends.md` (DOC-1), `docs/reference/error-codes.md`, `docs/reference/http-api.md` (DOC-2) |
| Parent and child in the same layer | The child inherits as before | §4.6 "Inheritance between a child and a parent in the same layer is unchanged" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A same-ID overlay whose pin resolves to the lower layer's copy, and that omits an executable field that copy sets | Rejected on the server; dropped by a filesystem sync, which then serves the lower copy | §4.6 "A same-ID overlay whose pin resolves to a version that a lower-precedence layer contributes crosses layers" (SPEC-1); §13.11.3 "resolves to the next-lower-precedence artifact under that ID" (SPEC-3); `docs/authoring/extends.md` (DOC-1), `docs/reference/cli.md` (DOC-2) |
| A same-ID overlay whose unpinned or range reference selects an earlier version of the overlay stored from its own layer, and that omits an executable field the lower layer's copy sets | Accepted on the server and dropped by a filesystem sync; an accepted instance of the parent-selection divergence (D-5) | SPEC-3 (§13.11.3); `docs/authoring/extends.md` (DOC-1), `docs/reference/cli.md` (DOC-2) |
| A0 (L1) sets a field, A (L1) inherits it, B (L2) extends A and omits it | B is rejected and A is accepted | §4.6 "a value the parent inherited from its ancestors counts as one it sets" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A stored version that the rule would refuse, presented again with unchanged bytes | Classified unchanged and served | §4.6 "a cycle that presents it with unchanged content classifies it unchanged (§7.3.1)" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A stored version that the rule would refuse, presented with changed bytes at the same version | `ingest.immutable_violation` conflict | §7.3.1 ingest cases (unchanged); `docs/reference/error-codes.md` (unchanged) |
| A stored child keeps its pin after its parent gains an executable field; the child's next version omits it | The stored version serves unchanged, and the new version is rejected | §4.6 "never invalidates a version already stored" (SPEC-1); §4.7.6 *Inheritance and re-ingest* (unchanged); `docs/authoring/extends.md` (DOC-1) |
| A child in L2 extends a stored version in L2 that inherited a value from L1 before it was stored | Accepted; the child serves the L1-authored value. Accepted consequence (D-6) | §4.6 "A child in the same layer as such a stored version keeps inheriting the executable values that version inherited from another layer" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A stored ancestor of the parent is missing, or the stored chain is a cycle | Rejected with `ingest.invalid_artifact` and the reason `extends: parent chain does not resolve` | §4.6 "rejects the child with `ingest.invalid_artifact` and a reason that names no parent" (SPEC-1); `docs/reference/error-codes.md` (DOC-2) |
| An `mcpServers` entry name, an `env` key, or an undeclared key that a field path would carry stands as a chain parent's ID | Rejected with `ingest.invalid_artifact`, the reason `extends: executable fields could not be read` (`layer.UnreadableExecutableFieldsReason`) in both modes, and no `details`; no field and no parent is named | §4.6 "The registry rejects the child the same way, naming no field" (SPEC-1); `docs/authoring/extends.md` (DOC-1), `docs/reference/error-codes.md` (DOC-2) |
| Reading a stored ancestor fails for a reason other than absence | The reingest call fails with the `registry.unavailable` envelope, as a parent-load failure does today. The message reads `ingest: resolve extends chain for <child>: store read failed` and names no ancestor, and the walk error, which can name one, reaches the server log alone (D-8, CODE-4) | §7.3.1 "A cycle the registry answers with a §6.10 error reports that error envelope instead and exits non-zero" (unchanged); §4.6 *Withheld* "A failure to resolve the chain reports the artifact the caller requested and not the parent it could not reach" (unchanged); `docs/reference/cli.md` (unchanged) |
| The child sets `mcpServers: []` | Every parent entry is still reported as `mcpServers[<name>]` | §4.6 "A child cannot remove an `mcpServers` entry ... so it restates each one" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| The child sets an undeclared key to `[]` or `{}`, or to an empty scalar | `[]` and `{}` count as restated; an empty scalar does not | §4.6 "a value other than an empty scalar" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A parent sets `external_resources`, `runtime_requirements`, `hook_event`, or `sandbox_profile`, and the child omits it | Inherited, no rejection | §4.6 "The remaining fields are outside the rule" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A parent's `seccomp-strict` replaces a child's `read-only-fs` under the single sandbox ranking | Served with the parent's profile. Deferred (Q-2) | §4.6 field-semantics table, `sandbox_profile` "Scalar; most-restrictive" (unchanged); `docs/authoring/extends.md` *Tightening sandbox profile* (DOC-1 adds the executable-field sentence only) |
| An extension type whose field the provider considers inert | The field is executable and must be restated | §4.6 "An extension-type field is an executable field whatever its `TypeProvider` declares (§9.1)" (SPEC-1); `docs/authoring/extends.md` (DOC-1) |
| A `type: hook` child omits `hook_action`, cross-layer, on a server | Lint refuses it first and the reingest reports a lint failure count. A filesystem sync drops it under the rule. The materialized trees match | §7.3.1 lint drop (unchanged); §13.11.3 (SPEC-3); `docs/authoring/extends.md` (DOC-1) |
| A `type: hook` child omits `hook_action` within one layer | The server lint refuses it and a filesystem sync merges it. Pre-existing divergence, non-goal | §7.3.1 lint drop (unchanged); `docs/authoring/extends.md` (DOC-1 states that the server refuses an authored hook without `hook_action`) |
| A filesystem drop strands a descendant whose ID no other layer contributes | The descendant is dropped with `ingest.invalid_artifact`, repeatedly down the chain | §13.11.3 "sync drops the referencing artifact with `ingest.invalid_artifact`" (SPEC-3); `docs/reference/cli.md`, `docs/reference/error-codes.md` (DOC-2) |
| A higher-precedence record without `extends:` collides only with a child the rule refuses (`P` in L1, `x` in L2 with `extends: P` refused, `x` in L3 with no `extends:`) | Both modes serve L3's `x`. The server never stores L2's `x`, so L3's `x` meets no stored row under its ID. A filesystem sync recomposes without L2's `x`, keeps L3's `x`, and reports only the L2 drop, with no `ingest.collision` line | §13.11.3 "It re-applies the collision rule to the remaining artifacts, so a higher-precedence artifact that collided only with a dropped artifact is kept" (SPEC-3); `docs/reference/cli.md` (DOC-2) |
| A filesystem cascade on a same-ID overlay whose only lower record was dropped | Code `ingest.invalid_artifact` in both modes; the server's reason reads `extends: self-extends cycle: ...` and the filesystem reason is the shared unresolved-parent text. Accepted | §13.11.3 (SPEC-3) states the code; no docs page states reason text |
| The filesystem resolver and the server select different parent records | The two modes can test different edges. Accepted (D-5) | SPEC-3 (§13.11.3); `docs/reference/cli.md` (DOC-2) |
| A filesystem sync under `--dry-run`, `--config`, or `podium sync override` drops a child under the rule | Exit 1 and a failed target as for a collision drop; override `--dry-run` reports nothing | §13.11.3 "treat these drops as they treat a collision drop" (SPEC-3); `docs/reference/cli.md` (DOC-2) |
| A cycle that accepts nothing and classifies nothing unchanged holds a refused child beside a same-version content change, or beside a lint failure and no such change | The response is the `409 ingest.immutable_violation` or the `422 ingest.lint_failed` error envelope, which names neither the refused child nor its `details`; the child is not stored, and the CLI prints the envelope and exits non-zero. Pre-existing for every rejection kind, accepted | §7.3.1 "Neither envelope reports the cycle's other rejections or their `details`" (SPEC-2); `docs/reference/http-api.md` (DOC-2) |
| Another rejection on the reingest response | The entry carries no `details` member | §7.3.1 "with a `details` object when the rule that rejected it defines one" (SPEC-2); `docs/reference/http-api.md` (DOC-2) |
| `podium lint` against a child that would be refused | Reports nothing for the rule. Non-goal | `docs/authoring/extends.md` Lint behavior (DOC-1 states that ingest and `podium sync` evaluate the rule and `podium lint` does not) |
| The web UI and the SDKs read a rejected entry | They ignore `details` | §7.3.1 (SPEC-2); `docs/reference/http-api.md` (DOC-2) |

## Testing

**New and extended tests.**

- **TEST-1, `pkg/manifest/executable_test.go`** (unit, `// Spec: §4.6 executable fields across layers`). A table-driven test for `InheritedExecutableFields`:
  - For each of `hook_action` and `server_identifier`, a parent that sets the field with a child that omits it gives the field, a child that sets an empty scalar gives the field, and a child that restates the same value or another value gives nothing.
  - `mcpServers`: the parent entry `jira` sets `transport`, `command`, `args`, and `env: {TOKEN_FILE: x}`; a child without `jira` gives `mcpServers[jira]`; child with `{name: jira}` gives `.args`, `.command`, `.env.TOKEN_FILE`, and `.transport`; child with every member and `env: {OTHER: y}` gives `mcpServers[jira].env.TOKEN_FILE`; a parent entry that sets `transport` alone with a name-only child gives `mcpServers[jira].transport`; a parent entry that sets only `name` with a name-only child gives nothing.
  - In one table: the child sets `mcpServers: []`, which gives `mcpServers[<name>]` for every parent entry, next to an undeclared key set to `[]` in the child, which gives nothing.
  - Undeclared keys: with parent `acme_hooks: {pre: x}`, a child that omits it gives `acme_hooks`, and a child that sets it to an empty scalar gives `acme_hooks`.
  - Chain-resolved undeclared key: `parentChain` is a grandparent block `acme_hooks: {pre: x}` and an intermediate block that omits it, and the child omits it, which gives `acme_hooks`. A variant where the intermediate sets `acme_hooks:` (an empty scalar) gives the same.
  - A parent chain whose only value for an undeclared key is an empty scalar gives nothing.
  - Excluded fields: `hook_event`, `runtime_requirements`, `sandbox_profile`, `external_resources`, `audit_redact`, `lint_suppress`, and `source` set on the parent and omitted by the child give nothing.
  - §4.3.4 `SKILL.md` keys in `ARTIFACT.md`: a `command` parent whose `ARTIFACT.md` sets `allowed-tools: [Bash]`, with a `command` child that omits it, gives `["allowed-tools"]`, and a child that restates it gives nothing. The case comment cites D-1. In the same table, a `skill` parent whose `ARTIFACT.md` sets `name`, `description`, and `license`, with a `skill` child whose `ARTIFACT.md` omits all three, gives nothing. The case comment cites D-1.
  - Disclosing field path: with `childID` `team/b`, a `parentChain` of `core/base` (root) and `shared/a` (`extends: core/base`), and a child with `extends: shared/a`, each of these returns an error that matches `ErrUnreadableChain` and no paths: an ancestor undeclared key named `core/base`, an ancestor `mcpServers` entry named `core/base@1.x`, and an `env` key named `shared/a` on a parent entry `jira` that the child carries by name without that key. The error message contains neither `core/base` nor `shared/a`. A same-ID variant, where `childID` and the parent ID are both `shared/a` and the parent sets an undeclared key named `shared/a`, gives `["shared/a"]` and no error, because `pinnedParents` drops the child's own ID.
  - A case with several missing fields checks sorting and de-duplication.
  - Errors: a parent chain block that does not parse, a child block that does not parse, and a child block that does not decode to a mapping each return an error that matches `ErrUnreadableChain`, whose message starts `manifest: executable fields:`, and that does not match `ErrUnhidableParent`.
  - Property case: over the table's inputs, the result is empty exactly when the executable values of `MergeExtends(parent, child)` and of the restored undeclared keys equal the child-authored ones.
- **CODE-3, `TestExtendsChain_ErrorContract`** (unit, `// Spec: §4.6`). It covers the three error arms CODE-3 lists.
- **TEST-2, `pkg/registry/ingest/executable_inheritance_test.go`** (integration, package `ingest_test`, `// Spec: §4.6`, plus `// Spec: §7.3.1` on the stored-version cases). Reuse `newStore`, `ingestOne`, and `parentLoadStore`. No case uses a `hook` example.
  - (a) An `mcp-server` parent P in L1 sets `server_identifier`, and a child C in L2 with `extends: P` omits it. Ingest returns one rejection with `Code` `ingest.invalid_artifact`, `Details` deep-equal to `{"constraint": "executable_inheritance", "fields": []string{"server_identifier"}}`, and a reason that contains neither P's ID, `L1`, nor P's version, and it does not store C. In the same file, a type-mismatch rejection has nil `Details`.
  - (b) As (a), with C restating `server_identifier` to the same value, and again to another value: both variants are accepted.
  - (c) With P and C both in L1, C is accepted, and the merged load serves P's `server_identifier`.
  - (d) A0 (L1, sets `server_identifier`), A (L1, `extends: A0`, omits it), B (L2, `extends: A`, omits it): A is accepted, and B is rejected with `["server_identifier"]`.
  - (e) Same-ID overlay: `x` 1.0.0 in L1 is an `agent` with `acme_hook: run.sh`; `x` 1.1.0 in L2 with `extends: x` omits it. Version 1.1.0 is rejected with `["acme_hook"]`, and loading `x` serves 1.0.0.
  - (f) Undeclared key: parent `acme_hooks: {pre: x}`; child with `acme_hooks: ""` is rejected; child with `acme_hooks: []` is accepted.
  - (g) A violating child stored directly with `PutManifest` (the precedent at `extends_conformance_test.go:487`), then reingested with the same bytes, yields `Idempotent` 1 and no rejection. The same child at the same version with changed bytes yields one `Conflicts` entry and no rejection.
  - (h) C@1.0.0 in L2 is accepted while P@1.0.0 in L1 sets no executable field. P@1.1.0 in L1 then sets `server_identifier`. Reingesting L2 unchanged classifies C@1.0.0 unchanged. C@1.1.0 with `extends: P` and no `server_identifier` is rejected.
  - (i) A0 (L1) to A (L1) to B (L2), with the store wrapped by `parentLoadStore{parentID: A0, err: boom}`, so the failure falls inside `core.ExtendsChain` and not on the immediate-parent load. `Ingest` returns an error matching `boom`, and B is not stored. The error's text equals `ingest: resolve extends chain for <B>: store read failed` and contains neither A0's ID nor A0's version, which pins the `chainReadError` message (D-8). The test comment states that the match needs CODE-3's two-`%w` wrap and `chainReadError.Unwrap`. The HTTP envelope needs no separate case, because `writeReingestError` writes `err.Error()` unchanged (`pkg/registry/server/layers.go:2057-2058`).
  - (j) The same chain with `parentLoadStore{parentID: A0, err: store.ErrNotFound}`: B is rejected with `ingest.invalid_artifact` and the reason `extends: parent chain does not resolve`, which names neither A0, A, nor L1, and `Ingest` returns no error.
  - (k) A stored chain cycle (two rows whose pins name each other, written with `PutManifest`) as the parent of a new L2 child: the child is rejected with the same reason, and `Ingest` returns no error.
  - (l) An `agent` child in L2 that restates a parent's `mcpServers` entry by name alone, where the parent entry sets only `command`: the child is rejected with `["mcpServers[jira].command"]`.
  - (m) A0 (L1) sets an undeclared key named with A0's canonical ID and carries an `mcpServers` entry with that same name, A (L1) extends A0 and omits both, and B (L2) extends A and omits both. B is rejected with `ingest.invalid_artifact`, the reason `layer.UnreadableExecutableFieldsReason`, and nil `Details`, and neither the reason nor the marshalled rejection contains A0's ID. `Ingest` returns no error. The case comment cites D-8 and D-12; TEST-3 (h) asserts the same constant on the filesystem side.
  - (n) Same-ID overlay whose pin resolves within its own layer, `// Spec: §4.6`, `// Spec: §4.7.6`. `x` 1.0.0 in L1 is an `agent` with `acme_hook: run.sh`. L2 is ingested with `x` 1.1.0 (`extends: x`, restating `acme_hook: team.sh`), which is stored. L2's source then holds `x` 1.2.0 with an unpinned `extends: x` that omits `acme_hook`, and a reingest of L2 accepts it with no rejection, because its pin resolves to `x@1.1.0` in L2. The test asserts the stored pin of 1.2.0 is `x@1.1.0` and that loading `x@1.2.0` serves `acme_hook: team.sh`. The case comment cites D-5 and TEST-3 (b3), which records the filesystem drop on the same layout.
- **TEST-3, `pkg/registry/filesystem/extends_executable_test.go`** (unit, `// Spec: §4.6, §13.11.3`, plus `§11` where a case claims equivalence). Walk with `CollisionPolicyDrop`, `ResolveExtends`, and `OnDrop` collecting drops:
  - (a) Two layers; a cross-layer `hook` child omits `hook_action`. The walk reports exactly one `Drop{Code: "ingest.invalid_artifact", Reason: (&layer.ExecutableInheritance{...}).Reason()}`. The child is absent, the parent is kept, and the reason names neither the parent's layer nor the parent's ID.
  - (b) A same-ID overlay drop: the L1 record serves under the ID, and its `ArtifactBytes` hold none of the dropped overlay's values.
  - (b2) Layers L1, L2, and L3 each hold `x`. L2 omits the field and is dropped. L3 extends `x`: run once with L3 restating the field relative to L1 (kept) and once omitting it (dropped).
  - (b3) The filesystem side of TEST-2 (n). L1 holds `x` 1.0.0, an `agent` with `acme_hook: run.sh`, and L2 holds `x` 1.2.0 with an unpinned `extends: x` that omits `acme_hook`. A filesystem layer holds one record per ID, so the earlier L2 version 1.1.0 that the server pins onto has no counterpart here. The walk reports one `Drop{ArtifactID: "x", Code: "ingest.invalid_artifact"}` whose reason names `acme_hook`, and `x` serves L1's 1.0.0. The case comment states that a server holding a stored `x` 1.1.0 from L2 accepts the same 1.2.0 (D-5), so a later fix to same-ID parent selection shows as a test change.
  - (c) Cascade: C (L2) is dropped, D (L2) extends C, and no other layer contributes C. D is dropped with `ingest.invalid_artifact` and `layer.UnresolvedParentReason("C")`, and the walk succeeds. IDs are chosen so that D sorts before C, which shows the fixpoint does not depend on processing order. The reported slice equals `[]layer.Drop{{ArtifactID: D, Code: "ingest.invalid_artifact", Reason: layer.UnresolvedParentReason("C")}, {ArtifactID: C, Code: "ingest.invalid_artifact", Reason: <C's ExecutableInheritance reason>}}` in that order, which pins CODE-6's byte-wise ID order for refusal drops against an order that follows the iteration that refused each record.
  - (c2) A0 (L1) sets `acme_hook`, A (L1) extends A0 and omits it, B (L2) extends A and omits it. B is dropped with fields `["acme_hook"]`, and A is kept.
  - (c3) C (L2, a different-ID child of P in L1) is dropped, and C (L3) declares `extends: C`. L3's C is dropped with `ingest.invalid_artifact`, and the walk succeeds. The reported slice holds L2's drop, with C's `ExecutableInheritance` reason, before L3's drop, with `layer.UnresolvedParentReason("C")`, which pins CODE-6's lowest-to-highest layer order for refusal drops under one ID.
  - (c4) Retracted collision. L1 holds `P`, an `mcp-server` that sets `server_identifier`. L2 holds `x`, an `mcp-server` with `extends: P` that omits `server_identifier`. L3 holds `x`, an `mcp-server` with no `extends:` and its own `server_identifier`. The walk succeeds and reports exactly one drop, `Drop{ArtifactID: "x", Code: "ingest.invalid_artifact"}` with the executable-inheritance reason naming `server_identifier`, and no `ingest.collision` drop. The returned `x` is L3's record (`x.Layer.ID` is L3). The case comment states that a fresh server never stores L2's `x` and accepts L3's (D-9), and that the first iteration's collision drop of L3's `x` is retracted.
  - (c5) Report order across kinds. L1 holds `P`, an `mcp-server` that sets `server_identifier`, and `z/clash`, an `agent` with no `extends:`. L2 holds `a/child`, an `mcp-server` with `extends: P` that omits `server_identifier`, and `z/clash`, an `agent` with no `extends:`. The walk succeeds, and the reported slice equals `[]layer.Drop{{ArtifactID: "z/clash", Code: "ingest.collision", Reason: layer.Collision{ArtifactID: "z/clash", Layer: "L2", ExistingLayer: "L1"}.Reason()}, {ArtifactID: "a/child", Code: "ingest.invalid_artifact", Reason: <the ExecutableInheritance reason naming server_identifier>}}`. The collision comes first although `a/child` sorts before `z/clash`, which pins CODE-6's collision-before-refusal order, and L2's `z/clash` stays dropped because the record it collides with is not refused. The test walks the same registry a second time and asserts the second slice equals the first.
  - (d) The input from (a) with `OnDrop` nil, under `CollisionPolicyHighestWins` and under `CollisionPolicyError`, with `ResolveExtends`: `Walk` returns an error for which `errors.As(err, new(*layer.ExecutableInheritance))` is true, and no records.
  - (e) A within-layer chain that inherits `hook_action` is unaffected.
  - (f) The divergent-parent fixture: L1 holds `P` 2.0.0; L2 holds an overlay of `P` and a child `C` with `extends: P`, where `C` sorts before `P`. Record the filesystem outcome (the edge tested is L2 to L2, so `C` is kept), with a comment naming the server's different selection, so a later fix to parent selection shows as a test change.
  - (g) Regression guard: under `CollisionPolicyDrop` with `OnDrop` set, a child whose `extends:` names an ID no layer ever contributed still fails the walk with `extends.unresolved` and produces no drop, and an `extends.type_mismatch` still fails the walk.
  - (h) Disclosing field path, `// Spec: §4.6` (D-8). Layer L1 holds `shared/base`, an `agent` with no `extends:` that sets an undeclared key named `shared/base`. Layer L2 holds `team/child`, an `agent` with `extends: shared/base` that omits the key. The walk succeeds and reports exactly one `Drop{ArtifactID: "team/child", Code: "ingest.invalid_artifact", Reason: layer.UnreadableExecutableFieldsReason}`, whose reason does not contain `shared/base`. `shared/base` is kept with its key. Today the same input fails the whole walk with `manifest.ErrUnhidableParent` when the rewrite pass serializes `team/child` (`pkg/registry/filesystem/extends.go:72-75`), so the case pins that the validation pass drops the child before the rewrite pass and that the drop's reason carries no ancestor ID. The key names the immediate parent's own ID on purpose: `pinnedParents` removes a record's own ID from its parent set, so `shared/base` serves its key, while a key named with a grandparent's ID would make the intermediate's own rewrite fail the walk before and after this change.
- **TEST-4** (integration).
  - (a) `test/integration/reingest_collision_reason_test.go`, extended, `// Spec: §4.6`, `// Spec: §7.3.1`. `rcReject` gains `Details map[string]any`. The fixture seeds a hidden-layer `agent` parent `core/mcpparent` with an `mcpServers` entry `jira` (command and args set). Reingest `alice-personal` holding `mine/agent` (extends `core/mcpparent`, omits `jira`) and a `shared/note` 2.0.0 collision. The `mine/agent` entry carries code `ingest.invalid_artifact`, `details.constraint` `executable_inheritance`, and `details.fields` equal to `["mcpServers[jira]"]`, and neither the reason nor the marshalled `details` contains `core/mcpparent`; the body does not contain `core-private-xq7`. The raw JSON of the `shared/note` entry has no `details` key.
  - (a2) Same file, `// Spec: §7.3.1`, pinning SPEC-2 edit 2's envelope scope. `rcReingest` fails the test on any status other than 200 (`test/integration/reingest_collision_reason_test.go:98-100`), so the case uses a variant of it that returns the status and the body. Reingest `alice-personal` holding only `mine/agent` from (a) and a `type: hook` artifact that omits `hook_action`, which `ruleTypeRequiredFields` refuses (`pkg/lint/required_fields.go:45-51`). The response is 422 with code `ingest.lint_failed`, the body has no `rejected` member and does not contain `executable_inheritance`, and `mine/agent` is not stored. The case comment states that the omission is the pre-existing envelope behavior SPEC-2 edit 2 records, so a later change that carries `rejected` into the envelope shows as a test change.
  - (b) `test/integration/extends_equivalence_test.go`, new `TestSyncEquivalence_ExecutableInheritanceDropIsByteIdentical`, modeled on `TestSyncEquivalence_LayerCollisionIsByteIdentical`, `// Spec: §11`, `§2.2`, `§4.6`, `§13.11.3`. Fixture: layer `top` holds an `mcp-server` child that extends an `mcp-server` parent in `base` and omits `server_identifier`, plus one unrelated kept artifact. Run `sync.Run` against the directory and against `server.NewFromFilesystem(dir)` for the `none` and `claude-code` adapters. The trees are byte-identical (`assertTreesEqual`, `assertLockArtifactsEqual`), the child is absent from both, `fsRes.Dropped` equals `[]layer.Drop{{ArtifactID: <child>, Code: "ingest.invalid_artifact", Reason: <the ExecutableInheritance reason>}]`, and `srvRes.Dropped` is empty. A same-ID overlay subcase serves the base copy in both modes. A disclosing-name subcase uses TEST-3 (h)'s fixture: the trees are byte-identical, `team/child` is absent from both, and `fsRes.Dropped` holds one entry for `team/child` with code `ingest.invalid_artifact` and reason `layer.UnreadableExecutableFieldsReason`, which pins the edge-case table's claim that both modes refuse the child with the same code. A retracted-collision subcase uses TEST-3 (c4)'s fixture in a three-layer directory: the trees are byte-identical, both modes materialize L3's `x`, `fsRes.Dropped` holds exactly one entry, for `x` with code `ingest.invalid_artifact`, and no `ingest.collision` entry.
- **TEST-5** (e2e).
  - (a) `test/e2e/artifact_extends_test.go`, new `TestExtends_ExecutableInheritanceRejected`, `// Spec: §4.6`, no `// Matrix:` line. Use `extendsBoot` with the parent and child that `TestExtends_McpServersParentOnlyInherited` used before TEST-6 moved it. Loading `exParentID` returns version 1.0.0, and the server log contains `rejected=1`. `podium layer reingest --registry <url> team-foo` exits 1, its standard error contains `rejected: finance/ap/pay-invoice (ingest.invalid_artifact):` and `mcpServers[jira]`, and it contains neither `org-defaults` nor any parent version.
  - (b) `test/e2e/filesystem_sync_test.go`, a new case, `// Spec: §13.11.3`. A two-layer `multi_layer` registry with a cross-layer `hook` child that omits `hook_action`. `podium sync` prints `rejected: <id> (ingest.invalid_artifact):`, exits 1, and leaves the child out of the target. `podium sync --dry-run` also exits 1.
- **TEST-6, fixture updates ahead of the rule** (unit, integration, e2e). Each update passes before and after the rule. The constraint for every update: the test still pins what its name and its `// Spec:` line say, no assertion is weakened, and the fixture stops relying on cross-layer inheritance of an executable field either by placing parent and child in one layer, when the cross-layer placement is incidental, or by restating the field, when the test pins served values. A same-layer placement, and any within-layer child a fixture adds, gives the parent a canonical ID that sorts byte-wise before the child's whenever a server ingests the layer, because server ingest processes one layer's records in ID order (`pkg/registry/ingest/ingest.go:1024`) and resolves a pin only against rows already stored (`:1519-1521`), so a child that sorts first is rejected with `ingest.invalid_artifact` before and after the rule.
  - `test/e2e/artifact_extends_test.go` `TestExtends_McpServersParentOnlyInherited` (about `:280`): move off `extendsBoot` onto a single-layer `writeRegistry`. Parent `finance/base` (context, `mcpServers` `jira`), child `finance/child` (context, `mcpServers` `slack`, `extends: finance/base@1.x`). Load `finance/child` and keep the assertion that `jira` and `slack` are both served, with `// Spec: §4.6`.
  - `pkg/registry/core/extends_test.go` `TestExtends_MergedFrontmatterServed` (about `:124`): ingest the child into L1. The assertions are unchanged.
  - `test/integration/extends_equivalence_test.go` `writeExtendsRegistry`: `top/team/derived` restates `x_review_board` and `x_charter`, and `top/team/overlay` restates `x_review_board`. Add a within-layer child in `top` that extends a `top` parent setting an `x_*` key and omits it, with the parent's ID sorting before the child's (for example parent `team/alpha` and child `team/beta`), so `TestSyncEquivalence_ExtendsChildMatchesAcrossModes` keeps pinning cross-mode equivalence of inherited undeclared keys. Update its doc comment and inherited-key assertions to match.
  - `pkg/registry/core/extends_merged_frontmatter_test.go`: `emfLoad`, `ingestPair` (defined in `extends_test.go`, about `:440`), and `overlayLoad` ingest parent and child into L1 and L2. Every test whose parent sets an undeclared key or an `mcpServers` entry the child does not restate is updated under the constraint above. **IMPLEMENTOR'S CHOICE:** whether `emfLoad` and `ingestPair` move both rows into one layer or each affected test's child restates its keys, and how `overlayLoad`'s same-ID cases are kept. Each affected test keeps asserting its named read-path behavior on an accepted child, and an overlay case that can only exist cross-layer becomes a same-layer later-version overlay or restates the key.
  - `test/integration/sync_manifest_body_test.go` (about `:28-34`): the large child in `top` restates `x_owner: platform`, so `TestServerSync_LargeMergedManifestMatchesFilesystemSync` keeps its `x_owner: platform` assertion.
  - `test/e2e/audit_manifest_redaction_test.go` `TestAudit_ExtendsInheritedRedaction` (about `:92`): place the child in `org-defaults` beside its parent `shared/payroll-base`, rename the child from `finance/inherited-payroll` to `shared/payroll-inherited` (the `childID` constant, about `:96`), so its ID sorts after the parent's and the single ingest pass stores the parent first, and drop `team-foo` from the fixture's `layer_order`. Rewrite the comment at about `:107-112` to state that the parent's ID sorts before the child's in the one layer. The redaction of an inherited `x_bank_account` stays pinned, because `audit_redact` and `x_bank_account` are inherited within one layer.
  - Audit: run `go test ./...` and `go test -v ./test/e2e/ -run 'Extends|FilesystemSync|Audit'`, grep the output for `SKIP`, and confirm no new failure. `TestExtends_McpServersOverrideByName` (about `:266`) restates `command`, its parent entry's only member, and needs no change. The single-layer fixtures in `TestSyncEquivalence_UnhidableParentFailsBothModes` and `TestSyncEquivalence_InheritedKeyNamingTheParentFailsBothModes` need no change.

**Existing tests to confirm unchanged.** Confirm each of the following passes unchanged: `pkg/registry/ingest/extends_conformance_test.go` (pin resolution, type mismatch, collision and overlay rules, fold, idempotency, and `TestIngest_ExtendsParentLoadFailureAbortsIngest`); `pkg/registry/filesystem/extends_test.go` (single-layer and no-executable-field fixtures, and `resolveExtends` keeps its signature); `test/integration/sync_equivalence_test.go` reference-registry equivalence (the `personal` overlay of `company-glossary` carries no executable field); and the golden files under `test/materialization/testdata/golden/` (no regeneration).

**Commands per step.**

- S1: `make coverage-gate`.
- S2: `go test ./pkg/registry/core/ ./test/integration/` and `go test -v ./test/e2e/ -run 'Extends|Audit'`, with no `SKIP` on the edited tests.
- S3 to S5: `gofmt -l pkg/manifest pkg/layer pkg/registry/core pkg/registry/ingest` prints nothing, then `go test ./pkg/manifest/ ./pkg/layer/ ./pkg/registry/core/ ./pkg/registry/ingest/`.
- S6: `go test ./pkg/registry/ingest/ ./pkg/registry/server/ ./pkg/registry/core/ ./test/integration/`.
- S7: `go build ./...`, `go test ./pkg/registry/filesystem/ ./pkg/sync/ ./cmd/podium/ ./test/integration/`, and `go test -v ./test/e2e/ -run 'FilesystemSync|Sync'`.
- S8 to S12: `go test -coverpkg=./... -coverprofile=cover.out ./pkg/manifest/ ./pkg/layer/ ./pkg/registry/... ./pkg/sync/ ./cmd/podium/ ./test/integration/`, then `go tool cover -func=cover.out`, and confirm each new function reaches 85%. For S12, `GOCOVERDIR=$(mktemp -d) go test -v ./test/e2e/ -run 'ExecutableInheritance|FilesystemSync'`, then `go tool covdata textfmt` and `go tool cover -func` to confirm `reportDropped` and `errDropped` coverage in the binary.
- Closing step: `make lint` and `make coverage-gate`.

## Manual validation

**MV-1.** Append scenario S91 to `test/manual-validation.md`, after S90, in the document's existing conventions. Port 8193 is unused by other scenarios. The example uses `mcpServers`, because server lint refuses a hook child that omits `hook_action` before the rule runs.

````markdown
## S91: A cross-layer extends: child that leaves an executable field to its parent is refused

**Goal.** Validate that a same-ID overlay in a higher layer that does not
restate its parent's `mcpServers` entry is dropped by a filesystem-source
`podium sync` and rejected by a server reingest with the same code, that the
reason names the missing field and neither the parent's layer nor its version,
that the reingest response carries `details`, that both modes materialize the
lower copy, and that restating the entry admits the overlay.

**Covers.** §4.6 executable fields across layers, §7.3.1 ingest outcome,
§13.11.3 executable inheritance, and §11 filesystem-to-server equivalence,
through the compiled binary.

**Why by hand.** An operator reads the `rejected:` line and the reingest body
directly and gates CI on `$?`. A reason that names `org` or `1.0.0`, a
`rejected` entry with no `details`, `exit=0` from either sync, or a target
holding `team-jira` before step 4 is the defect this scenario catches.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build a
   two-layer registry in which `org` holds a skill with an `mcpServers` entry
   and `team` holds a same-ID overlay that declares no entry.

   ```bash
   mkdir -p "$WORK/reg/org/ops/jira-skill" "$WORK/reg/team/ops/jira-skill"
   printf 'multi_layer: true\nlayer_order:\n  - org\n  - team\n' > "$WORK/reg/.registry-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/org/.layer-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/team/.layer-config"
   for L in org team; do
     printf -- '---\nname: jira-skill\ndescription: File Jira tickets. Use when a task needs a ticket.\n---\n\nFile the ticket.\n' \
       > "$WORK/reg/$L/ops/jira-skill/SKILL.md"
   done
   printf -- '---\ntype: skill\nversion: 1.0.0\nmcpServers:\n  - name: jira\n    transport: stdio\n    command: org-jira\n    args: ["--org"]\n---\n' \
     > "$WORK/reg/org/ops/jira-skill/ARTIFACT.md"
   printf -- '---\ntype: skill\nversion: 1.1.0\nextends: ops/jira-skill\n---\n' \
     > "$WORK/reg/team/ops/jira-skill/ARTIFACT.md"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the four files
   exist.

2. Sync through the filesystem source, then repeat with `--dry-run`.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none 2> "$WORK/err.txt"; echo "exit=$?"
   cat "$WORK/err.txt"
   cat "$WORK/fs/ops/jira-skill/ARTIFACT.md"
   podium sync --registry "$WORK/reg" --target "$WORK/dry" --harness none --dry-run; echo "exit=$?"
   ```

   **Expect.** `exit=1` twice. `$WORK/err.txt` holds one line beginning
   `rejected: ops/jira-skill (ingest.invalid_artifact): extends: a parent in another layer`
   that names `mcpServers[jira]` and contains neither `org` nor `1.0.0`. The
   materialized manifest carries `version: 1.0.0` and `command: org-jira`.

3. Serve the same directory, reingest `team`, read the reingest body, and sync
   through the server.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8193 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8193/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export URL=http://127.0.0.1:8193
   podium layer reingest --registry "$URL" team; echo "exit=$?"
   curl -s -X POST "$URL/v1/layers/reingest?id=team"; echo
   podium sync --registry "$URL" --target "$WORK/srv" --harness none; echo "exit=$?"
   diff "$WORK/fs/ops/jira-skill/ARTIFACT.md" "$WORK/srv/ops/jira-skill/ARTIFACT.md" && echo same
   ```

   **Expect.** `podium layer reingest` prints the same `rejected:` line as
   step 2 and `exit=1`. The `curl` body holds a `rejected` entry with
   `"details":{"constraint":"executable_inheritance","fields":["mcpServers[jira]"]}`
   and no occurrence of `"org"`. The server sync prints `exit=0`, and `diff`
   prints `same`. A difference means the two deployment modes kept different
   copies.

4. Restate the entry in the overlay with a team command, reingest, and sync
   both ways.

   ```bash
   printf -- '---\ntype: skill\nversion: 1.1.0\nextends: ops/jira-skill\nmcpServers:\n  - name: jira\n    transport: stdio\n    command: team-jira\n    args: ["--team"]\n---\n' \
     > "$WORK/reg/team/ops/jira-skill/ARTIFACT.md"
   podium layer reingest --registry "$URL" team; echo "exit=$?"
   podium artifact show --registry "$URL" ops/jira-skill
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none; echo "exit=$?"
   cat "$WORK/fs/ops/jira-skill/ARTIFACT.md"
   ```

   **Expect.** The reingest prints no `rejected:` line and `exit=0`.
   `podium artifact show` shows `version: 1.1.0` and `command: team-jira`. The
   filesystem sync prints `exit=0`, and its manifest carries `team-jira` and
   no `org-jira`.

5. Clean up.

   ```bash
   kill "$SRV"; rm -rf "$WORK"
   ```
````

**MV-2.** Update scenario S39 in `test/manual-validation.md` ("same-ID `extends:` overlay and a three-level chain", about `:3443`). S39 registers `base` and `team` as separate `--local` layers, and the `team` same-ID overlay of `greet` (about `:3479-3486`) omits the undeclared key `x_owner: platform` that the `base` copy sets (about `:3469-3477`). Under SPEC-1 the reingest of `team` rejects `greet` 2.0.0 with `ingest.invalid_artifact` and `details.fields` `["x_owner"]`, so the step 3 Expect (about `:3505-3508`) and the step 4 Expect (about `:3528-3530`) fail, and an operator would report a correct rejection as the inherited-key drop. No automated run executes the scenario, so neither CODE-4's nor CODE-6's fixture obligation reaches it. The update passes before and after the rule and keeps the same-ID hidden-parent check S39 exists for:

1. The `team/greet/ARTIFACT.md` heredoc gains `x_owner: platform`, and a sentence after the step 2 block states that the overlay restates `x_owner` because the §4.6 executable-field rule requires a child to restate each undeclared key a parent in another layer sets, and that scenario S91 covers the refusal.
2. The step 3 Expect reads: the served frontmatter carries the overlay's own `x_runbook` and its restated `x_owner: platform`, and the inherited `description: base greet`; the served `version` is the overlay's `2.0.0`. The `registry.invalid_argument` paragraph is unchanged.
3. The step 4 Expect keeps three `ok` lines and version `2.0.0`, and its diagnosis reads: a `MISS` on `base greet` is the inherited-key drop, a `MISS` on `x_owner` is the overlay's restated key being dropped, and a `MISS` on `x_runbook` is the leaf's own key being dropped, which is the more serious. The probe tuple is unchanged.
4. Steps 5 and 6 stay inside the single `deep` layer and are unchanged.

## Documentation changes

### DOC-1. `docs/authoring/extends.md` and the frontmatter pages

1. In `docs/authoring/extends.md`, add a section `## Executable fields across layers` between `## Field merge semantics` and `## Bundled files under extends`. It states:
   - The executable fields are `hook_action`, `server_identifier`, `mcpServers`, and every top-level `ARTIFACT.md` key that the [ARTIFACT.md section of the frontmatter schema](../reference/frontmatter-schema.md#artifactmd) does not list. That section lists `source`, `audit_redact`, and `lint_suppress`, so they are outside the set. It also lists `name`, `description`, and `license`, so they are outside the set even in a skill's `ARTIFACT.md`. The set includes custom metadata keys, extension-type fields, and a key that only `SKILL.md` or `DOMAIN.md` defines written into `ARTIFACT.md`, such as `allowed-tools`, `compatibility`, `metadata`, or `include`.
   - When the parent comes from another layer, the child restates each executable field the parent sets, including a value the parent inherited from its own `extends:` chain. The restatement rules for each field, including that every `mcpServers` entry name, its `transport`, `command`, and `args`, and its `env` keys are restated, that an empty scalar does not restate an undeclared key, and that a child cannot remove a parent's `mcpServers` entry, `env` key, or undeclared key.
   - A child and a parent in the same layer are unaffected. A same-ID overlay crosses layers when its `extends:` reference selects the copy a lower layer contributes, so it restates each executable field that copy sets. When a registry server resolves the reference to an earlier version of the overlay stored from the overlay's own layer, the edge lies within that layer and the overlay is accepted without a restatement, while `podium sync` against a filesystem registry always tests the edge to the next-lower layer's copy and drops the overlay.
   - The fields outside the rule: `hook_event`, `runtime_requirements`, `sandbox_profile`, `external_resources`, and the remaining merge-table fields.
   - The rejection is `ingest.invalid_artifact` with `details.constraint: "executable_inheritance"` and `details.fields`, linking to `../reference/http-api.md#reingest` and `../reference/error-codes.md` for the wire form.
   - When a field the child would have to restate is named with the ID of an artifact in its parent chain (an `mcpServers` entry name, an `env` key, or an undeclared key), the child is rejected with `ingest.invalid_artifact` and no `details`, and the reason names no field and no parent. A registry server and `podium sync` against a filesystem registry both give the reason `extends: executable fields could not be read`. Such a child would fail every read, because every read of it withholds the IDs in its parent chain.
   - Stored versions keep serving and an unchanged reingest is classified unchanged. A child in the same layer as a stored version keeps inheriting what that version inherited. `podium sync` against a filesystem registry applies the rule on every run.
   - A registry server already refuses an authored `type: hook` artifact that omits `hook_action`, through lint, before this rule runs.
   - One YAML example of a same-ID hook overlay that restates `hook_event` and `hook_action`, with a note that lint requires `hook_event` and the executable-field rule requires `hook_action`.
2. Append to the **Omitted fields** paragraph (about `:116`): "A child whose parent comes from another layer restates the executable fields that parent sets; see [Executable fields across layers](#executable-fields-across-layers)."
3. In the **Served** paragraph of *Hidden parents* (about `:174`), insert after the sentence that ends "list-valued fields carry the parent's entries beside the child's.": "The [executable-field rule](#executable-fields-across-layers) limits which executable keys a child may leave to a parent in another layer."
4. In **Lint behavior** (about `:281-288`), change the bullet "Child declaring `extends:` against a parent in a layer the child's layer cannot see: ingest succeeds; the parent resolves at request time per the visibility rules above." to "Child declaring `extends:` against a parent in a layer the child's layer cannot see: ingest succeeds when the child restates every executable field the parent sets, and the parent resolves at request time per the visibility rules above." Add the bullet "Child that leaves an executable field to a parent in another layer: ingest error (`ingest.invalid_artifact`). Ingest and `podium sync` evaluate this rule, and `podium lint` does not, because it resolves no `extends:` chain."
5. In *Refining a skill with team-specific MCP servers*, add after the merged-result paragraph: "The team child restates the parent's only `mcpServers` entry in full, so it satisfies the [executable-field rule](#executable-fields-across-layers)." In *Tightening sandbox profile* (about `:261-279`), add: "The child ingests because the parent sets no executable field. A parent that sets one requires the child to restate it."
6. In `docs/authoring/frontmatter-reference.md:292` and `docs/reference/frontmatter-schema.md:192`, append to the sentence "This holds for every frontmatter field, including the fields in the table above." the clause ", except that a child whose parent comes from another layer restates the [executable fields](../authoring/extends.md#executable-fields-across-layers) that parent sets" (with the relative link adjusted per page).

The new YAML block is not runnable, so `tools/doccov` needs no manifest entry.

### DOC-2. Reference pages

1. `docs/reference/error-codes.md:113`, the `ingest.invalid_artifact` row's description becomes: "The manifest could not be decoded into an artifact record; its `extends:` pin failed to resolve, crossed types, or named a deprecated version; its parent chain could not be read, or a field it would have to restate under the executable-field rule is named with the ID of an artifact in its parent chain, which carries no `details`; or a child left an executable field to a parent in another layer, which carries `details.constraint: \"executable_inheritance\"` and `details.fields`. The artifact is rejected; the rest of the ingest continues. A filesystem-source `podium sync` reports the same code for an artifact it drops under the executable-field rule, and for an artifact whose `extends:` names a dropped artifact that no other layer contributes."
2. `docs/reference/cli.md:239`, after the collision sentences, add: "A child that leaves an executable field to a parent in another layer is dropped and reported as `rejected: <id> (ingest.invalid_artifact): <reason>`. After such a drop, the layers are composed again without the dropped artifact, so a higher-layer artifact that collided only with it is kept and its collision is not reported. An artifact whose `extends:` names a dropped artifact that no other layer contributes is dropped with the same code. The local catalog resolves an `extends:` reference to the highest-precedence copy under the parent's ID, or for a same-ID overlay to the next-lower copy, whatever pin the reference carries, and a server resolves the pin among stored versions, so the two can select different parent copies, and the rule then compares different layers. The exit status, `--dry-run`, and `--config` terms above apply to these drops as they apply to a collision drop." At `:286`, change "Against a local catalog with a collision between two registry layers, it prints the same `rejected: <id> (ingest.collision): <reason>` line for each dropped artifact" to "Against a local catalog with a collision between two registry layers, or with a child that leaves an executable field to a parent in another layer, it prints the same `rejected: <id> (ingest.collision): <reason>` or `rejected: <id> (ingest.invalid_artifact): <reason>` line for each dropped artifact". The `podium layer reingest` section is unchanged, because it already describes each rejected line without naming a rule.
3. `docs/reference/http-api.md`, under `### Reingest`, after the break-glass body block, add a paragraph: "A `200` response reports each artifact the cycle dropped for a reason other than a lint failure or a same-version content conflict in a `rejected` array. Each entry carries `artifact_id`, `code`, and `reason`, and a `details` object when the rule that rejected it defines one. Same-version content conflicts are reported in the `conflicts` array, and lint failures only as the `lint_failures` count. A cycle that accepts no artifact and classifies none unchanged is answered with the `409 ingest.immutable_violation` error envelope when it has a same-version content conflict, and with the `422 ingest.lint_failed` error envelope when it has a lint failure and no conflict. Neither envelope carries a `rejected` array, so it reports none of the cycle's other drops and no `details`. The executable-field rule's entry reads:" followed by a JSON block:

   ```json
   {
     "artifact_id": "ops/jira-skill",
     "code": "ingest.invalid_artifact",
     "reason": "extends: a parent in another layer sets executable fields this artifact does not restate: hook_action, mcpServers[jira]; restate each one in this artifact",
     "details": {"constraint": "executable_inheritance", "fields": ["hook_action", "mcpServers[jira]"]}
   }
   ```

   and the sentence "The reason and `details` name no parent ID, version, or layer."

### DOC-3. CHANGELOG

Add under `## [Unreleased]` `### Changed`:

```markdown
- **An `extends:` child restates the executable fields a parent in another layer sets** (§4.6, §7.3.1, §13.11.3): ingest rejects a new version that pins a parent from another layer and leaves `hook_action`, `server_identifier`, an `mcpServers` entry or one of its `transport`, `command`, `args`, or `env` members, or an `ARTIFACT.md` frontmatter key that the `ARTIFACT.md` section of the frontmatter schema does not list, such as an extension-type field or `allowed-tools`, to that parent. A value the parent inherited from its own `extends:` chain counts as one it sets. A same-ID overlay whose `extends:` reference selects a lower layer's copy crosses layers, so such an overlay of a hook restates its `hook_action`; a reference that selects an earlier version stored from the overlay's own layer is a same-layer edge on a server. The rejection uses `ingest.invalid_artifact`, and its reingest `rejected` entry carries a new `details` member with `constraint: "executable_inheritance"` and `fields`, which lists each field to restate. The reason and `details` name no parent ID, version, or layer. A version stored before this change keeps its pin and keeps serving, and an unchanged re-ingest of it is classified unchanged. A filesystem-source `podium sync` applies the rule on every run. It drops each child the rule refuses, and each artifact whose `extends:` names a dropped ID that no other layer contributes. After a drop it composes the layers again, so a higher-layer artifact that collided only with a dropped child is kept, as a server keeps it. It reports each drop as `rejected: <id> (ingest.invalid_artifact): <reason>` and treats it as it treats a collision drop for exit status, `--dry-run`, `--config` targets, and `podium sync override`. In the `pkg/registry/filesystem` library API, `WalkOptions.OnCollision func(layer.Collision)` is replaced by `OnDrop func(layer.Drop)`, which reports both drop kinds. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the inheritance.
```

## Open questions

- **Q-1. Scope of undeclared keys.** D-1 treats every undeclared key as executable, on every artifact type, because the Claude Code adapter writes `agent`, `command`, and extension-type frontmatter verbatim into harness-native files. A cross-layer child must therefore also restate metadata-only custom keys such as `acme_owner`. Should the fail-closed treatment narrow to the types whose manifests reach harness files verbatim, which would exempt undeclared keys on `skill`, `context`, `rule`, `hook`, and `mcp-server`? The draft recommends keeping every type, so the rule stays one condition with no per-adapter knowledge in it. Resolved at sign-off: keep every type, as D-1 states.
- **Q-2. The `sandbox_profile` merge.** The §4.6 table's "most-restrictive" row gives no ordering, the code ranks the profiles on one scale (`pkg/manifest/merge.go:231-252`), and a parent's `seccomp-strict` replaces a child's `read-only-fs`. Should a follow-up proposal define the ordering or the composition of profiles? Resolved at sign-off: the `sandbox_profile` merge stays outside this proposal, as the Non-goals section states. No step of this proposal changes it.

## Non-goals

- Re-evaluating, purging, flagging, or reporting stored children that inherit executable fields across layers. They keep serving, and no migration tooling or report is added.
- Per-value layer provenance tracking, and closing the case where a same-layer child inherits through a stored, grandfathered intermediate.
- Syntax for removing an inherited `mcpServers` entry, `env` key, or undeclared key.
- Fixing the single-ranking `sandbox_profile` merge or validating profile values. Restatement cannot correct it, so it belongs in a separate proposal (Q-2).
- Extending the `TypeProvider` SPI with field-semantics or executable declarations (§9.1).
- Adding `ingest.invalid_artifact` to the §6.10 matrix axis, or fixing the orphaned `// Matrix: §6.10 (ingest.invalid_artifact)` annotations in existing tests.
- Any new §6.10 code, environment variable, flag, `registry.yaml` key, or opt-out.
- Changing the read-path merge in `pkg/registry/core`, the §4.7.10 delivery record, or the hidden-parent withholding rules.
- The workspace local overlay (§6.4), which resolves no `extends:` chain and is unaffected.
- Making `podium lint` evaluate the rule. Lint does not resolve `extends:` today (`cmd/podium/main.go:1081`), and the parent can live in a repository lint does not see.
- Rendering `details` in the web UI or reading it from the SDKs.
- Changing how the filesystem resolver handles other `extends.unresolved` and `extends.type_mismatch` causes, which keep failing the walk.
- Aligning parent selection between the filesystem resolver and the server, and aligning the same-ID cascade reason text.
- Closing the pre-existing divergence in which server lint refuses a same-layer `type: hook` child that omits `hook_action` while a filesystem sync merges it.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- DOC-1's draft amended the *Served* paragraph to say that the executable values in a merged block are values the child authored or an ancestor in the child's own layer supplied. SPEC-1's revised edit 3 removes that guarantee, because the grandfathered same-layer intermediate falsifies it. DOC-1 item 3 now inserts the limit sentence SPEC-1 uses.
- DOC-1 and DOC-3 described the undeclared keys as "every key §4.3 does not define", which counts `source`, `audit_redact`, and `lint_suppress` as executable while CODE-1 does not. Both now use SPEC-1's set.
- SPEC-1 edits 1 to 5 from the challenge are applied: the executable set names `§4.3`, `§4.4.2`, `audit_redact`, and `lint_suppress`; the disclosure paragraph is amended rather than cited; the signature sentence uses the edge form; `external_resources` cites what §4.3 states; and the stored-version sentence mirrors the deprecation rule's timeless form. D-13 matches.
- CODE-6 item 4 required SPEC-3 to state which parent each mode selects, and the SPEC-3 replacement paragraph did not. SPEC-3 now carries that sentence and the accepted consequence, and DOC-2 mirrors it.
- The draft's fixture audit grepped only `hook_action`, `mcpServers`, and `server_identifier`. Undeclared keys also break `pkg/registry/core/extends_merged_frontmatter_test.go`, `test/integration/sync_manifest_body_test.go`, and `test/e2e/audit_manifest_redaction_test.go`. TEST-6 collects every known fixture update in a step that lands before the rule, and CODE-4 and CODE-6 carry the obligation to fix any further one under TEST-6's constraint.
- The draft split compile-required test edits across TEST-3, TEST-4, and CODE-7, so a code commit would not build or a test file had two owners. The `walk_test.go` rename edits moved into CODE-6, the `sync_collision_test.go` and `sync_equivalence_test.go:342` edits stay in CODE-7 alone, and CODE-6 and CODE-7 share a step.
- The cascade reason "built by a helper shared with the server's unresolved-pin wording" had no definition. CODE-2 defines `layer.UnresolvedParentReason`, and the Watch-out list and edge-case table record that the server's same-ID reason differs.
- The executable-inheritance reason, the chain-failure reason, and the unreadable-fields reason were unspecified although tests assert them. CODE-2, CODE-4, and CODE-6 now give each exact text.

### Pass 1 (2026-11-09, automated)

- SPEC-1's executable-fields paragraph said a harness adapter launches `hook_action`, which contradicts the §6.7 adapter sandbox contract (`spec/06-mcp-server.md:359`). SPEC-1 now says the adapter writes `hook_action` into the harness's hook configuration as the command the harness runs, and derives from `server_identifier` the command or URL the harness launches. The *Code that carries it* paragraph states the same actor split.
- Scenario S39 in `test/manual-validation.md` relied on cross-layer inheritance of the undeclared key `x_owner` through a same-ID overlay, and no edit reached it. MV-2 makes the `team` overlay restate `x_owner` and updates the step 3 and step 4 Expect text, and S2 carries MV-2 beside TEST-6. The Summary and the fixture Watch-out name MV-2.
- CODE-6 never stated how `resolveExtends` returns a reduced record set, and its compile-required test edits omitted `extends_test.go:286` and `TestWalk_DropPolicyDroppedRecordIsNotAParent`. CODE-6 now gives the signature `resolveExtends(deduped, kept []ArtifactRecord, onDrop func(layer.Drop)) ([]ArtifactRecord, error)`, has `Walk` return its result, and lists both test edits. The existing-tests list excepts the changed call, and the Watch-out bullet names the compile-required lists instead of a file count. (Superseded by Pass 4: `resolveExtends` keeps its signature `resolveExtends(deduped, all []ArtifactRecord) error` as the rewrite pass, `composeExtends` owns the drop path, and the `extends_test.go` edit is no longer required.)
- The spec, Summary, D-1, DOC-3, and TEST-1 defined the undeclared keys as those §4.3 does not define, which leaves out the §4.3.4 `SKILL.md` keys that `declaredKeys` treats as undeclared. SPEC-1 now defines an undeclared key as an `ARTIFACT.md` key that §4.3 does not define as an `ARTIFACT.md` field, other than `source`, `audit_redact`, and `lint_suppress`, and names `compatibility`, `metadata`, and `allowed-tools` as undeclared when they appear in `ARTIFACT.md`. D-1 corrects the complement statement, CODE-1 states that `declaredKeys` is unchanged, DOC-3 and Q-1 follow, and TEST-1 adds an `allowed-tools` case on a `command` parent.
- Rejection field paths were built from parent-authored names without the §4.6 *Withheld* reference test, so an ancestor key or entry named with an ancestor's ID would print that ID. CODE-1 now takes `childID`, builds the parent set with `pinnedParents`, tests each emitted name component with `parentIDs.discloses`, and returns `ErrUnreadableChain` on a disclosure, which CODE-4 and CODE-6 already map to the no-parent chain-failure rejection. (Reason text superseded by the prune: both modes now give `layer.UnreadableExecutableFieldsReason`.) SPEC-1's details paragraph, D-8, the Summary, the edge-case table, TEST-1, and TEST-2 (m) carry the same predicate.
- DOC-1 defined the executable keys as those absent from the whole frontmatter-schema page, which also lists `SKILL.md` and `DOMAIN.md` keys. DOC-1 now scopes the definition to the page's ARTIFACT.md section and names `SKILL.md` and `DOMAIN.md` keys written into `ARTIFACT.md` as executable.
- DOC-2 limited the parent-selection divergence to an unpinned parent, although the filesystem resolver strips every pin (`pkg/registry/filesystem/extends.go:97`, `:109-114`). The `cli.md` sentence now states the SPEC-3 condition for any pin.
- TEST-6 moved the audit e2e child into its parent's layer, where `finance/inherited-payroll` sorts before `shared/payroll-base` and server ingest rejects it. TEST-6 now renames the child to `shared/payroll-inherited` and drops `team-foo`, its constraint requires a same-layer parent to sort before its child, the new `writeExtendsRegistry` child follows that order, and a Watch-out bullet records the requirement.
- Correction: D-8 cited `pkg/manifest/merge_serialize.go:140-145` for the read-path reference test, but those lines hold the `yaml.Marshal` call. D-8 now cites `:132-138`, the comment and the `parents.names(root)` check that returns `ErrUnhidableParent`.
- Correction: CODE-1's list of reused package-private helpers omitted the two that step (5) uses. The list now names `pinnedParents` and `parentIDs.discloses`, so the read path and the rejection share one withholding test.
- Correction: the edge-case row for a disclosing field path cited DOC-2, which did not describe that rejection, and no doc page stated the filesystem reason `extends: executable fields could not be read`. DOC-2 item 1's `ingest.invalid_artifact` row now names the cause and the absent `details`, DOC-1 item 1 gains a bullet that gives both modes' reasons, and the edge row cites DOC-1 beside DOC-2. (Superseded by the prune: the two modes now share one reason, and the bullet gives it once.)
- Correction: no test pinned the filesystem side of the disclosure filter, which turns a walk failure into a per-record drop. TEST-3 (h) adds a two-layer fixture whose L1 parent sets an undeclared key named with its own ID and whose L2 child omits it, and asserts that the walk succeeds, that exactly one drop with the unreadable-fields reason is reported, that the reason omits the parent ID, and that the parent is kept. The fixture names the immediate parent's own ID because a grandparent's ID would make the intermediate's own rewrite fail the walk. TEST-4 (b) reuses the fixture to pin cross-mode code equivalence.

### Pass 2 (2026-11-09, automated)

- D-5, SPEC-1, the edge-case table, DOC-1, and DOC-3 said a same-ID overlay crosses layers by definition. On the server, `resolveExtendsPin` excludes only the child's identical row (`pkg/registry/ingest/ingest.go:1507`), so an earlier version of the overlay stored from the overlay's own layer stays a candidate, and an unpinned reference selects it when it is the most recently ingested (`:1567-1581`). The server then tests a within-layer edge and accepts the overlay, while the filesystem resolver always tests the edge to the next-lower layer (`pkg/registry/filesystem/extends.go:101-108`) and drops it. SPEC-1 now states that a same-ID overlay crosses layers when its pin resolves to a version a lower-precedence layer contributes, and that a reference selecting an earlier same-layer version is a within-layer edge. D-5 gives the code evidence and records that resolving same-ID pins only among lower-layer rows would need a §4.7.6 edit. The edge-case table limits the overlay row to a pin onto the lower copy and adds a row for the same-layer pin as an accepted instance of the SPEC-3 parent-selection divergence. DOC-1 item 1 and DOC-3 carry the scoped statement, the parent-selection Watch-out and CODE-6's doc comment name the case, TEST-2 (n) pins the server acceptance, and TEST-3 (b3) records the filesystem drop.
- DOC-2 item 3 said the reingest `rejected` array reports every artifact the cycle rejected, but the handler leaves lint failures and same-version conflicts out of it (`pkg/registry/server/layers.go:1999-2010`, `:2026-2029`), and §7.3.1 counts both as rejections (`spec/07-external-integration.md:151`). DOC-2 item 3 now scopes `rejected` to drops other than a lint failure or a same-version content conflict, and it states that conflicts appear in `conflicts` and lint failures only as the `lint_failures` count. SPEC-2 edit 2 stated the same unscoped claim in spec text, so its wire sentence carries the same scope.

### Pass 3 (2026-11-09, automated)

- SPEC-2 edit 2 and DOC-2 item 3 promised that the reingest response reports every non-lint, non-conflict rejection with its `details`, but the handler builds `rejected` only for the 200 body. A cycle with a same-version conflict and nothing accepted or unchanged gets the `ingest.immutable_violation` 409 envelope (`pkg/registry/server/layers.go:1988-1997`), and a cycle with a lint failure, no conflict, and nothing accepted or unchanged gets the `ingest.lint_failed` 422 envelope (`pkg/registry/ingest/ingest.go:922-923`; `pkg/registry/server/layers.go:2045-2046`). Neither condition reads `res.Rejected`. SPEC-2 edit 2 now scopes the wire sentence to a cycle answered without a §6.10 error envelope and states that neither envelope reports the cycle's other rejections or their `details`, with the code evidence and the reason the envelopes are left unchanged. DOC-2 item 3 carries the same scope for `http-api.md`. CODE-5 states that the 409 branch and the 422 mapping are unchanged. TEST-4 (a2) pins the 422 case with a lint-failing hook beside the refused child. The edge-case table gains a row for the envelope case, the Summary states the scope, and checklist step S11 names it.

### Pass 4 (2026-11-09, automated)

- CODE-4's abort arm returned `fmt.Errorf("ingest: resolve extends chain for %s: %w", rec.ID, err)` around the `core.ExtendsChain` error, which names the `ExtendsPin` of the record whose parent read failed. For a chain B (L2) to A (L1) to A0, that pin is A0's ID and version, beyond the reference B's author wrote, and the reingest handler writes the runner error's text into the `registry.unavailable` envelope (`pkg/registry/server/layers.go:1931-1935`, `:2057-2058`). CODE-4 now logs the walk error and aborts with an unexported `*chainReadError` whose message names the child alone and whose `Unwrap` returns the walk error, following `invalidArtifactError`. D-8 records the withholding with its §4.6 *Withheld* basis, the Summary names it, the edge-case row gives the envelope and the message, and TEST-2 (i) asserts the exact text and the absence of A0's ID and version.
- CODE-6 kept a collision drop whose only lower-precedence record the rule later refused, so with `P` in L1, a refused `x` (`extends: P`) in L2, and an `x` without `extends:` in L3, a filesystem sync served no `x` while a fresh server, which never stores the refused row and checks collisions only against stored rows (`pkg/registry/ingest/ingest.go:567-573`, `:751-764`), served L3's `x`. That contradicted the §13.11.3 collision sentence (`spec/13-deployment.md:339`) and D-9's equivalence claim. CODE-6 now runs a recomposition fixpoint, `composeExtends`, that re-runs `dedupe` without the refused records on every iteration and reports only the settled iteration's collisions. `dedupe` returns its collision drops instead of calling the callback, and `resolveExtends` keeps its signature as the rewrite pass, which removes the `extends_test.go` compile-required edit. SPEC-3 and its rationale, D-9, the Summary, the Watch-out on the CODE-6 and CODE-7 commit, the edge-case table, DOC-2 item 2, and DOC-3 state the re-applied collision rule. TEST-3 (c4) and a TEST-4 (b) subcase pin it, and checklist step S10 names it.
- SPEC-1 defined an undeclared key to include "a `SKILL.md` field that §4.3.4 omits from `ARTIFACT.md`", but the §4.3.4 allocation table also marks the universal `name`, `description`, and `license` "Omitted" (`spec/04-artifact-model.md:263-265`), which lint admits in a skill's `ARTIFACT.md` with a warning and `declaredKeys` contains. SPEC-1 now names the fields §4.3 defines for `SKILL.md` alone and states that `name`, `description`, and `license` are not undeclared keys in a skill's `ARTIFACT.md`. D-1 gives the evidence, the SPEC-1 rationale, the Summary, CODE-1 step (4), DOC-1 item 1, and DOC-3 carry the same predicate, and TEST-1 adds a skill case where those three keys give nothing.
- D-1 cited `pkg/manifest/types.go:100-104` for the `Artifact` struct declaring `name`, `description`, and `license`, but that range holds `Type`, `Name`, `Version`, `Description`, and `WhenToUse` and omits `License`, which the struct declares at `:107`. D-1 now cites `pkg/manifest/types.go:101`, `:103`, and `:107`, the lines that declare the three keys.

### Prune (2026-11-09)

- An introspection pass found that the executable-set predicate and the parent-selection divergence were each restated in many sections, and that fixes to one copy had left others behind in three earlier rounds. The Summary's fixed decisions now name each decision and its subject and restate no predicate, and the *What changes* bullets point to SPEC-2 and SPEC-3 instead of restating the envelope scope and the recomposition. SPEC-1 edit 2 is the only normative statement of the executable set, D-1 is the only place that argues its equality with `declaredKeys`, CODE-1 step (4), the SPEC-1 rationale, TEST-1, and Q-1 point to D-1, and DOC-3 defines the set by reference to the `ARTIFACT.md` section of the frontmatter schema, as DOC-1 does. D-5 holds the evidence for the parent-selection divergence and SPEC-3 its normative statement; the Watch-out bullet and the two edge rows point to them, and CODE-6's doc-comment wording is an implementor's choice that names the selected record and cites §13.11.3. CODE-6's step-by-step recomposition loop is replaced by an implementor's choice constrained to the repeated-recomposition result, the collision-before-refusal report order, deterministic refusal order, and validation on authored bytes, which TEST-3 (c), (c4), and (h) and TEST-4 (b) pin; the `dedupe` return change, the `*refusedRecordError` contract, the cascade rule, and the `composeExtends` name stay specified. Earlier log bullets that say the Summary, the SPEC-1 rationale, CODE-1 step (4), the parent-selection Watch-out, or CODE-6's doc comment state one of these predicates are superseded by this prune.
- The two modes gave different reasons when `InheritedExecutableFields` returned an error (`extends: parent chain does not resolve` on a server, `extends: executable fields could not be read` on a filesystem sync), so D-12's claim of a shared reason held only for the fields outcome. CODE-2 now exports `layer.UnreadableExecutableFieldsReason`, which CODE-4 and CODE-6 both use, and `extends: parent chain does not resolve` stays only on the server's store-side missing-ancestor and cycle arms. D-8, D-12, the edge-case row for a disclosing field path, DOC-1 item 1, TEST-2 (m), TEST-3 (h), and TEST-4 (b) follow.

### Pass 5 (2026-11-09, automated)

- CODE-6's implementor's choice required collision drops before refusal drops and refusal drops in a deterministic order, and said TEST-3 (c), (c4), and (h) and TEST-4 (b) pin the result, but no listed test observed an order: (c4), (h), and every TEST-4 (b) subcase report one drop, (c) checked membership alone, and no fixture paired a surviving `ingest.collision` drop with a refusal. `podium sync` prints one `rejected:` line per drop in `OnDrop` order (`cmd/podium/layer.go:615-619`), and only collision order was pinned (`pkg/registry/filesystem/walk_test.go:390-434`). CODE-6 now fixes the order a test can assert: the settled `dedupe` collisions in the order it returns them, then the refusal drops sorted byte-wise by canonical ID and, for one ID, by layer from lowest to highest precedence. TEST-3 (c) compares the full slice, in which D precedes C although C is refused first, TEST-3 (c3) asserts L2's C before L3's C, and the new TEST-3 (c5) pairs a surviving `z/clash` collision with a refused `a/child`, asserts the collision first, and walks twice to show the same slice. CODE-6's list of pinning cases and checklist step S10 name the order.
