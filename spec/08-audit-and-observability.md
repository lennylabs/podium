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
| `layer.config_changed`         | Admin added, removed, or reordered admin-defined layers            | Registry |
| `layer.user_registered`        | A user registered or unregistered a personal layer                 | Registry |
| `admin.granted`                | An admin grant was added or revoked                                | Registry |
| `visibility.denied`            | A call was rejected because the requested resource was not visible | Registry |
| `freeze.break_glass`           | An admin used break-glass during a freeze window                   | Registry |
| `user.erased`                  | Admin invoked the GDPR erasure command                             | Registry |
| `registry.read_only_entered`   | Registry entered read-only mode (Postgres primary unreachable)     | Registry |
| `registry.read_only_exited`    | Registry exited read-only mode (Postgres primary restored)         | Registry |

Audit lives in two streams. The registry owns the events above. The MCP server can also write a local audit log for the meta-tool events through a `LocalAuditSink` interface (§9) when configured. Both streams share trace IDs.

**Caller identity in audit events.** Read events (`domain.loaded`, `domains.searched`, `artifacts.searched`, `artifact.loaded`) record the caller's identity from the OAuth token: typically `caller.identity = "<sub-claim>"`, with email and groups attached. In public-mode deployments (§13.10), the OAuth flow is skipped and these events instead record `caller.identity = "system:public"`, with the source IP address and any upstream `X-Forwarded-User` header preserved in `caller.network`. Public-mode events also carry the flag `caller.public_mode: true` so downstream consumers (SIEM, audit dashboards) can filter them without parsing identity strings.

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
| Vulnerability scan history          | 1 year                                                  |

Optional sampling for high-volume low-sensitivity events (e.g., `domain.loaded` at 10% sample) reduces storage cost.

## 8.5 Erasure

```
podium admin erase <user_id>
```

- Unregisters and purges any user-defined layers owned by the user (and the artifacts ingested from them).
- Redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`).
- Preserves audit event sequencing for integrity.

Use this command for GDPR right-to-erasure. Erasure is itself logged as a `user.erased` event.

## 8.6 Audit Integrity

Every audit event carries a hash chain: `event_hash = sha256(event_body || prev_event_hash)`. Detection of gaps is automated and alerted.

Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended for high-assurance deployments. SIEM mirroring is the operational integrity backstop.
