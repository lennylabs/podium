# Proposal 0018: A configured audience set for the registry's JWT verifiers

- Issue: (to be filed)
- Status: Implemented (2026-09-03). Signed off by the maintainer for implementation, whole, with every step in the checklist in scope. Converged after 7 adversarial review rounds (17 findings fixed). OQ-1 is resolved in favor of D4 as staged, so both verifiers read the whole set. OQ-2 is resolved in favor of D12 as staged, so `Settings()` reports one joined row.
- Date: 2026-09-02

This document stages the proposed spec amendments and the code changes that implement them. It does not modify any spec, code, or doc file. Apply the changes in the sections below after sign-off; `implement-proposal` lands the staged spec edits first, verifies them, and then implements the code against the committed spec.

## Summary

**What changes.**

- §6.3.3 states that `PODIUM_OAUTH_AUDIENCE` configures a set of accepted audiences, that a token satisfies the `aud` check when its claim carries at least one member, that each entry is an operator statement that the registry answers to that value, and that admission under any entry carries the same effective view, grants, and audit identity. §6.3 states the registry-process and client-process readings of the variable, the §6.9 untrusted-token row names the configured audiences, §6.3.4 names the canonical audience the browser sign-in request sends, and §13.12 gains the `PODIUM_OAUTH_AUDIENCE` row it has never carried.
- `PODIUM_OAUTH_AUDIENCE` becomes a comma-separated list and `identity_provider.audience` accepts a YAML sequence in addition to the scalar it accepts today. Both resolve into one `[]string` on `serverboot.Config` (`internal/serverboot/serverboot.go:1635`, `internal/serverboot/yaml_config.go:84`).
- `OIDCVerifier` holds the resolved set and passes it to the variadic `jwt.WithAudience`, so a token verifies when its `aud` claim intersects the set (`pkg/identity/oidc_jwt.go:98,153,209,235`).
- `RuntimeKeyVerifierStore.JWTVerifier` and `RuntimeKeyRegistry.JWTVerifier` take the same `[]string`, so the §6.3.2 injected-session-token verifier reads the setting with one meaning (`pkg/identity/runtime.go:74,135,168,175`).
- Both startup guards keep failing closed on a set that resolves to no entry, under their existing codes `config.oidc_jwt_audience_unset` and `config.injected_token_audience_unset` (`internal/serverboot/identity_verify.go:138,313`), and §6.3.2 gains the spec sentence the second code has never had.
- The first entry is canonical and is the value the registry sends when it initiates a flow itself, which is the §6.3.4 browser sign-in redirect (`internal/serverboot/serverboot.go:1307`).
- The §9.1 SPI field `identity.Config.Audience` becomes `Audiences []string` and carries the whole set, so a provider that verifies §6.3.2 tokens out of process verifies the same audiences the in-process verifiers do (`pkg/identity/registry.go:36-38`, `internal/serverboot/identity_verify.go:179`).
- `Settings()` reports the joined set and re-sources the row to `yamlSrc`, and the `oidc-jwt` boot line names the accepted audiences beside the accepted issuers (`internal/serverboot/serverboot.go:1902,1187`).

**Fixed decisions.**

- A token is accepted when its `aud` claim intersects the configured set. The conjunctive `jwt.WithAllAudiences` is never selected.
- The `aud` claim stays required and is verified on every request under every provider that verifies a token.
- Both registry-process JWT verifiers read the whole set. The `injected-session-token` verifier is not narrowed to the canonical entry.
- The §9.1 SPI field carries the whole set for the same reason, so a provider that verifies a token out of process verifies the audiences the in-process verifiers verify.
- The config-file key accepts a scalar and a sequence. A scalar is one audience verbatim and is never split on a separator. The comma-separated form belongs to the environment variable alone.
- Entries are trimmed, blank entries are dropped, duplicates are collapsed keeping the first occurrence, and order is otherwise preserved.
- The first entry is canonical. No other ordering property is significant.
- The client-side reading of `PODIUM_OAUTH_AUDIENCE` is unchanged. A client process sends the value verbatim as the one audience it asks for, and §6.3 records the two readings.
- `Settings()` reports the joined set under the existing `oauth_audience` key and attributes it to `registry.yaml` when the value came from the config file.
- No new environment variable, no new config-file key, no new §6.10 error code, no edit to the `auth.untrusted_token` catalog entry, and no per-audience authorization.

**Watch out for.**

- `jwt.WithAudience()` over an empty slice leaves the validator's expected set empty, and `Validator.Validate` runs `verifyAudience` only when that set is non-empty (`validator.go:131`). Splatting an empty set therefore disables the audience check rather than failing it, which inverts today's behavior: a verifier built with no audience would accept a token carrying no `aud` at all. The empty-set rejections at `pkg/identity/oidc_jwt.go:209` and `pkg/identity/runtime.go:168`, documented today as defense in depth, become load-bearing and must not be removed as redundant.
- A blank entry is not inert if one ever reaches the verifier. `verifyAudience` rejects an absent, empty, or `[""]` token `aud` (`validator.go:243-248`), but for `aud: ["", "x"]` it falls through to `slices.Contains(cmp, "")` and matches an empty configured entry (`validator.go:250-256`). Dropping blanks during resolution is what forecloses that, so the normalization is a security predicate rather than tidiness.
- A plain `[]string` YAML field rejects a scalar with a hard unmarshal error, which `readYAMLConfig` wraps as `parse %s: %w` (`internal/serverboot/yaml_config.go:234,255-256`). `LoadConfig` does not propagate that error. It logs `warning: ignored registry.yaml: %v` and skips the overlay entirely (`internal/serverboot/serverboot.go:2097-2101`), so a decode failure discards the whole config file rather than refusing startup; the only hard error on the config path is a named file that does not exist (`internal/serverboot/serverboot.go:742-748`). Declaring `Audience []string` would therefore make every registry whose `registry.yaml` writes `audience: https://podium.acme.com` boot with its entire config file thrown away, reverting the store selection, the layer paths, and `identity_provider.type` to whatever the environment carries, with one warning line as the only report. The affected files are the §13.12 example at `spec/13-deployment.md:575`, every OIDC cookbook under `docs/deployment/oidc/`, `docs/deployment/gateway-delegated-identity.md:32`, `docs/deployment/single-node.md:70`, `docs/deployment/vector-backends.md:105`, and `test/e2e/registry_config_format_test.go:44`. The custom unmarshaler is required rather than stylistic. The Helm chart's `config.identityProvider.audience` (`deploy/helm/podium/values.yaml:61`) is not in that set: the template renders it into the `PODIUM_OAUTH_AUDIENCE` environment variable (`deploy/helm/podium/templates/deployment.yaml:53-56`), so it never reaches `yamlIdentityCfg`. Non-strict decoding at `internal/serverboot/yaml_config.go:255` drops an unknown key silently but does not tolerate a type mismatch on a declared one.
- `RuntimeKeyVerifierStore.JWTVerifier` is an interface method (`pkg/identity/runtime.go:74`) with one concrete implementation, `RuntimeKeyRegistry`, which `FilePersistedRuntimeKeyRegistry` promotes through an embedded pointer (`pkg/identity/runtime_persist.go:26-30`). There is one non-test caller and roughly twenty test call sites.
- `Settings()` sources the `oauth_audience` row from `defaultSrc` today (`internal/serverboot/serverboot.go:1902`), reporting `default` for a value that has no default and does come from `registry.yaml`. `test/manual-validation.md:3856` instructs the runner to ignore that provenance. Correcting the source makes that paragraph wrong, so the two change together.
- `identity.Config.Audience` (`pkg/identity/registry.go:36-38`) is a §9.1 SPI field bound by §9.3 to be wire-serializable, and its own comment declares it the §6.3.2 `aud` claim the injected-session-token verifier requires. No built-in factory reads it (`pkg/identity/registry.go:63-68` read `AuthorizationEndpoint`, `TokenSource`, and `Verify`), and its only writer is `selectIdentityProvider` (`internal/serverboot/identity_verify.go:179`). It is a verification value rather than an acquisition value, so under D4 it has to carry the whole set; leaving it a single string would give a custom or out-of-process provider a strictly narrower audience set than the in-process verifiers hold on the same configuration.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §6.3.3 gains the accepted-audiences paragraphs, its verification paragraph names the configured audiences, and its requirement paragraph names a set that resolves to at least one entry. The file is `spec/06-mcp-server.md`.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-2. §6.3 gains the sentence distinguishing the registry-process and client-process readings of `PODIUM_OAUTH_AUDIENCE`, and §6.3.2's `aud` bullet names the configured set and gains the startup-guard sentence. The file is `spec/06-mcp-server.md`.
      Levels: —. Depends on: S1
- [x] **S3 · spec** — SPEC-3. §6.3.4's cookie paragraph, options paragraph, and authorization-request table row name the canonical audience, the cookie paragraph's closing limitation clause names an audience the registry accepts, and the §6.9 untrusted-token row names the configured audiences. The file is `spec/06-mcp-server.md`.
      Levels: —. Depends on: S1
- [x] **S4 · spec** — SPEC-4. §13.12 gains the `PODIUM_OAUTH_AUDIENCE` row it has never carried, its identity-variable preamble names both providers that verify against the whole set, its `identity_provider:` key-list sentence names the two written forms, and the §13.10 restatement names the canonical entry. The file is `spec/13-deployment.md`.
      Levels: —. Depends on: S1
- [x] **S5 · code** — CODE-1. `OIDCVerifier` holds the audience set, `NewOIDCVerifier` takes it and normalizes it through the shared normalization helper, `Verify` fails closed on an empty set and passes the set to `jwt.WithAudience`, `AcceptedAudiences` and `CanonicalAudience` report it, and the existing `NewOIDCVerifier` call sites pass a slice.
      Levels: unit, integration. Depends on: S1
- [x] **S6 · code** — CODE-2. `RuntimeKeyVerifierStore.JWTVerifier` and `RuntimeKeyRegistry.JWTVerifier` take the audience set and fail closed on an empty one, the existing `JWTVerifier` call sites pass a slice, and the §9.1 SPI field `identity.Config.Audience` becomes `Audiences []string` with the rewritten comment.
      Levels: unit, integration. Depends on: S2
- [x] **S7 · code** — CODE-3. The configuration plumbing: the `oauthAudiences` field, the `PODIUM_OAUTH_AUDIENCE` split, the `audienceList` YAML decoder, the `applyYAML` overlay, the `Settings()` row and its source, and the config tests that read the renamed field.
      Levels: unit, e2e. Depends on: S4, S5
- [ ] **S8 · code** — CODE-4. The boot wiring: the canonical accessor beside the guards, both startup guards, both verifier constructions, the canonical entry at the §6.3.4 send site, the whole set into `identity.Config.Audiences` in `selectIdentityProvider`, the `oidc-jwt` boot line, and the guard tests whose table columns become `[]string`.
      Levels: unit, integration, e2e. Depends on: S6, S7
- [x] **S9 · test** — TEST-1. The unit, integration, config-round-trip, and end-to-end tests named under Testing, including the empty-set and blank-entry regressions.
      Levels: unit, integration, e2e. Depends on: S8
- [x] **S10 · docs** — DOCS-1. The `docs/` edits, the `test/manual-validation.md` amendments to S33, S36, S43, and S44, and the CHANGELOG entry.
      Levels: —. Depends on: S9

## Deviations from the checklist

- **S11** landed the boot wiring the checklist carries as S8, which stayed unticked. It moved the canonical-audience accessor beside the startup guards, took both guards and both verifier constructions to the audience set, sent the canonical entry from the browser sign-in request, wrote the whole set into `identity.Config.Audiences` in `selectIdentityProvider`, added the accepted-audiences clause to the `oidc-jwt` boot line, and retyped the guard tests' table columns as `[]string`.

## Current state and the gap

`OIDCVerifier` holds one `audience string` field (`pkg/identity/oidc_jwt.go:98`), written once at construction from the second parameter of `NewOIDCVerifier` (`pkg/identity/oidc_jwt.go:153`) and never written afterward. `Verify` rejects every token when that field is empty, before it resolves a signing key (`pkg/identity/oidc_jwt.go:209-213`), and otherwise passes the single value to `jwt.WithAudience` in the verifying parse (`pkg/identity/oidc_jwt.go:235`). The §6.3.2 runtime verifier carries the same structure with the audience as a parameter rather than a field (`pkg/identity/runtime.go:135,168,175`).

The setting reaches both from one `serverboot.Config` field, `oauthAudience` (`internal/serverboot/serverboot.go:1635`), filled from `PODIUM_OAUTH_AUDIENCE` in `LoadConfig` (`internal/serverboot/serverboot.go:1985`) and from `identity_provider.audience` in `applyYAML` under env-wins precedence (`internal/serverboot/yaml_config.go:288-289`). It has four consumers inside the identity-provider branches, which open at `internal/serverboot/serverboot.go:1135` and close at `:1208`: `injectedTokenAudienceGuard` (`:1139`), `injectedTokenVerifier` (`:1153`), `oidcJWTConfigGuard` (`:1162`), and the `NewOIDCVerifier` construction (`:1170`). Two sit outside those branches and are the ones a grep of the branches alone misses: the §6.3.4 `AuthCodeFlow.Audience` the browser sign-in redirect sends, inside the web-UI authentication block at `:1285` (`:1307`), and `selectIdentityProvider`, which copies the setting into the §9.1 `identity.Config` a provider factory receives (`internal/serverboot/identity_verify.go:179`). `Settings()` reports it as one row sourced from `defaultSrc` (`internal/serverboot/serverboot.go:1902`).

Both startup guards test the same emptiness condition on that one field. `oidcJWTConfigGuard` returns `config.oidc_jwt_audience_unset` when the trimmed value is empty (`internal/serverboot/identity_verify.go:313`), and `injectedTokenAudienceGuard` returns `config.injected_token_audience_unset` on the same condition (`:138`). §6.3.3 states the requirement normatively (`spec/06-mcp-server.md:110`) and §6.3.2 lists `aud` among the required claims (`spec/06-mcp-server.md:71`). The second code appears nowhere in `spec/`; it exists in the code and in `docs/reference/error-codes.md:75` alone, which D13 closes.

The single value is the whole gap. A registry legitimately addressed by more than one audience value from one trusted issuer has no configuration that accepts both.

The motivating deployment is the AD FS farm proposal 0006 addressed. Developer CLIs authenticate through the OAuth device authorization grant, and the farm stamps the resulting access token's `aud` with the client identifier, because the device authorization request carries `client_id` and `scope` and cannot name a target resource. An agent runtime calls the same registry with an AD FS on-behalf-of token audienced to the registry's API URI. Both tokens come from the same issuer, attest the same IdP-verified user, and target the same registry, and the registry can accept one of them. The deployment's IdP administrator reports testing the RFC 8707 `resource` parameter and the MSAL resource-in-scope encoding against the farm, and reports that both fail, the latter with `MSIS9712`. Those are the reporting deployment's observations, as the AD FS discovery behavior was in proposal 0006; no file in this repository confirms them, and the staged spec text states the general rule rather than the vendor observation.

The fallback available today is two registry processes sharing one Postgres and one object store, each configured with one audience. That splits one catalog across two endpoint URLs, lets per-process configuration drift between two entrances to the same data, and contradicts the §2.2 invariant that the shared Go library is the single behavioral surface.

`golang-jwt/jwt/v5` already implements the disjunctive check. `go.mod:11` pins v5.3.1. `WithAudience(aud ...string)` is variadic and stores the whole slice in `p.validator.expectedAud` (`parser_option.go:83-87`). `verifyAudience` with `expectAllAud` false returns nil as soon as one of the token's `aud` values is in the expected set (`validator.go:250-256`), and `errorIfRequired(len(v.expectedAud) > 0, "aud")` makes a missing or empty `aud` a required-claim failure whenever any audience is expected (`validator.go:243-248`). `WithAllAudiences` is the conjunctive option in the same file and is not selected here.

Two properties of that library are traps rather than features, and both are load-bearing for this design. `Validator.Validate` calls `verifyAudience` only when `len(v.expectedAud) > 0` (`validator.go:131`), so an empty expected set skips the audience check rather than failing it. And a configured empty-string entry would match a token whose `aud` array carries an empty string, through `slices.Contains(cmp, "")`.

The config-file side carries a constraint the obvious reading misses. `yamlIdentityCfg.Audience` is `string` (`internal/serverboot/yaml_config.go:84`), and the decode at `internal/serverboot/yaml_config.go:255` is a non-strict `yaml.Unmarshal` from `gopkg.in/yaml.v3`. Non-strict decoding drops an unrecognized key silently, which is why proposal 0006 Decision 15 added a §13.12 round-trip guard, and it does not tolerate a type mismatch on a key that is declared. Decoding the scalar `audience: solo` into a `[]string` field returns `cannot unmarshal !!str `solo` into []string`, which `readYAMLConfig` wraps and `LoadConfig` then reduces to a warning that discards the whole config file (`internal/serverboot/serverboot.go:2097-2101`).

No served surface exposes the configured audience to a client. `cmd/podium/login.go:188` fetches `/.well-known/oauth-authorization-server` from the registry URL, and no route in this repository serves that document; `docs/reference/cli.md:118` already states that discovery succeeds only behind a proxy that publishes it. No MCP meta-tool schema names an audience, and no SDK reads one from the registry. The §6.10 `auth.untrusted_token` `suggested_action` names the variable to check and reveals no value.

## Decisions

These are settled design decisions for this proposal. They are the premises the spec amendments below encode.

**D1. A token is accepted when its `aud` claim intersects the configured audience set.** The comparison stays exact string equality on each pair. No entry is a prefix, a pattern, a wildcard, or a URL subjected to normalization, because each would turn an enumerated operator decision into a rule whose reach an operator has to derive. The set is fixed at startup, never grows at runtime, and no member is derived from a token or from any document the registry fetches. The set of tokens accepted is the union of what one registry process per configured value would accept between them, which is the arrangement the change removes the need for.

**D2. `aud` stays required and is verified on every request.** `jwt.WithAudience` over a non-empty set rejects a token carrying no `aud` as a required-claim failure (`validator.go:243-248`), so the widening covers which values are accepted rather than whether the claim is checked. Proposal 0006 Decision 1 recorded that after widening the accepted issuer set the residual controls are the mandatory audience and the signature. This proposal widens the first of those. The honest residual statement is that a token is accepted when it is signed by a key in the configured issuer's JWKS, carries an `iss` in the accepted issuer set, and carries an `aud` the operator listed. Each is an explicit operator trust decision recorded in configuration, and none is inferred from the token.

**D3. The first configured entry is the registry's canonical audience.** It is the value the registry sends whenever it initiates a flow itself, which is the §6.3.4 authorization request (`internal/serverboot/serverboot.go:1307`). Order is therefore significant, which is why the staged text says "the first value" and never "any value" for what the registry sends. Selecting the canonical entry through a separate key would add a second variable whose consistency with the first nothing checks.

**D4. Both registry-process JWT verifiers read the whole set.** The `injected-session-token` verifier is not narrowed to the canonical entry. This departs from the drafting position, which was the canonical entry alone on the reasoning that a runtime signing key is a weaker trust root than an IdP and that proposal 0006 Decision 8 is the precedent for scoping a new setting to `oidc-jwt`.

Both halves of that reasoning fail on inspection. Decision 8 scoped the claim-name settings to `oidc-jwt` because §6.3.2 fixes its claim set normatively (`spec/06-mcp-server.md:70-74`); it did not scope them because runtime keys are weaker. The `aud` claim is inside that enumeration, so §6.3.2 does not fix the setting away from this change, it states the claim the change is about. The providers are also mutually exclusive: `PODIUM_IDENTITY_PROVIDER` selects one, the boot branches at `internal/serverboot/serverboot.go:1135` and `:1158` are disjoint, and no process runs both verifiers, so a per-provider reinterpretation of one key never resolves a conflict. What it decides instead is what a deliberately configured set means on a registry running the other provider, and under the narrow rule the answer is that entries after the first are silently inert while `config show` reports them as applied. A setting reported as applied that does nothing is the failure mode proposal 0006 Decision 6 rejected for the subject claim and Decision 15 built a guard test against.

The widening this admits under `injected-session-token` is bounded by D1. The §6.3.2 concern the guard comment names is a token bound to a different registry (`internal/serverboot/identity_verify.go:129-133`), and every entry in this registry's set is an operator statement that this registry answers to that value. An operator who lists an audience naming another registry has decided to accept that registry's tokens, which is the same decision, on the same evidence, that the `oidc-jwt` operator makes. OQ-1 records the alternative and what a reviewer changes to take it.

**D5. `RuntimeKeyVerifierStore.JWTVerifier` takes `[]string`.** It is an interface method with one implementation and one non-test caller, so the change is mechanical, and leaving it a single string while `oidc-jwt` takes a set would put the two JWT verifiers on different contracts for the same claim. Podium is pre-1.0, so the interface changes and every caller is updated rather than a second method being added.

**D6. The config-file key accepts a scalar and a sequence, decoded by a `yaml.Unmarshaler` on a named type.** A scalar means a one-entry set and is the audience verbatim; it is never split on a comma or any other separator. A sequence means one entry per element. Any other node kind is a decode error naming the key. The scalar form has to keep working because every documented example writes one and a bare `[]string` field fails the decode, which costs the registry its whole config file rather than refusing startup (`internal/serverboot/serverboot.go:2097-2101`), and the scalar is the documented form for a single-audience deployment rather than a compatibility shim. The comma-separated form belongs to `PODIUM_OAUTH_AUDIENCE` alone, following the existing environment convention (`internal/serverboot/serverboot.go:148,177,204`), and §13.12 states the divergence so an operator does not write `audience: "a,b"` expecting a split.

**D7. Entries are trimmed, blank entries are dropped, duplicates are collapsed keeping the first occurrence, and order is otherwise preserved.** Normalizing once at the configuration boundary makes `len(audiences) == 0` the single fail-closed condition every guard and both verifiers test. Dropping blanks is a security predicate rather than tidiness: a surviving empty entry would match a token whose `aud` array carries an empty string (`validator.go:250-256`). Collapsing duplicates keeps the canonical entry canonical and makes the boot line and `config show` report the set that is installed. There is no cap on the number of entries, because verification is a containment check over a short slice and a cap would be a non-spec constant needing an override under §13.12.

**D8. Both startup guards keep their error codes and fail closed on the resolved empty set.** `config.oidc_jwt_audience_unset` and `config.injected_token_audience_unset` keep their identifiers and their message identifiers, with the condition changed from a trimmed-empty string to an empty set. The guards normalize defensively rather than trusting the fill sites, because they are the last check before a verifier is installed on a security-relevant path. No §6.10 code is added, so no `// Matrix:` cell is created.

**D9. The empty-set rejection inside each verifier is retained and is load-bearing.** `pkg/identity/oidc_jwt.go:209` and `pkg/identity/runtime.go:168` are documented today as defense in depth behind the startup guards. Under a variadic `jwt.WithAudience(v.audiences...)` an empty slice leaves the validator's expected set empty and the validator skips the audience check entirely (`validator.go:131`), so removing either check would turn a verifier constructed with no audience from one that rejects every token into one that accepts a token with no `aud`. Both checks stay, both keep the message substring their tests assert (`pkg/identity/oidc_jwt_jwk_test.go:184`, `pkg/identity/runtime_test.go:211`), and each carries a comment naming the library behavior that makes it load-bearing.

**D10. The §9.1 SPI field `identity.Config.Audience` becomes `Audiences []string` and carries the whole set.** The field is a verification value: its own comment declares it the §6.3.2 `aud` claim the injected-session-token verifier requires (`pkg/identity/registry.go:36-38`), and `identity.Config` is the resolved §6.3 / §13.12 settings a factory receives, which an out-of-process provider (§9.3) receives identically (`pkg/identity/registry.go:31-34`). It is not an acquisition value. The `audience` form parameter a device-code request sends (`pkg/identity/oauth_devicecode.go:104-105`) and the query parameter an authorization-code request sends (`pkg/identity/oauth_authcode.go:163`) read `DeviceCodeFlow.Audience` (`pkg/identity/oauth_devicecode.go:47`) and `AuthCodeFlow.Audience` (`pkg/identity/oauth_authcode.go:63`), which are different fields on different types and are unchanged by this proposal.

Under D4 the registry-process verifiers verify a token against the whole configured set. A field a provider receives for that same purpose that carried the canonical entry alone would make a custom or out-of-process provider verify a strictly narrower set than the in-process verifiers on the same configuration, with the later entries inert while `config show` reports them as applied. That is the failure mode D4 rejects for the built-in path. The field therefore widens with the setting, and §9.3 is satisfied because a list of strings is wire-serializable.

No built-in factory reads the field (`pkg/identity/registry.go:63-68` read `AuthorizationEndpoint`, `TokenSource`, and `Verify`), and `selectIdentityProvider` is its only writer (`internal/serverboot/identity_verify.go:179`), so the change is the field, its comment, and that one write site. The rename from `Audience` to `Audiences` follows D5's pre-1.0 reasoning: an out-of-tree provider reading the old name fails to compile rather than silently reading a narrower value, and Podium adds no second field for compatibility.

An empty set is reachable on a booted registry, so the field's comment states what a provider does with one. Each startup guard is scoped to a single provider id and exempts the rest (`internal/serverboot/identity_verify.go:136` for `injected-session-token`, `:304` for `oidc-jwt`), while `selectIdentityProvider` fills `identity.Config` for every registered provider and runs before either guard (`internal/serverboot/serverboot.go:1130`, ahead of `:1139` and `:1162`). A provider registered under another id through `identity.Default.Register` (§9.2) therefore receives an empty set whenever no audience is configured, and the comment tells such a provider to reject every token rather than read an empty set as permission to skip the audience check.

**D11. The client-side reading of `PODIUM_OAUTH_AUDIENCE` is unchanged, and §6.3 states the two readings.** `podium login` (`cmd/podium/login.go:38`), the MCP server (`cmd/podium-mcp/main.go:274`), and both SDKs read the variable as one opaque value and send it verbatim, because one acquisition produces one audience. One variable name now means a set in a registry process and one value in a client process, which is worth one sentence in §6.3 rather than being left for an operator to infer from a failure.

**D12. `Settings()` reports the comma-joined set and its source becomes `yamlSrc`.** The join follows `operator_admins` (`internal/serverboot/serverboot.go:1911`), the other list-valued row. The source change follows proposal 0006 Decision 16: the value has a config-file key and no default, so `defaultSrc` reports `default` for a value that came from `registry.yaml`, which is wrong today and stays wrong under a set. The row keeps its key, `oauth_audience`, so no end-to-end assertion on the key name changes.

**D13. §6.3.2 states its startup guard.** `config.injected_token_audience_unset` is returned at `internal/serverboot/identity_verify.go:138` and cataloged at `docs/reference/error-codes.md:75`, and a search of `spec/` finds no occurrence of it. The §6.3.2 paragraph this change amends is where the rule belongs, and stating it there closes the gap in the same edit rather than leaving a code-only refusal behind an amended sentence. Its guard test gains the `// Spec: §6.3.2` citation.

**D14. No new configuration surface, and no per-audience authorization.** No new environment variable, config-file key, flag, or opt-in boolean; the list form of an existing key is the whole interface, and a one-entry list reproduces today's behavior exactly. The audience gates whether a token is accepted and carries no authorization meaning, so §4.6, §4.7, §7.3.1, §8.1, and §11 are unamended. This was confirmed mechanically rather than assumed: `layer.Identity` (`pkg/layer/composer.go:21-33`) carries no audience field, and `Audience` appears nowhere in `pkg/layer`, `pkg/registry/core`, or `pkg/audit`.

**D15. The §6.10 `auth.untrusted_token` catalog entry and its `suggested_action` are unedited.** The text tells an operator to verify that the token comes from the issuer and audience configured for `oidc-jwt` and names the two variables (`spec/06-mcp-server.md:438`, `pkg/registry/server/error_envelope.go:85`). It stays accurate when the variable holds a list, it is asserted verbatim by `pkg/registry/server/error_envelope_test.go:99`, and editing it would change a §6.10 matrix cell for a wording improvement.

## Spec amendment: §6.3 the two readings of `PODIUM_OAUTH_AUDIENCE`

Insert one paragraph inside §6.3, after the section's introductory paragraph (`spec/06-mcp-server.md:40`) and before the provider list that begins at `spec/06-mcp-server.md:42`. The §6.2 provider-options sentence the paragraph paraphrases (`spec/06-mcp-server.md:36`) is unchanged, and the paragraph does not go there: that sentence is the last line of §6.2, and this is a normative §6.3 rule that a `// Spec: §6.3` citation has to be able to reach.

```
`PODIUM_OAUTH_AUDIENCE` is read differently by the two process kinds. A registry process reads it as the set of audience values that name this registry and verifies an inbound token's `aud` claim against that set (§6.3.2, §6.3.3). A client process reads it as the single audience it asks the IdP to stamp on the token it acquires, because one acquisition produces one audience, and the client-side providers send it verbatim.
```

Anchor: the new paragraph follows the paragraph beginning "Identity providers attach the caller's OAuth-attested identity to every registry call" and precedes the `oauth-device-code` list item.

## Spec amendment: §6.3.2 the audience under `injected-session-token`

Replace the `aud` bullet in the required-claims list (`spec/06-mcp-server.md:71`):

```
- `aud`: a registry endpoint. The registry verifies it against the audience set configured for the deployment (§6.3.3), and a token whose `aud` carries no member of that set is rejected with `auth.untrusted_runtime`. A deployment that configures no audience fails startup with `config.injected_token_audience_unset`, so the claim is always verified.
```

Anchor: the bullet sits between the `iss` bullet and the `sub` bullet in the required-claims list under **6.3.2 Runtime Trust Model (`injected-session-token`)**.

## Spec amendment: §6.3.3 the accepted audience set under `oidc-jwt`

Replace the verification paragraph (`spec/06-mcp-server.md:102`). Only the `aud` clause changes; the rest is reproduced so the replacement is mechanical:

```
The registry verifies the token on every request. It selects the signing key by `kid` from the issuer's JWKS, resolved from the OIDC discovery document at `<issuer>/.well-known/openid-configuration` and refreshed when the cached key set is older than `jwks_cache_ttl_seconds` (default 300) or when a token presents a `kid` absent from the cached set. It checks the signature against an asymmetric algorithm (RSA, ECDSA, or EdDSA; symmetric algorithms are rejected, so a public key cannot be replayed as an HMAC secret), and validates `iss` against the accepted issuers, `aud` against the configured audiences, and the `exp`/`nbf` window. On success it records the caller's subject and `email`, derives the organization from the verified `org_id` claim, and resolves groups through SCIM or the `IdpGroupMapping` adapter (§6.3.1) applied to the token's group claim. A token that fails signature, `iss`, or `aud` validation is rejected with `auth.untrusted_token`, and an expired token with `auth.token_expired`. While the issuer JWKS is unreachable at runtime, verification fails closed and the request is anonymous rather than rejected.
```

Insert the following paragraphs immediately after it, before the accepted-issuers paragraph:

```
`PODIUM_OAUTH_AUDIENCE` configures a set of accepted audiences rather than a single value. A token satisfies the `aud` check when its `aud` claim carries at least one member of the configured set. A token whose `aud` carries no member is rejected, and so is a token that carries no `aud` claim, an `aud` that is an empty list, and an `aud` that is the empty string. Blank entries are discarded before the set is formed, so no configuration admits a token on an empty audience value. The set is fixed at startup, it never grows at runtime, and no member is derived from a token or from any document the registry fetches. A registry configured with one audience behaves exactly as it did when the setting held a single value.

The first configured value is the registry's canonical audience. It is the value the registry sends whenever it initiates a flow itself, which is the §6.3.4 browser sign-in authorization request. Order is significant for that reason alone; verification is membership in the set, so the order of the remaining values has no observable effect.

Each configured value is an operator statement that this registry answers to that audience. The registry compares strings and cannot tell a resource identifier from a client identifier, cannot tell whether an identifier is assigned to one application, and cannot tell whether another resource server accepts the same value. An operator who lists a value that an OAuth client rather than this registry names accepts every token that client obtains from the trusted issuer, so a listed client identifier is one assigned to a single application. The set of tokens the registry accepts is the same set one registry process per configured audience would accept between them.

A caller admitted under one accepted audience has the same effective view, the same grants, and the same audit identity as a caller admitted under any other. The audience decides whether a token is verified at all and carries no authorization meaning, so a deployment that needs two entrances to one catalog to differ in privilege runs two registries rather than configuring two audiences on one.

A deployment configures more than one audience when the same registry is addressed by more than one audience value from one trusted issuer. The OAuth device authorization grant is the case this covers: that request carries `client_id` and `scope` and cannot name a target resource, so an IdP that stamps the client identifier on the resulting access token issues a different `aud` than the same IdP issues for a token exchange that names the registry's API URI. Both tokens come from the same issuer and attest the same IdP-verified user.
```

Replace the audience sentence in the `https`-scheme paragraph (`spec/06-mcp-server.md:110`), which reads today "`PODIUM_OAUTH_AUDIENCE` is required under `oidc-jwt`, so the required `aud` claim is always verified and a token issued for a different relying party that shares the issuer cannot be accepted; an unset audience fails startup with `config.oidc_jwt_audience_unset`":

```
`PODIUM_OAUTH_AUDIENCE` is required under `oidc-jwt` and must resolve to at least one audience value, so the required `aud` claim is always verified and a token issued for a relying party the operator did not list cannot be accepted; a setting that is unset, empty, or blank after each entry is trimmed fails startup with `config.oidc_jwt_audience_unset`. The registry names the accepted audiences in its startup log.
```

Anchor: the paragraph sits after the subject-claim stability paragraph and before the **`trusted-headers` (delegated)** subsection.

## Spec amendment: §6.3.4 the canonical audience the browser flow sends

Replace the audience clause in the cookie paragraph (`spec/06-mcp-server.md:124`), which reads "and it carries the registry's resolved audience because the authorization request asks the IdP for that audience the way the device-code flow does":

```
and it carries the registry's canonical audience because the authorization request asks the IdP for that audience the way the device-code flow does. The canonical audience is the first value of the configured audience set (§6.3.3).
```

Replace the closing clause of the same paragraph, which reads "and so cannot a deployment whose IdP neither honors the audience parameter nor is configured to mint the registry's resolved audience for this client":

```
and so cannot a deployment whose IdP neither honors the audience parameter nor is configured to mint an audience the registry accepts for this client (§6.3.3).
```

Both clauses sit in the paragraph at `spec/06-mcp-server.md:124`. The second replacement is required rather than cosmetic. Under the widening, a browser token minted with any configured audience verifies, so the limitation stated on the single resolved value would declare unusable a deployment the amended §6.3.3 admits. Together with the replacements of `spec/06-mcp-server.md:126`, `spec/06-mcp-server.md:138`, and `spec/13-deployment.md:194`, it leaves no occurrence of "resolved audience" in `spec/`, so the retired term does not sit beside the newly defined canonical audience for what would then be two different things.

Replace the `PODIUM_OAUTH_AUDIENCE` sentence in the options paragraph (`spec/06-mcp-server.md:126`):

```
`PODIUM_OAUTH_AUDIENCE` carries the registry's audience set, whose first value the authorization request sends; it is the audience `oidc-jwt` already requires, and the browser flow adds no key of its own for it. An operator whose IdP mints a different audience for this client orders the setting so that value comes first.
```

Replace the `audience` row of the authorization-request parameter table (`spec/06-mcp-server.md:138`):

```
| `audience` | the registry's canonical audience | configuration |
```

## Spec amendment: §6.9 the untrusted-token failure-mode row

Replace the row (`spec/06-mcp-server.md:391`):

```
| Untrusted token (`oidc-jwt`)                  | Reject with `auth.untrusted_token`. The token failed signature, `iss`, or `aud` validation against the accepted issuers and the configured audiences (§6.3.3), in either accepted credential location.                       |
```

The §6.10 `auth.untrusted_token` catalog entry at `spec/06-mcp-server.md:430-438` is unedited, per D15.

## Spec amendment: §13.10 and §13.12 the audience variable

Replace the §13.10 sentence naming where the browser flow's audience comes from (`spec/13-deployment.md:194`):

```
None of these keys carries a config-file form. The audience the sign-in redirect sends is the first value of the audience set configured through the `oidc-jwt` keys `PODIUM_OAUTH_AUDIENCE` or `identity_provider.audience` (§6.3.3, §13.12) rather than through a web-UI key.
```

Replace the sentence introducing the registry-process identity variables (`spec/13-deployment.md:492`):

```
The registry-process providers (§6.3.3) and the `injected-session-token` provider (§6.3.2) introduce the following registry-process variables. `oidc-jwt` also reuses `PODIUM_OAUTH_AUDIENCE` (§6.3) for the `aud` claim, which it requires, and both it and `injected-session-token` verify a token against the whole configured set.
```

Add a row at the head of the identity-provider table, before the `PODIUM_OAUTH_ISSUER` row (`spec/13-deployment.md:496`):

```
| `PODIUM_OAUTH_AUDIENCE` | Audience values the registry accepts in a verified token's `aud` claim. Comma-separated; each entry is trimmed, blank entries are dropped, and repeated entries are collapsed. A token is accepted when its `aud` carries at least one member of the set. A setting that resolves to no entry fails startup with `config.oidc_jwt_audience_unset` under `oidc-jwt` and `config.injected_token_audience_unset` under `injected-session-token`. The first value is canonical and is what the registry sends when it initiates a flow itself (§6.3.4). Config-file key `identity_provider.audience`, which accepts a string or a list of strings; a string is one audience verbatim and is not split on any separator. Read under `oidc-jwt` and `injected-session-token`. | (unset; required under both) |
```

Replace the `identity_provider:` key-list sentence (`spec/13-deployment.md:504`):

```
The `identity_provider:` object holds `type`, `audience`, `authorization_endpoint`, `issuer`, `token_header`, `subject_claim`, `groups_claim`, and `jwks_cache_ttl_seconds`. The `audience` key takes a string or a list of strings; a string configures one audience and is not split on a separator, and the comma-separated form belongs to `PODIUM_OAUTH_AUDIENCE`. The `identity_provider.issuer` key is distinct from the top-level domain-discovery `discovery:` block (§4.5.5), which configures domain-tree rendering and has no bearing on identity.
```

The `registry.yaml` example at `spec/13-deployment.md:575` keeps its scalar `audience:` line, which stays valid under the amended key.

## Proposed solution

### `pkg/identity` carries the set

Replace the `audience string` field (`pkg/identity/oidc_jwt.go:98`) with `audiences []string`, written once at construction and read without holding `v.mu`, matching the existing treatment of `subjectClaim` and `groupsClaim`. Change the constructor:

```go
func NewOIDCVerifier(issuer string, audiences []string, cacheTTL time.Duration, opts ...OIDCOption) *OIDCVerifier
```

The constructor stores the normalized set: each entry trimmed, blanks dropped, duplicates collapsed keeping the first occurrence, order otherwise preserved, copied so the caller's slice is not aliased. Normalizing at construction keeps the request path free of per-request allocation and makes `len(v.audiences) == 0` the single condition the fail-closed check tests.

Add accessors beside `AcceptedIssuers`:

```go
// AcceptedAudiences reports the configured audience values a token's aud claim
// is matched against (§6.3.3), in configuration order with the canonical value
// first. The returned slice is a copy.
func (v *OIDCVerifier) AcceptedAudiences() []string

// CanonicalAudience reports the first configured audience, which is the value
// the registry sends when it initiates a flow itself (§6.3.4). Empty when none
// is configured, which the startup guards refuse.
func (v *OIDCVerifier) CanonicalAudience() string
```

Replace the fail-closed check (`pkg/identity/oidc_jwt.go:209-213`), keeping the message text so the existing assertion at `pkg/identity/oidc_jwt_jwk_test.go:184` still matches:

```go
if len(v.audiences) == 0 {
	// The §13.12 config.oidc_jwt_audience_unset startup guard should already
	// have refused boot. This check is not redundant: jwt.WithAudience over an
	// empty slice leaves the validator's expected set empty, and the validator
	// skips the aud check entirely when that set is empty (validator.go:131),
	// so a verifier built with no audience would accept a token carrying no aud
	// at all rather than reject every token. (Spec: §6.3.3)
	return Identity{}, untrustedToken(issuer, "registry audience is not configured; the required aud claim cannot be verified")
}
```

Replace the parser option (`pkg/identity/oidc_jwt.go:235`) with `jwt.WithAudience(v.audiences...)`, carrying a comment that `jwt.WithAllAudiences` is the conjunctive option and is deliberately not used, because the two differ by one word and the wrong one silently requires every configured audience on every token.

Apply the same three changes to the runtime verifier: the interface method (`pkg/identity/runtime.go:74`) and its implementation (`pkg/identity/runtime.go:135`) take `audiences []string`, the emptiness check (`:168`) becomes `len(audiences) == 0` with the same load-bearing comment and the message `pkg/identity/runtime_test.go:211` asserts, and the parser option (`:175`) becomes `jwt.WithAudience(audiences...)`. `FilePersistedRuntimeKeyRegistry` needs no edit, because it embeds `*RuntimeKeyRegistry` and promotes the method (`pkg/identity/runtime_persist.go:26-30`).

Replace the §9.1 SPI field `Audience string` (`pkg/identity/registry.go:36-38`) with `Audiences []string` and rewrite its comment, per D10:

```go
// Audiences is the §6.3.2 / §6.3.3 accepted audience set. A verifying
// provider accepts a token whose `aud` claim carries at least one member
// and rejects one that carries none, including a token with no `aud`. The
// first member is the registry's canonical audience (§6.3.4). An empty set
// means no audience is configured. The startup guards refuse that state
// only under injected-session-token and oidc-jwt, so a provider registered
// under any other id can be handed an empty set on a running registry and
// must reject every token rather than skip the audience check.
Audiences []string
```

`selectIdentityProvider` (`internal/serverboot/identity_verify.go:179`) fills it with the whole resolved set. No built-in factory reads the field (`pkg/identity/registry.go:63-68`), so nothing else in the module changes.

The normalization helper is shared rather than written twice.

**IMPLEMENTOR'S CHOICE:** where the normalization helper lives, given that `pkg/identity` and `internal/serverboot` both need it. Any answer keeps exactly one implementation, applies the same trim, blank-drop, and duplicate-collapse rule at every fill site, and does not make `pkg/identity` import `internal/`.

### `internal/serverboot` resolves the set

Rename the `Config` field to `oauthAudiences []string` (`internal/serverboot/serverboot.go:1635`) and rewrite its comment to describe the set, the canonical entry, and both providers that read it. The rename makes every read site fail to compile until it is updated, which is the mechanical guarantee that no site keeps a single-value reading by accident.

Fill it from the environment in `LoadConfig` (`internal/serverboot/serverboot.go:1985`) with the shared normalization helper wrapped around `splitCSVTrim(os.Getenv("PODIUM_OAUTH_AUDIENCE"))`. `splitCSVTrim` (`internal/serverboot/serverboot.go:154-171`) already trims each entry, drops empties, and returns nil when nothing survives, so no new environment parser is added, and the wrapping helper supplies the duplicate collapse the staged §13.12 row states. `splitCSVTrim` itself is unchanged, because its other callers (`internal/serverboot/serverboot.go:148,177,204,2047`) do not ask for a duplicate collapse. The `LoadConfig` unit case named under Testing is what pins this site: no test drives it today, and `splitCSVTrim` carries none of its own.

Replace `Audience string` (`internal/serverboot/yaml_config.go:84`) with a named type carrying its own unmarshaler:

```go
// audienceList decodes the §13.12 identity_provider.audience key from a scalar
// or a sequence. A scalar configures one audience verbatim and is never split
// on a separator; the comma-separated form belongs to PODIUM_OAUTH_AUDIENCE. A
// plain []string field would reject the scalar form with a decode error, and
// the §13.12 example, every OIDC cookbook, and the end-to-end config-format
// test all write a scalar.
type audienceList []string

func (a *audienceList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("identity_provider.audience: %w", err)
		}
		*a = normalizeAudiences([]string{s})
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := node.Decode(&ss); err != nil {
			return fmt.Errorf("identity_provider.audience: %w", err)
		}
		*a = normalizeAudiences(ss)
		return nil
	default:
		return fmt.Errorf("identity_provider.audience: want a string or a list of strings, got %s", node.Tag)
	}
}
```

A YAML null decodes through the scalar arm to the empty string and is dropped, which leaves the set empty and lets the startup guard refuse it with the documented code rather than a decode error. `SchemaRef.UnmarshalYAML` (`pkg/manifest/parse.go:125`) is the precedent for one key accepting two written forms, switching on `node.Kind` between a scalar and a mapping. `Duration.UnmarshalYAML` (`pkg/sync/marketplace.go:137`) is the precedent for a named config type carrying its own unmarshaler whose error message names the key; it accepts one written form and is not a two-form precedent.

Change the overlay (`internal/serverboot/yaml_config.go:288-289`) to the length form, preserving env-wins precedence, and change the `Settings()` row (`internal/serverboot/serverboot.go:1902`):

```go
{"oauth_audience", strings.Join(c.oauthAudiences, ","), envOrSrc("PODIUM_OAUTH_AUDIENCE", yamlSrc)},
```

### The guards and the send sites

Add a canonical accessor beside the two guards in `internal/serverboot/identity_verify.go`, returning the empty string for an empty set. Change both guards to take the slice and test its length, normalizing defensively per D8. Both error codes and both message identifiers are unchanged, so `test/e2e/auth_gateway_test.go:183`, `test/e2e/standalone_server_test.go:397`, `internal/serverboot/identity_gateway_test.go:51-52`, and `internal/serverboot/identity_verify_test.go:327` keep asserting the same strings. The `injected-session-token` guard message names an audience set rather than "this registry's endpoint":

```go
if identityProvider == "injected-session-token" && len(audiences) == 0 {
	return fmt.Errorf("config.injected_token_audience_unset: PODIUM_IDENTITY_PROVIDER=injected-session-token requires PODIUM_OAUTH_AUDIENCE to name at least one audience this registry answers to, so the required aud claim is verified on every token (§6.3.2)")
}
```

Update the boot-path consumers: `internal/serverboot/serverboot.go:1139`, `:1153`, `:1162`, and `:1170` take the slice; the §6.3.4 `AuthCodeFlow.Audience` at `:1307` takes the canonical entry; and `selectIdentityProvider` (`internal/serverboot/identity_verify.go:179`) fills `identity.Config.Audiences` with the whole set, per D10. Extend the `oidc-jwt` boot line (`internal/serverboot/serverboot.go:1187`) to name the accepted audiences beside the accepted issuers, reading them from the verifier rather than from the config so the line reports what is installed.

The line names the audience set unconditionally, whatever the set's size. A line that named the set only when more than one entry is configured would print no audience on a single-audience registry, which is every registry that exists today, and would make the §6.3.3 sentence "The registry names the accepted audiences in its startup log" false for those deployments. The issuer half of the same line already logs unconditionally (`internal/serverboot/serverboot.go:1187`), and the end-to-end boot-line case, the `AcceptedAudiences` unit case, and the S36 and S44 manual-validation steps all read the unconditional form.

### Call sites that change mechanically

`NewOIDCVerifier`, second argument becomes a slice: `internal/serverboot/multitenant_integration_test.go`, `identity_gateway_integration_test.go`, `webui_session_cookie_test.go`, `webui_auth_integration_test.go`, `identity_verify_test.go`, and `pkg/identity/oidc_jwt_test.go`, `oidc_jwt_issuer_test.go`, `oidc_jwt_claims_test.go`, `oidc_jwt_jwk_test.go`. Declaring a slice-valued fixture beside the existing `testAudience` and `gwAudience` constants makes each substitution one token.

`JWTVerifier`, first argument becomes a slice: `pkg/identity/runtime_test.go`, `test/integration/auth_org_isolation_test.go`, and `test/integration/injected_session_token_test.go`.

Guard tests whose table columns change from `string` to `[]string`: `internal/serverboot/identity_gateway_test.go` and `internal/serverboot/identity_verify_test.go`. Config tests reading the renamed field: `internal/serverboot/yaml_config_test.go:191`, whose accessor joins the slice, and `internal/serverboot/backend_config_test.go:113`.

No test names the `identity.Config` audience field: the only other references to the type construct it empty or take it as an unnamed factory parameter (`internal/serverboot/identity_select_test.go:51,76`), so the D10 rename reaches no test.

`internal/serverboot/webui_auth_integration_test.go` holds an `audience string` option that feeds `AuthCodeFlow.Audience` and the stub IdP's assertion. It stays a single string, because the browser flow sends one value.

Nothing under `cmd/`, `sdks/`, `tools/minttoken/`, `deploy/helm/`, or `docker-compose.yml` changes. Those are client-side audience senders or single-value chart inputs, and a single value is still a valid list.

## Edge cases and accepted failure modes

Each row names the observable outcome and the text that states it.

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A token whose `aud` matches a non-canonical entry | Accepted, with the same effective view, grants, and audit identity as one matching the canonical entry | The accepted-audiences and privilege-equality paragraphs staged for §6.3.3; the §13.12 row; `docs/deployment/gateway-delegated-identity.md` |
| A token whose `aud` is an array carrying one accepted value among several | Accepted. The token's other audiences are not consulted and need not be declared (`validator.go:250-256`) | The staged §6.3.3 paragraph, "carries at least one member" |
| A token whose `aud` carries no accepted value | Rejected with `auth.untrusted_token` | The staged §6.3.3 paragraph; the §6.9 row; the §6.10 entry; `docs/reference/error-codes.md` |
| A token with no `aud`, an empty `aud` list, or an `aud` that is the empty string | Rejected as a required-claim failure rather than a mismatch (`validator.go:243-248`) | The staged §6.3.3 paragraph, which states it explicitly because the behavior is a library choice rather than a JWT requirement and a reader would otherwise expect an optional claim |
| A setting that is empty or blank after trimming | Boot fails with the documented code under each provider. `" , , "` and `""` reach the guard identically | The amended §6.3.3 requirement sentence; the staged §6.3.2 sentence; the §13.12 row |
| One blank entry among non-blank ones | Dropped, and the remaining entries form the set. `a,,b` is a plausible spelling of a list rather than a distinct intent, and `splitCSVTrim` already drops empty entries elsewhere in the package | The staged §6.3.3 paragraph, "Blank entries are discarded before the set is formed" |
| A configured entry that is the empty string reaching the verifier | Foreclosed by normalization. Were one to survive, a token whose `aud` array carried an empty string would match through `slices.Contains(cmp, "")` (`validator.go:250-256`), which is why the blank drop is a security predicate | D7; the regression test named under Testing |
| Duplicate entries, including entries differing only in surrounding whitespace | Collapsed, first occurrence kept, so the canonical entry stays canonical. Verification is unaffected either way; what changes is what the boot line and `config show` report | D7; the §13.12 row |
| A configuration whose canonical entry is not the value the IdP mints for the browser client | The §6.3.4 sign-in completes and the returned token is refused with `auth.untrusted_token`, unless the minted value is also in the set, in which case the mismatch is invisible. The remedy is ordering | The staged §6.3.4 options paragraph, and diagnosable from `config show` |
| An audience value containing a comma | Not expressible through the environment variable, which splits on commas, and expressible through the config-file scalar or sequence. A comma is legal in a URI, so some audience values cannot be written in the environment form. Accepted because the comma-separated environment form is the convention across the package and a second escaping convention for one key would be worse | D6; `docs/deployment/gateway-delegated-identity.md` |
| `identity_provider.audience` written as a mapping or another unsupported node | The decoder returns an error naming the key, `readYAMLConfig` wraps it as `parse <path>: ...`, and `LoadConfig` logs `warning: ignored registry.yaml: ...` and discards the whole config file rather than aborting boot (`internal/serverboot/serverboot.go:2097-2101`). The registry then continues on environment-derived configuration alone, and under `oidc-jwt` or `injected-session-token` with no environment audience the existing audience guard refuses startup with the documented code. Boot proceeding on a discarded config file is existing `LoadConfig` behavior for every key, and this proposal neither relies on it nor changes it | The `audienceList` decoder under Proposed solution; `internal/serverboot/serverboot.go:2097-2101` |
| `identity_provider.audience: []` or a null | Resolves to no entry, and the startup guard refuses with the audience-unset code rather than a decode error, which is the same outcome as omitting the key | The `audienceList` decoder under Proposed solution; the §13.12 row |
| A client process sharing the registry's environment | `cmd/podium/login.go:38` and `cmd/podium-mcp/main.go:274` default from `PODIUM_OAUTH_AUDIENCE` and send it as one `audience` form field, so a comma-separated registry list is sent verbatim and the IdP refuses it or mints a token the registry then refuses. Accepted rather than fixed: the client and the registry are separate processes with separate configuration, and teaching the device-code client to pick an entry would make one variable mean two things in one process. The remedy is `--audience` or a separate client environment | The §6.3 two-readings paragraph; `docs/reference/cli.md` |
| An IdP that ignores the `audience` parameter | The canonical entry governs verification rather than acquisition, and the operator lists the value the IdP mints | The amended closing clause of `spec/06-mcp-server.md:124`, which records that an IdP neither honoring the audience parameter nor configured to mint an audience the registry accepts cannot use the browser flow; `docs/deployment/oidc/entra-id.md` |
| An operator who wants two entrances to one catalog to differ in privilege | Not supported. This is the property the two-process workaround incidentally provided and this change removes, and the security question below covers it | The privilege-equality paragraph staged for §6.3.3, which names the two-registry arrangement as the answer |
| An audience value another resource server also accepts, or a client identifier shared across applications | Accepted by this registry, which is the operator's declaration rather than a registry decision | The operator-obligation paragraph staged for §6.3.3, which states the obligation rather than reporting one farm's assignment practice |
| The `oidc-jwt` boot line | Asserted end to end. `test/e2e/auth_oidc_jwt_test.go` drives the spawned binary against an in-test `https` IdP whose certificate it trusts through `SSL_CERT_FILE`, and already asserts the accepted-issuer half of this line (`test/e2e/auth_oidc_jwt_test.go:202`). The file skips on darwin alone, because darwin ignores `SSL_CERT_FILE` (`test/e2e/auth_oidc_jwt_test.go:48-59`), so the cases run in CI. The audience half is asserted in the same file | The end-to-end boot-line case under Testing; the `AcceptedAudiences` unit case; the S36 and S44 manual-validation steps |

**Deferred.** Per-audience authorization, scope narrowing, or read-only mode is not added; a deployment that needs it runs two registries. The rejected audience is not reported in the `auth.untrusted_token` envelope, which keeps `details.token_iss` alone, because the envelope reaches the caller and the accepted set is deployment configuration. No setting names which entries are client-identifier-shaped, because the registry cannot attribute an audience value to an application and the setting would record an assertion it could not act on.

### The security question

**The strongest case against the change.** The audience is the last per-relying-party control the `oidc-jwt` path has. Proposal 0006 widened the accepted issuer set and recorded that the residual controls are then the mandatory audience and the signature. This proposal touches the first of those. After both, a token is accepted when it is signed by a key in the configured issuer's JWKS, carries an `iss` in a set the discovery document partly determines, and carries an `aud` in a set the operator declares. On a single-tenant IdP that leaves "any token this IdP signs whose audience the operator listed". If one listed value is an OAuth client identifier, it leaves "any token this IdP signs for that client, under any grant, for any resource". That is the token-relay pattern audience binding exists to prevent, and RFC 8707's `resource` parameter exists because of it.

**What answers it.** The signing-key set is the invariant neither change moves. Keys come from the `jwks_uri` in the configured issuer's `https` discovery document in every case (`pkg/identity/oidc_jwt.go:90-95,339-348,357-378`, `spec/06-mcp-server.md:104`), so both changes widen string-equality predicates evaluated against claims that key set signed. The accepted-token set under a configured list is identical to the union of what one registry process per configured audience would accept over the same catalog, which is the arrangement the motivating deployment runs today through a second endpoint URL. This proposal removes a deployment workaround; it admits no token that deployment's present arrangement refuses. The staged tests assert that equivalence at the §4.6 result rather than restating it.

**What is conceded.** Two parts of that framing do not survive intact.

The equivalence is exact for the accepted-token set and inexact for configuration. Two processes can differ in more than their audience: one could run read-only (§13.2.1), carry a different `PODIUM_OPERATOR_ADMINS`, or bind where the other does not. Folding them into one removes the ability to make two entrances to one catalog differ, whether or not a deployment used it. That is a capability loss, and it is why the privilege-equality paragraph is staged into §6.3.3 rather than left implicit: an operator relying on per-entrance divergence has to read the statement before configuring a list. §2.2 is why the loss is acceptable, because two processes whose identity configuration differs over one catalog are already two behavioral surfaces the spec does not sanction.

The client-identifier obligation is unenforceable. The registry compares a string and cannot tell a resource identifier from a client identifier, cannot tell whether an identifier is assigned to one application, and cannot tell whether another resource server accepts the same value. The motivating farm's per-application assignment is one deployment's practice. The obligation is therefore stated normatively in §6.3.3 as something the operator declares, and the registry's part is stated as string equality and nothing more.

The pattern is not new to this registry. `docs/deployment/oidc/google-workspace.md:53` already configures a client identifier as the sole audience, because a Google ID token's `aud` is the client identifier. What changes is that a client-identifier audience can sit beside a resource audience rather than replacing it, which is a narrower step than the one the repository already documents.

## Testing

`.claude/rules/test-coverage.md` requires a test at the highest level each change reaches and at least 85% line coverage on the new lines measured with the cross-package profile. `.claude/rules/spec-driven-development.md` requires each to cite its section; every case below carries `// Spec: §6.3.3` unless another section is named. No §6.10 code is added, so no `// Matrix:` cell is created; the existing `config.*` guard tests keep their annotations and change only in the argument they pass.

**Unit, `pkg/identity`.** A token whose `aud` is the canonical entry verifies; a token whose `aud` is a later entry verifies, which is the case that fails today; a token whose `aud` names an unconfigured value is refused as `*UntrustedTokenError`; an `aud` array carrying one configured value among unconfigured ones verifies, pinning the disjunctive reading; a table over an absent `aud`, `aud: ""`, `aud: []`, and `aud: [""]` refuses each, pinning the required-claim behavior the staged §6.3.3 sentence asserts. Two regressions carry the traps: a verifier constructed with an empty slice and one constructed with only blank entries each refuse a well-formed token **and** a token carrying no `aud` at all, which is the assertion that fails if an empty set ever reaches `jwt.WithAudience`; and a pair of cases pins the `slices.Contains(cmp, "")` hole together with the drop that forecloses it. A verifier built in-package with an empty-string entry left in `v.audiences`, past the constructor's blank drop, **accepts** a token whose `aud` is `["", "https://other"]`, because `verifyAudience` takes the disjunctive early return and matches the token's empty entry against the configured one (`validator.go:250-256`). A verifier constructed through `NewOIDCVerifier` with the entries `{"", "https://configured"}` reports `AcceptedAudiences` as `["https://configured"]` and **refuses** that same token, which is what makes the constructor's blank drop a security predicate. A constructor case asserts trimming, blank-dropping, duplicate collapse, order preservation, and that the stored slice does not alias the caller's, read through `AcceptedAudiences` and `CanonicalAudience`. The existing `TestOIDCVerifier_AudienceUnsetFailsClosed` (`pkg/identity/oidc_jwt_jwk_test.go:177`) is extended rather than replaced. The §6.3.2 verifier gets the mirrored acceptance table under `// Spec: §6.3.2`, including a later-entry case, which is the D4 behavior and the case a reviewer taking OQ-1 deletes.

**Unit, `internal/serverboot`.** A `LoadConfig` case pins the environment form, modeled on `TestLoadConfig_OAuthClaimNamesFromEnv` (`internal/serverboot/identity_claim_names_test.go:27`) and carrying `// Spec: §13.12`. With the config file pointed at an absent path through the package's `noConfigFile` helper (`internal/serverboot/identity_claim_names_test.go:20`), `PODIUM_OAUTH_AUDIENCE=" https://a , , https://b , https://a "` resolves `c.oauthAudiences` to exactly `["https://a", "https://b"]` in that order, pinning the trim, the blank drop, the duplicate collapse, and order preservation together; a single value resolves to one entry, and `" , "` resolves to none. Nothing pins that resolution today: `splitCSVTrim` has no test of its own, no test sets the variable to a multi-entry value, and no test reads the resolved `oauth_audience` row, so without this case an implementor who fills the field with `[]string{os.Getenv(...)}` and no split leaves every other case below green. Both guard tables gain an empty-slice case, an all-blank case, and a multi-entry case that starts. The canonical accessor is covered over empty, one-entry, and multi-entry inputs. The YAML decoder gets a table: a scalar yields one entry; a scalar containing a comma yields one entry carrying the comma, which pins D6; a sequence yields one entry per element in order; blanks and duplicates are dropped; an empty sequence and a null each yield none; a mapping returns an error naming the key. The §13.12 round-trip guard proposal 0006 Decision 15 added is extended to exercise the sequence form, because that guard is what catches a documented key that never reaches the struct. A case over `selectIdentityProvider` registers a factory that captures its `identity.Config`, boots it over a multi-entry setting, and asserts that `Audiences` holds every configured entry in order, carrying `// Spec: §9.1`; it is what fails if the SPI field is ever narrowed back to the canonical entry.

**Integration, `internal/serverboot/identity_gateway_integration_test.go`.** Build a verifier over two audiences from one `jwksIdP`, sign one token with each `aud` for the same subject and group, and assert through the existing `gatewayServer` harness that both resolve the same group-scoped layer with 200 while a third token carrying an unconfigured `aud` receives the `auth.untrusted_token` envelope. Asserting the observable §4.6 result is what pins the deployment scenario and the privilege-equality claim in one case.

**Integration, `test/integration/injected_session_token_test.go`.** A runtime-signed token audienced to a later entry verifies.

**End to end.** Extend `TestGateway_OIDCJWTMissingAudienceRefused` (`test/e2e/auth_gateway_test.go:182`) and `TestInjectedToken_AudienceUnsetFailsStartup` (`test/e2e/standalone_server_test.go:392`) with a blank-list case asserting the same `config.*` strings from the spawned binary. Add a `registry_config_format_test.go` case writing `identity_provider.audience` as a two-element sequence and asserting through `podium config show --server` that `oauth_audience` reports both values joined with source `registry.yaml`, using the existing row helpers rather than a substring match, and a companion case setting `PODIUM_OAUTH_AUDIENCE=https://a,https://b` with no config-file audience and asserting through `settingRow` (`test/e2e/config_permutations_test.go:299`) that the same row reads `https://a,https://b` with source `PODIUM_OAUTH_AUDIENCE`. That companion case is what carries the environment split through the compiled binary, which is the level the resolved row reaches. The existing scalar case is unchanged; it is the regression test for D6 and its `registry.yaml` must keep starting a registry byte for byte.

Add a boot-line case to `test/e2e/auth_oidc_jwt_test.go`, which drives the spawned binary against an in-test `https` IdP it trusts through `SSL_CERT_FILE` and already asserts the accepted-issuer half of the same line (`test/e2e/auth_oidc_jwt_test.go:202`). The case boots the registry over two audiences, mints a token audienced to the second, asserts the group-scoped §4.6 result the existing cases assert, and asserts that `srv.log()` names both configured audiences beside the accepted issuer, which pins the staged §6.3.3 sentence "The registry names the accepted audiences in its startup log" at the level it reaches. The audience the `gwOIDCServer` helper sets (`test/e2e/auth_oidc_jwt_test.go:155`) comes from the caller so the case can configure two, and the file's darwin skip (`test/e2e/auth_oidc_jwt_test.go:48-59`) applies unchanged.

**Coverage.** Run `go test -coverpkg=./... -coverprofile=cover.out ./pkg/identity/... ./internal/serverboot/... ./test/integration/...` and confirm the new lines reach the threshold; the decoder's default arm and the two guard arms are the branches most likely to fall below it and each has a named case. Run the subprocess profile for the boot-path cases with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...`. Parts of `test/e2e` skip silently on macOS, so a green local run is not evidence for the end-to-end cases; confirm them on Linux or read the skip list.

## Manual validation

**S33 (`test/manual-validation.md:2148-2193`).** Extend step 3 with a second refusal: a registry started with `PODIUM_OAUTH_AUDIENCE=" , "` exits non-zero and prints `config.oidc_jwt_audience_unset`, so the blank-list case is checked against the binary as well as in the unit table. Update the **Covers** line to name the list form.

**S36 (`test/manual-validation.md:2478-2746`).** Add a step after the successful single-audience verification: configure two audiences, obtain a token for each from the same IdP for the same user, and confirm both authenticate and resolve the same visibility, which is the privilege-equality claim read by hand. Add the second audience to the scenario's **Prerequisites**, and state that a tenant that cannot mint a second audience skips the step and records the skip. Add a step confirming the startup log names both accepted audiences, and amend the scenario's boot-line expectation (`test/manual-validation.md:2658-2660`), which quotes the line with a trailing ellipsis and so already tolerates the added clause.

**S43 (`test/manual-validation.md:3836-3856`).** Replace the paragraph instructing the runner to ignore the `oauth_audience` provenance. Under D12 the column reads `registry.yaml` when the value came from the config file, alongside `identity_provider` and `identity_provider.issuer`. Add a variant writing `audience:` as a two-element sequence and confirm `podium config show --server` reports both values joined.

**S44 (`test/manual-validation.md:3910`).** The **Expect** block quotes the `oidc-jwt` boot line in full, with the closing parenthesis immediately after the issuer (`test/manual-validation.md:4235-4238`). Replace that quotation with the amended line, which names the accepted audiences beside the accepted issuers, so a runner following the scenario as written does not record a failure for the change working as designed. The rest of the block is unchanged.

## Documentation changes

The `spec/` amendments above are normative. The non-normative documentation under `docs/` follows on acceptance.

- **`docs/deployment/gateway-delegated-identity.md`** carries the registry-side `oidc-jwt` settings table, so the list form lands here: the audience row states that a token is accepted when its `aud` carries at least one configured value and that the config-file key takes a string or a list while the environment variable takes a comma-separated list; the YAML block gains a commented sequence form beside the scalar; and a paragraph after the table explains the device-authorization case, states that each listed value is a trust decision fixed at startup, states that the audience does not narrow what a caller may do, and names the shared-environment trap the edge-case table records.
- **`docs/reference/error-codes.md`** replaces the two audience rows so each names an empty or blank set rather than an unset single value, matching the amended §13.12 row and the guard messages.
- **`docs/reference/cli.md`** states in the client environment table that the registry accepts a comma-separated set while a client sends one value. The `--audience` flag row is unchanged.
- **`docs/deployment/single-node.md` and `docs/deployment/oidc/index.md`** replace their `aud`-validation sentences so each says the token's `aud` must carry one of the configured values. Their scalar examples are unchanged.
- **`docs/deployment/oidc/entra-id.md`** notes that an IdP setting `aud` from the requested scope's resource governs which value the operator lists, per the edge-case table's row on an IdP that ignores the `audience` parameter.
- **The other per-IdP cookbooks** are unchanged. Each configures one audience, the scalar form stays valid, and the troubleshooting entries that tell an operator to match `aud` against `audience:` stay accurate for a one-entry set. Adding the list form to pages that do not need it would put one paragraph in several places to drift.
- **`deploy/helm/podium/values.yaml`** extends the comment above `audience: ""` to say the value reaches `PODIUM_OAUTH_AUDIENCE` and accepts a comma-separated list. The key stays a string, so the chart tests are unaffected.
- **`CHANGELOG.md`** records under `Changed` that `PODIUM_OAUTH_AUDIENCE` and `identity_provider.audience` accept a set of audiences and that a token is accepted when its `aud` carries one of them, naming both providers, and that `podium config show --server` now attributes `oauth_audience` to `registry.yaml` when the value came from the config file. A single configured audience behaves as it did, so no `Fixed` or `Removed` entry is warranted.

## Resolved in adversarial review

### Pass 1 (2026-09-02, automated)

- **The §6.3 two-readings paragraph was anchored into §6.2.** `spec/06-mcp-server.md:36` is the last line of §6.2; the `## 6.3 Identity Providers` heading is at `:38`, the section's introductory paragraph at `:40`, and the provider list at `:42`. The amendment now names `:40` as the paragraph it follows and `:42` as the list item it precedes, and it records that the §6.2 sentence is unchanged and is only paraphrased. The two anchor statements now agree, and the paragraph lands where a `// Spec: §6.3` citation can reach it.
- **The §6.3.4 cookie paragraph kept a second "resolved audience".** The paragraph's closing limitation clause at `spec/06-mcp-server.md:124` stated a deployment limitation predicated on a single minted value, which the widening makes false and which contradicted the edge-case row on an IdP that ignores the `audience` parameter. The §6.3.4 amendment now replaces that clause as well, so it reads "an audience the registry accepts for this client (§6.3.3)". With the replacements of `:126`, `:138`, and `spec/13-deployment.md:194`, no occurrence of "resolved audience" survives in `spec/`. The edge-case row's citation and quotation and the S3 checklist entry were updated to match.
- **The Helm chart was misattributed as a `registry.yaml` scalar-audience site.** `deploy/helm/podium/values.yaml:61` is a chart value that `deploy/helm/podium/templates/deployment.yaml:53-56` renders into `PODIUM_OAUTH_AUDIENCE`, so it never reaches `yamlIdentityCfg` and a `[]string` field could not reject it. It was removed from the scalar list in "Watch out for" and named separately as an environment-variable input, and the `audienceList` doc comment no longer names the chart among the writers of a scalar.
- **D12 cited the wrong `Settings()` row.** `internal/serverboot/serverboot.go:1903` is the `identity_provider.authorization_endpoint` row, a plain string. The comma-joined `operator_admins` row D12 rests on is at `:1911`, and the citation now names it.
- **`Duration.UnmarshalYAML` was cited as a two-written-forms precedent.** It decodes into a `string` and parses it with `time.ParseDuration`, with no second node-kind arm (`pkg/sync/marketplace.go:137`). `SchemaRef.UnmarshalYAML` (`pkg/manifest/parse.go:125`) is now cited alone as the two-form precedent, and `Duration.UnmarshalYAML` is described accurately as the precedent for a named config type whose unmarshaler names its key in the error, with its line corrected.
- **The boot-line blank admitted an answer that contradicted staged spec text.** The delegated choice between logging the audience set unconditionally and logging it only for a multi-entry set is closed in favor of the unconditional form. The conditional answer would print no audience on a single-audience registry, which is every registry today, making the staged §6.3.3 sentence false for those deployments, and it is content the S36 manual step and the `AcceptedAudiences` unit case assert. The unconditional form matches the Summary bullet and the issuer half of the same line (`internal/serverboot/serverboot.go:1187`).
- **The blank-entry regression asserted a refusal the library contradicts.** A verifier holding an empty-string entry accepts a token whose `aud` is `["", "https://other"]` through the disjunctive early return (`validator.go:250-256`), which is what the edge-case row already stated. The Testing section now names two cases: the in-package verifier with the blank entry accepts that token, and a verifier constructed through `NewOIDCVerifier` with `{"", "https://configured"}` reports one accepted audience and refuses it.
- **The security argument cited `AcceptedIssuers` for where signing keys come from.** `pkg/identity/oidc_jwt.go:270-286` reports accepted `iss` values and says nothing about key resolution. The citation now names the type comment stating the invariant (`:90-95`), `refreshLocked` (`:339-348`), and `discoverJWKSURI` (`:357-378`).

### Pass 2 (2026-09-02, automated)

- **D10 rested on a rationale the field's own contract contradicts.** `identity.Config.Audience` is not an acquisition value: its comment declares it the §6.3.2 `aud` claim the injected-session-token verifier requires (`pkg/identity/registry.go:36-38`), and the two lines the old D10 cited read `DeviceCodeFlow.Audience` and `AuthCodeFlow.Audience`, different fields on different types (`pkg/identity/oauth_devicecode.go:47`, `pkg/identity/oauth_authcode.go:63`). Keeping the field a single string would have given a custom or out-of-process provider a strictly narrower audience set than the in-process verifiers hold on the same configuration, which is the inert-setting failure D4 rejects. D10 now widens the field to `Audiences []string` on D5's pre-1.0 reasoning, the "Proposed solution" section stages the field, its rewritten comment, and the one write site at `internal/serverboot/identity_verify.go:179`, and the Summary bullet, the "Watch out for" bullet, D3, the guards-and-send-sites paragraph, the deferred paragraph, the non-goal, OQ-1, the S6 and S8 checklist entries, and the `internal/serverboot` unit tests were reconciled with it.
- **The YAML parse-error wrapper named a function that does not exist.** No `loadYAMLConfig` exists in the module. The wrap is performed by `readYAMLConfig` (`internal/serverboot/yaml_config.go:234,255-256`), which the "Watch out for" bullet now names with its citation.
- **`AuthCodeFlow.Audience` was placed inside the identity-provider branches.** Those branches open at `internal/serverboot/serverboot.go:1135` and close at `:1208`, while `:1307` sits inside the web-UI authentication block at `:1285`. "Current state and the gap" now names four consumers inside the branches and two outside them that a branch-scoped grep misses.
- **The staged `Audiences` comment described the empty set as unreachable.** It said the startup guards refuse an empty set, which holds only under `injected-session-token` (`internal/serverboot/identity_verify.go:136`) and `oidc-jwt` (`:304`); both guards exempt every other provider id, and `selectIdentityProvider` fills `identity.Config` for every registered provider before either guard runs (`internal/serverboot/serverboot.go:1130`, ahead of `:1139` and `:1162`). A provider registered through `identity.Default.Register` (§9.2) or `oauth-device-code` can therefore be handed an empty set on a running registry. The staged comment now scopes the guards to those two ids and states the obligation the out-of-tree provider D10 widens the field for actually carries, and D10 records the same scoping.

### Pass 3 (2026-09-02, automated)

- **D4 cited a comment line and a closing brace as the two disjoint provider branches.** `internal/serverboot/serverboot.go:1136` is the `// §6.3.2:` comment inside the already-opened `injected-session-token` branch and `:1157` is that branch's closing brace. The branch conditions are `if cfg.identityProvider == "injected-session-token"` at `:1135` and `if cfg.identityProvider == "oidc-jwt"` at `:1158`. D4 now cites `:1135` and `:1158`, which also removes its contradiction with "Current state and the gap", where the same `if` is cited at `:1135`.

### Pass 4 (2026-09-02, automated)

- **The edge-case table claimed the `oidc-jwt` boot line is unreachable end to end.** `test/e2e/auth_oidc_jwt_test.go` drives the spawned binary against an in-test `https` IdP whose certificate it trusts through `SSL_CERT_FILE`, and it already asserts the accepted-issuer half of that line (`:202`, and again at `:275-282`); the skip is scoped to darwin alone (`:48-59`). The row now records that, and the Testing section gains an end-to-end case that boots over two audiences and asserts both in `srv.log()`, so the staged §6.3.3 sentence "The registry names the accepted audiences in its startup log" is pinned at the level `.claude/rules/test-coverage.md` requires for boot-path behavior. The S36 amendment no longer calls the manual step the only place that line is checked, and the boot-line paragraph under "The guards and the send sites" names the end-to-end case among the readers of the unconditional form.
- **S44's acceptance criterion quotes the boot line verbatim and was unstaged.** `test/manual-validation.md:4235-4238` expects `identity provider: oidc-jwt (verifying caller tokens against accepted issuers $ISSUER)` with the closing parenthesis immediately after the issuer, so an unconditional audience clause makes that expectation false on every run, including a single-audience registry. S36's own expectation ends in an ellipsis (`test/manual-validation.md:2658-2660`) and does not cover it. Manual validation now carries an S44 entry, the S36 entry names its own quotation, and the S10 checklist entry names S44.
- **No test pinned the comma-separated `PODIUM_OAUTH_AUDIENCE` resolution.** Every previously named case entered below or beside the fill site, so an implementor could satisfy all of them without splitting the variable at all, and the staged §13.12 duplicate-collapse sentence had nothing behind it. Testing now names a `LoadConfig` unit case modeled on `TestLoadConfig_OAuthClaimNamesFromEnv` (`internal/serverboot/identity_claim_names_test.go:27`) that pins trim, blank drop, duplicate collapse, and order together, and an end-to-end companion asserting the resolved `oauth_audience` row through `settingRow` (`test/e2e/config_permutations_test.go:299`). The fill site is also corrected: `splitCSVTrim` (`internal/serverboot/serverboot.go:154-171`) trims and drops empties and does not collapse duplicates, so the shared normalization helper wraps it rather than `splitCSVTrim` changing under its other callers.
- **A malformed `identity_provider.audience` was described as aborting boot.** `LoadConfig` reduces the `readYAMLConfig` error to `warning: ignored registry.yaml: %v` and skips the overlay (`internal/serverboot/serverboot.go:2097-2101`); the only hard error on the config path is a named file that does not exist (`:742-748`). The edge-case row now states the observable outcome, which is the whole config file discarded and the registry continuing on environment-derived configuration until the audience guard refuses it. The "Watch out for" bullet, the "Current state and the gap" paragraph, and D6 were rewritten the same way, which strengthens the case for the custom unmarshaler rather than weakening it.

## Open questions

**OQ-1. Whether a multi-entry set under `injected-session-token` should be refused at startup rather than honored.** D4 honors it and gives both verifiers the whole set. The alternative reads the canonical entry alone under that provider, which changes no accepted token relative to today and so fails closed by construction, at the cost of a setting whose later entries are silently inert while `config show` reports them as applied. A third option refuses the configuration at startup, which makes the narrower posture explicit but needs a new §6.10 code, its spec entry, and its `// Matrix:` annotated test for a configuration nothing in the motivating deployment produces. The recommendation is D4 as staged. A reviewer taking the second option changes the argument at `internal/serverboot/serverboot.go:1153` to the canonical entry, keeps `injectedTokenAudienceGuard` on its present single-string parameter, fills `identity.Config.Audiences` with the canonical entry alone so the §9.1 field states the same rule as the verifier that reads it, and deletes the `test/integration/injected_session_token_test.go` case named under Testing; the interface change in D5 stands either way, because the parameter is what makes the two verifiers read one setting with one meaning.

**OQ-2. Whether `Settings()` should report one joined row or one row per entry.** D12 joins, following `operator_admins`. The joined form cannot represent an audience value containing a comma, which the config-file path admits. That collision is recorded in the edge-case table as accepted, because `config show` is a human-readable report rather than a round-trippable serialization and the same limitation already applies to `operator_admins`.

## Relationship to proposal 0006

Proposal 0006 widened the accepted issuer set for `oidc-jwt` so a token stamped with an IdP's federation-service identifier verifies against a registry configured with that IdP's discovery base. Its Decision 1 recorded that after that widening the residual controls are the mandatory audience and the signature. This proposal widens the audience, which is one of those two, for the same deployment.

The two differ in where the widening comes from. 0006's second accepted issuer is read from a document the IdP publishes, so the operator names one value and the registry infers a second, and 0006's Open question 1 turned on whether that inference should be unconditional. Every audience here is written by the operator. Nothing is inferred, nothing is fetched, and the set cannot grow without a configuration change and a restart, so this proposal raises no equivalent question and adds no opt-in.

The two share three mechanisms. The boot line naming an accepted set follows 0006 Decision 3. Scoping a widening to one provider was 0006 Decision 8, which this proposal examines and departs from in D4. The §13.12 round-trip guard 0006 Decision 15 added is what this proposal's config-file key relies on to stay wired, and it is extended rather than duplicated. Correcting the `oauth_audience` row's source follows 0006 Decision 16, which settled the same question for the claim-name rows.

## Non-goals

- Running two registry processes against one Postgres and one object store as the supported answer. That arrangement splits one catalog across two endpoint URLs, lets per-process configuration drift between two entrances to the same data, and contradicts the §2.2 single-behavioral-surface invariant. It is what this proposal removes the need for.
- Any per-audience authorization, visibility, or scope narrowing. Two callers accepted under different audiences resolve identically thereafter, and a deployment that wants them to differ uses OAuth scope claims (§6.3.1), per-layer visibility (§4.6), or two registries.
- Deriving an audience at runtime from a token, a discovery document, a request header, or the registry's own public URL.
- Selecting `jwt.WithAllAudiences`, which would require every configured audience on every token.
- A new §6.10 error code, or an edit to the `auth.untrusted_token` catalog entry and its `suggested_action`.
- Adding an environment variable, a config-file key, a flag, or an opt-in boolean.
- Changing the client-side audience senders. `podium login`, the MCP server, and the SDKs each send one audience in a device-code request, which is what the grant carries.
- Supporting the RFC 8707 `resource` parameter or the MSAL resource-in-scope encoding in the device-code request. The motivating deployment's IdP administrator reports testing both against the farm and reports that both fail, which is why that token's `aud` is the client identifier and why this proposal exists.
- Serving `/.well-known/oauth-authorization-server` from the registry, or exposing the configured audiences on any served surface.
- Changing `trusted-headers`, which reads identity from headers, consults no token, and reads no audience.
- Changing `identity.Config.AuthorizationEndpoint`, `TokenSource`, or `Verify`, or any other §9.1 identity SPI member. `Audience` widens to `Audiences []string` under D10, and the rest of the interface is untouched.
- Adding a migration path or a compatibility shim. Podium is pre-1.0, and a single configured audience, written as a scalar in `registry.yaml` or as one value in the environment variable, behaves exactly as it does today.
