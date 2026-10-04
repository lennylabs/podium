# 8. Audit and Observability

## 8.1 What Gets Logged

Every significant event, each carrying a trace ID (W3C Trace Context):

| Event                          | When                                                               | Source   |
| ------------------------------ | ------------------------------------------------------------------ | -------- |
| `domain.loaded`                | Host invoked `load_domain`                                         | Registry |
| `domains.searched`             | Host invoked `search_domains`                                      | Registry |
| `artifacts.searched`           | Host invoked `search_artifacts`                                    | Registry |
| `artifact.loaded`              | Host invoked `load_artifact`                                       | Registry |
| `artifact.published`           | A new `(artifact_id, version)` was ingested                        | Registry |
| `artifact.deprecated`          | An ingested manifest set `deprecated: true`                        | Registry |
| `artifact.signed`              | Artifact version signed                                            | Registry |
| `domain.published`             | A `DOMAIN.md` was added or changed                                 | Registry |
| `layer.ingested`               | A layer completed an ingest cycle                                  | Registry |
| `layer.history_rewritten`      | Force-push or history rewrite detected on a `git`-source layer     | Registry |
| `layer.config_changed`         | An admin-defined layer was added, removed, restored, or patched, or the tenant's layer order was changed | Registry |
| `layer.user_registered`        | A personal layer was registered, unregistered, patched, restored, or erased | Registry |
| `admin.granted`                | An admin grant was added or revoked                                | Registry |
| `visibility.denied`            | A call was rejected because the requested resource was not visible | Registry |
| `freeze.break_glass`           | An admin used break-glass during a freeze window                   | Registry |
| `user.erased`                  | Admin invoked the GDPR erasure command                             | Registry |
| `registry.read_only_entered`   | Registry entered read-only mode (Postgres primary unreachable)     | Registry |
| `registry.read_only_exited`    | Registry exited read-only mode (Postgres primary restored)         | Registry |

Audit lives in two streams. The registry owns the events above. The MCP server can also write a local audit log for the meta-tool events through a `LocalAuditSink` interface (§9) when configured. Both streams share trace IDs.

**Caller identity in audit events.** Read events (`domain.loaded`, `domains.searched`, `artifacts.searched`, `artifact.loaded`) record the caller's identity from the OAuth token: typically `caller.identity = "<sub-claim>"`, with email and groups attached. In public-mode deployments (§13.10), the OAuth flow is skipped and these events instead record `caller.identity = "system:public"`, with the source IP address and any upstream `X-Forwarded-User` header preserved in `caller.network`. Public-mode events also carry the flag `caller.public_mode: true` so downstream consumers (SIEM, audit dashboards) can filter them without parsing identity strings.

**Tenant in audit events.** A registry audit event that concerns one tenant records that tenant's org ID (§4.7.1) as `tenant`. The records that carry an org's ID form that org's audit stream (§4.7.1, §6.3.1). An event about an ingest, a signature, or another operation on a tenant's layer or artifact records the tenant that owns that layer or artifact, even when a request triggered it. Any other event raised by a request records the tenant §6.3.1 selects for the request, and a single-tenant registry records its sole tenant. On a multi-tenant registry, an event from a request that resolves to no provisioned tenant records no tenant. An event about the registry as a whole records no tenant. This covers audit-chain maintenance events such as `audit.anchored` and `audit.anchor_failed` (§8.6), registry mode events such as `registry.read_only_entered` and `registry.read_only_exited`, and `vector.outbox_lagging` (§4.7). An event an operator raises under the operator grant also records no tenant, because that grant is scoped to no tenant (§4.7.1). `tenant` is part of the hashed event body (§8.6), and an empty `tenant` adds nothing to that body.

## 8.2 PII Redaction

Two redaction surfaces:

- **Manifest-declared.** Artifact manifests can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`). The registry honors redaction directives; the MCP server applies the same directives before writing to its local audit sink.
- **Query text.** Free-text `search_artifacts` and `search_domains` queries are regex-scrubbed for common PII patterns (SSN, credit-card, email, phone) before being written to audit. Patterns configurable via `PIIRedactionConfig`. Default-on.

## 8.3 Audit Sinks

The registry has its own sink for catalogue events. The local file log, when enabled via `PODIUM_AUDIT_SINK`, is written by the MCP server through the `LocalAuditSink` interface. The local sink defaults to `~/.podium/audit.log` (user-wide; one file across all workspaces). Operators who need per-project scoping point `PODIUM_AUDIT_SINK` at a workspace path such as `${WORKSPACE}/.podium/audit.log`. Both the registry and local sinks can be redirected to external SIEM / log aggregation independently.

## 8.4 Retention

Defaults, configurable per deployment:

| Data                                | Retention                                               |
| ----------------------------------- | ------------------------------------------------------- |
| Audit events (metadata)             | 1 year                                                  |
| Query text                          | 30 days (redacted to placeholders after 7 days)         |
| Deprecated artifact versions        | 90 days after the deprecation flag is set               |
| Layers unregistered by their owners | 30 days (artifacts soft-deleted, recoverable via admin) |

Optional sampling for high-volume low-sensitivity events (e.g., `domain.loaded` at 10% sample) reduces storage cost.

## 8.5 Erasure

```
podium admin erase <user_id>
podium admin erase <user_id> --local --audit-path <file> --operator <admin-id> --salt <salt>
```

The first form calls the registry. The second form is the offline erasure form: it rewrites the audit file at `<file>` directly without calling the registry, reaches every record in that file whatever its tenant, and records `<admin-id>` as the invoking admin on a `user.erased` event that names no tenant.

- Unregisters and purges any user-defined layers owned by the user (and the artifacts ingested from them).
- Redacts the user identity (replaces it with `redacted-<sha256(user_id+salt)>`) in the requesting tenant's audit records. On a multi-tenant registry, these are the records whose `tenant` (§8.1) is the tenant §6.3.1 selects for the request. Alias discovery reads only those records. On a single-tenant registry, these are the records whose `tenant` is the sole tenant and the records that name no tenant.
- Preserves audit event sequencing for integrity.
- On a multi-tenant registry, erasure acts in the tenant §6.3.1 selects for the request. The admin check (§4.7.2) runs in that tenant, and only that tenant's layers are purged. Under `oidc-jwt`, a request whose verified organization names no provisioned tenant is rejected with `auth.tenant_unknown` (§6.3.1). Any other request that resolves to no tenant is refused with `403 auth.forbidden` before the admin check, before any layer is read, and before any audit record is rewritten.
- On a multi-tenant registry, erasure leaves records that name no tenant (§8.1) unchanged, and the response carries no information about them, so a tenant admin cannot learn whether the user appears in records outside the tenant. An operator redacts them with the offline erasure form shown above, run against the registry's file sink (`PODIUM_AUDIT_LOG_PATH`, §13.12) while the registry is stopped. That form reaches every record in the file whatever its tenant, and every tombstone it writes uses the salt the operator supplies.

Use this command for GDPR right-to-erasure. Erasure is itself logged as a `user.erased` event that names the requesting tenant. The event is recorded whether the registry sink is a file or an external endpoint, and with an external endpoint the registry rewrites no record and the receiving system owns redaction of the shipped stream.

## 8.6 Audit Integrity

Every audit event carries a hash chain: `event_hash = sha256(event_body || prev_event_hash)`. Detection of gaps is automated and alerted.

Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended for high-assurance deployments. SIEM mirroring is the operational integrity backstop.

**Local chain-head anchoring.** When anchoring is enabled (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, §13.12) and the audit sink is a file, the registry periodically signs `sha256:<chain head>` with a dedicated Ed25519 anchor key (`PODIUM_AUDIT_SIGNING_KEY_PATH`, §13.12) and appends the result as an `audit.anchored` event. When anchoring is enabled and the file audit sink cannot be opened, the registry refuses to start (§13.12), because no chain exists to anchor. It anchors again immediately after a §8.4 retention pass moves the head or a §8.5 erasure rewrites the chain. A failed periodic attempt is recorded as `audit.anchor_failed`, and a failed attempt after a retention pass or an erasure is logged, and the next periodic anchor signs the current head. The `audit.retention_enforced` and `user.erased` events that close a rewrite record the chain head the rewrite superseded as `superseded_head`, so a verifier holding an anchor of that head can reconcile it with the rewritten log. Local anchoring submits nothing to a transparency log and is distinct from the transparency-log anchoring recommended above. An auditor verifies an anchor with the anchor key's public half. The anchor signature covers the digest bytes alone, with no label that distinguishes a chain-head signature from a §4.7.9 artifact signature. The anchor key and the registry signing key set must therefore be distinct, and while registry signing is on, the registry refuses to start when they share a key (§13.12). Podium does not add a purpose label. The accepted residual risk is a verifier outside the registry that trusts one key for both purposes.
