# Proposal 0012: §13 stops offering `oauth-device-code` as a registry provider

- Issue: (to be filed)
- Status: Implemented (2026-08-21). Signed off by the maintainer for implementation, whole, with every step in the checklist in scope. Converged after 10 adversarial review rounds (8 findings fixed); "Resolved in adversarial review" records what each pass changed. The "Manual validation" section was written after the implementation landed, so its scenarios are staged for `test/manual-validation.md` and have not been run.
- Date: 2026-08-20

This document records a spec-internal contradiction and the edits that resolve it, so a review run stages them rather than rediscovering the analysis.

## Summary

**What changes.**

- §13's identity-provider paragraph (`spec/13-deployment.md:468`) states which providers the registry process accepts and which belong to the MCP server, in place of asserting that `oauth-device-code` and `injected-session-token` both apply on both.
- §13's `registry.yaml` example (`spec/13-deployment.md:551`) stops selecting a provider the registry refuses to start on, and selects `oidc-jwt` with `issuer` and `audience` instead.
- §13.2.1's read-only-mode write set (`spec/13-deployment.md:41`) stops naming "`podium login`-driven token issuance against the local IdP-mediated session table" as a registry write path. The registry process runs no login session store and serves no token endpoint.
- §13's web-UI paragraph (`spec/13-deployment.md:170`) stops presenting the device-code flow as a standard-deployment authentication mode of its own. The device-code flow is how a CLI, an SDK, or another API client acquires its token, `oidc-jwt` is what verifies that token on a standard deployment that selects it, and the web UI runs no acquisition flow of its own and resolves identity from what the request carries. Decision 3, which gated this, is resolved by verification.
- The tests named under "Testing" reproduce the §13.12 `registry.yaml` example verbatim, including its `identity_provider` block, and a parser comment restates the same block. They move with the example, so this proposal stages test-fixture and comment edits alongside the spec edits. No product code changes.
- The two shipped restatements of the §13.2.1 write set that carry the struck clause, `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19`, drop it with S3, so the client-facing API reference and the operator runbook stop advertising a registry-side credential-issuing write endpoint.
- The one shipped restatement of the line 170 gateway sentence, `docs/deployment/gateway-delegated-identity.md:107`, takes the same predicate narrowing S4 applies, so the doc stops asserting under "either provider" a gateway-authenticates claim that does not hold for the directly reachable `oidc-jwt` registry its own line 24 already describes.

**Fixed decisions.**

- The proposal stages no product-code change. The startup guard, its `config.identity_provider_unverified` error code, and the set of providers the registry verifies stay as they are.
- The `registry.yaml` example keeps an `identity_provider` block and selects `oidc-jwt`. A runnable example that the mirroring tests named under "Testing" can pin is worth more than a cross-reference that no test exercises, and those tests are what would catch the next drift. The alternative of dropping the block is withdrawn (see "Resolved in adversarial review", pass 1).
- S3 strikes the `podium login` clause rather than replacing it with a corrected write path. `podium login` acquires its token from the IdP, and decision 3 records the endpoint-resolution chain that establishes it. The registry mux registers no auth, login, or token route, and no store holds a login session, so there is no registry write for a replacement clause to name. DOC1 applies the same strike to the two shipped restatements.
- Nothing in §6.3, §6.3.2, or §6.3.3 changes. Their account of the client-side boundary is what §13 contradicts.
- The Helm chart does not change. Its `values.yaml` default was corrected already and `test/chart/chart_test.go` pins it.
- The documentation of the §6.3 identity-provider boundary does not change. `docs/getting-started/how-it-works.md`, `docs/deployment/integrations.md`, and `docs/consuming/` already state that boundary correctly, so S1 and S2 have no documentation follow-on. Two documentation surfaces are the exceptions: the §13.2.1 write set, restated in `docs/reference/http-api.md` and `deploy/runbook.md` with the clause S3 strikes and staged as DOC1, and the line 170 gateway sentence, restated in `docs/deployment/gateway-delegated-identity.md:107` with the predicate S4 narrows and staged as DOC2.
- The `injected-session-token` half of the line 468 claim is true and the replacement keeps it.
- `spec/13-deployment.md:28`, which describes the `dex` compose service, is correct as written and is not edited.
- Neither §13.12's environment-variable table nor the §13.10 standalone-versus-standard mode table (`spec/13-deployment.md:138-152`) is edited. Decision 2 resolves that neither carries the claim S1 and S2 correct.
- The web UI gains no authentication flow of its own. S4 states that a directly reachable UI request is anonymous under `oidc-jwt`, and in-browser authentication is routed to its own proposal.
- D5's Go half is struck: `pkg/identity/identity.go:104` returns `ErrDeviceCodeRequired`. D5's SDK half stays open and is routed to its own proposal, because neither SDK satisfies §6.3's "SDK raises `DeviceCodeRequired`".

**Watch out for.**

- Decision 3 is resolved by verification rather than by preference, and its evidence chain is recorded under "Decisions for the reviewer". A prior review run restated the gate instead of executing it and certified the proposal with S4 still open, so confirm the chain against §6.3.3, `cmd/podium/login.go`, and the client's `Authorization: Bearer` call sites rather than accepting the resolution because it is written down. S4's staged text depends on it entirely.
- Acquisition and verification are separate concerns and the line 170 paragraph conflates them. `oauth-device-code` obtains a token and verifies nothing; `oidc-jwt` verifies a token and obtains nothing. A correction that names a provider without keeping that split will reintroduce the same confusion in new words.
- The shipped web UI attaches no credential of any kind. Its only network call is a bare same-origin `fetch` with no headers (`web/app.js:12`), the embedded bundle is `index.html`, `app.js`, and `style.css` (`web/web.go:12-13`), and `/ui/` is served by a plain `http.FileServer` with no auth middleware (`internal/serverboot/serverboot.go:1229-1231`). S4's wording therefore must not attribute a device-code flow or a held token to the browser, and it must not say that a directly reachable UI inherits an authenticated identity, because under `oidc-jwt` a request with no Bearer value is anonymous and sees public visibility only (`spec/06-mcp-server.md:96`).
- Changing only the example's `type:` to `oidc-jwt` looks sufficient and is not. `yamlIdentityCfg` (`internal/serverboot/yaml_config.go:82-92`) reads `authorization_endpoint` for the device-code flow, and `oidc-jwt` reads `issuer`, which §6.3.3 requires to be an `https` URL. An example that renames the type and keeps `authorization_endpoint` still yields a registry that does not start, now failing on `config.invalid_issuer_scheme` or an unset issuer instead. §6.3.3 also fails startup with `config.oidc_jwt_audience_unset`, so `issuer` and `audience` are the required pair. The same substitution has to reach the tests that copy the block, because their assertions read `c.oauthAuthorizationEndpoint` where the corrected example populates `c.oauthIssuer`.
- The documentation agreeing with this proposal is not evidence that this proposal is right. The docs-alignment rule says docs follow the spec, and the §6.3 boundary pages were corrected in an earlier audit while the spec was not, which is the reverse of the usual direction. Confirm those pages on the merits. Agreement is also not uniform: the §13.2.1 write set is restated in four documents, and two of them carry the clause S3 strikes.
- Accepted failure mode of the selected route for decision 1: a runnable `oidc-jwt` example can go stale the same way the `oauth-device-code` one did. The tests staged in T1 are what detects that, so they have to keep asserting the example's exact keys rather than being loosened.
- The stale spec text has already been copied into a shipped artifact once. The Helm chart's `values.yaml` selected `oauth-device-code` and a default `helm install` could not start. Fixing the chart again is a non-goal; the source text is what this proposal removes.
- The sweep has to cover `docs/` and `deploy/` as well as `spec/`, because scoping it to `spec/` is what left the DOC1 sites standing through pass 1. Decision 2 records the swept terms, the staged sites, and the tables it clears.

  **IMPLEMENTOR'S CHOICE:** how much of decision 2's enumeration the surrounding prose restates. Decision 2 is the single record of the swept terms, the staged sites, and the cleared tables, and every other mention cites it rather than repeating it.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §13's identity-provider paragraph (`spec/13-deployment.md:468`) names the registry-process providers and the MCP-server providers separately, and keeps the `injected-session-token` half of the current claim true, with the staged text in "The edits".
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-2. §13's `registry.yaml` example (`spec/13-deployment.md:551`) carries an `identity_provider` block of `type: oidc-jwt`, `issuer`, and `audience`, with the staged text in "The edits".
      Levels: —. Depends on: —
- [x] **S3 · spec** — SPEC-3. §13.2.1's read-only-mode write set (`spec/13-deployment.md:41`) drops the `podium login` token-issuance clause, with the staged text in "The edits".
      Levels: —. Depends on: —
- [x] **DOC1 · docs** — DOC-1. The two shipped restatements of the §13.2.1 write set drop the same clause S3 strikes: the write-endpoint list in `docs/reference/http-api.md:633` and the read-only-mode impact paragraph in `deploy/runbook.md:18-19`, with the staged text in "The edits".
      Levels: —. Depends on: S3
- [x] **T1 · test** — TEST-1. `TestReadYAMLConfig_SpecExampleNestedBlock` (`internal/serverboot/backend_config_test.go`), `TestRegistryConfig_SpecExampleNestedInterpolation` (`test/e2e/registry_config_format_test.go`), and the `yamlIdentityCfg` comment (`internal/serverboot/yaml_config.go:79-81`) follow the corrected example, with the staged content in "Testing".
      Levels: unit, e2e. Depends on: S2
- [x] **S4 · spec** — SPEC-4. §13's web-UI paragraph (`spec/13-deployment.md:170`) states that a standard deployment which selects `oidc-jwt` verifies the token a CLI, an SDK, or another API client acquired through the device-code flow, that the web UI runs no acquisition flow of its own and resolves identity from what the request carries, and that the retained gateway sentence applies to the gateway-fronted deployment, and it drops the false contrast implying that a standard deployment outside `oidc-jwt` and `trusted-headers` has another authenticated path, with the staged text in "The edits".
      Interleave: this spec step is sequenced after the DOC1 and T1 steps because those consume S3 and S2 rather than S4, so nothing earlier in the sequence reads text S4 stages.
      Levels: —. Depends on: S1, S2, S3
- [x] **DOC2 · docs** — DOC-2. The shipped restatement of the line 170 gateway sentence, the web-UI paragraph in `docs/deployment/gateway-delegated-identity.md:107`, takes the same predicate narrowing S4 applies, with the staged text in "The edits".
      Levels: —. Depends on: S4

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

Its §13.2.1 read-only-mode paragraph (`spec/13-deployment.md:41`) names "`podium login`-driven token issuance against the local IdP-mediated session table" among the registry writes that read-only mode rejects. The registry mux registers `/v1/layers`, `/v1/ingest/webhook/`, `/v1/admin/erase`, `/ui/`, `/metrics`, and the meta-tool handler, and no auth, login, or token route (`internal/serverboot/serverboot.go:1220-1239`). No store holds a login session. The phrase "local IdP-mediated session table" occurs nowhere else in `spec/`, and no §7 endpoint and no §4 data model defines it. The token `podium login` caches is signed by the IdP; decision 3 records how the command resolves its endpoints and is the single account of that chain. The clause therefore asserts a registry-side credential-issuing surface that does not exist, which is the same defect as the identity-provider paragraph and the example carry, and it carries a trust-anchor reading on top of it.

**IMPLEMENTOR'S CHOICE:** how much of `podium login`'s endpoint resolution the prose around S3 restates. Any account of that chain lives in decision 3 alone, and every other mention cites decision 3 rather than repeating it, because the only conclusions S3 rests on are that the cached token is IdP-signed and that no registry write exists for a replacement clause to name.

Its web-UI paragraph (`spec/13-deployment.md:170`) says that in standard deployments "the UI uses the same OAuth device-code flow as the CLI, with the verification URL handoff handled in-browser". The registry process serves the web UI, and the same §6.3.3 boundary that makes line 468 wrong says that process installs no request-time verifier for `oauth-device-code`, so the sentence does not name what verifies the token the flow produces.

Decision 3 verified which of two readings holds, and the first does for the CLI, the SDKs, and other API callers. A standard deployment that selects `oidc-jwt`, a verified registry-process provider, verifies the IdP-signed token the device-code flow acquired, against the IdP's JWKS. The evidence chain is recorded under "Decisions for the reviewer". §6.3 leaves no gap on that path, so S4 is a text correction rather than a §6.3 change. The web UI sits outside that chain: it performs no acquisition flow and attaches no credential, so a directly reachable UI request is anonymous, and decision 3 records that separately.

What the paragraph gets wrong is therefore a conflation rather than a missing verifier. Acquisition and verification are separate concerns, and the sentence presents the acquisition half as though it were a standard-deployment authentication mode in its own right, then contrasts it with `oidc-jwt` and `trusted-headers` as if those were peers of it. That contrast implies a standard deployment outside those two providers has some other authenticated path, and it has none: the guard verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers`, and refuses startup on anything else.

## Impact on shipped artifacts

The example has already been copied into a shipped artifact. The Helm chart's `values.yaml` selected `oauth-device-code` as its default identity provider, which meant a default `helm install` could not start, and it was corrected in the change that added `test/chart/chart_test.go`. The spec text that produced it was left in place and is still there to be copied again.

## The edits

The spec edits all land in `spec/13-deployment.md`. S1, S2, and S3 are narrow, and DOC1 carries S3 into the two shipped documents that restate the same write set. S4 rests on decision 3's verification and is described last, and DOC2 carries S4 into the one shipped document that restates the sentence S4 rewrites.

**S1.** The identity-provider paragraph states which providers the registry process accepts and which belong to the MCP server, rather than asserting that two apply to both. The second sentence of `spec/13-deployment.md:468` is replaced and the third is left intact, so the paragraph becomes:

> Identity-provider selection and per-provider config are documented in §6.3 (`PODIUM_IDENTITY_PROVIDER`, `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_*`, etc.). `injected-session-token` applies on both the registry and the MCP server. `oauth-device-code` is an MCP-server value, and the registry refuses it at startup with `config.identity_provider_unverified`. `oidc-jwt` and `trusted-headers` are registry-process values that the MCP server's `PODIUM_IDENTITY_PROVIDER` does not admit (§6.3, §6.3.3).

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

**S4.** The web-UI edit corrects that paragraph's account of how a standard deployment authenticates its callers and its UI. Decision 3's verification, recorded under "Decisions for the reviewer", established that a client acquires an IdP-signed token through the device-code flow and presents it as a Bearer credential that `oidc-jwt` verifies, so the edit removes a conflation rather than naming a provider the spec lacks.

The replacement text carries these properties.

- It states the acquisition and the verification halves conditionally: a standard deployment that authenticates its own callers with `oidc-jwt` verifies the IdP-signed token a CLI, an SDK, or another API client acquired through the device-code flow. It does not assert that `oidc-jwt` is the provider a standard deployment selects, because §6.3.3 makes `oidc-jwt` and `trusted-headers` both available on a standard (§13.1) backend (`spec/06-mcp-server.md:92`) and §6.3.2 binds `injected-session-token` to the same deployment (`spec/06-mcp-server.md:80`).
- It attributes the device-code flow to the client rather than to the browser. The shipped SPA runs no acquisition flow and attaches no credential: its only network call is a bare same-origin `fetch` with no headers (`web/app.js:12`), the embedded bundle is `index.html`, `app.js`, and `style.css` (`web/web.go:12-13`), and `/ui/` is served by a plain `http.FileServer` with no auth middleware and no auth, login, or token route beside it (`internal/serverboot/serverboot.go:1229-1231`, `:1220-1239`). The replacement states that the web UI runs no device-code flow of its own and resolves identity solely from what the request carries, so a directly reachable UI request is anonymous and sees public visibility only (`spec/06-mcp-server.md:96`) unless a gateway forwards the token or injects the identity headers.
- It scopes the retained gateway sentence to the gateway-fronted deployment. That sentence's predicate today is the bare "Under `oidc-jwt` or `trusted-headers`", and it asserts that the gateway authenticates the request, which does not hold for the directly reachable registry §6.3.3 sanctions (`spec/06-mcp-server.md:106`). The replacement narrows the predicate to the case where a gateway fronts the registry, and states the direct case beside it: under `oidc-jwt` a directly reachable registry verifies the IdP-signed token the caller presents itself.
- It removes the contrast that treats "the same OAuth device-code flow as the CLI" and "under `oidc-jwt` or `trusted-headers`" as alternative deployment postures. Under `oidc-jwt` the device-code flow is the acquisition half of the same path. The contrast is not extended to `trusted-headers`, under which the client presents no token at all and the registry reads headers whose contents it does not verify (`spec/06-mcp-server.md:108`), so the existing `trusted-headers` clause keeps its own account of identity.

**IMPLEMENTOR'S CHOICE:** the exact wording, and whether the standard case merges into the gateway sentence or stays a sentence of its own. Any wording keeps the properties above, leaves `trusted-headers` and `injected-session-token` available to a standard deployment per §6.3.3 and §6.3.2, leaves the `trusted-headers` bind-restriction sentence that closes the paragraph intact, and does not state that the registry performs the device-code flow or that the browser acquires or holds a token.

**DOC2.** One shipped document restates the sentence S4 narrows. The "Web UI" paragraph of `docs/deployment/gateway-delegated-identity.md:107` opens with "Under either provider the web UI is served by the same registry process behind the same gateway and carries no device-code flow of its own. The gateway authenticates the request and the registry resolves the caller's identity, exactly as for any other API request, so the UI inherits the request's resolved identity." That is the same unscoped predicate and the same two conjuncts, and a repo-wide search for `device-code flow of its own` over `spec/`, `docs/`, `deploy/`, and `site/src` returns only this line and `spec/13-deployment.md:170`. Left alone it would keep asserting the gateway-authenticates claim for the directly reachable `oidc-jwt` registry that §6.3.3 sanctions (`spec/06-mcp-server.md:106`) and that the same page already describes at line 24: "`oidc-jwt` covers a directly-reachable registry as well, where a Podium client presents a token it acquired itself through the device-code flow." The paragraph therefore takes the same predicate narrowing S4 applies: the gateway-authenticates account is scoped to the deployment where a gateway fronts the registry, and the directly reachable `oidc-jwt` case is stated beside it, where the registry verifies the token the caller presents itself and a browser request that carries none is anonymous.

**IMPLEMENTOR'S CHOICE:** the exact wording, which follows S4's rather than copying it verbatim, because the page addresses the gateway-fronted deployment throughout and the spec paragraph also covers the standalone case. Any wording keeps the `trusted-headers` bind-restriction sentence that closes the paragraph intact, keeps the page consistent with its own line 24, and does not state that the browser acquires or holds a token.

## Resolved in adversarial review

### Pass 1 (2026-08-21, automated)

- **§13.2.1 asserted registry-side login token issuance.** Decision 2's sweep terms reached only the literal `oauth-device-code` string, so `spec/13-deployment.md:41` was missed. The sweep terms now include `podium login`, `token issuance`, `session table`, and `IdP`; the site is recorded in "The contradiction" and in decision 2; and S3 stages the corrected sentence. The clause is struck rather than replaced, because `podium login` runs `identity.DeviceCodeFlow` against the IdP's endpoints (`cmd/podium/login.go:82-95`, `:122`), the registry mux registers no auth, login, or token route (`internal/serverboot/serverboot.go:1220-1239`), and no store holds a login session, so there is no registry write path for the clause to name.
- **The §13.12 example's mirrors were unstaged.** `internal/serverboot/backend_config_test.go:65-68` and `test/e2e/registry_config_format_test.go:41-43` reproduce the example's `identity_provider` block verbatim under §13.12 citations, and `internal/serverboot/yaml_config.go:79-81` restates it in a comment. The Summary's claim that nothing outside `spec/13-deployment.md` changes is corrected, "What needs no edit" now says the proposal stages no product-code change, T1 is added to the checklist, and a Testing section stages the fixture, assertion, and comment content.
- **D5 was struck on evidence that does not reach §6.3's claim.** §6.3 says the SDK raises `DeviceCodeRequired`. `sdks/podium-py/podium/client.py:56` declares the class and nothing raises it, its own docstring says so, and every raise in `sdks/podium-py/podium/_oauth.py` is `DeviceCodeError`; `sdks/podium-ts` has no `DeviceCodeRequired` and raises `DeviceCodeError` (`sdks/podium-ts/src/oauth.ts:13`). The strike is narrowed to the Go sentinel at `pkg/identity/identity.go:104`, and the SDK half stays open and is routed to its own proposal.
- **Decision 2's enumeration lagged its broadened sweep terms.** The terms were widened to close the §13.2.1 miss, and the site list was not re-run against them, so `podium login` at lines 206, 314, and 486 went unlisted and a reviewer could not tell a cleared site from a missed one. The terms were re-run and decision 2 now records those three, and the remaining `IdP` matches at lines 37, 115, 144, 226, 321, 342, and 474, as examined and correct as written, alongside line 28.
- **Decision 1's second route was claimed as staged and had no text.** The proposal stated that it staged both routes while only the `oidc-jwt` block existed. Decision 1 is resolved in favor of the `oidc-jwt` block, and dropping the block is withdrawn. The `oidc-jwt` route keeps a copyable configuration that the binary accepts, and it is the route the mirroring tests can pin, which is the check that would catch the next drift; a cross-reference is exercised by no test.

### Pass 2 (2026-08-21, automated)

- **S3's struck clause survived in two shipped documents.** The pass-1 sweep for the §13.2.1 clause was scoped to `spec/`, so `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19` kept listing registry-side login token issuance among the write endpoints that read-only mode rejects. Both restate the write set the spec names, the API reference is what a client reads and the runbook is what §13 says ships with the Helm chart (`spec/13-deployment.md:37`), and neither surface has a registry route behind it (`internal/serverboot/serverboot.go:1220-1239`). DOC1 is added to the checklist and to "The edits", staging both lists as the enumeration S3 leaves in the spec, which is the one `docs/deployment/operator-guide.md:132` and `docs/reference/error-codes.md:152` already carry. The blanket "the documentation is already correct" claims in the Summary and in "What needs no edit" are narrowed to the §6.3 identity-provider boundary that S1 and S2 touch, and decision 2 now records that the sweep terms were re-run over `docs/` and `deploy/` as well as `spec/`.

### Pass 3 (2026-08-21, automated)

- **Decision 3's first link attributed RFC 8414 discovery to the IdP.** The registry serves no such metadata document, so on the directly reachable deployment the decision rests on, the endpoints resolve against the IdP by another path. Decision 3 was corrected and now carries the chain. The conclusion that the cached token is IdP-signed is unchanged.
- **S4's chain covered the CLI while its properties claimed the browser.** Every call site the chain cites is a Go client, and the shipped SPA performs no device-code flow and attaches no credential (`web/app.js:12`, `web/web.go:12-13`, `internal/serverboot/serverboot.go:1229-1231`), so under `oidc-jwt` a directly reachable UI request is anonymous and public-only (`spec/06-mcp-server.md:96`). S4's properties, the Summary, the checklist, and decision 3 are narrowed to the CLI, the SDK, and other API callers, S4 now requires the paragraph to state that the UI resolves identity from what the request carries, and in-browser authentication is recorded as an unimplemented gap in the non-goals and routed to its own proposal.
- **S4 required the unqualified claim that a standard deployment selects `oidc-jwt`.** §6.3.3 makes `oidc-jwt` and `trusted-headers` both available on a standard backend (`spec/06-mcp-server.md:92`) and §6.3.2 binds `injected-session-token` to it (`spec/06-mcp-server.md:80`), and the guard verifies all three. The property is restated conditionally, the acquisition claim is scoped to `oidc-jwt` so the `trusted-headers` clause keeps its own account of identity, and the implementor's-choice constraint now requires the wording to leave `trusted-headers` and `injected-session-token` available.
- **S4 preserved a gateway sentence whose predicate is unscoped.** The retained sentence asserts under the bare "Under `oidc-jwt` or `trusted-headers`" that the gateway authenticates the request, which contradicts the directly reachable case §6.3.3 sanctions (`spec/06-mcp-server.md:106`) and would contradict the new standard-deployment sentence in the same paragraph. S4 now requires the predicate to be narrowed to the gateway-fronted deployment and the direct case to be stated beside it.
- **Correction: the narrowing left its shipped doc mirror unstaged.** The property added above rewrites a sentence that `docs/deployment/gateway-delegated-identity.md:107` restates with the same unscoped predicate and the same two conjuncts, and no edit list named it. While S4 preserved the sentence verbatim the doc matched; once S4 narrowed the predicate the doc would have kept asserting the gateway-authenticates claim "Under either provider", including for the directly reachable `oidc-jwt` registry its own line 24 describes. DOC2 stages the same narrowing on that paragraph, the Summary and "What needs no edit" now list two documentation surfaces rather than one, decision 2 records the separate `device-code flow of its own` sweep that finds these two sites and no others, and the non-goal at the end no longer implies that the line is edited only by the deferred in-browser-authentication proposal.

### Pass 4 (2026-08-21, automated)

- **The token endpoint was attributed to `--issuer`.** `--issuer` supplies the device-authorization endpoint alone, and the token endpoint has its own flag and its own fallback. Decision 3 was corrected and now states the two separately. The conclusion is unchanged, because the fallback keeps the device URL's host, so the cached token is still IdP-signed.

### Pass 5 (2026-08-21, automated)

- **Two surfaces were pruned for over-specification.** The `podium login` endpoint-resolution chain was told in five places, and the copies had already drifted: "The contradiction" still attributed the discovery document to the IdP after decision 3 was corrected. The chain now lives in decision 3 alone, "The contradiction" keeps only the claims S3 rests on, the S3 rationale in "Fixed decisions" cites decision 3, and the pass-3 and pass-4 entries are cut to their conclusions, with a blank recording that every other mention cites decision 3. Decision 2's standing instruction to re-run its sweep every round was removed from "Watch out for" and from the decision itself; the swept sites and the cleared lines stay as the auditable record, and a blank routes the re-run to the implementor applying S1 through S4. The checklist, "What needs no edit", and "Testing" name no removed text and are unchanged.

### Pass 6 (2026-08-21, automated)

- **T1's e2e staging replaced the audience assertion instead of adding the issuer one.** The two identity substrings at `test/e2e/registry_config_format_test.go:74-75` are `identity_provider.type` and the audience, so staging them to "become `oidc-jwt` and the issuer URL" would have removed the only e2e check that `audience:` reaches the resolved config from a config file; `TestRegistryConfig_OAuthClaimNamesFromConfigFile` asserts only `subject_claim` and `groups_claim` (`test/e2e/registry_config_keys_test.go:181-211`). That is a loosening of the coverage T1 exists to hold, and §6.3.3 makes the audience a required key for `oidc-jwt`. The bullet now stages the type, the issuer, and the audience together, and records that `config show --server` emits `oauth_audience` (`internal/serverboot/serverboot.go:1758`) and `identity_provider.issuer` (`:1760`) as independent rows, so all three can be asserted at once.

### Pass 7 (2026-08-21, automated)

- **Decision 2 cleared a table at a section that has none.** §13.11 (`spec/13-deployment.md:230-353`) is prose and bullet lists with no table row, and the standalone-versus-standard mode table it named lives in §13.10 (`spec/13-deployment.md:138-152`, identity row at `:144`). An implementor re-running the sweep would have looked in §13.11, found nothing, and been unable to tell a cleared surface from a missed one. Decision 2 and the "Watch out for" restatement now name the §13.10 table with its line range, and decision 2 records that line 144 of its `IdP`-match list is that table's identity row, so the clearance is traceable to a match the sweep already reports.

### Pass 8 (2026-08-21, automated)

- **Three duplicate restatements were pruned and S1 was staged literally.** Pass 5 removed one over-replicated mechanism and passes 6 and 7 then corrected a second and a third copy of mechanisms the document had already fixed elsewhere, so the residue the run was producing was drift between duplicates rather than new defects. The "Watch out for" restatement of decision 2's sweep terms and cleared tables is cut to a pointer plus a blank, leaving decision 2 as the single record. Decision 3's closing paragraph, which restated S4's requirements in words that differ from the properties staged in "The edits", is deleted, so the decision ends with the verified chain and S4's requirements live in "The edits" alone. The status line's per-pass changelog is cut to the draft status and the pass count, because this section already records every pass. S1, the one deliverable with neither staged text nor a blank, now stages its replacement paragraph literally against `spec/13-deployment.md:468`, keeping the first and third sentences intact and replacing the second, and its checklist entry points at that text the way S2's and S3's do.

## Decisions for the reviewer

1. **Resolved in pass 1.** The example keeps an `identity_provider` block and selects `oidc-jwt`, with the text staged in S2 and the tests staged in T1. Dropping the block for a cross-reference to §6.3 is withdrawn.
2. Whether §13.12's environment-variable table and the §13.10 standalone-versus-standard mode table (`spec/13-deployment.md:138-152`) carry the same claim and need the same correction. §13.11 contains no table, so the mode table is the §13.10 one. A sweep of `spec/13-deployment.md` for `oauth-device-code`, `podium login`, `token issuance`, `session table`, `IdP`, and the prose spellings of the flow found these sites: line 41, staged as S3; line 468, staged as S1; line 551, staged as S2; line 170, staged as S4 once decision 3 resolved; and four sites the sweep reached and found correct as written, each of which states that no authentication happens on the path it describes, consistent with §6.3 and §13.11: line 28, the `dex` compose service, which records that the registry service selects no identity provider; line 206, public mode skipping `podium login` and JWT verification; line 314, `podium login` as a no-op in the filesystem-registry feature list; and line 486, the same no-op stated for filesystem-source registries. The term `IdP` also matches lines 37, 115, 144, 226, 321, 342, and 474, which describe the IdP as external infrastructure or as the `oidc-jwt` issuer and select no registry-process provider, so they carry neither claim. Line 144 is the identity row of the §13.10 mode table, so that table is cleared by the same match. Neither table carries the claim, so the answer is no and neither table is edited.

   **IMPLEMENTOR'S CHOICE:** when to re-run the sweep terms while applying S1 through S4. The terms and the site list are fixed as recorded above, so any re-run reproduces this enumeration or reports a new site as a defect against this decision, and it is a task for the implementor rather than an obligation a review round repeats.

   The same terms were then re-run over `docs/` and `deploy/`, because a struck spec clause can already have been copied into a shipped document. `docs/reference/http-api.md:633` and `deploy/runbook.md:18-19` restate the §13.2.1 write set and carry the struck clause, and both are staged as DOC1. `docs/deployment/operator-guide.md:132` and `docs/reference/error-codes.md:152` restate the same write set without it and need no edit. Every `oauth-device-code` match in `docs/` states the client-side boundary and records that setting the value on the registry aborts startup with `config.identity_provider_unverified`, and `deploy/helm/podium/values.yaml:42` says the same in a comment, so none of them carries either claim. `site/dist/` is build output and is git-ignored, so it is regenerated rather than edited.

   The sentence S4 rewrites was swept separately, because it is not an `oauth-device-code` match. `device-code flow of its own` over `spec/`, `docs/`, `deploy/`, and `site/src` returns `spec/13-deployment.md:170` and `docs/deployment/gateway-delegated-identity.md:107` and nothing else, so DOC2 is the whole shipped follow-on to S4.
3. **Resolved by verification.** The first reading holds for the CLI, the SDKs, and other API callers: a client acquires an IdP-signed token, and a standard deployment that selects `oidc-jwt`, a verified registry-process provider, verifies it on every request. S4 is therefore a text correction on that path and no §6.3 gap is routed onward for it.

   The chain closes end to end. `podium login` takes the device-authorization endpoint from `--issuer` or `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` (`cmd/podium/login.go:35`, `:65`) and the token endpoint from `--token-url` or `PODIUM_OAUTH_TOKEN_URL` (`cmd/podium/login.go:36`, `:66`). When `--issuer` is unset it probes the resolved registry URL for an RFC 8414 metadata document (`cmd/podium/login.go:67-77`, `:184-207`). The registry process serves no such document (`internal/serverboot/serverboot.go:1220-1239`, `docs/reference/cli.md:118`), so on the directly reachable deployment this decision rests on, the `--issuer` path is the one that applies. When `--token-url` is also unset, the token endpoint is derived from the device-authorization URL by `guessTokenURL` (`cmd/podium/login.go:78-80`, `:385-392`), which rewrites the path suffix and keeps the same IdP host, so both endpoints resolve to the IdP on that path. `podium login` then runs `identity.DeviceCodeFlow` against the IdP's device-authorization and token endpoints (`cmd/podium/login.go:82-95`, `:122`), so the token it caches is signed by the IdP. The client attaches that token as `Authorization: Bearer` on every registry call (`cmd/podium/main.go:1479`, `cmd/podium/layer.go:536`, `pkg/sync/server.go:187`, `pkg/sync/watch_server.go:110`). Under `oidc-jwt` the registry reads the header named by `token_header`, default `Authorization`, and parses it as `Bearer <token>` (§6.3.3), then validates the signature against the JWKS published by `PODIUM_OAUTH_ISSUER`, which is the same IdP. §6.3.3 states that the direct case is intended rather than merely tolerated: the mode "trusts the issuer's signing key alone and no element of the network path, so the registry may be directly reachable without an authentication bypass" (`spec/06-mcp-server.md:106`). §13 says the same from its own side, in that a remote standard deployment runs its own OIDC IdP (`spec/13-deployment.md:321`, `spec/13-deployment.md:342`).

   So the defect at line 170 is narrower than the second reading feared, and it is not that the sentence names a flow with no verifier. The device-code flow is acquisition and `oidc-jwt` is verification, and the paragraph conflates the two. It presents "the same OAuth device-code flow as the CLI" as a standard-deployment authentication mode in its own right, which implies the registry runs a flow of its own, and it then contrasts that against `oidc-jwt` and `trusted-headers`, which implies a standard deployment outside those two providers has some other authenticated path. It has none: `identityVisibilityGuard` (`internal/serverboot/identity_verify.go`) verifies `injected-session-token`, `oidc-jwt`, and `trusted-headers` and refuses startup on anything else with `config.identity_provider_unverified`.

   The web UI sits outside that chain. Every call site named above is a Go client. The shipped SPA performs no device-code flow, holds no token, and attaches no credential: its only network call is a bare same-origin `fetch` with no headers (`web/app.js:12`), the embedded bundle is `index.html`, `app.js`, and `style.css` (`web/web.go:12-13`), and `/ui/` is a plain `http.FileServer` with no auth middleware (`internal/serverboot/serverboot.go:1229-1231`). Under `oidc-jwt` a request that carries no Bearer value is anonymous and sees public visibility only (`spec/06-mcp-server.md:96`), so a directly reachable UI request is anonymous and there is no resolved identity for it to inherit. The gateway-fronted case works because the gateway authenticates the browser session, which is a mechanism separate from device-code acquisition. In-browser authentication would need product code in `web/app.js`, a §6.3 edit, and an end-to-end test, all of which this proposal excludes, so it is recorded in the non-goals and routed to its own proposal.

## What needs no edit

The documentation of the §6.3 identity-provider boundary is already correct and is not a follow-on to S1 or S2. `docs/getting-started/how-it-works.md` and `docs/deployment/integrations.md` both state that setting `oauth-device-code` on the registry aborts startup with `config.identity_provider_unverified`, and `docs/consuming/` describes it as the client-side flow. The docs were corrected in an earlier audit and the spec was not, which is the reverse of the usual direction and worth noting: the docs-alignment rule says docs follow the spec, so a reviewer should confirm the docs are right on the merits rather than treating their agreement with this proposal as evidence. The §13.2.1 write set and the line 170 gateway sentence are the two documentation surfaces this proposal does change, and they are staged as DOC1 and DOC2 rather than deferred.

This proposal stages no product-code change. The guard already refuses the configuration this proposal stops advertising, and `test/chart/chart_test.go` already pins the chart against it. The non-spec edits are the test fixtures and the parser comment listed under "Testing", which follow the §13.12 example they copy, the two write-set restatements listed under DOC1, and the web-UI paragraph listed under DOC2.

## Testing

S2 moves text that the tests below mirror verbatim, so T1 moves them with it. Both tests cite §13.12 and declare that they pin "the documented config-file example", so leaving them on `oauth-device-code` would leave both citations false and would leave the corrected example with no mechanical check that it is a configuration the binary accepts. That absent check is what let the §13 text outlive the Helm-chart correction.

- `TestReadYAMLConfig_SpecExampleNestedBlock` (`internal/serverboot/backend_config_test.go:34`): the YAML fixture's `identity_provider` block (`:65-68`) becomes `type: oidc-jwt`, `issuer: https://acme.okta.com/oauth2/default`, and `audience: https://podium.acme.com`, and the assertion (`:111-113`) reads `c.identityProvider == "oidc-jwt"`, `c.oauthIssuer`, and `c.oauthAudience` in place of `c.oauthAuthorizationEndpoint`.
- `TestRegistryConfig_SpecExampleNestedInterpolation` (`test/e2e/registry_config_format_test.go:23`): the YAML body (`:41-43`) takes the same block, the expected `config show --server` substrings gain the issuer URL rather than losing the audience, and `PODIUM_OAUTH_ISSUER=` joins the cleared-env list (`:56-58`) so registry.yaml stays the source. The identity entries of the substring list (`:74-75`) become `oidc-jwt` for `identity_provider.type`, `https://acme.okta.com/oauth2/default` for `identity_provider.issuer`, and `https://podium.acme.com` for the audience, so every key of the corrected block stays pinned at the e2e level. `config show --server` emits the audience and the issuer as independent rows, `oauth_audience` (`internal/serverboot/serverboot.go:1758`) and `identity_provider.issuer` (`:1760`), so all three substrings can be asserted together. Replacing the audience substring instead of adding the issuer one would drop the only e2e assertion that `audience:` reaches the resolved config from a config file, since `TestRegistryConfig_OAuthClaimNamesFromConfigFile` asserts only `subject_claim` and `groups_claim` (`test/e2e/registry_config_keys_test.go:181-211`), and §6.3.3 makes the audience a required key for the newly selected provider.
- `internal/serverboot/yaml_config.go:79-81`: the `yamlIdentityCfg` comment states the keys the parser accepts rather than restating the spec example's block, which is what makes it go stale whenever the example moves.

`authorization_endpoint` parse coverage is unaffected and stays where it already lives, in `TestReadYAMLConfig_IdentityProviderKeysRoundTrip` (`internal/serverboot/yaml_config_test.go:188-200`), which asserts every documented `identity_provider` key independently of the example.

## Manual validation

This section was added after the implementation landed, so the scenarios below were not executed during the run that applied the edits. They are staged for `test/manual-validation.md` and their numbers are assigned when they land there. Each states the surface a human reads directly and the wrong output it would catch.

The change alters what an operator observes in three ways: a `registry.yaml` example an operator copies, an operator runbook consulted during a database outage, and what a browser sees when it loads the web UI. The first two are documents rather than code, and the third is a rendering no Go test reads.

**MV1: the documented `registry.yaml` example starts a registry.**

*Covers.* The §13.12 example, the `oidc-jwt` required key pair, and the failure the corrected example replaces.

*Why by hand.* `TestReadYAMLConfig_SpecExampleNestedBlock` and `TestRegistryConfig_SpecExampleNestedInterpolation` assert that the example parses and reaches the resolved config. Neither starts a registry on it, so both would stay green against an example that parses and then refuses to boot, which is the exact state the pre-fix example was in for as long as it stood.

*Substitution, and why it is not a shortcut.* The example's `issuer` is `https://acme.okta.com/oauth2/default`, which resolves to nothing. §6.3.3 states that the registry fails to start when the discovery document or JWKS is unreachable, so the block cannot be pasted verbatim and started by anyone. The scenario keeps the block's structure and substitutes a reachable issuer, using the IdP that S36 already requires under its prerequisites, and skips when no IdP is available rather than forcing it. What is under test is the key structure, which is what the defect was about; the placeholder hostname is not.

*Steps.* Run the isolation block. Write `$WORK/registry.yaml` containing the §13.12 `identity_provider` block with the issuer substituted and the audience set to the registry's own endpoint. Start `podium serve --config "$WORK/registry.yaml"` on a loopback port and record the PID.

**Expect.** The registry reaches ready and `podium status` reports it. It does not exit with `config.identity_provider_unverified`, `config.oidc_jwt_audience_unset`, or `config.invalid_issuer_scheme`.

*Negative control.* Repeat with the pre-fix block, `type: oauth-device-code` with `audience` and `authorization_endpoint`. **Expect** startup to fail with `config.identity_provider_unverified`. A run where both blocks start has an identity provider switched off somewhere and proves nothing; record the failure rather than the success.

*Second negative control.* Repeat with `type: oidc-jwt` carrying `authorization_endpoint` in place of `issuer`, which is the edit a reader makes when they change the type alone. **Expect** startup to fail on the unset issuer. This is the trap the Summary names, and it is the one a reader is most likely to reproduce.

**MV2: the web UI on a directly reachable `oidc-jwt` registry shows public artifacts only.**

*Covers.* The claim S4 newly states, that the web UI runs no acquisition flow and resolves identity from what the request carries.

*Why by hand.* This is a browser rendering. The assertion is what a person sees in the artifact list, and no Go test reads that. The prior spec text claimed the UI ran a device-code flow with an in-browser verification handoff, and no test contradicted it for as long as it stood, because no test looks at the UI at all.

*Steps.* Start a registry under `oidc-jwt` with `--web-ui`, carrying one public layer and one `users:`-restricted layer holding a distinctly named artifact. Confirm the provider is active before asserting anything: `podium status` reports the identity provider rather than public mode. Open `/ui/` in a browser directly, with no gateway in front and no credential.

**Expect.** The UI loads and lists the public artifact. The restricted artifact does not appear, and the UI reports no authentication error, because from the registry's side nothing failed: the request carried no bearer value and resolved as anonymous.

*Negative control.* Confirm the restricted artifact exists and is reachable to an authenticated caller, by requesting it over the API with a valid token. Without this, an empty restricted layer produces the same screen and the scenario passes on nothing.

*Records a known gap.* The absent artifact is current behavior rather than a defect this proposal introduces, and the deferred in-browser-authentication proposal is what would change it. The scenario pins what the spec now says so a later change to the UI has to move this text with it.

**MV3: the runbook's read-only write set matches what the registry rejects.**

*Covers.* S3 and DOC1, the enumeration in `deploy/runbook.md` and `docs/reference/http-api.md`.

*Why by hand.* An operator reads the runbook during a database outage and works from its list. The value is that the list matches the running registry, and the struck clause sent a reader looking for a credential-issuing endpoint that has never existed.

*Steps.* Bring a registry to read-only mode as S21 already does. For each endpoint the runbook now enumerates, issue the request and read the error code rather than the status class. Then request a login or token path under `/v1/`.

**Expect.** Each enumerated endpoint returns `registry.read_only`. The login or token path returns 404, because the registry registers no such route, which is what makes the struck clause wrong rather than merely stale.

*Overlap with S21.* S21 covers read-only fallback and may already assert several of these endpoints. Fold MV3 into S21 as an additional step rather than adding a scenario, if S21's setup already reaches this state.

## Non-goals

- Any change to §6.3, §6.3.2, or §6.3.3. Their account of the client-side boundary is correct and is what §13 contradicts. Decision 3 asked whether §6.3 also fails to state how a standard deployment authenticates a device-code client, and found that it does state it: §6.3.3 sanctions a directly reachable registry under `oidc-jwt`, which verifies the token the device-code flow acquires. No §6.3 gap remains on that path.
- Any product-code change that would give the web UI an authentication flow of its own. The shipped SPA attaches no credential (`web/app.js:12`), so a directly reachable UI request is anonymous under `oidc-jwt`, and S4 states that rather than repairing it. If in-browser authentication is the intended behavior, it needs code in `web/app.js`, a §6.3 edit, a further edit to the web-UI paragraph of `docs/deployment/gateway-delegated-identity.md` that DOC2 already rewrites for the predicate narrowing, and an end-to-end test, so it goes to its own proposal alongside D5's SDK half.
- Any change to the guard, its error code, or the set of providers the registry verifies.
- Any change to the Helm chart, which was corrected already.
- Any change to the SDKs. D5's SDK half is a client-surface defect against §6.3 and goes to its own proposal.

## Relationship to the deferred-defect list

This closes the items recorded as D1 and D2, and it adds the §13.2.1 site that the original sweep missed. D5, recorded alongside them as "the spec defines `DeviceCodeRequired` and no code path raises it", is partly closed: `pkg/identity/identity.go:104` returns `ErrDeviceCodeRequired` for the MCP-server-side provider, which §6.3 surfaces through MCP elicitation. The SDK half stands. §6.3 says the SDK raises `DeviceCodeRequired` with the URL and code, `sdks/podium-py` declares that exception and never raises it, and `sdks/podium-ts` exposes `DeviceCodeError` instead, which signals a flow failure rather than a required login. That gap is routed to its own proposal. The web-UI authentication gap that decision 3 surfaced is recorded in the non-goals and routed to its own proposal as well.
