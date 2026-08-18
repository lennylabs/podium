---
title: Progressive adoption
nav_order: 7
description: A staged on-ramp for adopting governance features (identity, sensitivity labels, signing, freeze windows) without forcing the whole feature set on day one.
---

# Progressive adoption

Podium ships with the full governance feature set: per-layer visibility, sensitivity labels, sandbox profiles, signing, hash-chained audit, freeze windows, and SCIM. Turning all of it on at once usually delays adoption.

This guide is a staged on-ramp for governance. It assumes a starting point of a permissive [single-node](single-node) deployment, or an equally permissive [clustered](clustered) one, and it tightens as the catalog and team grow into needing each control. Skip ahead when a particular feature is already required by an external constraint such as compliance, a security review, or a contractual obligation. The order below works for most teams, and other orderings are also valid.

---

## Day 0: install, public catalog, no auth

Goal: get artifacts flowing without governance gates.

- `podium serve --standalone` on a single VM, or `podium serve --strict` against Postgres and object storage when those already exist.
- One layer named `team-shared`, with `visibility: public` (the default when no identity provider is configured) and a `git` source pointing at one shared repo.
- No `PODIUM_VERIFY_SIGNATURES` setting on the registry, which does not read it. Signature verification runs in each consumer's MCP server, which resolves its policy from `PODIUM_VERIFY_SIGNATURES`, then `defaults.verify_signatures` in `sync.yaml`, then a `medium-and-above` fallback. A zero-flag or `--standalone` server writes `defaults.verify_signatures: never` into `~/.podium/sync.yaml` on the machine it runs on, and it writes nothing under `--strict` or against Postgres, so a consumer on another machine keeps the `medium-and-above` fallback. Every artifact is `sensitivity: low` at this stage, so no policy triggers a check.
- No sensitivity labels required; `sensitivity:` is optional and defaults to `low`.
- No SCIM, no freeze windows.

**Exit criteria:** several people have authored a skill, merged it, and seen it load in their harness. Artifacts are in active use, and the tooling is not blocking the authoring loop.

**Defer:** layer hierarchies, group-based visibility, and naming conventions.

---

## Week 4: add identity (no enforcement yet)

Goal: get OAuth identity working so audit and visibility have an identity subject. Enforcement remains permissive.

- Stand up an OIDC IdP, or hook into an existing one. Okta, Entra ID, and Keycloak issue a token the registry's `oidc-jwt` verifier accepts. Auth0 and Google Workspace do not, and route callers through a gateway under `trusted-headers` instead. The [OIDC cookbooks](oidc/) have per-IdP setup steps and cover both paths.
- Configure `PODIUM_IDENTITY_PROVIDER=oidc-jwt` on the registry, with `PODIUM_OAUTH_ISSUER` set to the IdP issuer and `PODIUM_OAUTH_AUDIENCE` set to the registry endpoint. The registry then verifies each presented token against the issuer's JWKS. This works on either tier; moving to the clustered tier at the same time is optional. See [Access control](access-control).
- Have each developer run `podium login` once. The CLI completes the device-code flow against the same IdP and caches the resulting token in the OS keychain. On the gateway path the gateway authenticates the caller, so this step does not apply.
- Existing `team-shared` layer keeps `visibility: public` for now; every authenticated user can still see everything.
- A user-defined layer per author, for example `alice-personal`. A user-defined layer carries implicit `users: [<registrant>]` visibility.

**Exit criteria:** every `load_artifact` and `search_artifacts` call in the audit log carries a `sub` claim. Anonymous calls are gone. Personal layers exist for in-progress work.

**Why now:** identity is a prerequisite for everything that follows. Without it, audit entries are anonymous, sensitivity has no enforcement target, and per-layer visibility has nothing to filter on.

**Defer:** changes to `team-shared` visibility, including `organization: true` and group-based scopes. Confirm that OIDC `sub` and `groups` claims arrive correctly first.

---

## Week 8: narrow `team-shared` to organization-only

Goal: stop public visibility after identity works.

- Change `team-shared` layer visibility to `organization: true`. Authenticated users from the organization see it; other callers do not.
- If multiple OIDC groups exist (engineering, sales, support, etc.) and some artifacts are team-specific, introduce group-based visibility on a second layer, for example `engineering-internal` with `groups: [engineering]`.
- Audit a week of `visibility.denied` events to confirm callers are not blocked from artifacts they should see.

**Exit criteria:** an unauthenticated caller sees an empty catalog, because no layer is public any more. Under `oidc-jwt` a request carrying no token is anonymous rather than rejected, so it resolves to public visibility only. A user from a different OIDC org cannot see the artifacts. Group-scoped layers, if any, work as expected.

---

## Month 2: sensitivity labels (advisory)

Goal: surface the existing risk profile of artifacts. No enforcement yet.

- Update lint rules to require `sensitivity:` in the frontmatter. Default is still `low`; the lint check is a warning at this stage and does not fail ingest.
- Authors annotate existing artifacts as part of their normal review cycle. Labels available: `low` (default), `medium`, `high`.
- Run `podium sync --preview` to print the aggregate scope preview, which breaks the caller's effective view down by sensitivity. Use the counts to size the review backlog. There is no sensitivity filter on `podium search`.
- The audit log now records sensitivity per `load_artifact` call: useful signal for later.

**Exit criteria:** every artifact in the catalog has an explicit `sensitivity:` field. Authors know roughly what fraction of the catalog is `medium` or `high`.

**Reason for advisory mode:** the lint warning is a nudge for authors to think about sensitivity without breaking ingest. After the catalog is fully labeled, enforcement can be enabled without breaking author flow.

---

## Month 3: enforce signing for `sensitivity: high`

Goal: integrity guarantees on artifacts where integrity matters.

- Set `PODIUM_VERIFY_SIGNATURES=medium-and-above` in each MCP server's environment, or set `defaults.verify_signatures: medium-and-above` in each consumer's `sync.yaml`. The registry does not read this variable. Loading an unsigned `sensitivity: high` or `sensitivity: medium` artifact through the MCP server then fails with `materialize.signature_invalid`. `podium sync` runs no signature check, so a workspace materialized that way is not covered by this control.
- Roll signing into the author flow: each `high` artifact gets signed at PR-merge time (Sigstore-keyless via OIDC, or a tenant signing key managed by the registry).
- Promote the lint check from warning to error: missing `sensitivity:` is now an ingest failure.

**Exit criteria:** an unsigned high-sensitivity artifact cannot be loaded. The CI signing job is reliable. The signing flow is part of normal authoring.

**Defer:** signing for `medium` unless a specific requirement exists. Most teams find `medium` sensitivity is the bulk of their useful catalog, and mandatory signatures slow authoring.

---

## Month 6: freeze windows for production-impacting changes

Goal: protect critical periods (release cuts, year-end close, on-call rotations) from in-flight artifact changes.

- Configure freeze windows in `registry.yaml` under `registry.freeze_windows:`. Each entry carries a `name`, an absolute RFC 3339 `start` and `end`, and the operations it blocks. There is no recurrence field, so a repeating window such as a weekend freeze is written as one entry per occurrence.
- Train the team on the break-glass protocol: dual-signoff + justification, auto-expires after 24h, queues for post-hoc review.
- Run a dry-run freeze for one window before enforcing it, and identify workflows that need an exception.

**Exit criteria:** freeze windows are scheduled and known to the team. The break-glass procedure has been used at least once in a controlled fashion. The audit log shows the expected pattern.

**Defer:** daily freeze windows and freeze windows for non-production layers. Reserve freeze windows for periods where ingest would create operational risk.

---

## Month 9+: graduate the rest as needed

By this point governance overhead is amortized; the further controls are easier to add when their specific need shows up:

- **Sandbox profile enforcement** (`PODIUM_ENFORCE_SANDBOX_PROFILE=true`) when artifacts ship code that runs on user machines and the harness honors profiles. Until then, the field is informational.
- **Transparency-log anchoring** when external auditors or regulators ask whether an artifact existed at time T. The hash-chained audit log already provides internal evidence; transparency-log anchoring extends it across organizational boundaries.
- **Multi-region replication** when single-region availability stops being acceptable.

Each of these warrants a planned rollout: read the relevant spec section, run a controlled trial, then enable broadly.

---

## Alternate ordering

Common reorderings:

- **Compliance-driven.** If SOC2, ISO 27001, or a customer contract requires signed-and-audited artifacts before launch, jump straight from Day 0 to Month 3's signing posture. The intermediate steps ease rollout for teams without external pressure; they are not required for correctness.
- **Multi-tenant from the start.** A deployment that serves separate customer organizations requires multi-tenancy and OIDC from the start. Skip the single-node phase and start on the [clustered](clustered) tier with a per-tenant layer plan.
- **High-sensitivity domain only.** If the catalog contains only `sensitivity: high` content (security playbooks, compliance runbooks), enable signing on day 1 alongside identity. Skip the advisory-sensitivity phase.

The order in this guide moves from lower operational friction to more control. Choose the starting point based on current requirements, then move forward as requirements change.
