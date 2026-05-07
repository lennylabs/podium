# Progressive adoption

Podium ships with the full governance feature set: per-layer visibility, sensitivity labels, sandbox profiles, signing, hash-chained audit, freeze windows, SBOM/CVE pipeline, SCIM. Turning all of it on at once is a recipe for "we'll get to it after this quarter." Then you don't.

This guide is a 30 / 60 / 90 / 180-day on-ramp. It assumes you start with `podium serve --standalone` (or the equivalent permissive standard deployment) and progressively tighten as the catalog and team grow into needing each control. Skip ahead if a particular feature is already required by an external constraint (compliance, security review, contractual obligation). The order below is one that works for most teams, not the only valid order.

## Day 0: Install, Public Catalog, No Auth

Goal: get artifacts flowing. Don't gatekeep.

- `podium serve --standalone` on a single VM (or `podium serve --strict` with a basic standard deployment if you already have Postgres + S3).
- One layer (`team-shared`), `visibility: public` (the standalone default per §13.10), `git`-source pointing at one shared repo.
- No `PODIUM_VERIFY_SIGNATURES` setting (defaults to `never` in standalone, off entirely).
- No sensitivity labels required; `sensitivity:` is optional and defaults to `low`.
- No SCIM, no freeze windows, no SBOM pipeline.

**What "done" looks like**: 3+ people have authored a skill, merged it, and seen it load in their harness. Artifacts are in active use; nobody is fighting the tooling.

**Don't yet**: spend time arguing about layer hierarchies, group-based visibility, or naming conventions. Premature.

## Week 4: Add Identity (No Enforcement Yet)

Goal: get OAuth identity working so audit and visibility have something to attach to. Still permissive.

- Stand up an OIDC IdP (or hook into your existing one: Okta / Entra ID / Google Workspace / Auth0 / Keycloak). The [OIDC cookbooks](oidc/) have per-IdP setup steps.
- Switch from standalone (no auth) to standard deployment (or run standalone with auth via `PODIUM_IDENTITY_PROVIDER=oauth-device-code`).
- Existing `team-shared` layer keeps `visibility: public` for now, so every authenticated user can still see everything.
- New `<person>-personal` user-defined layers per author, default `users: [<self>]` visibility (the standard default).

**What "done" looks like**: every `load_artifact` and `search_artifacts` call in the audit log carries a real `sub` claim. Anonymous calls are gone. Personal layers exist for in-progress work.

**Why now**: identity is a prerequisite for everything that follows. Without it, audit entries are anonymous, sensitivity has no enforcement target, and per-layer visibility has nothing to filter on.

**Don't yet**: change `team-shared` visibility to `organization: true` or any group-based scope. Confirm the OIDC `sub` and `groups` claims arrive correctly first.

## Week 8: Narrow `team-shared` to Organization-Only

Goal: stop public visibility once you're confident identity works.

- Change `team-shared` layer visibility to `organization: true`. Authenticated users from your org see it; nobody else does.
- If you have multiple OIDC groups (engineering, sales, support, etc.) and some skills are team-specific, this is the moment to introduce group-based visibility on a second layer (e.g., `engineering-internal` with `groups: [engineering]`).
- Audit a week of `visibility.denied` events to confirm nobody is being blocked from artifacts they should see.

**What "done" looks like**: an unauthenticated browser hitting the registry is rejected. A user from a different OIDC org can't see your artifacts. Group-scoped layers, if any, work as expected.

## Month 2: Sensitivity Labels (Advisory)

Goal: surface the existing risk profile of artifacts. No enforcement yet.

- Update lint rules to require `sensitivity:` in the frontmatter. Default is still `low`; the lint check is a warning, not a failure.
- Authors annotate existing artifacts as part of their normal review cycle. Labels available: `low` (default), `medium`, `high`.
- Run `podium search --filter sensitivity=medium` and `podium search --filter sensitivity=high` to find higher-risk artifacts and review them.
- The audit log now records sensitivity per `load_artifact` call, which is useful signal for later.

**What "done" looks like**: every artifact in the catalog has an explicit `sensitivity:` field. Authors know roughly what fraction of the catalog is `medium` or `high`.

**Why advisory first**: the lint warning is a nudge for authors to think about sensitivity without breaking ingest. Once the catalog is fully labeled, you can flip to enforcement without breaking anyone's flow.

## Month 3: Enforce Signing for `sensitivity: high`

Goal: integrity guarantees on the artifacts where it actually matters.

- Set `PODIUM_VERIFY_SIGNATURES=high-only`. Ingest of unsigned `sensitivity: high` artifacts now fails with `materialize.signature_invalid` at the consumer side.
- Roll signing into the author flow: each `high` artifact gets signed at PR-merge time (Sigstore-keyless via OIDC, or a tenant signing key managed by the registry).
- Promote the lint check from warning to error: missing `sensitivity:` is now an ingest failure.

**What "done" looks like**: an unsigned high-sensitivity artifact cannot be loaded. The CI signing job is reliable. Authors aren't fighting the signing flow.

**Don't yet**: extend signing to `medium` unless you have a specific reason. Most teams find `medium` sensitivity is the bulk of their useful catalog and hard-requiring signatures slows authoring.

## Month 6: Freeze Windows for Production-Impacting Changes

Goal: protect critical periods (release cuts, year-end close, on-call rotations) from in-flight artifact changes.

- Configure freeze windows per tenant, such as "no ingest from Friday 17:00 to Monday 09:00" or "no ingest during the last week of the fiscal quarter."
- Train the team on the break-glass protocol: dual-signoff + justification, auto-expires after 24h, queues for post-hoc review.
- Run a dry-run freeze for one window before you commit, to catch the people who needed an exception.

**What "done" looks like**: freeze windows are scheduled and known to the team. The break-glass procedure has been used at least once in a controlled fashion. Audit log shows the expected pattern.

**Don't yet**: enable freeze for daily windows or for non-production layers. Freeze fatigue is real; reserve it for the moments that matter.

## Month 9+: Graduate the Rest as Needed

By this point governance overhead is amortized; the further controls are easier to add when their specific need shows up:

- **SBOM / CVE tracking** when you start shipping skills that bundle scripts pulling third-party dependencies. The vulnerability feed adds value once dependency surface is non-trivial.
- **Sandbox profile enforcement** (`PODIUM_ENFORCE_SANDBOX_PROFILE=true`) when artifacts ship code that runs on user machines and the harness honors profiles. Until then, the field is informational.
- **Transparency-log anchoring** when external auditors or regulators ask "can you prove this artifact existed at time T?" The hash-chained audit log already gives you that internally; transparency-log anchoring extends it across organizational boundaries.
- **Multi-region replication** when single-region availability stops being acceptable.

Each of these warrants a planned rollout: read the relevant spec section, run a controlled trial, then enable broadly.

## What changes if you don't follow this order

The most common reorderings, and why they're fine:

- **Compliance-driven**: if SOC2 / ISO 27001 / a customer contract requires signed-and-audited artifacts before launch, jump straight from Day 0 to Month 3's signing posture. The intermediate steps were comfort, not correctness.
- **Multi-tenant from day 1**: if you're hosting Podium for separate customer organizations, multi-tenancy and OIDC are non-negotiable from Day 0. Skip the standalone phase and start with the standard deployment + per-tenant layer plan.
- **High-sensitivity domain only**: if your catalog is *only* `sensitivity: high` content (security playbooks, compliance runbooks), enable signing on day 1 alongside identity. Skip the advisory-sensitivity phase.

The order in this guide is "least friction → most control," not "least secure → most secure." Choose your starting point based on what you actually need, then walk forward.
