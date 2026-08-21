# Proposal 0012: §13 stops offering `oauth-device-code` as a registry provider

- Issue: (to be filed)
- Status: Draft. Converged after 4 adversarial review rounds (6 findings fixed), then reopened: that run left decision 3 unresolved, and S4 is gated on it. Decision 3 has since been resolved by verification and S4's text is not yet staged.
- Date: 2026-08-20

This document records a spec-internal contradiction and the edits that resolve it, so a review run stages them rather than rediscovering the analysis.

## Summary

**What changes.**

- §13's identity-provider paragraph (`spec/13-deployment.md:468`) states which providers the registry process accepts and which belong to the MCP server, in place of asserting that `oauth-device-code` and `injected-session-token` both apply on both.
- §13's `registry.yaml` example (`spec/13-deployment.md:551`) stops selecting a provider the registry refuses to start on, and selects `oidc-jwt` with `issuer` and `audience` instead.
- §13.2.1's read-only-mode write set (`spec/13-deployment.md:41`) stops naming "`podium login`-driven token issuance against the local IdP-mediated session table" as a registry write path. The registry process runs no login session store and serves no token endpoint.
- §13's web-UI paragraph (`spec/13-deployment.md:170`) stops presenting the device-code flow as a standard-deployment authentication mode of its own. A standard deployment selects `oidc-jwt`, the device-code flow supplies the token `oidc-jwt` verifies, and the UI inherits the resolved identity as the gateway-fronted case already does. Decision 3, which gated this, is resolved by verification.
- The tests named under "Testing" reproduce the §13.12 `registry.yaml` example verbatim, including its `identity_provider` block, and a parser comment restates the same block. They move with the example, so this proposal stages test-fixture and comment edits alongside the spec edits. No product code changes.
- The two shipped restatements of the §13.2.1 write set that carry the struck clause, `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19`, drop it with S3, so the client-facing API reference and the operator runbook stop advertising a registry-side credential-issuing write endpoint.

**Fixed decisions.**

- The proposal stages no product-code change. The startup guard, its `config.identity_provider_unverified` error code, and the set of providers the registry verifies stay as they are.
- The `registry.yaml` example keeps an `identity_provider` block and selects `oidc-jwt`. A runnable example that the mirroring tests named under "Testing" can pin is worth more than a cross-reference that no test exercises, and those tests are what would catch the next drift. The alternative of dropping the block is withdrawn (see "Resolved in adversarial review", pass 1).
- S3 strikes the `podium login` clause rather than replacing it with a corrected write path. `cmd/podium/login.go:188-207` polls the IdP directly, the registry mux registers no auth, login, or token route, and no store holds a login session, so there is no registry write for a replacement clause to name. DOC1 applies the same strike to the two shipped restatements.
- Nothing in §6.3, §6.3.2, or §6.3.3 changes. Their account of the client-side boundary is what §13 contradicts.
- The Helm chart does not change. Its `values.yaml` default was corrected already and `test/chart/chart_test.go` pins it.
- The documentation of the §6.3 identity-provider boundary does not change. `docs/getting-started/how-it-works.md`, `docs/deployment/integrations.md`, and `docs/consuming/` already state that boundary correctly, so S1 and S2 have no documentation follow-on. The §13.2.1 write set is the exception: its restatements in `docs/reference/http-api.md` and `deploy/runbook.md` carry the clause S3 strikes and are staged as DOC1.
- The `injected-session-token` half of the line 468 claim is true and the replacement keeps it.
- `spec/13-deployment.md:28`, which describes the `dex` compose service, is correct as written and is not edited.
- D5's Go half is struck: `pkg/identity/identity.go:104` returns `ErrDeviceCodeRequired`. D5's SDK half stays open and is routed to its own proposal, because neither SDK satisfies §6.3's "SDK raises `DeviceCodeRequired`".

**Watch out for.**

- Decision 3 is resolved by verification rather than by preference, and its evidence chain is recorded under "Decisions for the reviewer". A prior review run restated the gate instead of executing it and certified the proposal with S4 still open, so confirm the chain against §6.3.3, `cmd/podium/login.go`, and the client's `Authorization: Bearer` call sites rather than accepting the resolution because it is written down. S4's staged text depends on it entirely.
- Acquisition and verification are separate concerns and the line 170 paragraph conflates them. `oauth-device-code` obtains a token and verifies nothing; `oidc-jwt` verifies a token and obtains nothing. A correction that names a provider without keeping that split will reintroduce the same confusion in new words.
- Changing only the example's `type:` to `oidc-jwt` looks sufficient and is not. `yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-92`) reads `authorization_endpoint` for the device-code flow, and `oidc-jwt` reads `issuer`, which §6.3.3 requires to be an `https` URL. An example that renames the type and keeps `authorization_endpoint` still yields a registry that does not start, now failing on `config.invalid_issuer_scheme` or an unset issuer instead. §6.3.3 also fails startup with `config.oidc_jwt_audience_unset`, so `issuer` and `audience` are the required pair. The same substitution has to reach the tests that copy the block, because their assertions read `c.oauthAuthorizationEndpoint` where the corrected example populates `c.oauthIssuer`.
- The documentation agreeing with this proposal is not evidence that this proposal is right. The docs-alignment rule says docs follow the spec, and the §6.3 boundary pages were corrected in an earlier audit while the spec was not, which is the reverse of the usual direction. Confirm those pages on the merits. Agreement is also not uniform: the §13.2.1 write set is restated in four documents, and two of them carry the clause S3 strikes.
- Accepted failure mode of the selected route for decision 1: a runnable `oidc-jwt` example can go stale the same way the `oauth-device-code` one did. The tests staged in T1 are what detects that, so they have to keep asserting the example's exact keys rather than being loosened.
- The stale spec text has already been copied into a shipped artifact once. The Helm chart's `values.yaml` selected `oauth-device-code` and a default `helm install` could not start. Fixing the chart again is a non-goal; the source text is what this proposal removes.
- Decision 2's sweep covers `oauth-device-code`, `podium login`, `token issuance`, `session table`, `IdP`, and the prose spellings of the flow. The sweep's sites are enumerated in decision 2 below. Neither §13.12's environment-variable table nor the §13.11 mode table carries the claim, so the provisional answer is that they need no correction. Confirm the sweep rather than repeating the assumption, and keep it scoped to `docs/` and `deploy/` as well as `spec/`. Scoping it to `spec/` is what left the DOC1 sites standing through pass 1.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13's identity-provider paragraph (`spec/13-deployment.md:468`) names the registry-process providers and the MCP-server providers separately, and keeps the `injected-session-token` half of the current claim true.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §13's `registry.yaml` example (`spec/13-deployment.md:551`) carries an `identity_provider` block of `type: oidc-jwt`, `issuer`, and `audience`, with the staged text in "The edits".
      Levels: —. Depends on: —
- [ ] **S3 · spec** — SPEC-3. §13.2.1's read-only-mode write set (`spec/13-deployment.md:41`) drops the `podium login` token-issuance clause, with the staged text in "The edits".
      Levels: —. Depends on: —
- [ ] **DOC1 · docs** — DOC-1. The two shipped restatements of the §13.2.1 write set drop the same clause S3 strikes: the write-endpoint list in `docs/reference/http-api.md:633` and the read-only-mode impact paragraph in `deploy/runbook.md:18-19`, with the staged text in "The edits".
      Levels: —. Depends on: S3
- [ ] **T1 · test** — TEST-1. `TestReadYAMLConfig_SpecExampleNestedBlock` (`internal/serverboot/backend_config_test.go`), `TestRegistryConfig_SpecExampleNestedInterpolation` (`test/e2e/registry_config_format_test.go`), and the `yamlIdentityCfg` comment (`internal/serverboot/yaml_config.go:79-81`) follow the corrected example, with the staged content in "Testing".
      Levels: unit, e2e. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §13's web-UI paragraph (`spec/13-deployment.md:170`) names `oidc-jwt` as what authenticates a standard deployment's UI and CLI, states that the device-code flow supplies the token it verifies, and drops the false contrast implying that a standard deployment outside `oidc-jwt` and `trusted-headers` has another authenticated path, with the staged text in "The edits".
      Interleave: this spec step is sequenced after the docs and test steps because DOC1 and T1 consume S3 and S2 rather than S4, so nothing earlier in the sequence reads text S4 stages.
      Levels: —. Depends on: S1, S2, S3

## The contradiction

§6.3 and the code agree that `oauth-device-code` is the client-side acquisition provider: the consumer obtains and caches the token, and the registry has no request-time verifier for it. `identityVisibilityGuard` (`internal/serverboot/identity_verify.go:99-104`) refuses startup with `config.identity_provider_unverified` when a provider is selected and no verifier is installed, naming the providers the registry verifies server-side. §6.3.3 states the same boundary from the other side.

§13 contradicts both, in several places: the identity-provider paragraph, the `registry.yaml` example, the §13.2.1 read-only-mode write set, and the web-UI paragraph.

Its identity-provider paragraph (`spec/13-deployment.md:468`) says "`oauth-device-code` and `injected-session-token` apply on both the registry and the MCP server". Only the second half is true. A registry that selects `oauth-device-code` does not start.

Its `registry.yaml` example (`spec/13-deployment.md:551`) configures exactly that:

```yaml
  identity_provider:
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default
```

An operator who copies the example gets a registry that refuses to boot.

Its §13.2.1 read-only-mode paragraph (`spec/13-deployment.md:41`) names "`podium login`-driven token issuance against the local IdP-mediated session table" among the registry writes that read-only mode rejects. The registry issues no token and holds no login session. `podium login` reads the registry's RFC 8414 metadata document to discover the IdP's device-authorization and token endpoints (`cmd/podium/login.go:188-207`) and then polls the IdP, and `docs/reference/cli.md:118` records that the registry process does not serve that metadata document itself. The registry mux registers `/v1/layers`, `/v1/ingest/webhook/`, `/v1/admin/erase`, `/ui/`, `/metrics`, and the meta-tool handler, and no auth, login, or token route (`internal/serverboot/serverboot.go:1220-1239`). The phrase "local IdP-mediated session table" occurs nowhere else in `spec/`, and no §7 endpoint and no §4 data model defines it. The clause therefore asserts a registry-side credential-issuing surface that does not exist, which is the same defect as the identity-provider paragraph and the example carry, and it carries a trust-anchor reading on top of it.

Its web-UI paragraph (`spec/13-deployment.md:170`) says that in standard deployments "the UI uses the same OAuth device-code flow as the CLI, with the verification URL handoff handled in-browser". The registry process serves the web UI, and the same §6.3.3 boundary that makes line 468 wrong says that process installs no request-time verifier for `oauth-device-code`, so the sentence does not name what verifies the token the flow produces.

Decision 3 verified which of two readings holds, and the first does. A standard deployment authenticates its UI and CLI through `oidc-jwt`, which is a verified registry-process provider, and the device-code flow supplies the token `oidc-jwt` verifies against the IdP's JWKS. The evidence chain is recorded under "Decisions for the reviewer". §6.3 leaves no gap, so S4 is a text correction rather than a §6.3 change.

What the paragraph gets wrong is therefore a conflation rather than a missing verifier. Acquisition and verification are separate concerns, and the sentence presents the acquisition half as though it were a standard-deployment authentication mode in its own right, then contrasts it with `oidc-jwt` and `trusted-headers` as if those were peers of it. That contrast implies a standard deployment outside those two providers has some other authenticated path, and it has none: the guard verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers`, and refuses startup on anything else.

## Impact on shipped artifacts

The example has already been copied into a shipped artifact. The Helm chart's `values.yaml` selected `oauth-device-code` as its default identity provider, which meant a default `helm install` could not start, and it was corrected in the change that added `test/chart/chart_test.go`. The spec text that produced it was left in place and is still there to be copied again.

## The edits

The spec edits all land in `spec/13-deployment.md`. S1, S2, and S3 are narrow, and DOC1 carries S3 into the two shipped documents that restate the same write set. S4 rests on decision 3's verification and is described last.

**S1.** The identity-provider paragraph states which providers the registry process accepts and which belong to the MCP server, rather than asserting that two apply to both. The replacement has to keep the `injected-session-token` half true, since that provider does apply on both.

**S2.** The `registry.yaml` example selects a provider the registry accepts. `oidc-jwt` is the candidate, because it is the verified registry-process provider and the example's `audience` carries over unchanged. The example's `authorization_endpoint` does not carry over. `yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-92`) reads `authorization_endpoint` for the device-code flow, and `oidc-jwt` reads `issuer` instead, which §6.3.3 requires to be an `https` URL and which the registry uses to fetch the discovery document and the JWKS. The corrected block therefore renames the key rather than only changing the `type`:

```yaml
  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default
    audience: https://podium.acme.com
```

`token_header`, `subject_claim`, `groups_claim`, and `jwks_cache_ttl_seconds` are the block's remaining `oidc-jwt` keys and all carry defaults, so the two above are the required pair. §6.3.3 fails startup with `config.oidc_jwt_audience_unset` on an unset audience and with `config.invalid_issuer_scheme` on a non-`https` issuer.

**S3.** The §13.2.1 sentence at `spec/13-deployment.md:41` drops the clause the registry cannot satisfy. Its named examples become:

> Ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management are named examples and do not bound the rule.

Every remaining example maps to a registry endpoint that exists. The rest of the sentence, including the §6.3.1 SCIM receiver carve-out that follows it, is unchanged.

**DOC1.** Two shipped documents restate the §13.2.1 write set with the same clause and are corrected with it. `docs/reference/http-api.md:633` is the HTTP API reference a client reads, and `deploy/runbook.md:18-19` is the read-only-mode entry of the runbook that §13 says ships with the Helm chart (`spec/13-deployment.md:37`). Both list `login-driven token issuance` (the runbook spells it `podium login local IdP-mediated tokens`) as a rejected write endpoint. Left alone, both would advertise a registry-side credential-issuing surface after the spec stops naming it, which is the divergence S3 exists to remove. Each list becomes the same enumeration S3 leaves in the spec:

> ingest webhooks, layer admin operations, freeze toggles, admin grants, and tenant management

That is the enumeration `docs/deployment/operator-guide.md:132` and `docs/reference/error-codes.md:152` already carry, so all four restatements agree with §13.2.1 after DOC1. No other prose in either paragraph changes.

**IMPLEMENTOR'S CHOICE:** how the runbook paragraph re-wraps around the shorter list. Any wrapping has to keep `deploy/runbook.md` within the line width the rest of that file uses and leave the `**Impact.**` lead-in and the `registry.read_only` code span intact.

**S4.** The web-UI edit corrects that paragraph's account of how a standard deployment authenticates the UI. Decision 3's verification, recorded under "Decisions for the reviewer", established that a standard deployment authenticates through `oidc-jwt` and that the device-code flow supplies the token `oidc-jwt` verifies, so the edit removes a conflation rather than naming a provider the spec lacks.

The replacement sentence has to carry three properties. It states that a standard deployment selects `oidc-jwt`, so the paragraph names a provider the registry actually verifies. It keeps the device-code flow in the paragraph as the acquisition step, because a browser or CLI does obtain its token that way and the sentence is not wrong about that. It removes the contrast that treats "the same OAuth device-code flow as the CLI" and "under `oidc-jwt` or `trusted-headers`" as alternative deployment postures, since the first is the acquisition half of the second rather than a peer of it. The existing sentence about the gateway-fronted case is correct and stays; what changes is that the standard case stops reading as a separate authenticated path.

**IMPLEMENTOR'S CHOICE:** the exact wording and whether the standard case merges into the following `oidc-jwt` sentence or stays its own. Any wording keeps the three properties above, leaves the `trusted-headers` bind-restriction sentence that closes the paragraph intact, and does not state that the registry performs the device-code flow.

## Testing

S2 moves text that the tests below mirror verbatim, so T1 moves them with it. Both tests cite §13.12 and declare that they pin "the documented config-file example", so leaving them on `oauth-device-code` would leave both citations false and would leave the corrected example with no mechanical check that it is a configuration the binary accepts. That absent check is what let the §13 text outlive the Helm-chart correction.

- `TestReadYAMLConfig_SpecExampleNestedBlock` (`internal/serverboot/backend_config_test.go:34`): the YAML fixture's `identity_provider` block (`:65-68`) becomes `type: oidc-jwt`, `issuer: https://acme.okta.com/oauth2/default`, and `audience: https://podium.acme.com`, and the assertion (`:111-113`) reads `c.identityProvider == "oidc-jwt"`, `c.oauthIssuer`, and `c.oauthAudience` in place of `c.oauthAuthorizationEndpoint`.
- `TestRegistryConfig_SpecExampleNestedInterpolation` (`test/e2e/registry_config_format_test.go:23`): the YAML body (`:41-43`) takes the same block, the expected `config show --server` substrings (`:74-75`) become `oidc-jwt` and the issuer URL, and `PODIUM_OAUTH_ISSUER=` joins the cleared-env list (`:56-58`) so registry.yaml stays the source. `config show --server` reports `identity_provider.issuer` (`internal/serverboot/serverboot.go:1760`), and no e2e test reads that key from a config file today, so this also closes that coverage gap.
- `internal/serverboot/yaml_config.go:79-81`: the `yamlIdentityCfg` comment states the keys the parser accepts rather than restating the spec example's block, which is what makes it go stale whenever the example moves.

`authorization_endpoint` parse coverage is unaffected and stays where it already lives, in `TestReadYAMLConfig_IdentityProviderKeysRoundTrip` (`internal/serverboot/yaml_config_test.go:188-200`), which asserts every documented `identity_provider` key independently of the example.

## Resolved in adversarial review

### Pass 1 (2026-08-21, automated)

- **§13.2.1 asserted registry-side login token issuance.** Decision 2's sweep terms reached only the literal `oauth-device-code` string, so `spec/13-deployment.md:41` was missed. The sweep terms now include `podium login`, `token issuance`, `session table`, and `IdP`; the site is recorded in "The contradiction" and in decision 2; and S3 stages the corrected sentence. The clause is struck rather than replaced, because `cmd/podium/login.go:188-207` polls the IdP directly, the registry mux registers no auth, login, or token route (`internal/serverboot/serverboot.go:1220-1239`), and no store holds a login session, so there is no registry write path for the clause to name.
- **The §13.12 example's mirrors were unstaged.** `internal/serverboot/backend_config_test.go:65-68` and `test/e2e/registry_config_format_test.go:41-43` reproduce the example's `identity_provider` block verbatim under §13.12 citations, and `internal/serverboot/yaml_config.go:79-81` restates it in a comment. The Summary's claim that nothing outside `spec/13-deployment.md` changes is corrected, "What needs no edit" now says the proposal stages no product-code change, T1 is added to the checklist, and a Testing section stages the fixture, assertion, and comment content.
- **D5 was struck on evidence that does not reach §6.3's claim.** §6.3 says the SDK raises `DeviceCodeRequired`. `sdks/podium-py/podium/client.py:56` declares the class and nothing raises it, its own docstring says so, and every raise in `sdks/podium-py/podium/_oauth.py` is `DeviceCodeError`; `sdks/podium-ts` has no `DeviceCodeRequired` and raises `DeviceCodeError` (`sdks/podium-ts/src/oauth.ts:13`). The strike is narrowed to the Go sentinel at `pkg/identity/identity.go:104`, and the SDK half stays open and is routed to its own proposal.
- **Decision 2's enumeration lagged its broadened sweep terms.** The terms were widened to close the §13.2.1 miss, and the site list was not re-run against them, so `podium login` at lines 206, 314, and 486 went unlisted and a reviewer could not tell a cleared site from a missed one. The terms were re-run and decision 2 now records those three, and the remaining `IdP` matches at lines 37, 115, 144, 226, 321, 342, and 474, as examined and correct as written, alongside line 28.
- **Decision 1's second route was claimed as staged and had no text.** The proposal stated that it staged both routes while only the `oidc-jwt` block existed. Decision 1 is resolved in favor of the `oidc-jwt` block, and dropping the block is withdrawn. The `oidc-jwt` route keeps a copyable configuration that the binary accepts, and it is the route the mirroring tests can pin, which is the check that would catch the next drift; a cross-reference is exercised by no test.

### Pass 2 (2026-08-21, automated)

- **S3's struck clause survived in two shipped documents.** The pass-1 sweep for the §13.2.1 clause was scoped to `spec/`, so `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19` kept listing registry-side login token issuance among the write endpoints that read-only mode rejects. Both restate the write set the spec names, the API reference is what a client reads and the runbook is what §13 says ships with the Helm chart (`spec/13-deployment.md:37`), and neither surface has a registry route behind it (`internal/serverboot/serverboot.go:1220-1239`). DOC1 is added to the checklist and to "The edits", staging both lists as the enumeration S3 leaves in the spec, which is the one `docs/deployment/operator-guide.md:132` and `docs/reference/error-codes.md:152` already carry. The blanket "the documentation is already correct" claims in the Summary and in "What needs no edit" are narrowed to the §6.3 identity-provider boundary that S1 and S2 touch, and decision 2 now records that the sweep terms were re-run over `docs/` and `deploy/` as well as `spec/`.

## Decisions for the reviewer

1. **Resolved in pass 1.** The example keeps an `identity_provider` block and selects `oidc-jwt`, with the text staged in S2 and the tests staged in T1. Dropping the block for a cross-reference to §6.3 is withdrawn.
2. Whether §13.12's environment-variable table and the §13.11 mode table carry the same claim and need the same correction. A sweep of `spec/13-deployment.md` for `oauth-device-code`, `podium login`, `token issuance`, `session table`, `IdP`, and the prose spellings of the flow found these sites: line 41, staged as S3; line 468, staged as S1; line 551, staged as S2; line 170, staged as S4 once decision 3 resolved; and four sites the sweep reached and found correct as written, each of which states that no authentication happens on the path it describes, consistent with §6.3 and §13.11: line 28, the `dex` compose service, which records that the registry service selects no identity provider; line 206, public mode skipping `podium login` and JWT verification; line 314, `podium login` as a no-op in the filesystem-registry feature list; and line 486, the same no-op stated for filesystem-source registries. The term `IdP` also matches lines 37, 115, 144, 226, 321, 342, and 474, which describe the IdP as external infrastructure or as the `oidc-jwt` issuer and select no registry-process provider, so they carry neither claim. Neither table carries the claim, so the answer is provisionally no, and a review run should confirm the sweep rather than repeat the assumption.

   The same terms were then re-run over `docs/` and `deploy/`, because a struck spec clause can already have been copied into a shipped document. `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19` restate the §13.2.1 write set and carry the struck clause, and both are staged as DOC1. `docs/deployment/operator-guide.md:132` and `docs/reference/error-codes.md:152` restate the same write set without it and need no edit. Every `oauth-device-code` match in `docs/` states the client-side boundary and records that setting the value on the registry aborts startup with `config.identity_provider_unverified`, and `deploy/helm/podium/values.yaml:42` says the same in a comment, so none of them carries either claim. `site/dist/` is build output and is git-ignored, so it is regenerated rather than edited.
3. **Resolved by verification.** The first reading holds: a standard deployment authenticates its UI and its CLI through `oidc-jwt`, which is a verified registry-process provider, so S4 is a text correction and no §6.3 gap is routed onward.

   The chain closes end to end. `podium login` performs RFC 8414 discovery against the IdP and polls its token endpoint (`cmd/podium/login.go:188-207`), so the token it caches is signed by the IdP. The client attaches that token as `Authorization: Bearer` on every registry call (`cmd/podium/main.go:1479`, `cmd/podium/layer.go:536`, `pkg/sync/server.go:187`, `pkg/sync/watch_server.go:110`). Under `oidc-jwt` the registry reads the header named by `token_header`, default `Authorization`, and parses it as `Bearer <token>` (§6.3.3), then validates the signature against the JWKS published by `PODIUM_OAUTH_ISSUER`, which is the same IdP. §6.3.3 states that the direct case is intended rather than merely tolerated: the mode "trusts the issuer's signing key alone and no element of the network path, so the registry may be directly reachable without an authentication bypass" (`spec/06-mcp-server.md:106`). §13 says the same from its own side, in that a remote standard deployment runs its own OIDC IdP (`spec/13-deployment.md:321`, `spec/13-deployment.md:342`).

   So the defect at line 170 is narrower than the second reading feared, and it is not that the sentence names a flow with no verifier. The device-code flow is acquisition and `oidc-jwt` is verification, and the paragraph conflates the two. It presents "the same OAuth device-code flow as the CLI" as a standard-deployment authentication mode in its own right, which implies the registry runs a flow of its own, and it then contrasts that against `oidc-jwt` and `trusted-headers`, which implies a standard deployment outside those two providers has some other authenticated path. It has none: `identityVisibilityGuard` (`internal/serverboot/identity_verify.go`) verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers` and refuses startup on anything else with `config.identity_provider_unverified`.

   S4 therefore states that a standard deployment selects `oidc-jwt`, that the device-code flow supplies the token `oidc-jwt` verifies, and that the UI inherits the resolved identity exactly as the gateway-fronted case already does. It removes the false contrast rather than adding a provider.

## What needs no edit

The documentation of the §6.3 identity-provider boundary is already correct and is not a follow-on to S1 or S2. `docs/getting-started/how-it-works.md` and `docs/deployment/integrations.md` both state that setting `oauth-device-code` on the registry aborts startup with `config.identity_provider_unverified`, and `docs/consuming/` describes it as the client-side flow. The docs were corrected in an earlier audit and the spec was not, which is the reverse of the usual direction and worth noting: the docs-alignment rule says docs follow the spec, so a reviewer should confirm the docs are right on the merits rather than treating their agreement with this proposal as evidence. The §13.2.1 write set is the one documentation surface this proposal does change, and it is staged as DOC1 rather than deferred.

This proposal stages no product-code change. The guard already refuses the configuration this proposal stops advertising, and `test/chart/chart_test.go` already pins the chart against it. The non-spec edits are the test fixtures and the parser comment listed under "Testing", which follow the §13.12 example they copy, and the two write-set restatements listed under DOC1.

## Non-goals

- Any change to §6.3, §6.3.2, or §6.3.3. Their account of the client-side boundary is correct and is what §13 contradicts. Decision 3 asked whether §6.3 also fails to state how a standard deployment authenticates a device-code client, and found that it does state it: §6.3.3 sanctions a directly reachable registry under `oidc-jwt`, which verifies the token the device-code flow acquires. No §6.3 gap remains to route onward.
- Any change to the guard, its error code, or the set of providers the registry verifies.
- Any change to the Helm chart, which was corrected already.
- Any change to the SDKs. D5's SDK half is a client-surface defect against §6.3 and goes to its own proposal.

## Relationship to the deferred-defect list

This closes the items recorded as D1 and D2, and it adds the §13.2.1 site that the original sweep missed. D5, recorded alongside them as "the spec defines `DeviceCodeRequired` and no code path raises it", is partly closed: `pkg/identity/identity.go:104` returns `ErrDeviceCodeRequired` for the MCP-server-side provider, which §6.3 surfaces through MCP elicitation. The SDK half stands. §6.3 says the SDK raises `DeviceCodeRequired` with the URL and code, `sdks/podium-py` declares that exception and never raises it, and `sdks/podium-ts` exposes `DeviceCodeError` instead, which signals a flow failure rather than a required login. That gap is routed to its own proposal.
