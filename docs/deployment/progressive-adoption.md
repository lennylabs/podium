---
title: Progressive adoption
nav_order: 6
description: A staged plan for adopting governance features (identity, sensitivity labels, signing, and freeze windows) as a catalog and team grow, rather than enabling every control on day one.
---

# Progressive adoption

Podium ships with a full set of governance controls: per-layer visibility, sensitivity labels, sandbox profiles, signing, a hash-chained audit log, freeze windows, and SCIM. Turning all of them on at once usually delays adoption. This guide stages them, so each control arrives when the catalog and the team need it.

The guide starts from a permissive [single-node](modes/single-node) deployment, or an equally permissive [clustered](modes/clustered) one. Each stage lists its goal, the changes to make, and the exit criteria for moving on. Skip ahead when an external constraint, such as compliance, a security review, or a contractual obligation, already requires a control. The order below works for most teams, and [Alternate ordering](#alternate-ordering) lists common variations.

---

## Day 0: install, public catalog, no auth

**Goal:** get artifacts flowing without governance gates.

- Run `podium serve --standalone` on a single VM, or `podium serve --strict` against Postgres and object storage when those already exist.
- Create one layer named `team-shared`, with `visibility: public` and a `git` source that points at one shared repository. Public visibility is the default when no identity provider is configured. [Connecting layer sources](content/layer-sources#git-repositories) covers declaring the layer.
- Leave `PODIUM_VERIFY_SIGNATURES` unset on the registry, which does not read it. Each consumer verifies signatures, as [Signing from Day 0](#signing-from-day-0) describes.
- Require no sensitivity labels. `sensitivity:` is optional and defaults to `low`.
- Configure no SCIM and no freeze windows.

**Exit criteria:** several people have authored a skill, merged it, and seen it load in their harness. Artifacts are in active use, and the tooling is not blocking the authoring loop.

**Defer:** layer hierarchies, group-based visibility, and naming conventions.

### Signing from Day 0

The registry signs every artifact at ingest by default.

- A start against Postgres also sets `PODIUM_SIGN_KEY_PATH` to a key file on persistent storage, or sets `PODIUM_SIGN=none`. A registry with signing on, a Postgres store, and no key path is refused at start.
- A deployment that means to sign keeps signing on from Day 0. Choosing `PODIUM_SIGN=none` now and turning signing on later leaves every artifact ingested before the switch unloadable, for every reader. The artifacts stay unloadable until a reviewed `sign-stored-rows` run signs the stored rows, or until a new version of each artifact is ingested.
- The reviewed run has three parts: `podium admin sign-stored-rows --include-unsigned --dry-run`, a review of the rows it lists, and `podium admin sign-stored-rows --include-unsigned --plan-digest=<digest>` with the digest on the dry run's last line. A deployment that ships only the server image runs the same pair through `podium-server sign-stored-rows`.

Each consumer's MCP server verifies the signature on every artifact it loads. It resolves its policy from `PODIUM_VERIFY_SIGNATURES`, then `defaults.verify_signatures` in `sync.yaml`, then an `always` fallback. A signing registry also verifies each stored signature before it serves the row, for every reader.

- A consumer on the registry's machine resolves the verification key set from the `public:` line and every `verify:` line of the registry's own key file.
- A consumer on another machine sets `PODIUM_SIGNATURE_PROVIDER=registry-managed` and `PODIUM_SIGNATURE_VERIFY_KEY`, one key or, during a key rotation, a comma-separated list. See [Configure your harness](../consuming/configure-your-harness).

The `sensitivity` label plays no part in signature verification.

---

## Week 4: add identity (no enforcement yet)

**Goal:** get OAuth identity working so audit and visibility have an identity subject. Enforcement remains permissive.

1. Stand up an OIDC IdP, or connect an existing one. Okta, Entra ID, Auth0, and Keycloak issue a token the registry's `oidc-jwt` verifier accepts. Google Workspace caches an opaque access token that the verifier cannot parse, so it routes callers through a gateway under `trusted-headers` instead. The [OIDC cookbooks](identity/oidc/) have per-IdP setup steps and cover both paths.
2. Configure `PODIUM_IDENTITY_PROVIDER=oidc-jwt` on the registry, with `PODIUM_OAUTH_ISSUER` set to the IdP issuer and `PODIUM_OAUTH_AUDIENCE` set to the registry endpoint. The registry then verifies each presented token against the issuer's JWKS. This works on either deployment mode, and moving to the Clustered deployment mode at the same time is optional. See [Access control](content/access-control).
3. Have each developer run `podium login` once. The CLI completes the device-code flow against the same IdP and caches the resulting token in the OS keychain. On the gateway path the gateway authenticates the caller, so this step does not apply.
4. Keep `visibility: public` on the existing `team-shared` layer for now, so every authenticated user still sees everything.
5. Register a user-defined layer per author, for example `alice-personal`. A user-defined layer carries implicit `users: [<registrant>]` visibility.

**Exit criteria:** every `load_artifact` and `search_artifacts` call in the audit log carries a `sub` claim. Anonymous calls are gone. Personal layers exist for in-progress work.

**Why now:** identity is a prerequisite for every later stage. Without it, audit entries are anonymous, sensitivity has no enforcement target, and per-layer visibility has nothing to filter on.

**Defer:** changes to `team-shared` visibility, including `organization: true` and group-based scopes. Confirm first that the OIDC `sub` and `groups` claims arrive correctly.

---

## Week 8: narrow team-shared to organization-only

**Goal:** stop public visibility once identity works.

1. Change the `team-shared` layer's visibility to `organization: true`. Authenticated users from the organization see it, and other callers do not.
2. When several OIDC groups exist, such as engineering, sales, and support, and some artifacts are team-specific, introduce group-based visibility on a second layer. An example is `engineering-internal` with `groups: [engineering]`.
3. Audit a week of `visibility.denied` events to confirm callers are not blocked from artifacts they should see.

**Exit criteria:**

- An unauthenticated caller sees an empty catalog, because no layer is public any more. Under `oidc-jwt`, a request whose configured token header carries no `Bearer` credential is anonymous rather than rejected, so it resolves to public visibility only. Where the registry enables the browser flow, such a request is anonymous only when it also presents no valid session cookie.
- A user from a different OIDC org cannot see the artifacts.
- Group-scoped layers, if any, work as expected.

---

## Month 2: sensitivity labels (advisory)

**Goal:** surface the existing risk profile of artifacts, with no enforcement yet.

1. Update the lint rules to require `sensitivity:` in the frontmatter. The default is still `low`. At this stage the lint check is a warning and does not fail ingest.
2. Have authors annotate existing artifacts as part of their normal review cycle. The labels are `low` (the default), `medium`, and `high`.
3. Run `podium sync --preview` to print the aggregate scope preview, which breaks the caller's effective view down by sensitivity. Use the counts to size the review backlog. There is no sensitivity filter on `podium search`.

From this stage the audit log records sensitivity on each `load_artifact` call, which later stages use.

**Exit criteria:** every artifact in the catalog has an explicit `sensitivity:` field, and authors know roughly what fraction of the catalog is `medium` or `high`.

**Reason for advisory mode:** the lint warning prompts authors to consider sensitivity without breaking ingest. Once the catalog is fully labeled, enforcement can be enabled without breaking the authoring flow.

---

## Month 3: enforce signing everywhere

**Goal:** confirm that every consumer verifies every artifact it loads.

1. Confirm every consumer runs with `PODIUM_VERIFY_SIGNATURES=always`, the default. Remove any `never` that a machine's `sync.yaml` still carries. `podium-mcp` prints a startup line naming the file that supplied a `never`.
2. Promote the lint check from warning to error, so a missing `sensitivity:` is an ingest failure.
3. Add the registry's signing key to the backup set.

Under `always`, loading an unsigned artifact through the MCP server fails with `materialize.signature_missing`, and loading one whose signature does not verify fails with `materialize.signature_invalid`. The registry does not read `PODIUM_VERIFY_SIGNATURES`.

- Server-source `podium sync` applies the same policy and key resolution as `podium-mcp`.
- A filesystem-source sync runs no delivery check.
- An SDK client with no verification key configured defaults to `never`.

The registry signs at ingest with its registry-managed key. That key is one Ed25519 keypair per registry deployment at `PODIUM_SIGN_KEY_PATH`, shared by every process serving the store and across every tenant it serves.

### Rows stored unsigned

`never` fits only a registry running with `PODIUM_SIGN=none`. A signing registry refuses every row stored unsigned, to every reader, whatever the consumer's policy. Turning signing on over a store that holds unsigned rows makes each of them unloadable until a reviewed `sign-stored-rows` run signs it or a new version of it is ingested.

1. Run `podium admin sign-stored-rows --include-unsigned --dry-run`, or `podium-server sign-stored-rows` on a deployment that ships only the server image.
2. Read the rows it lists.
3. Run the command with `--include-unsigned --plan-digest=<digest>`, passing the digest on the dry run's last line.

The flag attests every unsigned row the reviewed dry run lists. The run refuses with exit status 3, writing nothing, when its plan differs from the reviewed one.

When the store holds no record that the first-start rewrite of stored content hashes has completed, the command performs that rewrite. It must therefore run only while no registry process on the previous release serves the store, and it cannot detect such a process ([The `sign-stored-rows` command](operations/signing-keys#the-sign-stored-rows-command)).

**Exit criteria:** an unsigned artifact cannot be loaded through `podium-mcp`, and the registry's signing key is in the backup set.

---

## Month 6: freeze windows for production-impacting changes

**Goal:** protect critical periods, such as release cuts, year-end close, and on-call rotations, from in-flight artifact changes.

1. Configure freeze windows in `registry.yaml` under `registry.freeze_windows:`. Each entry carries a `name`, an absolute RFC 3339 `start` and `end`, and the operations it blocks. There is no recurrence field, so a repeating window such as a weekend freeze is written as one entry per occurrence.
2. Train the team on the break-glass protocol. A break-glass grant requires dual sign-off and a justification, expires after 24 hours, and queues for post-hoc review.
3. Run a dry-run freeze for one window before enforcing it, and identify workflows that need an exception.

**Exit criteria:** freeze windows are scheduled and known to the team. The break-glass procedure has been used at least once in a controlled fashion, and the audit log shows the expected pattern.

**Defer:** daily freeze windows and freeze windows for non-production layers. Reserve freeze windows for periods where ingest would create operational risk.

---

## Month 9 and later: graduate the rest as needed

By this point the governance overhead is amortized, and each remaining control is easier to add when its specific need appears:

- **Sandbox profile enforcement** (`PODIUM_ENFORCE_SANDBOX_PROFILE=true`), when artifacts ship code that runs on user machines and the harness honors profiles. Until then, the field is informational.
- **Local chain-head anchoring** (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`), when auditors ask for signed checkpoints of the audit chain. [Audit chain anchoring](#audit-chain-anchoring) describes it.
- **Multi-region replication**, when single-region availability stops being acceptable.

Give each of these a planned rollout: read the relevant spec section, run a controlled trial, and then enable it broadly.

### Audit chain anchoring

The registry periodically signs the chain head with a dedicated anchor key (`PODIUM_AUDIT_SIGNING_KEY_PATH`) and appends an `audit.anchored` event. An auditor verifies the anchor with the key's public half.

- Anchoring runs only when `PODIUM_AUDIT_LOG_PATH` lists a file destination. A registry whose list names no file destination, such as one that ships its stream only to `stdout` or to an HTTP or OTLP receiver, logs that anchoring is disabled. The receiving system then checks the shipped stream as [Audit events](../reference/audit-events#receiver-checks) describes.
- The signature covers the chain-head digest alone, and the operator holds the anchor key. The evidence therefore stays within the operator's trust domain and does not establish when an anchor was made.
- Podium does not submit the chain head to a public transparency log.

[Audit and observability](operations/observability#audit-log) covers the audit log and its destinations.

---

## Alternate ordering

Common reorderings:

- **Compliance-driven.** When SOC 2, ISO 27001, or a customer contract requires signed and audited artifacts before launch, go straight from Day 0 to the Month 3 signing posture, which is setting `always` against a registry that already signs. The intermediate stages ease rollout for teams without external pressure and are not required for correctness.
- **Multi-tenant from the start.** A deployment that serves separate customer organizations runs in multi-tenant mode (`PODIUM_MULTI_TENANT`) from the start, with an identity provider that authenticates each caller. Start it on the [Clustered](modes/clustered) deployment mode with a per-tenant layer plan, and skip the single-node phase. The Clustered deployment mode's Postgres backend isolates each org in its own schema.
- **High-sensitivity domain only.** When the catalog contains only `sensitivity: high` content, such as security playbooks and compliance runbooks, enable identity on Day 1 and skip the advisory-sensitivity phase. Signing is already on from Day 0 by default. A registry that started with `PODIUM_SIGN=none` keeps every artifact ingested before signing is turned on unloadable, for every reader, until a reviewed `sign-stored-rows --include-unsigned` run signs it, or until a new version of it is ingested. [Signing from Day 0](#signing-from-day-0) lists the run's steps.

The order in this guide moves from lower operational friction to more control. Choose the starting point from current requirements, and move forward as the requirements change.
