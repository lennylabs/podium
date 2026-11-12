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

**Client in audit events.** An event the registry raises while serving a request that names its client (§7.2.2) records the normalized value as `caller.client`. The value is an object whose members `kind`, `name`, `version`, `host`, `host_version`, and `harness` each appear only when non-empty. Read events and the events of write and administrative requests, including `user.erased`, record it. An event the ingest pipeline raises (`artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`) records no `caller.client`, even when a request triggered the ingest, and neither does an event raised without a request. An event about the registry as a whole, such as `audit.anchored` (§8.6), records no `caller.client`, including the `audit.anchored` the registry appends after a §8.5 erasure while it serves the erase request. `caller.client` records what the client reported. It is not an identity attribute, and §7.2.2 lists the inputs it never feeds. The MCP server's local events (§8.3) record the `caller.client` value the MCP server builds for the call under §7.2.2, which is the value it sends to the registry when the call reaches the registry. They also record it when the call sends no registry request: a load served from the workspace overlay (§6.4), or from the content cache without revalidation in the `offline-first` or `offline-only` cache mode (§6.5). `caller.client` is part of the hashed event body (§8.6), and an absent `caller.client` adds nothing to that body.

**Record format.** Every destination (§8.3) carries an audit record as one JSON object with these members: `type`, the event type; `timestamp`, an RFC 3339 time in UTC with up to nine fractional-second digits and trailing zeros omitted; `trace_id`; `caller`, an object with `identity`, `email`, `groups`, `network`, `public_mode`, and `client`, where `network` is an object with `source_ip` and `forwarded_user` and `client` is the object the "Client in audit events" paragraph defines; `target`; `context`, an object whose values are strings; `tenant`; `resolved_layers`; `result_size`; `schema_version`; `hash`; and `prev_hash`. A member with an empty, false, or zero value is omitted, except `type`, `timestamp`, and `hash`, which every record carries. `schema_version` is `1` for the format this paragraph defines, and a change to the record's members or their meaning increments it. A new event type does not change the format. `schema_version` is part of the hashed event body (§8.6), and a record that lacks it adds nothing to that body, so a record written before the member existed keeps its hash and an existing log still verifies. Podium publishes a JSON Schema for the record, versioned by `schema_version`. A record without `schema_version` predates that schema and is outside it.

`hash` identifies a record, so a receiver that gets a record more than once keeps one copy per `hash`. A receiver detects a missing record as a non-empty `prev_hash` that matches no `hash` it has received. That is the check the registry applies to its file destination (§8.6), and §8.6 lists the records whose `prev_hash` a receiver cannot resolve although no record was lost.

## 8.2 PII Redaction

Two redaction surfaces:

- **Manifest-declared.** Artifact manifests can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`). The registry honors redaction directives; the MCP server applies the same directives before writing to its local audit sink.
- **Query text.** Free-text `search_artifacts` and `search_domains` queries are regex-scrubbed for common PII patterns (SSN, credit-card, email, phone) before being written to audit. Patterns configurable via `PIIRedactionConfig`. Default-on.

## 8.3 Audit Sinks

The registry writes its stream of catalogue events through the `RegistryAuditSink` interface. The local stream, when enabled via `PODIUM_AUDIT_SINK` (§6.2), is written by the MCP server through the `LocalAuditSink` interface. The local stream defaults to the file `~/.podium/audit.log` (user-wide; one file across all workspaces). Operators who need per-project scoping point `PODIUM_AUDIT_SINK` at a workspace path such as `${WORKSPACE}/.podium/audit.log`.

**Destinations.** `PODIUM_AUDIT_LOG_PATH` (§13.12) lists the registry stream's destinations, and `PODIUM_AUDIT_SINK` (§6.2) lists the local stream's, independently. Each is a comma-separated list, and whitespace around an element is ignored. An element is one of the following:

- A file path, the file destination. Each record is one line of JSON.
- An `http://` or `https://` URL, the HTTP destination. Records are sent in `POST` requests whose body has the media type `application/x-ndjson` and holds one record per line.
- An `otlp+http://` or `otlp+https://` URL, the OTLP destination. Records are sent as OTLP/HTTP protobuf log export requests to the URL with the `otlp+` prefix removed, and to the path `/v1/logs` when that URL's path is empty or `/`.
- The token `stdout`, the stdout destination. Each record is one line of JSON on the process's standard output. Only the registry accepts it, because the MCP server's standard output carries the MCP protocol.

A list names at most one file destination and names no element twice. An empty element, an element whose `scheme://` prefix is none of the above, and an element with one of the above prefixes that does not parse as a URL refuse startup (§13.12). A file path that contains a comma cannot be listed.

The §8.2 redaction directives and the §8.4 sampling rates apply before an event is chained, so every destination receives the same redacted record and a sampled-out event reaches no destination. The process then chains the event once (§8.6) and writes the chained record to every destination in its list. Every destination therefore carries the same member values for that event: the file, stdout, and HTTP destinations write the record's JSON, and the OTLP destination carries it as the log record body. A destination receives a process's records in the order the process chained them, apart from the repeats that delivery below allows.

Each process chains its own records. Registry replicas do not share a chain, and each MCP server process chains its own local records. When the list names a file destination, a starting process continues the chain from the last record in that file, which any process that writes the file may have appended. Otherwise the first record a process chains carries an empty `prev_hash`. A §8.4 or §8.5 rewrite replaces the file destination through a rename, so a deployment ships the stream through a stdout, HTTP, or OTLP destination listed beside the file rather than by tailing the file.

**Delivery.** The file and stdout destinations are written before the emitting operation continues, so a write that blocks, such as a write to a stalled reader of standard output, delays that operation. The registry keeps running when a write to standard output fails.

The HTTP and OTLP destinations are written asynchronously. The process places the record in that destination's queue and continues, and a background sender delivers it, so the emitting operation never waits for or fails because of an HTTP or OTLP receiver. The process places a record in every HTTP and OTLP queue before it writes that record to the stdout destination, so a record whose stdout write blocks is already queued, or counted as dropped, for each HTTP and OTLP destination. Each queue is bounded by `PODIUM_AUDIT_QUEUE_MAX_BYTES` (§13.12). The sender sends a batch when it holds 500 records or 1 MiB of serialized records, and at least once per second while records are queued and no retry delay (below) is pending. While a retry delay is pending, the sender sends nothing until the delay ends or the shutdown flush stops the sender. Each request times out after 10 seconds, and redirects are never followed. A response with a 2xx status delivers the batch. A 400, 413, or 422 response splits the batch in half and sends each half again. A single record that is still refused is dropped (Loss accounting). A network error and every other status, 3xx included, is retried after a delay drawn uniformly between zero and a cap. The cap starts at 1 second and doubles after each consecutive failure up to 60 seconds. A `Retry-After` header raises the delay to its value, up to 60 seconds, when that value is larger than the drawn delay, and never shortens the drawn delay.

A record leaves its queue only when it is delivered or counted as dropped, so a batch the sender has sent without a 2xx response stays queued and counts toward the bound. A persistent retryable failure drops nothing by itself: it holds the queue until `queue_full` drops begin, and the queue-depth gauge and a rate-limited log line report it. The queues live in process memory. A deployment that needs delivery to survive a process restart points the HTTP or OTLP destination at a local collector that keeps a persistent queue.

A process flushes its queues once while it ends, after it has stopped taking new work and its work in flight has finished or its shutdown deadline has passed. It flushes however it ends, including a refusal to start or a stop on an error after its queues exist, except on an abnormal exit. At that flush the process stops each background sender, then sends the whole queue in chain order, a batch the sender sent without a 2xx response included, for up to 5 seconds, and drops the rest with reason `shutdown`. When the flush ends, the process seals its stream. A record it chains before the seal takes effect, including one it is chaining at that moment, is accounted for under Loss accounting, and the process chains no record after the seal, so an operation still running after the seal records no further audit event. An abnormal exit is a crash, a signal that ends the process by its default action (SIGKILL included), or a forced termination such as a Windows `TerminateProcess` call. The MCP server ends on SIGINT or SIGTERM as §6.8 states, and its shutdown deadline for a tool call in progress is the arrival of that signal.

**Loss accounting.** When the list names a file destination, an event is chained only when its record is written to the file. When that write fails, the chain head does not advance, no other destination receives the record, the write's failure is reported to the operation that emitted the event, and the record is counted once, as dropped for the file destination with reason `write_error`. No other destination's failure is reported to the emitting operation. A §8.4 or §8.5 rewrite that fails leaves the file unchanged and emits no closing event. Every record the process chains is written to the stdout destination and delivered at least once to each HTTP and OTLP destination in the list, or it is counted as dropped for that destination with exactly one of these reasons:

| Reason | Condition |
| --- | --- |
| `write_error` | The write to standard output failed, or, for the file destination, the write that would have chained the record failed (above). |
| `rejected` | The record is larger than 1 MiB, the receiver refused it in a request that held it alone, or it is among the records an OTLP receiver's partial-success response reports as rejected. |
| `queue_full` | The record is at most 1 MiB and did not fit in the destination's queue when the process chained it, including a record larger than `PODIUM_AUDIT_QUEUE_MAX_BYTES`. |
| `shutdown` | The record was still queued when the shutdown flush ended, or the process chained it after the shutdown flush started. |

A loss is counted in every case except the following:

- **Abnormal exit.** When the process exits abnormally, as the Delivery shutdown paragraph defines it, a record it has chained but whose write to its stdout destination has not returned, such as a write blocked on a stalled reader, and the records still queued for an HTTP or OTLP destination are lost.
- **Rewrite by another process.** When another process also writes the file destination, such as several MCP server processes on the local file or `sign-stored-rows` beside a serving registry (§13.4), a record that a process appends after another process's §8.4 or §8.5 rewrite has read the file and before that rewrite's rename is lost from the file, and it stays at the appending process's other destinations. A process keeps its own chain head across another process's rewrite, so the next record it chains can carry a `prev_hash` that the file no longer holds, and §8.6 verification of the file reports that record as a gap whether or not a record was lost. When the appending process chains no later record, no check detects the loss.
- **Stored part of a failed write.** A write to the file destination that fails after it stored part of the record leaves that part in the file, and the failed record is counted with `write_error` (above). The next record written to the file joins that part in one line that cannot be parsed, so that record is missing from the file while it stays at the process's other destinations. §8.4 retention, §8.5 erasure, and §8.6 anchoring and verification of the file fail on that line until an operator removes the part.

A receiver keeps one copy per `hash` (§8.1), and a record dropped for a destination appears to that destination's receiver as a missing record (§8.1).

**Transport.** The HTTP and OTLP destinations of a process take their request headers, CA bundle, and client certificate from the §13.12 audit transport settings. A header value and URL userinfo are never logged, and an error or log line names a destination by its scheme, host, and path. A destination URL that carries userinfo refuses startup (§13.12).

**OTLP mapping.** The OTLP destination maps each record to one OpenTelemetry log record:

| Log record field | Value |
| --- | --- |
| `time_unix_nano`, `observed_time_unix_nano` | The record's `timestamp` |
| `severity_number`, `severity_text` | `9`, `INFO` |
| `event_name` | The record's `type` |
| `trace_id` | The record's `trace_id` decoded from hexadecimal, or empty when it is not 32 hexadecimal characters |
| `body` | A string holding the record's JSON, byte for byte as the file destination writes the line, without the line ending |
| Attributes | `podium.audit.schema_version` as an integer value, and `podium.audit.hash`, `podium.audit.prev_hash` when it is non-empty, and `podium.tenant` when the record names a tenant, each as a string value |

Caller identity stays in the body and is not copied into attributes. The resource carries `service.name`, which is the name the process uses for §13.8 tracing, and `service.instance.id`, a random identifier the process generates when it opens the stream. The instrumentation scope name is `github.com/lennylabs/podium/audit`. The OTLP destination reads no `OTEL_EXPORTER_OTLP_*` variable and is independent of `PODIUM_TRACING` (§13.8).

**Observability.** Every dropped record is counted per destination kind and reason, and the queued records of the HTTP and OTLP destinations are reported as a queue depth per destination kind (§13.8). Dropped records are also logged, whether or not metrics are enabled, in at most one line per destination and reason per minute. A log line names a destination by its file path, as `stdout`, or by its scheme, host, and path.

## 8.4 Retention

Defaults, configurable per deployment:

| Data                                | Retention                                               |
| ----------------------------------- | ------------------------------------------------------- |
| Audit events (metadata)             | 1 year                                                  |
| Query text                          | 30 days (redacted to placeholders after 7 days)         |
| Deprecated artifact versions        | 90 days after the deprecation flag is set               |
| Layers unregistered by their owners | 30 days (artifacts soft-deleted, recoverable via admin) |

Retention rewrites only the file destination (§8.3). The receiving system of a stdout, HTTP, or OTLP destination owns the retention of the records it received.

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
- On a multi-tenant registry, erasure leaves records that name no tenant (§8.1) unchanged, and the response carries no information about them, so a tenant admin cannot learn whether the user appears in records outside the tenant. An operator redacts them with the offline erasure form shown above, run against the registry stream's file destination (`PODIUM_AUDIT_LOG_PATH`, §13.12) while the registry is stopped. That form reaches every record in the file whatever its tenant, and every tombstone it writes uses the salt the operator supplies.

Use this command for GDPR right-to-erasure. Erasure is itself logged as a `user.erased` event that names the requesting tenant. Erasure through the registry rewrites the registry stream's file destination when its list names one, and every destination of that stream receives the `user.erased` event. When the list names no file destination, the registry rewrites no record and records only the `user.erased` event. The offline form rewrites only the file at `<file>`, and its `user.erased` event reaches no other destination. The registry cannot reach records already delivered to a stdout, HTTP, or OTLP destination, and the receiving system owns their redaction.

## 8.6 Audit Integrity

Every audit event carries a hash chain: `event_hash = sha256(event_body || prev_event_hash)`. Detection of gaps is automated and alerted. The registry verifies the registry stream's file destination on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule (§13.12): every non-empty `prev_hash` in the file must match the `hash` of an earlier record, so the file holds one chain per writing process and a deleted record that a later record links to breaks that link. A receiver of a stdout, HTTP, or OTLP destination applies the same check to the records it received (§8.1). Two kinds of record carry a `prev_hash` that a receiver cannot resolve although no record was lost. The first is the event that closes a §8.4 or §8.5 rewrite, which links to the rewritten file, and a receiver accepts it when it holds the record that the event's `superseded_head` names. The second is a record that continued the chain from the last record of a file destination, when its process started (§8.3), where the receiver never received that last record, such as a record that the offline §8.5 form appended or the event that closed another process's rewrite. A receiver cannot tell the second kind from a record that follows a lost record. The check cannot detect records lost after the last record a destination delivered. §8.3 Loss accounting states which losses the process counts and which it does not.

Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended for high-assurance deployments. SIEM mirroring is the operational integrity backstop.

**Local chain-head anchoring.** When anchoring is enabled (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, §13.12) and the registry stream lists a file destination (§8.3), the registry periodically signs `sha256:<chain head>` with a dedicated Ed25519 anchor key (`PODIUM_AUDIT_SIGNING_KEY_PATH`, §13.12) and appends the result as an `audit.anchored` event. A file destination that the registry cannot open refuses startup whatever the anchoring setting (§13.12), so anchoring never runs without a chain to anchor. It anchors again immediately after a §8.4 retention pass moves the head or a §8.5 erasure rewrites the chain. A failed periodic attempt is recorded as `audit.anchor_failed`, and a failed attempt after a retention pass or an erasure is logged, and the next periodic anchor signs the current head. Every rewrite closes with an event that records the chain head the rewrite superseded as `superseded_head`: a retention pass that drops an event or ages out query text appends `audit.retention_enforced`, and an erasure appends `user.erased`. A verifier holding an anchor of the superseded head can therefore reconcile it with the rewritten log, and a receiver of another destination can reconcile the closing event's `prev_hash`. Local anchoring submits nothing to a transparency log and is distinct from the transparency-log anchoring recommended above. An auditor verifies an anchor with the anchor key's public half. The anchor signature covers the digest bytes alone, with no label that distinguishes a chain-head signature from a §4.7.9 artifact signature. The anchor key and the registry signing key set must therefore be distinct, and while registry signing is on, the registry refuses to start when they share a key (§13.12). Podium does not add a purpose label. The accepted residual risk is a verifier outside the registry that trusts one key for both purposes.
