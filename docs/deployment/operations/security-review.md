---
title: Security review checklist
nav_order: 7
description: "The checks to run before a tenant that handles sensitive content goes live: the review checklist, signature enforcement, tamper checks, the limits of stored-row admission, and removal of the identity provider."
---

# Security review checklist

This page lists the checks to run before a tenant that handles sensitive content goes live. Re-run the checklist after every major release and after any change to the layer configuration, the identity provider, or the sandbox enforcement settings. The sections after the checklist describe how signature enforcement works, how to test it by tampering, what stored-row admission does not protect against, and what removing the identity provider exposes.

## Checklist

| Item | Check |
|:--|:--|
| OAuth identity flow | Test the device-code flow for every identity provider in production use. Set token lifetimes to 15 minutes or less. Confirm that revocation propagates within 60 seconds. |
| OIDC group claim mapping | Confirm that the group claims the identity provider issues arrive in the registry's audit log. Test with a non-admin user. [Groups](../identity/groups) describes how the registry reads them. |
| Per-layer visibility | Confirm that each layer's `visibility:` declaration is correct. Test with a non-member identity, for example a session token signed for the `injected-session-token` provider. [Access control](../content/access-control) describes layer visibility. |
| Identity configuration control | Confirm that only the operators who may expose every layer can change the registry's environment, `registry.yaml`, Helm values, and secrets, or restart it. Alert on the signals [Removing the identity provider](#removing-the-identity-provider) lists. |
| Signature enforcement | Confirm that every consumer resolves its signature policy to `always` and that every SDK client has a verification key configured, as [Signature enforcement](#signature-enforcement) describes. Test tampering as [Tamper checks](#tamper-checks) describes. |
| Audit hash chain | Set `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` to a non-zero value with a file destination in `PODIUM_AUDIT_LOG_PATH`, so the registry re-verifies the chain on a schedule. Without a file destination, the receiving system applies the `prev_hash` check to the shipped stream, as the [audit events reference](../../reference/audit-events#receiver-checks) describes. Alert on the `audit.gap_detected` event and on the "audit integrity ALERT" log line. [Hash chain](observability#hash-chain) describes both checks. |
| Webhook signing | Use a unique Git provider webhook HMAC secret per layer. Test with an invalid signature, and expect `ingest.webhook_invalid`. |
| Outbound receiver URL policy | Confirm that receiver changes are admin-gated and that `PODIUM_WEBHOOK_ALLOWED_TARGETS` lists only the internal receivers the deployment intends. Test by registering an `http` or private-address receiver, and expect `registry.invalid_argument`. [Outbound webhooks](outbound-webhooks#receiver-url-policy) describes the policy. |
| Sandbox profile honoring | Confirm that the hosts in production honor `sandbox_profile` for artifacts other than `unrestricted` ones. Test with a `read-only-fs` artifact and confirm that the host enforces it. |
| Object-storage credentials | Use IAM roles or short-lived credentials instead of static keys. Confirm that the bucket policy denies public access. |
| Backup encryption | Encrypt Postgres backups and S3 object versioning at rest. Match the PITR window to the recovery time objective. |
| Scope preview gating | Set `tenant.expose_scope_preview` deliberately per tenant. Set it to `false` for tenants where aggregate visibility counts would leak signal. |
| Metadata store access | Restrict write access to the metadata store and to its backups, as [Limits of stored-row admission](#limits-of-stored-row-admission) explains. |

## Signature enforcement

The signature a consumer verifies is the registry's delivery signature. The registry signs the delivery hash of each response it serves with its registry-managed key, as [What the registry signs](signing-keys#what-the-registry-signs) describes.

Each consumer resolves its signature policy as follows:

- Each consumer's MCP server resolves the signature policy from `PODIUM_VERIFY_SIGNATURES`, then from `defaults.verify_signatures` in its `sync.yaml`, then from an `always` fallback. The values are `never` and `always`. Confirm that the resolved policy is `always` on every consumer.
- A machine where an earlier release's standalone bootstrap wrote `verify_signatures: never` into `~/.podium/sync.yaml` keeps that setting until the line is removed. `podium-mcp` announces that `never` with a startup line naming the file.
- Server-source `podium sync` applies the same policy and key resolution as `podium-mcp`, with the same `always` default. A filesystem-source sync runs no delivery check.
- The Python and TypeScript SDKs read `PODIUM_VERIFY_SIGNATURES`. They default to `always` with a verification key configured and to `never` otherwise, so confirm that a key is configured on every SDK client.
- The registry does not read `PODIUM_VERIFY_SIGNATURES`.

The registry side carries these conditions:

- Every node that serves reads holds the registry signing key. A read-only replica topology, if one is ever deployed, places the key on each replica.
- A registry running with `PODIUM_SIGN=none` serves each delivery record unsigned, and `podium-mcp` still recomputes the delivery hash on every load.
- The delivery signature is a registry-managed envelope whatever key model signed the artifact at ingest. A consumer verifies it with `PODIUM_SIGNATURE_PROVIDER=registry-managed` and the registry's verification key set in `PODIUM_SIGNATURE_VERIFY_KEY`. `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`.
- A registry process signs under a rotated key from its next response onward, so each consumer adds the new key to its set before the rotation, as [Rotating the signing key](signing-keys#rotating-the-signing-key) states.

[Verifying deliveries](../../consuming/verifying-deliveries) describes the consumer settings in full.

## Tamper checks

Run each tamper check through a full load, and expect the refusal from the registry. A full load is either a direct HTTP `load_artifact` GET with no `If-None-Match` header or a load by a consumer whose cache is empty.

- **Altered response.** A response altered in transit fails at the consumer with `materialize.content_hash_mismatch`. One whose signature was altered fails with `materialize.signature_invalid`.
- **Stored-row admission.** The registry admits each stored row before it serves the row's content. It recomputes the content hash, checks each bundled resource's stored hash and size against its bytes, and then verifies the signature. Its refusal reaches every reader's full load, `podium sync` and the SDKs included. A HEAD and a matching conditional GET are answered without admission, so a consumer that already holds the row under an unchanged content hash keeps serving that copy.
- **Altered stored row.** On any registry, a row whose stored bytes were altered while its stored content hash was left unchanged is refused with `materialize.content_hash_mismatch`. On a signing registry, a row altered together with its hash is refused with `materialize.signature_invalid`, and one whose stored signature was removed with `materialize.signature_missing`. A registry with `PODIUM_SIGN=none` serves a row altered together with its hash.
- **Altered object-storage body.** A bundled-resource body held in object storage and altered there is refused by the registry on a full load with `materialize.content_hash_mismatch`. The object-storage copy of a resource the row holds inline is never served, because the registry serves that resource inline from the admitted row on `load_artifact` and on each `artifacts:batchLoad` item.
- **Body altered after admission.** An object-held body altered after the registry admitted the row, and a manifest-body object replaced at its key, which admission does not read, fail at the fetch in `podium-mcp` with `materialize.fetch_failed` and a `content hash mismatch` message.
- **Body checks by other clients.** Server-source `podium sync` refuses a manifest-body object whose bytes do not hash to the content hash its link carries. The sync then fails with `load_artifact <id>: manifest body content hash mismatch` without materializing any artifact. `podium sync` and the SDKs compare no fetched bundled-resource body with its hash, so each receives such a body unchecked. An operator who needs that check reads the artifact through `podium-mcp`.

## Limits of stored-row admission

Admission checks a row's content. It does not attest which row answers a request, because the stored signature covers the content hash alone. A party that can write the metadata store without holding the signing key keeps the following abilities, which admission does not remove:

- It can withhold an admissible row, serve it under another row's identity or layer, or have an older admissible row served in place of a newer one.
- Where the store is the SQLite store in the key file's directory, it can delete the completion record of the first-start rewrite, so that the next start signs the unsigned rows the party wrote. On every other store, such a start is refused in either signing mode and signs nothing.
- On any store, a later `sign-stored-rows --include-unsigned` signs every unsigned row its reviewed dry run lists, including one such a party stored before that dry run. Read the rows the dry run lists before passing its plan digest.
- An unsigned row such a party stores between the dry run and the run changes the plan digest, and the run refuses with exit status 3. A row stored after the run builds its plan is absent from that plan and stays unsigned.
- A `sign-stored-rows` run also re-signs rows signed under a `verify:` key. While a compromised key stays on a `verify:` line, a run re-signs rows its holder stored. [Compromised keys](signing-keys#compromised-keys) gives the response.

Restrict write access to the metadata store and to its backups.

## Removing the identity provider

Whoever can change the registry's configuration or restart it can remove its identity provider. A registry restarted with no request-time identity verifier, or in public mode, resolves every caller as anonymous and applies no layer visibility. Every caller that can reach it then sees every layer, whatever the layer's `visibility:` declares. On a registry with no identity provider and not in public mode, every caller also passes the admin rule, so every caller can register, update, and unregister layers and request erasure. The stored declarations stay in place and take effect again once a provider is configured.

On a multi-tenant registry, removing the identity provider refuses startup with `config.multitenant_no_verifier`, because a multi-tenant registry selects each request's tenant from the verified caller. Turning multi-tenant mode off as well starts a single-tenant registry that serves the default tenant alone. Every caller then sees every layer of the default tenant, and the other tenants' layers stay in the store and are served to no caller.

The registry reads its identity configuration at startup and has no record of who changed it, so it cannot tell an intended removal from an unintended one. The defence is control over who can change the deployment's configuration and secrets: the environment variables, `registry.yaml`, the Helm values, the container or service definition, and the secret store that feeds them. Treat write access to any of them as equivalent to read access to every layer.

Alert on the signals that report a registry running without verified identity:

- `/healthz` returns `mode: public` when the registry runs in public mode. A registry restarted with no identity provider and not in public mode returns `mode: ready`, so this signal does not cover that case.
- The startup log carries the public-mode banner, `⚠  PUBLIC MODE: all artifacts visible to all callers without authentication.`
- The startup log carries the warning that lists the stored layers with no `public` grant. The registry logs it whenever it verifies no caller and its tenant holds such a layer, in public mode and without an identity provider alike. [Restricted layers on a registry that verifies no caller](../content/access-control#restricted-layers-on-a-registry-that-verifies-no-caller) gives its text.
- The startup log lacks the line that names a verifying provider, such as `identity provider: oidc-jwt (verifying caller tokens against accepted issuers ...)`, `identity provider: trusted-headers (...)`, or `identity provider: injected-session-token (verifying runtime-signed JWTs)`. A registry that verifies its callers logs one of these lines at every start, so alert when the line expected for the deployment is missing.
- The startup line `podium-server listening on <address> (mode=<mode>)` reports `mode=standalone` for a registry with no identity provider and `mode=public` for public mode. A registry whose `PODIUM_IDENTITY_PROVIDER` names a label it resolves to no configured provider reports that label, such as `mode=external`, although it verifies no caller, and its `/healthz` returns `mode: ready`. On such a registry, the mode value does not show that verification is absent, and the missing provider line above does.
- Audit records for calls from clients that normally authenticate carry `caller.identity: "system:public"` and `caller.public_mode: true`. An anonymous call to a registry that verifies identity records the same values, so alert on a change in their volume rather than on a single record.

[Public-mode misconfiguration](troubleshooting#public-mode-misconfiguration) gives the recovery steps for public mode. For a registry restarted without its provider, restore the provider's configuration and restart the registry.
