# Proposal 0006: AD FS compatibility for the `oidc-jwt` identity provider

- Issue: (to be filed)
- Status: Applied to spec (2026-08-13). Converged after 3 adversarial review rounds (4 findings fixed). Open question 1 is resolved in favor of unconditional acceptance.
- Date: 2026-08-13

## Summary

A deployment fronted by AD FS cannot authenticate against a registry running the `oidc-jwt` provider (§6.3.3). The failure was observed against a production AD FS farm on Windows Server through the device authorization grant: the device flow completes and the token response is valid, yet every access token is rejected at verification. An implementation was written and validated end to end against that farm and then reverted from PR #62 (revert commit `aef3f93`; the work is in `e6fef17`, `619bb07`, and `97ba89c` on `feat/oidc-jwt-adfs-compat`) because it amended `spec/` inline rather than through this workflow. This proposal stages those amendments for sign-off, together with the corrections that adversarial review found in them.

The registry rejects an AD FS token on the issuer before anything else. §6.3.3 mandates that the registry validate `iss` against the configured `issuer` and resolve the JWKS from the discovery document at `<issuer>/.well-known/openid-configuration`, and it requires that same value use the `https` scheme; §13.12 repeats all three roles for `PODIUM_OAUTH_ISSUER`. One `OIDCVerifier` field carries all three (`pkg/identity/oidc_jwt.go:92`), and the comparison is exact string equality run twice, at `pkg/identity/oidc_jwt.go:157-159` before key resolution and again through `jwt.WithIssuer(v.issuer)` at `pkg/identity/oidc_jwt.go:180`. The startup guard at `internal/serverboot/identity_verify.go:236-248` refuses a non-`https` issuer, so an operator cannot configure the AD FS federation-service identifier instead. AD FS serves discovery under `issuer=https://<host>/adfs` while stamping `iss=http://<host>/adfs/services/trust` on the access token and publishing that second value as `access_token_issuer` in the same document, so no single configured value satisfies all three roles. `discoverJWKSURI` decodes only `jwks_uri` (`pkg/identity/oidc_jwt.go:275-277`), and a repository-wide search finds no `access_token_issuer` handling. The AD FS discovery behavior is a vendor claim from the reporter's farm that no file in this repository confirms; the spec-side conclusion follows from it.

Further deviations sit behind the issuer rejection. AD FS access tokens carry no `sub`; a stable pairwise subject is present under another claim name, and `claimIdentity` reads the literal key `sub` and returns `sub claim missing` when it is empty (`pkg/identity/runtime.go:219-223`), which `pkg/identity/oidc_jwt.go:197-199` wraps as `auth.untrusted_token`. The registry operator who runs Podium does not administer the AD FS farm, so authoring an issuance rule that renames the claim IdP-side is not available to the deployment that reported the failure. AD FS issuance rules emit group membership under the full claim-type URI `http://schemas.microsoft.com/ws/2008/06/identity/claims/groups` unless the rule is authored with a short name, and the group reader is bound to the literal key `groups` (`pkg/identity/runtime.go:231`). A caller in exactly one group receives that claim as a plain JSON string, and the same line asserts `[]any` only, with no else branch, no error, and no log, so the caller authenticates with an empty `Identity.Groups`, `IdpGroupMapping` has nothing to map (`internal/serverboot/identity_verify.go:185-188`, `pkg/identity/group_mapping.go:80-83`), and every `groups:` layer disappears from the effective view without an authentication error. The same file already accepts both encodings for the scope claims (`scopesFromClaims`, `pkg/identity/runtime.go:277-296`), so the array-only group reader is an internal inconsistency.

Changing what populates the subject reaches past §6.3.3. §6.3.1 fixes the claim set as `{sub, org_id, email, exp, iss, aud, groups?}`, and §6.3.3 states that both gateway-delegated providers record the caller's `sub` and `email` and match them against `users:` layer visibility. The §4.6 contract itself is claim-agnostic, because it names "the caller's OIDC subject or email" and the evaluator matches `Identity.Sub` or `Identity.Email` by plain string equality with no provenance or stability check (`pkg/layer/composer.go:91-97`), so §4.6 needs no amendment. The exposure reaches further than read-side visibility: a user-defined layer stores `Owner = caller.Sub` and derives its `users:` entry from it (`pkg/registry/server/layers.go:607-622`), the per-identity layer cap compares owners by string equality (`pkg/registry/server/layers.go:644`), per-tenant admin grants key on the subject (`pkg/registry/core/admin.go:25`), and the instance-operator grant seeded from `PODIUM_OPERATOR_ADMINS` keys on it too (`pkg/registry/core/tenant.go:30`). A reassignable subject claim would transfer visibility, layer ownership, and both authorization grants. The asymmetry matters operationally: `users:` visibility and the SCIM group resolver match subject or email (`pkg/layer/composer.go:83,94`), while operator and admin authorization match the subject with no email fallback.

The reverted work also documented `identity_provider.subject_claim` and `identity_provider.groups_claim` as config-file keys in the docs and in §13.12 (`619bb07`) without wiring them. No commit in `e6fef17..97ba89c` touches `internal/serverboot/yaml_config.go`; `yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-91`) and the identity overlay in `applyYAML` (`internal/serverboot/yaml_config.go:281-303`) carry only the existing keys, and the decode at `internal/serverboot/yaml_config.go:252` is a non-strict `yaml.Unmarshal`, so an unrecognized key under `identity_provider:` is dropped without error. §13.12 currently matches `yamlIdentityCfg` one for one, so repeating that edit would make the spec false.

## Decisions

These are settled design decisions for this proposal. They are the premises the spec amendments below encode.

1. **Accept a token `iss` that equals the configured `PODIUM_OAUTH_ISSUER` or the `access_token_issuer` published by the same discovery document.** The value is compared as a string and is never dereferenced, and signing keys still come from the `jwks_uri` in that document (`pkg/identity/oidc_jwt.go:249-266,269-285`), so the set of trusted signing keys is unchanged. The set of accepted tokens does widen: after the amendment a token stamped with the second issuer is accepted when it is signed by a JWKS key and carries the configured `aud`. The residual controls are the mandatory audience (`config.oidc_jwt_audience_unset`) and the signature.

2. **Add no configuration surface for the second issuer.** It has one source, the `https` discovery document fetched from the configured issuer, and the startup guard at `internal/serverboot/identity_verify.go:236-248` keeps that fetch on `https`. A discovery document that publishes no `access_token_issuer` leaves the stored value empty and the comparison reduces to today's single-issuer check, so an absent field degrades to the present rejection rather than widening the trust boundary.

3. **Report the accepted issuer values in the existing boot line.** The oidc-jwt boot branch already logs one issuer (`internal/serverboot/serverboot.go:1115`). When the discovery document publishes an `access_token_issuer` that differs from the configured `issuer`, that line names both accepted values, so the widened accepted set is visible to the operator at boot instead of implicit in a vendor extension field. The second issuer never reaches `Settings()`, which reports configuration alone (`internal/serverboot/serverboot.go:1707-1720`), so the boot line is the operator-visible record and a separate `log.Printf` after `Prime()` would scroll away from the provider line it belongs to.

4. **Resolve `access_token_issuer` once, together with `jwks_uri`.** `refreshLocked` calls `discoverJWKSURI` only when `jwksURI` is empty (`pkg/identity/oidc_jwt.go:251-258`), so both discovery-derived values survive for the process lifetime. An IdP that changes either one requires a registry restart, and the JWKS refresh on TTL expiry and on a `kid` miss is unaffected.

5. **Drop `jwt.WithIssuer(v.issuer)` from the verifying parse (`pkg/identity/oidc_jwt.go:180`), because the option admits a single value and the explicit check at `pkg/identity/oidc_jwt.go:157` already carries the rule.** `golang-jwt/jwt/v5` ends `verifyIssuer` in an equality against one expected string, so leaving the option in place rejects every token carrying the `access_token_issuer` value and makes Decision 1 inert. `jwt.Parser.Parse` re-decodes and then verifies the same payload segment `ParseUnverified` read, so the claims the identity is derived from are the signature-verified payload and no second read of the parse result is needed.

6. **Make the subject claim name configurable through `PODIUM_OAUTH_SUBJECT_CLAIM` (config-file key `identity_provider.subject_claim`), with no fallback.** When the setting is present the registry reads the named claim alone, and a token that does not carry it is rejected with `auth.untrusted_token`. A fallback to `sub` would let two tokens from the same IdP record subjects in two identifier namespaces, and a collision between one principal's `sub` and another principal's configured claim would transfer ownership and visibility. A fallback would also make a mistyped setting inert but apparently working on any IdP that does emit `sub`, while `Settings()` and the boot line both report the setting as applied. When the setting is unset the subject is `sub`, which reproduces today's behavior.

7. **Make the group claim name configurable through `PODIUM_OAUTH_GROUPS_CLAIM` (config-file key `identity_provider.groups_claim`).** It composes with the §6.3.1 `IdpGroupMapping` adapter, which maps group values through a registry-side table and names no claim (`pkg/identity/group_mapping.go:80-106`). `PODIUM_IDP_GROUP_MAPPING` and its parsing are unchanged. The named claim is read on every verified `oidc-jwt` token whether or not SCIM is configured (`pkg/identity/oidc_jwt.go:197`, `pkg/identity/runtime.go:231`), and an installed SCIM resolver adds a second match path in the §4.6 evaluator rather than suppressing the claim-derived groups (`pkg/layer/composer.go:75-88`).

8. **Scope both claim-name settings to `oidc-jwt`.** §6.3.2 fixes the injected-session-token claim set, so the runtime-key verifier keeps `sub` and `groups`.

9. **Keep one claim-derivation implementation.** `claimIdentity` (`pkg/identity/runtime.go:219-240`) gains the claim names as a parameter, and the runtime verifier passes the defaults. The reverted commit forked a verifier-local `oidcClaimIdentity` that duplicated the email, organization, and scope derivation; that duplication is not carried forward.

10. **Pass the claim names at construction rather than through setters.** `NewOIDCVerifier` gains variadic options (`WithSubjectClaim`, `WithGroupsClaim`), following the option pattern at `pkg/registry/server/server.go:129-188`. Every existing call site compiles unchanged, the single production call site at `internal/serverboot/serverboot.go:1106` passes the options, and no field is written after the verifier starts serving concurrent `Verify` calls. The reverted commit's setters wrote fields that `Verify` reads without holding `v.mu`. No `newOIDCVerifierFromConfig` boot helper is added.

11. **Read the group claim in the array form and in the single-string form, in the shared helper, so both JWT verifiers accept both encodings.** The spec constrains no JSON type for the claim, so this is a conformance widening. It is stated once in §6.3.1 where the claim set is enumerated, and it is not repeated in §6.3.3. The single-string form yields one group whose name is the claim value and is not split on any separator, which distinguishes it from the comma-separated `trusted-headers` group header.

12. **State normatively in §6.3.3 that the claim named by `PODIUM_OAUTH_SUBJECT_CLAIM` must identify one principal for the life of the deployment and must not be reassigned**, and enumerate what the recorded subject keys: `users:` layer visibility (§4.6), user-defined layer ownership (§7.3.1), per-tenant admin grants (§4.7.2), the instance-operator grant (§4.7.1), and the audit caller identity (§8.1). A deployment that sets the key lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS`.

13. **Leave §4.6 unamended.** It names "the caller's OIDC subject or email" without naming a claim or a derivation rule, `pkg/layer/composer.go` is untouched, and the restatements in the §4.6 visibility table, the §7.3.1 user-defined-layer paragraph, and the §11 visibility tests are likewise claim-agnostic. What §4.6 needs is operator guidance about which identifier to list, which lands in the deployment docs.

14. **Leave §8.1 unamended.** Its statement that read events record "typically `caller.identity = \"<sub-claim>\"`" is hedged and stays accurate when the subject comes from a differently named claim.

15. **Wire both config-file keys into `yamlIdentityCfg` and `applyYAML` in the same change that documents them, and add a guard test that every `identity_provider` key §13.12 names round-trips through `registry.yaml`.** The decode at `internal/serverboot/yaml_config.go:252` is non-strict, so an undeclared key is dropped silently and nothing else would catch the drift.

16. **Report both settings in `Settings()` with `envOrSrc(..., yamlSrc)`**, matching the other YAML-wired identity rows (`internal/serverboot/serverboot.go:1713-1716`). The reverted commit used `defaultSrc`, which reports the source as `default` for a setting that has no default and, once the YAML keys exist, is wrong.

17. **Add no §6.10 error code.** A rejected `iss` and a missing subject both stay `auth.untrusted_token` carrying `details.token_iss` (`pkg/registry/server/identity_verify.go:99-109`). The §6.10 catalog entry and its `suggested_action` remain accurate under the amended rule and are left unedited. The one-line restatements of the rule in the §6.9 failure-mode table and in `docs/reference/error-codes.md` are amended, because both name the configured issuer as the sole accepted value in prose the amendment supersedes.

18. **Derive the identity from the claim map `ParseUnverified` produced, without re-reading the verified parse result.** An earlier draft added a second read of the token's claims from the verifying parse and re-ran the issuer check on it. `jwt.Parser.Parse` calls `ParseUnverified` on the same string and then verifies the signature over the same payload segment, and neither call site sets a decoder option that would make the two decodes differ, so the map read before verification is the signature-verified payload once `Parse` returns. Both branches the re-read would add are unreachable, and `.claude/rules/test-coverage.md` requires either a test or a comment naming why a branch cannot run, which is self-refuting for a check added as defense in depth. A comment at the `ParseUnverified` site names the invariant instead. This also keeps the oidc verifier's derivation call structurally identical to the runtime verifier's at `pkg/identity/runtime.go:204`, which the shared-helper change depends on.

19. **Do not add AD FS to the §6.3.1 tested-IdP list.** That list enumerates the IdPs that carry a device-code setup cookbook, and its membership is one to one with `docs/deployment/oidc/` (`okta.md`, `entra-id.md`, `auth0.md`, `google-workspace.md`, and `keycloak.md`). The same names are restated in `docs/deployment/oidc/index.md`, `docs/deployment/index.md`, `docs/deployment/progressive-adoption.md`, `docs/deployment/organization.md`, and `pkg/scim/scim.go:3`, the last of which frames them as SCIM-capable, which this proposal's non-goals exclude for AD FS. Adding AD FS would put the spec out of sync with those enumerations while this proposal declines to add a cookbook page. AD FS is recorded normatively in §6.3.3, operationally on the gateway-delegated identity page, and reproducibly in the manual-validation scenario, so the list adds no information. The prerequisite for a future addition is a reproducible AD FS setup path, at which point the line and the parallel enumerations are updated together. The commits validated against the farm (`619bb07`, `97ba89c`) never touched the list.

## Current state and the gap

`OIDCVerifier` holds one `issuer` field (`pkg/identity/oidc_jwt.go:92`), trailing-slash-trimmed at construction (`pkg/identity/oidc_jwt.go:117`). `Verify` rejects a mismatched `iss` before key resolution (`pkg/identity/oidc_jwt.go:157-159`) and the verifying parse re-checks it through `jwt.WithIssuer` (`pkg/identity/oidc_jwt.go:180`), which is dead code today because the earlier check rejects every mismatch first. `discoverJWKSURI` decodes `jwks_uri` alone and never reads the document's own `issuer` field (`pkg/identity/oidc_jwt.go:269-285`). `refreshLocked` re-resolves the discovery document only when `jwksURI` is empty (`pkg/identity/oidc_jwt.go:251-258`), so a discovery-derived value survives for the process lifetime, and `Prime()` failure aborts boot (`internal/serverboot/serverboot.go:1108-1111`).

`claimIdentity` (`pkg/identity/runtime.go:219-240`) is the sole JWT claim-derivation implementation, with two call sites (`pkg/identity/runtime.go:204` and `pkg/identity/oidc_jwt.go:197`). It hardcodes the literal keys `sub` (`:220`) and `groups` (`:231`), and the group read asserts `[]any` with no else arm, so a string-encoded claim yields a nil `Groups` with no error and no log. `scopesFromClaims` in the same file accepts both encodings for `scope` and `scp` (`pkg/identity/runtime.go:277-296`).

§6.3.1 resolves group membership registry-side through SCIM 2.0 push or the `IdpGroupMapping` adapter, and neither covers the claim name. SCIM is implemented and wired (`internal/serverboot/serverboot.go:927-935`, `pkg/layer/composer.go:80-86`): it expands a layer's `groups:` filter into member IDs and matches them against the caller's subject or email, so it never reads the token's group claim. It is also conditional, because without `PODIUM_SCIM_TOKENS` no resolver is installed and the evaluator matches groups from JWT claims alone (`internal/serverboot/serverboot.go:65-69`), and a standalone backend has no SCIM path. `IdpGroupMapping` maps group values through a registry-side table (`pkg/identity/group_mapping.go:22-24,80`) configured as a value list (`internal/serverboot/serverboot.go:1968`) and names no claim.

The claim-name gap is vendor-neutral and already documented as an unimplemented capability. Auth0 rejects a non-namespaced custom claim, so an Auth0 access token cannot carry a bare `groups` (`docs/deployment/oidc/auth0.md:70,132`), and the cookbook already instructs the operator to configure the adapter to read the namespaced claim path (`docs/deployment/oidc/auth0.md:83`), which no code implements. A deployment on such an IdP without SCIM has no way to resolve group visibility today. AD FS is reported to emit group membership under a full claim-type URI; no file in this repository confirms it, and the AD FS operator can also author the rule with a short name, so AD FS alone would not justify the setting.

`yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-91`) declares `type`, `audience`, `authorization_endpoint`, `issuer`, `token_header`, and `jwks_cache_ttl_seconds`, matching the §13.12 key list one for one, and `applyYAML` fills each of them under env-wins precedence (`internal/serverboot/yaml_config.go:281-303`). The decode is a non-strict `yaml.Unmarshal` (`internal/serverboot/yaml_config.go:252`), and no test cross-checks the documented key list against the struct.

`docs/deployment/gateway-delegated-identity.md` carries the registry-side `oidc-jwt` settings table (`:36-41`), and no other page under `docs/` names those settings. `docs/reference/cli.md:702` names `PODIUM_OAUTH_AUDIENCE` and `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` in the client env table, whose `PODIUM_IDENTITY_PROVIDER` row admits `oauth-device-code` and `injected-session-token` alone.

No hand-runnable scenario exercises a successful `oidc-jwt` verification against any live IdP. S32 covers `trusted-headers` and carries no token, and S33 asserts that `oidc-jwt` refuses to start on misconfiguration.

## Spec amendment: §6.3.3 accepted issuers under `oidc-jwt`

Replace the `oidc-jwt` verification paragraph in §6.3.3 (the paragraph beginning "The registry verifies the token on every request", `spec/06-mcp-server.md:92`) with the following:

> The registry verifies the token on every request. It selects the signing key by `kid` from the issuer's JWKS, resolved from the OIDC discovery document at `<issuer>/.well-known/openid-configuration` and refreshed when the cached key set is older than `jwks_cache_ttl_seconds` (default 300) or when a token presents a `kid` absent from the cached set. It checks the signature against an asymmetric algorithm (RSA, ECDSA, or EdDSA; symmetric algorithms are rejected, so a public key cannot be replayed as an HMAC secret), and validates `iss` against the accepted issuers, `aud` against `PODIUM_OAUTH_AUDIENCE`, and the `exp`/`nbf` window. On success it records the caller's subject and `email`, derives the organization from the verified `org_id` claim, and resolves groups through SCIM or the `IdpGroupMapping` adapter (§6.3.1) applied to the token's group claim. A token that fails signature, `iss`, or `aud` validation is rejected with `auth.untrusted_token`, and an expired token with `auth.token_expired`. While the issuer JWKS is unreachable at runtime, verification fails closed and the request is anonymous rather than rejected.
>
> The accepted issuers are the configured `issuer` and the `access_token_issuer` the same discovery document publishes. The registry reads `access_token_issuer` once, when it resolves that document, and compares it to a token's `iss` as a string. It never fetches the second value and never resolves keys from it: the signing keys come from the `jwks_uri` in the configured issuer's `https` document in either case, so the set of trusted signing keys is the same under both accepted values. A discovery document that publishes no `access_token_issuer` leaves the configured `issuer` as the sole accepted value. When the document publishes an `access_token_issuer` that differs from the configured `issuer`, the registry names both accepted values in its startup log. AD FS is the deployment this rule covers: it serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value can serve as the discovery base and the expected `iss` at once.

Anchor: the replacement paragraph occupies the position of the current single paragraph at `spec/06-mcp-server.md:92`, between the bearer-parsing paragraph (`:90`) and the `https`-scheme paragraph (`:94`). The `https` rule at `:94` is unchanged and stays accurate, because the second accepted value is never dereferenced.

## Spec amendment: §6.3.3 subject and group claim names

Insert the following two paragraphs into §6.3.3 after the accepted-issuers paragraph above and before the `https`-scheme paragraph (`spec/06-mcp-server.md:94`):

> `PODIUM_OAUTH_SUBJECT_CLAIM` names the claim the registry reads as the caller's subject, and `PODIUM_OAUTH_GROUPS_CLAIM` names the claim it reads for group membership. When `PODIUM_OAUTH_SUBJECT_CLAIM` is set, the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. When it is unset, the subject is `sub`. When `PODIUM_OAUTH_GROUPS_CLAIM` is set, the registry reads group membership from the named claim; when it is unset, it reads `groups`. Both settings are read only under `oidc-jwt`, and the §6.3.2 injected-session-token verifier reads `sub` and `groups`. An AD FS deployment sets both, because its access tokens carry a pairwise subject under a claim other than `sub` and its issuance rules emit group membership under a full claim-type URI unless the rule is authored with a short name.
>
> The claim named by `PODIUM_OAUTH_SUBJECT_CLAIM` must identify one principal for the life of the deployment, and the IdP must not reassign one principal's value to another. The registry matches the recorded subject against `users:` layer visibility (§4.6), stores it as the owner of a user-defined layer (§7.3.1), matches it against per-tenant admin grants (§4.7.2) and the instance-operator grant (§4.7.1), and records it as the audit caller identity (§8.1). A reassigned value therefore transfers layer visibility, layer ownership, and any admin or operator grant with it. A deployment that sets `PODIUM_OAUTH_SUBJECT_CLAIM` lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS`, because operator and admin authorization match the recorded subject and have no email fallback, while `users:` visibility matches the subject or the email.

Anchor: both paragraphs land inside the **`oidc-jwt` (verified)** subsection of §6.3.3, after the accepted-issuers paragraph and before "The `issuer` must use the `https` scheme". The §6.3.3 sentence at `spec/06-mcp-server.md:88` that both gateway-delegated providers "record the caller's `sub` and `email`" is amended in the same edit to read "record the caller's subject and `email`", because under a configured subject claim the recorded value does not come from `sub`.

## Spec amendment: §6.3.1 claim names and group-claim encoding

Insert the following paragraph into §6.3.1 after the claim-set sentence (`spec/06-mcp-server.md:54`):

> Under `oidc-jwt` the claim read as the caller's subject and the claim read for group membership are named by configuration (§6.3.3), so a deployment whose IdP emits neither `sub` nor `groups` names the claims it does emit. A group claim is read in the multi-value form and in the single-string form an IdP emits for a caller in exactly one group. The single-string form yields one group whose name is the claim value; it is not split on any separator.

Replace the `IdpGroupMapping` sentence (`spec/06-mcp-server.md:56`) with:

> For IdPs without SCIM, the `IdpGroupMapping` adapter reads OIDC group claims from the token and maps them to group names per a registry-side configuration. The adapter maps group values. The claim that carries them is named by `PODIUM_OAUTH_GROUPS_CLAIM` under `oidc-jwt` (§6.3.3) and is `groups` under `injected-session-token` (§6.3.2).

Anchor: the inserted paragraph follows the sentence that enumerates `{sub, org_id, email, exp, iss, aud, groups?}` and precedes the `IdpGroupMapping` sentence, which the second edit replaces in place. The tested-IdP sentence (`spec/06-mcp-server.md:58`) is left as written, per Decision 19.

## Spec amendment: §6.9 failure-mode table

Replace the untrusted-forwarded-token row of the §6.9 failure-mode table (`spec/06-mcp-server.md:317`) with:

> | Untrusted forwarded token (`oidc-jwt`)        | Reject with `auth.untrusted_token`. The token failed signature, `iss`, or `aud` validation against the accepted issuers and the configured audience (§6.3.3).                       |

Anchor: the row sits between the untrusted-runtime row and the verified-token-names-no-tenant row in the failure-mode table. The §6.10 `auth.untrusted_token` catalog entry and its `suggested_action` are unedited, because both stay accurate under the amended rule.

## Spec amendment: §13.12 identity-provider variables

Replace the `PODIUM_OAUTH_ISSUER` row (`spec/13-deployment.md:473`) with:

> | `PODIUM_OAUTH_ISSUER` | OIDC issuer URL of the IdP that signs the token forwarded under `oidc-jwt`. Must use the `https` scheme; a non-`https` value fails startup with `config.invalid_issuer_scheme`. The registry fetches the JWKS from the issuer's discovery document at `<issuer>/.well-known/openid-configuration`, and validates the token `iss` against this value or against the `access_token_issuer` that same document publishes (§6.3.3). Config-file key `identity_provider.issuer`. Read only under `oidc-jwt`. | (unset; required for `oidc-jwt`) |

Add two rows after the `PODIUM_OAUTH_TOKEN_HEADER` row (`spec/13-deployment.md:474`):

> | `PODIUM_OAUTH_SUBJECT_CLAIM` | Claim read as the caller's subject in place of `sub`. When it is set, the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. The recorded subject keys `users:` layer visibility, user-defined layer ownership, per-tenant admin grants, and the instance-operator grant, so a deployment that sets this key lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS` (§6.3.3). Config-file key `identity_provider.subject_claim`. Read only under `oidc-jwt`. | (unset; `sub`) |
> | `PODIUM_OAUTH_GROUPS_CLAIM` | Claim read for group membership in place of `groups`. Its values are matched against a layer's `groups:` filter after the `IdpGroupMapping` adapter rewrites them (§6.3.1). The registry reads the claim on every verified token, and a registry that also resolves membership through SCIM matches SCIM-resolved membership in addition. Config-file key `identity_provider.groups_claim`. Read only under `oidc-jwt`. | (unset; `groups`) |

Replace the `identity_provider:` key list (`spec/13-deployment.md:478`) with:

> The `identity_provider:` object holds `type`, `audience`, `authorization_endpoint`, `issuer`, `token_header`, `subject_claim`, `groups_claim`, and `jwks_cache_ttl_seconds`. The `identity_provider.issuer` key is distinct from the top-level domain-discovery `discovery:` block (§4.5.5), which configures domain-tree rendering and has no bearing on identity.

Anchor: all three edits land in the **Identity provider** subsection of §13.12, in the table introduced by "The gateway-delegated providers (§6.3.3) introduce the following registry variables" and in the sentence that follows the table.

## Proposed solution

### Accept the discovery document's `access_token_issuer` as a second token issuer

Add an `accessTokenIssuer string` field to `OIDCVerifier` (`pkg/identity/oidc_jwt.go:91-104`), guarded by the existing `v.mu` alongside `jwksURI`, `keys`, and `fetchedAt`. Extend the anonymous struct in `discoverJWKSURI` (`pkg/identity/oidc_jwt.go:275-277`) with `AccessTokenIssuer string \`json:"access_token_issuer"\``, and change its signature so `refreshLocked` (`pkg/identity/oidc_jwt.go:251-258`) stores both values on the one call it makes when `jwksURI` is empty. Store the second value normalized the same way the configured issuer is normalized at `pkg/identity/oidc_jwt.go:117`: `v.accessTokenIssuer = strings.TrimRight(doc.AccessTokenIssuer, "/")`. An absent field leaves it empty.

Replace the equality check at `pkg/identity/oidc_jwt.go:157-158` with a method:

```go
// issuerAccepted reports whether iss is the configured issuer or the
// access_token_issuer the configured issuer's discovery document published.
func (v *OIDCVerifier) issuerAccepted(iss string) bool {
	iss = strings.TrimRight(iss, "/")
	if iss == v.issuer {
		return true
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.accessTokenIssuer != "" && iss == v.accessTokenIssuer
}
```

Both accepted values and the token's `iss` are trimmed under the same rule, so the two comparisons do not diverge. The rejection message names the accepted set rather than the configured issuer alone.

Remove `jwt.WithIssuer(v.issuer)` from the parser option list at `pkg/identity/oidc_jwt.go:180` (Decision 5). `golang-jwt`'s `verifyIssuer` ends in an equality against one expected string, so the option cannot express a two-value rule, and the explicit check already rejects every `iss` outside the accepted set before the parse runs. Replace the option with a comment above the list recording that the issuer is checked against the configured issuer and the discovery document's `access_token_issuer`, that `jwt.WithIssuer` admits a single value and cannot carry that rule, and that `jwt.Parser.Parse` re-decodes and then verifies the same payload segment `ParseUnverified` read, so the claims read before the parse are the signature-verified payload. Leave the claim-derivation call reading the map from `pkg/identity/oidc_jwt.go:145` (Decision 18): the parsed token is not captured, no `tok.Claims.(jwt.MapClaims)` assertion is added, and `issuerAccepted` is not re-run on the verified claims.

Add an exported `AcceptedIssuers() []string` that returns the configured issuer and, when set, the stored `access_token_issuer`. The oidc-jwt boot branch calls it after `Prime()` succeeds and extends the existing provider line (`internal/serverboot/serverboot.go:1115`) so the log names every accepted value rather than the configured issuer alone (Decision 3).

### Name the subject and group claims at construction

Change the constructor to `NewOIDCVerifier(issuer, audience string, cacheTTL time.Duration, opts ...OIDCOption)` and add `WithSubjectClaim(name string)` and `WithGroupsClaim(name string)` (Decision 10). The variadic parameter leaves every existing call site compiling unchanged. Add `subjectClaim` and `groupsClaim` fields to `OIDCVerifier`, written once at construction and read without a lock, because nothing writes them after the verifier serves its first request.

Add `oauthSubjectClaim` and `oauthGroupsClaim` to `Config` (`internal/serverboot/serverboot.go`), read them in `LoadConfig` from `PODIUM_OAUTH_SUBJECT_CLAIM` and `PODIUM_OAUTH_GROUPS_CLAIM` beside `oauthTokenHeader` (`internal/serverboot/serverboot.go:1785-1786`), and pass them as options at the single production construction site (`internal/serverboot/serverboot.go:1106`). Log each applied claim name at that call site. No `newOIDCVerifierFromConfig` helper is added.

### Parameterize the shared claim derivation and read the group claim in both encodings

Add a `claimNames` struct with `Subject` and `Groups` fields to `pkg/identity`, and change `claimIdentity` (`pkg/identity/runtime.go:219`) to take one. The runtime verifier passes the zero value at `pkg/identity/runtime.go:204`, and the oidc verifier passes `claimNames{Subject: v.subjectClaim, Groups: v.groupsClaim}` at `pkg/identity/oidc_jwt.go:197`. Replace the subject read (`pkg/identity/runtime.go:220-223`) with an exact read of the configured claim:

```go
subKey := "sub"
if names.Subject != "" {
	subKey = names.Subject
}
sub, _ := claims[subKey].(string)
if sub == "" {
	return Identity{}, fmt.Errorf("%s claim missing", subKey)
}
```

This preserves the current `sub claim missing` text on the default path. No other site in the tree emits that string, so no other message changes, and the read adds no fallback branch (Decision 6).

Replace the group read (`pkg/identity/runtime.go:231-237`) with a read of the configured key that accepts both encodings, mirroring `scopesFromClaims` (`pkg/identity/runtime.go:277-296`): a `[]any` yields each string element, and a plain `string` yields one group whose name is the whole value, with no splitting on any separator.

The encoding tolerance reaches both JWT verifiers, including the §6.3.2 injected-session-token path whose groups are mapped at `internal/serverboot/identity_verify.go:31-33`. Its direction is privilege-widening: a deployment whose IdP already emits the single-string form resolves to no groups today and gains group-derived visibility on upgrade. The implementation carries a CHANGELOG entry recording that behavior change under `Changed`, naming both verifiers.

### Wire the config-file keys and guard against key drift

Add `SubjectClaim string \`yaml:"subject_claim,omitempty"\`` and `GroupsClaim string \`yaml:"groups_claim,omitempty"\`` to `yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-91`). In `applyYAML`, beside the existing oidc-jwt keys (`internal/serverboot/yaml_config.go:294-303`), add the env-wins fills:

```go
if c.oauthSubjectClaim == "" && y.Identity.SubjectClaim != "" {
	c.oauthSubjectClaim = y.Identity.SubjectClaim
}
if c.oauthGroupsClaim == "" && y.Identity.GroupsClaim != "" {
	c.oauthGroupsClaim = y.Identity.GroupsClaim
}
```

Add two `Settings()` rows after `identity_provider.token_header` (`internal/serverboot/serverboot.go:1715`), both using `envOrSrc(..., yamlSrc)` (Decision 16):

```go
{"identity_provider.subject_claim", c.oauthSubjectClaim, envOrSrc("PODIUM_OAUTH_SUBJECT_CLAIM", yamlSrc)},
{"identity_provider.groups_claim", c.oauthGroupsClaim, envOrSrc("PODIUM_OAUTH_GROUPS_CLAIM", yamlSrc)},
```

Add a table-driven guard test in `internal/serverboot/yaml_config_test.go` that writes a `registry.yaml` setting every `identity_provider` key §13.12 names, loads it, and asserts each resolved `Config` field. The test names the keys explicitly, so a future documented key that is never added to `yamlIdentityCfg` fails rather than being dropped by the non-strict decode. The existing coverage exercises a subset (`internal/serverboot/backend_config_test.go:63-67`, `internal/serverboot/yaml_config_test.go:190-193`), so this guard is new.

### Tests

`.claude/rules/test-coverage.md` requires a test at the highest level each change reaches. The set below is anchored on the harnesses that already exist rather than on new vendor-named files, and no new test file is named after an IdP; the AD FS reference is carried in comments (`test/e2e/naming_convention_test.go:10-21`).

**Unit, `pkg/identity/oidc_jwt_test.go`.** Extend `testIdP` (`:21-45`) with a mutex-guarded `accessTokenIssuer` published in the discovery document when set. Add the rejection variants to the existing `TestOIDCVerifier_Rejections` table (`:156-190`) rather than as standalone functions, and put the new positive cases there or in a behavior-named file such as `oidc_jwt_claims_test.go`. Cases: a token carrying the `access_token_issuer` value is accepted after `Prime`; the same token is rejected when the document publishes no `access_token_issuer`; an unrelated issuer is still rejected; `AcceptedIssuers()` reports two values after `Prime` with the field present and one without; a configured subject claim populates `Identity.Sub`; a token carrying `sub` but not the configured claim is rejected; a token carrying neither is rejected; a configured group claim populates `Identity.Groups`; a single-string group claim yields a one-element slice under the default and under the configured claim name; and a JWKS refresh on a `kid` miss does not clear the stored `accessTokenIssuer`, which is the process-lifetime assumption Decision 4 rests on. The access-token-issuer acceptance case fails today with `jwt.WithIssuer` present and passes once it is removed, so it is the direct regression test for that deletion and no separate test is added for it.

**Unit, `pkg/identity/runtime_test.go`.** Add a case that the §6.3.2 runtime verifier accepts a single-string `groups` claim, covering the shared helper's other call site.

**Integration, `internal/serverboot/identity_gateway_integration_test.go`.** Extend `jwksIdP` (`:35-63`) with an optional `access_token_issuer` in its discovery document. Add `TestGatewayIntegration_OIDCJWTSplitIssuerAndClaimNames`: construct the verifier with `WithSubjectClaim` and `WithGroupsClaim`, sign a token carrying the federation-service `iss`, a subject under the configured claim, and the group value as a plain string under the configured claim name, and assert through the existing `gatewayServer` harness that a group-scoped layer returns 200 while a caller with no group returns 404. That single case covers the split issuer, both claim names, the single-string encoding, and the boot wiring, and it asserts the observable §4.6 result rather than an `Identity` value, which is the failure the problem statement names. Extend `TestGatewayIntegration_OIDCJWTGroupMapping` (`:230`) with a caller whose raw IdP group value arrives as a single string under the configured claim name and maps to `engineering`, proving the composition with `IdpGroupMapping` that Decision 7 asserts. Add a small case in the same file asserting that an unset claim-name pair reproduces today's derivation. Do not add a second discovery-and-JWKS stub to this package; `jwksIdP` is already shared across files in it (`internal/serverboot/multitenant_integration_test.go:76`).

**Config-file guard, `internal/serverboot/yaml_config_test.go`.** Add the table-driven round-trip guard described above.

**End to end, `test/e2e/registry_config_keys_test.go`.** Add one case in the style of `TestRegistryConfig_OllamaURLFromConfigFile` (`:71`) that writes a `registry.yaml` carrying `registry.identity_provider.subject_claim` and `groups_claim`, runs `podium config show --server`, and asserts both rows report the configured value with source `registry.yaml`, using `settingRow` (`test/e2e/config_permutations_test.go:299`) and `rcShowSetting` (`test/e2e/registry_backend_config_test.go:17`) rather than a substring match. Leave `test/e2e/auth_oidc_test.go` unchanged. A full `oidc-jwt` happy path is not reachable end to end, which the header comment at `test/e2e/auth_gateway_test.go:1-10` already records and which the https issuer guard (`internal/serverboot/identity_verify.go:236-248`) makes true against the live IdP lane the suite has (`test/e2e/dex_login_test.go:56`), so the deep behavior stays at the integration level.

**Boot log.** The accepted-issuer values are covered by the `AcceptedIssuers()` unit case. The `log.Printf` in the boot branch is not asserted automatically, because reaching it needs an https IdP the binary trusts. The manual-validation scenario is where the log line is checked.

## Relationship to the reverted branch

This proposal stages the work reverted from PR #62 (`e6fef17`, `619bb07`, and `97ba89c`), with the corrections adversarial review found. It carries forward the split-issuer acceptance, the two claim-name settings, and the docs and spec framing. It changes the reverted work in the following respects: the subject fallback to `sub` is removed (Decision 6); the claim names are passed at construction rather than through setters that wrote fields `Verify` read without synchronization (Decision 10); the forked `oidcClaimIdentity` is replaced by a parameterized shared helper (Decision 9); the group-encoding tolerance is added in that shared helper, which the reverted work never touched (Decision 11); the config-file keys are wired rather than documented alone (Decision 15); the `Settings()` rows report `yamlSrc` rather than `defaultSrc` (Decision 16); the second accepted issuer is stored trailing-slash-normalized like the configured one; and the stale rule restatements in the §6.9 failure-mode table and `docs/reference/error-codes.md` are amended.

## Documentation changes

The `spec/` amendments above are normative. The non-normative documentation under `docs/` follows on acceptance.

### `docs/deployment/gateway-delegated-identity.md`

This is the page that carries the registry-side `oidc-jwt` settings, so the AD FS profile lands here. The edit stays close to the size the reverted commit validated.

Add two rows to the settings table after `:41`:

> | `identity_provider.subject_claim` | `PODIUM_OAUTH_SUBJECT_CLAIM` | `(unset; sub)` | Claim read as the caller's subject. When it is set the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`. AD FS access tokens carry `idsub` and no `sub`. |
> | `identity_provider.groups_claim` | `PODIUM_OAUTH_GROUPS_CLAIM` | `(unset; groups)` | Claim read for group membership. AD FS issuance rules emit the full claim-type URI (`http://schemas.microsoft.com/ws/2008/06/identity/claims/groups`) unless authored with a short name. Single-value and multi-value forms are both accepted. |

The `groups_claim` Notes text is the wording `e6fef17` validated. The `subject_claim` Notes text drops that commit's sentence "The configured claim takes precedence and `sub` remains the fallback", because Decision 6 removes the fallback and the §13.12 row staged above states the rejection instead. Both Default cells are written to agree with the §13.12 rows staged above; `e6fef17` wrote `unset` for one and `groups` for the other.

Extend the existing YAML block at `:26-34` with two commented lines, matching how the page already annotates optional keys. Do not add a second `identity_provider:` block, because five of its six lines already appear in the first one and two example blocks for the same provider drift apart.

```yaml
  # subject_claim: idsub                       # AD FS; default: sub
  # groups_claim: http://schemas.microsoft.com/ws/2008/06/identity/claims/groups   # AD FS; default: groups
```

Insert a paragraph after `:45`:

> The token's `iss` is accepted when it matches the configured issuer or the `access_token_issuer` published by the discovery document that issuer resolves. AD FS serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token, so no single configured value covers both roles. The signing keys still come from the `jwks_uri` in that same `https` document, so the set of trusted keys is unchanged. When the document publishes an `access_token_issuer` that differs from the configured issuer, the registry logs both accepted values at startup.
>
> A deployment that sets `identity_provider.subject_claim` lists the value of that claim in a `users:` entry, and the claim must identify one principal for the life of the deployment.

The reverted commit's sentence "Both values come from the same https discovery document" is dropped: the configured issuer comes from `PODIUM_OAUTH_ISSUER` and the document's own `issuer` field is never read (`pkg/identity/oidc_jwt.go:117,269-285`). The keys-unchanged conclusion follows from `jwks_uri`. The startup-log sentence is what Open question 1 leans on, so it belongs on the operator page. No caution about recycled email addresses is added here: that behavior is provider-independent and unchanged (`pkg/layer/composer.go:91-97`), and `users:` is defined at `docs/getting-started/concepts.md:149`.

Update the group-resolution pointer at `:45` to name `identity_provider.groups_claim` / `PODIUM_OAUTH_GROUPS_CLAIM` for an `oidc-jwt` deployment rather than sending the operator to the Auth0 cookbook sentence.

The page carries no shell fence, and `tools/doccov/scan.go:12-23,96-99` classifies YAML as non-runnable, so no `tools/doccov/manifest.yaml` entry is required and none becomes required by adding YAML lines.

### `docs/reference/error-codes.md`

Change the `auth.untrusted_token` row (`:59`) from "against the configured issuer JWKS" to "against the accepted issuers and the issuer JWKS", so it matches the amended §6.9 failure-mode row.

### `docs/deployment/oidc/auth0.md`

The cookbook currently tells operators that the `IdpGroupMapping` adapter reads the namespaced claim path, which no code implements. Change `:83` and `:132` to name `identity_provider.groups_claim` / `PODIUM_OAUTH_GROUPS_CLAIM`, state that the setting applies under `oidc-jwt`, and stop asserting that the registry reads the token under the page's own `type: oauth-device-code` configuration (`:77`), which installs no registry-side verifier (`internal/serverboot/serverboot.go:1084-1120`).

### `test/manual-validation.md`

Add S36 after S35. Title it `## S36: Successful oidc-jwt verification against a live IdP`, with the goal that an IdP-issued access token authenticates against a directly-reachable `oidc-jwt` registry and resolves group-scoped visibility (§4.6, §6.3.3). Structure it in two parts: a baseline part runnable against any IdP in the §6.3.1 tested list, which is the coverage hole S32 and S33 leave open, and an AD FS profile part covering the split issuer, `PODIUM_OAUTH_SUBJECT_CLAIM`, and the claim-type-URI group claim. The baseline part makes the scenario runnable by a maintainer with a free Okta or Entra developer tenant instead of permanently skipped.

Add the index row after the S35 row, which is the table's last row (`test/manual-validation.md:119`), following the S35 precedent in `e3510b6`:

```
| S36 | Successful oidc-jwt verification against a live IdP | standalone | none | none | OIDC IdP (AD FS for the split-issuer steps) |
```

Add a **Prerequisites** block, which `test/manual-validation.md:68-70` requires for a live-infrastructure scenario: an OIDC IdP whose discovery document is reachable over `https`, a client that can complete an authorization or device-code grant against it, an `aud` value the token carries, and, for the AD FS part, a farm whose issuance rules emit a group claim. State that the AD FS part is skipped and the skip recorded when no farm is available.

Trim the **Covers** line. The split issuer, the subject claim, the group claim name, and the single-string group encoding are all asserted by the unit and integration tests staged above against a synthetic IdP. Say so in one sentence, and scope Covers to what a live IdP alone establishes: a published discovery document, a token signed by that IdP, and the path from the bearer header to resolved visibility.

Step-level details the scenario has to name:

1. How the bearer token is obtained. The registry is directly reachable under `oidc-jwt` (§6.3.3), so the token comes from the IdP directly through a raw device-code exchange with `curl`, rather than through a Podium client, which sends no credential of its own in this mode.
2. Which value goes in the layer's `groups:` list. With `PODIUM_IDP_GROUP_MAPPING` unset the claim values pass through unmapped (`internal/serverboot/identity_verify.go:185-188`, `pkg/identity/group_mapping.go:80-104`), so the step decodes the token, reads the raw group value the IdP emits, and puts that value in `groups:`, or sets `PODIUM_IDP_GROUP_MAPPING` and says so.
3. The scenario adds no "caller in exactly one group" step, because group membership on a production directory is not arrangeable by the runner and both encodings are unit-tested.

Add a capture step: the runner saves the farm's discovery document with the hostname redacted and records the observed `issuer`, `access_token_issuer`, and `jwks_uri`. The repository holds no AD FS fixture and no captured discovery document, and the staged tests write their own discovery JSON, so this step is what converts the vendor claim the issuer amendment rests on into repository evidence.

The step that confirms the startup log names both accepted issuers depends on Decision 3 and on Open question 1 resolving to unconditional acceptance. A reviewer who adds an opt-in boolean changes that step and the Prerequisites with it.

The new and edited prose follows `.claude/rules/doc-style.md`.

## Non-goals

- Amending §4.6. It names "the caller's OIDC subject or email" without naming a claim, `pkg/layer/composer.go` is untouched, and the restatements in the §4.6 visibility table, §7.3.1, and §11 stay accurate.
- Adding a §6.10 error code, or editing the `auth.untrusted_token` catalog entry. The entry and its `suggested_action` remain accurate under the amended issuer rule.
- Changing `trusted-headers`. It reads identity from headers and consults no token, so none of these settings apply to it.
- Changing the §6.3.2 injected-session-token claim requirements. The claim-name settings are scoped to `oidc-jwt`; the runtime verifier keeps `sub` and `groups` and gains the group-encoding tolerance from the shared helper.
- Adding an AD FS branch or any vendor-conditional code path. The settings name claims and read a discovery-document field, and nothing tests for a specific IdP.
- Making the email claim configurable. AD FS emits `email` through an issuance rule, and no observed failure needs it.
- Re-reading the discovery document on a JWKS refresh, or adding a discovery refresh interval. `jwks_uri` and `access_token_issuer` are both resolved once per process and stay that way.
- Adding a per-IdP cookbook page under `docs/deployment/oidc/`. Those pages cover client-side `oauth-device-code` setup, and the AD FS content belongs on the gateway-delegated identity page.
- Changing `PODIUM_IDP_GROUP_MAPPING`, the `IdpGroupMapping` adapter, or SCIM group resolution.
- Adding a client-side or MCP-server surface. The MCP server's `PODIUM_IDENTITY_PROVIDER` does not admit `oidc-jwt` (§6.3.3), so the new settings are registry-only.
- Adding a migration path or a compatibility flag. Podium is pre-1.0, both settings are unset by default, and an unset setting reproduces today's behavior.

## Resolved in adversarial review

### Pass 1 (2026-08-13, automated)

- **The failure-mode table is §6.9, not §6.3.** The amendment heading, the edit instruction, Decision 17, the reverted-branch summary, and the `docs/reference/error-codes.md` rationale all labeled the table §6.3. §6.3 is "Identity Providers" (`spec/06-mcp-server.md:38-103`) and holds no table; the replaced row is in the table under "## 6.9 Failure Modes" (`spec/06-mcp-server.md:308`, row at `:317`). The label now reads §6.9 in all five places. The quoted replacement row and the `spec/06-mcp-server.md:317` anchor were already correct and are unchanged.
- **The staged §13.12 groups-claim row asserted that a SCIM registry does not read the token group claim.** The claim is read on every verified `oidc-jwt` token by the shared derivation helper (`pkg/identity/oidc_jwt.go:197` calling `pkg/identity/runtime.go:231`), `oidcJWTVerifier` forwards the result into `layer.Identity.Groups` with no SCIM condition (`internal/serverboot/identity_verify.go:185-193`), and the §4.6 evaluator matches those claim-derived groups before it consults the resolver (`pkg/layer/composer.go:75-88`). A SCIM resolver is installed additively on `PODIUM_SCIM_TOKENS` (`internal/serverboot/serverboot.go:927-936`) and suppresses nothing. The row and Decision 7 now state that the claim is read on every verified token and that SCIM-resolved membership is matched in addition. Exclusive semantics would be a change to SCIM group resolution, which the non-goals exclude.
- **The `docs/deployment/gateway-delegated-identity.md` instruction reused `e6fef17` wording that reintroduced the `sub` fallback.** That commit's `subject_claim` row read "The configured claim takes precedence and `sub` remains the fallback", which contradicts Decision 6, the staged §13.12 row, the staged §6.3.3 paragraph, and the staged code. The instruction now spells out both rows verbatim with the fallback sentence dropped, keeps the `groups_claim` Notes wording from `e6fef17`, and records why the fallback sentence is dropped.
- **The manual-validation index anchor pointed at the S32 row.** `test/manual-validation.md:116` is the S32 row and the table's last row is S35 at `:119`, so the cited anchor would have inserted S36 between S31 and S32 and contradicted the neighboring "Add S36 after S35" instruction. The anchor now names the S35 row at `test/manual-validation.md:119`, matching the `e3510b6` precedent of appending after the previous last row.

## Open questions

1. **Unconditional acceptance of `access_token_issuer`, or an explicit operator opt-in. Resolved at sign-off (2026-08-13): unconditional, as recommended. No opt-in setting is added, and the staged amendments and code stand as written.** The audience check is the reason the residual risk below does not bind: §6.3.3 makes `PODIUM_OAUTH_AUDIENCE` mandatory and refuses boot without it, so a token accepted under the second issuer still has to carry this registry's audience. An IdP would have to share a JWKS across tenants, share an audience across them, and publish `access_token_issuer` for the boundary to collapse, and at that point `iss` was not the boundary. The reasoning recorded below stands as the rationale.

   The recommendation was unconditional, because the value has one source (the `https` discovery document the configured issuer resolves), it is compared as a string and never dereferenced, and an absent field reduces the rule to today's single-issuer check. The startup log naming both accepted issuers (Decision 3) is the operator-visible signal that stands in for an opt-in. The residual risk is a shared-JWKS multi-tenant IdP whose `iss` check is the tenant boundary, where a published `access_token_issuer` would collapse that boundary with no operator action; that IdP class does not publish the field, and the value is fixed at boot from a document the operator already named. A reviewer who wants the trust decision recorded in configuration rather than inferred from a vendor extension field adds a boolean setting instead, at the cost of one more environment variable and config-file key, and the S36 startup-log step and its Prerequisites change with it.
