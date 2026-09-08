# Proposal 0024: Refuse startup on a malformed `PODIUM_IDP_GROUP_MAPPING`, and state the variable in §13.12

- Issue: (to be filed)
- Status: Approved (2026-09-08). Verified after 10 adversarial review rounds (6 findings fixed).
- Date: 2026-09-08

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §13.12's identity-provider table gains one row for `PODIUM_IDP_GROUP_MAPPING`, stating its syntax, that it has no config-file key, that a claim value with no entry passes through, and that a non-empty value which does not resolve to a table fails startup with `config.invalid_idp_group_mapping` under every identity provider. `spec/13-deployment.md` states no group-mapping variable today.
- §6.3.1 gains one clause naming `PODIUM_IDP_GROUP_MAPPING` as what supplies the adapter's table and naming `config.invalid_idp_group_mapping`, which closes the dangling "per a registry-side configuration" reference in `spec/06-mcp-server.md`.
- `internal/serverboot/serverboot.go` records the group-mapping parse failure on the `Config` at the parse site instead of logging it, and `(*Config).validate` returns `config.invalid_idp_group_mapping`. The `log.Printf("warning: ignored PODIUM_IDP_GROUP_MAPPING: %v", err)` line is removed.
- A unit test in package `serverboot` drives `LoadConfig` and `validate` over the refused and the accepted settings, carrying the `// Spec:` and `// Matrix:` annotations.
- An end-to-end test in `test/e2e/auth_gateway_test.go` asserts the refusal through the built binary with no identity-provider variable set, and asserts that `podium config show --server` still exits 0 on a malformed value.
- `docs/deployment/oidc/google-workspace.md` and `docs/deployment/oidc/entra-id.md` replace the text stating that a malformed entry is logged and dropped, and the replaced span on `google-workspace.md` reaches back over the adjacent false claim that the registry reads the top-level `groups` claim only. `CHANGELOG.md` records the behavior change, and S33 in `test/manual-validation.md` gains a refusal step and names the code on its "Covers" line.

**Fixed decisions.**

- The spec lands and is verified before the code. §6.3.1 defines no configuration surface for the table today, so the current pass-through is a specification gap and the refusal is not enforceable until §13.12 names the variable and its failure mode.
- One new §6.10 code, `config.invalid_idp_group_mapping`. No new variable, no new flag, and no `registry.yaml` key.
- The refusal covers a non-empty value carrying a malformed entry and a non-empty value that resolves to no `claim=group` entry, including a separators-only value and a whitespace-only value. An unset or empty variable stays the legitimate no-mapping state. The predicate turns on the value being non-empty rather than on the variable being set, because `os.Getenv` cannot distinguish an unset variable from an empty one and the parse-site guard is written on the value.
- The refusal applies under every identity provider, including `trusted-headers` and public mode, matching `config.runtime_keys_unavailable`.
- `pkg/identity.ParseIdpGroupMapping` is not changed. The defect is confined to its single production caller.
- The refusal is reported from `(*Config).validate` rather than from `LoadConfig`. `LoadConfig` has no error channel and is shared with `podium config show --server`.
- `podium config show --server` keeps working on a malformed value and its `idp_group_mapping` row is not changed. The row's source column already names `PODIUM_IDP_GROUP_MAPPING` whenever the value is non-empty, which is what distinguishes a malformed setting from an unset one.
- The startup error echoes the raw setting through the parser's message. Claim values are configuration rather than secrets. `config show` keeps withholding the claim-to-group pairs.
- Podium is pre-1.0. No flag, environment variable, or configuration key restores the log-and-continue behavior, and no dual code path is added.

**Watch out for.**

- **The whitespace-only value is the boundary the two halves of this change meet on, and the first draft got it wrong.** Do not rewrite the parse-site guard to `strings.TrimSpace(spec) != ""`. That would exempt `PODIUM_IDP_GROUP_MAPPING=" "` from the very refusal the staged §13.12 row states, while `" , "` is refused, and the two are the same operator mistake. The guard stays `spec != ""`, so a whitespace-only value reaches the parser, resolves to an empty table, and is refused. Both TEST-1 and TEST-2 carry that arm so the divergence cannot land unpinned.
- **`LoadConfig` cannot fail.** It returns `*Config` with no error and `podium config show --server` calls it without calling `validate` (`cmd/podium/config.go:62`). A `log.Fatal` at the parse site would abort the diagnostic command, and changing the signature would touch a second caller for no gain. Record the error on the `Config` and report it from `validate`.
- **`validate` is called once, and that is enough.** `internal/serverboot/serverboot.go:796` inside `run` is the single call, and the exported `Run` that reaches `run` is the single entry point for both `podium-server` (`cmd/podium-server/main.go:20`) and `podium serve` (`cmd/podium/serve.go:105`), so the refusal covers every registry deployment mode with no second gate.
- **The parser already rejects the whole specification on one malformed entry** (`pkg/identity/group_mapping.go:56-58`), so a single typo discards every well-formed entry beside it. That is what makes the silent drop dangerous, and it is not changed here.
- **`config show --server` already discriminates the malformed case, through the source column rather than the value.** `envOrSrc` (`internal/serverboot/serverboot.go:1984-1989`) returns the variable name whenever `os.Getenv` is non-empty, so a malformed setting renders `idp_group_mapping | (empty) | PODIUM_IDP_GROUP_MAPPING` and an unset one renders `... | default`. Do not add an `invalid` verdict string to that column; the dropped-alternatives Non-goal records why.
- **Do not copy the fixture from `test/e2e/auth_idp_group_mapping_test.go`.** That test selects `injected-session-token` and seeds a runtime key set and an audience, none of which a refusal arm needs, and copying it would pin the refusal under one provider when the decision is that it applies under every one.
- **`docs/deployment/oidc/index.md:60` carries further false claims** about the group claim being fixed at `groups` and a single-string claim resolving to no groups. Both are pre-existing and neither is falsified by this change. They are a separate finding, recorded in Non-goals. `docs/deployment/oidc/google-workspace.md:60` carries the claim-key falsehood as well, and DOC-1 corrects it there because it sits inside the span that page's replacement covers.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.12's identity-provider table gains the `PODIUM_IDP_GROUP_MAPPING` row, stating the syntax, the pass-through, and the `config.invalid_idp_group_mapping` refusal. Committed alone and verified before any code.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §6.3.1 names `PODIUM_IDP_GROUP_MAPPING` as what carries the adapter's table, names `config.invalid_idp_group_mapping`, and points at §13.12.
      Levels: —. Depends on: S1
- [ ] **S3 · code** — CODE-1. The parse site records the failure on the `Config`, the warning line is removed, and `validate` returns `config.invalid_idp_group_mapping`.
      Levels: unit, e2e. Depends on: S1, S2
- [ ] **S4 · test** — TEST-1. The unit table over `LoadConfig` and `validate` covering the refused and the accepted settings.
      Levels: unit. Depends on: S3
- [ ] **S5 · test** — TEST-2. The end-to-end refusal arm through the built binary and the `config show --server` arm.
      Levels: e2e. Depends on: S3
- [ ] **S6 · docs** — DOC-1, DOC-2. The two OIDC cookbook pages, `CHANGELOG.md`, and S33 in `test/manual-validation.md`.
      Levels: manual. Depends on: S4, S5

**Ordering constraints.** S1 precedes S2 because §6.3.1's clause points at the §13.12 row, and both precede the code per `.claude/rules/spec-driven-development.md`. S4 and S5 are independent of each other and both depend on S3, because both assert a refusal that does not exist before it. S6 follows the tests so each page describes what the tested build does. `docs/deployment/oidc/google-workspace.md` and `docs/deployment/oidc/entra-id.md` are listed in `tools/doccov/manifest.yaml` (`:67`, `:69`) and the staged edits add no fenced block, so no `doccov-check` obligation is created.

## Current state and the gap

### A malformed table is logged, dropped, and never mentioned again

`internal/serverboot/serverboot.go:2282-2288` reads the table from the environment and, on a parse error, calls `log.Printf("warning: ignored PODIUM_IDP_GROUP_MAPPING: %v", err)` and leaves `c.idpGroupMapping` nil. The comment above it states the consequence outright: "A malformed spec is logged and ignored rather than crashing startup; groups then pass through unmapped." `:2286` is the only assignment to that field and there is no `registry.yaml` key or flag that could supply it instead (no `group_mapping` key exists in `internal/serverboot/yaml_config.go`, and `deploy/helm/podium/templates/deployment.yaml:129-132` sets the environment variable alone), so the field stays nil for the process lifetime.

The parser rejects the whole specification on one malformed entry (`pkg/identity/group_mapping.go:56-58`), so a single typo discards every well-formed entry with it. The verifiers overwrite the raw claim groups only when the table is non-empty (`internal/serverboot/identity_verify.go:32-35`, `:264-267`), so a nil table puts raw claim values directly into `layer.Identity.Groups`, and `pkg/layer/composer.go:64-87` compares those values to a layer's `groups:` filter by plain string equality.

The registry then evaluates §4.6 `groups:` filters against raw claim values under a vocabulary the operator did not configure. The direction of the error depends on the layer's filter text: a filter naming `finance` denies a caller carrying `oktaGroupOID`, and a filter that happens to name a raw claim value grants. Nothing at request time distinguishes the ignored-table state from the legitimate no-mapping state, which the same nil value also represents (`pkg/identity/group_mapping.go:19-22`, `:66`).

A second setting is silent in a different way. A value of separators alone, such as `","` or `" , "`, is skipped entry by entry (`pkg/identity/group_mapping.go:48-52`) into an empty pass-through table with no error and no log line at all. The operator set the variable and got no table and no diagnostic.

### This is the outlier among the registry's configuration failures

`internal/serverboot/serverboot.go:1181` refuses startup with `config.runtime_keys_unavailable` for an unreadable key set. §13.12 states `config.identity_provider_unverified` for `oauth-device-code` on the registry, `config.oidc_jwt_audience_unset` for an audience setting that resolves to no entry, and `config.invalid_issuer_scheme` for a non-`https` issuer, and it states that the registry refuses to start when a selected backend's required values are missing. `internal/serverboot/serverboot.go:2345` refuses `config.invalid_sign_mode` for a `PODIUM_SIGN` typo. `pkg/identity/group_mapping.go:40-45` documents its own error as existing "so a misconfiguration surfaces at startup rather than silently dropping a mapping", and its single production caller does the opposite.

### The refusal cannot be stated as a §6.3.1 violation today

§6.3.1 says the `IdpGroupMapping` adapter maps group values "per a registry-side configuration" and names no variable, no syntax, no malformed-input behavior, and no failure mode. `grep -rn "PODIUM_IDP_GROUP_MAPPING" spec/` returns nothing, while the §13.12 identity-provider table documents every adjacent variable including `PODIUM_OAUTH_GROUPS_CLAIM`, whose row names the `IdpGroupMapping` adapter. The configuration surface this change refuses on is absent from the source of truth, so the specification must name it before a refusal-to-start requirement can exist. Documenting the variable while leaving the failure silent fixes nothing, which is why the two halves are one change in this order.

§13.12 is the established home for a variable's contract, and the §6.3.x section that governs the behavior names the code and points at §13.12 for the rest. §6.3.2 states `config.runtime_keys_unavailable` in its own prose with a `(§13.12)` pointer (`spec/06-mcp-server.md:82`) while the `PODIUM_RUNTIME_KEYS_PATH` row carries the full contract (`spec/13-deployment.md:503`), and §6.3.3 states both `config.trusted_headers_public_bind` and `config.trusted_headers_multitenant_no_secret` (`spec/06-mcp-server.md:130`) while the `PODIUM_TRUSTED_PROXY_SECRET` row carries theirs (`spec/13-deployment.md:502`). SPEC-1 and SPEC-2 follow that division. §6.3.1 and §13.12 both already carry test citations, so neither staged edit creates a new `speccov-drift` obligation.

## Spec amendment: §13.12 the group-mapping variable

**SPEC-1.** Anchor: `spec/13-deployment.md`, the "Identity provider" section's variable table. The row is appended after the `PODIUM_RUNTIME_KEYS_PATH` row, which is the table's last row today, and before the paragraph beginning "The `identity_provider:` object holds". The table's lead-in paragraph and the `identity_provider:` paragraph are not edited.

> | `PODIUM_IDP_GROUP_MAPPING` | The §6.3.1 registry-side group-mapping table, as a comma-separated list of `<claim-value>=<group-name>` pairs. Whitespace around each name is trimmed and a blank entry is dropped. A group claim value that has a table entry is rewritten to that entry's group name before §4.6 visibility evaluation, and a value with no entry passes through unchanged, so a deployment whose IdP already emits the layer group names needs no table. A non-empty value that carries an entry without a `=`, or with an empty name on either side, or that resolves to no `claim=group` entry, fails startup with `config.invalid_idp_group_mapping` under every identity provider, because a table the registry ignored would leave every §4.6 `groups:` filter evaluating raw claim values. An unset or empty value configures no table and startup proceeds, while a value of whitespace or separators alone is non-empty and is refused. Environment only; no config-file key. Read under `oidc-jwt` (§6.3.3) and `injected-session-token` (§6.3.2); `trusted-headers` does not consult the adapter (§6.3.3). | (unset; no table, and every claim value passes through) |

The row carries the syntax, the entry-less arm, and the reason the refusal exists. §6.3.1 names the same code and points at this row for the rest, which is what the sibling sections do: §6.3.2 names `config.runtime_keys_unavailable` with a `(§13.12)` pointer (`spec/06-mcp-server.md:82`), and §6.3.3 names both `config.trusted_headers_public_bind` and `config.trusted_headers_multitenant_no_secret` while their contract lives in the `PODIUM_TRUSTED_PROXY_SECRET` row (`spec/06-mcp-server.md:130`, `spec/13-deployment.md:502`).

This adds one §6.10 code, `config.invalid_idp_group_mapping`, whose obligation is a `// Matrix:` annotated test (TEST-1 and TEST-2 both carry it). Whether the code also becomes a declared axis entry is left open in Non-goals.

## Spec amendment: §6.3.1 what supplies the adapter's table

**SPEC-2.** Anchor: `spec/06-mcp-server.md`, §6.3.1 Claim Derivation, the paragraph beginning "For IdPs without SCIM". One sentence is appended to that paragraph, after "...and is `groups` under `injected-session-token` (§6.3.2)".

> `PODIUM_IDP_GROUP_MAPPING` (§13.12) carries the table, and a non-empty setting for it that does not resolve to a table fails startup with `config.invalid_idp_group_mapping`.

That closes the dangling "a registry-side configuration" reference and makes §6.3.1 self-contained. Naming the code here follows §6.3.2 and §6.3.3, which name their own startup codes beside a §13.12 pointer. The syntax, the entry-less arm, and the reason the refusal exists stay in the §13.12 row.

## Proposed solution

### CODE-1: record the parse failure on the `Config` and refuse it in `validate`

The `Config` struct gains one unexported field beside `idpGroupMapping` (`internal/serverboot/serverboot.go:1746-1750`), holding the §6.3.1 group-mapping parse failure so the one error-returning gate in the boot path can report it. Its comment states why the error is deferred: `LoadConfig` has no error return and also serves `podium config show --server`.

The parse site (`internal/serverboot/serverboot.go:2278-2288`) keeps its existing `spec != ""` guard, drops the `log.Printf` warning, and records the two refusal conditions:

```go
if spec := os.Getenv("PODIUM_IDP_GROUP_MAPPING"); spec != "" {
	m, err := identity.ParseIdpGroupMapping(spec)
	switch {
	case err != nil:
		c.idpGroupMappingErr = err
	case m.Empty():
		c.idpGroupMappingErr = fmt.Errorf("idp group mapping: %q resolves to no claim=group entry", spec)
	default:
		c.idpGroupMapping = m
	}
}
```

The guard decides the whitespace-only value. With it, `" "` parses into an empty table and is refused exactly as `","` and `" , "` are, which is what the §13.12 row states. Rewriting it to `strings.TrimSpace(spec) != ""` would boot a whitespace-only value with no table and no diagnostic while refusing `" , "`, which is a spec-to-code divergence at the one input where the refusal and the "unset is legitimate" carve-out meet. An unset or explicitly empty variable remains the legitimate no-mapping state, which is today's behavior.

`(*Config).validate` (`internal/serverboot/serverboot.go:2316`) returns the refusal beside the sibling `config.invalid_sign_mode` guard, in that guard's style:

```go
if c.idpGroupMappingErr != nil {
	return fmt.Errorf("config.invalid_idp_group_mapping: %w", c.idpGroupMappingErr)
}
```

The parser's message names the offending entry (`pkg/identity/group_mapping.go:56-58`), and the entry-less message echoes the raw setting, so the startup error carries the diagnostic. Claim values are configuration rather than secrets, so echoing them at startup is intended; `config show --server` keeps withholding the claim-to-group pairs (`idpGroupMappingStr`, `internal/serverboot/serverboot.go:2064-2073`), which is unchanged by this proposal.

**IMPLEMENTOR'S CHOICE:** the field name and the exact wording of the two error messages, subject to these constraints. The field is unexported and lives on `Config`. The error `validate` returns begins with `config.invalid_idp_group_mapping:` so the string the tests and the docs name appears in the process output verbatim, and it wraps the recorded error with `%w`. The entry-less message names the variable's value and says it resolves to no `claim=group` entry.

`pkg/identity.ParseIdpGroupMapping` is not touched, and neither is `idpGroupMappingStr` or any row of `Settings()`.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| `PODIUM_IDP_GROUP_MAPPING` unset | Startup proceeds with no table; every claim value passes through to §4.6 evaluation | SPEC-1's row default; `docs/deployment/oidc/index.md:60` |
| Set to the empty string | Same as unset: the parse site's guard does not fire, no table is built, and startup proceeds | SPEC-1's row (an empty value configures no table; the refusal predicate covers non-empty values); pinned by TEST-1 |
| Set to whitespace alone (`" "`) | Startup fails with `config.invalid_idp_group_mapping`. The value is non-empty and resolves to no entry | SPEC-1's row; pinned by TEST-1 and TEST-2 |
| Set to separators alone (`","`, `" , "`) | Startup fails with `config.invalid_idp_group_mapping`. Today this is silent, with no warning and no table | SPEC-1's row; pinned by TEST-1 and TEST-2 |
| One malformed entry beside well-formed ones | Startup fails, and the error names the offending entry. The well-formed entries are not applied, because the parser rejects the whole specification | SPEC-1's row; `docs/deployment/oidc/google-workspace.md` and `entra-id.md` per DOC-1 |
| A claim value with no table entry | Passes through unchanged and is matched against the layer's `groups:` filter as it stands. This is a valid configuration and is unchanged | SPEC-1's row; `docs/deployment/oidc/index.md:60` |
| A malformed value under `trusted-headers` | Startup fails. The adapter is not consulted under that provider, and the refusal is a typo guard rather than an adapter guard | SPEC-1's row ("under every identity provider"); the non-consultation half is stated at `docs/deployment/gateway-delegated-identity.md:85` |
| A malformed value with `PODIUM_PUBLIC_MODE=true` | Startup fails. `validate` runs before any provider selection is consulted, and public mode bypasses visibility entirely, so the value is dead configuration the operator got wrong | SPEC-1's row; `CHANGELOG.md` per DOC-2 |
| `podium config show --server` with a malformed value | Exits 0 and prints the `idp_group_mapping` row with an empty value and `PODIUM_IDP_GROUP_MAPPING` in its source column. The diagnostic command never calls `validate` | Pinned by TEST-2's second arm; no spec text governs the `--server` value column |
| A deployment running today with a malformed value | Boots today and stops booting after this lands. Accepted: pre-1.0 policy admits a breaking change in a MINOR bump, and the release note is the mitigation | `CHANGELOG.md` per DOC-2; OQ-1 |
| A SCIM store path the registry cannot read | Unchanged and deferred: the registry logs `warning: SCIM persistence disabled` and falls back to an in-memory directory for the process lifetime (`internal/serverboot/serverboot.go:1029-1035`), and a missing file is not an error (`pkg/scim/file_store.go:46-51`). That failure narrows a caller's view rather than widening it, so it is fail-closed | Recorded in Non-goals; no spec text states it today, and this proposal does not add any |

## Testing

**TEST-1 · unit, `internal/serverboot/`.** A new `*_test.go` in package `serverboot`, following `internal/serverboot/webui_sign_config_test.go`, which is in-package and tests the sibling `config.invalid_sign_mode` refusal the same way. `validate` is unexported and the `Settings()` rows are in-package, so the test has to be in-package.

```go
// Spec: §6.3.1, §13.12 — a non-empty PODIUM_IDP_GROUP_MAPPING that does not
// resolve to a table fails startup rather than leaving §4.6 `groups:`
// filters evaluating raw claim values.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestConfig_MalformedIdpGroupMappingRefusesStartup(t *testing.T) { ... }
```

The table covers, in one loop over `t.Setenv("PODIUM_IDP_GROUP_MAPPING", ...)` followed by `LoadConfig()` and `validate()`:

- Refused, malformed entry: `"00g1financeOID"` (no `=`), `"=finance"` (empty claim value), and `"okta="` (empty group name). Each asserts a non-nil error whose text contains `config.invalid_idp_group_mapping` and the offending entry.
- Refused, resolves to no entry: `","`, `" , "`, and `"   "`. The whitespace-only arm is the boundary CODE-1's retained guard pins, and it is what an implementor who reaches for `strings.TrimSpace` would break.
- Refused, one malformed entry beside a well-formed one: `"00g1financeOID=finance,platformOID"`. Asserts the error and that `c.idpGroupMapping` is nil, which pins that a typo does not silently apply a partial table.
- Accepted: `"00g1financeOID=finance, 00g2platformOID = platform "`. Asserts `validate()` returns nil, that the table has two entries, and that the `idp_group_mapping` row of `Settings()` reads `2 mappings` with `PODIUM_IDP_GROUP_MAPPING` in its source column.
- Accepted, empty: the variable set to `""`. Asserts `validate()` returns nil and `c.idpGroupMapping` is nil, which pins the `""` versus `" "` boundary from the accepted side and holds the code to the value-based predicate SPEC-1's row states.
- Accepted, unset: the variable unset. Asserts `validate()` returns nil, `c.idpGroupMapping` is nil, and the row's source column reads the default source, which pins that the refusal does not over-reach into the legitimate no-mapping state.

**TEST-2 · end-to-end, `test/e2e/auth_gateway_test.go`.** The arms land beside `gwExpectStartupFailure` and the sibling refusal tests, which is where this repository's `config.*` startup arms live. The helper (`test/e2e/auth_gateway_test.go:155-174`) runs `podium serve --standalone --layer-path <reg>` with extra environment, asserts a non-zero exit, and asserts the code string in the combined output, so no new harness is written.

```go
// Spec: §6.3.1 / §13.12 — a non-empty PODIUM_IDP_GROUP_MAPPING that carries a
// malformed entry, or resolves to no claim=group entry, fails startup with
// config.invalid_idp_group_mapping under every identity provider: a table the
// registry ignored leaves §4.6 `groups:` filters evaluating raw claim values.
// Matrix: §6.10 (config.invalid_idp_group_mapping)
func TestGateway_InvalidIdpGroupMappingRefused(t *testing.T) {
	for _, spec := range []string{"00g1financeOID", "=finance", "okta=", " , ", "   "} {
		gwExpectStartupFailure(t, "config.invalid_idp_group_mapping",
			"PODIUM_IDP_GROUP_MAPPING="+spec)
	}
}
```

No identity-provider variable is set, which is what pins "under every identity provider" and keeps the arm free of setup that can fail for unrelated reasons.

A second arm proves the cross-package fact that `podium config show --server` reaches `LoadConfig().Settings()` without `validate()` (`cmd/podium/config.go:62`), so the refusal does not break the diagnostic command. It reuses the existing helper form from `test/e2e/registry_config_keys_test.go:31-38`:

```go
res := runPodium(t, "", []string{"HOME=" + t.TempDir(), "PODIUM_IDP_GROUP_MAPPING=00g1financeOID"},
	"config", "show", "--server")
// exit 0, and the idp_group_mapping row names PODIUM_IDP_GROUP_MAPPING as its source
```

It asserts the exit status and the source column alone. The value column is unchanged by this proposal, and TEST-1 already covers the row's rendering.

Both arms carry `// Matrix: §6.10 (config.invalid_idp_group_mapping)` whether or not the axis gains the cell, per the Non-goal's IMPLEMENTOR'S CHOICE. Neither arm touches a darwin-gated path, so no macOS skip applies. Coverage: CODE-1 adds three branches at the parse site and one in `validate`, and the TEST-1 table reaches every one, which meets the 85% bar in `.claude/rules/test-coverage.md` for the changed lines.

## Manual validation

**DOC-2 (manual half).** S33, "Gateway-delegated providers fail closed on misconfiguration" (`test/manual-validation.md:2153`), is the scenario for this class: it already collects `config.invalid_issuer_scheme`, `config.oidc_jwt_audience_unset`, and `config.trusted_headers_public_bind` into one isolation block and one `$WORK/reg` scaffold, each as a foreground `podium serve` that exits immediately. Its step 3 already carries the entry-less precedent this change follows, refusing `PODIUM_OAUTH_AUDIENCE=" , "` because every entry is blank after trimming. The refusal is appended there rather than opened as a new scenario, so the two "resolves to no entry" refusals stay in one place. No index row is added; S33's row (`test/manual-validation.md:136`) already describes the scenario.

The "Covers" line gains `config.invalid_idp_group_mapping`, stated beside the audience guard as the second setting refused when it resolves to no entry.

A step 5 is added after the current step 4, reusing S33's isolation block, scaffold, and foreground-run form. The surface a human reads is the terminal: the process exit status and the error line the registry prints before it binds a listener. It catches a build that boots with the mapping silently dropped, which is the wrong output this change exists to remove.

> 5. A malformed `PODIUM_IDP_GROUP_MAPPING` is refused, and so is a value that
>    resolves to no `claim=group` entry. Both runs exit immediately.
>
>    ```bash
>    PODIUM_IDENTITY_PROVIDER=oidc-jwt \
>      PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
>      PODIUM_OAUTH_AUDIENCE=https://podium.acme.example \
>      PODIUM_IDP_GROUP_MAPPING=00g1financeOID \
>      podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
>    echo "exit=$?"
>
>    PODIUM_IDENTITY_PROVIDER=oidc-jwt \
>      PODIUM_OAUTH_ISSUER=https://acme.okta.example/oauth2/default \
>      PODIUM_OAUTH_AUDIENCE=https://podium.acme.example \
>      PODIUM_IDP_GROUP_MAPPING=" , " \
>      podium serve --standalone --no-embeddings --layer-path "$WORK/reg" --bind 127.0.0.1:8133
>    echo "exit=$?"
>    ```
>
>    **Expect.** Both runs exit non-zero and print `config.invalid_idp_group_mapping`.
>    The first names the entry `"00g1financeOID"` as malformed. The second is
>    refused because the value resolves to no `claim=group` entry, parallel to the
>    audience guard in step 3. A run that starts and serves is the shipped
>    behavior this step exists to catch: the registry would then evaluate every
>    `groups:` filter against raw claim values.

S33's existing "Each server refuses to start, so no background process is left to stop." line and its `rm -rf "$WORK"` cleanup already cover the new step, and the Expected block gains the two matching bullets.

## Documentation changes

**DOC-1.** Two OIDC cookbook pages state the behavior this change removes, in the same words. Both are corrected.

`docs/deployment/oidc/google-workspace.md:60`. The replaced span runs from "A raw value with no entry passes through unchanged" through "...so every group value then passes through unmapped.", and is replaced with:

> A raw value with no entry passes through unchanged. The registry reads group membership from the `groups` claim unless `PODIUM_OAUTH_GROUPS_CLAIM`, whose config-file key is `identity_provider.groups_claim`, names another claim, and the mapping has no `registry.yaml` key. A non-empty value that carries a malformed entry, or that resolves to no `<token-value>=<group-name>` pair, fails startup with `config.invalid_idp_group_mapping`, and one malformed entry discards the well-formed entries beside it.

The span covers the sentence this change falsifies and the sentence before it, which claims "the registry reads the token's top-level `groups` claim only". That clause is false: the claim is named by `PODIUM_OAUTH_GROUPS_CLAIM` under `oidc-jwt` and by the `identity_provider.groups_claim` config key, read at `pkg/identity/runtime.go:265-268`, reached for `oidc-jwt` at `pkg/identity/oidc_jwt.go:297`, reported as its own `config show --server` row at `internal/serverboot/serverboot.go:2016`, and stated in §6.3.1 (`spec/06-mcp-server.md:120`) and in the `PODIUM_OAUTH_GROUPS_CLAIM` row of §13.12 (`spec/13-deployment.md:500`). Restating the pass-through and the missing `registry.yaml` key inside the replacement keeps both facts on the page, because the wider span removes the sentence that carried them.

`docs/deployment/oidc/entra-id.md:53`. The sentence "A malformed entry is logged and the whole mapping is dropped, so group values then pass through unchanged." is replaced with the same refusal wording. The adjacent sentence "There is no `registry.yaml` key for the mapping." stays, and remains true under SPEC-1.

**IMPLEMENTOR'S CHOICE:** the exact phrasing on each page, subject to `.claude/rules/doc-style.md` and to these constraints. Each page names `config.invalid_idp_group_mapping` verbatim, states that startup fails rather than that the value is ignored, covers both the malformed-entry arm and the resolves-to-no-entry arm, and states that one malformed entry discards the well-formed entries beside it. On `docs/deployment/oidc/google-workspace.md` the replacement additionally carries the group claim correctly, as `groups` unless `PODIUM_OAUTH_GROUPS_CLAIM` or `identity_provider.groups_claim` names another claim, and keeps the pass-through of an unmapped value and the absence of a `registry.yaml` key, because the wider span removes the sentence that stated them. `docs/deployment/oidc/entra-id.md` carries no claim-key clause, so its replacement covers the refusal alone.

Four adjacent pages are not edited. `docs/deployment/access-control.md:125` and `:147` are orientation prose and a debugging checklist item, `docs/deployment/integrations.md:92` is a one-line SCIM contrast, and `docs/deployment/gateway-delegated-identity.md:57` does not mention the variable at all. None states a failure mode, a syntax, or a value, so none goes stale, and the refusal is stated once in the §13.12 row and once per cookbook that configures the variable.

**DOC-2.** `CHANGELOG.md` gains one bullet under `## [Unreleased]` / `### Changed`:

> A non-empty `PODIUM_IDP_GROUP_MAPPING` that carries a malformed entry, or that resolves to no `claim=group` entry, now fails startup with `config.invalid_idp_group_mapping` under every identity provider. The registry previously logged the parse failure and started with no table, which left every layer `groups:` filter evaluating raw IdP claim values under a vocabulary the operator did not configure, and one malformed entry discarded the well-formed entries beside it. A deployment carrying a typo in that variable today will stop booting; the error names the offending entry. An unset or empty variable is unchanged and remains the no-mapping state. Pre-1.0, no flag, environment variable, or configuration key restores the previous behavior.

and one bullet under `### Documentation`:

> §13.12 states `PODIUM_IDP_GROUP_MAPPING`: its syntax, that it has no config-file key, that a claim value with no table entry passes through unchanged, and that a setting which does not resolve to a table fails startup. §6.3.1 names the variable as what carries the group-mapping table.

The manual-validation half of DOC-2 is the S33 edit staged in Manual validation above.

## Resolved in adversarial review

Review rounds populate this section. Each entry names the defect the round found in this proposal and the amendment that answered it.

### Pass 0 (2026-09-08, drafting)

- **The draft's CODE-1 sketch and TEST-1 arms disagreed on the whitespace-only value.** CODE-1 proposed rewriting the parse-site guard to `strings.TrimSpace(spec) != ""`, which would boot `PODIUM_IDP_GROUP_MAPPING=" "` with no table and no diagnostic while refusing `" , "`, contradicting the §13.12 row this proposal stages; TEST-1 carried a stale arm asserting that `"   "` starts successfully. The guard stays `spec != ""`, the whitespace-only value is a refusal arm in both TEST-1 and TEST-2, and a Watch-out-for entry names the rewrite as the trap.
- **The `config show --server` deliverable was dropped and its dependents restated.** TEST-2's second arm now asserts the exit status and the source column rather than an `invalid` value, TEST-1's row assertion covers the accepted case alone, and the CHANGELOG entry names no `config show` behavior. The reasoning is recorded as a Non-goal.

### Pass 1 (2026-09-08, automated)

- **The staged §13.12 predicate refused a value the staged code accepts.** The row stated the refusal for "a set value ... that resolves to no entry at all", which covers `PODIUM_IDP_GROUP_MAPPING=`, while CODE-1's retained `spec != ""` guard boots it and the Edge cases table records it as accepted. `os.Getenv` returns the same empty string for an unset and an empty variable, so a set-ness predicate is not implementable at the parse site. The row now states the predicate on the value: a non-empty value that carries a malformed entry or resolves to no `claim=group` entry is refused, an unset or empty value configures no table and startup proceeds, and a whitespace-only or separators-only value is non-empty and is refused. The same predicate is carried into the Summary, the Fixed decisions, SPEC-2's sentence, the Edge cases row for the empty string, the test annotations, the CHANGELOG bullet, and the DOC-1 wording, and TEST-1 gains an accepted arm for `""` so both sides of the `""` versus `" "` boundary are pinned.
- **The precedent claim about where a startup code is stated was false.** The proposal asserted that §6.3.3 carries no copy of the `PODIUM_TRUSTED_PROXY_SECRET` codes and that `config.runtime_keys_unavailable` lives in its §13.12 row alone. §6.3.2 names `config.runtime_keys_unavailable` with a `(§13.12)` pointer (`spec/06-mcp-server.md:82`), and §6.3.3 names both `config.trusted_headers_public_bind` and `config.trusted_headers_multitenant_no_secret` (`spec/06-mcp-server.md:130`), while §13.12 carries each variable's full contract (`spec/13-deployment.md:502`, `:503`). The precedent paragraph and the SPEC-1 note now state that division, and SPEC-2 follows it by naming `config.invalid_idp_group_mapping` in the appended §6.3.1 sentence.

### Pass 2 (2026-09-08, automated)

- **DOC-1 claimed a correction its staged edit did not make.** The false clause "the registry reads the token's top-level `groups` claim only" sits in the sentence before the one DOC-1 replaced on `docs/deployment/oidc/google-workspace.md:60`, so an implementor applying the staged replacement and satisfying every stated constraint left the falsehood on the page. DOC-1's anchor is now the span running from "A raw value with no entry passes through unchanged" through "...passes through unmapped.", the replacement states the group claim as `groups` unless `PODIUM_OAUTH_GROUPS_CLAIM` or `identity_provider.groups_claim` names another (`pkg/identity/runtime.go:265-268`, `pkg/identity/oidc_jwt.go:297`, `internal/serverboot/serverboot.go:2016`, `spec/06-mcp-server.md:120`, `spec/13-deployment.md:500`) and restates the pass-through and the missing `registry.yaml` key that the wider span removes, and the IMPLEMENTOR'S CHOICE constraints carry the correction for `google-workspace.md` while recording that `entra-id.md` has no claim-key clause. The Summary, the Watch-out-for entry, and the `docs/deployment/oidc/index.md` Non-goal now say which page's copy of the clause this change corrects and which stays a separate finding.

### Pass 3 (2026-09-08, automated)

- **Two of the sibling startup codes were attributed to `(*Config).validate`.** The Watch-out-for entry and the matrix Non-goal both said the sibling refusals are "returned from the same `validate`", naming `config.invalid_sign_mode`, `config.runtime_keys_unavailable`, and `config.oidc_jwt_audience_unset`. Only `config.invalid_sign_mode` is returned from `validate` (`internal/serverboot/serverboot.go:2345`). `config.runtime_keys_unavailable` is returned from `run` (`:1181`), and `config.oidc_jwt_audience_unset` is returned from `oidcJWTConfigGuard` (`internal/serverboot/identity_verify.go:324`), which `run` calls at `:1243` after `validate` has already returned. Both entries now say the codes are emitted on the boot path and name each one's site, which is the reading the proposal already used elsewhere (`internal/serverboot/serverboot.go:1181` in Current state). The conclusion is unchanged: none carries a §6.10 matrix cell, so the obligation this change creates is the `// Matrix:` annotation alone. The same Non-goal's counts of the out-of-axis codes were removed, because they were stale and the documentation rules ban them.

### Pass 4 (2026-09-08, automated)

- **The matrix Non-goal rested on a false reading of the §6.10 axis.** The Non-goal and the matching Watch-out-for entry stated that a refusal from `validate` never produces an envelope and that the boot-path startup codes therefore carry no cell. The axis already declares `config.public_mode_with_idp` (`tools/matrix/matrices.go:95`), a §13.10 startup guard raised by `StartupConfig.Validate` through the same `(*Config).validate` call the new guard sits beside (`internal/serverboot/serverboot.go:2338`), whose outcome is process output rather than an envelope (`test/e2e/standalone_server_test.go:361-368`) and whose annotation sits on a startup-guard unit test (`pkg/registry/server/config_validate_test.go:14`). Both places now name that cell, record that the remaining boot-path codes carry none, and rest the decision on `matrix-audit`, which requires a covering test for each declared cell and no cell for a code (`tools/matrix/main.go:76-99`). The conclusion is unchanged: no axis edit, and the obligation is the `// Matrix:` annotation TEST-1 and TEST-2 already carry.

### Pass 5 (2026-09-08, pruning)

- **The §6.10 matrix rationale existed in two independently worded copies and had to be corrected twice.** A Watch-out-for bullet and the "Backfilling the §6.10 matrix axis" Non-goal each argued the same decision from the same evidence, and passes 3 and 4 corrected both, the second correcting text this loop had written. Because a reviewer sees one copy at a time, the duplication regenerated findings rather than converging. The Watch-out-for bullet is deleted whole, and the Non-goal is reduced to the `matrix-audit` contract (`tools/matrix/main.go:76-99`) and the observation that the axis's startup-code membership is inconsistent today. The axis survey it carried is deleted: the axis title and `StubPrefix`, the `config.public_mode_with_idp` provenance chain, the enumeration of which sibling boot-path codes carry no cell, and the note about `config.trusted_headers_public_bind` annotating an undeclared cell. In its place the Non-goal carries an IMPLEMENTOR'S CHOICE blank leaving the axis edit open, constrained by the `// Matrix:` annotation on TEST-1 and TEST-2, by `make coverage-gate`, and by no other axis entry being edited. The SPEC-1 note and the Testing section now point at that blank. The whitespace-only guard, stated in several places, is left as it stands: Pass 0 records that this boundary diverged once between CODE-1 and TEST-1, and the repetition is what pins it.

### Pass 6 (2026-09-08, automated)

- **The trusted-headers edge-case row cited a docs line that carries neither half of its outcome.** The row pointed at `docs/deployment/access-control.md:125`, which is a bullet on the OIDC `groups` claim under "Where group membership comes from" and states nothing about `trusted-headers`, about `IdpGroupMapping` being skipped, or about startup failing. The Documentation-changes section also lists that same line as orientation prose that states no failure mode, so the two statements disagreed. The pointer is replaced: SPEC-1's row carries the startup outcome, and the non-consultation half now cites `docs/deployment/gateway-delegated-identity.md:85`, which states that `IdpGroupMapping` is not consulted under `trusted-headers`. The Documentation-changes list is unchanged and still holds, because that line states no failure mode and does not go stale.

## Open questions

**OQ-1.** The refusal is a breaking change for any deployment running today with a malformed `PODIUM_IDP_GROUP_MAPPING`: it boots now and will not boot after this lands, and its group vocabulary changes either way, because the table it never applied starts being applied as soon as the typo is fixed. Pre-1.0 policy admits this in a MINOR bump. Confirm that the release note is the whole mitigation.

**OQ-2.** Does the entry-less arm belong in the refusal? A value of separators alone parses without error today and produces a pass-through table with no warning. Refusing it follows the `PODIUM_OAUTH_AUDIENCE` precedent in §13.12. Leaving it as a pass-through would keep the change to the parse-error case alone and would leave one silent path open. The staged position refuses it.

## Non-goals

- **Changing `ParseIdpGroupMapping` or any other `pkg/identity` surface.** The parser already returns the error and its behavior is already pinned (`pkg/identity/group_mapping_test.go:79`).
- **Adding a `registry.yaml` key or a flag for the mapping table.** It stays environment-only, which is what the §13.12 row states.
- **Changing the pass-through of a claim value that has no table entry** (`pkg/identity/group_mapping.go:80-92`). A configured table that does not cover every claim value is a valid configuration and stays one.
- **Adding a request-time signal that no mapping table is configured.** Under the refusal, the ignored-table state no longer exists, and the legitimate no-mapping state needs no signal.
- **Changing `trusted-headers` behavior.** The adapter is not consulted there (§6.3.3, `internal/serverboot/identity_verify.go:284`); only the startup refusal reaches that provider.
- **Documenting `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` in §13.12** (dropped alternative, originally part of SPEC-1). Neither is named anywhere under `spec/`, and both decide whether the SCIM directory exists and whether it survives a restart, so the divergence is real. Nothing in this proposal's problem depends on it: a malformed mapping table leaves `groups:` filters evaluating raw claim values whether or not SCIM is documented. The `PODIUM_SCIM_STORE_PATH` row is the reason to route them elsewhere rather than fold them in: it would write into the source of truth, as normative behavior, exactly the warn-and-continue pattern this proposal calls the outlier, while this proposal declines to adjudicate whether that fallback is correct. They belong in a separate documentation proposal, together with the `PODIUM_SCIM_TOKEN`/`PODIUM_SCIM_TOKENS` naming error at `pkg/scim/scim.go:13`, which decides on its own evidence whether the store-path fallback (`internal/serverboot/serverboot.go:1029-1035`) is behavior to write into the spec or a second defect to fix.
- **Changing the SCIM store's read-failure behavior.** It narrows a caller's view rather than widening it, so it is fail-closed. Its accepted outcome is recorded in the Edge cases table.
- **Reporting a malformed table as `invalid` in `podium config show --server`** (dropped alternative CODE-2). The premise that the operator's diagnostic command cannot distinguish a malformed setting from an unset variable is false: `envOrSrc` (`internal/serverboot/serverboot.go:1984-1989`) returns the variable name whenever `os.Getenv` is non-empty, so a malformed value already renders `idp_group_mapping | (empty) | PODIUM_IDP_GROUP_MAPPING` while an unset one renders `... | default`, and the same holds for the entry-less arm. It is also redundant: once CODE-1 lands, `validate` returns `config.invalid_idp_group_mapping` wrapping the parser's `malformed entry %q (want claim=group)` on every boot attempt, which names the offending entry and is strictly more information than the word `invalid` in a table cell. It would additionally introduce a validation verdict into a column where every row prints a resolved value, a redaction, a bool, or an int (`internal/serverboot/serverboot.go:2006-2050`); no spec text governs the `--server` output, `grep -rn "config show" spec/` returning only §7.7 client-side material; and the string would land in `podium config show --server --json` (`cmd/podium/config.go:61-66`) as a contract the spec never states. It would also promote a pure package-level helper to a method on `*Config` to report a state that, after CODE-1, cannot exist on a running registry. If an explicit signal is still wanted, the minimal form is a sentence in the cookbook pages telling the operator to read the source column.
- **Backfilling the §6.10 matrix axis in `tools/matrix/matrices.go`.** `matrix-audit` walks the declared cells and requires a covering annotation for each, and it requires no cell for a code (`tools/matrix/main.go:76-99`). The axis's startup-code membership is inconsistent today, and settling it is a separate change.

  **IMPLEMENTOR'S CHOICE:** whether `config.invalid_idp_group_mapping` is added to the §6.10 axis in `tools/matrix/matrices.go`, subject to TEST-1 and TEST-2 carrying `// Matrix: §6.10 (config.invalid_idp_group_mapping)` either way, to `make coverage-gate` passing, and to no other axis entry being edited.
- **Correcting the two claim-form errors at `docs/deployment/oidc/index.md:60`.** "The claim key is fixed at `groups`" and "A `groups` claim encoded as a single delimited string is ignored and resolves to no groups" are both false (`pkg/identity/runtime.go:265-273`, pinned at `pkg/identity/runtime_test.go:244-268`, shared with `oidc-jwt` through `pkg/identity/oidc_jwt.go:297`, and stated in §6.3.1). Neither is falsified by this change and that line states nothing about the dropped mapping, so this proposal has no reason to open the file. They are a separate documentation finding. DOC-1 corrects the same claim-key error on `docs/deployment/oidc/google-workspace.md` only because it falls inside the span that page's replacement already covers, and the single-string claim form appears on neither cookbook page.
- **A general audit of the boot path's other `log.Printf("warning: ...")` continue-anyway branches.** This change covers the group-mapping table alone.
