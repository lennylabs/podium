# Proposal 0012: §13 stops offering `oauth-device-code` as a registry provider

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-20

This document stages no changes yet. It records a spec-internal contradiction and the edits that resolve it, so a review run stages them rather than rediscovering the analysis. It has not been through the adversarial review loop.

## The contradiction

§6.3 and the code agree that `oauth-device-code` is the client-side acquisition provider: the consumer obtains and caches the token, and the registry has no request-time verifier for it. `identityVisibilityGuard` (`internal/serverboot/identity_verify.go`) refuses startup with `config.identity_provider_unverified` when a provider is selected and no verifier is installed, naming the three the registry verifies server-side. §6.3.3 states the same boundary from the other side.

§13 contradicts both, in three places. The first two are established. The third is diagnosed but unverified, and is marked as such below and in the edits.

Its identity-provider paragraph (`spec/13-deployment.md:468`) says "`oauth-device-code` and `injected-session-token` apply on both the registry and the MCP server". Only the second half is true. A registry that selects `oauth-device-code` does not start.

Its `registry.yaml` example (`spec/13-deployment.md:551`) configures exactly that:

```yaml
  identity_provider:
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default
```

An operator who copies the example gets a registry that refuses to boot.

Its web-UI paragraph (`spec/13-deployment.md:170`) says that in standard deployments "the UI uses the same OAuth device-code flow as the CLI, with the verification URL handoff handled in-browser". The registry process serves the web UI, and the same §6.3.3 boundary that makes line 468 wrong says that process installs no request-time verifier for `oauth-device-code`, so the sentence does not name what verifies the token the flow produces.

This diagnosis is not yet verified, and the difference between its two readings decides whether the edit below is correct. Under the first, the sentence is a third instance of the same defect and a standard deployment authenticates its UI and CLI through a verified provider, which makes the edit a text correction. Under the second, §6.3 leaves the standard-deployment authentication path underspecified, the defect is larger than D1 and D2, and the edit would paper over it. Nothing in this document establishes which reading holds.

## Why it matters more than a stale sentence

The example has already been copied into a shipped artifact. The Helm chart's `values.yaml` selected `oauth-device-code` as its default identity provider, which meant a default `helm install` could not start, and it was corrected in the change that added `test/chart/chart_test.go`. The spec text that produced it was left in place and is still there to be copied again.

## The edits

Three, all in `spec/13-deployment.md`. The first two are narrow and their diagnosis is established. The third is conditional on a verification the review run has to perform first, and it is described last.

The identity-provider paragraph states which providers the registry process accepts and which belong to the MCP server, rather than asserting that two apply to both. The replacement has to keep the `injected-session-token` half true, since that provider does apply on both.

The `registry.yaml` example selects a provider the registry accepts. `oidc-jwt` is the candidate, because it is the verified registry-process provider and the example's `audience` carries over unchanged. The example's `authorization_endpoint` does not carry over. `yamlIdentityCfg` (`internal/serverboot/yaml_config.go`) reads `authorization_endpoint` for the device-code flow, and `oidc-jwt` reads `issuer` instead, which §6.3.3 requires to be an `https` URL and which the registry uses to fetch the discovery document and the JWKS. The corrected block therefore renames the key rather than only changing the `type`:

```yaml
  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default
    audience: https://podium.acme.com
```

`token_header`, `subject_claim`, `groups_claim`, and `jwks_cache_ttl_seconds` are the block's remaining `oidc-jwt` keys and all carry defaults, so the two above are the required pair. §6.3.3 fails startup with `config.oidc_jwt_audience_unset` on an unset audience and with `config.invalid_issuer_scheme` on a non-`https` issuer.

The alternative is to drop the `identity_provider` block from the example and cross-reference §6.3, which trades a working example for a shorter one. Decision 1 selects between the two, and this proposal stages both routes rather than picking one.

The third edit corrects the web-UI paragraph's account of how a standard deployment authenticates the UI, and it is gated. The review run establishes which of the two readings above holds before any text is written, and the finding is recorded in the proposal whichever way it falls.

Under the first reading, the edit names the provider a standard deployment actually uses and drops the claim that the registry runs a device-code flow of its own, in the way the paragraph's existing `oidc-jwt` and `trusted-headers` sentence already does for the gateway-fronted case. Under the second, no text is staged here: the finding is recorded, this proposal closes on the two established edits alone, and the underspecification goes to its own proposal, because a §6.3 gap is a change to a section this document's non-goals put out of scope.

An edit written before that verification would assert an authentication path this document has not established, which is the defect D1 and D2 already are.

## Decisions for the reviewer

1. Whether the example selects `oidc-jwt` or drops the block. Selecting a provider keeps the example runnable and risks the same staleness later; dropping it removes a copyable default and makes the reader follow a cross-reference.
2. Whether §13.12's environment-variable table and the §13.11 mode table carry the same claim and need the same correction. A sweep of `spec/13-deployment.md` for both `oauth-device-code` and the prose spellings of the flow found four sites. Lines 468 and 551 are the two this proposal stages. Line 28 describes the `dex` compose service and is correct as written. Line 170 is the open one, below. Neither table carries the claim, so the answer is provisionally no, and a review run should confirm the sweep rather than repeat the assumption.

3. Which reading of `spec/13-deployment.md:170` holds. This is a verification the review run performs rather than a preference it selects, and it gates the third edit. Establish what verifies the token a device-code CLI or web UI presents to a standard registry, from §6.3 and from the code, and record the answer. If a standard deployment authenticates through a verified provider, stage the text correction. If §6.3 does not establish a path, stage no text, record the finding, and route it to its own proposal. Do not resolve it by asserting whichever reading makes the smaller edit.

## What needs no edit

The documentation is already correct and is not a follow-on. `docs/getting-started/how-it-works.md` and `docs/deployment/integrations.md` both state that setting `oauth-device-code` on the registry aborts startup with `config.identity_provider_unverified`, and `docs/consuming/` describes it as the client-side flow. The docs were corrected in an earlier audit and the spec was not, which is the reverse of the usual direction and worth noting: the docs-alignment rule says docs follow the spec, so a reviewer should confirm the docs are right on the merits rather than treating their agreement with this proposal as evidence.

No code changes. The guard already refuses the configuration this proposal stops advertising, and `test/chart/chart_test.go` already pins the chart against it.

## Non-goals

- Any change to §6.3, §6.3.2, or §6.3.3. Their account of the client-side boundary is correct and is what D1 and D2 contradict. If decision 3 finds that §6.3 never states how a standard deployment authenticates a device-code client, that is a gap in what the section omits rather than an error in what it says, and it goes to its own proposal.
- Any change to the guard, its error code, or the set of providers the registry verifies.
- Any change to the Helm chart, which was corrected already.

## Relationship to the deferred-defect list

This closes the items recorded as D1 and D2. D5, recorded alongside them as "the spec defines `DeviceCodeRequired` and no code path raises it", was struck after verification: `pkg/identity/identity.go` raises `ErrDeviceCodeRequired` and `sdks/podium-py/podium/client.py` defines the SDK's `DeviceCodeRequired`, which is what §6.3 claims.
