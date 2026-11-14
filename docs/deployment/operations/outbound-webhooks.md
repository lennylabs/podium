---
title: Outbound webhooks
nav_order: 8
description: "Send registry change events to webhook receivers: the events delivered, receiver registration, the receiver URL policy, and integrations such as a CI relay built on the events."
---

# Outbound webhooks

The registry sends change events to webhook receivers that a tenant admin registers. A receiver feeds a Slack channel, a ticket tracker, a deployment pipeline, or an internal dashboard. The registry emits the events, and the receiver decides what to do with them.

This page covers the operator side: which events a receiver gets, who can register a receiver, the URL policy that limits where the registry sends deliveries, and integrations built on the events. [HTTP API: Outbound webhooks](../../reference/http-api#outbound-webhooks) documents the event payload, the receiver object, and the batch envelope.

## Events

The registry delivers these events to a receiver whose filter matches them:

- `artifact.published`
- `artifact.deprecated`
- `domain.published`
- `layer.ingested`
- `layer.history_rewritten`

The registry signs each delivery with the receiver's HMAC secret. Read events such as `domains.searched` are recorded in the audit log and are never delivered to a receiver.

## Outbound webhook receivers

An admin registers receivers per tenant through the receiver CRUD endpoints under `/v1/webhooks`. Each receiver belongs to the tenant whose admin registered it. The endpoints require the per-tenant admin role and return `auth.forbidden` to a caller without it, the same authorization as the admin-grant endpoints. [Receiver CRUD](../../reference/http-api#receiver-crud) lists the endpoints and the receiver fields.

The registry originates each delivery request, so a receiver URL is an outbound target that the registry reaches on a caller's behalf. The URL policy below limits those targets.

### Receiver URL policy

The registry validates a receiver URL at registration and checks it again at delivery. The policy prevents a caller who can register a receiver from pointing the registry at an internal endpoint the caller would not otherwise reach. By default, the registry applies these rules:

- It requires the `https` scheme.
- It rejects a URL that resolves to a loopback, link-local, or private address, for example `127.0.0.0/8`, `::1`, `169.254.0.0/16`, and the RFC 1918 ranges.
- It follows a redirect only when the redirect target passes the same policy.

A rejected target returns `registry.invalid_argument` naming the disallowed host.

### Allowing internal receivers

A deployment whose receiver is legitimately internal, such as an in-cluster relay, sets `PODIUM_WEBHOOK_ALLOWED_TARGETS`. The variable is a comma-separated allowlist of hosts or CIDRs that the policy permits in addition to public addresses.

- An entry is either a bare host, matched against the URL host, or a CIDR, matched against the resolved addresses.
- The allowlist lifts only the address rule. Every receiver URL still requires the `https` scheme.
- The variable is empty by default, which keeps the strict policy.
- A malformed entry aborts startup, so the registry fails closed rather than silently widening the policy.
- The startup log reports the allowlist in effect.

### Receiver registration on a deployment without authentication

A caller that the registry cannot authenticate resolves to the anonymous public identity, which fails the admin check. Receiver registration is therefore closed on a registry with no identity provider. `POST /v1/admin/grants` is itself admin-gated and cannot create the first admin on such a deployment.

To register receivers on such a deployment:

1. Seed the first admin at startup with `PODIUM_BOOTSTRAP_ADMINS`.
2. Configure an identity provider that the registry verifies: `injected-session-token`, `oidc-jwt`, or `trusted-headers`. [Identity providers](../identity/) compares them.
3. Have that admin present its credential when registering a receiver.

## Webhook-driven integrations

Common uses of the events include the following:

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

Set the receiver's `debounce` field to coalesce a burst of `layer.ingested` events into one batch delivery and one CI dispatch. With `debounce` unset, the relay fires once per event, and the CI system's own concurrency control collapses the redundant runs. [Debounce and batch delivery](../../reference/http-api#debounce-and-batch-delivery) describes the batch envelope.

A relay that runs inside the cluster resolves to a private address, so the deployment lists it in `PODIUM_WEBHOOK_ALLOWED_TARGETS`, as [Allowing internal receivers](#allowing-internal-receivers) describes.
