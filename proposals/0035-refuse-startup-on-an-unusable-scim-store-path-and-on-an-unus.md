# Proposal 0035: Refuse startup on an unusable SCIM store path, on an unopenable audit sink while anchoring, and on an unusable or shared audit anchor key, and specify the SCIM and anchor variables

- Issue: (to be filed)
- Status: Applied to spec (2026-10-02). Signed off as staged, including the OQ-1 decision: with anchoring enabled, an audit file sink that cannot be opened refuses startup with config.audit_sink_unavailable; an http(s) sink never refuses; the interval-0 warning is unchanged.
- Date: 2026-10-02

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §13.12 and §6.3.1: `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` are specified. A set `PODIUM_SCIM_STORE_PATH` that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable` under every identity provider (SPEC-1, SPEC-2).
- §13.12 and §8.6: a new "Audit anchoring" table specifies `PODIUM_AUDIT_LOG_PATH`, `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, and `PODIUM_AUDIT_SIGNING_KEY_PATH`, and §8.6 specifies local chain-head anchoring, the key-separation rule, and the accepted purpose-label risk. An anchor key that cannot be read, parsed, or generated refuses startup with `config.audit_anchor_key_unavailable`, and an anchor key that the registry signing key file also carries refuses startup with `config.audit_anchor_key_shared` while registry signing is on (SPEC-3, SPEC-4). With anchoring enabled, a file audit sink that cannot be opened refuses startup with `config.audit_sink_unavailable`, while an `http(s)` sink never refuses.
- `pkg/scim`: `LoadFileStore` probes the store's parent directory for writability at load (CODE-1). `internal/serverboot` returns the SCIM load error in place of the in-memory fallback (CODE-2), returns a file-sink open failure from `openAuditSink` instead of logging it, and loads and checks the audit sink and the anchor key between the audit-sink open and the §13.4 first-start rewrite (CODE-3).
- `tools/matrix/matrices.go`: the new codes become §6.10 matrix cells, each with a `// Matrix:` annotated end-to-end test (CODE-4, TEST-3, TEST-4).
- Tests at unit, integration, and end-to-end level pin every refusal condition and every negative control (TEST-1 to TEST-4). A `Run`-level integration test in TEST-2 pins that every anchor refusal precedes the §13.4 rewrite and the bootstrap ingest.
- The configuration reference, the error-code catalog, the deployment pages, the operator guide, the changelog, and a manual-validation scenario follow (DOC-1, DOC-2, DOC-3, CL-1, MV-1).

**Fixed decisions.**

- D26 is settled: an unusable `PODIUM_SCIM_STORE_PATH` refuses startup and never falls back to the in-memory store.
- The SCIM refusal applies whenever `PODIUM_SCIM_STORE_PATH` is non-empty, under every identity provider, and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver.
- "Unusable" means: a read that fails for a reason other than the file's absence, a non-empty file that does not parse, a parent directory that cannot be created, or a parent directory in which a file cannot be created. A missing file and an empty file load as an empty directory.
- The SCIM store is opened where it is opened today, after the bootstrap ingest. The refusal claims no ordering relative to the §13.4 rewrite or the ingest.
- C30 option (c) is settled: with anchoring running and registry signing on, an anchor public key equal to the registry `public:` key or any `verify:` key refuses startup with `config.audit_anchor_key_shared`. The comparison is `ed25519.PublicKey.Equal`.
- The signed-message format does not change, and no purpose label is added (C30 option (a) stays deferred).
- An anchor key that cannot be read, parsed, or generated while anchoring runs refuses startup with `config.audit_anchor_key_unavailable`. With the interval at 0, the anchor key is never read.
- With `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0 and `PODIUM_AUDIT_LOG_PATH` not an `http(s)` value, a file sink that `resolveAuditPath` or `audit.NewFileSink` cannot open refuses startup with `config.audit_sink_unavailable` (OQ-1, decided 2026-10-02). The sink check runs before the anchor key is read. An `http(s)` sink never refuses, whether or not it constructs. With the interval at 0, a failed file sink still logs a warning and the registry starts.
- §8.6 names local chain-head anchoring as a mechanism distinct from the transparency-log anchoring it recommends and that §13.10 lists as out of scope for standalone.
- Every anchor refusal runs before the §13.4 first-start rewrite and before the bootstrap ingest.
- The new codes are defined in §13.12 beside their variables and are §6.10 matrix cells. §6.10 itself is not edited. The sink, key, and shared-key failures carry separate codes because their remedies differ.
- The SCIM and audit-anchoring variables that SPEC-1 and SPEC-3 specify are environment only and have no `registry.yaml` key.
- Both binaries exit 1 on each refusal.

**Watch out for.**

- **CODE-3 breaks the compile of existing tests.** Changing `loadOrGenerateAuditSigner` to return `(sign.RegistryManagedKey, string, error)` breaks `internal/serverboot/audit_anchor_test.go:25`, `:33`, and `:114` and `internal/serverboot/coverage_gaps_test.go:358`, `:362`, and `:419`. Changing `startAnchorScheduler` breaks `internal/serverboot/schedulers_test.go:16` and `:32`. Step S8 lands CODE-3 and TEST-2 together for that reason.
- **`openAuditSink` has two callers and three test call sites.** Adding the error return changes `internal/serverboot/serverboot.go:960` and `internal/serverboot/signpass.go:190`, and breaks `internal/serverboot/schedulers_test.go:185`, `:202`, and `:220`. `sign-stored-rows` never anchors, so it logs the error and continues, as today. Step S8 updates all five.
- **`audit.NewFileSink` does not test writability.** It creates the parent directory and reads an existing file (`pkg/audit/file.go:33-51`). A log that can be read but not written passes startup and fails on the first `Append`. The refusal covers only what `NewFileSink` and `resolveAuditPath` detect. Do not add a write probe.
- **`key_id` is a digest of the public key.** `sign.KeyIDFor` is the hex of the first 8 bytes of SHA-256 of the public key (`pkg/sign/registry_managed.go`). Compare the keys with `ed25519.PublicKey.Equal`, and use `KeyIDFor` only to name the matching key in the error.
- **Do not wrap `sign.ErrRegistryManagedUnavailable` in the anchor errors.** That sentinel carries `config.signature_provider_unavailable`, and an `errors.Is` check would then classify an anchor failure as a registry-key failure.
- **The anchor key loads in one slot only.** It must load after `openAuditSink` (`internal/serverboot/serverboot.go:960`) and before the §13.4 rewrite block (`:966`). Placing it after the bind-error gate, or at the current scheduler call site (`:1624`), lets a refused start rewrite rows, and `TestRun_AnchorKeyRefusalPrecedesTheRewrite` (TEST-2) fails on either placement.
- **A refused anchor start can still write files.** `registrySignerFor` runs earlier and may generate the default registry signing key, and `audit.NewFileSink` creates the audit log's parent directory (`pkg/audit/file.go:41`). It does not create the file, which the first `Append` opens (`pkg/audit/file.go:97`). A start refused with `config.audit_sink_unavailable` generates no anchor key, because the sink check precedes the key load. Tests assert only what the spec promises. `TestRun_AnchorKeyRefusalPrecedesTheRewrite` (TEST-2) asserts that a refused start leaves seeded rows and the §13.4 completion record unchanged and logs no bootstrap ingest, and the TEST-4 end-to-end tests assert no anchor event and no listener.
- **The SCIM refusal does not run early.** The `config.runtime_keys_unavailable` precedent returns inline where its file is loaded, after the rewrite and the ingest. Moving the SCIM open ahead of `refuseUnpersistedSigningKey` would still follow `bootstrapDefaultTenant` and `seedBootstrapAdmins`, and with the CODE-1 probe it would create the SCIM directory on a start a later refusal rejects.
- **`envInt` silently maps a bad interval to 0.** `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60s` disables anchoring with no message (`internal/serverboot/serverboot.go:266-276`). SPEC-3 states this. Do not change `envInt`.
- **The writability probe must not call `save()`.** `save()` would rewrite the operator's file on every start, could overwrite another replica's write, and uses the fixed `<path>.tmp` name. The probe uses `os.CreateTemp`.
- **`TestRun_StandaloneGracefulShutdown` enables anchoring.** It sets the interval to 86400 under a fresh `HOME`, so the default anchor key and the default registry key are generated as distinct keypairs and the test stays green. A failure there means the anchor key is being compared against the wrong file.
- **The manual-validation scenario number.** Proposals 0036, 0037, and 0038 each stage S78, so this proposal stages S79. A proposal that lands after another has taken its number renumbers its scenario and index row to the next free number.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.12 Identity provider table gains the `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` rows and defines `config.scim_store_unavailable`.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-3. §13.12 gains the "Audit anchoring" table, which specifies `PODIUM_AUDIT_LOG_PATH`, and defines `config.audit_sink_unavailable`, `config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared`.
      Levels: —. Depends on: —
- [ ] **S3 · spec** — SPEC-2. §6.3.1 gains the "Receiver and persistence" paragraph.
      Levels: —. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. §8.6 gains the "Local chain-head anchoring" paragraph.
      Levels: —. Depends on: S2
- [ ] **S5 · code** — CODE-1. `LoadFileStore` probes the parent directory for writability.
      Levels: unit. Depends on: S1
- [ ] **S6 · test** — TEST-1. `pkg/scim` load and probe table test.
      Levels: unit. Depends on: S5
- [ ] **S7 · code** — CODE-2. `serverboot` returns `config.scim_store_unavailable` through `openSCIMStore` in place of the in-memory fallback.
      Levels: unit, e2e. Depends on: S1, S3, S5
- [ ] **S8 · code** — CODE-3, TEST-2. `openAuditSink` returns a file-sink open failure, `serverboot` loads the anchor key early through `loadAnchorSigner`, refuses an unopenable file sink, an unusable key, or a shared key, corrects the two comments, and the serverboot unit tests are updated and extended. Bundled because the CODE-3 signature changes break the compile of the existing tests TEST-2 rewrites.
      Levels: unit, integration. Depends on: S2, S4, S7
- [ ] **S9 · test** — TEST-3. End-to-end SCIM refusal test and the nested-path extension of the persistence test.
      Levels: e2e. Depends on: S5, S7
- [ ] **S10 · test** — TEST-4. End-to-end anchor-sink and anchor-key refusal tests and negative controls, and the journey-test comment fix.
      Levels: e2e. Depends on: S8, S9
- [ ] **S11 · code** — CODE-4. The new codes become §6.10 matrix cells. Placed after S9 and S10 because `matrix-audit` fails on a cell that has no `// Matrix:` annotated test.
      Levels: unit (matrix-audit). Depends on: S9, S10
- [ ] **S12 · docs** — DOC-1. `docs/reference/cli.md` environment rows and `docs/reference/error-codes.md` code rows.
      Levels: —. Depends on: S7, S8
- [ ] **S13 · docs** — DOC-2. Deployment pages state the SCIM refusal and name the anchor variables.
      Levels: —. Depends on: S7, S8
- [ ] **S14 · docs** — DOC-3. Operator-guide troubleshooting subsection.
      Levels: —. Depends on: S7, S8
- [ ] **S15 · docs** — CL-1. `## [Unreleased]` changelog entries.
      Levels: —. Depends on: S7, S8
- [ ] **S16 · docs** — MV-1. Manual-validation scenario S79.
      Levels: manual. Depends on: S7, S8

**Ordering constraints.** SPEC-2 cites the §13.12 rows SPEC-1 adds, and SPEC-4 cites the table SPEC-3 adds. Each code step follows the spec step whose text it implements. TEST-4 reads the output that TEST-3's change to `gwExpectStartupFailure` returns, so S10 follows S9. CODE-4 follows the tests that carry its matrix annotations.

## Current state and the gap

Two operator-configuration surfaces are implemented in `internal/serverboot` and appear nowhere in `spec/`. Both degrade silently where a misconfigured start should be refused.

### SCIM receiver and store

`buildSCIMHandler` reads `PODIUM_SCIM_TOKENS`. It splits the value on commas, trims each entry, drops blank entries, and collapses duplicates into a map. It mounts the receiver at `/scim/v2/` only when at least one token remains (`internal/serverboot/serverboot.go:70-86`, `:1254-1256`; `pkg/registry/server/server.go`). `PODIUM_SCIM_STORE_PATH` is opened at `internal/serverboot/serverboot.go:1144`, before the token check and regardless of the identity provider. No default location exists, so an unset path holds the directory in memory only.

When `scim.LoadFileStore` fails, boot logs `warning: SCIM persistence disabled: <err>` and keeps `scim.NewMemory()` (`internal/serverboot/serverboot.go:1143-1151`), so every directory record pushed afterwards is lost on restart. `LoadFileStore` detects two failures at load: a read error other than not-exist (EACCES, EISDIR, ENOTDIR) and a JSON parse error (`pkg/scim/file_store.go:47-60`). A missing file, a missing parent directory, and an empty file all load as an empty store. An uncreatable or unwritable parent directory surfaces only in `save()` on the first mutation (`pkg/scim/file_store.go:70-92`). Each mutator updates `Memory` before `save()` runs (`pkg/scim/file_store.go:96-107`, `:134-141`, `:182-188`). The handler answers a failed save on a create or a replace (POST or PUT) with HTTP 400 `invalidValue` (`pkg/scim/handler.go:151-164`, `:196-209`, `:257-270`, `:302-315`), and the record is then served from memory and lost on restart, which is the same data loss as the load fallback. It answers a failed save on a delete with HTTP 500 and no `scimType` (`pkg/scim/handler.go:214-217`, `:320-323`), and the deleted record returns from the file on restart. The receiver routes no PATCH.

Under `oidc-jwt` and `injected-session-token`, the same store feeds §4.6 `groups:` expansion (`internal/serverboot/serverboot.go:111-118`, `:1163`). Under every other provider it feeds no visibility decision (§6.3.1 "Where the directory expansion applies", §6.3.3; proposal 0022). No file under `spec/` names `PODIUM_SCIM_TOKENS` or `PODIUM_SCIM_STORE_PATH`. The docs describe them on five deployment pages (`docs/deployment/oidc/index.md:59`, `docs/deployment/gateway-delegated-identity.md:57`, `docs/deployment/oidc/okta.md:106`, `docs/deployment/oidc/entra-id.md:144`, `docs/deployment/oidc/keycloak.md:118`). The configuration reference (`docs/reference/cli.md:808`) and the operator guide's troubleshooting section do not.

### Audit anchoring

Anchoring runs only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 (`internal/serverboot/serverboot.go:2293`, `:1623-1625`). The anchor key is an Ed25519 `RegistryManagedKey` read from `PODIUM_AUDIT_SIGNING_KEY_PATH`, default `~/.podium/standalone/audit.key`. It is generated only when the file is absent, and its `verify:` lines are ignored (`internal/serverboot/audit_anchor.go:142-178`). The registry signing key is a separate file at `PODIUM_SIGN_KEY_PATH`, default `~/.podium/standalone/registry-signing.key` (`pkg/sign/keyfile.go`), and its verification key set is the `public:` line plus every `verify:` line (`internal/serverboot/signing.go:105-121`).

Both purposes sign through `RegistryManagedKey.Sign`, which Ed25519-signs only the decoded digest bytes. The signed message carries no purpose label and no algorithm name (`pkg/sign/registry_managed.go:90-98`). `audit.Anchor` signs `"sha256:" + chainHead` (`pkg/audit/anchor.go:37-38`). Only the distinct default paths keep the two keys apart. An operator who points both variables at one keypair, or copies one keypair to both paths, makes a signature over a chain head and a signature over an equal artifact content hash interchangeable. `PODIUM_SIGN` accepts only `registry-key` or `none` (§13.12 Signing), so `registrySigningEnabled(cfg.signMode)` (`internal/serverboot/signing.go:19-21`) already means the registry-managed key is in use.

When anchoring is enabled and the anchor key cannot be read, parsed, or generated, `startAnchorScheduler` logs `warning: audit anchor disabled (signer): <err>`, returns nil, and the registry runs unanchored (`internal/serverboot/audit_anchor.go:79-83`). The comment at `internal/serverboot/serverboot.go:1619-1622` says anchoring uses "the registry-managed signing key", which reads as the registry's own key and is inaccurate. The `loadRegistrySigner` comment (`internal/serverboot/signing.go:103-106`) says the distinct default path is what keeps the keys apart.

The audit sink opens at `internal/serverboot/serverboot.go:960`, after the signing-key refusals and before the §13.4 rewrite (`:966`) and the bootstrap ingest (`:1050`, `:1063`). For a file path, `openAuditSink` logs `warning: audit sink disabled (path)` when `resolveAuditPath` fails, which happens only when the path is empty and the home directory cannot be resolved. It logs `warning: audit sink disabled (open)` when `audit.NewFileSink` cannot create the parent directory or read an existing file. In both cases it returns two nil sinks (`internal/serverboot/audit_anchor.go:45-54`). An `http(s)` value that does not parse returns the same two nils (`:37-43`). The scheduler call site therefore cannot tell an endpoint from a failed file sink. With anchoring enabled it logs `warning: audit anchor disabled (no sink)` (`:75-77`), and the registry runs with no audit sink and no anchoring.

§8.6 recommends anchoring to a public transparency log and says nothing about the local mechanism the registry ships. §13.10 lists transparency-log anchoring as out of scope for standalone. Nothing in `spec/` or `docs/` names either audit variable, and `docs/deployment/single-node.md:187` names `audit.key` without them.

### Precedents

Proposal 0024 (`config.invalid_idp_group_mapping`), `config.runtime_keys_unavailable`, and `config.signature_provider_unavailable` each define a refusal beside its variable in §13.12, refuse under every identity provider, and return an error that names the code. `config.invalid_idp_group_mapping` and `config.signature_provider_unavailable` are §6.10 matrix cells (`tools/matrix/matrices.go:91-98`).

## Decisions

- **D26 is settled.** The proposal specifies `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` in §13.12 and §6.3.1, and an unusable `PODIUM_SCIM_STORE_PATH` refuses startup with `config.scim_store_unavailable` in place of the in-memory fallback.
- **The SCIM refusal scope.** The refusal applies whenever `PODIUM_SCIM_STORE_PATH` is non-empty, under every identity provider, and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. The rule protects persistence, and it follows the "fails startup under every identity provider" wording of `config.invalid_idp_group_mapping` and `config.runtime_keys_unavailable`. The operator who set the path asked for persistence, and under `oidc-jwt` and `injected-session-token` the store also decides `groups:` visibility. Under `trusted-headers`, an unset provider, and an unnamed provider, the justification is data loss alone, because the store feeds no visibility decision there.
- **The definition of "unusable".** It is what `LoadFileStore` detects at boot after this change: (a) a read of the file fails for any reason other than not-exist; (b) the file is non-empty and does not parse as the store's JSON document; (c) the parent directory cannot be created; or (d) a file cannot be created in the parent directory. Conditions (c) and (d) come from a new boot-time writability probe. They are included because a failed `save()` on a create or replace keeps the record in memory while the IdP receives HTTP 400, which loses the same records on restart. A missing file and an empty file stay valid. The probe checks the parent directory and leaves the existing file's mode unchecked, because `save()` replaces the file by renaming `<path>.tmp` over it, which needs only the directory to be writable.
- **The SCIM refusal stays where the store opens.** CODE-2 returns the error at the current call site, as `config.runtime_keys_unavailable` does. The SCIM refusal creates no key file and no row whose fate differs from the store, and a refusal after the bootstrap ingest leaves the store as a corrected restart finds it. The spec states no ordering for it.
- **C30 option (c) is settled.** When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0, the audit sink is an open file sink, and `registrySigningEnabled(cfg.signMode)` is true, startup refuses with `config.audit_anchor_key_shared` if the anchor public key equals the registry signing public key or any `verify:` key in the registry key file. The comparison is `ed25519.PublicKey.Equal`. The error names the matching key's `key_id` (from `sign.KeyIDFor`) and both key paths, and never prints private material.
- **The signed-message format does not change.** Adding a purpose label is option (a) and stays deferred. §8.6 states the missing label as an accepted risk. Once the equality check holds, no second signer in the registry process shares the anchor key, so a cross-purpose signature fails verification under the other purpose's key. The residual case is a verifier outside the registry that trusts one key for both purposes.
- **An unusable anchor key refuses startup.** With anchoring running, a key that cannot be read, parsed, or generated refuses startup with `config.audit_anchor_key_unavailable`. The operator enabled anchoring explicitly, and warn-and-continue leaves the chain unanchored with a log line as the only signal, which is the condition D26 rejects for SCIM. The rule matches `config.signature_provider_unavailable` for the registry key and `config.runtime_keys_unavailable` for the runtime key file. With the interval at 0, the default, the anchor key is never read, so no new refusal reaches a deployment that did not enable anchoring.
- **An unopenable file sink refuses an anchored start (OQ-1, decided 2026-10-02).** When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0, `PODIUM_AUDIT_LOG_PATH` is not an `http(s)` value, and `resolveAuditPath` or `audit.NewFileSink` fails, startup refuses with `config.audit_sink_unavailable`. The operator enabled anchoring explicitly, and a start with no sink leaves nothing to anchor, with a warning as the only signal. The code is sink-specific because the remedy is to fix `PODIUM_AUDIT_LOG_PATH` or its directory rather than the anchor key. The check runs before the anchor key is read, so a sink failure is reported even when the key is also bad, and no key is generated. With an `http(s)` value the aggregator owns integrity by design (`internal/serverboot/audit_anchor.go:28-33`), so anchoring is disabled with a warning and the start proceeds, whether or not the endpoint sink constructs. With the interval at 0, a failed file sink keeps its warning and the registry starts. Refusing that case is a separate decision (Non-goals). The anchor key is loaded only when anchoring would run, so no key is generated for an endpoint-sink deployment.
- **New codes in §13.12, as matrix cells.** `config.scim_store_unavailable`, `config.audit_sink_unavailable`, `config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared` are defined in the §13.12 tables beside their variables, following proposal 0024. Each is added to the §6.10 axis in `tools/matrix/matrices.go` with a `// Matrix: §6.10 (<code>)` end-to-end test. §6.10 lists only namespaces and is not edited. The anchor failures use separate codes because their remedies differ: fix the audit log path, fix or replace the key file, or give the anchor its own key.
- **Environment only.** No configuration-file overlay reads the four variables. Their §13.12 rows say "Environment only; no config-file key", as the `PODIUM_TRUSTED_PROXY_SECRET` row does, because §13.12 otherwise states that every listed variable is a config-file key.
- **Local chain-head anchoring is a named mechanism.** The anchor key Ed25519-signs the chain head, and the result is appended as an `audit.anchored` event. §8.6 keeps it distinct from the transparency-log anchoring it recommends and that §13.10 lists as out of scope for standalone. The existing standalone journey (`test/e2e/audit_signing_journey_test.go`) runs the local form.
- **Exit status.** `podium serve` prints `podium serve: <err>` and returns 1 (`cmd/podium/serve.go:105-108`). `podium-server` calls `log.Fatal` (`cmd/podium-server/main.go:31-33`), which exits 1.

## Spec amendment: §13.12 SCIM variables

**SPEC-1.** `spec/13-deployment.md`, §13.12 "Identity provider", the variable table. Insert two rows immediately after the `PODIUM_IDP_GROUP_MAPPING` row (the last row of the table, which ends "`(unset; no table, and every claim value passes through)`"), before the paragraph that begins "The `identity_provider:` object holds".

> | `PODIUM_SCIM_TOKENS` | Bearer tokens the §6.3.1 SCIM 2.0 receiver accepts, as a comma-separated list. Each entry is trimmed and blank entries are dropped. The registry mounts the receiver at `/scim/v2/` when at least one token remains, and any listed token authorizes every request to the receiver. With no token listed, the registry mounts no receiver. Environment only; no config-file key. Read under every identity provider; the pushed directory feeds §4.6 `groups:` expansion only under the providers §6.3.1 names. | (unset; no receiver) |
> | `PODIUM_SCIM_STORE_PATH` | Path to the JSON file that persists the §6.3.1 SCIM directory across restarts. When the variable is unset, the directory is held in memory and is lost on restart. When the variable is set, the registry creates the parent directory at startup, and it creates the file on the first push. A missing file and an empty file load as an empty directory. A file the registry cannot read for a reason other than its absence, a non-empty file that does not parse as the directory document, a parent directory the registry cannot create, and a parent directory in which the registry cannot create a file each fail startup with `config.scim_store_unavailable`, naming the path. The refusal applies under every identity provider and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. Environment only; no config-file key. | (unset; the directory is held in memory) |

## Spec amendment: §6.3.1 receiver and persistence

**SPEC-2.** `spec/06-mcp-server.md`, §6.3.1 Claim Derivation. Insert a new paragraph immediately after the paragraph that begins "**Where the directory expansion applies.**" and ends "because a directory that can only widen a caller's view is a grant the operator did not make.", before the `### 6.3.2 Runtime Trust Model` heading.

> **Receiver and persistence.** The registry mounts the SCIM 2.0 receiver at `/scim/v2/` when `PODIUM_SCIM_TOKENS` (§13.12) lists at least one bearer token, and it rejects a request that does not present one of the listed tokens as a bearer token with HTTP 401. `PODIUM_SCIM_STORE_PATH` (§13.12) persists the directory across restarts. Without it, the directory is held in memory and is lost on restart. When the variable is set to a store the registry cannot read, parse, or write, startup fails with `config.scim_store_unavailable`.

## Spec amendment: §13.12 Audit anchoring

**SPEC-3.** `spec/13-deployment.md`, §13.12. Insert a new subsection between the end of "### Signing" (its last paragraph begins "The consumer-side settings that decide whether a signature is checked") and the "### Layers" heading.

> ### Audit anchoring
>
> | Var | Description | Default |
> | --- | --- | --- |
> | `PODIUM_AUDIT_LOG_PATH` | The registry's §8.3 audit sink for catalogue events. A value that begins with `http://` or `https://` names an external endpoint that receives the stream. Any other value is a file path, and unset resolves to `audit.log` under `.podium` in the home directory of the registry process. `PODIUM_AUDIT_SINK` is the separate MCP-server local sink and does not set this sink. Environment only; no config-file key. | `~/.podium/audit.log` |
> | `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` | Interval in seconds between §8.6 local chain-head anchors. A value of 0 disables anchoring. A negative or non-integer value is treated as 0, which also disables anchoring. With anchoring disabled, the anchor key is never read. Anchoring runs only when the registry audit sink is a file. With an `http://` or `https://` `PODIUM_AUDIT_LOG_PATH`, the registry logs that anchoring is disabled and starts, whether or not the endpoint sink can be constructed. When anchoring is enabled and `PODIUM_AUDIT_LOG_PATH` is a file path or unset, a file sink the registry cannot open refuses startup with `config.audit_sink_unavailable`, naming the path and the cause. The registry cannot open the file sink when the path is unset and the home directory cannot be resolved, when the file's parent directory cannot be created, or when an existing file at the path cannot be read. The registry checks the sink before it reads the anchor key. With anchoring disabled, a file sink that cannot be opened is logged and the registry starts with no audit sink. Environment only; no config-file key. | `0` (anchoring disabled) |
> | `PODIUM_AUDIT_SIGNING_KEY_PATH` | Path to the §8.6 anchor key file. The file uses the registry key-file format (`PODIUM_SIGN_KEY_PATH`, §4.7.9). Its `private:` line signs the chain head, and its `verify:` lines are ignored, because the anchor key is not rotated through a verification key set. When anchoring runs and the file is absent, the registry generates it. When anchoring runs, a file that cannot be read, a file with no `private:` line or no `public:` line, a repeated `private:` or `public:` line, a line that does not decode, a `public:` line that is not the public half of the `private:` line, and a file the registry cannot generate all refuse startup with `config.audit_anchor_key_unavailable`, naming the key file. When anchoring runs and the signing mode, set by `PODIUM_SIGN` or `podium serve --sign`, resolves to `registry-key`, an anchor public key equal to the `public:` key or to any `verify:` key of the `PODIUM_SIGN_KEY_PATH` file refuses startup with `config.audit_anchor_key_shared`, naming the shared key's `key_id` and both key paths. Environment only; no config-file key. | `~/.podium/standalone/audit.key` |
>
> The anchor key and the registry signing key are distinct keypairs (§8.6). Each refusal in this table runs before the §13.4 first-start rewrite and before the bootstrap ingest.

## Spec amendment: §8.6 local chain-head anchoring

**SPEC-4.** `spec/08-audit-and-observability.md`, §8.6 Audit Integrity. Keep both existing paragraphs unchanged. Insert a new paragraph immediately after the paragraph that begins "Periodic anchoring of the chain head to a public transparency log" and ends "SIEM mirroring is the operational integrity backstop.", at the end of §8.6.

> **Local chain-head anchoring.** When anchoring is enabled (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, §13.12) and the audit sink is a file, the registry periodically signs `sha256:<chain head>` with a dedicated Ed25519 anchor key (`PODIUM_AUDIT_SIGNING_KEY_PATH`, §13.12) and appends the result as an `audit.anchored` event. When anchoring is enabled and the file audit sink cannot be opened, the registry refuses to start (§13.12), because no chain exists to anchor. It anchors again immediately after a §8.4 retention pass moves the head. A failed periodic attempt is recorded as `audit.anchor_failed`, and a failed attempt after a retention pass is logged, and the next periodic anchor signs the current head. Local anchoring submits nothing to a transparency log and is distinct from the transparency-log anchoring recommended above. An auditor verifies an anchor with the anchor key's public half. The anchor signature covers the digest bytes alone, with no label that distinguishes a chain-head signature from a §4.7.9 artifact signature. The anchor key and the registry signing key set must therefore be distinct, and while registry signing is on, the registry refuses to start when they share a key (§13.12). Podium does not add a purpose label. The accepted residual risk is a verifier outside the registry that trusts one key for both purposes.

## Proposed solution

### CODE-1. `pkg/scim`: probe the store directory in `LoadFileStore`

`pkg/scim/file_store.go`. After `out.load()` succeeds, `LoadFileStore` calls a new unexported `probeWritable(dir string) error` with `filepath.Dir(path)`:

```go
// probeWritable confirms that save() can write the store: it creates the
// parent directory and then a temporary file in it. save() replaces the
// store by renaming <path>.tmp over it, so the directory's writability is
// what matters and the existing file's mode is not checked. A remove
// failure leaves a stray probe file and does not make the store unusable,
// so it is logged.
//
// Spec: §6.3.1, §13.12
func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("scim: prepare %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".scim-probe-*")
	if err != nil {
		return fmt.Errorf("scim: probe %s: %w", dir, err)
	}
	name := f.Name()
	// Close and Remove cannot be made to fail in a unit test once CreateTemp
	// has succeeded on a local filesystem; the branches stay for a network
	// filesystem that reports a deferred write error at close.
	if err := f.Close(); err != nil {
		return fmt.Errorf("scim: probe %s: %w", dir, err)
	}
	if err := os.Remove(name); err != nil {
		log.Printf("scim: remove probe file %s: %v", name, err)
	}
	return nil
}
```

Rewrite the `LoadFileStore` doc comment: it reads the file when present, creates the parent directory at load, and creates the file on the first mutation. A missing or empty file loads as an empty store. An unreadable file, a malformed file, and a parent directory that cannot be created or written are errors (`scim: read`, `scim: parse`, `scim: prepare`, and `scim: probe`). Add `// Spec: §6.3.1, §13.12`. `pkg/webhook` keeps its own `LoadFileStore` unchanged (a non-goal).

### CODE-2. `serverboot`: refuse with `config.scim_store_unavailable`

`internal/serverboot/serverboot.go`. Leave the SCIM store block at `:1135-1152`. Extract it into an unexported `openSCIMStore(path string) (scim.Store, error)` in `serverboot.go` (no new code file). It returns `scim.NewMemory()` and nil for an empty path, and otherwise:

```go
fs, err := scim.LoadFileStore(path)
if err != nil {
	return nil, fmt.Errorf("config.scim_store_unavailable: PODIUM_SCIM_STORE_PATH=%q cannot hold the SCIM directory (§6.3.1, §13.12): %w; fix the file or its directory, or unset PODIUM_SCIM_STORE_PATH to keep the directory in memory", path, err)
}
log.Printf("SCIM directory persisted at %s", path)
return fs, nil
```

`Run` calls it as `scimStore, err := openSCIMStore(os.Getenv("PODIUM_SCIM_STORE_PATH"))` and returns the error unwrapped. The `warning: SCIM persistence disabled` line is deleted. Rewrite the comment at `:1135-1142` to say that a set path the registry cannot read, parse, or write refuses the start, and cite `// Spec: §6.3.1, §13.12`. `buildSCIMHandler`, `scimResolvesGroups`, and the resolver wiring are unchanged.

### CODE-3. `serverboot`: load the anchor key early and refuse an unopenable file sink or an unusable or shared key

`internal/serverboot/audit_anchor.go`:

- `openAuditSink(cfg *Config) (audit.Sink, *audit.FileSink, error)`. The endpoint branch is unchanged: on a construction failure it logs `warning: audit sink disabled (endpoint)` and returns `nil, nil, nil`. In the file branch, a `resolveAuditPath` failure returns `nil, nil, fmt.Errorf("audit: resolve default log path ~/.podium/audit.log: %w", err)`, and an `audit.NewFileSink` failure returns `nil, nil, fmt.Errorf("audit: open %s: %w", logPath, err)`. Neither logs. Rewrite the doc comment's last sentence: the error is non-nil only for a file-path value that cannot be opened, the caller decides between refusing and warning, and an endpoint failure is logged here and returns a nil error. Add `// Spec: §8.3, §8.6, §13.12`.
- `loadOrGenerateAuditSigner(path string) (sign.RegistryManagedKey, string, error)` returns the key and the resolved path (the default `~/.podium/standalone/audit.key` when `path` is empty).
- New `loadAnchorSigner(cfg *Config, auditFile *audit.FileSink, sinkErr error, registryKey sign.RegistryManagedKey, signingOn bool) (sign.RegistryManagedKey, bool, error)`, annotated `// Spec: §8.6, §13.12`:
  1. `cfg.auditAnchorInterval <= 0`: return the zero key, false, nil. Nothing is read.
  2. `sinkErr != nil`: return `fmt.Errorf("config.audit_sink_unavailable: PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS is %d, and the audit log file named by PODIUM_AUDIT_LOG_PATH cannot be opened, so there is no chain to anchor (§8.6, §13.12): %w", cfg.auditAnchorInterval, sinkErr)`. The anchor key is not read.
  3. `auditFile == nil`, which is now reached only for an `http(s)` value: log `warning: audit anchor disabled (no sink)` and return the zero key, false, nil.
  4. Load the key with `loadOrGenerateAuditSigner(cfg.auditSigningKeyPath)`. On error, return `fmt.Errorf("config.audit_anchor_key_unavailable: PODIUM_AUDIT_SIGNING_KEY_PATH resolves to %s, which cannot be read, parsed, or generated as the audit anchor key (§8.6, §13.12): %w", path, err)`. When the path itself cannot be resolved, the message names `PODIUM_AUDIT_SIGNING_KEY_PATH` and the resolution error. The cause is never `sign.ErrRegistryManagedUnavailable`.
  5. When `signingOn`, call `refuseSharedAnchorKey`.
  6. Return the key, true, nil.
- New `refuseSharedAnchorKey(anchor sign.RegistryManagedKey, anchorPath string, registryKey sign.RegistryManagedKey, registryPath string) error`, annotated `// Spec: §8.6, §13.12`. It compares `anchor.PublicKey` with `registryKey.PublicKey` and then with each key in `registryKey.Trusted`, using `ed25519.PublicKey.Equal`. On a match it returns `config.audit_anchor_key_shared: the audit anchor key at <anchorPath> (PODIUM_AUDIT_SIGNING_KEY_PATH) has key_id <id>, which the registry signing key file at <registryPath> (PODIUM_SIGN_KEY_PATH) carries as its <signing|verify:> key; the anchor signature carries no purpose label, so generate a separate keypair for PODIUM_AUDIT_SIGNING_KEY_PATH (§8.6, §13.12)`. The message carries no key material beyond the `key_id`.
- `startAnchorScheduler(ctx context.Context, cfg *Config, sink *audit.FileSink, signer sign.Provider)` takes the loaded signer and no longer loads a key or checks the sink. Its doc comment drops the key-path description.

`internal/serverboot/serverboot.go`:

- Change `auditSink, auditFile := openAuditSink(cfg)` to `auditSink, auditFile, sinkErr := openAuditSink(cfg)` and, between it and the scrubber (`:960`) and the §13.4 rewrite block (`:966`), add:

  ```go
  anchorKey, anchoringOn, err := loadAnchorSigner(cfg, auditFile, sinkErr, signKey, signingOn)
  if err != nil {
  	return err
  }
  // Reached with a sink error only when anchoring is off (§13.12).
  if sinkErr != nil {
  	log.Printf("warning: audit sink disabled: %v", sinkErr)
  }
  ```

  `registryPath` inside `loadAnchorSigner` resolves through `registrySigningKeyPath(os.Getenv("PODIUM_SIGN_KEY_PATH"))`. The call runs whatever the bind outcome and ahead of the bind-error gate.
- At `:1619-1636`, replace `if cfg.auditAnchorInterval > 0 { if signer := startAnchorScheduler(...) ...` with `if anchoringOn { startAnchorScheduler(ctx, cfg, auditFile, anchorKey); reAnchor = ... }`, where `reAnchor` signs with `anchorKey` and keeps its `log.Printf` failure handling (`internal/serverboot/serverboot.go:1629-1633`), which SPEC-4 states: only the periodic `Scheduler` records `audit.anchor_failed`, through `notifyFailure` (`pkg/audit/scheduler.go:58-68`). Rewrite the comment: "§8.6 local chain-head anchoring: with PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS above 0 and a file sink, a goroutine signs the chain head with the dedicated anchor key at PODIUM_AUDIT_SIGNING_KEY_PATH, loaded and checked above, and appends audit.anchored. Operators monitor audit.anchored and audit.anchor_failed."

`internal/serverboot/signpass.go:190`: `sink, _, sinkErr := openAuditSink(cfg)`, followed by `if sinkErr != nil { log.Printf("warning: audit sink disabled: %v", sinkErr) }`. `sign-stored-rows` does not anchor, so its behavior is unchanged. The sink open needs no move, because `openAuditSink` already runs before the §13.4 rewrite (`internal/serverboot/serverboot.go:966`) and the bootstrap ingest.

`internal/serverboot/signing.go`: rewrite the last two sentences of the `loadRegistrySigner` doc comment (`:103-106`) to say that `loadAnchorSigner` refuses startup with `config.audit_anchor_key_shared` when the anchor key equals the registry public key or any `verify:` key. Delete the claim that the distinct default path keeps the keys apart.

### CODE-4. Matrix cells

`tools/matrix/matrices.go`, §6.10 axis. Insert `"config.scim_store_unavailable"`, `"config.audit_sink_unavailable"`, `"config.audit_anchor_key_unavailable"`, and `"config.audit_anchor_key_shared"` immediately after `"config.signature_provider_unavailable"`.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `PODIUM_SCIM_STORE_PATH` unset | Starts; the directory is held in memory and lost on restart | §13.12 `PODIUM_SCIM_STORE_PATH` (SPEC-1), §6.3.1 (SPEC-2); `docs/reference/cli.md` (DOC-1) |
| Set path, file missing, parent directory exists | Starts with an empty directory; the file is created on the first push | §13.12 (SPEC-1); `docs/reference/cli.md` (DOC-1) |
| Set path, parent directory missing and creatable | The directory is created at startup; starts empty | §13.12 "creates the parent directory at startup" (SPEC-1); `docs/reference/cli.md` (DOC-1) |
| Set path, empty file | Starts with an empty directory | §13.12 (SPEC-1); `docs/reference/cli.md` (DOC-1) |
| Set path, malformed file | Exit 1 with `config.scim_store_unavailable` naming the path | §13.12 (SPEC-1); `docs/reference/error-codes.md` (DOC-1), `docs/deployment/operator-guide.md` (DOC-3) |
| Set path names a directory, or a parent component is a regular file | Exit 1 with `config.scim_store_unavailable` (`scim: read`) | §13.12 (SPEC-1); `docs/reference/error-codes.md` (DOC-1) |
| Parent directory cannot be created (for example, a dangling symlink component) | Exit 1 with `config.scim_store_unavailable` (`scim: prepare`) | §13.12 (SPEC-1); `docs/reference/error-codes.md` (DOC-1) |
| Parent directory exists and is not writable | Exit 1 with `config.scim_store_unavailable` (`scim: probe`) | §13.12 (SPEC-1); `docs/reference/error-codes.md` (DOC-1) |
| Existing store file is read-only, parent directory is writable | Starts; `save()` replaces the file by rename | §13.12 lists the parent directory as the write condition (SPEC-1); no further sentence |
| Bad path with `PODIUM_SCIM_TOKENS` unset, or under `trusted-headers` | Exit 1 with `config.scim_store_unavailable` | §13.12 "whether or not `PODIUM_SCIM_TOKENS` mounts the receiver" and "under every identity provider" (SPEC-1); `docs/reference/cli.md` (DOC-1) |
| SCIM refusal ordering | The refusal runs after the bootstrap ingest, so a refused start may have ingested the bootstrap layer; a corrected restart finds the same store state (accepted) | No ordering is specified (SPEC-1 states none); not documented |
| The store becomes unwritable after startup | A create or replace push is answered with HTTP 400 `invalidValue`, and its record is served until restart and then lost. A delete push is answered with HTTP 500, and the deleted record returns on restart (accepted and deferred) | No spec sentence, because stating the in-memory hold would sanction it (Non-goals); `docs/deployment/operator-guide.md` states both statuses and the full-push remedy (DOC-3) |
| Replicas share one store path | Pre-existing lost-update race between replicas; the probe adds no collision because it uses a unique temporary name (accepted) | Not specified; outside this proposal |
| The probe file cannot be removed | Logged; starts | Not specified; the stray file has no observable effect on the directory |
| Anchor interval 0 (default) | Starts; the anchor key is never read, a malformed anchor file is untouched, and no `audit.key` is created | §13.12 `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Anchor interval negative or non-integer (`60s`) | Treated as 0; anchoring disabled with no message | §13.12 (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Interval above 0 with an `http(s)` `PODIUM_AUDIT_LOG_PATH` | Warning; starts unanchored; the anchor key is not read | §13.12 (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Interval above 0, and the file sink path is a directory, has a regular-file parent, or names an unreadable existing file | Exit 1 with `config.audit_sink_unavailable` naming the path; the anchor key is not read or generated | §13.12 (SPEC-3), §8.6 (SPEC-4); `docs/reference/error-codes.md` (DOC-1), `docs/deployment/operator-guide.md` (DOC-3) |
| Interval above 0, `PODIUM_AUDIT_LOG_PATH` unset, home directory unresolvable | Exit 1 with `config.audit_sink_unavailable` | §13.12 (SPEC-3); `TestOpenAuditSink_UnresolvableHomeReturnsError` (TEST-2) |
| Interval above 0, unopenable file sink, and a malformed anchor key | Exit 1 with `config.audit_sink_unavailable`; the key is checked on the next start | §13.12 "checks the sink before it reads the anchor key" (SPEC-3) |
| Interval 0 and an unopenable file sink | Warning `audit sink disabled`; starts with no audit sink (unchanged) | §13.12 "With anchoring disabled" (SPEC-3); `docs/reference/cli.md` (DOC-1) |
| Interval above 0 and an `http(s)` value that does not parse | Warning; starts with no audit sink and no anchoring (unchanged) | §13.12 "whether or not the endpoint sink can be constructed" (SPEC-3) |
| Interval above 0 and a file sink that opens but cannot be written | Starts; the first `Append` fails (accepted) | No spec sentence; Non-goals |
| Interval above 0, anchor key file absent | Generated; anchoring runs | §13.12 (SPEC-3); `docs/deployment/single-node.md` (DOC-2) |
| Interval above 0, anchor key file malformed or public-only | Exit 1 with `config.audit_anchor_key_unavailable` naming the file | §13.12 (SPEC-3); `docs/reference/error-codes.md` (DOC-1), `docs/deployment/operator-guide.md` (DOC-3) |
| Anchor key equals the registry `public:` key, signing on | Exit 1 with `config.audit_anchor_key_shared` naming the `key_id` and both paths | §13.12 (SPEC-3), §8.6 (SPEC-4); `docs/reference/error-codes.md` (DOC-1), `docs/deployment/single-node.md` (DOC-2), `docs/deployment/operator-guide.md` (DOC-3) |
| Anchor key equals a registry `verify:` key, signing on | Exit 1 with `config.audit_anchor_key_shared` | §13.12 (SPEC-3); `docs/reference/error-codes.md` (DOC-1) |
| Shared key with `PODIUM_SIGN=none` | Starts; the registry key file is not read. A key file kept from an earlier signing period can still verify old artifact signatures under the same key (accepted) | §8.6 "while registry signing is on" and the residual-risk sentence (SPEC-4); `docs/reference/error-codes.md` (DOC-1) |
| Anchor key trusted by a consumer for artifacts through `PODIUM_SIGNATURE_VERIFY_KEY` but absent from the registry key file | Not detected (accepted) | §8.6 "a verifier outside the registry that trusts one key for both purposes" (SPEC-4); `docs/deployment/operator-guide.md` (DOC-3) |
| `verify:` lines in the anchor key file | Ignored, including one equal to the registry key | §13.12 "its `verify:` lines are ignored" (SPEC-3) |
| A refused anchor start | No rewrite, no ingest, no listener. The default registry signing key may already be generated and the audit log's parent directory created (accepted) | §13.12 "Each refusal in this table runs before the §13.4 first-start rewrite and before the bootstrap ingest" (SPEC-3) |
| Upgrade: a deployment with a malformed SCIM store, an unwritable store directory, an anchored deployment whose file audit sink cannot be opened, a malformed anchor key, or a shared anchor key | Stops booting; the pre-1.0 MINOR policy permits the break | `CHANGELOG.md` (CL-1) |

## Testing

**TEST-1 · unit, `pkg/scim/file_store_test.go`.** Lands in S6. `TestLoadFileStore_UsableAndUnusable`, annotated `// Spec: §6.3.1, §13.12`. Each clause of the "unusable" definition and each valid-empty case has a case. The cases use type conflicts and dangling symlinks where those trigger the failure, and the case that needs permission bits skips when `os.Geteuid() == 0`.

- A missing file in an existing directory loads with no users.
- A missing parent directory is created and the store loads. Assert the directory exists afterwards.
- An empty file loads with no users.
- Malformed JSON (`{not json`) returns an error containing `scim: parse` (clause b).
- A path that is a directory returns `scim: read` (clause a).
- A regular-file parent component returns `scim: read` with `errors.Is(err, syscall.ENOTDIR)` (clause a).
- A dangling-symlink parent component (`os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "link"))`, path `link/scim.json`) returns `scim: prepare` (clause c). The case does not depend on permission bits, so it also runs as root.
- An existing directory at mode `0o500` returns `scim: probe` (clause d). Skips under root and restores the mode in `t.Cleanup`.
- After a successful load, no `.scim-probe-*` file remains. After a load that created the directory, `CreateUser` persists and a second `LoadFileStore` sees the record, which shows the probe did not disturb `save()`.

**TEST-2 · unit and integration, `internal/serverboot`.** Lands in S8 with CODE-3. Targets `internal/serverboot/scim_store_test.go` (new), `internal/serverboot/audit_anchor_test.go`, `internal/serverboot/schedulers_test.go`, `internal/serverboot/coverage_gaps_test.go`, and `internal/serverboot/rehash_boot_test.go`.

- `TestOpenAuditSink_FileFailureReturnsError`, annotated `// Spec: §8.6, §13.12`, table-driven over a path that is an existing directory and a path under a regular file. Each returns nil, nil, and an error that contains the path. Update `TestOpenAuditSink_WithExplicitPath`, `TestOpenAuditSink_EndpointRedirect`, and `TestOpenAuditSink_BadEndpointDisables` (`internal/serverboot/schedulers_test.go:181-224`) to the three return values, and assert that the error is nil in each.
- `TestOpenAuditSink_UnresolvableHomeReturnsError`, annotated `// Spec: §8.6, §13.12`, in `internal/serverboot/schedulers_test.go`. It does not call `t.Parallel`, because it calls `t.Setenv("HOME", "")` and `t.Setenv("USERPROFILE", "")`, following `TestRefuseUnpersistedSigningKey_UnresolvableHome` (`internal/serverboot/signing_test.go:80-89`). With an empty `auditLogPath`, `openAuditSink` returns nil, nil, and an error that contains `~/.podium/audit.log`. The test pins the `resolveAuditPath` branch, which `TestResolveAuditPath_ExpandsHome` (`internal/serverboot/schedulers_test.go:167-179`) covers only on the success path.
- `TestOpenSCIMStore`, annotated `// Spec: §6.3.1, §13.12`: an empty path returns an in-memory store; a valid path returns a file store; a malformed file returns an error whose text starts with `config.scim_store_unavailable` and contains the path.
- Update every `loadOrGenerateAuditSigner` caller (`coverage_gaps_test.go:358-371`, `:419`; `audit_anchor_test.go:25`, `:33`, `:114`) to the new return. Drop the type assertions. Assert that the returned path equals the requested path, or the resolved `~/.podium/standalone/audit.key` default when the path is empty.
- Move `TestStartAnchorScheduler_NilSinkLogsAndReturns` (`schedulers_test.go:12-17`) to a `loadAnchorSigner` case: interval above 0 with a nil sink and a nil sink error (the endpoint case) returns the zero key, false, and nil. Change `TestStartAnchorScheduler_BadKeyPathLogsAndReturns` (`schedulers_test.go:19-36`) to pass a signer loaded from the temporary key file.
- `TestLoadAnchorSigner`, table-driven, annotated `// Spec: §8.6, §13.12`:
  - Interval above 0 with a non-nil sink error returns an error starting with `config.audit_sink_unavailable`, `errors.Is(err, sinkErr)` is true, and an absent key path stays absent. Interval 0 with the same sink error returns false and nil.
  - Interval 0 with the key path under a regular-file parent returns false and nil, and interval 0 with a non-existent path returns false and nil and creates no file at that path.
  - Interval above 0, a file sink, and an absent key returns true and creates the file.
  - A malformed file returns an error starting with `config.audit_anchor_key_unavailable` that names the path, and `errors.Is(err, sign.ErrRegistryManagedUnavailable)` is false.
  - A public-only file returns `config.audit_anchor_key_unavailable`.
  - A shared key with `signingOn` false returns true and nil.
  - A shared key with `signingOn` true returns `config.audit_anchor_key_shared`.
- `TestRefuseSharedAnchorKey`, annotated `// Spec: §8.6, §13.12`: equal to the registry public key refuses and names `signing`; equal to the second of two `verify:` keys refuses and names `verify:`; distinct from all keys returns nil; the error contains `sign.KeyIDFor` of the shared key and both paths, and does not contain the base64 private key.
- `TestRun_AnchorKeyRefusalPrecedesTheRewrite`, integration level in `internal/serverboot/rehash_boot_test.go` beside `TestRun_RefusesToStrandAStoredSignature` (`:282-319`), annotated `// Spec: §8.6, §13.12, §13.4`. It pins the SPEC-3 sentence "Each refusal in this table runs before the §13.4 first-start rewrite and before the bootstrap ingest." Three subtests, each with its own `newBootFixture` (`:37`): `shared`, which sets `PODIUM_AUDIT_SIGNING_KEY_PATH` to `f.keyPath`; `unavailable`, which sets it to a file in `f.home` holding only a `public:` line; and `sink`, which sets `PODIUM_AUDIT_LOG_PATH` (set by the fixture at `rehash_boot_test.go:61`) to a directory created in `f.home` and `PODIUM_AUDIT_SIGNING_KEY_PATH` to an absent path in `f.home` before the second boot. Each subtest sets `PODIUM_SIGN=registry-key` and `PODIUM_SIGN_KEY_PATH=f.keyPath`, boots once with `f.boot`, opens the store with `f.openStoreDirect`, downgrades the rows with `downgradeRows(t, st, f.keyPath)` (`:157`), which also clears the completion record, and closes the store. It then sets `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60` and the subtest's anchor path and boots again. Assert that the error starts with `config.audit_anchor_key_shared`, `config.audit_anchor_key_unavailable`, or `config.audit_sink_unavailable` respectively, that the `sink` subtest's anchor key path does not exist, that every seeded row keeps its downgraded `ContentHash` and `Signature`, that `DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)` stays false, and that the returned logs contain no `ingested layer ` line (`internal/serverboot/serverboot.go:551`). Registry signing is on so that the rewrite would run if the refusal sat after it: a refusal placed at the scheduler call site (`internal/serverboot/serverboot.go:1624-1625`) or after the bind-error gate rewrites the rows, sets the marker, and logs the ingest, and the test fails.

**TEST-3 · e2e, `test/e2e/scim_store_refusal_test.go` (new).** Lands in S9.

- Change `gwExpectStartupFailure` (`test/e2e/auth_gateway_test.go:155`) to return the combined output as a `string`. Existing callers discard it and compile unchanged.
- `TestSCIMStore_UnusablePathRefusesStart`, annotated `// Spec: §6.3.1, §13.12` and `// Matrix: §6.10 (config.scim_store_unavailable)`, table-driven over two cases: (a) the file holds `{not json` and `PODIUM_SCIM_TOKENS=tok`; (b) the path names an existing directory and `PODIUM_SCIM_TOKENS` is unset, which pins that the refusal fires without the receiver. Each case calls `gwExpectStartupFailure(t, "config.scim_store_unavailable", ...)` and asserts the returned output contains the path.
- Extend `TestAuth_SCIMStorePersistsAcrossRestart` (`test/e2e/auth_oidc_test.go:1099`) so `storePath` is `filepath.Join(t.TempDir(), "nested", "scim.json")`, a parent that does not exist yet. Add `// Spec: §6.3.1, §13.12` to its comment. The test then covers the negative control and the CODE-1 directory creation.

**TEST-4 · e2e, `test/e2e/audit_anchor_key_refusal_test.go` (new).** Lands in S10. Every case sets `PODIUM_AUDIT_LOG_PATH` and the key paths inside a `t.TempDir()` it owns.

- `TestAuditAnchor_SharedKeyRefusesStart`, annotated `// Spec: §8.6, §13.12` and `// Matrix: §6.10 (config.audit_anchor_key_shared)`, with two subtests. In the first, one keypair written with `sign.WriteKeyFile` is named by both `PODIUM_SIGN_KEY_PATH` and `PODIUM_AUDIT_SIGNING_KEY_PATH`, with `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60`. In the second, the registry key file holds its own keypair and a `verify:` line equal to the anchor public key. Each asserts through `gwExpectStartupFailure` that the output names the code and `sign.KeyIDFor` of the shared key, and does not contain the base64 private key.
- `TestAuditAnchor_UnusableKeyRefusesStart`, annotated `// Spec: §8.6, §13.12` and `// Matrix: §6.10 (config.audit_anchor_key_unavailable)`: a public-only anchor key file written with `sign.WriteKeyFile`, interval 60, expects the code and the key path.
- `TestAuditAnchor_UnopenableSinkRefusesStart`, annotated `// Spec: §8.6, §13.12` and `// Matrix: §6.10 (config.audit_sink_unavailable)`: `PODIUM_AUDIT_LOG_PATH` names a directory created with `t.TempDir()`, `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60`, and `PODIUM_AUDIT_SIGNING_KEY_PATH` names an absent file. It expects the code and the directory path through `gwExpectStartupFailure`, and asserts that the anchor key file does not exist afterwards.
- Every refusal case asserts that the output does not contain `listening on`. The anchor-key cases also assert that the audit log at `PODIUM_AUDIT_LOG_PATH` is absent or holds no `audit.anchored` event, and the sink case asserts that the directory named by `PODIUM_AUDIT_LOG_PATH` is still empty. These assertions confirm the refused start serves nothing. They do not pin the SPEC-3 ordering, because the boot-time ingest writes no audit event (`bootstrapLayerPath` and `bootstrapDeclaredLayers` take no audit sink, `internal/serverboot/serverboot.go:464`, `:632`) and a fresh store gives the §13.4 rewrite no rows. `TestRun_AnchorKeyRefusalPrecedesTheRewrite` in TEST-2 pins the ordering.
- Negative controls through `startServerArgs` with an explicit `HOME`, each annotated `// Spec: §8.6, §13.12`: the shared key with `PODIUM_SIGN=none` starts and `/healthz` answers; the public-only anchor key with the interval at 0 starts and the key file is byte-identical afterwards; a fresh `HOME` with the interval at 0 starts and `<HOME>/.podium/standalone/audit.key` does not exist; a directory `PODIUM_AUDIT_LOG_PATH` with the interval at 0 starts, `/healthz` answers, and the output contains `warning: audit sink disabled`; and an `http://` `PODIUM_AUDIT_LOG_PATH` pointing at an `httptest` recorder with the interval at 60 starts, `/healthz` answers, and the output contains `audit anchor disabled (no sink)`.
- `test/e2e/audit_signing_journey_test.go`: the assertions stay unchanged. Reword the header Spec comment (lines 28-30) from "transparency anchoring via the registry-managed key" to "local chain-head anchoring with the dedicated anchor key at PODIUM_AUDIT_SIGNING_KEY_PATH".

Run pattern: `go test ./pkg/scim/ ./internal/serverboot/` and `go test ./test/e2e/ -run 'SCIMStore|SCIMStorePersists|AuditAnchor|AuditSigningJourney|Gateway_'`. Measure subprocess coverage with `GOCOVERDIR` per `.claude/rules/test-coverage.md`, and confirm the e2e runs report no SKIP on the target platform.

## Manual validation

**MV-1.** Add scenario S79 to `test/manual-validation.md` after the last scenario, and add the index row `| S79 | The registry refuses an unusable SCIM store, audit sink, or audit anchor key | standalone | none | none | none |`. Lands in S16.

~~~~markdown
## S79: The registry refuses an unusable SCIM store, audit sink, or audit anchor key

**Goal.** Validate that `podium serve` refuses to start when a set
`PODIUM_SCIM_STORE_PATH` cannot hold the SCIM directory, when an enabled audit
anchor's file sink cannot be opened or its key cannot be loaded, and when the
anchor key is also the registry signing key, and that a deployment with
anchoring disabled never reads the anchor key.

**Covers.** The `config.scim_store_unavailable`, `config.audit_sink_unavailable`,
`config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared`
startup refusals, the refusal without `PODIUM_SCIM_TOKENS`, and the
anchoring-disabled negative control (§6.3.1, §8.6, §13.12).

**Why by hand.** The end-to-end suite asserts the code in the output. It does
not show the operator's terminal: that the message names the path or the
`key_id` an operator has to act on, that it prints no private key, and that
`$?` is 1 for a supervisor to gate on.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block, then scaffold a one-artifact layer the server can
   load.

   ```bash
   mkdir -p "$WORK/reg/seed"
   podium artifact scaffold --type context --description "seed" --force "$WORK/reg/seed"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and
   `$WORK/reg/seed/ARTIFACT.md` exists.

2. A malformed SCIM store file is refused with `PODIUM_SCIM_TOKENS` unset. This
   runs in the foreground and exits immediately.

   ```bash
   printf '{' > "$WORK/scim.json"
   PODIUM_SCIM_STORE_PATH="$WORK/scim.json" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.scim_store_unavailable`
   and `$WORK/scim.json`. No `listening on` line appears.

3. A SCIM store path that names a directory is refused.

   ```bash
   PODIUM_SCIM_STORE_PATH="$WORK" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.scim_store_unavailable`.

4. An anchor key that is the registry signing key is refused. The isolation
   block already exports `PODIUM_SIGN_KEY_PATH`; the first command generates
   the registry key by starting and stopping the server once.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146 > "$WORK/srv.log" 2>&1 &
   PID=$!; server_alive "$PID" "$WORK/srv.log"; kill "$PID"; wait "$PID" 2>/dev/null
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_SIGNING_KEY_PATH="$PODIUM_SIGN_KEY_PATH" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains `config.audit_anchor_key_shared`,
   a 16-character hexadecimal `key_id`, and both key paths. The output does not
   contain the `private:` value from `$PODIUM_SIGN_KEY_PATH`.

5. An anchor key file with no `private:` line is refused while anchoring is
   enabled.

   ```bash
   grep '^public:' "$PODIUM_SIGN_KEY_PATH" > "$WORK/anchor-public-only.key"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-public-only.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"
   ```

   **Expect.** `exit=1`, and the output contains
   `config.audit_anchor_key_unavailable` and `$WORK/anchor-public-only.key`.

6. The same key file with anchoring disabled starts, and the file is untouched.

   ```bash
   shasum "$WORK/anchor-public-only.key" > "$WORK/before.sum"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=0 PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-public-only.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146 > "$WORK/srv.log" 2>&1 &
   PID=$!; server_alive "$PID" "$WORK/srv.log"
   curl -fsS --retry 40 --retry-connrefused --retry-delay 1 http://127.0.0.1:8146/healthz; echo
   kill "$PID"; wait "$PID" 2>/dev/null
   shasum -c "$WORK/before.sum"
   ```

   **Expect.** `/healthz` answers, `srv.log` contains no
   `config.audit_anchor_key_unavailable`, and `shasum -c` prints `OK`.

7. An audit log path that names a directory is refused while anchoring is enabled.

   ```bash
   mkdir -p "$WORK/auditdir"
   PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS=60 PODIUM_AUDIT_LOG_PATH="$WORK/auditdir" PODIUM_AUDIT_SIGNING_KEY_PATH="$WORK/anchor-new.key" \
     podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8146
   echo "exit=$?"; ls "$WORK/anchor-new.key"
   ```

   **Expect.** `exit=1`, the output contains `config.audit_sink_unavailable` and
   `$WORK/auditdir`, and `ls` reports that `anchor-new.key` does not exist.

**Expected.**

- Steps 2 and 3 exit 1 with `config.scim_store_unavailable`. A wrong build
  prints `warning: SCIM persistence disabled` and starts.
- Step 4 exits 1 with `config.audit_anchor_key_shared` and a `key_id`. A wrong
  build starts and anchors with the registry key.
- Step 5 exits 1 with `config.audit_anchor_key_unavailable`. A wrong build
  prints `warning: audit anchor disabled (signer)` and starts unanchored.
- Step 6 starts and leaves the key file unchanged. A wrong build refuses or
  rewrites the file.
- Step 7 exits 1 with `config.audit_sink_unavailable`. A wrong build prints
  `warning: audit sink disabled` and starts unanchored.

**Cleanup.** `rm -rf "$WORK"`.
~~~~

## Documentation changes

**DOC-1 · `docs/reference/cli.md` and `docs/reference/error-codes.md`.** Lands in S12. Copy the condition wording from the final SPEC-1 and SPEC-3 text, because no tool checks `error-codes.md` against the code.

(a) `docs/reference/cli.md`, "## Environment variables" table. After the `PODIUM_RUNTIME_KEYS_PATH` row, add:

> | `PODIUM_SCIM_TOKENS` | Registry-process boot setting, environment only and no config-file key. Comma-separated list of the bearer tokens the SCIM 2.0 receiver at `/scim/v2/` accepts. Each entry is trimmed and blank entries are dropped. With no token listed, the registry mounts no receiver. Whether the pushed directory feeds layer `groups:` filters depends on the identity provider, as [OIDC identity](../deployment/oidc/) states. |
> | `PODIUM_SCIM_STORE_PATH` | Registry-process boot setting, environment only and no config-file key. Path to the JSON file that persists the SCIM directory across restarts; unset, the directory is held in memory and lost on restart. The registry creates the parent directory at startup and the file on the first push. A missing or empty file loads as an empty directory. A file the registry cannot read for a reason other than its absence, a non-empty file that does not parse, a parent directory it cannot create, and a parent directory in which it cannot create a file abort startup with `config.scim_store_unavailable`, under every identity provider and whether or not the receiver is mounted. |
> | `PODIUM_AUDIT_LOG_PATH` | Registry-process boot setting, environment only and no config-file key. The registry audit sink: a file path, or an `http://` or `https://` endpoint. Unset, the registry writes `~/.podium/audit.log`. `PODIUM_AUDIT_SINK` is the separate `podium-mcp` local sink and has no effect on the registry. A file path whose parent directory cannot be created, or whose existing file cannot be read, leaves the registry with no audit sink and a startup warning, or aborts startup with `config.audit_sink_unavailable` when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0. |
> | `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` | Registry-process boot setting, environment only and no config-file key. Interval in seconds between local chain-head anchors. `0`, the default, disables anchoring, and a negative or non-integer value is treated as `0`. Anchoring runs only when the registry audit sink named by `PODIUM_AUDIT_LOG_PATH` is a file path or unset. An `http(s)` value disables anchoring with a startup warning. A file sink the registry cannot open aborts startup with `config.audit_sink_unavailable` while anchoring is enabled, and is logged as a warning while it is disabled. `PODIUM_AUDIT_SINK` does not affect anchoring. |
> | `PODIUM_AUDIT_SIGNING_KEY_PATH` | Registry-process boot setting, environment only and no config-file key. Path to the anchor key file, default `~/.podium/standalone/audit.key`, generated when anchoring runs and the file is absent. Read only when anchoring runs. A file that cannot be read, parsed, or generated aborts startup with `config.audit_anchor_key_unavailable`. While registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`), an anchor key that the `PODIUM_SIGN_KEY_PATH` file also carries as its `public:` key or a `verify:` key aborts startup with `config.audit_anchor_key_shared`. |

(b) `docs/reference/error-codes.md`, "### config.*" table. After the `config.signature_provider_unavailable` row, add:

> | `config.scim_store_unavailable` | `PODIUM_SCIM_STORE_PATH` is non-empty and names a store the registry cannot use: a file it cannot read for a reason other than its absence, a non-empty file that does not parse as the directory document, a parent directory it cannot create, or a parent directory in which it cannot create a file. Raised under every identity provider and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. The message names the path and the cause. Fix the file or its directory, or unset the variable to keep the directory in memory. |
> | `config.audit_sink_unavailable` | `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0, `PODIUM_AUDIT_LOG_PATH` is a file path or unset, and the registry cannot open the file sink: the path is unset and the home directory cannot be resolved, the file's parent directory cannot be created, or an existing file at the path cannot be read. The message names the path and the cause. Fix the path or its directory, or set the interval to 0 to start without anchoring. |
> | `config.audit_anchor_key_unavailable` | `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0, the registry audit sink is a file that opens, and the anchor key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` cannot be read, carries no `private:` or no `public:` line, repeats either line, carries a line that does not decode or a `public:` line that is not the public half of the `private:` line, or cannot be generated. The message names the key file. Restore the file, or move it aside so the registry generates a new keypair. |
> | `config.audit_anchor_key_shared` | Under the same anchoring conditions and while registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`), the anchor public key equals the `public:` key or a `verify:` key of the `PODIUM_SIGN_KEY_PATH` file. The message names the shared `key_id` and both key paths. Generate a separate keypair for `PODIUM_AUDIT_SIGNING_KEY_PATH`. |

**DOC-2 · deployment pages.** Lands in S13.

(a) `docs/deployment/oidc/index.md:59`, `docs/deployment/oidc/okta.md:106`, `docs/deployment/oidc/entra-id.md:144`, and `docs/deployment/oidc/keycloak.md:118`. Directly after the sentence "Set `PODIUM_SCIM_STORE_PATH` to a writable file path so the pushed directory survives a restart.", add:

> A set path that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable`.

On `oidc/index.md` only, link the code to `../../reference/error-codes#config`.

(b) `docs/deployment/gateway-delegated-identity.md:57`. Replace the sentence ending "and `PODIUM_SCIM_STORE_PATH` persists the pushed directory across restarts." so that it ends:

> and `PODIUM_SCIM_STORE_PATH` persists the pushed directory across restarts. A set path that the registry cannot read, parse, or write refuses startup with `config.scim_store_unavailable`.

Leave line 85 unchanged.

(c) `docs/deployment/single-node.md:187`, the "**Backup.**" bullet. Replace "and `audit.key` when audit anchoring is enabled." with:

> and `audit.key` when audit anchoring is enabled. `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` enables anchoring, and `PODIUM_AUDIT_SIGNING_KEY_PATH` moves `audit.key`. Keep the anchor keypair distinct from the registry signing key at `PODIUM_SIGN_KEY_PATH`. While anchoring is enabled and signing is on, a shared key refuses startup with `config.audit_anchor_key_shared`. Include the file named by `PODIUM_SCIM_STORE_PATH` when SCIM persistence is configured.

(d) `docs/deployment/clustered.md:33`. Replace the sentence "Anchoring a chain head to a public transparency log applies to a replica that keeps the on-disk sink, because the anchor and verify passes walk the file." with:

> Local chain-head anchoring (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`) runs only on a replica that keeps the on-disk sink, because the anchor and verify passes walk the file. It signs the chain head with the replica's anchor key and submits nothing to a public transparency log.

Leave `docs/deployment/index.md:15` and `docs/deployment/local.md:162` unchanged; whether a clustered deployment offers transparency-log anchoring is outside this proposal.

**DOC-3 · `docs/deployment/operator-guide.md`.** Lands in S14. Add a subsection "### SCIM and audit-anchor startup refusals" after "### Signing failures" and before the `---` that precedes "## Public-mode misconfiguration":

> - **The registry refuses to start with `config.scim_store_unavailable`.** The error names `PODIUM_SCIM_STORE_PATH` and the cause: a non-empty file that does not parse, a path that is a directory or has a non-directory parent, or a parent directory that cannot be created or written. Fix the permissions, or point the variable at a valid path. For a corrupt file, move it aside and start the registry, which loads a missing file as an empty directory, and then trigger a full push from the IdP's provisioning console to repopulate users and groups, as the SCIM lag entry describes. Unsetting the variable keeps the directory in memory, so it is lost on every restart. When the store becomes unwritable after startup, the receiver answers a create or replace push with HTTP 400 `invalidValue`, and that record is served until restart and then lost. It answers a delete push with HTTP 500, and the deleted record returns on restart. In either case, fix the directory and trigger a full push.
> - **The registry refuses to start with `config.audit_sink_unavailable`.** This refusal applies only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and `PODIUM_AUDIT_LOG_PATH` is a file path or unset. The error names the path and the cause: the path is a directory, a parent component is a regular file, the parent directory cannot be created, an existing log file cannot be read, or the home directory cannot be resolved for the default `~/.podium/audit.log`. Fix the path or its permissions. Setting the interval to 0 lets the registry start, but it then runs with no audit sink, which disables audit retention, erasure, and verification as well as anchoring. The startup check reads the file and does not write it, so a log that can be read but not written passes startup, and the first audit event reports the failure.
> - **The registry refuses to start with `config.audit_anchor_key_unavailable`.** This refusal applies only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above 0 and the audit sink is a file. The key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` exists and cannot be read, lacks a `private:` line, or does not decode, or it cannot be generated. Restore the file, or move it aside so the registry generates a new keypair. Keep the old public key, because anchors written before the change were signed with it and anyone who checks them needs it.
> - **The registry refuses to start with `config.audit_anchor_key_shared`.** The anchor key is also the registry signing key or one of its `verify:` keys. Generate a separate keypair for `PODIUM_AUDIT_SIGNING_KEY_PATH`. The anchor signature carries no purpose label, so the anchor key cannot also be a registry signing or `verify:` key, and an external verifier must not trust one key for both purposes.

**CL-1 · `CHANGELOG.md`.** Lands in S15. Under `## [Unreleased]` / `### Changed`, add in the style of the surrounding entries:

> - **SCIM store refusal** (§6.3.1, §13.12): a non-empty `PODIUM_SCIM_STORE_PATH` that the registry cannot read for a reason other than the file's absence, a non-empty file that does not parse as the directory document, a parent directory the registry cannot create, and a parent directory in which it cannot create a file each refuse startup with `config.scim_store_unavailable`. This applies under every identity provider and whether or not `PODIUM_SCIM_TOKENS` mounts the receiver. A missing file and an empty file still load as an empty directory. Previously a read or parse failure logged `warning: SCIM persistence disabled` and kept the directory in memory, and an unwritable directory surfaced only on the first push. In both cases pushed records were lost on restart.
> - **Audit anchor key refusals** (§8.6, §13.12): with `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0 and a file audit sink, an anchor key file that cannot be read, parsed, or generated refuses startup with `config.audit_anchor_key_unavailable`. Previously it logged a warning and ran unanchored. While registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`), an anchor public key equal to the registry signing key or one of its `verify:` keys refuses startup with `config.audit_anchor_key_shared`, which names the `key_id`. Previously that configuration started with no warning. With the interval at 0, the anchor key is not read.
> - **Audit sink refusal while anchoring** (§8.6, §13.12): with `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0, a file `PODIUM_AUDIT_LOG_PATH` that the registry cannot open refuses startup with `config.audit_sink_unavailable`. Previously the registry logged `warning: audit sink disabled` and ran with no audit sink and no anchoring. An `http(s)` value still disables anchoring with a warning, and with the interval at 0 an unopenable file sink is still logged and the registry starts. The warning text is now `warning: audit sink disabled: <cause>`.
> - A deployment carrying one of these configurations stops booting after the upgrade. This is a backward-incompatible change under the pre-1.0 MINOR policy.

Under `### Documentation`:

> - §13.12 gains rows for `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` and an Audit anchoring table for `PODIUM_AUDIT_LOG_PATH`, `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, and `PODIUM_AUDIT_SIGNING_KEY_PATH`. §6.3.1 states how the SCIM receiver is mounted and persisted, and §8.6 specifies local chain-head anchoring and the key-separation rule. `docs/reference/cli.md` and `docs/reference/error-codes.md` list the variables and the new codes.

Follow `.claude/rules/doc-style.md` in the final text.

## Open questions

None. OQ-1 (a file sink that fails to open while anchoring is enabled) was decided on 2026-10-02. Startup refuses with `config.audit_sink_unavailable` when anchoring is enabled and the file sink cannot be opened. An `http(s)` sink never refuses, and the interval-0 warning is unchanged. The Decisions section records the rule.

## Non-goals

- Changing the signed-message format or adding a purpose label to anchor or artifact signatures (C30 option (a), deferred).
- Comparing the anchor key against a registry key file when `PODIUM_SIGN=none`. The decision scopes the check to registry signing being on.
- Refusing startup on an audit sink failure while anchoring is disabled. With the interval at 0, an unopenable file sink keeps its warning and the registry runs with no audit sink, which also disables §8.4 retention, §8.5 erasure, and §8.6 verification. This is a follow-up that needs its own decision.
- Changing the `http(s)` sink branch, including an endpoint value that does not parse, which keeps its warning whether or not anchoring is enabled.
- Probing the audit log for writability at startup. `audit.NewFileSink` reads and does not write (`pkg/audit/file.go:33-51`), so a log that can be read but not written is detected at the first `Append` (`pkg/audit/file.go:97`).
- Refusing in `sign-stored-rows`, which never anchors and keeps its warning on an unopenable file sink.
- Applying the same fail-closed rule to `PODIUM_WEBHOOK_STORE_PATH`, which has the same warn-and-fall-back pattern (`internal/serverboot/serverboot.go:1186-1196`).
- Fixing the `FileStore` mutators that update `Memory` before `save()`, or the handler's HTTP 400 `invalidValue` and HTTP 500 answers on a save failure (`pkg/scim/file_store.go:96-107`, `pkg/scim/handler.go:157-164`, `:214-217`). The boot probe reduces their reach, and runtime write failures stay as they are.
- Constant-time comparison of SCIM bearer tokens (`pkg/scim/handler.go:56` uses a map lookup).
- Specifying the other audit variables (`PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` and the retention intervals) in §13.12. SPEC-3 specifies `PODIUM_AUDIT_LOG_PATH` because the anchoring and `config.audit_sink_unavailable` conditions depend on its file and endpoint forms and its default.
- Submitting anchors to a public transparency log, or changing the §13.10 statement that transparency-log anchoring is out of scope for standalone.
- Refusing an anchor key whose default path resolves under the process's home on a non-co-located store (the anchor analogue of `refuseUnpersistedSigningKey`).
- Moving the SCIM store open ahead of the §13.4 rewrite or the bootstrap ingest.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-02, automated)

- **No test pinned the SPEC-3 ordering, and the TEST-4 "no ingest event" check could not fail.** The boot-time ingest writes no audit event, and the end-to-end fresh store gives the §13.4 rewrite no rows, so every TEST-4 assertion passed with the refusal at the scheduler call site. TEST-2 gains `TestRun_AnchorKeyRefusalPrecedesTheRewrite`, a `Run`-level integration test modeled on `TestRun_RefusesToStrandAStoredSignature` that downgrades seeded rows and asserts a refused anchor start leaves them, the completion record, and the ingest log line untouched. TEST-4 drops the ingest clause and the ordering claim, the TEST-2 header and target list name the integration level and `rehash_boot_test.go`, and the Summary and both affected Watch-out bullets cite the new test.
- **SPEC-4 said every failed anchor attempt is recorded as `audit.anchor_failed`.** Only the periodic `Scheduler` records it; the post-retention `reAnchor` closure logs. SPEC-4 now scopes the event to a failed periodic attempt and states that a failed post-retention attempt is logged. CODE-3 states that `reAnchor` keeps its log-only failure handling.
- **DOC-3 and the edge-case table said every post-start push gets HTTP 400 `invalidValue`.** A delete whose save fails gets HTTP 500, and the deleted record returns on restart. DOC-3, the "store becomes unwritable after startup" edge row, the current-state paragraph, the "unusable" decision, and the Non-goals bullet now distinguish create or replace (HTTP 400, record lost on restart) from delete (HTTP 500, record returns on restart), and the current-state paragraph records that the receiver routes no PATCH.

### Redesign 1 (2026-10-02, automated)

- **Areas redesigned.** The audit-sink branch of anchoring (OQ-1), which touches the Summary, the fixed decisions, the Watch-out list, the checklist steps S2, S8, S10, and S11, the current-state "Audit anchoring" section, the Decisions, SPEC-3, SPEC-4, CODE-3, CODE-4, the edge-case table, TEST-2, TEST-4, MV-1, DOC-1, DOC-3, CL-1, the open questions, and the Non-goals. The proposal title, the CODE-3 heading, and the MV-1 scenario title now name the sink refusal.
- **Why.** OQ-1 was decided on 2026-10-02 in favor of refusal. With `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0, a file audit sink that `resolveAuditPath` or `audit.NewFileSink` cannot open left the registry running with no audit sink and no anchoring, and a warning was the only signal. Startup now refuses with the new code `config.audit_sink_unavailable`. `openAuditSink` returns a file-sink failure as a third return value in place of logging it, and `loadAnchorSigner` checks that error before it reads the anchor key, so a refused start generates no anchor key. An `http(s)` value never refuses, and with the interval at 0 the warning stays. The code is sink-specific because the operator's remedy is the audit log path rather than the key file. The redesign also corrected a factual error: `audit.NewFileSink` creates the log's parent directory and does not create the file, which the first `Append` opens.
- **What the redesign deleted.** The fixed decision and the Decisions bullet that kept the no-sink warning, the SPEC-3 clause that let a failed file sink start unanchored, the edge-case row accepting that start pending OQ-1, the OQ-1 open question, the Non-goals bullet deferring the no-sink branch, the `warning: audit sink disabled (path)` and `(open)` log lines inside `openAuditSink`, and the claim in the Watch-out list and the refused-start edge-case row that `audit.NewFileSink` creates the audit log file. Every quotation of the former SPEC-3 sentence "Both refusals run before the §13.4 first-start rewrite and before the bootstrap ingest." now reads "Each refusal in this table runs before the §13.4 first-start rewrite and before the bootstrap ingest."
- **What the redesign added.** The `openAuditSink` signature change and its `signpass.go` caller, the `sinkErr` parameter and step 2 of `loadAnchorSigner`, the `config.audit_sink_unavailable` matrix cell, two `openAuditSink` unit tests, a `TestLoadAnchorSigner` case, a `sink` subtest in `TestRun_AnchorKeyRefusalPrecedesTheRewrite`, the `TestAuditAnchor_UnopenableSinkRefusesStart` end-to-end test, two negative controls (a directory log path with the interval at 0, and an `http://` log path with the interval at 60), MV-1 step 7, the `cli.md` and `error-codes.md` rows, a DOC-3 troubleshooting bullet, and a CL-1 changelog bullet.
- **Open decisions recorded.** The redesign took three defaults that sign-off can reverse. It refuses rather than keeping the warning, following the recommendation the user accepted; reversing it reduces the redesign to the `NewFileSink` factual correction. It adds a sink-specific code rather than reusing `config.audit_anchor_key_unavailable`, because the remedies differ. It adds no write probe for the audit log, so a log that can be read but not written passes startup and fails at the first `Append`; the Non-goals record this. A refusal of a failed file sink with the interval at 0 is a separate follow-up decision, also recorded in the Non-goals.

### Pass 2 (2026-10-02, automated)

- **SPEC-3 cited §8.3 for `PODIUM_AUDIT_LOG_PATH`, which no spec section defines.** §8.3 names only `PODIUM_AUDIT_SINK` and its MCP-side default (`spec/08-audit-and-observability.md:39`), and the only spec mention of `PODIUM_AUDIT_LOG_PATH` is the §9.1 SPI row (`spec/09-extensibility.md:14`). SPEC-3 now adds a `PODIUM_AUDIT_LOG_PATH` row to the "Audit anchoring" table that specifies the `http://` or `https://` endpoint form, the file-path form, the `~/.podium/audit.log` default, and environment-only configuration, matching `isAuditEndpoint` and `resolveAuditPath` (`internal/serverboot/audit_anchor.go:61-63`, `:131-140`) and the sole read at `internal/serverboot/serverboot.go:2291`. The interval row drops the "(§8.3)" citation. The Summary, checklist step S2, and the Non-goals bullet name the new row, and the Non-goals bullet no longer claims that a docs row resolves the cross-reference.
- **DOC-1 and CL-1 conditioned the shared-key refusal on `PODIUM_SIGN` alone.** `podium serve --sign` takes precedence (`spec/13-deployment.md:529`). The DOC-1 `cli.md` `PODIUM_AUDIT_SIGNING_KEY_PATH` row, the DOC-1 `error-codes.md` `config.audit_anchor_key_shared` row, and the CL-1 bullet now read "while registry signing is on (`PODIUM_SIGN` or `podium serve --sign` resolves to `registry-key`)", matching SPEC-3.
- **DOC-2(c) stated that a shared anchor key always refuses startup.** The replacement text now reads "Keep the anchor keypair distinct from the registry signing key at `PODIUM_SIGN_KEY_PATH`. While anchoring is enabled and signing is on, a shared key refuses startup with `config.audit_anchor_key_shared`.", matching SPEC-3 and the edge-case row for a shared key with `PODIUM_SIGN=none`.
- **The CL-1 Documentation bullet omitted the new `PODIUM_AUDIT_LOG_PATH` row.** The bullet now lists the Audit anchoring table as covering `PODIUM_AUDIT_LOG_PATH`, `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, and `PODIUM_AUDIT_SIGNING_KEY_PATH`, matching the SPEC-3 table, the Summary, and checklist step S2.
