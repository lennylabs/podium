---
title: Seeing how the catalog is used
nav_order: 2
description: Read the built-in usage report for loads, syncs, and search hits per artifact, per layer, and for the catalog, and answer further usage questions from the audit records.
---

# Seeing how the catalog is used

The registry keeps usage counts and serves them in a usage report: how often each artifact was loaded, synced, and returned by searches, per artifact, per layer, and for the catalog, broken down by client and harness and over time. The registry also records every discovery and load call as an audit event, which answers the questions the report does not, such as who loaded an artifact or which queries returned nothing.

This page covers the usage report, the events that describe use, where the records are read, how to answer common questions from them, how to audit changes to the catalog itself, and the privacy controls that apply to the records.

## Who can do this

| Task | Access |
|:--|:--|
| Read the usage report | Any caller that can search the registry. Every total covers only the layers the caller can see. |
| Query the stored usage counts directly | Whoever the operator grants read access to the metadata store. |
| Read the audit records | Whoever the operator grants access to an audit destination. The registry serves no endpoint that returns audit records. |
| Configure audit destinations, sampling, and retention | Operator, through environment variables on the registry and on `podium-mcp`. |
| Configure the usage counts' flush interval and retention | Operator, through environment variables on the registry. |
| Erase a user's identity from the records | Per-tenant `admin` role for `podium admin erase --registry`. The `--local` form needs file access to the audit log. |
| Follow catalog changes as they happen | Any caller, through the change-event stream. A tenant admin registers outbound webhook receivers. |

---

## Built-in usage report

The registry counts three things for each artifact, per UTC day, per layer, per client kind, and per harness:

- **Loads**: resolved `load_artifact` calls, including each item of a batch load, from a client other than `podium sync`. An agent's load through `podium-mcp` is a load. The registry counts each call it resolves, so one agent load can count twice: under the default `always-revalidate` cache mode, `podium-mcp` revalidates a cached artifact with a `HEAD` request and, when the content changed, fetches it with a `GET`, and a load repeated to replace an expired download URL counts again.
- **Syncs**: the same calls from `podium sync`. A server-source sync fetches every artifact the caller can see on each run, so syncs measure what workspaces fetch rather than what agents use.
- **Search hits**: searches with a query that returned the artifact among their first 10 results. A search that asks for fewer than 10 results counts each result it returns. A search with an empty query lists the catalog and counts nothing.

The layer is the layer that serves the artifact. The client kind and the harness come from the `X-Podium-Client` header that `podium-mcp`, `podium sync`, the CLI, and the SDKs send: `podium-mcp` reports its harness, or the harness a load names, and `podium sync` reports the harness of its run. A request without the header counts under `none`. The header is self-reported, so the split between loads and syncs and the harness breakdown are as reliable as the clients that call the registry.

The registry counts only the calls it serves. A load `podium-mcp` answers from its workspace overlay, or from its content cache under the `offline-first` and `offline-only` cache modes, and a filesystem-source `podium sync` do not reach the registry and are not counted. The counts carry no caller identity.

Counts are stored under the layer's ID. When an unregistered layer is purged at the end of its recovery window (`PODIUM_LAYER_RECOVERY_DAYS`, 30 days by default), its counts are deleted with it, including a personal layer that `podium admin erase` unregistered. A layer restored with `podium layer restore` within the window keeps its counts. A layer removed from the registry configuration, rather than unregistered, keeps its counts until retention deletes them, and a layer configured later under the same ID shows them.

### Read the report

`podium usage` prints the report for the last 30 days:

```text
podium usage --registry https://podium.acme.com
```

Narrow it with a window, layers, artifacts, and artifact types, and add a series for use over time:

```text
podium usage --registry https://podium.acme.com --from 2026-09-01 --to 2026-11-30 --granularity month
podium usage --registry https://podium.acme.com --layer team-finance --days 7 --granularity day
podium usage --registry https://podium.acme.com --type agent --days 30
podium usage --registry https://podium.acme.com --artifact finance/ap/pay-invoice --artifact finance/ap/submit-invoice --json
```

A catalog longer than one page ends the output with the `--cursor` value that lists the following artifacts:

```text
podium usage --registry https://podium.acme.com --limit 100
podium usage --registry https://podium.acme.com --limit 100 --cursor <next_cursor>
```

The command calls [`GET /v1/usage`](../reference/http-api#usage-report), which takes the same parameters. `POST /v1/usage` takes them as a JSON body, which suits a page that reads the counts of every artifact it lists. The response holds:

- `totals` for the catalog: every layer the caller can see. When the caller's token carries read scopes, such as `podium:read:finance/*`, every total in the response covers only the artifacts the scopes allow.
- `layers`: one entry per layer, in precedence order, including layers with no counts. A layer's counts include artifacts it no longer serves.
- `artifacts`: one entry per artifact the caller can see, including artifacts with no counts, ordered by loads, syncs, and search hits, with `last_loaded` and `last_synced` days. The list comes in pages of `limit` entries, 50 by default and at most 200. `artifact_count` gives the length of the whole list, and `next_cursor`, when it is not `null`, reads the next page.

Each total carries `loads`, `syncs`, and `search_hits`, a `by_client` breakdown by client kind, and a `by_harness` breakdown by harness. Under `granularity` `day`, `week`, or `month`, each total also carries a `series` of periods, where weeks run Monday to Sunday and the first and last periods are cut to the window. A long window coarsens the periods: a window of more than 366 days serves `day` as `week`, and a window of more than 1,096 days serves `day` and `week` as `month`, so a calendar year stays daily and three calendar years stay weekly. The response's `granularity` names the periods it holds.

An artifact with no loads in the window lists `loads` 0 and `last_loaded` `null`, so the report answers which artifacts go unused without a separate catalog listing.

### How current the report is

Each registry replica buffers its counts and adds them to the metadata store every `PODIUM_USAGE_FLUSH_INTERVAL`, one minute by default, and once more at a graceful shutdown, which waits for a flush in progress and has 10 seconds to finish. The report therefore trails live traffic by up to one interval, and a replica that stops without a graceful shutdown loses the counts since its last flush. A flush that fails, such as one on a read-only registry, keeps its counts for the next flush. Replicas that share a metadata store add their counts together, so the report covers every replica.

The registry keeps `PODIUM_USAGE_RETENTION_DAYS` days of counts, 365 by default, and the report's window lies within them. [Usage report settings](../reference/configuration#usage-report) lists both settings.

### Query the stored counts

The Postgres and SQLite metadata stores keep the counts in a table named `usage_daily`, with one row per tenant, day, layer, artifact, client kind, and harness:

| Column | Content |
|:--|:--|
| `tenant_id` | The tenant. |
| `day` | The UTC date. SQLite stores it as `YYYY-MM-DD` text, and Postgres as a `DATE`. |
| `layer` | The layer that served the artifact. |
| `artifact_id` | The canonical artifact ID. |
| `client_kind` | `mcp`, `sync`, `sdk`, `cli`, `other`, or `none`. |
| `harness` | The harness the client reported, or `none`. |
| `loads`, `syncs`, `search_hits` | The day's counts. |

The counts add across rows, so any grouping is a sum. On Postgres, each tenant's table is in the tenant's schema. Loads per artifact and week, for example:

```text
SELECT artifact_id, date_trunc('week', day) AS week, sum(loads) AS loads, sum(syncs) AS syncs
FROM usage_daily
GROUP BY artifact_id, week
ORDER BY week, loads DESC;
```

A direct query reads every layer of the tenant, whatever the reader can see in the registry.

---

## Events that record use

The registry records one event for each meta-tool call it serves, whether an agent made the call through `podium-mcp`, a program made it through an SDK, or `podium sync` made it while materializing a workspace from a server.

| Event | Recorded when | Members that describe the call |
|:--|:--|:--|
| `artifact.loaded` | A caller loaded an artifact through `load_artifact`. A server-source `podium sync` loads every artifact of the caller's effective view this way on each run and applies the workspace's scope after the loads. | `target` holds the artifact ID. On a load that resolves, `context` holds `version`, `content_hash`, and `layer`, and `result_size` is `1`. |
| `artifacts.searched` | A caller invoked `search_artifacts`. | `context` holds `query`, `scope`, and `type`, and `result_size` holds the number of results returned. When the call returned results, `context.results` holds them in result order as a JSON array of `{artifact_id, layer}` objects, encoded in the string value. |
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

The usage report answers how often each artifact is loaded, synced, and found, and which artifacts go unused. The audit records answer the questions below, which need the individual calls, and they can reproduce the report's counts, because every counted call records an audit event with the same artifact, layer, client kind, and harness. The two differ where sampling drops audit events, where the receiver lacks a replica's stream, where retention has removed a query, and where an artifact's manifest redacts `layer` in audit records, which hides the layer of its loads from the audit stream while the report keeps it.

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

### Which artifacts searches return

Count the appearances of each artifact in the first 10 results of searches with a query, as the usage report counts search hits:

```text
jq -r 'select(.type == "artifacts.searched" and (.context.query // "") != "" and .context.results != null) | .context.results | fromjson | .[:10][] | .artifact_id' audit.log | sort | uniq -c | sort -rn
```

Replace `.artifact_id` with `.layer` to count by layer.

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
