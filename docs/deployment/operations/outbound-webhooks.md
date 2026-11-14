---
title: Outbound webhooks
nav_order: 8
description: "Send registry events to webhook receivers, the receiver URL policy, and integrations built on outbound events."
---

# Outbound webhooks

## Outbound webhook receivers

The registry emits outbound webhooks for change events to receivers that an admin registers per tenant through the receiver CRUD endpoints (`/v1/webhooks`). The registry originates the delivery request, so a receiver URL is an outbound target the registry reaches on a caller's behalf. The following settings govern it.

### Receiver URL policy

The registry validates a receiver URL at registration and re-checks it at delivery. This policy prevents a caller who can register a receiver from pointing the registry at an internal endpoint the caller would not otherwise reach. By default the registry:

- Requires the `https` scheme.
- Rejects a URL that resolves to a loopback, link-local, or private address, for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges.
- Does not follow a redirect to such a target.

A rejected target returns `registry.invalid_argument` naming the disallowed host.

### Allowing internal receivers

A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets `PODIUM_WEBHOOK_ALLOWED_TARGETS`. The variable is a comma-separated allowlist of hosts or CIDRs that the policy permits in addition to public addresses.

- An entry is either a bare host matched against the URL host or a CIDR matched against the resolved addresses.
- The variable is empty by default, which keeps the strict policy.
- A malformed entry aborts boot, so the registry fails closed rather than silently widening the policy.
- The startup log reports the wired allowlist.

### Receiver registration on a deployment without authentication

The receiver CRUD endpoints require the per-tenant admin role and return `auth.forbidden` for a non-admin caller, the same authorization posture as the admin-grant endpoints. A caller the registry cannot authenticate resolves to the anonymous public identity, which fails the admin check. Receiver registration is therefore closed on a bind with no identity provider. `POST /v1/admin/grants` is itself admin-gated and cannot create the first admin on such a deployment.

To register receivers on such a deployment:

1. Seed the first admin at boot with `PODIUM_BOOTSTRAP_ADMINS`.
2. Configure an identity provider the registry verifies: `injected-session-token`, `oidc-jwt`, or `trusted-headers`.
3. Have that admin present its credential when registering a receiver.

---

## Webhook-driven integrations

Receivers for the outbound webhooks feed Slack channels, ticket trackers, deployment pipelines, and internal dashboards. The registry emits the events, and the receiver decides what to do with them.

The registry delivers these events to a receiver:

- `artifact.published`
- `artifact.deprecated`
- `domain.published`
- `layer.ingested`
- `layer.history_rewritten`

Read events such as `domains.searched` are recorded in the audit log and are never delivered to a receiver.

Common uses:

- Notify owners on `artifact.deprecated`.
- Page on-call on `layer.history_rewritten`.
- Start a downstream rebuild on `artifact.published` for artifacts that match certain paths.

### Triggering CI from a webhook (repository_dispatch relay)

A common receiver starts a CI job from a registry event. Marketplace publishing is one case: a receiver filtered to `layer.ingested` starts a CI job that runs `podium sync --config` against a `kind: marketplace` target. [Publishing marketplace repositories](../content/publishing) covers that target.

GitHub starts a workflow from an external system only through its authenticated REST API (`repository_dispatch` or `workflow_dispatch`). A Podium receiver posts an HMAC-signed event body, which GitHub's dispatch endpoint does not accept. A small operator relay bridges the two:

1. The receiver posts the signed event to the relay.
2. The relay verifies the HMAC against the receiver secret.
3. The relay calls `POST https://api.github.com/repos/<owner>/<repo>/dispatches` with `{"event_type":"podium-layer-ingested"}` and a GitHub token.
4. The workflow, which listens on `repository_dispatch`, runs.

Set the receiver's `debounce` field to coalesce a burst of `layer.ingested` events into one batch delivery and one CI dispatch. With `debounce` unset, the relay fires once per event, and the CI system's own concurrency control collapses the redundant runs.

Registering the receiver requires the per-tenant admin role on the receiver CRUD endpoints. They return `auth.forbidden` to a caller without it. [HTTP API → Outbound webhooks](../../reference/http-api#outbound-webhooks) documents the receiver fields, the SSRF policy on the receiver URL, and the batch envelope.
