# Proposal 0012: §13 stops offering `oauth-device-code` as a registry provider

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-20

This document stages no changes yet. It records a spec-internal contradiction and the two edits that resolve it, so a review run stages them rather than rediscovering the analysis. It has not been through the adversarial review loop.

## The contradiction

§6.3 and the code agree that `oauth-device-code` is the client-side acquisition provider: the consumer obtains and caches the token, and the registry has no request-time verifier for it. `identityVisibilityGuard` (`internal/serverboot/identity_verify.go`) refuses startup with `config.identity_provider_unverified` when a provider is selected and no verifier is installed, naming the three the registry verifies server-side. §6.3.3 states the same boundary from the other side.

§13 contradicts both, in two places.

Its identity-provider paragraph (`spec/13-deployment.md:468`) says "`oauth-device-code` and `injected-session-token` apply on both the registry and the MCP server". Only the second half is true. A registry that selects `oauth-device-code` does not start.

Its `registry.yaml` example (`spec/13-deployment.md:551`) configures exactly that:

```yaml
  identity_provider:
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default
```

An operator who copies the example gets a registry that refuses to boot.

## Why it matters more than a stale sentence

The example has already been copied into a shipped artifact. The Helm chart's `values.yaml` selected `oauth-device-code` as its default identity provider, which meant a default `helm install` could not start, and it was corrected in the change that added `test/chart/chart_test.go`. The spec text that produced it was left in place and is still there to be copied again.

## The edits

Two, both in `spec/13-deployment.md`, and both narrow.

The identity-provider paragraph states which providers the registry process accepts and which belong to the MCP server, rather than asserting that two apply to both. The replacement has to keep the `injected-session-token` half true, since that provider does apply on both.

The `registry.yaml` example selects a provider the registry accepts. `oidc-jwt` is the natural choice, because the surrounding example already carries an `audience` and an `authorization_endpoint`, which are the fields `oidc-jwt` uses. The alternative is to drop the `identity_provider` block from the example and cross-reference §6.3, which trades a working example for a shorter one.

## Decisions for the reviewer

1. Whether the example selects `oidc-jwt` or drops the block. Selecting a provider keeps the example runnable and risks the same staleness later; dropping it removes a copyable default and makes the reader follow a cross-reference.
2. Whether §13.12's environment-variable table and the §13.11 mode table carry the same claim and need the same correction. This proposal stages edits to the two sites named above; a review run should establish whether the claim appears elsewhere in §13 before the edits are called complete.

## What needs no edit

The documentation is already correct and is not a follow-on. `docs/getting-started/how-it-works.md` and `docs/deployment/integrations.md` both state that setting `oauth-device-code` on the registry aborts startup with `config.identity_provider_unverified`, and `docs/consuming/` describes it as the client-side flow. The docs were corrected in an earlier audit and the spec was not, which is the reverse of the usual direction and worth noting: the docs-alignment rule says docs follow the spec, so a reviewer should confirm the docs are right on the merits rather than treating their agreement with this proposal as evidence.

No code changes. The guard already refuses the configuration this proposal stops advertising, and `test/chart/chart_test.go` already pins the chart against it.

## Non-goals

- Any change to §6.3, §6.3.2, or §6.3.3, which are already correct.
- Any change to the guard, its error code, or the set of providers the registry verifies.
- Any change to the Helm chart, which was corrected already.

## Relationship to the deferred-defect list

This closes the items recorded as D1 and D2. D5, recorded alongside them as "the spec defines `DeviceCodeRequired` and no code path raises it", was struck after verification: `pkg/identity/identity.go` raises `ErrDeviceCodeRequired` and `sdks/podium-py/podium/client.py` defines the SDK's `DeviceCodeRequired`, which is what §6.3 claims.
