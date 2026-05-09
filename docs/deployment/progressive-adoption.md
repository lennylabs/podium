---
layout: default
title: Progressive adoption
parent: Deployment
nav_order: 4
description: A staged on-ramp for adopting governance features (identity, sensitivity labels, signing, freeze windows) without forcing the whole feature set on day one.
---

# Progressive adoption

Podium ships with the full governance feature set: per-layer visibility, sensitivity labels, sandbox profiles, signing, hash-chained audit, freeze windows, and SCIM. Turning all of it on at once usually delays adoption.

This guide is a staged on-ramp for governance. It assumes a starting point of `podium serve --standalone` (or an equivalent permissive standard deployment) and progressively tightens as the catalog and team grow into needing each control. Skip ahead if a particular feature is already required by an external constraint (compliance, security review, contractual obligation). The order below works for most teams; other orderings are also valid.

---

## Day 0: install, public catalog, no auth

Goal: get artifacts flowing without governance gates.

- `podium serve --standalone` on a single VM (or `podium serve --strict` with a basic standard deployment if you already have Postgres + S3).
- One layer (`team-shared`), `visibility: public` (the standalone default), `git`-source pointing at one shared repo.
- No `PODIUM_VERIFY_SIGNATURES` setting (defaults to `never` in standalone, off entirely).
- No sensitivity labels required; `sensitivity:` is optional and defaults to `low`.
- No SCIM, no freeze windows.

**Exit criteria:** several people have authored a skill, merged it, and seen it load in their harness. Artifacts are in active use, and the tooling is not blocking the authoring loop.

**Defer:** layer hierarchies, group-based visibility, and naming conventions.

---

## Week 4: add identity (no enforcement yet)

Goal: get OAuth identity working so audit and visibility have an identity subject. Enforcement remains permissive.

- Stand up an OIDC IdP (or hook into your existing one: Okta, Entra ID, Google Workspace, Auth0, Keycloak). The [OIDC cookbooks](oidc/) have per-IdP setup steps.
- Switch from standalone (no auth) to standard deployment (or run standalone with auth via `PODIUM_IDENTITY_PROVIDER=oauth-device-code`).
- Existing `team-shared` layer keeps `visibility: public` for now; every authenticated user can still see everything.
- New `<person>-personal` user-defined layers per author, default `users: [<self>]` visibility (the standard default).

**Exit criteria:** every `load_artifact` and `search_artifacts` call in the audit log carries a `sub` claim. Anonymous calls are gone. Personal layers exist for in-progress work.

**Why now:** identity is a prerequisite for everything that follows. Without it, audit entries are anonymous, sensitivity has no enforcement target, and per-layer visibility has nothing to filter on.

**Defer:** changes to `team-shared` visibility, including `organization: true` and group-based scopes. Confirm that OIDC `sub` and `groups` claims arrive correctly first.

---

## Week 8: narrow `team-shared` to organization-only

Goal: stop public visibility after identity works.

- Change `team-shared` layer visibility to `organization: true`. Authenticated users from the organization see it; other callers do not.
- If multiple OIDC groups exist (engineering, sales, support, etc.) and some artifacts are team-specific, introduce group-based visibility on a second layer, for example `engineering-internal` with `groups: [engineering]`.
- Audit a week of `visibility.denied` events to confirm callers are not blocked from artifacts they should see.

**Exit criteria:** an unauthenticated browser hitting the registry is rejected. A user from a different OIDC org cannot see the artifacts. Group-scoped layers, if any, work as expected.

---

## Month 2: sensitivity labels (advisory)

Goal: surface the existing risk profile of artifacts. No enforcement yet.

- Update lint rules to require `sensitivity:` in the frontmatter. Default is still `low`; the lint check is a warning at this stage and does not fail ingest.
- Authors annotate existing artifacts as part of their normal review cycle. Labels available: `low` (default), `medium`, `high`.
- Run `podium search --filter sensitivity=medium` and `podium search --filter sensitivity=high` to find higher-risk artifacts and review them.
- The audit log now records sensitivity per `load_artifact` call: useful signal for later.

**Exit criteria:** every artifact in the catalog has an explicit `sensitivity:` field. Authors know roughly what fraction of the catalog is `medium` or `high`.

**Reason for advisory mode:** the lint warning is a nudge for authors to think about sensitivity without breaking ingest. After the catalog is fully labeled, enforcement can be enabled without breaking author flow.

---

## Month 3: enforce signing for `sensitivity: high`

Goal: integrity guarantees on artifacts where integrity matters.

- Set `PODIUM_VERIFY_SIGNATURES=high-only`. Ingest of unsigned `sensitivity: high` artifacts now fails with `materialize.signature_invalid` at the consumer side.
- Roll signing into the author flow: each `high` artifact gets signed at PR-merge time (Sigstore-keyless via OIDC, or a tenant signing key managed by the registry).
- Promote the lint check from warning to error: missing `sensitivity:` is now an ingest failure.

**Exit criteria:** an unsigned high-sensitivity artifact cannot be loaded. The CI signing job is reliable. The signing flow is part of normal authoring.

**Defer:** signing for `medium` unless a specific requirement exists. Most teams find `medium` sensitivity is the bulk of their useful catalog, and mandatory signatures slow authoring.

---

## Month 6: freeze windows for production-impacting changes

Goal: protect critical periods (release cuts, year-end close, on-call rotations) from in-flight artifact changes.

- Configure freeze windows per tenant, e.g., "no ingest from Friday 17:00 to Monday 09:00" or "no ingest during the last week of the fiscal quarter."
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
- **Multi-tenant from the start.** For a Podium deployment that serves separate customer organizations, multi-tenancy and OIDC are required from the start. Skip the standalone phase and start with the standard deployment plus a per-tenant layer plan.
- **High-sensitivity domain only.** If the catalog contains only `sensitivity: high` content (security playbooks, compliance runbooks), enable signing on day 1 alongside identity. Skip the advisory-sensitivity phase.

The order in this guide moves from lower operational friction to more control. Choose the starting point based on current requirements, then move forward as requirements change.
