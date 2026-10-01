# Proposal 0031: State the policy condition on the malformed PODIUM_SIGNATURE_VERIFY_KEY refusal, and keep the §13.12 key-persistence rule scoped to the backends the spec defines

- Issue: (to be filed)
- Status: Approved (2026-10-01). Signed off as staged; OQ-1 resolved: keep CODE-1, no §13.12 sentence.
- Date: 2026-10-01

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §6.2: the `PODIUM_SIGNATURE_VERIFY_KEY` row limits the malformed-entry start refusal to a policy above `never`, matching the adjacent `PODIUM_SIGN_KEY_PATH` row and the §6.9 failure row (SPEC-1).
- §4.7.9: the verification-key-set paragraph limits the same start refusal to a consumer whose policy is above `never`, which removes the paragraph's contradiction with its own "Under `never` it resolves none" sentence and keeps "refuses the start" off `podium verify` (SPEC-2).
- `internal/serverboot/signing.go`: the `refuseUnpersistedSigningKey` doc comment states that the memory-store exemption covers a test backend that §13.12 does not list. The code is unchanged (CODE-1).
- `cmd/podium-mcp/config_env_test.go`: `TestLoadConfig_VerifierResolution` gains one row that pins a malformed `PODIUM_SIGNATURE_VERIFY_KEY` under `never` as a successful start with no verifier (TEST-1).
- `docs/consuming/configure-your-harness.md`: the `PODIUM_SIGNATURE_VERIFY_KEY` row states the policy condition (DOC-1).
- `docs/reference/cli.md`: the Environment variables `PODIUM_SIGNATURE_VERIFY_KEY` row keeps the malformed-entry error unconditional for `podium verify` and conditions it on a policy above `never` for `podium-mcp` (DOC-2).
- `CHANGELOG.md` `[Unreleased]`: the `PODIUM_SIGNATURE_VERIFY_KEY` entry attaches the policy condition to the `podium-mcp` start alone, so it no longer puts `podium verify` under a policy it does not have (DOC-3).

**Fixed decisions.**

- The proposal changes no behavior. The code already implements the conditioned rule in `cmd/podium-mcp/main.go` (`resolveVerifier`) and `cmd/podium/sign.go` (`registryManagedVerifier`).
- The condition is worded "a policy above `never`", the phrase §6.2 and §6.9 already use. No new term is introduced.
- §4.7.9 gains a qualifier on the start refusal only. No `podium verify` clause is added, because the last paragraph of §4.7.9 already states how that invocation fails.
- SPEC-1 and SPEC-2 carry no provider qualifier, because the §6.2 row and the §4.7.9 paragraph already concern `registry-managed` verification.
- The §13.12 key-persistence rule, the `PODIUM_REGISTRY_STORE` value list, the operator guide, and the `CHANGELOG.md` restatement of the key-persistence rule are not edited. The memory store stays out of the spec and out of operator documentation. OQ-1 records the alternative of a §13.12 sentence in place of CODE-1, and the recommendation is to keep CODE-1.
- The §13.4 and §13.12 rewrite, admission, and unmigrated-store passages scoped to the SQLite store in the key file's directory stay as written.
- DOC-3 corrects the existing `[Unreleased]` entry in place. No CHANGELOG entry is added, and every released section is unchanged.
- The policy condition attaches to the `podium-mcp` start only. Every staged sentence that names `podium verify` (SPEC-2, DOC-2, and DOC-3) keeps the `podium verify` failure unconditional, because `cmd/podium` reads no `PODIUM_VERIFY_SIGNATURES` and `registryManagedVerifier` checks no policy.
- No error code, environment variable, flag, or `registry.yaml` key is added or changed.
- The §6.2 row gains no sentence saying the MCP server does not read the variable under `never`.

**Watch out for.**

- **The §6.2 trailing sentence.** An earlier draft added "Under `never` the MCP server does not read the variable." It is dropped on purpose: it restates §4.7.9 and §6.9, and it implies `never` is the only case that skips the variable, while `noop` and `sigstore-keyless` skip it too. Do not reintroduce it.
- **The §4.7.9 paragraph governs `podium verify` as well.** A rewrite that keeps "refuses the start" unqualified, or splits the sentence so the qualifier attaches to a different clause, leaves `podium verify` told to refuse a start it does not have. Insert the qualifier exactly where SPEC-2 places it and keep the "rather than falling through to the file" clause.
- **The spec line numbers in this proposal are anchors at the time of writing.** Locate each edit by its quoted current text, because later spec edits shift the lines.
- **TEST-1 must use the `registry-managed` provider and no key file.** Under `noop`, or with a resolvable key file, the row would pass even against a `resolveVerifier` that read the variable under `never`. With `registry-managed`, a malformed variable, and no key file, any resolution attempt refuses, so the row fails as soon as `never` stops short-circuiting.
- **Item (2) looks like a spec defect and is not one.** `refuseUnpersistedSigningKey` exempts `cfg.storeType == "memory"`, and §13.12 names no such exemption. §13.12 defines only `postgres` and `sqlite`, and `openStore` in `internal/serverboot/serverboot.go` labels `memory` an undocumented test affordance. Adding the memory store to §13.12, the operator guide, or the CHANGELOG brings a test backend into the operator surface.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §6.2 `PODIUM_SIGNATURE_VERIFY_KEY` row conditions the malformed-entry start refusal on a policy above `never`.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §4.7.9 verification-key-set sentence conditions the start refusal on a consumer whose policy is above `never`.
      Levels: —. Depends on: —
- [ ] **S3 · code** — CODE-1. The `refuseUnpersistedSigningKey` doc comment names the memory store as a test backend that §13.12 does not list.
      Levels: unit. Depends on: —
- [ ] **S4 · test** — TEST-1. `TestLoadConfig_VerifierResolution` gains the `never` row with a malformed `PODIUM_SIGNATURE_VERIFY_KEY`, and its doc comment names the case.
      Levels: unit. Depends on: S1, S2
- [ ] **S5 · docs** — DOC-1. The configure-your-harness `PODIUM_SIGNATURE_VERIFY_KEY` row states the policy condition.
      Levels: —. Depends on: S1, S2
- [ ] **S6 · docs** — DOC-2. The `docs/reference/cli.md` Environment variables `PODIUM_SIGNATURE_VERIFY_KEY` row conditions the `podium-mcp` error on a policy above `never` and keeps the `podium verify` error unconditional.
      Levels: —. Depends on: S1, S2
- [ ] **S7 · docs** — DOC-3. The `CHANGELOG.md` `[Unreleased]` `PODIUM_SIGNATURE_VERIFY_KEY` entry attaches the policy condition to the `podium-mcp` start only.
      Levels: —. Depends on: S1, S2

**Ordering constraints.** S1 and S2 touch different spec files and are independent. S3 is a comment edit with no spec dependency. S4 pins the conditioned wording, so it follows the spec steps it cites. S5, S6, and S7 restate the spec text, so each follows S1 and S2.

## Current state and the gap

### The malformed verification key

The §6.2 `PODIUM_SIGNATURE_VERIFY_KEY` row states that an entry which is empty or does not decode to an Ed25519 public key "refuses the start with `config.signature_provider_unavailable`, naming the variable". It names no policy condition. The `PODIUM_SIGN_KEY_PATH` row directly below it and the §6.9 row "A policy above `never` and no verification material" both limit that refusal to a policy above `never`.

The code follows the two conditioned rows. `loadConfig` in `cmd/podium-mcp/main.go` calls `resolveVerifier`, which returns `nil, nil` under `sign.PolicyNever` before it calls `buildSignatureProvider`. `buildSignatureProvider` reaches `registryManagedVerifyKey`, the only podium-mcp reader of the variable, through its `registry-managed` arm alone. A malformed value under `never` therefore starts.

§4.7.9 carries a parallel unconditional sentence: "a set value with an entry that is empty or does not decode to an Ed25519 public key refuses the start with `config.signature_provider_unavailable`, naming the variable, rather than falling through to the file". The same paragraph later says "Under `never` it resolves none and verifies nothing", and closes with "This rule governs every party that verifies a delivery signature", which includes `podium verify`. `podium verify` has no verification policy and no start to refuse. `registryManagedVerifier` in `cmd/podium/sign.go` always resolves the key set and fails the invocation with `config.signature_provider_unavailable`, which the last paragraph of §4.7.9 already states ("An invocation that cannot resolve the key it needs fails with `config.signature_provider_unavailable`").

`docs/consuming/configure-your-harness.md` repeats the unconditional wording in its `PODIUM_SIGNATURE_VERIFY_KEY` row (line 35). The `docs/reference/cli.md` Environment variables table (heading at line 802) does the same: its `PODIUM_SIGNATURE_VERIFY_KEY` row (line 816) applies the variable to `podium-mcp` and `podium verify` and says "an entry that is empty or does not decode is an error naming it" with no policy qualifier, although `resolveVerifier` returns before the variable is read under `never` (`cmd/podium-mcp/main.go`, the `policy == sign.PolicyNever` early return). The `podium verify` passage of the same page (line 752, under the `### podium verify` heading at line 736) describes a command with no policy, so its unconditional wording is correct.

The `CHANGELOG.md` `[Unreleased]` entry "`PODIUM_SIGNATURE_VERIFY_KEY` takes a verification key set" (line 546) errs in the other direction. It reads "Under a policy above `never`, an entry that is empty or does not decode refuses the `podium-mcp` start, and fails `podium verify`, with `config.signature_provider_unavailable`, naming the variable", which puts the `podium verify` failure under a policy condition. `registryManagedVerifier` in `cmd/podium/sign.go` checks no policy, and no non-test file in `cmd/podium` reads `PODIUM_VERIFY_SIGNATURES`. The release workflow publishes this section verbatim as the GitHub Release body.

`docs/reference/error-codes.md` (line 88) and `docs/deployment/operator-guide.md` (line 235) already state the condition correctly.

No test pins the `never` case with a malformed key. The `never` rows of `TestLoadConfig_VerifierResolution` in `cmd/podium-mcp/config_env_test.go` set no `PODIUM_SIGNATURE_VERIFY_KEY`, and every malformed-key row runs under `always`.

### Key persistence and the memory store

§13.12 states, in the `PODIUM_SIGN_KEY_PATH` table cell and in the paragraph after the table, that a registry with signing on and `PODIUM_SIGN_KEY_PATH` unset refuses to start unless its store is the SQLite store in the directory the default key path resolves to. `refuseUnpersistedSigningKey` in `internal/serverboot/signing.go` also returns nil when `cfg.storeType == "memory"`.

§13.12 defines only `postgres` and `sqlite` for `PODIUM_REGISTRY_STORE`. `openStore` in `internal/serverboot/serverboot.go` labels `memory` "an undocumented test affordance" and logs a non-durable warning when it is selected. For every backend the spec defines, the §13.12 rule matches the code. The exemption is also safe: a memory store holds no row that outlives the process, and the generated key persists at the default path. The defect is a doc comment that cites §13.12 on a branch §13.12 does not state, without saying that the branch covers a backend outside the spec. A reader tracing the citation finds an apparent contradiction, which is how the item was reported.

## Decisions

- **No behavior change.** The code implements the conditioned rule, and the spec, the docs rows, and the `[Unreleased]` entry move to match it. The alternative, refusing a malformed value under `never`, would read a variable the consumer has been told it does not need and would contradict §4.7.9's "Under `never` it resolves none" and §6.9.
- **Reuse the existing phrase.** SPEC-1 and SPEC-2 use "a policy above `never`", which the §6.2 `PODIUM_SIGN_KEY_PATH` row and the §6.9 row already use.
- **Qualify the start refusal in §4.7.9, and only that clause.** The qualifier "of a consumer whose policy is above `never`" describes a start, so it excludes `podium verify` by construction, and the last paragraph of §4.7.9 already covers how `podium verify` fails. Splitting the sentence or adding a `podium verify` clause would restate that paragraph and change the "authoritative, no fall-through" wording without need.
- **No provider qualifier in SPEC-1 or SPEC-2.** The §6.2 row already scopes itself to `registry-managed` verification, and the §4.7.9 paragraph concerns the registry-managed key set throughout.
- **No spec edit for item (2).** `memory` is not a §13.12 backend, and naming it in the key-persistence rule would bring an unspecified backend into the spec. For every defined backend, the §13.12 rule matches `refuseUnpersistedSigningKey`.
- **No docs or CHANGELOG edit for item (2).** The operator guide and the `CHANGELOG.md` restatements of the key-persistence rule describe every documented store correctly. Naming the memory store there would advertise a test-only backend to operators.
- **The co-located-SQLite rewrite and admission passages stay as written.** The §13.4 and §13.12 passages scoped to "the SQLite store in the key file's directory" are a separate rule. `refuseUnmigratedStore` in `internal/serverboot/rehash.go` has no memory branch and passes only because a fresh memory store is empty.
- **No new error code, environment variable, flag, or config key.** `config.signature_provider_unavailable` and its §6.10 entry are unchanged.
- **Released CHANGELOG sections stay untouched.** DOC-3 edits an existing `[Unreleased]` entry and adds none. Every released section is unchanged.

## Spec amendment: §6.2 PODIUM_SIGNATURE_VERIFY_KEY malformed-entry refusal

**SPEC-1.** Anchor: `spec/06-mcp-server.md`, §6.2 Configuration, the configuration table row whose first cell is `` `PODIUM_SIGNATURE_VERIFY_KEY` `` (line 36 at the time of writing).

In the row's description cell, replace the sentence:

> The variable is authoritative, and an entry that is empty or does not decode to an Ed25519 public key refuses the start with `config.signature_provider_unavailable`, naming the variable.

with:

> The variable is authoritative. Under a policy above `never`, an entry that is empty or does not decode to an Ed25519 public key refuses the start with `config.signature_provider_unavailable`, naming the variable (§4.7.9, §6.9).

The rest of the row, including "The registry's §4.7.9 verification key set for `registry-managed` verification: ...", "Environment only; no flag or config-file key", and the default cell, is unchanged. No sentence about which policies or providers read the variable is added.

## Spec amendment: §4.7.9 malformed-entry refusal scope

**SPEC-2.** Anchor: `spec/04-artifact-model.md`, §4.7.9 Signing, the paragraph that begins "The consumer verifies the signature it is served under the registry's verification key set" (line 920 at the time of writing).

Replace the sentence:

> A set `PODIUM_SIGNATURE_VERIFY_KEY` is authoritative: the key file is consulted only when the variable is unset, and a set value with an entry that is empty or does not decode to an Ed25519 public key refuses the start with `config.signature_provider_unavailable`, naming the variable, rather than falling through to the file.

with:

> A set `PODIUM_SIGNATURE_VERIFY_KEY` is authoritative: the key file is consulted only when the variable is unset, and a set value with an entry that is empty or does not decode to an Ed25519 public key refuses the start of a consumer whose policy is above `never` with `config.signature_provider_unavailable`, naming the variable, rather than falling through to the file.

The later sentences of the paragraph ("A consumer resolves this material once, at startup, ...", "Under `never` it resolves none and verifies nothing.", "No configuration file carries key material.", and "This rule governs every party that verifies a delivery signature.") are unchanged. The last paragraph of §4.7.9, which states that a `podium verify` invocation that cannot resolve its key fails with `config.signature_provider_unavailable`, is unchanged.

## Proposed solution

**CODE-1.** `internal/serverboot/signing.go`, the `refuseUnpersistedSigningKey` doc comment. Replace the clause

```go
// then re-signs them under the current key so the line can be removed. A
// memory store persists nothing and so strands no signature;
```

with

```go
// then re-signs them under the current key so the line can be removed. A
// memory store, a test backend that §13.12 does not list (see openStore),
// persists nothing and so strands no signature;
```

The rest of the comment, the `Spec: §13.12, §4.7.9.` citation, the function body, and `internal/serverboot/signing_test.go` are unchanged. Run `gofmt` and `go build ./internal/serverboot/`.

**TEST-1.** See Testing.

The proposal makes no other code change. `resolveVerifier`, `buildSignatureProvider`, `registryManagedVerifyKey`, `registryManagedVerifier`, `sign.VerificationKeys`, and `refuseUnpersistedSigningKey` keep their behavior.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `PODIUM_VERIFY_SIGNATURES=never` with a malformed or empty `PODIUM_SIGNATURE_VERIFY_KEY` | `podium-mcp` starts with no verifier and verifies nothing; the value is never parsed | SPEC-1, SPEC-2, and §4.7.9 "Under `never` it resolves none and verifies nothing"; `docs/consuming/configure-your-harness.md` (DOC-1), `docs/reference/cli.md` (DOC-2), `docs/deployment/operator-guide.md` |
| A consumer that started under `never` with a malformed value later switches to `always` | The next start refuses with `config.signature_provider_unavailable`, naming the variable | SPEC-1; §6.9 "A policy above `never` and no verification material"; `docs/reference/error-codes.md` |
| `always` with a malformed `PODIUM_SIGNATURE_VERIFY_KEY` under `PODIUM_SIGNATURE_PROVIDER=noop` | The start refuses with `config.signature_provider_unavailable`, naming `noop`; the variable is not read | §6.9 row, which applies to `noop` under any policy above `never`; `docs/consuming/configure-your-harness.md` `PODIUM_SIGNATURE_PROVIDER` row |
| `always` with a malformed `PODIUM_SIGNATURE_VERIFY_KEY` under `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless` | The variable is not read; the start depends on `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` alone | §6.2 `PODIUM_SIGNATURE_VERIFY_KEY` row, which scopes the variable to `registry-managed` verification, and the `PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE` row; `docs/consuming/configure-your-harness.md` |
| `podium verify` with a malformed `PODIUM_SIGNATURE_VERIFY_KEY` | The invocation fails with `config.signature_provider_unavailable`, naming the variable; there is no start and no policy | Last paragraph of §4.7.9; `docs/reference/cli.md` (`podium verify` passage and the DOC-2 row); `CHANGELOG.md` `[Unreleased]` (DOC-3) |
| A registry with signing on, `PODIUM_SIGN_KEY_PATH` unset, and `PODIUM_REGISTRY_STORE=memory` | The start proceeds, generates a key at the default path, and logs the non-durable store warning; no row outlives the process | No spec text by design, because §13.12 does not define the memory backend; recorded in the `refuseUnpersistedSigningKey` and `openStore` comments (CODE-1). No docs page states it, because the backend is a test affordance |

## Testing

**TEST-1 · unit, `cmd/podium-mcp/config_env_test.go`, `TestLoadConfig_VerifierResolution`.** Lands in S4.

- Add exactly one row to the cases table, immediately after the row named `never with noop`:

  ```go
  {
  	name: "never with a malformed verify key", policy: "never", provider: "registry-managed",
  	env:  map[string]string{"PODIUM_SIGNATURE_VERIFY_KEY": "!!!not base64"},
  	want: outcome{},
  },
  ```

  The row sets no `keyFile` and no `bodyFile`. `want: outcome{}` asserts that `loadConfig` returns no error and a nil verifier. The row fails against a `resolveVerifier` that resolves the provider before it checks the policy, because the malformed variable is authoritative and the absent key file leaves no fallback, so any resolution attempt refuses with `config.signature_provider_unavailable`.
- In the function's doc comment, extend the clause "never resolves no material" to "never resolves no material, even from a malformed PODIUM_SIGNATURE_VERIFY_KEY". The comment's existing citation of §4.7.9, §6.2, and §6.9 carries the spec tie, so `speccov-drift` needs no new citation.
- Add no second `never` row with an empty entry or a trailing comma. `resolveVerifier` returns before the variable is read, so a second malformed form runs the same path.
- Run `go test ./cmd/podium-mcp/ -run TestLoadConfig_VerifierResolution` and `make coverage-gate`.

CODE-1 is a comment edit. `go build ./internal/serverboot/`, `go test ./internal/serverboot/ -run TestRefuseUnpersistedSigningKey`, and `make lint` confirm it.

No end-to-end test is added. The change alters no behavior, and the startup refusal under a policy above `never` keeps its existing coverage in `TestLoadConfig_VerifierResolution` and the end-to-end suite.

## Manual validation

No manual validation scenario is staged. The change alters no behavior, so no consumer or operator observes a different outcome after this proposal lands.

## Documentation changes

**DOC-1.** `docs/consuming/configure-your-harness.md`, the configuration table row whose first cell is `` `PODIUM_SIGNATURE_VERIFY_KEY` `` (line 35 at the time of writing). Replace the sentence

> When set it is authoritative, and an entry that is empty or does not decode refuses the start.

with

> When set it is authoritative, and under a policy above `never` an entry that is empty or does not decode refuses the start.

Add no error code and no "naming the variable" clause: the paragraph after the table names `config.signature_provider_unavailable` for the start refusal, and `docs/reference/error-codes.md` lists the malformed-entry case under that code. The page is listed in `tools/doccov/manifest.yaml`, and the edit changes prose only, with no runnable block.

**DOC-2.** `docs/reference/cli.md`, the Environment variables table row whose first cell is `` `PODIUM_SIGNATURE_VERIFY_KEY` `` (line 816 at the time of writing). Replace the sentence

> When set it is authoritative, and an entry that is empty or does not decode is an error naming it.

with

> When set it is authoritative, and an entry that is empty or does not decode is an error naming it in `podium verify`, and in `podium-mcp` under a policy above `never`.

The rest of the row is unchanged. The `podium verify` passage at line 752 is unchanged, because that command has no policy. The page is listed in `tools/doccov/manifest.yaml` (slug `D-cli`), and the edit changes prose in a table row only, with no runnable block.

**DOC-3.** `CHANGELOG.md`, the `[Unreleased]` entry that begins "**`PODIUM_SIGNATURE_VERIFY_KEY` takes a verification key set**" (line 546 at the time of writing). Replace the sentence

> Under a policy above `never`, an entry that is empty or does not decode refuses the `podium-mcp` start, and fails `podium verify`, with `config.signature_provider_unavailable`, naming the variable.

with

> An entry that is empty or does not decode fails `podium verify`, and under a policy above `never` refuses the `podium-mcp` start, with `config.signature_provider_unavailable`, naming the variable.

Rewrap the entry to the file's existing line width. The rest of the entry and every released section are unchanged.

No other docs page changes. `docs/reference/error-codes.md` and `docs/deployment/operator-guide.md` already state the condition.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-01, automated)

- **`docs/reference/cli.md:816` states the malformed-key error unconditionally for `podium-mcp`.** The proposal had listed `cli.md` among pages that already state the condition. Fixed by adding DOC-2 and checklist step S6, which condition the `podium-mcp` error on a policy above `never` and keep the `podium verify` error unconditional, and by removing `cli.md` from the "already state the condition" lists in Current state and Documentation changes. The `podium verify` passage at line 752 stays unchanged.
- **The `CHANGELOG.md` `[Unreleased]` entry puts `podium verify` under a policy condition.** The proposal had said the entry already stated the conditioned rule. Fixed by adding DOC-3 and checklist step S7, which attach the condition to the `podium-mcp` start alone, by rewriting the "Released CHANGELOG sections" decision, by scoping the Fixed decisions line on `CHANGELOG.md` to the key-persistence restatement, by adding a Fixed decision that every staged `podium verify` sentence stays unconditional, and by replacing the Non-goal on CHANGELOG entries.

## Open questions

**OQ-1. Item (2): a spec sentence in place of CODE-1.** The recommendation is no spec edit, because `memory` is an undocumented test backend (`internal/serverboot/serverboot.go`, `openStore`) and §13.12 lists only `postgres` and `sqlite`. If the reviewer wants the spec to state the exemption anyway, the alternative is one sentence in §13.12 after the key-persistence paragraph, for example: "A store that persists no row, which no listed backend is, strands no signature and is outside this refusal." That sentence states the exemption without naming or endorsing the memory backend. Should that alternative replace CODE-1?

## Non-goals

- Changing the behavior of `resolveVerifier`, `registryManagedVerifier`, `sign.VerificationKeys`, or `refuseUnpersistedSigningKey`.
- Adding a policy gate to `podium verify`, which has no verification policy by design (last paragraph of §4.7.9).
- Defining the memory store as a §13.12 backend, or adding it to the `PODIUM_REGISTRY_STORE` value list.
- Editing the §13.12 key-persistence rule, the operator-guide troubleshooting bullet on it, or the `CHANGELOG.md` restatements of it.
- Widening the §13.4 and §13.12 rewrite, admission, and unmigrated-store passages scoped to the SQLite store in the key file's directory.
- Replay protection for delivery records, and delivery verification beyond `podium-mcp`. These are separate items in the same request and get their own proposals.
- Adding a new CHANGELOG entry. The change alters no user-visible behavior, and DOC-3 corrects the existing `[Unreleased]` entry in place.
