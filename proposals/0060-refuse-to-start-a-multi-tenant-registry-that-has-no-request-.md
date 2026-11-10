# Proposal 0060: Refuse to start a multi-tenant registry that has no request-time identity verifier

- Issue: (to be filed)
- Status: Applied to spec (2026-11-09). Verified on 2026-11-09 after 8 adversarial review rounds (12 findings fixed).
- Date: 2026-11-09

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is backward-incompatible: a registry that starts today with `PODIUM_MULTI_TENANT=true` and no request-time identity verifier exits at startup after it, and it ships in 0.6.0.

## Summary

**What changes.**

- §6.3.1 defines a new startup refusal, `config.multitenant_no_verifier`, for a registry started with `PODIUM_MULTI_TENANT` that configures no identity provider, names an identity-provider label the registry resolves to no configured provider, or runs in public mode with no identity provider. §13.10 and §13.12 state it, and §4.7.8 drops the quota clause for "every request on a registry with no request-time identity verifier" (SPEC-1, SPEC-2, SPEC-3, SPEC-4).
- `internal/serverboot` gains `multiTenantVerifierGuard`, called from `(*Config).validate` after `server.StartupConfig.Validate`, before the store opens (CODE-1). The `config.identity_provider_unverified` message names the preconditions for public mode (CODE-2).
- The multi-tenant-without-router boot path becomes unreachable and is removed: the serverboot `server.WithMultiTenant()` block, the `server.WithMultiTenant` option, and the unit tests that build the unreachable combination (CODE-3).
- Unit and end-to-end tests pin the refusal, its precedence, and the message. `TestLayerEndpoint_MultiTenantNoRouter` is deleted and its unrouted-request assertions move into `TestLayerEndpoint_MultiTenantRouting` step 6 under `trusted-headers` (TEST-1, TEST-2, TEST-3). Scenario S90 in `test/manual-validation.md` gains a negative-control step (MV-1).
- The deployment pages, the reference pages, and `CHANGELOG.md` replace the unrouted-registry outcome with the refusal (DOC-1, DOC-2, DOC-3).

**Fixed decisions.**

- D-1: the refusal predicate is `multiTenant && !identity.Default.Has(identityProvider)`, the nil case of `selectIdentityProvider`.
- D-2: the new code `config.multitenant_no_verifier`; `config.identity_provider_unverified` keeps its meaning and scope.
- D-3: the code is defined in §6.3.1 and referenced from §13.10 and §13.12, with no §6.10 entry and no matrix cell.
- D-4: the guard runs in `(*Config).validate`, after `server.StartupConfig.Validate`, so every earlier config refusal keeps precedence.
- D-5: the `config.identity_provider_unverified` change is message-only, with one message for both modes and the guard signature unchanged.
- D-6: the §6.10 `registry.tenant_management_unavailable` envelope text and its code mirror stay unchanged.
- D-7: the spec edits are §6.3.1, §13.10, §13.12, and §4.7.8, in one commit, before any code. §4.6 and §7.3.1 stay unchanged.
- D-8: `server.WithMultiTenant` and the serverboot block that calls it are removed; `Server.multiTenant` stays and `WithTenantRouter` alone sets it.
- D-9: unrouted-request coverage moves to a verified provider by extending an existing boot, with no new standard-stack boot.
- D-10: the change is backward-incompatible and ships in 0.6.0, and no flag, environment variable, or configuration key restores the old behavior.

**Watch out for.**

- **Key the guard on `identity.Default.Has`, never on `verifiedProviders`.** `oauth-device-code` and a custom provider registered through `identity.Default.Register` are registered without a verifier. A predicate over `verifiedProviders` would move them to the new code and break D-2 and the existing `config.identity_provider_unverified` tests. They pass CODE-1 and are refused by `identityVisibilityGuard` at `internal/serverboot/serverboot.go:1469`, after ingest.
- **Place the guard after `startup.Validate()`.** `TestValidate_MultiTenantPrecedence` (TEST-1) fails when the guard runs first, because every `StartupConfig.Validate` refusal (§6.3.1) keeps precedence, and its `config.public_bind_refused` and `config.web_ui_public_bind_refused` rows reach the guard's predicate.
- **CODE-3 depends on CODE-1.** Removing `server.WithMultiTenant()` from the boot before the refusal lands makes a multi-tenant boot with no verifier write the `podium:unrouted` binding into §8.1 audit records.
- **`TestLayerEndpoint_MultiTenantNoRouter` boots the refused configuration and skips without the standard stack.** It calls `msSkipIfNoStack`, so a local run of CODE-1 can look green while CI fails. S2 deletes the test before CODE-1 lands.
- **`ltrRouterLog` loses its only reader when the test is deleted.** The `unused` linter then fails `make lint`. TEST-3 gives it a new reader in `TestLayerEndpoint_MultiTenantRouting`.
- **The end-to-end harness inherits the test process environment.** `mergeEnv` (`test/e2e/helpers_test.go:43`) starts from `os.Environ()`, and `testenv.Load()` can populate it from `test.env`. Set `PODIUM_IDENTITY_PROVIDER=` explicitly in every no-provider case so an inherited provider does not let the boot pass.
- **A comma-separated `// Spec:` line credits no section.** specparser chains only `/` or `+` aliases after the first section, so a test annotation lists several sections as `§6.3.1 / §13.10 / §13.12`. The existing comment on `TestSmallTeam_IdentityProviderUnverifiedFailsStartup` uses the comma form, and TEST-2 rewrites it.
- **The §6.10 envelope text is pinned.** `pkg/registry/server/tenants_test.go:196` asserts the suggested action, and D-6 keeps it. Do not edit `pkg/registry/server/tenants.go:107` or `pkg/registry/server/error_envelope.go:123`.
- **Parts of `test/e2e` skip silently on macOS.** Run the end-to-end steps with `-v` and grep the output for `SKIP` before claiming they passed.
- **Q-1 is open.** The relayed instruction "F1: leave the spec as is" names no finding. Resolve Q-1 with the reviewer before S1 lands.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4. The §6.3.1 refusal, the §13.10 sentence and public-mode bullet, the §13.12 row sentence, and the §4.7.8 clause removal land in one commit, because the §13.12 sentence contradicts the current §4.7.8 text and §13.10 cites §6.3.1. Run `make coverage-gate`.
      Levels: —. Depends on: —
- [ ] **S2 · test** — TEST-3. Delete `TestLayerEndpoint_MultiTenantNoRouter` and move its unrouted assertions into step 6 of `TestLayerEndpoint_MultiTenantRouting`.
      Interleave: this test step precedes the code steps on purpose. The extension passes before and after CODE-1, and the deleted test would fail once CODE-1 lands.
      Levels: e2e. Depends on: S1
- [ ] **S3 · code** — CODE-1. `multiTenantVerifierGuard` and its call in `(*Config).validate`.
      Levels: unit, e2e. Depends on: S1, S2
- [ ] **S4 · code** — CODE-2. The rewritten `config.identity_provider_unverified` message.
      Levels: unit, e2e. Depends on: S1
- [ ] **S5 · code** — CODE-3. Remove the `WithMultiTenant` boot block and option, rewrite the stale comments, and update the unit tests that build the unreachable combination.
      Levels: unit, integration, e2e. Depends on: S3
- [ ] **S6 · test** — TEST-1. Unit tests for both guards and the `(*Config).validate` precedence.
      Levels: unit. Depends on: S3, S4
- [ ] **S7 · test** — TEST-2. End-to-end refusal through the compiled binary, and the single-tenant and multi-tenant table for `config.identity_provider_unverified`.
      Levels: e2e. Depends on: S3, S4
- [ ] **S8 · test** — MV-1. Scenario S90 gains the negative-control step 7.
      Levels: manual. Depends on: S3, S4
- [ ] **S9 · docs** — DOC-1. `docs/deployment/single-node.md`, `docs/deployment/clustered.md`, and `docs/deployment/layers.md`.
      Levels: —. Depends on: S1
- [ ] **S10 · docs** — DOC-2. `docs/reference/error-codes.md`, `docs/reference/cli.md`, and `docs/reference/http-api.md`.
      Levels: —. Depends on: S1
- [ ] **S11 · docs** — DOC-3. The CHANGELOG bullet.
      Levels: —. Depends on: S1–S10

**Ordering constraints.** S1 lands first and passes `make coverage-gate` before any later step. S2 lands before S3. S3 and S4 are independent of each other. S5 lands after S3. S6, S7, and S8 are independent of one another once S3 and S4 land. S9 and S10 are independent. S11 describes the whole change and lands last. Land every step in one pull request.

## Current state and the gap

### A multi-tenant registry with no verifier starts and routes nothing

A registry started with `PODIUM_MULTI_TENANT=true` and no request-time identity verifier boots, routes no request, and gives no sign that multi-tenant mode has no effect.

- `cfg.multiTenant` is read only from the environment (`internal/serverboot/serverboot.go:2316`).
- A multi-tenant boot binds the core to the `podium:unrouted` no-data tenant (`internal/serverboot/serverboot.go:1103-1110`; `internal/serverboot/orgid.go:16`).
- The tenant router is installed only under `cfg.multiTenant && verifierInstalled` (`internal/serverboot/serverboot.go:1452-1459`). `verifierInstalled` becomes true only in the `injected-session-token` (`:1392`), `oidc-jwt` (`:1419`), and `trusted-headers` (`:1443`) branches.
- `server.WithMultiTenant()` (`:1461-1467`) and the layer endpoint's `WithTenantRouting()` (`:1494-1503`) key on `cfg.multiTenant` alone, so every request stays on the no-data tenant.
- With no router installed, `tenantAdminGate` answers `404 registry.tenant_management_unavailable` with the fixed message "Tenant management requires a multi-tenant registry started with PODIUM_MULTI_TENANT." (`pkg/registry/server/tenants.go:105-108`) and the suggested action "Start the registry in multi-tenant mode (PODIUM_MULTI_TENANT) to manage tenants." (`pkg/registry/server/error_envelope.go:122-124`). Both mirror the §6.10 envelope, and both tell the operator to set the variable they already set.
- The only multi-tenant log line is the router line at `internal/serverboot/serverboot.go:1458`, so the operator gets no warning.
- On Postgres an unrouted search answers 500 rather than an empty catalog (`test/e2e/layer_tenant_routing_test.go:566-570`).

### The configurations that reach this state

Three configurations reach it.

1. No provider is set.
2. An unregistered free-form label such as `oidc` or `none` is set. `selectIdentityProvider` returns nil for both of the first two cases (`internal/serverboot/identity_verify.go:187-190`; `pkg/identity/registry.go:70-94`), and §4.6 *No-identity bypass* recognizes the label case as external authentication.
3. Public mode is on. `config.public_mode_with_idp` confines it to an empty provider or `none` (`pkg/registry/server/config_validate.go:126-129`).

`identityVisibilityGuard` exempts all three, because it returns nil when `!providerSelected || publicMode || verifierInstalled` (`internal/serverboot/identity_verify.go:119-124`). The only other guard that reads `MultiTenant` is `config.trusted_headers_multitenant_no_secret` (`pkg/registry/server/config_validate.go:157-165`).

A registered provider without a verifier, such as `oauth-device-code` or a custom provider, is already refused, but only by the post-ingest `identityVisibilityGuard` call (`internal/serverboot/serverboot.go:1469`). That refusal's message tells the operator to set `PODIUM_PUBLIC_MODE=true` (`internal/serverboot/identity_verify.go:123`), which on a multi-tenant registry leads back to the unrouted state.

### The spec describes the running unrouted registry

- §13.10 *Features enabled by configuration* says multi-tenant mode "routes requests to tenants only on a registry with a request-time identity verifier". Proposal 0056 SPEC-1 added that sentence deliberately.
- §13.12, in the `PODIUM_MULTI_TENANT` row, says "When `true`, the registry runs in multi-tenant mode" with no condition.
- §4.7.8 charges "every request on a registry with no request-time identity verifier" to the unrouted budget.
- §7.3.1 *Tenant selection* gives the unrouted refusal precedence "over the admin bypass on a registry started in public mode or with no identity provider configured".

No spec clause refuses this boot. Proposal 0056 documented the outcome in `docs/deployment/single-node.md`, `docs/reference/error-codes.md`, and the `PODIUM_MULTI_TENANT` row of `docs/reference/cli.md`, and it left the spec case open as its Q-2. One end-to-end test pins the unrouted boot: `TestLayerEndpoint_MultiTenantNoRouter` (`test/e2e/layer_tenant_routing_test.go:522-575`), which boots with `PODIUM_IDENTITY_PROVIDER=`.

### The decided fix

The maintainer has decided the fix: the registry refuses to start when `PODIUM_MULTI_TENANT` is set and no request-time identity verifier is installed, public mode included. This proposal stages the spec text, the startup guard, the cleanup of the code path that becomes dead, the tests, the docs, and the CHANGELOG entry for that refusal. The change is backward-incompatible and ships in 0.6.0.

## Decisions

- **D-1. Scope of the refusal.** Together, two codes cover every multi-tenant configuration without a request-time verifier. A new code, `config.multitenant_no_verifier`, covers the configurations whose identity provider resolves to no registered provider: an empty `PODIUM_IDENTITY_PROVIDER`, an unregistered label (`oidc`, `none`, or any other free-form value), and public mode, which `config.public_mode_with_idp` already restricts to an empty value or `none` (`pkg/registry/server/config_validate.go:126-129`). The predicate is `multiTenant && !identity.Default.Has(identityProvider)`. It is the same condition under which `selectIdentityProvider` returns nil (`internal/serverboot/identity_verify.go:187-190`), so the guard and the provider selection cannot drift apart. A registered provider without a verifier (`oauth-device-code`, or a custom provider imported through `identity.Default.Register`) keeps its existing refusal `config.identity_provider_unverified` (`internal/serverboot/identity_verify.go:119-124`) in either mode. With both guards in place, `PODIUM_MULTI_TENANT` on a booted registry implies `verifierInstalled`, and that implies an installed router (`internal/serverboot/serverboot.go:1452`).
- **D-2. Error code.** Add `config.multitenant_no_verifier` rather than reuse `config.identity_provider_unverified`. Three facts rule out reuse. The spec and the docs define the existing code for a provider the registry recognizes but does not verify server-side (§4.6 *No-identity bypass*, §13.12, `docs/reference/error-codes.md:81`). Its message offered `PODIUM_PUBLIC_MODE=true` as the remedy (`internal/serverboot/identity_verify.go:123`), and public mode is one of the refused causes. Widening its meaning would also force edits at every site that states the `oauth-device-code` refusal (§13.12, §4.6, `docs/getting-started/how-it-works.md:408`, `docs/deployment/integrations.md:80`). The name follows the `multitenant` spelling of `config.trusted_headers_multitenant_no_secret`, and no existing code collides with it.
- **D-3. Where the code is defined, and no matrix cell.** The new code follows the precedent of `config.trusted_headers_multitenant_no_secret`, the closest existing boot refusal on a multi-tenant registry. That code is defined in its governing section (§6.3.3), listed in `docs/reference/error-codes.md:80`, and pinned by an end-to-end test (`test/e2e/auth_gateway_test.go:253-259`), with neither a §6.10 entry nor a matrix cell. §6.10 lists no `config.*` code individually. The §6.10 error-code axis in `tools/matrix/matrices.go:84-127` does not list startup refusals such as `config.oidc_jwt_audience_unset` and `config.runtime_keys_unavailable`. The new code is therefore defined in §6.3.1, where per-request tenant selection lives, and referenced from §13.10 and §13.12, with no matrix cell. The tests carry `// Spec:` annotations for §6.3.1, §13.10, and §13.12.
- **D-4. Config-time placement and precedence.** The guard runs in `(*Config).validate` (`internal/serverboot/serverboot.go:2538-2562`) immediately after `server.StartupConfig.Validate()`. `loadBootConfig` calls it before the listener opens and before `openStore` (`internal/serverboot/serverboot.go:804-831`, `:870`), so the refusal fails before the store opens and before ingest runs. It sits in serverboot rather than in `StartupConfig.Validate` because the provider registry and `verifiedProviders` (`internal/serverboot/identity_verify.go:109`) live in `internal/serverboot`, and copying the list into `pkg/registry/server` creates the drift that the comment at `internal/serverboot/identity_verify.go:100-108` warns about. `config.invalid_sign_mode` already runs in the same function. Because it runs after `StartupConfig.Validate`, every `StartupConfig.Validate` refusal (§6.3.1) takes precedence.
- **D-5. The `config.identity_provider_unverified` message.** The message is rewritten once for both modes, and `identityVisibilityGuard` keeps its signature and its call site. The old remedy, "Set PODIUM_PUBLIC_MODE=true to run an open registry", omits two preconditions. An operator who keeps the provider and sets public mode gets `config.public_mode_with_idp`, on a single-tenant registry as on a multi-tenant one. An operator who also clears the provider on a multi-tenant registry gets `config.multitenant_no_verifier`. The new message offers the verified providers first and states both preconditions for public mode. The spec and the docs fix the code and not the message text, so this change needs no spec edit.
- **D-6. §6.10 text unchanged.** With D-1 in place, the nil-router branch of `tenantAdminGate` (`pkg/registry/server/tenants.go:105-108`) is reachable only on a registry started without `PODIUM_MULTI_TENANT`. The §6.10 fixed message and suggested action, §7.3.3, and §4.7.1 therefore become accurate with no text change. The suggested action names only `PODIUM_MULTI_TENANT`. That is a necessary condition, and the new boot refusal names the verifier remedy itself. This decision reads the relayed "F1: leave the spec as is" as covering the §6.10 envelope text (see Q-1).
- **D-7. Spec edits are required and land first.** Under the spec-driven rule, code with no spec basis is not written, and no spec clause refuses this boot today. §13.12 states without condition that the registry runs. The change set therefore adds the refusal to §6.3.1, states it in §13.10 and §13.12, and deletes the §4.7.8 clause that describes a state the refusal makes unreachable. §4.6 stays unchanged: its *No-identity bypass* paragraph describes how a running registry evaluates visibility, and a multi-tenant registry with no verifier no longer runs. §7.3.1 stays unchanged for the reasons the Non-goals record. The spec commit lands and passes `make coverage-gate` before any code step.
- **D-8. Dead multi-tenant-without-router path removed.** After D-1, a booted registry never has `cfg.multiTenant` set without a router: the one remaining case, a registered provider without a verifier, fails at `internal/serverboot/serverboot.go:1469` before `server.New`. The `if cfg.multiTenant { server.WithMultiTenant() }` block (`:1461-1467`) only duplicates `WithTenantRouter`, which already sets `s.multiTenant` (`pkg/registry/server/server.go:225-229`). `server.WithMultiTenant` (`pkg/registry/server/server.go:232-242`) has no other production caller. Podium is pre-1.0 and keeps no dual code paths, so the block and the option are removed. The unit tests that build the unreachable combination are deleted, except one audit-label row that is rebuilt as an anonymous request under a router (CODE-3). `Server.multiTenant` stays and is set by `WithTenantRouter` alone. Its comment and the comments at `internal/serverboot/serverboot.go:1452-1455` and `:1495-1500` are rewritten to drop the no-verifier case.
- **D-9. Unrouted-request coverage kept under a verified provider.** Requests that resolve to no tenant still exist under `trusted-headers` (an organization that names no tenant, or an anonymous request) and under `oidc-jwt` (a token without `org_id`). The behavior that `TestLayerEndpoint_MultiTenantNoRouter` pins therefore stays live: §7.3.1 *Tenant selection*, the §8.5 unrouted erase refusal, §7.3.4 `manage_any_layer` false, and the §8.1 unlabeled audit record. `TestLayerEndpoint_MultiTenantRouting` already boots `trusted-headers` with a proxy secret and drives an unprovisioned organization in its step 6. TEST-3 extends that step with the erase refusal, the refusal message, and the unlabeled search record, with no new boot. On a verified provider, the admin check runs in the routed tenant (§8.5), so the message "request resolves to no tenant" is the signal that attributes the 403 to tenant selection.
- **D-10. Backward-incompatible, with no shim.** A registry that starts today with `PODIUM_MULTI_TENANT=true` and no verifier exits non-zero after this change, as the boot at `test/e2e/layer_tenant_routing_test.go:540-552` shows such a registry starting today. No flag, environment variable, or configuration key restores the old behavior. The change ships in 0.6.0, and the CHANGELOG entry says it is backward-incompatible. `podium-server sign-stored-rows` shares `loadBootConfig` (`internal/serverboot/signpass.go:98`), so it refuses the same configuration, as every existing config guard already does.

## Spec amendment: §6.3.1 Claim Derivation, per-request tenant selection

**SPEC-1.** `spec/06-mcp-server.md`, §6.3.1. One edit, in one commit with SPEC-2, SPEC-3, and SPEC-4.

**Rationale.** §6.3.1 defines per-request tenant selection and the single-tenant and multi-tenant distinction, so it is the governing section for the new refusal and the definition site of `config.multitenant_no_verifier` (D-3). Without this text the boot refusal has no spec basis (D-7). The text does not restate `config.identity_provider_unverified`, which §4.6 and §13.12 already state with no mode condition. It states the precedence as one general rule, because the guard runs after `StartupConfig.Validate` (D-4), which holds the §13.10 configuration checks, and the `TestValidate_MultiTenantPrecedence` rows (TEST-1) pin the overlaps. The rule covers checks on configuration values. It claims no precedence over `config.layer_path_ambiguous`, which the registry raises from the layer content during the bootstrap ingest after the store opens, or over the §13.12 `config.invalid_sign_mode`, which runs after the guard. `config.trusted_headers_multitenant_no_secret` never meets this check, because `trusted-headers` is a verified provider. The text also states that `none` configures no identity provider. The implemented public-mode exclusion and the web UI bind check both treat `none` as no provider (`pkg/registry/server/config_validate.go:126`, `:144`), so public mode with `none` passes `config.public_mode_with_idp` and reaches this check, and the §13.10 *Mutually exclusive with an identity provider* bullet reads consistently with the code.

**Anchor.** The paragraph that begins "**Per-request tenant selection.**", directly above the paragraph that begins "**Where the directory expansion applies.**". Append text to the end of that paragraph, after its last sentence.

Current last sentence:

> A single-tenant registry, which is a standalone (§13.10) or standard (§13.1) registry started without `PODIUM_MULTI_TENANT`, resolves every authenticated request against its sole tenant and does not consult the organization value; provisioning additional tenants on such a registry does not enable per-request routing.

Appended text:

> A multi-tenant registry selects each request's tenant from the authenticated identity, so it requires a request-time identity verifier (§6.3.2, §6.3.3). A registry started with `PODIUM_MULTI_TENANT` fails to start with `config.multitenant_no_verifier` when it configures no identity provider, when it names an identity-provider label the registry resolves to no configured provider (§4.6), or when it is started in public mode (§13.10). The error names the identity providers the registry verifies at request time. A configuration that another §13.10 configuration check refuses fails with that check's error. The value `none` configures no identity provider, so public mode with `PODIUM_IDENTITY_PROVIDER` unset, empty, or `none` passes the identity-provider exclusion and fails with `config.multitenant_no_verifier`.

**IMPLEMENTOR'S CHOICE:** the code and the spec leave the set of `StartupConfig.Validate` checks that overlap the guard unenumerated — the guard runs after every `StartupConfig.Validate` check, so any refusal `Validate` returns wins.

§6.10 is unchanged (D-6).

## Spec amendment: §13.10 Standalone Deployment, multi-tenant mode and public mode

**SPEC-2.** `spec/13-deployment.md`, §13.10. Two edits.

**Rationale.** Proposal 0056 SPEC-1 wrote the *Features enabled by configuration* sentence to describe a running multi-tenant registry without a verifier, and the refusal contradicts it. The public-mode safety constraints list each startup exclusion, so the new exclusion belongs there.

**Edit 1, features sentence.** Anchor: the paragraph that begins "**Features enabled by configuration.**". Replace its second sentence.

Current text:

> Multi-tenant mode runs when `PODIUM_MULTI_TENANT` is `true` (§13.12), and it routes requests to tenants only on a registry with a request-time identity verifier (§6.3.1, §6.3.3).

Replacement:

> Multi-tenant mode runs when `PODIUM_MULTI_TENANT` is `true` (§13.12) on a registry with a request-time identity verifier (§6.3.2, §6.3.3). A standalone server started with `PODIUM_MULTI_TENANT` that configures no identity provider, names an identity-provider label the registry resolves to no configured provider, or runs in public mode with no identity provider fails to start with `config.multitenant_no_verifier` unless an earlier startup check refuses the configuration first (§6.3.1), and one that names a provider the registry recognizes but does not verify server-side fails with `config.identity_provider_unverified` (§4.6).

The rest of the paragraph is unchanged.

**Edit 2, public-mode safety constraint.** Anchor: the "Safety constraints:" list under public mode. Insert a bullet directly after the bullet that begins "**Mutually exclusive with an identity provider.**" and ends "The deployment must choose one.", and before the bullet that begins "**Loopback bind by default.**":

> - **Mutually exclusive with multi-tenant mode.** Setting `PODIUM_PUBLIC_MODE` and `PODIUM_MULTI_TENANT` together fails at startup. The registry reports `config.multitenant_no_verifier` unless an earlier startup check refuses the configuration first (§6.3.1). A multi-tenant registry selects each request's tenant from the verified caller identity, and public mode verifies no caller.

## Spec amendment: §13.12 Backend Configuration Reference, `PODIUM_MULTI_TENANT`

**SPEC-3.** `spec/13-deployment.md`, §13.12. One edit.

**Rationale.** The row says without condition that the registry runs in multi-tenant mode when the variable is `true`, which contradicts the refusal. §13.12 rows point to their governing section for detail, so the inserted sentence states the requirement and cites §6.3.1 and §4.6. It names the causes of each code, because the multi-tenant configurations without a request-time verifier fail under two codes (D-1), and a sentence that assigned `config.multitenant_no_verifier` to all of them would contradict the §13.12 identity-provider paragraph, which refuses `oauth-device-code` with `config.identity_provider_unverified`. The public-mode cause carries "with no identity provider" and the clause defers to §6.3.1 for precedence, so the row agrees with the unchanged public-mode paragraph, which refuses public mode with an identity provider with `config.public_mode_with_idp`.

**Anchor.** The table row whose first cell is `` `PODIUM_MULTI_TENANT` ``. In its description cell, insert the sentence below directly after the sentence that ends "rejecting an `oidc-jwt` token whose `org_id` names no provisioned tenant with `auth.tenant_unknown`." and before the sentence that begins "When unset, the registry binds every request to the single `default` org".

Inserted sentence:

> It requires a request-time identity verifier (§6.3.2, §6.3.3): a registry started with `true` that configures no identity provider, names an identity-provider label the registry resolves to no configured provider, or runs in public mode with no identity provider fails to start with `config.multitenant_no_verifier` unless an earlier startup check refuses the configuration first (§6.3.1), and one that names a provider the registry recognizes but does not verify server-side fails with `config.identity_provider_unverified` (§4.6).

The rest of the row, its default column, the identity-provider paragraph that names `config.identity_provider_unverified`, and the public-mode paragraph are unchanged. The §13.10 bullet SPEC-2 adds states the public-mode exclusion.

## Spec amendment: §4.7.8 Quotas, unrouted requests

**SPEC-4.** `spec/04-artifact-model.md`, §4.7.8. One edit.

**Rationale.** The clause "every request on a registry with no request-time identity verifier" names a state that no booted registry reaches after the refusal. The two remaining unrouted cases stay, and the sentence keeps covering them.

**Anchor.** The paragraph that begins "Each `search_domains` and `search_artifacts` request counts against the search QPS limit". Replace its third sentence.

Current text:

> This covers an unauthenticated request, including one made anonymous by an unavailable key set (§6.3.3), a request whose organization names no provisioned tenant under `trusted-headers` (§6.3.1), and every request on a registry with no request-time identity verifier.

Replacement:

> This covers an unauthenticated request, including one made anonymous by an unavailable key set (§6.3.3), and a request whose organization names no provisioned tenant under `trusted-headers` (§6.3.1).

The rest of the paragraph is unchanged.

## Proposed solution

### CODE-1. `internal/serverboot`: the config-time multi-tenant verifier guard

Targets: `internal/serverboot/identity_verify.go` and `internal/serverboot/serverboot.go` (`(*Config).validate`, about lines 2538-2562; the `Config.multiTenant` field comment, about lines 1990-1993).

In `identity_verify.go`, next to `identityVisibilityGuard`:

```go
// multiTenantVerifierGuard refuses a multi-tenant configuration whose identity
// provider resolves to no registered provider: an empty value, a free-form
// label such as "oidc" or "none", or public mode, which config.public_mode_with_idp
// already confines to those values. A multi-tenant registry selects each
// request's tenant from the verified identity, so without a verifier it would
// bind every request to the no-data tenant and route none. A registered
// provider without a verifier is left to identityVisibilityGuard, which
// refuses it in either mode, so the predicate matches selectIdentityProvider's
// nil case and cannot drift from it.
//
// Spec: §6.3.1, §13.10, §13.12
func multiTenantVerifierGuard(multiTenant, publicMode bool, identityProvider string) error {
	if !multiTenant || identity.Default.Has(identityProvider) {
		return nil
	}
	cause := fmt.Sprintf("PODIUM_IDENTITY_PROVIDER=%q names no identity provider the registry verifies at request time", identityProvider)
	remedy := "set PODIUM_IDENTITY_PROVIDER to one of " + strings.Join(verifiedProviders, ", ")
	if publicMode {
		cause = "public mode (PODIUM_PUBLIC_MODE) verifies no caller"
		remedy = "unset PODIUM_PUBLIC_MODE and " + remedy
	}
	return fmt.Errorf("config.multitenant_no_verifier: PODIUM_MULTI_TENANT=true selects each request's tenant from the verified caller identity (§6.3.1), and %s; %s, or unset PODIUM_MULTI_TENANT", cause, remedy)
}
```

In `(*Config).validate`, immediately after `if err := startup.Validate(); err != nil { return err }`:

```go
// §6.3.1: a multi-tenant registry routes by verified identity, so refuse a
// configuration with no verifier before any backend opens. It runs after
// StartupConfig.Validate so every refusal Validate returns keeps precedence.
if err := multiTenantVerifierGuard(c.multiTenant, c.publicMode, c.identityProvider); err != nil {
	return err
}
```

**IMPLEMENTOR'S CHOICE:** the wording of this comment — it must not name a precedence set that differs from the SPEC-1 text it cites.

Extend the `Config.multiTenant` field comment with: "validate refuses it without a request-time identity verifier (§6.3.1)." The guard follows the `fmt.Errorf("config.<code>: ...")` style of the neighbouring serverboot guards and adds no exported sentinel, because no caller branches on it.

### CODE-2. `internal/serverboot`: the `config.identity_provider_unverified` message

Target: `internal/serverboot/identity_verify.go` (`identityVisibilityGuard`, about lines 111-124).

Keep the signature `identityVisibilityGuard(identityProvider string, providerSelected, publicMode, verifierInstalled bool) error` and the call at `internal/serverboot/serverboot.go:1469`. Replace the returned error with this exact text, so the tests in TEST-1 and TEST-2 can assert it:

```go
return fmt.Errorf("config.identity_provider_unverified: PODIUM_IDENTITY_PROVIDER=%q has no request-time token verifier wired, so the registry would resolve every caller as anonymous-public and never apply per-layer visibility (§2.2, §6.3.1); the registry verifies %s server-side. Select one of those. To run an open single-tenant registry instead, unset PODIUM_IDENTITY_PROVIDER and set PODIUM_PUBLIC_MODE=true; a registry started with PODIUM_MULTI_TENANT requires a verified provider (§6.3.1)", identityProvider, strings.Join(verifiedProviders, ", "))
```

Append to the comment above the guard: "The remedy names the preconditions for public mode: no identity provider (config.public_mode_with_idp) and no PODIUM_MULTI_TENANT (config.multitenant_no_verifier). The previous message offered public mode alone, which led an operator who kept the provider into config.public_mode_with_idp."

### CODE-3. Remove the dead multi-tenant-without-router path

Targets: `internal/serverboot/serverboot.go` (about lines 1452-1467 and 1494-1503), `pkg/registry/server/server.go` (the `multiTenant` field comment, about lines 69-73; `WithMultiTenant`, about lines 232-242), `internal/serverboot/audit_emitter_test.go` (the rows at about lines 174-175), and `pkg/registry/server/audit_context_test.go` (the subtest at about lines 232-238 and `TestAuditMeta_PublicModeMultiTenantNoRouter`, about lines 247-285).

1. **serverboot router block.** Keep the `if cfg.multiTenant && verifierInstalled` block and its log line. Append to its comment: "validate refuses multi-tenant mode without a request-time verifier (config.multitenant_no_verifier), and identityVisibilityGuard refuses a registered provider without one before server.New, so every multi-tenant registry that serves requests installs this router. WithTenantRouter also selects the §8.1 audit tenant-label rule."
2. **serverboot `WithMultiTenant` block.** Delete the `if cfg.multiTenant { bootOpts = append(bootOpts, server.WithMultiTenant()) }` block and its comment.
3. **serverboot layer endpoint.** Keep `if cfg.multiTenant { layers = layers.WithTenantRouting() }`. Replace its comment with: "§7.3.1 Tenant selection: a multi-tenant endpoint serves the tenant §6.3.1 routes each request to, and refuses an unrouted write. validate refuses multi-tenant mode without a request-time verifier, so the tenant router installed above is present whenever this branch runs."
4. **`pkg/registry/server/server.go`.** Delete `WithMultiTenant` and its doc comment. Replace the `multiTenant` field comment with: "multiTenant records that the registry runs in §6.3.1 multi-tenant mode. WithTenantRouter sets it. withAuditMetaMiddleware is its only reader: it selects the §8.1 tenant-label rule, so an unrouted request records no tenant instead of the podium:unrouted binding."
5. **`internal/serverboot/audit_emitter_test.go`.** Replace the two rows `{"no router, public mode", ...}` and `{"no router, no identity provider", ...}` with one row:

   ```go
   {"anonymous under a router", layer.Identity{}, []server.Option{server.WithTenantRouter(router, false)}, "", "podium:unrouted"},
   ```

   The "unrouted under a router" row pins an organization with no tenant, which takes the tenant-lookup branch of `routeTenant` (`pkg/registry/server/server.go:506-509`). The new anonymous row pins an unauthenticated request, which takes the `!id.IsAuthenticated || id.OrgID == ""` branch (`pkg/registry/server/server.go:502-503`). Both stay reachable under `trusted-headers` and `oidc-jwt`.
6. **`pkg/registry/server/audit_context_test.go`.** Delete the subtest "multi-tenant with no router records no tenant" and `TestAuditMeta_PublicModeMultiTenantNoRouter`. The subtest "unrouted request under a router records no tenant" keeps the §8.1 rule pinned at the library level.

Run `go build ./...`, `go vet ./...`, and `make lint`.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| `PODIUM_MULTI_TENANT=true` with `PODIUM_IDENTITY_PROVIDER` unset or empty | Startup exits non-zero with `config.multitenant_no_verifier` before the store opens; the message names `injected-session-token`, `oidc-jwt`, and `trusted-headers` and contains `unset PODIUM_MULTI_TENANT` | §6.3.1 "fails to start with `config.multitenant_no_verifier` when it configures no identity provider" (SPEC-1); `docs/reference/cli.md`, `docs/reference/error-codes.md` (DOC-2), `docs/deployment/single-node.md` (DOC-1) |
| `PODIUM_MULTI_TENANT=true` with a label the registry does not register, such as `oidc`, `none`, or a misspelled `oidc_jwt` | Same refusal; the message quotes the configured value | §6.3.1 "when it names an identity-provider label the registry resolves to no configured provider (§4.6)" (SPEC-1); `docs/reference/cli.md` (DOC-2), `docs/deployment/single-node.md` (DOC-1) |
| `PODIUM_MULTI_TENANT=true` with `PODIUM_PUBLIC_MODE=true` and an empty provider or `none` | Same refusal; the message also contains `unset PODIUM_PUBLIC_MODE` | §6.3.1 "or when it is started in public mode (§13.10)" and "The value `none` configures no identity provider, so public mode with `PODIUM_IDENTITY_PROVIDER` unset, empty, or `none` passes the identity-provider exclusion and fails with `config.multitenant_no_verifier`." (SPEC-1); §13.10 "**Mutually exclusive with multi-tenant mode.**" (SPEC-2); `docs/deployment/single-node.md`, `docs/deployment/clustered.md` (DOC-1) |
| Multi-tenant and public mode with a provider label other than `none`, such as `oidc` or `oidc-jwt` | `config.public_mode_with_idp` | §6.3.1 "A configuration that another §13.10 configuration check refuses fails with that check's error." (SPEC-1); §13.10 "**Mutually exclusive with an identity provider.**" (unchanged); `docs/deployment/single-node.md` (unchanged) |
| Multi-tenant and public mode on a non-loopback bind without `--allow-public-bind` | `config.public_bind_refused` | §6.3.1 "A configuration that another §13.10 configuration check refuses fails with that check's error." (SPEC-1); §13.10 "**Loopback bind by default.**" (unchanged); `docs/deployment/single-node.md` (unchanged) |
| Multi-tenant with no identity provider and the web UI on a non-loopback bind, or with the browser flow enabled | `config.web_ui_public_bind_refused`, or `config.web_ui_auth_unconfigured` naming the failed conjunct (`pkg/registry/server/config_validate.go:143-149`, `:188-199`) | §6.3.1 "A configuration that another §13.10 configuration check refuses fails with that check's error." (SPEC-1); §13.10 *Behind a flag* and *Browser-flow configuration guard* (unchanged) |
| Multi-tenant `trusted-headers` without `PODIUM_TRUSTED_PROXY_SECRET` | `config.trusted_headers_multitenant_no_secret`, unchanged | §6.3.3 (unchanged); `docs/reference/error-codes.md` (unchanged) |
| Multi-tenant with `oauth-device-code` or a custom registered provider without a verifier | `config.identity_provider_unverified` after ingest, as in single-tenant mode; the message contains `unset PODIUM_IDENTITY_PROVIDER` and `PODIUM_MULTI_TENANT`. The refusal stays post-ingest, which this proposal accepts (Non-goals) | §4.6 "A deployment naming a provider the registry recognizes but does not verify server-side is refused at startup with `config.identity_provider_unverified`" (unchanged); §13.12 (unchanged); `docs/reference/error-codes.md`, `docs/getting-started/how-it-works.md` (unchanged); `docs/deployment/single-node.md` (DOC-1) |
| `podium-server sign-stored-rows` on a multi-tenant configuration with no verifier | Refuses with `config.multitenant_no_verifier` and writes nothing | §13.4 "It reads the configuration a registry start reads" (unchanged); `CHANGELOG.md` (DOC-3) |
| A single-tenant registry with no provider, a label, or public mode | Starts as before; `/v1/admin/tenants` answers `404 registry.tenant_management_unavailable` with the unchanged envelope | §6.10 envelope (unchanged, D-6); §6.3.1 single-tenant sentence (unchanged); `docs/reference/error-codes.md`, `docs/reference/http-api.md` (DOC-2) |
| A verified multi-tenant registry receives a request that resolves to no tenant: an anonymous request, an unprovisioned organization under `trusted-headers`, or an `oidc-jwt` token without `org_id` | Empty layer list; a layer write or an erase answers `403 auth.forbidden` with "request resolves to no tenant"; the §8.1 record carries no tenant; the request draws on the shared unrouted budget | §7.3.1 *Tenant selection* (unchanged); §8.5 erasure bullet (unchanged); §4.7.8 "This covers an unauthenticated request ... and a request whose organization names no provisioned tenant under `trusted-headers` (§6.3.1)." (SPEC-4); `docs/reference/http-api.md` (DOC-2), `docs/deployment/layers.md` (DOC-1), `docs/deployment/gateway-delegated-identity.md`, `docs/deployment/oidc/index.md` (unchanged) |
| An unrouted search on a verified multi-tenant Postgres registry | Answers 500, because the Postgres store refuses the `podium:unrouted` binding as a schema name. Accepted and deferred (Non-goals) | No spec text states the 500; §7.3.1 and §4.7.8 state the routing and the budget. No docs page states it |
| An `oidc-jwt` token without `org_id` on a multi-tenant registry | The request stays unrouted rather than being rejected; §4.7.8 does not name this case in its list. Accepted and deferred | §6.3.1 (unchanged); `docs/deployment/oidc/index.md:67` (unchanged) |
| A deployment that starts today with `PODIUM_MULTI_TENANT=true` and no verifier | Exits at startup after the upgrade; no setting restores the old behavior | §6.3.1 (SPEC-1); `CHANGELOG.md` (DOC-3) |

## Testing

**New and extended tests.**

- **TEST-1, `TestMultiTenantVerifierGuard`** in `internal/serverboot/identity_visibility_guard_test.go` (unit, `// Spec: §6.3.1 / §13.10 / §13.12 — a multi-tenant configuration whose provider resolves to no registered provider is refused.`). A table test:
  - Cases that fail with `config.multitenant_no_verifier`: multi-tenant with provider `""`, with `"none"`, with `"oidc"`, with `"oidc_jwt"`, with public mode and provider `""`, and with public mode and provider `"none"`.
  - Cases that pass: multi-tenant with each entry of `verifiedProviders`, and multi-tenant with `"oauth-device-code"`.
  - Also passing: multi-tenant with a custom provider registered under the unique id `acme-mt-guard` into `identity.Default`, guarded by `if !identity.Default.Has(id)`, following `internal/serverboot/identity_select_test.go:47-60`. The registry is mutex-guarded, so the case is safe under `t.Parallel`.
  - Non-multi-tenant cases that pass: `""`, `"oidc"`, and public mode.
  - For every failing case, assert that the message names each `verifiedProviders` entry and contains `unset PODIUM_MULTI_TENANT`. For both public-mode cases, also assert `unset PODIUM_PUBLIC_MODE`. For the `"oidc"` case, assert that the message contains `"oidc"` with its quotes.
- **TEST-1, `TestIdentityVisibilityGuard_MessageNamesEveryVerifiedProvider`** (unit, existing). Extend it to assert that the message contains `unset PODIUM_IDENTITY_PROVIDER`, `PODIUM_PUBLIC_MODE=true`, and `PODIUM_MULTI_TENANT`. Add `// Spec: §4.6 / §13.12 — the refusal names the preconditions for public mode.` The existing call sites and the existing `TestIdentityVisibilityGuard` table are unchanged.
- **TEST-1, `TestValidate_MultiTenantPrecedence`** in `internal/serverboot/webui_sign_config_test.go` (unit, `// Spec: §6.3.1 / §13.10 — earlier startup refusals keep precedence over config.multitenant_no_verifier.`). Every `Config` sets `storeType: "sqlite", objectStore: "filesystem"`. Assert the `pkg/registry/server` sentinels with `errors.Is`, and the new code with `strings.Contains`.
  - `{multiTenant, publicMode, identityProvider: "oidc-jwt", bind: "127.0.0.1:8080"}` gives `server.ErrPublicModeWithIdP`.
  - `{multiTenant, publicMode, identityProvider: "none", bind: "127.0.0.1:8080"}` gives `config.multitenant_no_verifier` and the message contains `unset PODIUM_PUBLIC_MODE`. This row pins the SPEC-1 sentence that `none` configures no identity provider: `config.public_mode_with_idp` exempts `none` (`pkg/registry/server/config_validate.go:126`), so the configuration reaches the new guard.
  - `{multiTenant, publicMode, bind: "0.0.0.0:8080"}` gives `server.ErrPublicBindNonLoopback`.
  - `{multiTenant, webUI, bind: "0.0.0.0:8080"}` gives `server.ErrWebUIPublicBindRefused`. This row pins the SPEC-1 general rule for a web UI check: the configuration has no identity provider, so it meets the new guard's predicate and must still fail with the web UI bind refusal (`pkg/registry/server/config_validate.go:143-149`).
  - `{multiTenant, identityProvider: "trusted-headers", bind: "127.0.0.1:8080"}` gives `server.ErrTrustedHeadersMultitenantNoSecret`. A comment marks it as a regression row: `trusted-headers` is a verified provider, so the new guard never fires for it, and the row pins that the existing refusal still fires.
  - `{multiTenant, bind: "127.0.0.1:8080"}` gives `config.multitenant_no_verifier`.
  - `{multiTenant, identityProvider: "oidc-jwt", bind: "127.0.0.1:8080"}` passes.
  - The test carries no `// Matrix:` line, consistent with D-3.
- **TEST-2, `TestGateway_MultiTenantNoVerifierRefused`** in `test/e2e/auth_gateway_test.go`, beside `TestGateway_TrustedHeadersMultitenantNoSecretRefused` (end-to-end, `// Spec: §6.3.1 / §13.10 / §13.12 — a registry started with PODIUM_MULTI_TENANT and no request-time identity verifier fails to start with config.multitenant_no_verifier.`). Four cases, each through `gwExpectStartupFailure(t, "config.multitenant_no_verifier", ...)` with `PODIUM_MULTI_TENANT=true`:
  - `PODIUM_IDENTITY_PROVIDER=` (set explicitly, see Watch out for).
  - `PODIUM_IDENTITY_PROVIDER=none`.
  - `PODIUM_IDENTITY_PROVIDER=oidc`.
  - `PODIUM_IDENTITY_PROVIDER=` with `PODIUM_PUBLIC_MODE=true`. The harness's default bind is loopback, so `config.public_bind_refused` does not fire first.
  - For each case, assert that the output names `injected-session-token`, `oidc-jwt`, and `trusted-headers` and contains `unset PODIUM_MULTI_TENANT`. In the public-mode case, also assert `unset PODIUM_PUBLIC_MODE`.
- **TEST-2, `TestSmallTeam_IdentityProviderUnverifiedFailsStartup`** in `test/e2e/standalone_server_test.go:303-311`, with its comment at `:297-302` (end-to-end, existing). Convert it into a table with two rows, each asserting `config.identity_provider_unverified` and that the output contains `unset PODIUM_IDENTITY_PROVIDER`, `PODIUM_PUBLIC_MODE=true`, and `PODIUM_MULTI_TENANT`:
  - single-tenant: `PODIUM_IDENTITY_PROVIDER=oauth-device-code`.
  - multi-tenant: the same, with `PODIUM_MULTI_TENANT=true`. This row pins that CODE-1 passes a registered provider through to the post-ingest guard, and that the boot reaches `config.identity_provider_unverified` rather than `config.multitenant_no_verifier`.
  - Rewrite the first line of its comment, `// spec: §2.2, §6.3.1 — a registry configured with` (`test/e2e/standalone_server_test.go:297`), in the alias form specparser parses: `// Spec: §4.6 / §6.3.1 / §13.12 — a registry that names a recognized provider without a request-time verifier fails to start with config.identity_provider_unverified in single-tenant and multi-tenant mode.` Keep the existing prose as continuation lines. The comma-separated form matches no `specLineRegex` (`tools/internal/specparser/specparser.go:403`), so `parseSpecLine` returns an empty `Citation` (`:415-418`) and the test credits no section today. Appending `, §13.12` to that line would leave the only end-to-end test of the multi-tenant `config.identity_provider_unverified` row uncited.
- **TEST-3, `TestLayerEndpoint_MultiTenantRouting`** in `test/e2e/layer_tenant_routing_test.go` (end-to-end, existing, standard stack). Delete `TestLayerEndpoint_MultiTenantNoRouter` and its comment block, including its IMPLEMENTOR'S CHOICE paragraph. Keep `ltrWantUnlabeledSearch`. Add no new boot.
  - After `ltrSetup`, assert that `f.srv.log()` contains `ltrRouterLog`, which keeps the constant in use and pins that a multi-tenant boot with a verified provider installs the router.
  - Replace the body of step 6 with a call to a new helper `ltrUnroutedRefusals(t, f, erinH)`, called before step 7. The helper does four things. (a) It keeps the `POST /v1/layers` refusal and asserts that the body `apiWantCodeAs` returns contains `request resolves to no tenant`, which attributes the 403 to §7.3.1 tenant selection. (b) It calls `apiWantCodeAs(t, f.srv, http.MethodPost, "/v1/admin/erase", erinH, ltrEraseBody("frank-"+f.suffix+"@acme.com"), http.StatusForbidden, "auth.forbidden", "erin erases")` and asserts the same message, which pins the §8.5 refusal at the serverboot tenant-routed erase mount. (c) It asserts that erin's list is still empty. (d) It issues one `apiDoAs(t, http.MethodGet, f.srv.BaseURL+"/v1/search_artifacts?query=ltr-erin-"+f.suffix, erinH, nil)` without asserting the status, because the Postgres 500 caveat carries over, and then calls `ltrWantUnlabeledSearch(t, f.auditPath)`. The test issues no other search, so the helper's check that every searched record is unlabeled holds.
  - Add to the test's annotation block: `// Spec: §8.5 — an unrouted erase is refused with 403 at the erase mount.` and `// Spec: §8.1 — an unrouted search records no tenant, and no record carries podium:unrouted.`
  - Do not add a request from carol routed to an unprovisioned organization, a `manage_any_layer` positive control, or a §7.3.1 annotation that claims precedence over an admin grant in another tenant. On a verified provider the admin check runs in the routed tenant (§8.5), so such a request cannot tell the two refusals apart. `TestLayerTenantRouting_UnroutedRefusal` with `alwaysAdmit` (`pkg/registry/server/layers_tenant_routing_test.go:1149-1170`) already pins the ordering in process.
- **CODE-3, `internal/serverboot/audit_emitter_test.go`** (unit, existing). The anonymous-under-a-router row replaces the two no-router rows, as CODE-3 item 5 states.

**Removed tests.** `TestLayerEndpoint_MultiTenantNoRouter`, the `audit_context_test.go` subtest "multi-tenant with no router records no tenant", and `TestAuditMeta_PublicModeMultiTenantNoRouter`. Each builds a combination that no booted registry reaches after CODE-1. The sections they cite (§6.3.1, §7.3.1, §7.3.4, §8.1, §8.5) keep other citing tests, so `speccov-drift` loses no section.

**Coverage.** Run `go test -coverpkg=./... -coverprofile=cover.out ./internal/serverboot/ ./pkg/registry/server/` and confirm that `multiTenantVerifierGuard` and the rewritten `identityVisibilityGuard` reach 85% with `go tool cover -func=cover.out`. Both guards are pure, so the unit tests reach every branch. Run `GOCOVERDIR=$(mktemp -d) go test -v ./test/e2e/ -run 'MultiTenantNoVerifier|IdentityProviderUnverified|MultiTenantRouting'` for the subprocess path, and grep the output for `SKIP` before reporting a pass. The routing test needs the standard stack.

## Manual validation

**MV-1.** Amend scenario S90 in `test/manual-validation.md` (about lines 11200-11329). Add no new scenario. S90 already starts a multi-tenant `trusted-headers` registry in its step 1 and a single-tenant registry in its step 6, so the refusal reads beside the working multi-tenant boot. Steps 1 to 6 are unchanged.

1. **Goal.** Replace the final sentence ending "...and that a single-tenant standalone registry refuses tenant management with a suggested action that names no backend." with:

   ```markdown
   **Goal.** Validate that a standalone registry on the SQLite store, started with
   `PODIUM_MULTI_TENANT` behind `trusted-headers`, provisions and lists tenants
   through `/v1/admin/tenants`, routes each caller to the tenant its organization
   names, including a tenant created through the API, refuses a layer write from
   an organization that names no tenant, that a single-tenant standalone
   registry refuses tenant management with a suggested action that names no
   backend, and that a registry started with `PODIUM_MULTI_TENANT` and no
   request-time identity verifier fails to start with
   `config.multitenant_no_verifier`.
   ```

2. **Covers.** Replace the paragraph with:

   ```markdown
   **Covers.** §13.10 Features enabled by configuration, §6.3.1 per-request tenant
   selection and the multi-tenant verifier requirement, §7.3.1 Tenant selection,
   §7.3.3 Tenant Management, §13.12 `PODIUM_MULTI_TENANT`, the §6.10
   `registry.tenant_management_unavailable` envelope, and the
   `config.multitenant_no_verifier` startup refusal, through the compiled binary.
   ```

3. **Why by hand.** Append this sentence to the paragraph: "A multi-tenant server that starts without a verifier, or a refusal that offers `PODIUM_PUBLIC_MODE=true` without the conditions it requires, is the defect step 7 catches."

4. **Step 7.** Insert after step 6 and before **Cleanup**:

   ````markdown
   7. **Negative control: a multi-tenant registry with no request-time verifier.**
      Stop the single-tenant server, then start the registry with
      `PODIUM_MULTI_TENANT=true` under each configuration that has no
      request-time verifier, in the foreground, and print each exit status and
      the last lines of its log.

      ```bash
      kill "$SRV"; wait "$SRV" 2>/dev/null
      refuse() {
        env "$@" PODIUM_MULTI_TENANT=true podium serve --standalone --no-embeddings --bind 127.0.0.1:8193 > "$WORK/refuse.log" 2>&1
        echo "[$*] exit=$?"; tail -2 "$WORK/refuse.log"
      }
      refuse PODIUM_IDENTITY_PROVIDER=
      refuse PODIUM_IDENTITY_PROVIDER=oidc
      refuse PODIUM_IDENTITY_PROVIDER= PODIUM_PUBLIC_MODE=true
      refuse PODIUM_IDENTITY_PROVIDER=oauth-device-code
      ```

      Each run passes its variables to `refuse` as separate words and the
      function forwards them with `"$@"`, so the block relies on no word
      splitting and behaves the same under bash and zsh.
      `PODIUM_TRUSTED_PROXY_SECRET` from step 1 stays exported and plays no
      part, because no run selects `trusted-headers`. A run that does not return
      within a few seconds has started a server, which is the defect. Stop it with
      Ctrl-C and record the failure.

      **Expect.** The first three runs exit non-zero with
      `config.multitenant_no_verifier`. Each message names
      `injected-session-token`, `oidc-jwt`, and `trusted-headers`, and each
      contains `unset PODIUM_MULTI_TENANT`. The `oidc` run quotes `"oidc"`, and
      the public-mode run contains `unset PODIUM_PUBLIC_MODE`. The fourth run
      exits non-zero with `config.identity_provider_unverified`, and its message
      contains `unset PODIUM_IDENTITY_PROVIDER` and `PODIUM_MULTI_TENANT`. Any
      `exit=0` is the defect. A fourth-run message that offers
      `PODIUM_PUBLIC_MODE=true` without `unset PODIUM_IDENTITY_PROVIDER` is the
      defect.
   ````

5. **Cleanup.** Replace `kill "$SRV"` with `kill "$SRV" 2>/dev/null`, because step 7 has already stopped the server.

The surface a human reads is the exit status and the refusal line of each run. The scenario catches a registry that starts and serves an empty catalog, a refusal under the wrong code, and a remedy that leads into another refusal.

## Documentation changes

### DOC-1. Deployment pages

1. **`docs/deployment/single-node.md`, the paragraph that begins "A single-node deployment is single-tenant by default."** Replace the sentence "Without such a provider, or in public mode, a registry started with `PODIUM_MULTI_TENANT` binds every request to a tenant that holds no data, so every caller sees an empty catalog and `/v1/admin/tenants` answers `registry.tenant_management_unavailable`." with:

   > A registry started with `PODIUM_MULTI_TENANT` fails to start with `config.multitenant_no_verifier` when it configures no identity provider, names a provider value the registry does not recognize, or runs in public mode with no identity provider, unless another startup check refuses the configuration first. It fails with `config.identity_provider_unverified` when it names a recognized provider the registry does not verify, such as `oauth-device-code`.

2. **`docs/deployment/single-node.md`, the paragraph that begins "Public mode is mutually exclusive with an identity provider".** Insert after its first sentence:

   > It is also mutually exclusive with `PODIUM_MULTI_TENANT`, and setting both fails at startup. The registry reports `config.multitenant_no_verifier` when `PODIUM_IDENTITY_PROVIDER` is unset or `none`, unless another startup check refuses the configuration first.

3. **`docs/deployment/clustered.md`, the paragraph that begins "Run the registry in multi-tenant mode with `PODIUM_MULTI_TENANT=true`".** Leave the existing sentences unchanged. Insert after its first sentence:

   > A registry started with `PODIUM_MULTI_TENANT=true` fails to start with `config.multitenant_no_verifier` when it configures no identity provider, names a provider value the registry does not recognize, or runs in public mode with no identity provider, unless another startup check refuses the configuration first. On this chart, `config.publicMode` reaches that refusal only when `config.identityProvider.type` is empty or `none` and `config.allowPublicBind` is set: with any other type, including the default `oidc-jwt`, it fails with `config.public_mode_with_idp`, and with an empty or `none` type but without `config.allowPublicBind` it fails with `config.public_bind_refused`, because the chart renders a `0.0.0.0` bind.

4. **`docs/deployment/layers.md`, the paragraph that begins "`podium layer list` prints the registered layers the caller can see".** Replace "Any other caller whose organization selects no tenant sees no layers, including on a registry that authenticates no caller." with the sentence below, which mirrors DOC-2 item 4. A multi-tenant registry that authenticates no caller is one with no identity provider or in public mode, and CODE-1 refuses it at startup.

   > Any other caller whose organization selects no tenant sees no layers.

### DOC-2. Reference pages

1. **`docs/reference/error-codes.md`.** Insert a row directly after the `config.trusted_headers_multitenant_no_secret` row:

   > | `config.multitenant_no_verifier` | A registry started with `PODIUM_MULTI_TENANT` configures no identity provider, names a provider value the registry does not recognize, or runs in public mode with no identity provider, so it has no request-time identity verifier to select each request's tenant. The message names the providers the registry verifies: `injected-session-token`, `oidc-jwt`, and `trusted-headers`. A configuration that `config.public_mode_with_idp`, `config.public_bind_refused`, `config.web_ui_public_bind_refused`, or `config.web_ui_auth_unconfigured` also refuses fails with that code instead. |

   This row is the only doc site that names the ordering. Every other staged doc sentence that assigns `config.multitenant_no_verifier` carries the qualifier "unless another startup check refuses the configuration first".

   **IMPLEMENTOR'S CHOICE:** whether a doc sentence that assigns `config.multitenant_no_verifier` also links this row — no doc sentence may assign `config.multitenant_no_verifier` without that qualifier or a link to this row.

2. **`docs/reference/error-codes.md`, the `registry.tenant_management_unavailable` row.** Replace its description cell with:

   > A `/v1/admin/tenants` request (or a `podium admin tenant` command) reached a registry that serves no tenant management, which is a single-tenant registry started without `PODIUM_MULTI_TENANT`. Start the registry in multi-tenant mode with `PODIUM_MULTI_TENANT` and an identity provider the registry verifies to manage tenants.

3. **`docs/reference/cli.md`, the `PODIUM_MULTI_TENANT` row.** Keep the first sentence ("When `true` and the registry runs an identity provider it verifies, the registry routes each request to the tenant its organization names, and the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints are available.") and the "When unset" sentence. Replace only the sentence "When `true` with no such provider, or in public mode, every request binds to a tenant that holds no data and tenant management is rejected with `registry.tenant_management_unavailable`." with:

   > When `true` with no provider, a provider value the registry does not recognize, or public mode with no provider, the registry fails to start with `config.multitenant_no_verifier` unless another startup check refuses the configuration first; the providers the registry verifies are `injected-session-token`, `oidc-jwt`, and `trusted-headers`.

4. **`docs/reference/http-api.md`, the paragraph that begins "**Tenant selection.** On a multi-tenant registry, each layer endpoint acts".** Replace "Any other request that resolves to no tenant lists no layers, and every layer write is refused with `403 auth.forbidden`, including on a registry started in public mode or with no identity provider configured." with:

   > Any other request that resolves to no tenant lists no layers, and every layer write is refused with `403 auth.forbidden`.

5. After DOC-1 and the edits above, grep `docs/` for `tenant that holds no data`, for `no identity provider the registry verifies, or in public mode`, and for `registry that authenticates no caller`, and fix any match that states the outcome of a multi-tenant registry with no identity provider or in public mode. A match that describes a single-tenant registry stays. Then grep `docs/` and `CHANGELOG.md` for `multitenant_no_verifier` and confirm that every sentence that assigns the code, other than the error-codes row, carries the qualifier or links that row.

### DOC-3. CHANGELOG entry

Add under `## [Unreleased]`, `### Changed`:

```markdown
- **A multi-tenant registry refuses to start without a request-time identity verifier** (§6.3.1, §13.10, §13.12): a registry started with `PODIUM_MULTI_TENANT=true` exits at startup with the new `config.multitenant_no_verifier` when `PODIUM_IDENTITY_PROVIDER` is unset, when it names a value the registry does not recognize as a provider such as `oidc` or `none`, or when `PODIUM_PUBLIC_MODE=true` is set with no identity provider, unless another startup check refuses the configuration first. The message names `injected-session-token`, `oidc-jwt`, and `trusted-headers`. Such a registry used to start, bind every request to a tenant that holds no data, and answer `/v1/admin/tenants` with `registry.tenant_management_unavailable`. `podium-server sign-stored-rows` refuses the same configuration. A multi-tenant registry that names a recognized provider without a verifier, such as `oauth-device-code`, still fails with `config.identity_provider_unverified`, and that message now states that public mode requires an unset `PODIUM_IDENTITY_PROVIDER` and a registry started without `PODIUM_MULTI_TENANT`. In the `pkg/registry/server` library API, `WithMultiTenant` is removed, and `WithTenantRouter` marks multi-tenant mode. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the old behavior.
```

## Open questions

- **Q-1. Reading of the relayed "F1: leave the spec as is".** Nothing in the repository says which finding F1 names. This proposal reads F1 as leaving the §6.10 `registry.tenant_management_unavailable` text unchanged (D-6), and it keeps the spec edits to the minimum the refusal requires. The boot refusal still needs spec edits in §6.3.1, §13.10, §13.12, and §4.7.8 (SPEC-1 to SPEC-4): the spec-driven rule forbids code with no spec basis, and §13.12 currently says without condition that the registry runs. If F1 instead means that this change must leave `spec/` untouched, the decided boot refusal cannot be implemented under the project rules, and the reviewer must choose between the refusal and an unchanged spec. Resolved at sign-off: "F1: leave the spec as is" referred to the unrelated break-glass approver item. The staged spec edits (SPEC-1 to SPEC-4) stand.

## Non-goals

- Changing the §6.10 `registry.tenant_management_unavailable` envelope text, its code mirror (`pkg/registry/server/tenants.go:107`, `pkg/registry/server/error_envelope.go:122-124`), §7.3.3, or §4.7.1. D-6 shows they become accurate as written.
- Editing §7.3.1 *Tenant selection* to drop the precedence over the no-identity admin bypass (a dropped SPEC-5). The clause is a conditional rule that stays true when its condition never occurs, and it contradicts no other section. It is the only spec sentence that orders the unrouted refusal ahead of an admitting admin callback, which the library still implements: `requireTenant` runs before the admin callback (`pkg/registry/server/layers.go:250-262`), and library tests pin that order with `alwaysAdmit` (`pkg/registry/server/layers_tenant_routing_test.go:729`, `:1204-1222`). The edit was also incomplete: the sibling qualifier in the §7.3.1 visibility text, from the same proposal 0047 edit, and the `requireTenant` comment would stay. The relayed "F1: leave the spec as is" favours no spec edit beyond what the refusal requires. A later cleanup that removes the combination would cover the clause, its sibling qualifier, and the `requireTenant` comment together, and would keep a precedence over the admin check in general.
- Adding a sentence to §4.6 *No-identity bypass* that limits the bypass to single-tenant registries. §6.3.1 (SPEC-1) states the refusal and §13.10 (SPEC-2) states the public-mode exclusion. The §4.6 paragraph describes running registries, and its framing ("The condition is the absent verifier rather than the topology") would conflict with a tenancy condition.
- Adding "and with `PODIUM_MULTI_TENANT`" to the §13.12 public-mode paragraph. The `PODIUM_MULTI_TENANT` row (SPEC-3) and the §13.10 bullet (SPEC-2) state the exclusion.
- Moving the `config.identity_provider_unverified` refusal from its post-ingest call site (`internal/serverboot/serverboot.go:1469`) to configuration time, or changing its scope or code.
- Adding a §6.10 matrix cell (`tools/matrix/matrices.go:84-127`) or a §6.10 entry for `config.multitenant_no_verifier` (D-3).
- Adding `injected-session-token` to the organization sources §6.3.1 lists, although the boot installs the router for it (`internal/serverboot/serverboot.go:1392`, `:1452`). That is a separate pre-existing omission.
- Adding an `oidc-jwt` token without `org_id` to the §4.7.8 list of unrouted requests. That is a separate pre-existing omission.
- Removing the `Server.multiTenant` field or replacing it with `tenantRouter != nil`. CODE-3 removes only the option and the boot block that duplicate `WithTenantRouter`.
- Changing how a verified multi-tenant registry handles an unrouted request: the `trusted-headers` empty view, the `oidc-jwt` `auth.tenant_unknown`, and the Postgres 500 on an unrouted search.
- A compatibility flag, a warning-only mode, or a migration path for registries that start today with `PODIUM_MULTI_TENANT` and no verifier (D-10).
- Editing proposal 0056, a historical record. This proposal resolves its Q-2.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- The revised CODE-2 replaces a mode-conditional message and a fifth `bool` parameter with one rewritten message. The draft's TEST-1 item (b), which updated the guard's call sites and added an "oauth-device-code multi-tenant" case asserting the absence of `PODIUM_PUBLIC_MODE=true`, conflicted with it. The CODE-2 revision governs: TEST-1 extends `TestIdentityVisibilityGuard_MessageNamesEveryVerifiedProvider` instead, the TEST-2 table asserts the same substrings in both rows, and MV-1 step 7 and the DOC-3 sentence follow. D-5 is rewritten to match.
- SPEC-4 is reduced to its §4.7.8 edit, and SPEC-5 is dropped. D-7, the Summary, and Q-1 no longer list §4.6 or §7.3.1, and the Non-goals record both.
- The revised SPEC-3 left an optional edit to the §13.12 public-mode paragraph. It is decided against and recorded in the Non-goals, because an optional item would leave the spec commit undetermined.
- The revised DOC-1 sentence for `docs/deployment/clustered.md` said a registry with "no identity provider it verifies" fails with `config.multitenant_no_verifier`. That wording repeats the imprecision the DOC-1 review found in the single-node sentence, because `oauth-device-code` is unverified and fails with `config.identity_provider_unverified`. The clustered sentence now names the three causes D-1 covers.
- The revised TEST-3 deletes `TestLayerEndpoint_MultiTenantNoRouter`, the only reader of `ltrRouterLog`. TEST-3 now asserts the router line on the routing test's boot, which keeps the constant in use for the `unused` linter and pins D-1's implication that a verified multi-tenant boot installs the router.
- The revised MV-1 runs each refused configuration in the foreground. Step 7 now tells the operator that a run that does not return has started a server, which is the defect.
- D-9 described a replacement test with a new boot and a bootstrap-admin control. The revised TEST-3 extends the existing step 6 with no new boot and no control, so D-9 is rewritten to match.
- D-4 named three earlier refusals. The guard runs after every `StartupConfig.Validate` refusal, so D-4 now says so and names the three as examples.

### Pass 1 (2026-11-09, automated)

- SPEC-2 Edit 1 and the SPEC-3 sentence assigned `config.multitenant_no_verifier` to every multi-tenant registry without a request-time verifier, which includes `oauth-device-code` and a custom registered provider that D-1, the edge-case table, TEST-2, and MV-1 send to `config.identity_provider_unverified` (`internal/serverboot/identity_verify.go:109`, `:119-124`; `spec/13-deployment.md:505`; `spec/04-artifact-model.md:617`). Both sentences now name the SPEC-1 causes for `config.multitenant_no_verifier` and name `config.identity_provider_unverified` (§4.6) for a provider the registry recognizes but does not verify. The SPEC-3 Rationale now says why the row names the causes of each code.
- SPEC-1 gave `config.public_mode_with_idp` precedence over any identity-provider label, which includes `none`, while `StartupConfig.Validate` exempts `none` (`pkg/registry/server/config_validate.go:126`) and the edge-case table sends public mode with `none` to `config.multitenant_no_verifier`. SPEC-1 now limits that precedence to a label other than `none` and states that `none` configures no identity provider, matching both `none` exemptions in the code (`:126`, `:144`). The SPEC-1 Rationale records the reason, the edge-case rows quote the new text, and TEST-1 gains a public mode with `none` case in `TestMultiTenantVerifierGuard` and a `TestValidate_MultiTenantPrecedence` row that asserts `config.multitenant_no_verifier`.
- MV-1 step 7 relied on word splitting of an unquoted `$env`, which zsh does not perform, so the public-mode run set no `PODIUM_PUBLIC_MODE` under zsh (`test/manual-validation.md:33-36`; `internal/serverboot/serverboot.go:2290`, `:2307`). Step 7 now defines a `refuse` function that forwards its arguments with `"$@"` and calls it once per configuration, and a sentence after the block states why.
- SPEC-1 named one precedence overlap, and the SPEC-2 public-mode bullet stated `config.multitenant_no_verifier` without condition, while the guard yields to every `StartupConfig.Validate` refusal (D-4; `internal/serverboot/serverboot.go:2560-2562`). SPEC-1 now states that the §13.10 startup checks run first and names the overlaps: public mode with a label other than `none`, the public-mode and web UI bind refusals, and the browser-flow guard (`pkg/registry/server/config_validate.go:126`, `:133`, `:143-149`, `:175`). The SPEC-1 Rationale drops the "one overlap" claim, the SPEC-2 bullet defers to §6.3.1 for the order, the `config.public_bind_refused` edge-case row cites SPEC-1, and a new edge-case row covers the web UI and browser-flow overlaps.
- The DOC-1 clustered sentence said that `config.publicMode` leads to `config.multitenant_no_verifier`, but the chart renders `PODIUM_IDENTITY_PROVIDER` from `config.identityProvider.type` (default `oidc-jwt`) and a `0.0.0.0` bind (`deploy/helm/podium/values.yaml:68-69`, `:81-89`; `deploy/helm/podium/templates/_pod.tpl:140-141`, `:186-196`). The sentence now names public mode with no identity provider as the cause and states that `config.publicMode` reaches the refusal only with an empty or `none` type and `config.allowPublicBind`, failing with `config.public_mode_with_idp` or `config.public_bind_refused` otherwise.
- The SPEC-1 sentence on `none` said that public mode with `PODIUM_IDENTITY_PROVIDER` unset, empty, or `none` passes "those checks", which reads as every §13.10 startup check the previous sentence lists, including the public-mode bind refusal. `StartupConfig.Validate` exempts `none` only from the identity-provider exclusion (`pkg/registry/server/config_validate.go:126`) and refuses public mode on a non-loopback bind without `--allow-public-bind` whatever the provider is (`:133`), and the `TestValidate_MultiTenantPrecedence` row `{multiTenant, publicMode, bind: "0.0.0.0:8080"}` asserts `server.ErrPublicBindNonLoopback` (TEST-1). The sentence now states that such a configuration passes the identity-provider exclusion and, unless a bind or browser-flow check refuses it, fails with `config.multitenant_no_verifier`. The edge-case row for public mode with an empty provider or `none` quotes the corrected text.

### Pass 2 (2026-11-09, automated)

- SPEC-1 said the guard runs after "the §13.10 startup checks", which includes `config.layer_path_ambiguous` (`spec/13-deployment.md:168`). The registry raises that refusal during the bootstrap ingest, after `(*Config).validate` returns and the store opens (`internal/serverboot/serverboot.go:817`, `:870`, `:1098`; `pkg/registry/filesystem/registry.go:222`), and `config.invalid_sign_mode` runs in `(*Config).validate` after the guard's position (`internal/serverboot/serverboot.go:2560-2569`). The SPEC-1 sentence now names the §13.10 configuration checks for public mode, the bind address, and the browser flow, which `StartupConfig.Validate` holds (`pkg/registry/server/config_validate.go:126`, `:133`, `:143-149`, `:175`). The SPEC-1 Rationale states that the text claims no precedence over the later refusals. The SPEC-2 bullet already says "an earlier startup check" and is unchanged.
- SPEC-2 Edit 1 and the SPEC-3 sentence assigned `config.multitenant_no_verifier` to every multi-tenant public-mode start, which contradicted SPEC-1, the edge-case table, TEST-1, and the unchanged §13.12 public-mode paragraph (`spec/13-deployment.md:525`; `pkg/registry/server/config_validate.go:126`, `:133`). Both sentences now read "runs in public mode with no identity provider fails to start with `config.multitenant_no_verifier` unless an earlier startup check refuses the configuration first (§6.3.1)", and the SPEC-3 Rationale records why. The same qualifier lands in DOC-1 item 1, the DOC-2 `cli.md` sentence, and the DOC-3 CHANGELOG bullet. DOC-1 item 2 now states that the registry reports `config.multitenant_no_verifier` when `PODIUM_IDENTITY_PROVIDER` is unset or `none` and no other public-mode check refuses the configuration first, which agrees with `docs/deployment/single-node.md:171`.
- The `podium layer list` paragraph of `docs/deployment/layers.md:131` repeated the clause DOC-2 item 4 removes from `docs/reference/http-api.md:500`, and the DOC-2 item 5 grep strings missed it. DOC-1 gains item 4, which drops "including on a registry that authenticates no caller", and checklist step S9 and the verified-unrouted edge-case row name the page. DOC-2 item 5 adds `registry that authenticates no caller` to its grep strings, scopes the fix to matches about a multi-tenant registry, and no longer claims that no other site exists.
- Correction to this pass: DOC-1 item 2 limited the precedence to "public-mode" checks, so it assigned `config.multitenant_no_verifier` to two configurations that SPEC-1 and the web UI edge-case row assign to a web UI refusal. The web UI non-loopback bind refusal and the browser-flow guard are not keyed on public mode (`pkg/registry/server/config_validate.go:143-149`, `:175`). DOC-1 item 2 now reads "no earlier startup check, such as the loopback-bind default or a web UI bind or browser-flow check, refuses the configuration first", which agrees with the qualifier in SPEC-2 Edit 1, SPEC-3, and SPEC-1. The preceding bullet's description of DOC-1 item 2 records the wording this correction replaces.

### Pass 3 (2026-11-09, automated)

- DOC-1 item 1 and DOC-2 item 3 assigned `config.multitenant_no_verifier` to every multi-tenant configuration with no provider, an unrecognized provider value, or public mode with no provider, without the precedence qualifier that SPEC-1, SPEC-2 Edit 1, SPEC-3, and DOC-1 item 2 carry. `StartupConfig.Validate` refuses public mode on a non-loopback bind without `--allow-public-bind` with `config.public_bind_refused` and the web UI on a non-loopback bind without its opt-in and an identity provider with `config.web_ui_public_bind_refused` before the guard runs (`pkg/registry/server/config_validate.go:133`, `:143-149`; D-4). Both staged sentences, and the DOC-3 CHANGELOG bullet, now add "unless an earlier startup check, such as the loopback-bind default or a web UI bind or browser-flow check, refuses the configuration first", the wording of DOC-1 item 2. DOC-1 item 1 states the `config.identity_provider_unverified` case as its own sentence.
- TEST-2 told the implementor to add §13.12 to the spec comment of `TestSmallTeam_IdentityProviderUnverifiedFailsStartup`, which reads `// spec: §2.2, §6.3.1 — ...` (`test/e2e/standalone_server_test.go:297`). That comma-separated line matches no `specLineRegex` and credits no section (`tools/internal/specparser/specparser.go:403`, `:415-418`), so the addition would leave the test uncited. TEST-2 now rewrites the first line as `// Spec: §4.6 / §6.3.1 / §13.12 — ...` and keeps the existing prose as continuation lines, and the *Watch out for* bullet on comma-separated annotations names this comment.

### Pruning pass (2026-11-09)

- The startup-check precedence was restated at more than ten sites, and D-4, the CODE-1 comment, and the *Watch out for* bullet named `config.trusted_headers_multitenant_no_secret` as an overlap while the SPEC-1 Rationale said it never meets the guard. SPEC-1 now states the order once as a general rule, "A configuration that another §13.10 configuration check refuses fails with that check's error.", and its `none` sentence drops the "unless a bind or browser-flow check refuses it" tail, which the rule covers. The SPEC-1 Rationale drops the line-anchored list of overlaps and keeps the reasons the rule claims no precedence over `config.layer_path_ambiguous` or `config.invalid_sign_mode`. SPEC-2 Edit 2 now carries the qualifier SPEC-2 Edit 1 and SPEC-3 use. DOC-1 items 1 to 3, DOC-2 item 3, and DOC-3 carry one identical qualifier, "unless another startup check refuses the configuration first", and the DOC-2 error-codes row is the single doc site that names the overlapping codes. D-4, the CODE-1 comment, and the *Watch out for* bullet name every `StartupConfig.Validate` refusal (§6.3.1) with no list. The edge-case rows quote the general rule. Three IMPLEMENTOR'S CHOICE markers record what stays open: the unenumerated overlap set (the guard runs after every `StartupConfig.Validate` check), the CODE-1 comment wording (no precedence set that differs from SPEC-1), and whether a doc sentence links the error-codes row (no sentence assigns the code without the qualifier or that link). DOC-2 item 5 gains a grep that checks the last constraint. `TestValidate_MultiTenantPrecedence` gains a `{multiTenant, webUI, bind: "0.0.0.0:8080"}` row asserting `server.ErrWebUIPublicBindRefused`, so the web UI precedence the general rule asserts has a test. The Pass 2 bullet no longer claims that its qualifier landed in the Summary or the DOC-2 error-codes row, because neither carried it.
