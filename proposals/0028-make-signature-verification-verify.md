# Proposal 0028: Make signature verification verify

- Issue: (to be filed)
- Status: Draft
- Date: 2026-09-15

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- `pkg/sign` splits the §4.7.9 policy into two axes. A signature the response carries is verified under any policy other than `never`, with no sensitivity consulted. `PODIUM_VERIFY_SIGNATURES` governs only whether a *missing* signature aborts the load. `sign.EnforceVerification` today returns `nil` before touching the provider whenever `needsVerification` is false, so a response declaring `sensitivity: low` skips the cryptographic path entirely.
- `pkg/sign` makes `Noop.Verify` refuse every signature. `Sign` is unchanged. The value `Sign` produces is `"noop:" + contentHash`, and `content_hash` is served in the clear on every `load_artifact` response, so accepting it lets any party mint a passing envelope for any artifact.
- `cmd/podium-mcp` reorders `deliverLoadArtifact`: the signature check runs first, the content-hash recomputation second, and the §8.2 read event, the §4.4.1 sandbox gate, and the §4.4.1 runtime gate move below both, because each reads manifest bytes that nothing has bound to the served digest at the point it runs today. The same function also maps a missing signature onto `materialize.signature_missing`, a §6.10 cell no served error currently carries.
- The registry signs at ingest by default. `PODIUM_SIGN` resolves to `registry-key` when unset and accepts `none` to turn signing off. The keypair is already generated on first run, so a standalone deployment needs no operator action and an unenforcing consumer ignores the envelope. The Helm chart cannot generate a key, because its root filesystem is read-only and every replica has to sign under one key, so it mounts an operator-supplied Secret at `PODIUM_SIGN_KEY_PATH` and refuses to render without one unless signing is set to `none`.
- The consumer defaults become `registry-managed` for `PODIUM_SIGNATURE_PROVIDER` and `always` for `PODIUM_VERIFY_SIGNATURES`, and `podium-mcp` refuses at startup when the resolved policy is enforcing and the configured provider cannot verify. The standalone bootstrap stops writing `defaults.verify_signatures: never` and hands the consumer the registry's public key through a new `defaults.signature_verify_key_path` in the `sync.yaml` it writes.
- §4.7.9 is rewritten. It states what a signature attests, states that the identity and log properties keyless verification does not check are not checked, drops the claim that the registry key is rotated quarterly, and states that a rotation invalidates every prior envelope. §6.6 step 2 states the verification order normatively, §6.2 gains the consumer-side signing rows, §6.9 gains the failure rows, §6.10 gains the codes this surface already returns, §13.10's signing row flips and its verification relaxation is deleted, §13.12 gains a `### Signing` subsection, and §7.5.2 gains the new `sync.yaml` key.

**Fixed decisions.**

- The whole of this proposal ships in the release that carries proposal 0026. 0026 rewrites every stored content hash in a pass the registry runs once, on the first start of the new version, before it ingests or serves; where a signer is configured that pass re-signs every row it rewrites and signs a stored row that carries no signature. Flipping the producer default in the same release means that one pass signs every stored row. The constraint is hard: a marker records that the pass completed and nothing runs it again, so a default flipped in a later release signs no stored row.
- 0026 lands and is implemented first. Its pass takes the boot path's own signer, and the start is refused in two cases while the rewrite has not completed: a row the pass would rewrite is signed and no signer is configured, and signing is on, the key file is absent, and a stored row carries a signature. The signing-mode test behind both lives in one function, `registrySigningEnabled`, so CODE-4's change there reaches the loader and both refusals together, and no code in the pass changes here. CODE-4 and TEST-4 own the fixtures in 0026's tests that read an unset `PODIUM_SIGN` as no signer, and the no-signer refusal's error wording. The Relationship section states the handoff.
- The policy has two axes. Verification of a present signature is unconditional under any policy other than `never`; the policy governs only a missing signature. `needsVerification` is deleted rather than kept beside the new predicate.
- The verification order is signature, then content hash, then every gate that reads the manifest. Neither direction may be relaxed, and the cache-served path takes the same order because `deliverLoadArtifact` is shared.
- The policy never reads `sensitivity` from the hash-covered bytes. For a child declaring `extends:` the merge takes the most-restrictive value while the hash covers the child's pre-merge bytes, so reading the trigger from the attested bytes lowers the gate on exactly the artifacts §4.6 protects.
- `Noop.Sign` stays. The audit anchor and the ingest tests depend on it. `Noop.Verify` refuses unconditionally, and "this deployment does not verify" is stated with `PODIUM_VERIFY_SIGNATURES=never`.
- Verification capability is checked once, at startup, by one predicate over the resolved policy and the configured provider. There is no second check that pairs a provider name against a policy value elsewhere.
- Consumer key distribution stays out of band for every deployment other than standalone, where the key is on the same filesystem. This proposal adds no key-publication route and no trust-on-first-use pin.
- Sigstore verification correctness is a separate proposal. Nothing in the product mints a Sigstore envelope, so every defect there is latent. What lands here is the §4.7.9 text that stops claiming otherwise.
- Podium is pre-1.0. There is no grace policy value, no "artifacts ingested before the upgrade" exemption, and no compatibility path for an unsigned row.

**Watch out for.**

- **Enabling signing on an existing registry signs no stored row.** `ingest.Ingest` short-circuits on `existing.ContentHash == mr.ContentHash` with `res.Idempotent++; continue` before the signing block, and no ingest path writes a signature onto an existing row. 0026's first-start rewrite signs stored rows once and does not run again after its marker is set, so a registry that enables signing after that start keeps its unsigned rows until the rebuild: delete the tenant's manifest rows and re-ingest each layer. That is why the release window is a constraint rather than a preference.
- **The standalone relaxation fails open and no code change reaches an existing machine.** `writeFileIfAbsent` never rewrites `~/.podium/sync.yaml`, so a machine that ran `podium serve` once keeps `defaults.verify_signatures: never` indefinitely, including when it is later pointed at a production registry through `PODIUM_REGISTRY`. The remedy is an operator editing one line, and the proposal says so rather than implying self-healing.
- **The relaxation also fires on the explicit-server-config branch.** `standaloneStartup` bootstraps on both branches, so a SQLite-backed server with an OIDC identity provider on a non-loopback bind writes the relaxation too. Deleting the line removes that leak; do not attempt to rescope the bootstrap itself, which also writes the registry pointer, `registry.yaml`, and the artifacts directory.
- **`sensitivity` has no attested source for a child declaring `extends:`.** Relocating the read to the hash-covered bytes is a regression rather than a partial fix. Do not implement the relocation an earlier review asked for.
- **The signature check needs no manifest bytes.** `Provider.Verify(ctx, contentHash, signature)` consumes two scalars off the envelope, which is what makes it runnable before any fetch. Running it first is what turns `verifyContentHash` from a self-consistency check over two registry-supplied values into an integrity check.
- **The cache replays old envelopes through the new gate.** `putExtras` persists the served `sensitivity` and `signature`, and `deliverLoadArtifact` is the shared path, so a warmed `offline-only` consumer refuses those artifacts with no in-product recovery. 0026's mandated cache-directory clear repairs it in the same release, which is another reason the two ship together.
- **A test that boots a standalone server and then runs `mcpExec` shares one isolated home.** `cmdharness.IsolatedHome` is keyed on `t.Name()`, so such a test inherits the bootstrapped `sync.yaml` and starts enforcing. Enumerate the fixtures carrying `sensitivity: medium` or `high` at implementation time rather than assuming which ones are affected.
- **`materialize.signature_missing` is a matrix cell and a documented code that no served error carries.** `deliverLoadArtifact` prefixes every policy failure with `materialize.signature_invalid`, so the missing-signature case surfaces under the wrong code. Existing tests assert on a substring and pass either way.
- **`podium sync` and both SDKs verify nothing at any setting.** `EnforceVerification` has one production caller. Every claim this proposal makes about what a consumer verifies is a claim about `podium-mcp`.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §4.7.9 is rewritten: what a signature attests, the two policy axes, the verification order, consumer-side key handling, the rotation limitation, and what keyless verification does not check.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §6.6 step 2 states the verification order normatively and names the gates that follow it.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.2's `PODIUM_VERIFY_SIGNATURES` default becomes `always`, and the table gains the consumer-side provider and key rows.
      Levels: —. Depends on: S1
- [ ] **S4 · spec** — SPEC-4. §6.9 gains the verification failure rows, and §6.10 gains `materialize.signature_missing`, `config.signature_provider_unavailable`, and `ingest.sign_failed`.
      Levels: —. Depends on: S1, S2
- [ ] **S5 · spec** — SPEC-5. §13.10's signing row flips, its signature-verification bullet is deleted, and §13.12 gains a `### Signing` subsection.
      Levels: —. Depends on: S1, S3
- [ ] **S6 · spec** — SPEC-6. §7.5.2 gains `defaults.signature_verify_key_path`.
      Levels: —. Depends on: S1
- [ ] **S7 · code** — CODE-1, CODE-2, TEST-1. `EnforceVerification` splits into the two axes, `needsVerification` is deleted, `Noop.Verify` refuses, and `pkg/sign`'s tests are rewritten. One commit, because the existing round-trip and policy tests express their expectations through the behavior both changes remove.
      Levels: unit. Depends on: S1, S3
- [ ] **S8 · code** — CODE-3, CODE-7, TEST-2, TEST-3, TEST-7. `deliverLoadArtifact` is reordered, the missing-signature case maps onto its own code, and the four §6.10 cells are added to `tools/matrix/matrices.go` with their annotated tests.
      Levels: unit, e2e. Depends on: S2, S4, S7
- [ ] **S9 · code** — CODE-4, TEST-4. `PODIUM_SIGN` resolves to `registry-key` when unset and accepts `none` in `registrySignerFor`, and the fixtures in 0026's first-start rewrite tests that read an unset `PODIUM_SIGN` as no signer move to `none`. Producer-side only; no consumer behavior changes in this step.
      Levels: unit, e2e. Depends on: S5
- [ ] **S10 · code** — CODE-5, CODE-6, TEST-5, TEST-6. The consumer defaults flip, startup refuses a provider that cannot verify under an enforcing policy, the standalone bootstrap hands over the key instead of relaxing the policy, and `pkg/sync` gains the new default.
      Levels: unit, e2e. Depends on: S3, S5, S6, S8, S9
- [ ] **S11 · code** — CODE-8, TEST-8. The Helm chart mounts an operator-supplied Secret holding the registry signing key and requires it unless signing is set to `none`.
      Levels: unit. Depends on: S9
- [ ] **S12 · docs** — DOC-1, MAN-1. The `docs/` pages that state the current defaults, the operator guide's failure narrative, `CHANGELOG.md`, and `test/manual-validation.md` scenarios S68 through S72.
      Levels: manual. Depends on: S7, S8, S9, S10, S11

**Ordering constraints.** The spec steps precede the code per `.claude/rules/spec-driven-development.md`. S7 precedes S8 because the reordered pipeline calls the two-axis function. S9 precedes S10 so that the first-start rewrite 0026 stages signs every stored row before any consumer starts requiring a signature; landing S10 first refuses every load in the window between them. S11 follows S9 because the chart's signing values name the modes CODE-4 defines, and S12 follows the code so the changelog describes what the tested build does.

## Current state and the gap

### The policy skips the cryptography on a value the registry supplies

`sign.EnforceVerification` (`pkg/sign/sign.go`) returns `nil` before reaching the provider whenever `needsVerification(policy, sensitivity)` is false, which under the documented `medium-and-above` default (`spec/06-mcp-server.md:34`) is every artifact the response does not report at `medium` or `high`. The sensitivity it reads is `resp.Sensitivity`, the JSON envelope field, served from the `manifests` column and carried across a restart by the cache's side files. A registry that sets `"sensitivity":"low"` on a response therefore skips the entire cryptographic path with no forgery, no key, and no collision.

Cross-checking the wire field against the frontmatter closes nothing, because a registry forging an artifact writes the same value into the frontmatter and recomputes the hash over its own bytes. Relocating the read to the hash-covered bytes is worse than neutral. `pkg/manifest/merge.go` merges sensitivity most-restrictive and `foldExtendsParent` folds the merged value into the stored column, so for a child declaring `extends:` the served value can be `high` while the pre-merge `ARTIFACT.md` the hash covers declares `low` or declares nothing. A consumer reading the trigger from the attested bytes would skip verification on an artifact the registry itself classified `high`. There is no attested source for a child's effective sensitivity, which makes the conditional trigger a control with no sound input.

### The order gives the content-hash check nothing to defend against

`deliverLoadArtifact` (`cmd/podium-mcp/main.go`) emits the §8.2 read event, enforces the signature policy, enforces the §4.4.1 sandbox gate, and enforces the §4.4.1 runtime gate, and only then recomputes the digest in `verifyContentHash`. Each of the three gates reads `resp.Frontmatter` at a point where nothing has bound those bytes to the served digest, and the read event applies the manifest's `audit_redact` directive from the same unverified bytes.

`verifyContentHash` compares a value recomputed from registry-supplied bytes against a registry-supplied digest, which detects a tampering intermediary and nothing else. It becomes a check against the registry only once the digest has been attested. `Provider.Verify(ctx, contentHash, signature)` consumes no manifest bytes, so the signature check can run before any fetch, and running it first is what gives the recomputation a party to defend against. The spec fixes neither order: §6.6 step 2 is one undivided sentence (`spec/06-mcp-server.md:239`).

### No default verifies anything

`registrySignerFor` (`internal/serverboot/signing.go`) returns `(nil, nil)` unless the mode is `registry-key`, and `registry-key` is the only accepted non-empty value, so `ingest.Request.Signer` stays nil and `mr.Signature` is never assigned. `buildSignatureProvider` maps both `""` and `"noop"` to `sign.Noop{}`, whose `Verify` accepts `"noop:" + contentHash`, a value any party holding the response can compute. The standalone bootstrap writes `defaults.verify_signatures: never` into `~/.podium/sync.yaml`, honored by `loadConfig` whenever `PODIUM_VERIFY_SIGNATURES` is unset.

The one verifying configuration needs a registry started with `--sign registry-key`, a consumer provider of `registry-managed`, a consumer key in `PODIUM_SIGNATURE_VERIFY_KEY`, and a policy above `never`, coordinated across two processes with no in-band key distribution. `test/e2e/signed_artifact_helpers_test.go` is the only place in the repository where all of them are set.

### Enabling signing later signs no stored row

`ingest.Ingest` short-circuits the idempotent branch before the signing block, `PutManifest` enforces the §4.7 immutability invariant, and `podium layer unregister` soft-deletes without removing the row. A `podium layer reingest` after enabling signing therefore reports every artifact idempotent and leaves every stored `Signature` empty. 0026's first-start rewrite is the one path that attaches an envelope to a stored row: where a signer is configured it re-signs every row it rewrites and signs every stored row that carries no signature. It runs once, on the first start of the new version, and a marker keeps it from running again. Flipping the producer default in the same release therefore signs every stored row in the start every deployment already performs. A registry that enables signing after that start gets no second pass, and its repair is the rebuild: delete the tenant's manifest rows and re-ingest each layer.

### The bootstrap relaxation is sticky and out of scope

`verifySignaturesFromSyncYAML` resolves `defaults.verify_signatures` from the workspace `.podium/sync.yaml` and then the home-global file with no association to the registry in use, and `writeFileIfAbsent` never rewrites an existing file. A developer who runs `podium serve` once and is later pointed at a production registry through `PODIUM_REGISTRY` keeps the relaxation. `standaloneStartup` also bootstraps on the `hasExplicitServerConfig` branch, so a multi-user SQLite deployment with an OIDC provider writes it too.

### Codes this surface returns are unregistered

`materialize.content_hash_mismatch` gained its §6.9 row, its §6.10 axis cell, and its annotated test from proposal 0026, so it needs nothing here. `config.signature_provider_unavailable` and `ingest.sign_failed` are returned by `pkg/sign` and `pkg/registry/ingest` and appear in neither. `materialize.signature_missing` is on the matrix axis and in the docs but nowhere in `spec/`, and no served error carries it as its leading code, because `deliverLoadArtifact` prefixes every policy failure with `materialize.signature_invalid`.

### §4.7.9 describes a model the code does not implement

The section calls Sigstore-keyless preferred and names a transparency-log entry. `SigstoreKeyless.Verify` never reads the certificate's subject or issuer, validates the chain at wall-clock time against a certificate that lives minutes, and treats an HTTP 200 from Rekor as sufficient while discarding the body and the attested time. The log check is also skipped when the envelope itself declares `log_index: -1`. None of this has a product effect: `registrySignerFor` accepts only `registry-key`, and no code path anywhere outside tests mints a Sigstore envelope into the store. The section also states that the registry-managed key is rotated quarterly, while `RegistryManagedKey` holds one keypair and no path re-signs a stored row, so a rotation invalidates every prior envelope permanently.

## Decisions

- **Split the policy into two axes rather than removing the sensitivity condition.** The conflation of "check the signature I was given" with "require a signature" is the defect. Unconditional verification of a present signature costs nothing operationally and closes the free bypass for every signed artifact, while `medium-and-above` survives as the rollout posture `docs/deployment/progressive-adoption.md` is built on. The residual, a registry serving an unsigned forgery labelled `low`, is closed by `always` and by no trigger, and §4.7.9 says so in one sentence.
- **Verify the signature before the content hash.** The signature consumes no manifest bytes, so the order is available, and it is the order under which the recomputation means something.
- **Move the read event and the manifest gates below the content-hash check.** Each is a fail-closed control reading unattested input, which is the same defect class as the sensitivity read. A refused load stops emitting a local `artifact.loaded` event; the registry's own audit stream still records the call.
- **Make `Noop.Verify` refuse.** A provider that accepts a value derived from a public field is an acceptance predicate rather than a placeholder, and it satisfies `always`. `Sign` is a legitimate placeholder and is unchanged.
- **Check verification capability once, at startup.** One predicate over the resolved policy and the configured provider refuses `noop` under an enforcing policy, `registry-managed` with no resolvable key, and `sigstore-keyless` with no readable trust root, all with `config.signature_provider_unavailable`. A second predicate pairing provider names against policy values elsewhere is where the two would drift.
- **Flip the producer default before the consumer defaults, in that order, in one release.** Signing is producer-side and breaks nothing on its own. The consumer defaults are what break a deployment, and they are safe only once 0026's first-start rewrite has attached envelopes.
- **Keep consumer key distribution out of band, except in standalone.** In standalone the registry's key file sits on the same filesystem as the consumer, so the bootstrap can hand over a path. Elsewhere the operator copies the public half, which is a documented step rather than a new protocol. A published-key route with a first-use pin is a mechanism with its own security semantics and belongs in its own proposal.
- **Carry the §4.7.9 correction now and defer the Sigstore work.** A spec that claims verification properties the code does not deliver is the defect this proposal exists to remove, and correcting text costs nothing. Building certificate-identity policy and transparency-log verification carries a dependency decision and has no product effect until the registry gains a keyless signing mode.
- **Do not repair key rotation.** A key set is a provider redesign that belongs with the Sigstore work. 0026's first-start rewrite leaves untouched a row whose stored envelope does not verify under the configured key, because it cannot tell a rotated key from a forged envelope, and it runs once, so it is not a rotation repair. §4.7.9 states the accepted behavior instead, and OQ-2 carries the question.

## Spec amendment: §4.7.9 signing

Replace the section body at `spec/04-artifact-model.md:883-892`, keeping the `### 4.7.9 Signing` heading.

```markdown
Each artifact version is signed by the author's key at commit time, or by a registry-managed key at ingest. Two key models:

- **Registry-managed key.** A per-org Ed25519 keypair held by the registry, generated on first run. The registry signs at ingest by default (§13.10).
- **Sigstore-keyless.** An OIDC-attested signature with a transparency-log entry and no key management. No registry signing mode produces one; the consumer-side verifier does not check the certificate's signer identity and does not check the log entry's contents, so a keyless envelope attests that some certificate the configured trust root issued signed the digest, and nothing about who holds it.

A signature attests the artifact's `content_hash` (§4.7.6) and nothing else. It does not attest the artifact's `sensitivity`, the version the registry selected, or, for an artifact declaring `extends:`, the merged bytes the consumer materializes (§4.6).

The MCP server verifies every signature it is served, before it checks the content hash and before any gate reads the manifest (§6.6 step 2). `PODIUM_VERIFY_SIGNATURES` (§6.2) governs only whether a *missing* signature aborts the load. `always` requires one on every artifact. `medium-and-above` requires one for an artifact the registry reports at `sensitivity: medium` or `high`; the registry reports that value, and an `extends:` child's effective sensitivity is the most-restrictive merge of its chain rather than a value the content hash covers, so `medium-and-above` is a rollout posture for a deployment whose signing pipeline does not yet cover every artifact. `always` is the only policy under which a registry serving an unsigned artifact is refused. A signature that does not validate aborts materialization with `materialize.signature_invalid`; a required signature that is absent aborts with `materialize.signature_missing`.

Verification runs in the consumer. The consumer holds the registry's public key, supplied out of band through `PODIUM_SIGNATURE_VERIFY_KEY` or, in a standalone deployment, through the `defaults.signature_verify_key_path` the bootstrap writes (§7.5.2, §13.10). A consumer whose policy is above `never` and whose provider has no usable verification material refuses to start with `config.signature_provider_unavailable`.

Rotating the registry-managed key invalidates every signature made under the retired key. A stored signature is written at ingest, and the §13.4 first-start rewrite re-signs only a row whose stored envelope verifies under the configured key, so neither repairs a rotation. A rotation is repaired by deleting the tenant's manifest rows and re-ingesting each layer.

`podium verify <artifact>` for ad-hoc verification. `podium sign <artifact>` for explicit signing outside the ingest flow.
```

## Spec amendment: §6.6 step 2 the verification order

Replace list item 2 at `spec/06-mcp-server.md:239`. The step count is unchanged, so no `§6.6 step N` cross-reference moves.

```markdown
2. **Verify.** Verification is two ordered checks, and both precede every gate that reads the manifest.

   First the signature. Under any policy other than `never`, the consumer verifies a `signature` the response carries against the served `content_hash` through the configured `SignatureProvider`, and refuses a signature that does not validate with `materialize.signature_invalid`. The consumer does not consult `sensitivity` to decide whether to check a signature it was given. Whether a *missing* signature is itself a refusal is the axis `PODIUM_VERIFY_SIGNATURES` governs (§4.7.9), and a required signature that is absent refuses with `materialize.signature_missing`.

   Then the content hash. The consumer recomputes the §4.7.6 canonical serialization over the delivered bytes and refuses a mismatch with `materialize.content_hash_mismatch` before anything is cached or written.

   The order is normative in both directions. The signature attests the `content_hash` and nothing else, so the recomputation is an integrity check against the serving party only once the signature over that hash has been verified; run the other way round it compares two values the same party supplied. The §4.4.1 `sandbox_profile` and `runtime_requirements` gates, the §6.7 adapter translation, and the §8.2 read event all read manifest content, so all of them run after the content hash matches, and a refused load records no read event on the consumer's local sink. Bundle contents (scripts, configs, SBOMs) are not introspected; vulnerability scanning is a CI/CD concern per §1.1.
```

## Spec amendment: §6.2 the consumer signing settings

Replace the `PODIUM_VERIFY_SIGNATURES` row at `spec/06-mcp-server.md:34` and append the rows below it.

| Parameter | Description | Default |
| --- | --- | --- |
| `PODIUM_VERIFY_SIGNATURES` | Signature policy on materialization: `never`, `medium-and-above`, or `always` (§4.7.9) | `always` |
| `PODIUM_SIGNATURE_PROVIDER` | Selected `SignatureProvider` (§9.1): `noop`, `registry-managed`, or `sigstore-keyless`. An unrecognized value refuses startup with `config.invalid` | `registry-managed` |
| `PODIUM_SIGNATURE_VERIFY_KEY` | Base64 Ed25519 public key for `registry-managed` verification, supplied out of band | (unset → `defaults.signature_verify_key_path` from `sync.yaml`, §7.5.2) |
| `PODIUM_SIGNATURE_KEY_ID` | Expected signing-key fingerprint. When both it and the envelope's `key_id` are set, an envelope from another key is refused | (unset → no fingerprint check) |

## Spec amendment: §6.9 failure modes and §6.10 codes

Replace the "Signature verification failure" row in §6.9's table with the rows below. The "Content hash mismatch at the §6.6 step-2 check" row that proposal 0026 added stays as it is.

| Failure | Behavior |
| --- | --- |
| Signature verification failure | Fail with `materialize.signature_invalid`; do not write to disk. The check runs before the content-hash match and before any gate reads the manifest (§6.6 step 2) |
| Required signature absent | Fail with `materialize.signature_missing`; do not write to disk. Required on every artifact under `always`, and on a `medium` or `high` artifact under `medium-and-above` |
| Enforcing policy with a provider that cannot verify | Refuse to start with `config.signature_provider_unavailable`, naming the missing key or trust root. The consumer supplies verification material or sets `PODIUM_VERIFY_SIGNATURES=never` |

§6.10's enumerated codes gain `materialize.signature_missing`, `config.signature_provider_unavailable`, and `ingest.sign_failed`, each with the meaning the row above or the returning site already gives it. `ingest.sign_failed` rejects one artifact at ingest when the configured signer fails, leaving the rest of the batch unaffected. The `tools/matrix/matrices.go` mirror of the §6.10 axis is updated in the same change (CODE-7), and each new cell carries a `// Matrix: §6.10 (<code>)` annotated test.

## Spec amendment: §13.10 and §13.12 the deployment defaults

Replace the Signing row at `spec/13-deployment.md:147`.

```markdown
| Signing                 | Registry-managed key                            | Enabled by default. The Ed25519 keypair is generated on first run at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`) and the public half is written beside it. `--sign none` disables it |
```

Delete the "Signature verification" bullet at `spec/13-deployment.md:201` in full. The standalone deployment no longer relaxes the consumer policy, because the registry signs and the bootstrap hands the consumer the key.

Add a `### Signing` subsection to §13.12, after `### Identity provider` (`spec/13-deployment.md:488`).

```markdown
### Signing

| Setting | Description | Default |
| --- | --- | --- |
| `PODIUM_SIGN` | Ingest signing mode: `registry-key` or `none`. Any other value refuses startup with `config.invalid_sign_mode` | `registry-key` |
| `PODIUM_SIGN_KEY_PATH` | Path to the registry-managed Ed25519 keypair. Generated on first run when absent | `~/.podium/standalone/registry-signing.key` |

Signing is producer-side: it determines whether an artifact ingested from now on carries a §4.7.9 envelope. Ingest does not attach one to an already-stored artifact, because a stored signature is written once, at ingest. The §13.4 first-start rewrite signs the stored rows it rewrites and the stored rows that carry no signature, and it does not run again once it has completed. Turning signing on afterwards for a registry that holds unsigned rows requires deleting the tenant's manifest rows and re-ingesting each layer.

The consumer-side settings that decide whether a signature is checked are client configuration and live in §6.2 and §7.5.2.
```

## Spec amendment: §7.5.2 the consumer verification key

Add `signature_verify_key_path` to the §7.5.2 `defaults:` block documentation, beside `verify_signatures`.

```markdown
`defaults.signature_verify_key_path` names a file holding the registry's base64 Ed25519 verification key, used by the `registry-managed` provider when `PODIUM_SIGNATURE_VERIFY_KEY` is unset. A standalone deployment writes it on first run, pointing at the public half of the keypair the registry generated (§13.10). The file's last non-empty line is the key. A path that does not resolve to a usable key refuses startup under any policy above `never` with `config.signature_provider_unavailable`.
```

## Proposed solution

### CODE-1: split `EnforceVerification` into two axes

`pkg/sign/sign.go`. The exported signature and name are unchanged, so the production caller and `test/conformance/reference_visibility_test.go` compile untouched. `needsVerification` is deleted; leaving both predicates is how they drift.

```go
// EnforceVerification applies policy to one served artifact.
//
// Two axes, deliberately separated. A signature that is present is always
// verified under any policy other than never, because the sensitivity the
// policy would otherwise gate on is reported by the same party that supplies
// the signature, and for a child declaring extends: (§4.6) no attested source
// for it exists: the merge takes the most-restrictive value
// (pkg/manifest/merge.go) while the content hash covers only the child's
// pre-merge bytes. Whether a missing signature aborts the load is the axis the
// policy governs, and only `always` resists a registry that serves an unsigned
// forgery.
//
// spec: §4.7.9, §6.6 step 2.
func EnforceVerification(ctx context.Context, policy VerificationPolicy, provider Provider, sensitivity manifest.Sensitivity, contentHash, signature string) error {
	if policy == PolicyNever {
		return nil
	}
	if signature == "" {
		if !requiresSignature(policy, sensitivity) {
			return nil
		}
		return fmt.Errorf("%w: sensitivity %q requires a signature", ErrSignatureMissing, sensitivity)
	}
	return provider.Verify(ctx, contentHash, signature)
}

// requiresSignature reports whether a missing signature is a refusal. An
// unrecognized policy fails closed; loadConfig refuses such a value at startup
// (§6.2), so this is defense in depth for a direct constructor.
func requiresSignature(policy VerificationPolicy, s manifest.Sensitivity) bool {
	switch policy {
	case PolicyAlways:
		return true
	case PolicyMediumAndAbove:
		return s == manifest.SensitivityMedium || s == manifest.SensitivityHigh
	case PolicyNever:
		return false
	default:
		return true
	}
}
```

### CODE-2: `Noop.Verify` refuses

`pkg/sign/sign.go`. `Noop.Sign` and `Noop.ID` are unchanged, and the type stays selectable so `podium sign` keeps its scaffolding value and the SPI keeps a trivially constructible implementation. The type's doc comment loses the sentence calling it a safe default in standalone deployments, which stops being true once any policy above `never` is in force.

```go
// Verify always refuses. The value Sign produces is "noop:" + contentHash, and
// contentHash is served in the clear on every load_artifact response, so
// accepting it would let any party mint a passing signature for any artifact.
// A deployment that does not verify says so with PODIUM_VERIFY_SIGNATURES=never
// (§6.2); it does not say so by configuring a provider that accepts a public
// value.
func (Noop) Verify(_ context.Context, _, _ string) error {
	return fmt.Errorf("%w: the noop provider does not verify; set PODIUM_SIGNATURE_PROVIDER to registry-managed or sigstore-keyless, or set PODIUM_VERIFY_SIGNATURES=never", ErrSignatureInvalid)
}
```

### CODE-3: reorder `deliverLoadArtifact` and name the missing-signature failure

`cmd/podium-mcp/main.go`. The body of `deliverLoadArtifact` is reordered to:

1. `fetchManifestBody`, unchanged in position. It reconstitutes bytes the later check covers and issues no request the signature gate would have avoided.
2. `enforceSignaturePolicy`, moved above `decodeInlineResources` and `fetchLargeResources`. A refused load now stops before any presigned resource URL is requested.
3. `decodeInlineResources`, then `fetchLargeResources`, unchanged relative to each other.
4. `verifyContentHash`, moved above the gates below.
5. `auditLoadArtifact`, `enforceSandboxPolicy`, and `enforceRuntimePolicy`, moved below the content-hash check in that order.
6. `cache.put` and `cache.putExtras` onward, unchanged.

The doc comment on `deliverLoadArtifact` states the order and why it is normative, citing §6.6 step 2 rather than restating the argument. The three moved gates keep their existing error codes.

`enforceSignaturePolicy`'s call site stops prefixing every failure with one code:

```go
if err := s.enforceSignaturePolicy(resp); err != nil {
	// §6.10: a missing required signature and a signature that does not
	// validate are separate codes with separate operator remedies. Both were
	// served as materialize.signature_invalid, so the documented
	// materialize.signature_missing code never reached a client.
	if errors.Is(err, sign.ErrSignatureMissing) {
		return errorResult("materialize.signature_missing: " + err.Error())
	}
	return errorResult("materialize.signature_invalid: " + err.Error())
}
```

### CODE-4: the registry signs by default

`internal/serverboot/signing.go` and `internal/serverboot/serverboot.go`. `registrySignerFor` treats an empty mode as `registry-key` and returns `(nil, nil)` only for `none`. The startup validation that returns `config.invalid_sign_mode` accepts `registry-key` and `none` and refuses any other value, including the empty string it must no longer see as "disabled". `cmd/podium/serve.go`'s `--sign` help text names both values.

0026 is implemented, and the mode reading now lives in `registrySigningEnabled` (`internal/serverboot/signing.go`), which `registrySignerFor` and the generated-key refusal `refuseGeneratedSigningKey` (`internal/serverboot/rehash.go`) both call. The change lands in that one function: an empty mode and `registry-key` enable signing, and `none` disables it, so ingest, the first-start rewrite, and both refusals read the mode identically. `registrySignerFor` already returns the whole `sign.Provider`. The rewrite's refusal to start when a row it would rewrite is signed and no signer is configured is keyed on a nil provider, which `none` still produces, so no code in the pass changes. That refusal's error tells the operator to set `PODIUM_SIGN=registry-key`; under this proposal it is reachable only under `none`, so its wording changes to tell the operator to remove `PODIUM_SIGN=none` and point `PODIUM_SIGN_KEY_PATH` at the key that signed the rows. The generated-key refusal needs no edit, and the default now reaches it: a store holding signed rows whose key file is absent at a start before the rewrite completes is refused with no signing configuration set, which the edge-case table records. 0026's accepted-failure text for a `signature_unverified` row cites the consumer gate order of the binary it lands on; CODE-3's reorder keeps the signature gate ahead of the content-hash check, so those load outcomes hold, and the implementor re-reads that text against the reordered function and restates it only where a code changes.

No key material work is required: `loadOrGenerateRegistrySigner` already generates the keypair on first run at `PODIUM_SIGN_KEY_PATH`. The public half is written beside it as `<path>.pub` in the same call, which is what CODE-6 hands to the consumer; the keypair file itself already carries the public half and is not the file a consumer should be pointed at.

### CODE-5: the consumer defaults, and one startup capability check

`cmd/podium-mcp/main.go` and `cmd/podium/sign.go`.

- `envDefault("PODIUM_SIGNATURE_PROVIDER", "noop")` becomes `"registry-managed"` at all three sites, and `buildSignatureProvider`'s `case "", "noop"` splits so that an empty name is no longer an alias for `noop`.
- The `verifyPolicy` fallback in `loadConfig` becomes `sign.PolicyAlways`. The `sync.yaml` resolution above it is unchanged, so an operator-set `never` still wins.
- `loadConfig` refuses an unrecognized `PODIUM_SIGNATURE_PROVIDER` with `config.invalid`, beside the existing refusals for an unknown policy, identity provider, cache mode, and harness. `buildSignatureProvider` stays per-load, so this makes a typo a startup refusal rather than a per-artifact error.
- `loadConfig` refuses, with `config.signature_provider_unavailable`, when the resolved policy is not `never` and the configured provider cannot verify. The predicate is: `noop` never can; `registry-managed` cannot when neither `PODIUM_SIGNATURE_VERIFY_KEY` nor a `defaults.signature_verify_key_path` resolves to a usable key; `sigstore-keyless` cannot when `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` names no readable file. The error names which value is missing.

Key resolution for `registry-managed` runs `PODIUM_SIGNATURE_VERIFY_KEY`, then `defaults.signature_verify_key_path` resolved with the same workspace-then-home scope order `verifySignaturesFromSyncYAML` uses, and `buildSignatureProvider` reads the resolved key rather than the environment alone. The file's last non-empty line is the base64 key, which is the layout `readOrCreateEd25519` writes.

**IMPLEMENTOR'S CHOICE:** whether the resolved key is read once into `mcpConfig` at startup or re-read per load. Any answer resolves it identically in the startup check and in `buildSignatureProvider`, so a consumer that starts cannot then fail a load for a key the startup check accepted.

### CODE-6: the standalone bootstrap hands over the key

`internal/serverboot/standalone_bootstrap.go` and `pkg/sync/config.go`.

The bootstrapped `sync.yaml` body stops carrying `verify_signatures` and carries the key path instead:

```go
syncBody := []byte("defaults:\n  registry: " + cfg.publicURL +
	"\n  signature_verify_key_path: " + pubKeyPath + "\n")
```

where `pubKeyPath` is the `.pub` file CODE-4 writes beside the keypair. First run is green under the `always` default because the registry signs and the consumer is handed the key, rather than because verification was switched off. Deleting the line also removes the leak onto the `hasExplicitServerConfig` branch, so no rescoping of `bootstrapStandaloneFiles` is needed.

`pkg/sync.Defaults` gains `SignatureVerifyKeyPath string \`yaml:"signature_verify_key_path,omitempty"\`` with a doc comment citing §7.5.2 and §13.10. `VerifySignatures` stays, because an operator may still set it.

An existing `~/.podium/sync.yaml` is never rewritten. `podium-mcp` logs one line at startup when the resolved policy is `never` and it came from a `sync.yaml` rather than from the environment, naming the file and the key to remove. The warning is the only mechanism that reaches an already-bootstrapped machine, and it does not change the resolved policy.

### CODE-7: register the §6.10 cells

`tools/matrix/matrices.go`. The §6.10 axis gains `config.signature_provider_unavailable` and `ingest.sign_failed`; `materialize.content_hash_mismatch` is already on it from proposal 0026; `materialize.signature_missing` is already on the axis and gains its spec entry through SPEC-4. Each new cell needs a `// Matrix: §6.10 (<code>)` annotated test, staged in TEST-3.

### CODE-8: the Helm chart mounts the signing key

`deploy/helm/podium/values.yaml` and `deploy/helm/podium/templates/deployment.yaml`, in the form the chart's `runtimeKeys` block already uses. `values.yaml` gains:

```yaml
# Registry signing key (§4.7.9, §13.10). Every replica signs under this one key.
signing:
  mode: registry-key        # or none
  secretName: ""
  mountPath: /signing
  key: registry-signing.key
```

When `signing.mode` is `registry-key` the deployment template renders `PODIUM_SIGN=registry-key` and `PODIUM_SIGN_KEY_PATH` as `<mountPath>/<key>`, mounts the Secret read-only at `signing.mountPath`, and resolves the Secret's name through `required "signing.secretName is required unless signing.mode=none"`, so a render without a Secret fails with that message. When it is `none` the template renders `PODIUM_SIGN=none` and neither the variable for the path, the mount, nor the volume. Any other value fails the render. The `volumeMounts` and `volumes` guards, which today test `objects.enabled` and `runtimeKeys.enabled`, also test the signing mode.

The Secret holds the key file in the format `readOrCreateEd25519` writes. The loader reads an existing file and writes nothing, so the read-only mount is sufficient, and the `<path>.pub` file CODE-4 writes beside a key is written only when the loader generates one, which a chart install never does. The operator produces the file by starting a standalone registry once with `PODIUM_SIGN_KEY_PATH` naming a scratch path, and creates the Secret from it; DOC-1 states the commands. A deployment upgrading a store that already holds signed rows creates the Secret from the key that signed them, because the §13.4 rewrite leaves a row whose envelope does not verify untouched.

The chart's default now requires a value it did not before, so every site that renders or installs the chart with default values follows: the render tests in `test/chart/` that use no overrides, the `helm install` command in `docs/deployment/clustered.md`, and the chart-install manual scenario in `test/manual-validation.md`.

**IMPLEMENTOR'S CHOICE:** the wording of the value comments and of the `fail` message for an unknown mode, subject to the message naming `signing.mode` and both accepted values.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A response declaring `sensitivity: low` with a signature that does not validate | Refused with `materialize.signature_invalid` under any policy above `never`. Previously loaded cleanly | SPEC-1; pinned by TEST-1 and TEST-2 |
| A response declaring `sensitivity: low` with no signature, under `medium-and-above` | Loads. The rollout affordance survives, and `always` is the policy that refuses it | SPEC-1; pinned by TEST-1 |
| An unsigned forgery served as `sensitivity: low` under `medium-and-above` | Loads. No trigger closes this, because the serving party chooses the trigger. `always` is the only policy that refuses it | SPEC-1's `medium-and-above` sentence; `docs/deployment/progressive-adoption.md` per DOC-1 |
| A child declaring `extends:` whose effective sensitivity is higher than its own | Verified the same as any other artifact. The effective value is attested by nothing, and the policy no longer depends on it | SPEC-1's attestation paragraph |
| A response whose signature is invalid and whose bytes also mismatch | Refused with `materialize.signature_invalid`. The signature check runs first | SPEC-2; pinned by TEST-2 |
| A response whose bytes mismatch and whose `sandbox_profile` is unsupported | Refused with `materialize.content_hash_mismatch`. Previously the sandbox gate answered first | SPEC-2; pinned by TEST-2 |
| A refused load's local audit stream | Records no `artifact.loaded` event. The registry's own stream still records the call, so the load is not invisible | SPEC-2's final paragraph; `docs/deployment/operator-guide.md` per DOC-1 |
| A registry that enables signing after its first start on this release, against an upgraded consumer | Every load of a row stored unsigned fails with `materialize.signature_missing`. The first-start rewrite does not run again, so the repair is the rebuild: delete the tenant's manifest rows and re-ingest each layer | `CHANGELOG.md` per DOC-1; SPEC-5's §13.12 subsection |
| Consumers upgraded before the registry | Every load fails with `materialize.signature_missing` until the registry is rolled; its first start rewrites and signs every stored row before it serves. The operator rolls the registry first | `CHANGELOG.md` per DOC-1; `docs/deployment/operator-guide.md` per DOC-1 |
| A machine whose `~/.podium/sync.yaml` already carries `verify_signatures: never` | Keeps verifying nothing. No code rewrites an operator's config. The bridge logs one startup line naming the file and the key to remove, and the upgrade notes name the edit | CODE-6; `CHANGELOG.md` and `docs/deployment/operator-guide.md` per DOC-1; pinned by TEST-6 |
| A machine with that stale file that also sets `PODIUM_VERIFY_SIGNATURES` | Refuses to start with `config.signature_provider_unavailable`, because the stale file carries no key path. The operator supplies the key or removes the override | CODE-5; SPEC-1's final consumer paragraph |
| A consumer in `offline-only` holding a cache warmed before the upgrade | Refuses those artifacts with no in-product recovery until the cache directory is cleared. 0026 already mandates that clearing in this release | `CHANGELOG.md` per DOC-1; proposal 0026's DOC-1 item 4 |
| `podium verify --provider noop` | Always refuses. It previously reported success for a value the CLI could compute itself | `docs/deployment/operator-guide.md` per DOC-1 |
| `podium sign` with no provider flag and no registry key | Fails, because the default provider is now `registry-managed`. `--provider noop` still produces the placeholder | `docs/reference/cli.md` per DOC-1 |
| A registry started with `PODIUM_SIGN=none` against a consumer on the `always` default | Every load fails with `materialize.signature_missing`. The pairing is a misconfiguration and the error names it | SPEC-5's §13.12 subsection |
| A rotated registry-managed signing key | Every prior envelope stops verifying, permanently. 0026's first-start rewrite leaves such a row untouched and does not run again, and the repair is the rebuild: delete the tenant's manifest rows and re-ingest each layer | SPEC-1's rotation paragraph; OQ-2 |
| An envelope carrying no `key_id` against a consumer that pinned one | Verifies. The pin is skipped when either side is empty, which is unchanged here | SPEC-3's `PODIUM_SIGNATURE_KEY_ID` row; OQ-2 |
| A `sigstore-keyless` envelope | Verified against the configured trust root, with no signer-identity check and no transparency-log entry check, and refused once the certificate's validity window has passed. No registry signing mode produces one | SPEC-1's keyless bullet; carried to the follow-up proposal in Non-goals |
| `podium sync` and both SDKs | Verify no signature at any setting, unchanged. `docs/deployment/operator-guide.md` already says so | `docs/deployment/operator-guide.md` per DOC-1 |
| A `registry.yaml`-configured deployment that never set `PODIUM_SIGN` | Starts signing. No operator action is required, because the keypair is generated on first run, and a consumer that does not verify ignores the field | SPEC-5's §13.12 subsection |
| A store holding signed rows, upgraded with no signing variable set and no key file at `PODIUM_SIGN_KEY_PATH` | The start is refused by the §13.4 generated-key rule, which the default now reaches. The operator restores the key that signed the rows; `PODIUM_SIGN=none` is refused as well while signed rows await the rewrite | §13.4; `CHANGELOG.md` per DOC-1 |
| A target registry started after `podium admin migrate-to-standard` without the source's signing key | The start is refused by the same rule, because the copy keeps the stored signatures and clears the rewrite record. The operator copies the key file to the target's `PODIUM_SIGN_KEY_PATH` before the first start | §13.4; the migration pages per DOC-1 |
| A Helm install or upgrade with no signing Secret named | The render fails with a message naming `signing.secretName`, before any pod starts. The operator creates the Secret or sets `signing.mode=none` | CODE-8; `docs/deployment/clustered.md` per DOC-1; pinned by TEST-8 |
| Replicas of one Helm release | All sign under the one mounted key, so an envelope verifies whichever replica minted it | CODE-8 |

## Testing

**TEST-1 · unit, `pkg/sign/`.** `pkg/sign/sign_test.go` and `pkg/sign/policy_validate_test.go` are rewritten, because both express their expectations through the two behaviors CODE-1 and CODE-2 remove.

- `TestEnforceVerification_PresentSignatureVerifiedRegardlessOfSensitivity`: `medium-and-above`, `sensitivity: low`, and a signature that does not validate returns `ErrSignatureInvalid`. **Fails against today's code**, which returns nil before reaching the provider. `// Spec: §4.7.9`
- `TestEnforceVerification_MissingSignatureBelowThreshold`: `medium-and-above`, `low`, and an empty signature returns nil. Pins the surviving rollout affordance.
- `TestEnforceVerification_MissingSignatureUnderAlways`: `always`, `low`, and an empty signature returns `ErrSignatureMissing`. Carries the `// Matrix: §6.10 (materialize.signature_missing)` citation the existing file holds.
- `TestEnforceVerification_NeverSkipsAPresentInvalidSignature`: `never` with an invalid signature returns nil.
- `TestEnforceVerification_UnknownPolicyFailsClosed`: an unrecognized policy with an empty signature returns `ErrSignatureMissing`, and with an invalid signature returns `ErrSignatureInvalid`. **Fails against today's code** in its second half, where the existing case asserts a `noop:` envelope passes.
- `TestNoopVerifyRefusesItsOwnSignature`: `Noop{}.Verify` refuses the value `Noop{}.Sign` produced for the same hash. **Fails against today's code.**
- `TestNoopSignUnchanged`: `Sign` still returns `"noop:" + hash`, which the §8.3 audit anchor depends on.

**TEST-2 · unit, `cmd/podium-mcp/`.** A table over `deliverLoadArtifact` with a stub provider recording call order.

- `TestDeliverLoadArtifact_SignatureCheckedBeforeResourceFetch`: a response whose signature is invalid and whose bytes also mismatch returns `materialize.signature_invalid`, and no presigned resource URL was requested. **Fails against today's code** in the fetch half.
- `TestDeliverLoadArtifact_ManifestGatesRunAfterContentHash`: a response with a mismatched hash and an unsupported `sandbox_profile` returns `materialize.content_hash_mismatch`. **Fails against today's code**, which returns the sandbox error.
- `TestDeliverLoadArtifact_NoAuditEventOnRefusedLoad`: a load refused by the content-hash check emits no `artifact.loaded` event on the local sink. **Fails against today's code.**
- `TestDeliverLoadArtifact_MissingSignatureCarriesItsOwnCode`: an enforcing policy with an empty `signature` returns an error whose leading code is `materialize.signature_missing`. **Fails against today's code**, which leads with `materialize.signature_invalid`. `// Matrix: §6.10 (materialize.signature_missing)`

**TEST-3 · unit; the newly registered §6.10 cells.** One annotated test per cell added in CODE-7, each asserting the code a client observes: `config.signature_provider_unavailable` in `cmd/podium-mcp/` on a startup refusal under an enforcing policy with no verification material; `ingest.sign_failed` in `pkg/registry/ingest/` with a signer that returns an error, asserting the artifact lands in `res.Rejected` while the rest of the batch is accepted. `matrix-audit` fails without both.

**TEST-4 · unit and e2e; the producer default.** `internal/serverboot/`: `registrySignerFor("")` returns a provider, `registrySignerFor("none")` returns nil, and any other value refuses startup with `config.invalid_sign_mode`. **The first two fail against today's code.** The fixtures that leave `PODIUM_SIGN` unset to mean no signer set `none` instead: `internal/serverboot/rehash_test.go`, `internal/serverboot/rehash_boot_test.go`, `test/e2e/boot_rehash_test.go`, `TestRegistrySignerFor` in `internal/serverboot/webui_sign_config_test.go`, and `cmd/podium/serve_flags_test.go`. The boot refusal case asserts the error names `PODIUM_SIGN=none`, and a new case in `rehash_boot_test.go` starts with no signing variable set, no key file, and a signed stored row, and asserts the generated-key refusal, which is the default's new reach. `test/e2e/`: a zero-flag standalone server ingests a fixture layer and `load_artifact` returns a non-empty `signature`. **Fails against today's code.** The e2e case runs on Linux and macOS, and the existing signing tests skip on neither.

**TEST-5 · unit and e2e; the consumer defaults.** `cmd/podium-mcp/config_env_test.go` gains cases for the `always` fallback and the provider default, and for the startup refusals: an unrecognized provider refuses with `config.invalid`, and `noop` under an enforcing policy, `registry-managed` with no resolvable key, and `sigstore-keyless` with no readable trust root each refuse with `config.signature_provider_unavailable`. **All fail against today's code**, where the first two are per-load errors and the rest do not exist. The existing `sync.yaml`-precedence cases are extended rather than replaced, because an operator-set `never` must still win. `test/e2e/`: `TestSignedArtifact_NoVerifyKeyRefusesUnderTheDefaults` drives the binary with no signature environment at all and asserts a non-zero exit naming `config.signature_provider_unavailable`; the subprocess coverage caveat applies, so it runs under `GOCOVERDIR`.

**TEST-6 · e2e; the standalone journey.** In `test/e2e/`, on Linux and macOS:

- `TestStandaloneBootstrap_WritesTheKeyPathAndNoPolicy`: a fresh `HOME`, a zero-flag `podium serve`, then assert `~/.podium/sync.yaml` carries `defaults.registry` and `defaults.signature_verify_key_path` and no `verify_signatures` key. **Fails against today's code.**
- `TestStandaloneFirstRun_LoadsUnderTheAlwaysDefault`: the journey. A zero-flag server with a `--layer-path` fixture, then a `load_artifact` through `podium-mcp` with no signature environment, against a `medium` artifact. It must load. This is the guard that catches the consumer defaults landing without the producer default or the key handover.
- `TestStandaloneBootstrap_PreservesAnExistingNever`: a pre-existing `~/.podium/sync.yaml` carrying `verify_signatures: never` is not rewritten, the load still succeeds without verification, and the bridge logs the warning naming the file. Pins the accepted failure mode rather than leaving it undocumented.

**TEST-7 · e2e; the existing signed-artifact suite.** `test/e2e/signed_artifact_helpers_test.go` gains `TamperWireSensitivity(v string)` beside the existing `TamperContentHash` and `TamperBody`, mutating only the wire field so it can disagree with the frontmatter. `TestSignedArtifact_WireSensitivityDowngradeStillVerifies` then tampers the content hash so the envelope no longer validates, sets the wire `sensitivity` to `low`, and asserts a refusal with `materialize.signature_invalid` under `medium-and-above`. **Fails against today's code**, which loads the artifact cleanly. `TestSignedArtifact_LowSensitivitySkipsVerification` is renamed to `..._LowSensitivityStillVerifiesAPresentSignature` and re-pointed: an untampered low-sensitivity artifact still loads, and a tampered one no longer does. The suite's fixtures already set the provider and key explicitly, so the default flip does not disturb them.

**Audit before implementing S10.** Enumerate the tests under `test/e2e/` and `test/integration/` that boot a standalone server and then run `mcpExec` under the same `t.Name()`-keyed isolated home, and that carry a `sensitivity: medium` or `high` fixture. Those inherit the bootstrapped `sync.yaml` and begin enforcing.

**TEST-8 · unit; the chart's signing key.** `test/chart/render_test.go`, in the style of `TestChart_RuntimeKeysPathNamesAFileInsideTheMount`, skipping when `helm` is absent as the file's other cases do. A render with `signing.secretName` set carries `PODIUM_SIGN=registry-key`, a `PODIUM_SIGN_KEY_PATH` naming a file inside the mount, a read-only mount, and a volume from that Secret. A render with default values fails and the error names `signing.secretName`. A render with `signing.mode=none` carries `PODIUM_SIGN=none` and no signing mount, volume, or path variable. A render with an unknown mode fails. The file's existing cases that render with default values pass `signing.secretName`, and `TestChart_DefaultInstallOverridesNoSecretSuppliedKey` keeps its meaning by passing it too.

## Manual validation

`test/manual-validation.md` ends at S64 today, which proposal 0026 added, and gains S65 through S67 from proposal 0027, so these are S68 through S72. The numbers follow landing order and are reassigned at application time if that order changes. Each follows the document's per-scenario isolation block, numbered steps, and per-step **Expect** block.

- **S68 — A registry that declares `low` cannot skip verification.** Run a registry serving a signed `medium` artifact, edit the served envelope's `sensitivity` to `low` by hand through a proxy, and load through `podium-mcp` under `medium-and-above`. The surface a human reads is the tool error envelope. The wrong output it catches is a successful load, which is what the suite reports today.
- **S69 — First run verifies with no configuration.** `podium serve` on a clean `HOME`, then a `podium-mcp` load. The human reads `~/.podium/sync.yaml` for the absence of `verify_signatures` and the presence of `signature_verify_key_path`, and reads the successful load. Catches the consumer defaults shipping without the producer default or the key handover, which an automated suite passes when its fixtures set the key explicitly.
- **S70 — Signing on an existing registry needs the rebuild.** Ingest with `PODIUM_SIGN=none`, restart with the default, run `podium layer reingest`, and read the still-empty `signature` off `load_artifact`; then delete the tenant's manifest rows and re-ingest each layer, and read a non-empty one. Catches an implementation that claims the re-ingest alone repairs the store.
- **S71 — `podium verify --provider noop` refuses.** Catches a reintroduced permissive `Noop.Verify` at the surface an operator types.
- **S72 — A stale `never` is announced.** A machine carrying `verify_signatures: never` from an earlier standalone run loads without verification and prints the startup warning naming the file. The human reads the bridge's stderr. Catches the warning regressing to silence, which no assertion on a load result can see.

## Documentation changes

**DOC-1.** The pages below state the current defaults or the current failure narrative and become wrong.

- `docs/deployment/progressive-adoption.md` — the paragraphs describing the default chain, including the claim that every artifact at this stage is `sensitivity: low` so no policy triggers a check, which is exactly what stops being true for a signed artifact.
- `docs/deployment/operator-guide.md` — the `podium verify --provider` paragraph describing the noop acceptance; the sensitivity-enforcement row, whose fleet-wide hunt for `never` becomes a one-machine cleanup the bridge now announces; the `materialize.signature_invalid` troubleshooting bullet, which gains the startup refusal and the missing-key cause; and a new bullet for `materialize.signature_missing` and for the refused load that records no local read event.
- `docs/deployment/clustered.md` — the sentence naming `medium-and-above` as the typical setting; the `helm install` command gains `--set signing.secretName=...`, preceded by the commands that generate a key file with a standalone registry's first start and create the Secret from it; a sentence states that an upgrade of a store holding signed rows uses the key that signed them, and that `signing.mode=none` turns signing off. The chart-install scenario in `test/manual-validation.md` gains the same Secret step.
- The migration sections of `docs/deployment/single-node.md` and `docs/deployment/clustered.md`, and `docs/reference/cli.md`'s `podium admin migrate-to-standard` section — a step that copies the registry signing key to the target's `PODIUM_SIGN_KEY_PATH` before the target's first start, because a migrated store is signed by default and the target refuses to start without the key.
- `docs/deployment/extending.md` — the `SignatureProvider` row, including the claim that registry-side ingest signing is off unless the registry starts with `--sign registry-key`.
- `docs/reference/cli.md` — the `--sign` flag's accepted values, the `--provider` defaults on `podium sign` and `podium verify`, and the `PODIUM_VERIFY_SIGNATURES` default.
- `docs/reference/error-codes.md` — the `materialize.signature_missing` description, which cites the old default policy; a `config.signature_provider_unavailable` row for the startup refusal; and spec cross-references on `materialize.content_hash_mismatch` and `ingest.sign_failed`.
- `docs/getting-started/concepts.md` and `docs/deployment/access-control.md` — one line each naming where the policy applies.
- `OPERATIONS.md` — the signing environment block, which documents the consumer-side variables the spec now carries.
- `CHANGELOG.md` — a `Changed` entry naming the default flips, the registry-before-consumers roll order, and the machines that keep a stale `never`; a `Fixed` entry for the verification bypass and the reordering; a note that the first-start rewrite this release already performs for the content-hash change is also what attaches envelopes to stored rows, written as an extension of 0026's upgrade note; and a statement that a registry enabling signing after that start needs the rebuild.

`grep -n -i sign tools/doccov/manifest.yaml` returns nothing, and none of these edits adds a runnable example, so no `tools/doccov/manifest.yaml` entry or doc-e2e test is created.

## Relationship to proposal 0026

0026 length-frames the §4.7.6 content hash, which moves every stored digest and therefore invalidates every stored envelope, since each envelope is over the digest string. Its migration is a rewrite the registry performs once, on the first start of the new version, before it ingests or serves. The pass recomputes each stored row from the bytes the registry holds and, where a signer is configured, re-signs every row it rewrites and signs a stored row that carries no signature. It leaves untouched a row whose stored envelope does not verify under the configured key. It refuses the start, writing nothing, when a row it would rewrite is signed and no signer is configured, or when the signing key was generated at that start while signed rows fail verification. A marker records completion and nothing runs the pass again. This proposal ships in the same release because that start is the one event that signs stored rows: with the producer default flipped beside it every stored row leaves the upgrade signed, and a default flipped later would sign nothing already stored. 0026's mandated consumer cache clearing is also what removes the stale envelopes a warmed cache would otherwise replay through the stricter gate. 0026's OQ-1 is resolved in favour of the first-start rewrite.

0026 is implemented. The pass uses the boot path's signer, and the signing-mode test is the single function `registrySigningEnabled`, so CODE-4's change reaches the loader, the pass, and both start refusals with no second edit; the no-signer refusal is keyed on a nil provider, which `none` still produces. CODE-4 and TEST-4 own what remains: the fixtures in 0026's tests that read an unset `PODIUM_SIGN` as no signer, the no-signer refusal's error wording, and the test for the generated-key refusal under the new default. `podium admin migrate-to-standard` copies stored signatures unchanged and clears the target's rewrite record, so under this proposal's default the target registry's first start is refused until the source's signing key is at the target's `PODIUM_SIGN_KEY_PATH`; the edge-case table and DOC-1 state it. CODE-4 also states how 0026's accepted-failure text for a `signature_unverified` row is re-read against CODE-3's reordered `deliverLoadArtifact`. A registry that turns signing on after its marker is set is this proposal's case; SPEC-5's §13.12 text states the rebuild as its repair, and OQ-5 carries a re-triggerable signing pass.

`test/e2e/signed_artifact_helpers_test.go` is edited by both. 0026 owns the migration of its `version.ContentHash` call; TEST-7 adds the wire-sensitivity knob on top.

## Relationship to proposal 0027

0027 adds a registry delivery attestation over the merged served record, drops `raw_frontmatter`, and closes the §4.6 hidden-parent disclosures. Three contacts, none of them a sequencing constraint in either direction.

- 0027's attestation is a second envelope over different bytes. The two-axis policy extends to it unchanged: verify what is present, and let the policy govern what is required. The sentence saying which envelope each policy value requires belongs to 0027; SPEC-1 reserves the slot by scoping its policy paragraph to the §4.7.9 signature.
- 0027's attestation covers the merged record, so it is the only mechanism that could make a child's effective sensitivity attestable. That does not revive the conditional trigger, because the attesting party would be the registry, which is the party the trigger defends against.
- Dropping `raw_frontmatter` removes the `resp.ManifestMerged` branch inside `verifyContentHash`. The ordering staged here is indifferent to what that function recomputes over.

## Open questions

1. **Does the consumer-facing default flip ship in the same release as the producer default?** The checklist orders S9 before S10 and this document assumes one release, because splitting them across releases leaves an interval in which the store is signed and no consumer checks, which is the status quo, and because 0026's first-start rewrite runs once. A reviewer who wants the consumer flip held to the following MINOR gets a safer rollout and a second coordinated upgrade for operators. S10 is severable without touching S7 through S9.
2. **Is the rotation limitation acceptable as documented behavior?** §4.7.9 currently claims quarterly rotation and SPEC-1 replaces that with the truth: a rotation invalidates every prior envelope and is repaired only by the rebuild, because 0026's first-start rewrite leaves untouched a row whose envelope does not verify under the configured key and does not run again. The alternatives are a verification key set, which is a provider redesign, and a re-triggerable signing pass that trusts a retired key, which is a key set by another name. Neither is staged here.
3. **Should the bootstrap point at the keypair file or a separate public half?** CODE-4 writes a `.pub` beside the keypair so the consumer is never handed a file containing the private key, at the cost of a second file on disk. Pointing `signature_verify_key_path` at the keypair itself would work, since the consumer reads only the public line, and is rejected here on that ground alone.
4. **Which release carries the follow-up Sigstore proposal?** It is latent today, and it stops being latent the moment a registry signing mode mints a keyless envelope. The two should be scheduled together.
5. **Should a later proposal add a re-triggerable signing pass?** 0026's `RehashManifest` can write a fresh envelope onto a stored row, and only 0026's marker-gated first-start rewrite drives it. A registry that enables signing after that start has the rebuild as its only repair. A pass an operator can trigger again would remove that, at the cost of a trigger surface and of the marker semantics 0026 declined.
6. **How does the Helm chart hold the signing key under the new default?** Resolved: the chart mounts an operator-supplied Secret at `PODIUM_SIGN_KEY_PATH` and refuses to render without one unless `signing.mode` is `none` (CODE-8). A chart default of `none` was rejected because it would leave clustered deployments, which most need verification, unsigned by default.

## Non-goals

- **Generating the signing key inside the cluster.** A pre-install hook Job that mints the key and writes the Secret would remove one operator step, and it would need cluster RBAC to create Secrets and would mint a fresh key on a reinstall over a store whose rows an earlier key signed. The operator supplies the Secret.

- **Sigstore verification correctness.** The certificate-identity policy, verification at the transparency log's attested time rather than at wall-clock time, entry binding, inclusion-proof and signed-entry-timestamp verification, refusing an envelope that declares `log_index: -1`, refusing a digest algorithm other than `sha256`, and a versioned self-describing envelope are a separate proposal. Nothing in the product mints a Sigstore envelope, so every one of those defects is latent, and the fix carries a dependency decision about adopting an upstream Sigstore library that must be taken on its own terms. What lands here is the §4.7.9 text that stops describing the model as working.
- **A registry key-publication route with a first-use pin.** It would make `always` the default with no operator action on a non-standalone deployment. It is also a trust-on-first-use mechanism with its own security semantics, its own `sync.yaml` state, and its own re-pinning flow, and adding it here would land it unreviewed inside a rollout proposal.
- **A verification key set and non-destructive rotation.** See OQ-2.
- **A re-triggerable pass that signs already-stored rows.** 0026's `RehashManifest` writes a fresh envelope onto a stored row, and only 0026's marker-gated first-start rewrite drives it. This proposal adds no second driver, no second store method, and no trigger. The same-release constraint makes one unnecessary for the upgrade, and OQ-5 carries the question for a registry that enables signing later.
- **Signature verification in `podium sync` or the SDKs.** `EnforceVerification` has one production caller, and that bound is unchanged. The operator guide already states it.
- **A grace policy value, a legacy provider alias, or an exemption for artifacts ingested before the upgrade.** Podium is pre-1.0 and each is a second behavior retained beside the first, which `.claude/rules/code-best-practices.md` forbids.
- **Rescoping `bootstrapStandaloneFiles` away from the explicit-server-config branch.** Deleting the relaxation line removes the leak this proposal cares about, and the bootstrap's other writes are benign.
