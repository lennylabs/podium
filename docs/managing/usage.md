---
title: Seeing how the catalog is used
nav_order: 2
description: Answer usage questions from the audit records, such as which artifacts are loaded and how often, which go unused, which harnesses and client versions call the registry, and which searches return nothing.
---

# Seeing how the catalog is used

The registry records every discovery and load call as an audit event. This page covers the events that describe use, where the records are read, how to answer common questions from them, how to audit changes to the catalog itself, and the privacy controls that apply to the records.

> [!IMPORTANT]
> Podium has no built-in usage report. No command, endpoint, or web view reports how often an artifact is loaded or how often a query is searched. The answers on this page come from the audit records, read in the system that receives them.

## Who can do this

| Task | Access |
|:--|:--|
| Read the audit records | Whoever the operator grants access to an audit destination. The registry serves no endpoint that returns audit records. |
| Configure audit destinations, sampling, and retention | Operator, through environment variables on the registry and on `podium-mcp`. |
| Erase a user's identity from the records | Per-tenant `admin` role for `podium admin erase --registry`. The `--local` form needs file access to the audit log. |
| Follow catalog changes as they happen | Any caller, through the change-event stream. A tenant admin registers outbound webhook receivers. |

---

## Events that record use

The registry records one event for each meta-tool call it serves, whether an agent made the call through `podium-mcp`, a program made it through an SDK, or `podium sync` made it while materializing a workspace from a server.

| Event | Recorded when | Members that describe the call |
|:--|:--|:--|
| `artifact.loaded` | A caller loaded an artifact through `load_artifact`. A server-source `podium sync` loads every artifact of the caller's effective view this way on each run and applies the workspace's scope after the loads. | `target` holds the artifact ID. On a load that resolves, `context` holds `version`, `content_hash`, and `layer`, and `result_size` is `1`. |
| `artifacts.searched` | A caller invoked `search_artifacts`. | `context` holds `query`, `scope`, and `type`, and `result_size` holds the number of results returned. |
| `domain.loaded` | A caller invoked `load_domain`. | `target` holds the domain path, and `result_size` holds the number of subdomains and notable entries returned. |
| `domains.searched` | A caller invoked `search_domains`. | `context` holds `query` and `scope`, and `result_size` holds the number of domains returned. |

Every record also carries `timestamp`, `tenant`, `resolved_layers` (the caller's effective layer composition), and a `caller` object:

- `caller.identity`, `caller.email`, and `caller.groups` identify the caller.
- `caller.client` names the client software, with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`. `kind` is `mcp`, `sync`, `cli`, `sdk`, or `other`, so a load by an agent and a load by `podium sync` are told apart by `caller.client.kind`.

A member whose value is empty or zero is omitted. A load that does not resolve, because the artifact does not exist or the caller cannot see it, therefore carries no `result_size`, and neither does a search that returned nothing or failed.

The client reports `caller.client` itself, and the registry does not verify it. A request that sends no client header produces a record without the member. [Record format](../reference/audit-events#record-format) and [Client software](../reference/audit-events#client-software) define every member.

### Loads the registry does not see

`podium-mcp` keeps a content cache. Under the default `always-revalidate` cache mode, a cache hit revalidates with the registry, and the registry records `artifact.loaded`. Under the `offline-first` and `offline-only` cache modes, a cache hit can be served without a registry request, and a load served from a workspace overlay never reaches the registry. Only the `podium-mcp` local stream records those loads.

The local stream is off until the operator sets `PODIUM_AUDIT_SINK` for `podium-mcp`. It records `artifact.loaded`, `artifacts.searched`, `domain.loaded`, and `domains.searched` for the calls the MCP server serves, with the same `caller.client` value the server sends to the registry. Its records name the MCP session in `caller.identity` and carry no `result_size`. [Record local audit from podium-mcp](../deployment/operations/observability#record-local-audit-from-podium-mcp) describes the setup.

A filesystem-source `podium sync` reads the catalog from disk and records no audit event.

---

## Where to read the records

The operator lists the registry stream's destinations in `PODIUM_AUDIT_LOG_PATH`. Every destination receives the same JSON record:

| Destination | Where the records are |
|:--|:--|
| File | One JSON line per record, in `~/.podium/audit.log` under the registry process's home directory when the variable is unset. |
| HTTP receiver | NDJSON batches posted to the receiver, one record per line. |
| OpenTelemetry Collector | One log record per audit record, with the record's JSON as the log body. |
| Standard output | One JSON line per record. The Helm chart writes here by default, so the records land in the container log. |

Each registry replica writes its own stream. A question about the whole catalog needs the records of every replica, so answer it in a log pipeline or receiver that collects the streams of all replicas. [Audit destinations](../deployment/operations/observability#audit-destinations) covers configuring each destination, and [Destinations](../reference/audit-events#destinations) gives the list rules.

The examples below use `jq` over a file destination named `audit.log`. A receiver that stores the records with their member names, such as a log search over the OTLP body or the NDJSON lines, takes the same member paths in its own query language.

Run the examples against the registry stream. The `podium-mcp` local stream carries no `result_size`, and an example that tests `result_size` reads every local event as an unresolved call.

## Answer common questions

### Which artifacts are loaded, and how often

Count resolved loads per artifact:

```text
jq -r 'select(.type == "artifact.loaded" and .result_size == 1) | .target' audit.log | sort | uniq -c | sort -rn
```

To count only agent loads, add `and .caller.client.kind == "mcp"` to the `select`. A server-source `podium sync` loads every artifact the caller can see on each run, whatever the workspace's scope, so its loads measure what syncs fetch rather than what agents use.

To see who loads one artifact, select it by ID:

```text
jq -r 'select(.type == "artifact.loaded" and .result_size == 1 and .target == "finance/ap/pay-invoice") | .caller.identity' audit.log | sort | uniq -c | sort -rn
```

### Which artifacts are never loaded

Compare the catalog with the set of loaded IDs. Write the catalog's artifact IDs to `catalog.txt`, one per line, for example from the `ids` member of [`GET /v1/catalog`](../reference/http-api#catalog), which lists the artifacts the caller can see. Then list the IDs with no resolved load:

```text
jq -r 'select(.type == "artifact.loaded" and .result_size == 1) | .target' audit.log | sort -u > loaded.txt
sort -u catalog.txt | comm -23 - loaded.txt
```

The result covers the period the records span. Retention drops records older than its window, 365 days by default on the file destination, and a sampled event type undercounts, so an artifact that the result lists was not loaded within the retained and sampled records.

### Which harnesses and client versions are in use

Group resolved loads by client:

```text
jq -r 'select(.type == "artifact.loaded" and .result_size == 1) | .caller.client // {} | [.kind, .name, .version, .harness] | map(. // "-") | @tsv' audit.log | sort | uniq -c | sort -rn
```

A `-` marks a member the client did not report, and a row of `-` values counts the requests that named no client.

### Which searches return nothing

List the queries of artifact searches that returned no result:

```text
jq -r 'select(.type == "artifacts.searched" and .result_size == null and (.context.query // "") != "" and .context.query != "[redacted]") | .context.query' audit.log | sort | uniq -c | sort -rn
```

Replace `artifacts.searched` with `domains.searched` for domain searches. A search with an empty query lists the catalog, and the filter leaves it out. The filter also leaves out a query that retention replaced with `[redacted]` or removed, because its text no longer names what the caller searched for. A search that failed, such as one with an invalid `top_k`, also carries no `result_size` and appears in the list. Query text is scrubbed of common PII patterns before it is recorded, and on the file destination retention replaces it with a placeholder after 7 days and removes it after 30 days, so the list holds recent queries only. A query that returns nothing points at a missing artifact, a description that does not use the words callers search with, or a domain whose `keywords` miss them.

---

## Audit changes to the catalog

The same stream records changes to the catalog and to who can reach it:

| Event | Recorded when |
|:--|:--|
| `artifact.published` | A new `(artifact_id, version)` was ingested. `context` holds `version`, `content_hash`, and `layer`. |
| `artifact.deprecated` | An ingested version set `deprecated: true` while the artifact had a version that was not deprecated. |
| `domain.published` | A `DOMAIN.md` was added or changed. |
| `layer.ingested`, `layer.history_rewritten` | A layer completed an ingest cycle, or a force-push was detected on a `git` layer. |
| `layer.config_changed`, `layer.user_registered` | An admin-defined layer or a personal layer was added, removed, restored, or patched, or the layer order changed. |
| `admin.granted`, `admin.visibility_override` | An admin grant was added or revoked, or an admin read a layer with a visibility override. |
| `visibility.denied` | A call was rejected because the requested resource was not visible. |
| `freeze.break_glass` | A caller with reingest rights on the layer used break-glass during a freeze window. |
| `user.erased` | An admin ran the erasure command. |

[Event types](../reference/audit-events#event-types) lists every event. The events the ingest pipeline raises carry no `caller.client`, even when a reingest request triggered the ingest. To list the versions published in the retained period:

```text
jq -r 'select(.type == "artifact.published") | [.timestamp, .target, .context.version, .context.layer] | @tsv' audit.log
```

To follow changes as they happen, read the change-event stream, [`GET /v1/events`](../reference/http-api#events-stream), which delivers `artifact.published`, `artifact.deprecated`, `layer.config_changed`, and the other change events the caller can see. A tenant admin can also register an [outbound webhook](../reference/http-api#outbound-webhooks) receiver, which receives every matching event of the tenant.

---

## Privacy

The registry applies the following controls before it chains an event, so every destination receives the same record:

- **Manifest-declared redaction.** An artifact's `audit_redact` field names frontmatter fields whose values the registry replaces with `[redacted]` in records about that artifact. `podium-mcp` applies the same directive to its local stream.
- **Query-text scrub.** The registry scrubs common PII patterns, such as email addresses, phone numbers, and card numbers, from search query text. `pii_redaction` in `registry.yaml` adds patterns or turns the scrub off.
- **Sampling.** `PODIUM_AUDIT_SAMPLE_RATES` keeps a fraction of each listed event type, such as `domain.loaded=0.1`. A sampled-out event reaches no destination, so a count over a sampled type is divided by its rate to estimate the total.

Retention and erasure act on the file destination alone. The system that receives a stdout, HTTP, or OTLP destination owns the retention and redaction of the records it holds.

- **Retention.** The registry drops file records older than `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS`, 365 by default, and ages out query text after 7 and 30 days. `podium admin retention` applies per-event-type windows to a file.
- **Erasure.** `podium admin erase` redacts a user's identity in the file destination's records and records `user.erased`. It does not rewrite `caller.client`, so a value a client placed in that member stays in the record.

[Redaction and sampling](../deployment/operations/observability#redaction-and-sampling) and [Retention and erasure](../deployment/operations/observability#retention-and-erasure) give the settings, and [`podium admin erase`](../reference/cli#podium-admin-erase) gives the command's forms.
