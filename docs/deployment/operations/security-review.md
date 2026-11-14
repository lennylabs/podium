---
title: Security review checklist
nav_order: 7
description: "The checks to run before a security review of a Podium deployment: signature enforcement, tamper checks, and the limits of stored-row admission."
---

# Security review checklist

## Security review checklist

Review these items before a tenant that handles sensitive content goes live. Re-run the checklist after every major release and after any change to layer config, IdP, or sandbox enforcement settings.

| Item | Check |
|:--|:--|
| OAuth identity flow | Test the device-code flow for every IdP in production use. Set token lifetimes to 15 minutes or less. Confirm that revocation propagates within 60 seconds. |
| OIDC group claim mapping | Confirm that the group claims your IdP produces arrive in the registry's audit log. Test with a non-admin user. |
| Per-layer visibility | Confirm that each layer's `visibility:` declaration is correct. Test by impersonating a non-member identity through the `injected-session-token` test harness. |
| Signature enforcement | Confirm that every consumer resolves its signature policy to `always` and that every SDK client has a verification key configured, as [Signature enforcement](#signature-enforcement) describes. Test tampering as [Tamper checks](#tamper-checks) describes. |
| Audit hash chain | `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` is non-zero and `PODIUM_AUDIT_LOG_PATH` lists a file destination, so the registry re-verifies the chain on a schedule. Otherwise, the receiving system applies the `prev_hash` check to the shipped stream (see the [audit events reference](../../reference/audit-events#receiver-checks)). Alert on the `audit.gap_detected` event and on the "audit integrity ALERT" log line. [Hash chain](observability#hash-chain) describes both checks. |
| Webhook signing | Use a unique Git provider webhook HMAC secret per layer. Test with an invalid signature, and expect `ingest.webhook_invalid`. |
| Outbound receiver URL policy | Receiver CRUD is admin-gated, and `PODIUM_WEBHOOK_ALLOWED_TARGETS` lists only the internal receivers the deployment intends. Test by registering an `http` or private-address receiver, and expect `registry.invalid_argument`. |
| Sandbox profile honoring | Confirm that the hosts in production honor `sandbox_profile` for non-`unrestricted` artifacts. Test with a `read-only-fs` artifact and confirm the host enforces it. |
| Object-storage credentials | Use IAM roles or short-lived credentials, never static keys. Confirm the bucket policy denies public access. |
| Backup encryption | Encrypt Postgres backups and S3 object versioning at rest. Match the PITR window to your RTO. |
| Scope preview gating | Set `tenant.expose_scope_preview` deliberately per tenant. Set it to `false` for tenants where aggregate visibility counts would leak signal. |

### Signature enforcement

- Each consumer's MCP server resolves the signature policy from `PODIUM_VERIFY_SIGNATURES`, then from `defaults.verify_signatures` in its `sync.yaml`, then from an `always` fallback. The values are `never` and `always`. Confirm that the resolved policy is `always` on every consumer.
- A machine where an earlier release's standalone bootstrap wrote `verify_signatures: never` into `~/.podium/sync.yaml` keeps that setting until the line is removed. `podium-mcp` announces that `never` with a startup line naming the file.
- The registry does not read `PODIUM_VERIFY_SIGNATURES`.
- Server-source `podium sync` applies the same policy and key resolution as `podium-mcp`, with the same `always` default. A filesystem-source sync runs no delivery check.
- The Python and TypeScript SDKs read `PODIUM_VERIFY_SIGNATURES`. They default to `always` with a verification key configured and to `never` otherwise, so confirm that a key is configured on every SDK client.

The signature a consumer verifies is the registry's delivery signature. The registry mints it with its registry-managed key over the delivery hash of each response it serves.

- Every node that serves reads holds the registry signing key. A read-only replica topology, if one is ever deployed, places the key on each replica.
- A registry running with `PODIUM_SIGN=none` serves each delivery record unsigned, and `podium-mcp` still recomputes the delivery hash on every load.
- The delivery signature is a registry-managed envelope whatever key model signed the artifact at ingest. A consumer verifies it with `PODIUM_SIGNATURE_PROVIDER=registry-managed` and the registry's verification key set in `PODIUM_SIGNATURE_VERIFY_KEY`. `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`.
- A registry process signs under a rotated key from its next response onward, so each consumer adds the new key to its set before the rotation, as [Rotating the signing key](signing-keys#rotating-the-signing-key) states.

### Tamper checks

Run each tamper check through a full load, and expect the refusal from the registry. A full load is either a direct HTTP `load_artifact` GET with no `If-None-Match` header or a consumer whose cache is empty.

- **Altered response.** A response altered in transit fails at the consumer with `materialize.content_hash_mismatch`. One whose signature was altered fails with `materialize.signature_invalid`.
- **Stored-row admission.** The registry admits each stored row before it serves the row's content. It recomputes the content hash, checks each bundled resource's stored hash and size against its bytes, and then verifies the signature. Its refusal reaches every reader's full load, `podium sync` and the SDKs included. A HEAD and a matching conditional GET are answered without admission, so a consumer that already holds the row under an unchanged content hash keeps serving that copy.
- **Altered stored row.** On any registry, a row whose stored bytes were altered while its stored content hash was left unchanged is refused with `materialize.content_hash_mismatch`. On a signing registry, a row altered together with its hash is refused with `materialize.signature_invalid`, and one whose stored signature was removed with `materialize.signature_missing`. A registry with `PODIUM_SIGN=none` serves a row altered together with its hash.
- **Altered object-storage body.** A bundled-resource body held in object storage and altered there is refused by the registry on a full load with `materialize.content_hash_mismatch`. The object-storage copy of a resource the row holds inline is never served, because the registry serves that resource inline from the admitted row on `load_artifact` and on each `artifacts:batchLoad` item.
- **Body altered after admission.** An object-held body altered after the registry admitted the row, and a manifest-body object replaced at its key, which admission does not read, fail at the fetch in `podium-mcp` with `materialize.fetch_failed` and a `content hash mismatch` message.
- **Body checks by other clients.** Server-source `podium sync` refuses a manifest-body object whose bytes do not hash to the content hash its link carries. The sync then fails with `load_artifact <id>: manifest body content hash mismatch` without materializing any artifact. `podium sync` compares no fetched bundled-resource body with its hash, and the SDKs compare no fetched body with its hash, so each receives such a body unchecked. An operator who needs that check reads the artifact through `podium-mcp`.

Admission checks a row's content and does not attest which row answers a request. A party that can write the metadata store can therefore withhold a row, relabel an admissible row, or have an older admissible row served in place of a newer one.

### Limits of stored-row admission

Stored-row admission has limits against a party that can write the metadata store without holding the signing key. These limits are known and are not closed by admission.

- Such a party can withhold, relabel, re-layer, or roll back an admissible row.
- Where the store is the SQLite store in the key file's directory, it can also delete the first-start rewrite's completion record, so that the next start signs the unsigned rows it wrote. On every other store, such a start refuses in either signing mode and signs nothing.
- On any store, a later `sign-stored-rows --include-unsigned` signs every unsigned row its reviewed dry run lists, including one such a party stored before that dry run. Read the rows the dry run lists before passing its plan digest.
- An unsigned row such a party stores between the dry run and the run changes the plan digest, and the run refuses with exit status 3. A row stored after the run builds its plan is absent from that plan and stays unsigned.
- A `sign-stored-rows` run also re-signs rows signed under a `verify:` key. While a compromised key stays on a `verify:` line, a run re-signs rows its holder stored.

Restrict write access to the metadata store and to its backups.

---
