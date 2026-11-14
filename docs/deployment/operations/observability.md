---
title: Audit and observability
nav_order: 1
description: Configure where Podium writes its audit records, scrape its Prometheus metrics, export OpenTelemetry traces, send operational notifications, and set up alerts.
---

# Audit and observability

This page describes how to get audit records, metrics, traces, and operational notifications out of a Podium registry and out of the `podium-mcp` server. It is for operators of a [single-node](../modes/single-node) or [clustered](../modes/clustered) deployment. The [local](../modes/local) deployment mode runs no registry process, so only the `podium-mcp` settings on this page apply to it.

The following table summarizes each signal, the setting that turns it on, and the section that covers it. [Alerting](#alerting) gives a starting set of Prometheus alert rules over these signals.

| Signal | Produced by | Setting | Default | Section |
|:--|:--|:--|:--|:--|
| Audit records | Registry | `PODIUM_AUDIT_LOG_PATH` | The file `~/.podium/audit.log` | [Audit log](#audit-log), [Audit destinations](#audit-destinations) |
| Audit records | `podium-mcp` | `PODIUM_AUDIT_SINK` | Off | [Record local audit from podium-mcp](#record-local-audit-from-podium-mcp) |
| Prometheus metrics | Registry | `PODIUM_METRICS` | On, at `/metrics` | [Registry metrics](#registry-metrics) |
| Prometheus metrics | `podium-mcp` | `PODIUM_MCP_METRICS_ADDR` | Off | [podium-mcp metrics](#podium-mcp-metrics) |
| Traces | Registry and `podium-mcp` | `PODIUM_TRACING` or `OTEL_EXPORTER_OTLP_ENDPOINT` | Off | [Tracing](#tracing) |
| Notifications | Registry | `PODIUM_NOTIFICATION_PROVIDER` | Off | [Notifications](#notifications) |

The [audit events reference](../../reference/audit-events) holds the record format, the event types, and the exact delivery and loss-accounting rules. This page covers configuration.

---

## Audit log

The registry and `podium-mcp` each write their own audit stream. The registry records every meta-tool read, every ingest outcome, every layer, grant, and tenant change, its read-only mode transitions, and its own audit-chain maintenance. `podium-mcp` records the meta-tool calls it serves when `PODIUM_AUDIT_SINK` enables its local stream. Records from both streams carry the W3C trace ID of the request, so a trace and its audit records can be joined. [Event types](../../reference/audit-events#event-types) lists every event.

Each record is one JSON object. It names the event type, the time, the caller, the target, and the tenant, as [Record format](../../reference/audit-events#record-format) describes.

### Hash chain

Every record carries a `hash` and a `prev_hash`. The `hash` is the SHA-256 digest of the record's body chained to the `hash` of the record before it, and `prev_hash` holds that earlier `hash`. A deleted record that a later record links to therefore leaves a gap that a verifier detects.

Each process chains its own records. Registry replicas do not share a chain, and each `podium-mcp` process chains its own local records. A receiver that collects several processes holds one chain per process.

The registry checks the chain on a schedule when its stream lists a file destination:

- **Verification.** The registry verifies the file every `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` seconds, 3600 by default. `0` disables it. A gap logs the line `audit integrity ALERT` and records an `audit.gap_detected` event.
- **Local chain-head anchoring.** When `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above `0`, the registry signs the chain head at that interval with a dedicated Ed25519 anchor key and appends an `audit.anchored` event. A failed periodic attempt records `audit.anchor_failed`. The default is `0`, which disables anchoring.
- **Anchor key.** `PODIUM_AUDIT_SIGNING_KEY_PATH` names the anchor key file, `~/.podium/standalone/audit.key` by default. The registry generates the file when anchoring runs and the file is absent. The anchor key must be a separate keypair from the registry signing key. Keep its public half, because an auditor needs it to check the anchors.

Local anchoring submits nothing to a public transparency log. When the stream lists no file destination, the registry runs neither verification nor anchoring, and the receiving system applies the [gap check](../../reference/audit-events#receiver-checks) to the records it collects.

### Redaction and sampling

The registry applies redaction and sampling before it chains an event, so every destination receives the same record:

- **Manifest-declared redaction.** An artifact manifest can name fields to redact from audit records. `podium-mcp` applies the same directives to its local stream.
- **Query-text scrub.** The free text of `search_artifacts` and `search_domains` queries is scrubbed of common PII patterns (social security numbers, credit-card numbers, email addresses, and phone numbers) before it is recorded.
- **Sampling.** `PODIUM_AUDIT_SAMPLE_RATES` takes comma-separated `<event-type>=<rate>` pairs, with each rate between `0` and `1`. For example, `domain.loaded=0.1` keeps one `domain.loaded` event in ten. A sampled-out event reaches no destination. An entry that does not parse is ignored.

### Retention and erasure

Retention and erasure act on the file destination only. The system that receives a stdout, HTTP, or OTLP destination owns the retention and redaction of the records it holds.

- **Scheduled retention.** The registry runs a retention pass at start and then every `PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS` seconds, 86400 (one day) by default. The pass drops events older than `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS`, 365 by default. Query text is replaced with a placeholder after 7 days and removed after 30 days. Setting either variable to `0` disables the scheduled pass.
- **Offline retention.** [`podium admin retention`](../../reference/cli#podium-admin-retention) runs a pass against a file with explicit policies.
- **Erasure.** [`podium admin erase`](../../reference/cli#podium-admin-erase) redacts a user's identity for a right-to-erasure request and records a `user.erased` event.

A retention pass or an erasure rewrites the file by renaming a new file over the old one, and closes with an event that records the superseded chain head. [Retention and erasure](../../reference/audit-events#retention-and-erasure) gives the rules for each destination.

---

## Audit destinations

A destination is a place an audit stream writes to. Each process chains every record once and writes the same chained record to every destination in its list, so every destination carries the same records with the same `hash` values. A receiver can verify the copy it holds without access to the other destinations.

Two variables hold the destination lists:

| Variable | Stream | Unset |
|:--|:--|:--|
| `PODIUM_AUDIT_LOG_PATH` | The registry's audit stream. | The file `~/.podium/audit.log` under the registry process's home directory. |
| `PODIUM_AUDIT_SINK` | The `podium-mcp` local stream. | No local stream. |

Each variable takes a comma-separated list. Each element names one destination:

| Element | Destination | Behavior |
|:--|:--|:--|
| A file path | File | Appends one JSON line per record before the audited operation continues. Retention, erasure, anchoring, and verification act on it. |
| `http://` or `https://` URL | HTTP | Sends batches of records as NDJSON `POST` requests from an in-memory queue, retried with backoff. |
| `otlp+http://` or `otlp+https://` URL | OTLP | Sends OTLP/HTTP log export requests, such as an OpenTelemetry Collector accepts, from an in-memory queue. |
| `stdout` | Stdout | Writes one JSON line per record to standard output. The registry accepts it, and `podium-mcp` refuses it. |

The following lists are valid:

```text
PODIUM_AUDIT_LOG_PATH=/var/lib/podium/audit.log,otlp+https://collector.acme.com:4318
PODIUM_AUDIT_LOG_PATH=stdout
PODIUM_AUDIT_SINK=default,https://audit.acme.com/ingest
```

A list names at most one file destination and names no element twice. An empty element, a trailing comma, an unknown `scheme://` prefix, and a URL that carries userinfo each refuse startup with `config.audit_sink_unavailable`. [Destinations](../../reference/audit-events#destinations) gives the complete list rules.

To ship the stream to another system, list a stdout, HTTP, or OTLP destination beside the file.

> [!WARNING]
> Do not tail the audit file to ship it. Retention and erasure replace the file through a rename, so a tailer re-ships or skips records, and the records it already shipped keep their unredacted identities.

### Write to a file

The file destination is the default for the registry. It is the destination that retention, erasure, anchoring, and scheduled verification act on.

1. Choose a path on persistent storage that the registry process can create and append to.
2. Set `PODIUM_AUDIT_LOG_PATH` to that path, alone or with other destinations in the list.
3. Restart the registry.

A file the registry cannot open, or cannot open for appending, refuses startup even when it is the only destination, because the registry would otherwise run with no audit stream.

### Send records to an HTTP receiver

The HTTP destination fits a SIEM or log service that accepts newline-delimited JSON over HTTP, and a local Vector or Fluent Bit agent.

Before you start, confirm that the receiver:

- Accepts `POST` requests whose body has the media type `application/x-ndjson`, with one record per line.
- Answers a 2xx status for a batch it accepted.
- Answers 400, 413, or 422 for a batch it refuses. Podium then splits the batch in half and resends each half, and drops a single record the receiver still refuses with reason `rejected`.
- Keeps one copy of each record per `hash`, because a retried batch can deliver a record twice.

To add the destination:

1. Write each request header the receiver needs to a headers file, one `Name: value` header per line. Lines that begin with `#` and blank lines are ignored. An `Authorization: Basic` line supplies HTTP Basic authentication.

   ```text
   # /etc/podium/audit/headers
   Authorization: Bearer <receiver-token>
   ```

2. Set `PODIUM_AUDIT_HEADERS_FILE` to the path of that file.
3. When the receiver's certificate is issued by a private CA, set `PODIUM_AUDIT_TLS_CA_FILE` to a PEM bundle of that CA. The bundle replaces the system trust store.
4. When the receiver requires mutual TLS, set `PODIUM_AUDIT_TLS_CERT_FILE` and `PODIUM_AUDIT_TLS_KEY_FILE` together to the PEM client certificate and its private key.
5. Add the receiver's URL to the destination list:

   ```text
   PODIUM_AUDIT_LOG_PATH=/var/lib/podium/audit.log,https://audit.acme.com/ingest
   ```

6. Restart the registry. A setting the registry cannot use refuses startup with `config.audit_sink_unavailable`, as [Troubleshooting](#troubleshooting) describes.
7. Check delivery with the `podium_audit_queue_depth{destination="http"}` and `podium_audit_dropped_total{destination="http"}` series under [Metrics](#metrics).

Secrets come from files only. A credential in the URL refuses startup, and no log line or error includes a header value or URL userinfo. The transport settings apply to every HTTP and OTLP destination of the process.

### Send records to an OpenTelemetry Collector

The OTLP destination sends OTLP/HTTP protobuf log export requests. Elasticsearch, S3, Splunk, and Loki are reached through the collector's own exporters, which the [OpenTelemetry Collector documentation](https://opentelemetry.io/docs/collector/) lists.

1. Run a collector with the `otlp` receiver's `http` protocol enabled, on port 4318 by default.
2. Add the collector's URL to the destination list with the `otlp+` prefix, for example `otlp+http://localhost:4318`. When the URL's path is empty or `/`, Podium sends to the path `/v1/logs`.
3. Set the transport variables from [Send records to an HTTP receiver](#send-records-to-an-http-receiver) when the collector requires headers or TLS.
4. Restart the registry.
5. Confirm that log records arrive with the `service.name` resource attribute `podium-registry`, or `podium-mcp` for the local stream.

Each audit record becomes one log record. Its `body` holds the record's JSON exactly as the file destination writes the line, so a verifier can parse it as a file line. [OTLP](../../reference/audit-events#otlp) lists the field mapping.

The OTLP destination reads no `OTEL_EXPORTER_OTLP_*` variable and runs independently of `PODIUM_TRACING`. Audit export works with tracing off, and it leaves span export unchanged.

### Keep records across restarts

The HTTP and OTLP queues live in process memory, and Podium keeps no spool on disk. Records still queued when a process exits abnormally are lost. To make delivery survive a restart, point the HTTP or OTLP destination at a local collector that keeps a persistent queue.

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

A Vector disk buffer and a Fluent Bit filesystem buffer provide the same restart durability for the HTTP destination.

### Write to standard output

The stdout destination writes each record as one JSON line on the registry's standard output, for a container log pipeline. Only the registry accepts it, because the standard output of `podium-mcp` carries the MCP protocol.

- The registry writes the line before the audited operation continues. A reader of standard output that stalls therefore delays audited requests.
- The registry ignores SIGPIPE while `stdout` is listed. A reader that goes away makes the write fail, the record is counted as dropped with reason `write_error`, and the registry keeps serving.
- `stdout` cannot be combined with `PODIUM_TRACING=stdout`, because a span and an audit record could interleave within one line. The combination refuses startup with `config.audit_sink_unavailable`.

[Stdout](../../reference/audit-events#stdout) describes the ordering between the stdout write and the other destinations.

### Configure the Helm chart

The Helm chart sets `config.auditLogPath`, which renders `PODIUM_AUDIT_LOG_PATH`, to `stdout` by default. Each registry pod then writes its audit records to the container log, where the cluster's log pipeline holds them under the platform's retention and outside the reach of erasure.

To change the default, set `config.auditLogPath` to one of the following, or to a comma-separated list that joins one of them to `stdout`:

- A file path on a writable volume, to keep a file destination.
- An `http(s)` URL, to send records to an HTTP receiver.
- An `otlp+http(s)` URL, to send records to a collector.

With the `stdout` default, or any list without a file destination, the registry runs no local chain-head anchoring and no scheduled verification. The receiving system then applies the `prev_hash` check to the shipped stream, as [Receiver checks](../../reference/audit-events#receiver-checks) describes.

For the HTTP and OTLP transport files, set `audit.transportSecret.name` to a Secret and list its keys in `audit.transportSecret.keys`. The chart mounts the Secret at `/etc/podium/audit` and sets the variable for each listed key:

| Key | Variable |
|:--|:--|
| `headers` | `PODIUM_AUDIT_HEADERS_FILE` |
| `ca.crt` | `PODIUM_AUDIT_TLS_CA_FILE` |
| `tls.crt` | `PODIUM_AUDIT_TLS_CERT_FILE` |
| `tls.key` | `PODIUM_AUDIT_TLS_KEY_FILE` |

List `tls.crt` and `tls.key` together or not at all. [Helm chart reference](../modes/helm-chart) covers the chart values in context.

### Record local audit from podium-mcp

`podium-mcp` writes a separate local stream of the meta-tool calls it serves. The stream is off until `PODIUM_AUDIT_SINK` is set. The `--audit-sink` flag and the `audit-sink` config-file key take the same list.

| `PODIUM_AUDIT_SINK` value | Result |
|:--|:--|
| Unset | No local stream. The registry still records each call that reaches it. |
| Empty, or `default` | The file `~/.podium/audit.log`. |
| A list | The listed file, HTTP, and OTLP destinations. `default` can appear in the list. |

The default file is user-wide, with one file across all workspaces, and every `podium-mcp` process on the machine appends to it. For per-project scoping, point the variable at a workspace path such as `${WORKSPACE}/.podium/audit.log`.

`podium-mcp` reads the transport and queue variables only when `PODIUM_AUDIT_SINK` is set. It refuses `stdout` with `config.audit_sink_unavailable`. Its delivery metrics are served on the address that `PODIUM_MCP_METRICS_ADDR` names.

### Queue bound and dropped records

Each HTTP and OTLP destination has its own in-memory queue. An audited operation never waits for, or fails because of, an HTTP or OTLP receiver.

- `PODIUM_AUDIT_QUEUE_MAX_BYTES` bounds each queue in bytes of serialized records. The default is 67108864 (64 MiB).
- The sender sends a batch at 500 records or 1 MiB, and at least once per second while records are queued.
- A receiver that refuses or does not answer holds records in the queue. Once the queue is full, new records are dropped with reason `queue_full`.

Every record that does not reach a destination is counted in `podium_audit_dropped_total{destination,reason}` with one of these reasons:

| Reason | Meaning |
|:--|:--|
| `write_error` | A write to standard output failed, or the file write that would have chained the record failed. |
| `rejected` | The record is larger than 1 MiB, or the receiver refused it on its own. |
| `queue_full` | The record did not fit in the destination's queue. |
| `shutdown` | The record was still queued when the shutdown flush ended. |

Dropped records are also logged, whether or not metrics are enabled, in at most one line per destination and reason per minute. A failed file write is also reported to the operation that raised the event. [Loss accounting](../../reference/audit-events#loss-accounting) gives the exact conditions and the cases that are not counted.

### Shutdown flush

A process flushes its HTTP and OTLP queues once while it ends. The flush sends the queued records in chain order for up to 5 seconds and drops the rest with reason `shutdown`.

- **Registry.** The first SIGINT or SIGTERM stops new connections, drains requests in flight for up to 15 seconds, and then flushes. A second SIGINT or SIGTERM ends the process at once, and the records still queued are lost without being counted.
- **`podium-mcp`.** The server flushes when its standard input closes and when it receives SIGINT or SIGTERM, and then exits with status 0.

SIGKILL, a crash, and other forced terminations skip the flush. [Shutdown](../../reference/audit-events#shutdown) gives the complete rules.

---

## Metrics

The registry serves Prometheus metrics at `/metrics` by default. Set `PODIUM_METRICS=false` to remove the endpoint.

`podium-mcp` is a stdio process with no listener of its own. It serves metrics only when `PODIUM_MCP_METRICS_ADDR`, the `--metrics-addr` flag, or the `metrics-addr` config-file key names a bind address, such as `127.0.0.1:9090`.

The repository holds a reference Grafana dashboard at `deploy/grafana-dashboard.json`.

### Registry metrics

| Metric | Type | Description |
|:--|:--|:--|
| `podium_request_duration_seconds{endpoint}` | Histogram | Latency per endpoint. |
| `podium_request_total{endpoint}` | Counter | Requests per endpoint. |
| `podium_request_errors_total{endpoint}` | Counter | Responses with a status at or above 400, per endpoint. |
| `podium_visibility_denied_total` | Counter | Reads rejected by visibility filtering. |
| `podium_cache_hits_total`, `podium_cache_misses_total` | Counter | Hits and misses on the cache of `DOMAIN.md` import-glob expansions. |
| `podium_ingest_success_total`, `podium_ingest_failure_total` | Counter | Ingest attempts that succeeded or failed. |
| `podium_vector_outbox_depth` | Gauge | Pending rows in the external-vector-backend outbox. |
| `podium_audit_queue_depth{destination}` | Gauge | Audit records queued for the `http` or `otlp` destination. |
| `podium_audit_dropped_total{destination,reason}` | Counter | Audit records dropped for the `file`, `stdout`, `http`, or `otlp` destination. |

The meta-tool endpoints carry these latency targets at p99:

| `endpoint` label | p99 target |
|:--|:--|
| `load_domain`, `search_domains`, `search_artifacts` | Under 200 ms each. |
| `load_artifact` | Under 500 ms for the manifest alone, and under 2 s when bundled resources are fetched on a cache miss. |
| `batch_load` | No target. The label covers the bulk route `/v1/artifacts:batchLoad`. |

Read the metrics as follows:

- **Error rate.** The per-endpoint error rate is `rate(podium_request_errors_total) / rate(podium_request_total)`.
- **Visibility denials.** The counter is informational. A sudden spike usually means a layer configuration error rather than an authorization issue.
- **Cache hit ratio.** The cache key combines the include and exclude patterns with a fingerprint of the caller's visible artifact-ID set. The ratio falls when ingest changes that set frequently, when callers see different subsets of the catalog, or when a domain's globs vary from request to request.
- **Ingest failures.** Flag a recent rise in `podium_ingest_failure_total`.
- **Vector outbox.** A rising depth means the drain worker is falling behind. The gauge reads 0 on a collocated backend, which uses no outbox.
- **Audit queue.** A depth that keeps rising means the receiver is refusing or not answering. `queue_full` drops follow once the queue reaches `PODIUM_AUDIT_QUEUE_MAX_BYTES`.

Both audit series export no series until a destination first reports a queued or dropped record. A registry whose list names only a file destination never exports `podium_audit_queue_depth`, and exports `podium_audit_dropped_total` only after a file write fails.

### podium-mcp metrics

| Metric | Type | Description |
|:--|:--|:--|
| `podium_mcp_requests_total{tool}` | Counter | Calls per meta-tool. |
| `podium_mcp_request_errors_total{tool}` | Counter | Calls per meta-tool that returned an error. |
| `podium_mcp_request_duration_seconds{tool}` | Histogram | Latency per meta-tool. |
| `podium_cache_hits_total`, `podium_cache_misses_total` | Counter | Hits and misses on the server's resolution cache. |
| `podium_audit_queue_depth{destination}`, `podium_audit_dropped_total{destination,reason}` | Gauge, Counter | Delivery of the local audit stream, with the same labels and reasons as the registry series. |

The audit series export nothing until a network destination of the local stream is first used or a file write fails.

---

## Tracing

The registry and `podium-mcp` export OpenTelemetry traces over OTLP/HTTP. Tracing is off by default.

| `PODIUM_TRACING` | Result |
|:--|:--|
| Unset | Off, unless `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set, which turns on OTLP export. |
| `otlp` | Exports spans over OTLP/HTTP. |
| `stdout` | Writes spans to standard output for local inspection. |
| `off`, `none`, or `false` | Off, even when an `OTEL_EXPORTER_OTLP_*` endpoint is set. |

Any other value refuses startup with an error naming `PODIUM_TRACING`.

To export traces to a collector:

1. Set `PODIUM_TRACING=otlp`.
2. Set the standard OpenTelemetry exporter variables, such as `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`. The exporter reads the standard `OTEL_EXPORTER_OTLP_*` environment.

   ```text
   PODIUM_TRACING=otlp
   OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability:4318
   ```

3. Restart the registry, and restart each `podium-mcp` that should export.
4. Confirm that spans arrive under the service name `podium-registry` or `podium-mcp`.

Each meta-tool call opens one root span, with child spans for the registry round trip, the object-storage fetch, adapter translation, and materialization. The registry opens a server span for each request, named by the operation.

### Trace context propagation

Both processes always install the W3C Trace Context propagator, even when export is off. `podium-mcp` sends the `traceparent` header on its registry calls, and the registry continues that trace. The audit records of both streams carry the same trace ID in `trace_id`.

### What exported spans omit

No exported span carries a URL query component or fragment, the text of a `search_artifacts` or `search_domains` query, or a `session_id`. This applies to span names, attributes, events, links, and status descriptions.

- A span attribute that holds an `http` or `https` URL, such as `url.full`, shows the scheme, host, and path only. The signature of a presigned URL therefore stays out of the trace backend.
- A failed outbound call exports a status description that names the host and port and no URL.

---

## Notifications

The registry can send operational notifications to an operator through a notification provider. Notifications are off by default. `PODIUM_NOTIFICATION_PROVIDER` selects the provider.

| Value | Delivery |
|:--|:--|
| Unset, or `noop` | None. |
| `log` | One line per notification in the registry log. |
| `webhook` | A JSON `POST` to `PODIUM_NOTIFICATION_WEBHOOK_URL`. |
| `email`, or `smtp` | An email over SMTP. |
| `multi` | The log provider, plus the webhook provider when `PODIUM_NOTIFICATION_WEBHOOK_URL` is set, plus the email provider when `PODIUM_NOTIFICATION_SMTP_HOST` and `PODIUM_NOTIFICATION_SMTP_FROM` are set. |

The startup log names the wired provider in a `notification provider:` line. An unknown value, a `webhook` provider without a URL, and an `email` provider without a host or sender each log a warning at startup, and the registry runs with no notifier.

### Provider settings

| Variable | Provider | Meaning |
|:--|:--|:--|
| `PODIUM_NOTIFICATION_WEBHOOK_URL` | `webhook` | The URL that receives each notification. Required. |
| `PODIUM_NOTIFICATION_WEBHOOK_SECRET` | `webhook` | A key for an HMAC-SHA256 signature of the request body. When set, the `X-Podium-Signature` header carries `sha256=<hex digest>`. |
| `PODIUM_NOTIFICATION_SMTP_HOST` | `email` | The SMTP relay host. Required. |
| `PODIUM_NOTIFICATION_SMTP_PORT` | `email` | The relay port. Default `587`. |
| `PODIUM_NOTIFICATION_SMTP_FROM` | `email` | The sender address. Required. |
| `PODIUM_NOTIFICATION_SMTP_TO` | `email` | A comma-separated list of recipient addresses. |
| `PODIUM_NOTIFICATION_SMTP_USERNAME`, `PODIUM_NOTIFICATION_SMTP_PASSWORD` | `email` | Credentials for PLAIN authentication, used when both are set. |

The webhook body is a JSON object with the members `severity`, `title`, `body`, `tags`, and `time`. The email subject has the form `[podium <severity>] <title>`, and the message body carries the notification body followed by one `name: value` line per tag.

### What sends a notification

The registry sends a notification when a layer ingest fails, whether a manual reingest or an inbound Git webhook triggered it. The notification has severity `error`, the title `ingest failed for layer <layer-id>`, the ingest error as its body, and the tags `layer` and `tenant`. No other condition sends a notification in this release.

### Delivery

Delivery is best effort:

- Each provider makes one attempt per notification and does not retry.
- A failure, such as a webhook response at or above 400 or a refused SMTP submission, is logged as a `notification (<provider>):` line.
- A failed delivery does not change the outcome of the ingest. The reingest response carries the ingest error either way.
- The registry sends the notification before it answers the reingest request or the webhook delivery, so a slow notification target delays that response.
- The `multi` provider delivers to its providers concurrently. A failure in one does not stop the others.

Notifications are separate from the [outbound webhook receivers](outbound-webhooks#outbound-webhook-receivers) that carry catalog change events to tooling.

---

## Alerting

The following rules are a starting set, tuned for the baseline deployment that [Capacity planning](capacity#capacity-planning) describes:

```yaml
# Critical: page on-call
- alert: PodiumDown
  expr: up{job="podium-registry"} == 0
  for: 2m

- alert: PodiumLoadArtifactSLOBreached
  expr: histogram_quantile(0.99, rate(podium_request_duration_seconds_bucket{endpoint="load_artifact"}[5m])) > 0.5
  for: 5m

- alert: PodiumHighErrorRate
  expr: sum(rate(podium_request_errors_total[5m])) / sum(rate(podium_request_total[5m])) > 0.05
  for: 5m

# Warning: investigate within hours
- alert: PodiumIngestFailing
  expr: increase(podium_ingest_failure_total[1h]) > 5
  for: 15m

- alert: PodiumVectorOutboxBacklog
  expr: podium_vector_outbox_depth > 1000
  for: 10m

# Informational: review weekly
- alert: PodiumLowCacheHitRatio
  expr: sum(rate(podium_cache_hits_total[1h])) / (sum(rate(podium_cache_hits_total[1h])) + sum(rate(podium_cache_misses_total[1h]))) < 0.5
```

The chart at `deploy/helm/podium` does not carry alert rules. Add the rules to the deployment's own Prometheus rule set, and tune the thresholds to its SLOs.

For the audit stream, also alert on the following:

- Any increase in `podium_audit_dropped_total`.
- A `podium_audit_queue_depth` that keeps rising.
- The `audit integrity ALERT` log line and the `audit.gap_detected` event. Treat a gap as a security incident, as [When to escalate](troubleshooting#when-to-escalate) describes.
- The `audit.anchor_failed` event, when anchoring is on.

---

## Troubleshooting

### The process refuses to start with `config.audit_sink_unavailable`

The registry, `sign-stored-rows`, or `podium-mcp` cannot use its audit configuration. Every cause refuses startup whatever the anchoring setting. The error names the cause and the variable that holds the unusable setting. It names a destination by its scheme, host, and path, and it never includes a header value or URL userinfo.

| Cause | Fix |
|:--|:--|
| `PODIUM_AUDIT_LOG_PATH` holds an empty element, an element whose `scheme://` prefix is not `http`, `https`, `otlp+http`, or `otlp+https`, an element with one of those prefixes that does not parse as a URL, two file destinations, or an element named twice. | Correct the list. |
| A destination URL carries userinfo. | Move the credential from the URL into the `PODIUM_AUDIT_HEADERS_FILE` file. |
| `PODIUM_AUDIT_LOG_PATH` lists `stdout` while `PODIUM_TRACING=stdout` also writes spans to standard output. | Remove one of the two. |
| The registry cannot open the file destination: the path is a directory, a parent component is a regular file, the parent directory cannot be created, an existing log file cannot be read, or the home directory cannot be resolved for the default `~/.podium/audit.log`. A file the registry can read but cannot open for appending also refuses startup, even when it is the only destination. | Fix the path or its permissions. |
| `PODIUM_AUDIT_HEADERS_FILE` names a file that cannot be read, or the file holds a line without a colon, an empty name, or the name `Host`, `Content-Length`, or `Content-Type`. The error names the file and the line number. | Fix the file. |
| `PODIUM_AUDIT_TLS_CA_FILE` names a file that cannot be read or holds no certificate. | Fix the bundle. |
| Only one of `PODIUM_AUDIT_TLS_CERT_FILE` and `PODIUM_AUDIT_TLS_KEY_FILE` is set, or the pair cannot be loaded. | Set both to a matching pair, or neither. |
| `PODIUM_AUDIT_QUEUE_MAX_BYTES` is not empty and is not a positive base-10 integer. | Correct the value. |

`podium-mcp` refuses with the same code, naming `PODIUM_AUDIT_SINK` or the transport variable, when `PODIUM_AUDIT_SINK` lists `stdout`, names a list these rules do not admit, or names a file destination that cannot be opened or cannot be opened for appending. It also refuses when a transport or queue variable refuses its value while `PODIUM_AUDIT_SINK` is set.

### The registry refuses to start with `config.audit_anchor_key_unavailable`

This refusal applies only when `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` is above `0` and the registry stream lists a file destination.

- **Cause.** The key file at `PODIUM_AUDIT_SIGNING_KEY_PATH` exists and cannot be read, lacks a `private:` line, or does not decode, or the registry cannot generate it.
- **Fix.** Restore the file, or move it aside so the registry generates a new keypair. Keep the old public key, because anchors written before the change were signed with it and anyone who checks them needs it.

### The registry refuses to start with `config.audit_anchor_key_shared`

- **Cause.** The anchor key is also the registry signing key or one of its `verify:` keys. The anchor signature carries no purpose label, so an external verifier must not trust one key for both purposes.
- **Fix.** Generate a separate keypair for `PODIUM_AUDIT_SIGNING_KEY_PATH`.

### The audit queue keeps growing

- **Cause.** The HTTP or OTLP receiver refuses batches or does not answer. A persistent retryable failure drops nothing by itself. It holds the queue until `queue_full` drops begin, and a rate-limited log line reports it.
- **Fix.** Check the receiver's availability and the transport settings. Each request times out after 10 seconds and follows no redirect, so a receiver that answers with a 3xx status is retried until the queue fills.

---

## Related pages

- [Audit events](../../reference/audit-events): the record format, event types, and delivery rules.
- [Capacity and availability](capacity): the baseline deployment the alert thresholds assume, and read-only mode.
- [Troubleshooting](troubleshooting): symptoms and error codes across the registry, and when to escalate.
- [Configuration](../../reference/configuration#audit): every audit key and environment variable, with its default.
