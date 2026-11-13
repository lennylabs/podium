---
title: Audit events
nav_order: 5
description: The audit record format, the file, HTTP, OTLP, and stdout destinations, delivery and loss accounting, receiver checks, and the OTLP and ECS field mappings.
---

# Audit events

The registry and `podium-mcp` each write their own audit stream. Each process chains every event into a hash chain once and writes the chained record to every destination its configuration lists. This page describes the record, the destinations, how records are delivered and counted when they are lost, and how a receiving system checks the stream it collects.

---

## Record format

Every destination carries a record as one JSON object with the following members.

| Member | Content |
|:--|:--|
| `type` | The event type, such as `artifact.loaded` or `layer.ingested`. |
| `timestamp` | The event time in RFC 3339, in UTC, with up to nine fractional-second digits and trailing zeros omitted. |
| `trace_id` | The W3C trace ID of the request that raised the event. |
| `caller` | An object with `identity`, `email`, `groups`, `network`, `public_mode`, and `client`. `network` holds `source_ip` and `forwarded_user` and appears on public-mode events. `client` holds `kind`, `name`, `version`, `host`, `host_version`, and `harness`, as described in [Client software](#client-software). |
| `target` | The artifact, domain, layer, or user the event concerns. |
| `context` | A map of event-specific string values, such as `version` on `artifact.loaded` or `superseded_head` on a closing event. |
| `tenant` | The org ID of the tenant the event concerns. |
| `resolved_layers` | The caller's effective layer composition on a read event. |
| `result_size` | The number of results a read event returned. |
| `schema_version` | The version of this record format. The current version is `1`. |
| `hash` | The SHA-256 hash of the record's body chained to `prev_hash`. |
| `prev_hash` | The `hash` of the record before this one in the same chain. |

`type`, `timestamp`, and `hash` are always present. Every other member is omitted when its value is empty, false, or zero, so the first record of a chain carries no `prev_hash`.

```json
{"type":"artifact.loaded","timestamp":"2026-11-11T14:03:22.418273Z","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","caller":{"identity":"alice","email":"alice@acme.com","groups":["platform"],"client":{"kind":"mcp","name":"podium-mcp","version":"0.6.0","host":"claude-code","host_version":"2.1.0","harness":"claude-code"}},"target":"platform/deploy-runbook","context":{"version":"1.2.0"},"tenant":"acme","resolved_layers":["org-defaults","team-platform"],"result_size":1,"schema_version":1,"hash":"9f2c4e7a1b8d3f60c5e2a9d4b7f1038e6c2d5a9b4e7f1c3a8d6b2e0f5a9c7d41","prev_hash":"3e8b1d6f0a4c7e2b9d5f8a1c4e7b0d3f6a9c2e5b8d1f4a7c0e3b6d9f2a5c8e10"}
```

`schema_version` increments when the record's members or their meaning change. A new event type leaves it unchanged. The JSON Schema for version 1 is published at [`https://raw.githubusercontent.com/lennylabs/podium/main/pkg/audit/schema/audit-event.v1.schema.json`](https://raw.githubusercontent.com/lennylabs/podium/main/pkg/audit/schema/audit-event.v1.schema.json).

`schema_version` is part of the hashed body, and a record that lacks it adds nothing to that body. Records written before the release that added `schema_version` therefore lack the member, keep their hashes, and still verify. They predate the schema and fall outside it, so skip them when validating a file destination against the schema.

### Client software

`caller.client` names the client software that sent the request, as the client stated it in the `X-Podium-Client` request header. Each of its members `kind`, `name`, `version`, `host`, `host_version`, and `harness` appears only when it is non-empty. `kind` is `mcp`, `sync`, `cli`, `sdk`, or `other`.

The client reports `caller.client` itself, and the registry does not verify it. The registry never uses it for authentication, visibility, quotas, or tenant selection. The registry bounds and sanitizes the value and records the result as the client reported it. Neither the query-text PII scrub nor `podium admin erase` rewrites `caller.client`, so a value a client placed in a member stays in the record after an erasure.

Read events and the events of write and administrative requests, including `user.erased`, carry `caller.client` when the request named its client. The following events carry none:

- The events the ingest pipeline raises: `artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`. They carry no `caller.client` even when a reingest or webhook request triggered the ingest.
- Events raised without a request, such as the `user.erased` that `podium admin erase --local` writes.
- Events about the registry as a whole, such as `audit.anchored`. This includes the `audit.anchored` the registry appends after an erasure while it serves the erase request.

The `podium-mcp` local events carry the `caller.client` value the MCP server builds for the call, which is the value it sends to the registry. They also carry it when the call sends no registry request: a load served from the workspace overlay, or from the content cache without revalidation in the `offline-first` or `offline-only` cache mode. A cache hit that the MCP server revalidates with the registry, through a HEAD request under the default `always-revalidate` cache mode or a conditional GET answered with 304, also produces a registry `artifact.loaded` event with `caller.client`.

A record without `caller.client` keeps the hash it had before the member existed, so a log that mixes older and newer records verifies.

---

## Destinations

`PODIUM_AUDIT_LOG_PATH` lists the registry stream's destinations, and `PODIUM_AUDIT_SINK` lists the `podium-mcp` local stream's destinations. The two lists are independent. Each is a comma-separated list, and whitespace around an element is ignored. An element is one of the following:

| Element | Destination | What it writes |
|:--|:--|:--|
| A file path | File | One JSON line per record, appended to the file. |
| `http://` or `https://` URL | HTTP | `POST` requests whose body has the media type `application/x-ndjson` and holds one record per line. |
| `otlp+http://` or `otlp+https://` URL | OTLP | OTLP/HTTP protobuf log export requests, described in [OTLP](#otlp). |
| `stdout` | Stdout | One JSON line per record on the process's standard output. The registry accepts it, and `podium-mcp` refuses it, because its standard output carries the MCP protocol. See [Stdout](#stdout). |

An unset or empty `PODIUM_AUDIT_LOG_PATH` resolves to the file `~/.podium/audit.log` under the home directory of the registry process. An unset `PODIUM_AUDIT_SINK` disables the local stream. `PODIUM_AUDIT_SINK` set to an empty value, or to the token `default`, names the file `~/.podium/audit.log`.

A list names at most one file destination and names no element twice. A file path that contains a comma cannot be listed, because the comma separates elements and the list has no escape syntax. Examples:

```text
PODIUM_AUDIT_LOG_PATH=/var/lib/podium/audit.log,otlp+https://collector.acme.com:4318
PODIUM_AUDIT_LOG_PATH=stdout
PODIUM_AUDIT_SINK=default,https://audit.acme.com/ingest
```

A list that these rules do not admit refuses startup with `config.audit_sink_unavailable`. The causes are listed in the [error code catalog](error-codes#config).

Manifest-declared redaction and the query-text PII scrub apply before an event is chained, and so does per-event-type sampling. Every destination therefore receives the same redacted record, and a sampled-out event reaches no destination.

The file destination is storage that Podium owns: retention, erasure, anchoring, and verification act on it, and retention and erasure rewrite it by replacing the file through a rename. A deployment ships the stream to another system through a stdout, HTTP, or OTLP destination listed beside the file, rather than by tailing the file.

---

## Delivery

The file and stdout destinations are written before the operation that raised the event continues, so a write that blocks, such as a write to a stalled reader of standard output, delays that operation. The registry keeps running when a write to standard output fails.

The HTTP and OTLP destinations are written asynchronously. The process places the record in the destination's queue and continues, and a background sender delivers it, so an operation never waits for or fails because of an HTTP or OTLP receiver. The process places a record in every HTTP and OTLP queue before it writes that record to the stdout destination, so a record whose stdout write blocks is already queued, or counted as dropped, for each HTTP and OTLP destination. A destination receives a process's records in the order the process chained them, apart from the repeats that retries cause.

The sender follows these rules:

- Each queue is bounded by `PODIUM_AUDIT_QUEUE_MAX_BYTES` bytes of serialized records, 64 MiB by default.
- The sender sends a batch when it holds 500 records or 1 MiB of serialized records, and at least once per second while records are queued and no retry delay is pending. While a retry delay is pending, the sender sends nothing until the delay ends or the shutdown flush stops the sender.
- Each request times out after 10 seconds, and redirects are never followed.
- A response with a 2xx status delivers the batch.
- A 400, 413, or 422 response splits the batch in half, and the sender sends each half again. A single record that is still refused is dropped with reason `rejected`.
- A network error and every other status, 3xx included, is retried after a delay drawn uniformly between zero and a cap. The cap starts at 1 second and doubles after each consecutive failure up to 60 seconds. A `Retry-After` header raises the delay to its value, up to 60 seconds, when that value is larger than the drawn delay, and never shortens the drawn delay.

A record leaves its queue only when it is delivered or counted as dropped, so a batch the sender has sent without a 2xx response stays queued and counts toward the bound. A persistent retryable failure drops nothing by itself: it holds the queue until `queue_full` drops begin, and the queue-depth gauge and a rate-limited log line report it.

The transport and queue settings are process-wide and apply to every HTTP and OTLP destination of the process. `PODIUM_AUDIT_HEADERS_FILE` names a file of `Name: value` request headers, one per line, and an `Authorization: Basic` line supplies HTTP Basic authentication. `PODIUM_AUDIT_TLS_CA_FILE` names a PEM CA bundle that replaces the system trust store. `PODIUM_AUDIT_TLS_CERT_FILE` and `PODIUM_AUDIT_TLS_KEY_FILE` name the PEM client certificate and its private key for mutual TLS, and are set together. `PODIUM_AUDIT_QUEUE_MAX_BYTES` sets the queue bound. Secrets come from files only, and a destination URL that carries userinfo refuses startup. The registry reads these variables, and `podium-mcp` reads them only when `PODIUM_AUDIT_SINK` is set. The Helm chart sets the file variables from the Secret that `audit.transportSecret` names. A header value and URL userinfo are never logged, and a log line or an error names a destination by its scheme, host, and path. The [CLI reference](cli#environment-variables) rows give each variable's refusals.

### Metrics and drop logging

Two series report delivery. The registry exports them at `/metrics`, and `podium-mcp` exports them on the metrics address that `PODIUM_MCP_METRICS_ADDR` names.

| Series | Type | Content |
|:--|:--|:--|
| `podium_audit_queue_depth{destination}` | Gauge | Records queued for the HTTP or OTLP destination. `destination` is `http` or `otlp`. |
| `podium_audit_dropped_total{destination,reason}` | Counter | Records dropped for a destination. `destination` is `file`, `stdout`, `http`, or `otlp`, and `reason` is `queue_full`, `rejected`, `shutdown`, or `write_error`. |

Each series is exported once a destination first reports a queued or dropped record, so a process whose list names only a file destination exports neither series until a file write fails.

Dropped records are also logged, whether or not metrics are enabled, in at most one line per destination and reason per minute. A log line names a destination by its file path, as `stdout`, or by its scheme, host, and path.

### Loss accounting

When the list names a file destination, an event is chained only when its record is written to the file. When that write fails, the chain head does not advance, no other destination receives the record, the write's failure is reported to the operation that raised the event, and the record is counted once, as dropped for the file destination with reason `write_error`. No other destination's failure is reported to the operation that raised the event. A retention or erasure rewrite that fails leaves the file unchanged and emits no closing event. Every record the process chains is written to the stdout destination and delivered at least once to each HTTP and OTLP destination in the list, or it is counted as dropped for that destination with exactly one of these reasons:

| Reason | Condition |
|:--|:--|
| `write_error` | The write to standard output failed, or, for the file destination, the write that would have chained the record failed. |
| `rejected` | The record is larger than 1 MiB, the receiver refused it in a request that held it alone, or it is among the records an OTLP receiver's partial-success response reports as rejected. |
| `queue_full` | The record is at most 1 MiB and did not fit in the destination's queue when the process chained it, including a record larger than `PODIUM_AUDIT_QUEUE_MAX_BYTES`. |
| `shutdown` | The record was still queued when the shutdown flush ended, or the process chained it after the shutdown flush started. |

A loss is counted in every case except the following:

- **Abnormal exit.** When the process exits abnormally, as [Shutdown](#shutdown) defines it, a record it has chained but whose write to its stdout destination has not returned, such as a write blocked on a stalled reader, and the records still queued for an HTTP or OTLP destination are lost.
- **Rewrite by another process.** When another process also writes the file destination, a record that a process appends after another process's retention or erasure rewrite has read the file and before that rewrite's rename is lost from the file, and it stays at the appending process's other destinations. A process keeps its own chain head across another process's rewrite, so the next record it chains can carry a `prev_hash` that the file no longer holds, and verification of the file reports that record as a gap whether or not a record was lost. When the appending process chains no later record, no check detects the loss. Files with several writers include the `podium-mcp` local file, which every `podium-mcp` process with the same `PODIUM_AUDIT_SINK` file appends to, and the registry's file while `podium-server sign-stored-rows` runs beside a serving registry, because that command appends its `artifact.signed` records to the same `PODIUM_AUDIT_LOG_PATH` file. The offline `podium admin erase --local` and `podium admin retention` forms are writers of the same kind when they rewrite a file that a running registry or `podium-mcp` process appends to.
- **Stored part of a failed write.** A write to the file destination that fails after it stored part of the record leaves that part in the file, and the failed record is counted with `write_error`. The next record written to the file joins that part in one line that cannot be parsed, so that record is missing from the file while it stays at the process's other destinations. Retention, erasure, anchoring, and verification of the file fail on that line until an operator removes the part.

A receiver keeps one copy per `hash`, and a record dropped for a destination appears to that destination's receiver as a missing record (see [Receiver checks](#receiver-checks)).

### Shutdown

A process flushes its queues once while it ends, after it has stopped taking new work and its work in flight has finished or its shutdown deadline has passed. It flushes however it ends, including a refusal to start or a stop on an error after its queues exist, except on an abnormal exit. At that flush the process stops each background sender, then sends the whole queue in chain order, a batch the sender sent without a 2xx response included, for up to 5 seconds, and drops the rest with reason `shutdown`. When the flush ends, the process seals its stream. A record it chains before the seal takes effect, including one it is chaining at that moment, is accounted for under [Loss accounting](#loss-accounting), and the process chains no record after the seal, so an operation still running after the seal records no further audit event.

An abnormal exit is a crash, a signal that ends the process by its default action (SIGKILL included), or a forced termination such as a Windows `TerminateProcess` call.

Each process handles signals as follows:

- **Registry.** The first SIGINT or SIGTERM starts the shutdown: the registry stops accepting connections, drains the requests in flight for up to 15 seconds, and then flushes. A second SIGINT or SIGTERM ends the process by the signal's default action, which is an abnormal exit.
- **`podium-mcp`.** The host ends the MCP server by closing its standard input or by sending SIGINT or SIGTERM. Once the server has finished starting, which it does before it reads its first request, it ends when its standard input reaches end-of-file and when it receives SIGINT or SIGTERM. A SIGINT or SIGTERM that arrives while the server is starting ends it by the signal's default action, before it has dispatched any request or written any audit record. On either signal after startup it dispatches no further request and does not wait for a tool call in progress. In each case it then flushes its local audit stream, when `PODIUM_AUDIT_SINK` enables one, and exits with status 0. A SIGINT or SIGTERM that arrives during the flush does not shorten it. SIGKILL and other forced terminations end the server without the flush. On Windows, a host that closes standard input and waits for the server to exit gets the flush, because a `TerminateProcess` call skips it.
- **`podium-server sign-stored-rows` and the offline `podium admin` forms.** These commands handle no signal, so SIGINT or SIGTERM ends them by the default action, which is an abnormal exit.

---

## Durability

The HTTP and OTLP queues live in process memory, and Podium keeps no spool on disk. Records still queued when a process exits abnormally are lost, and records still queued when the shutdown flush ends are dropped, as [Shutdown](#shutdown) states. A deployment that needs delivery to survive a process restart points the HTTP or OTLP destination at a local collector that keeps a persistent queue.

The following OpenTelemetry Collector configuration receives the OTLP destination on port 4318 and forwards the records through a sending queue that the `file_storage` extension keeps on disk. Set the registry's list to `otlp+http://localhost:4318`, or add that element to an existing list.

```yaml
extensions:
  file_storage:
    directory: /var/lib/otelcol/queue

receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318

exporters:
  otlphttp:
    endpoint: https://logs.acme.com
    sending_queue:
      enabled: true
      storage: file_storage

service:
  extensions: [file_storage]
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [otlphttp]
```

Elasticsearch, S3, Splunk, and Loki are reached through the collector's own exporters, which the [OpenTelemetry Collector documentation](https://opentelemetry.io/docs/collector/) lists. A Vector disk buffer and a Fluent Bit filesystem buffer provide the same restart durability for the HTTP destination.

---

## Receiver checks

A receiving system checks the stream it collects with the following rules.

- **Deduplication.** `hash` identifies a record. A retried batch can deliver a record more than once, so a receiver keeps one copy per `hash`.
- **Gap check.** A receiver detects a missing record as a non-empty `prev_hash` that matches no `hash` it has received. The registry applies the same check to its file destination on the `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` schedule. Each process chains its own records, so a receiver that collects several registry replicas or several `podium-mcp` processes holds one chain per process.
- **Closing events.** Every retention or erasure rewrite of a file destination closes with an event that records the chain head the rewrite superseded as `context.superseded_head`: a retention pass that drops an event or ages out query text appends `audit.retention_enforced`, and an erasure appends `user.erased`. The closing event's `prev_hash` links to the rewritten file, so a receiver of another destination cannot resolve it. The receiver accepts it when it holds the record whose `hash` equals `superseded_head`.
- **Chain continued from a file.** When the list names a file destination, a starting process continues the chain from the last record in that file, which any process that writes the file may have appended. When the receiver never received that last record, such as a record the offline `podium admin erase --local` form appended, the event that closed another process's rewrite, or any record in the file when an HTTP or OTLP destination is added to a list that already names the file, the first record the process delivers carries an unresolvable `prev_hash` although no record was lost. A receiver cannot tell this case from a record that follows a lost record. Without a file destination, the first record a process chains carries no `prev_hash`.
- **End of a process's records.** The check cannot detect records lost after the last record a destination delivered. [Loss accounting](#loss-accounting) states which losses the process counts and which it does not.

---

## OTLP

The OTLP destination sends OTLP/HTTP protobuf log export requests to the URL with the `otlp+` prefix removed. When that URL's path is empty or `/`, the requests go to the path `/v1/logs`, so `otlp+https://collector.acme.com:4318` sends to `https://collector.acme.com:4318/v1/logs`. Each record maps to one OpenTelemetry log record:

| Log record field | Value |
|:--|:--|
| `time_unix_nano`, `observed_time_unix_nano` | The record's `timestamp`. |
| `severity_number`, `severity_text` | `9`, `INFO`. |
| `event_name` | The record's `type`. |
| `trace_id` | The record's `trace_id` decoded from hexadecimal, or empty when it is not 32 hexadecimal characters. |
| `body` | A string holding the record's JSON, byte for byte as the file destination writes the line, without the line ending. |
| Attributes | `podium.audit.schema_version` as an integer value, and `podium.audit.hash`, `podium.audit.prev_hash` when it is non-empty, and `podium.tenant` when the record names a tenant, each as a string value. |

Caller identity stays in the body and is not copied into attributes. A receiver recomputes `hash` from the body. The resource carries `service.name`, which is `podium-registry` for the registry and `podium-mcp` for the MCP server, and `service.instance.id`, a random identifier the process generates when it opens the stream. The instrumentation scope name is `github.com/lennylabs/podium/audit`.

The OTLP destination reads no `OTEL_EXPORTER_OTLP_*` variable and runs independently of `PODIUM_TRACING`, so audit export works with tracing off and leaves span export unchanged.

---

## Stdout

The stdout destination is available to the registry only. It writes each record as one JSON line on the registry's standard output, before the operation that raised the event continues. A reader of standard output that stalls therefore delays audited requests, and it holds the registry's exit until the blocked write returns. The registry places each record in every HTTP and OTLP queue before it writes the record to standard output, so a record whose stdout write blocks is already queued, or counted as dropped, for each HTTP and OTLP destination.

When the list names `stdout`, the registry ignores SIGPIPE on platforms that have it. A reader that goes away then makes the write fail with a broken-pipe error, which is counted as dropped with reason `write_error`, and the registry keeps serving.

A list that names `stdout` refuses startup with `config.audit_sink_unavailable` while `PODIUM_TRACING=stdout` also writes spans to standard output, because a span and an audit record could interleave within one line.

`podium-server sign-stored-rows` prints its `plan:`, `dry-run:`, and `rehash:` summary lines on standard output, so with `stdout` listed its standard output carries those lines beside the `artifact.signed` audit lines. Each audit record stays one complete JSON line that begins with `{`, and a collector that parses JSON separates the two.

---

## Retention and erasure

Retention and erasure rewrite only the file destination. The receiving system of a stdout, HTTP, or OTLP destination owns the retention and redaction of the records it received, and that includes records a tailer shipped from the file.

An erasure through the registry rewrites the registry stream's file destination when its list names one, and every destination of that stream receives the `user.erased` event. When the list names no file destination, such as the Helm chart's `stdout` default, the registry rewrites no record and records only the `user.erased` event, and the receiving system redacts the records already delivered to it. The offline `podium admin erase --local` form rewrites only the file it names, and its `user.erased` event reaches no other destination.

With a list that names no file destination, the registry also runs no local chain-head anchoring and no scheduled verification. The receiving system applies the [gap check](#receiver-checks) to the shipped stream.

---

## ECS mapping

A receiver that stores records under the Elastic Common Schema maps the members as follows. Podium documents no OCSF mapping.

| Audit member | ECS field |
|:--|:--|
| `type` | `event.action` |
| `timestamp` | `@timestamp` |
| `trace_id` | `trace.id` |
| `caller.identity` | `user.id` |
| `caller.email` | `user.email` |
| `caller.groups` | `user.group.name` |
| `caller.network.source_ip` | `source.ip` |
| `tenant` | `organization.id` |
| `target` | `event.reference` |
| `hash` | `event.id` |
