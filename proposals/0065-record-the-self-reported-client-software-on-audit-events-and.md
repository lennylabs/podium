# Proposal 0065: Record the self-reported client software on audit events, and strip the query string from every exported trace span

- Issue: (to be filed)
- Status: Approved (2026-11-11). Verified (2026-11-11) after 10 adversarial review rounds (21 findings fixed).
- Date: 2026-11-11

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. One part is backward-incompatible: the MCP server's local audit events lose the constant `context.source` key. The change ships in a MINOR release.

## Summary

**What changes.**

- **Client identification header (spec §7.2.2, SPEC-1).** A control-plane request can name its client software in `X-Podium-Client`, a form-encoded value with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`. The registry reads it as an advisory, bounded, and sanitized value that never fails a request and never feeds an access decision.
- **Audit attribute (spec §8.1, SPEC-2; `pkg/audit`, `pkg/registry/server`, `internal/serverboot`, CODE-1 to CODE-3).** Read events, the events of write and administrative requests (including `user.erased`), and the MCP server's local events record the normalized value as `caller.client`. It enters the §8.6 hashed body only when present, so existing logs verify unchanged. Every event the ingest pipeline raises (`artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`) records none, even when a reingest or webhook request triggered it. Every event raised without a request records none, and so does every event about the registry as a whole, including the `audit.anchored` that the erase handler appends after a §8.5 erasure. `audit.EraseUser` takes the client as its own parameter, so `EraseScope` stays a pure record selector.
- **First-party clients (CODE-4 to CODE-7).** `podium-mcp` sends `kind=mcp` with the MCP host's `clientInfo` and the harness, and drops the local `context.source` key. `pkg/sync` sends `kind=sync` with the run's harness, and `podium sync --preview` sends the same value through the exported `sync.ClientHeaderValue`. The `podium` CLI sends `kind=cli`, including on the `podium status` scope preview. `podium-py` and `podium-ts` send `kind=sdk`. None sends it on a presigned or `/objects` link or on a health probe.
- **Span scrub (spec §13.8, SPEC-4; `pkg/tracing`, CODE-8).** Every exporter `pkg/tracing` builds is wrapped so that a span attribute whose value is an `http` or `https` URL, `url.full` among them, is exported with its query and fragment removed. No other exported span field carries a URL query today (D-13), and §13.8 now forbids one in any field. Search text, `session_id`, and SigV4 signatures leave the trace stream.
- **Docs, manual validation, and CHANGELOG (DOC-1, DOC-2a, DOC-2b, MV-1, DOC-3).** The HTTP API reference documents the header. The operator guide gains an "Audit records" section that answers what an `http(s)` audit target receives (DOC-2a lands first and documents current behavior), then the `caller.client` key and a tracing paragraph. `test/manual-validation.md` gains S104, and the CHANGELOG gains the `[Unreleased]` Added, Changed, and Fixed entries.

**Fixed decisions.**

- D-1: the transport is one request header, `X-Podium-Client`. User-Agent and query parameters are rejected.
- D-2: the value is `application/x-www-form-urlencoded`, encoded with `url.Values.Encode` and parsed with `url.ParseQuery`; member order carries no meaning.
- D-3: the `kind`, `name`, `version`, and `harness` values each first-party client sends are fixed in D-3 and SPEC-1. The `harness` member takes the per-call argument only on `load_artifact`.
- D-4: the header goes on every `/v1/` control-plane request and on no presigned link, `/objects` link, `/healthz`, or `/readyz`.
- D-5: the value is advisory and never an input to authentication, authorization, visibility, quotas, sampling, or tenant selection.
- D-6: the bounds (1024-byte header, 64-byte member), the character set, the first-wins rule, and the `other` kind are spec-fixed and not operator-tunable.
- D-7: an absent, oversized, or malformed header never fails a request. No error code, lint code, matrix cell, environment variable, or flag is added.
- D-8: `caller.client` is a nested object under `caller`. Events the ingest pipeline raises, events raised without a request, and events about the registry as a whole, such as `audit.anchored`, record none.
- D-9: hashing appends one `caller_client_<member>` part per non-empty member after the tenant part. Every sink's `Append`, and `audit.EraseUser` for the `user.erased` it appends, normalize the value before hashing.
- D-10: local events record the value `podium-mcp` builds for the call, including on a load that sends no registry request, and `context.source` is removed from them.
- D-11: `caller.client` is event metadata. The §8.2 query scrub, §8.4 sampling, and §8.5 erasure leave it unchanged, and no §8.4 or §8.5 text changes.
- D-12 and D-13: the span rule lives in §13.8 and covers every exported span field. The scrub is an export-time exporter wrapper in `pkg/tracing` that cuts the query and fragment from every attribute value that is an `http` or `https` URL, and it rewrites no other field.
- D-14: a binary-level test searches the raw OTLP bytes `podium-mcp` exports on success and error paths. No source-scan guard over tracer-provider construction is added.
- D-15: the spec steps land first and the docs and CHANGELOG last. DOC-2a documents current behavior and may land at any point, and the tracing part does not depend on the audit part.

**Watch out for.**

- **The layer routes bypass the audit metadata middleware.** `/v1/layers` and `/v1/layers/` are mounted through `srv.TenantRouted(layers.Handler())` (`internal/serverboot/serverboot.go:1529-1530`), and `/v1/ingest/webhook/` is mounted unwrapped through `layers.WebhookHandler()` (`internal/serverboot/serverboot.go:1531-1536`). All of them sit outside `srv.Handler()`, where `withAuditMetaMiddleware` runs. Layer write events reach `caller.client` only through the `auditMetaFrom` fallback in `LayerEndpoint.emitAudit` (`pkg/registry/server/layers.go:521-528`), and ingest events never see it. Do not extend `ingestAuditEmitter`; D-8 records every event the ingest pipeline raises as carrying no `caller.client`, and TEST-2 (d) pins that. The emitter's doc comment (`internal/serverboot/reingest.go:179-183`) lists only part of that event set; SPEC-2 carries the full set, taken from the emit sites.
- **`user.erased` bypasses `emitAuditEvent`.** It is built by `audit.UserErasedEvent` on the endpoint-sink branch and appended inside `audit.EraseUser` on the file-sink branch (`pkg/registry/server/layers.go:1127-1149`, `pkg/audit/retention.go:234`, `:343`). CODE-1 step 8 gives `EraseUser` a `client` parameter, and CODE-2 (b) passes the request's client on both branches.
- **The erase handler re-anchors inside the request.** With anchoring on and a file sink, `eraseAudit` runs the `afterErase` hook with `r.Context()` (`pkg/registry/server/layers.go:1137-1139`), and `audit.Anchor` appends `audit.anchored` (`pkg/audit/anchor.go:47-58`). Leave `audit.Anchor` and the hook unchanged. §8.1 records no `caller.client` on an event about the registry as a whole (D-8), and TEST-2 (e) pins it.
- **Do not put the client on `audit.EraseScope`.** `EraseUser` refuses a cross-tenant scope only through `scope == (EraseScope{})` (`pkg/audit/retention.go:241-243`). A non-selector field on `EraseScope` would let a header-derived value make `{Tenant: "", Unlabeled: false}` pass that guard, and `includes` would then select every tenant's records (`retention.go:188-193`).
- **`callerForJSON` drops a caller that has only a client.** Its nil check (`pkg/audit/file.go:220-224`) lists every other caller attribute. Forgetting `CallerClient` there loses the key on write and breaks `Verify` for that record. TEST-1 has the case.
- **Normalize before deciding the kind.** Mapping an absent kind to `other` before the all-empty check makes `kind=&name=` record `{kind: other}` (CODE-1 step 2).
- **`url.Values.Encode` sorts keys.** The wire value is `harness=...&kind=...&name=...&version=...`. Tests parse the header and compare members; a literal string comparison fails against a correct implementation.
- **`podium sync --preview` does not go through `pkg/sync`.** Its one control-plane request, `GET /v1/scope/preview`, is sent by `cmd/podium`'s `fetchScopePreview` (`cmd/podium/main.go:340-344`, `cmd/podium/scope_preview.go:44-47`), the helper `podium status` also calls (`cmd/podium/status.go:103`). CODE-6 makes the header a parameter of `fetchScopePreview`, so the sync branch sends `kind=sync` with the resolved harness and `podium status` sends `kind=cli`.
- **The harness override is `load_artifact` only.** `harnessFromArgs` reads `args` on every meta-tool (`cmd/podium-mcp/main.go:2135-2140`). CODE-4 applies it only when the path is `/v1/load_artifact`.
- **Set the host fields after protocol negotiation succeeds.** `negotiateProtocol` can still refuse the `initialize` after the version-floor check (`cmd/podium-mcp/main.go:1165-1172`).
- **A span processor cannot do the scrub.** otelhttp sets `url.full` after `tracer.Start` (otelhttp `transport.go:142`), and `go.opentelemetry.io/otel/sdk` v1.45.0 has no `OnEnding` hook. The wrapper sits at export.
- **Never record an `http.Client.Do` error on a span.** otelhttp records the base transport's error, which names a host and port and no URL (otelhttp `internal/semconv/client.go:294-297`). `http.Client.Do` adds the `*url.Error` that carries the full URL, query included, above the transport (Go `net/http/client.go:622-637`). The scrub rewrites only attribute values that are whole URLs, so a `span.RecordError(err)` or `span.SetStatus(codes.Error, err.Error())` with such an error would export the query, which §13.8 forbids.
- **The source guard must name the module, and must not walk `.claude/worktrees/`.** `go test` runs a test binary in its package's directory, so `go list ./...` inside a test lists only that package's subtree and the guard passes vacuously. Enumerate with the module pattern `github.com/lennylabs/podium/...`, which resolves from any directory in the module and leaves out the nested repository copies under `.claude/worktrees/`, and assert a positive control (TEST-2 (f)).
- **The TypeScript SDK keeps Node built-ins out of top-level imports** (`sdks/podium-ts/src/config.ts:274-276`). The version lookup uses a guarded dynamic import.
- **Parts of `test/e2e` skip silently on macOS.** Run with `-v` and grep the output for `SKIP` before claiming the end-to-end steps passed.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.2.2 Client Identification lands after §7.2.1.
      Levels: unit. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §8.1 "Client in audit events" paragraph lands after the tenant paragraph.
      Levels: unit. Depends on: S1
- [ ] **S3 · spec** — SPEC-4. The §13.8 Tracing bullet gains the exported-span-content rule.
      Levels: unit. Depends on: —
- [ ] **S4 · docs** — DOC-2a. The operator guide's "Audit records" section documents the current file and endpoint audit formats.
      Levels: e2e. Depends on: —
      Interleave: this docs step precedes the code because it documents behavior that exists today and depends on no other step.
- [ ] **S5 · code** — CODE-1. `pkg/audit` gains `CallerClient`, the header codec, normalization at every sink's `Append`, the hashed parts, the wire key, and the `client` parameter of `EraseUser`.
      Levels: unit. Depends on: S2
- [ ] **S6 · test** — TEST-1. The codec, hash, wire-form, and erasure unit tests in `pkg/audit`.
      Levels: unit. Depends on: S5
- [ ] **S7 · code** — CODE-2. `pkg/registry/server` reads the header into `AuditMeta` and records it on write-handler events and on `user.erased`.
      Levels: unit, integration. Depends on: S5
- [ ] **S8 · code** — CODE-3. `internal/serverboot` copies `caller.client` onto read events.
      Levels: unit, integration. Depends on: S7
- [ ] **S9 · test** — TEST-2. Registry integration tests through the serverboot mux.
      Levels: unit, integration. Depends on: S8
- [ ] **S10 · code** — CODE-4. `podium-mcp` sends the header and records `caller.client` on local events, and drops `context.source`.
      Levels: unit. Depends on: S5
- [ ] **S11 · test** — TEST-3. `podium-mcp` header and local-stream tests.
      Levels: unit, integration. Depends on: S10
- [ ] **S12 · code** — CODE-5. `pkg/sync` sends `kind=sync` with the run's harness, and the interactive `podium sync override` passes its harness to the view resolution.
      Levels: unit. Depends on: S5
- [ ] **S13 · test** — TEST-4. `pkg/sync` header tests on control-plane requests and their absence on links, and the `cmd/podium` test that the interactive `podium sync override` sends its resolved harness.
      Levels: integration. Depends on: S12
- [ ] **S14 · code** — CODE-6. The `podium` CLI sends `kind=cli`, and `podium sync --preview` sends the `pkg/sync` value through the `fetchScopePreview` header parameter, with its own `cmd/podium` unit test.
      Levels: unit. Depends on: S12
- [ ] **S15 · code** — CODE-7. `podium-py` and `podium-ts` send `kind=sdk` on API calls only.
      Levels: unit. Depends on: S1
- [ ] **S16 · test** — TEST-6. SDK header tests in the existing suites.
      Levels: unit, integration. Depends on: S15
- [ ] **S17 · test** — TEST-5. End-to-end tests that the real binaries record `caller.client` in both audit streams.
      Levels: e2e. Depends on: S9, S11, S13, S14
- [ ] **S18 · code** — CODE-8. `pkg/tracing` wraps every exporter in the export-time scrub through an unexported `newProvider`.
      Levels: unit. Depends on: S3
- [ ] **S19 · test** — TEST-7. The scrub unit table, the provider-level test over every span field, and the binary-level OTLP test on success and error paths.
      Levels: unit, integration. Depends on: S18
- [ ] **S20 · test** — MV-1. Scenario S104 in `test/manual-validation.md`.
      Levels: e2e. Depends on: S17, S19
- [ ] **S21 · docs** — DOC-2b. The operator guide adds `caller.client` and the tracing paragraph.
      Levels: e2e. Depends on: S4, S9, S11, S18
- [ ] **S22 · docs** — DOC-1. The HTTP API reference documents `X-Podium-Client` and links to the operator guide's `caller.client` text.
      Levels: e2e. Depends on: S1, S21
- [ ] **S23 · docs** — DOC-3. The `[Unreleased]` CHANGELOG entries.
      Levels: —. Depends on: S1 to S22

**Ordering constraints.**

- S1 to S3 land before any code step and pass `make coverage-gate` (D-15). S2 depends on S1 because its paragraph cites §7.2.2.
- CODE-1 changes the signature of `audit.EraseUser`, CODE-4 changes the signatures of `auditMeta`, `auditSearch`, and `auditLoadArtifact`, CODE-5 changes `httpGetJSON`, `httpGetBody`, `streamServerEvents`, and `readServerEventStream`, and CODE-6 changes `fetchScopePreview` and `runScopePreview`. Each step carries the compile-only call-site edits in the production and test files that call those functions, so every package and test package compiles after every step. CODE-1 therefore passes nil at the `EraseUser` call sites in `pkg/registry/server/layers.go` and `cmd/podium/admin.go`, and CODE-2 (b) replaces the nil in `layers.go`.
- S14 depends on S12, because the `podium sync --preview` branch calls the `sync.ClientHeaderValue` that CODE-5 exports.
- The tracing part (S3, S18, S19) does not depend on the audit part and can land on its own.
- S21 (DOC-2b) precedes S22 (DOC-1), because DOC-1 (f) links to the operator guide's "Audit records" section for `caller.client`, which DOC-2b adds.
- S23 describes the whole change and lands last.

## Current state and the gap

### Audit records do not name the client software

§8.1 defines the caller attributes on read events as `caller.identity` (the OAuth `sub` claim) with email and groups attached, and in public mode `caller.network` (source IP and any `X-Forwarded-User`) and `caller.public_mode`. No section from §8.1 to §8.6 names the client software, the MCP host application, or the harness. The code matches the spec. `audit.Event` (`pkg/audit/audit.go:126-160`) and its wire form `jsonEvent` and `jsonCaller` (`pkg/audit/file.go:170-198`) have no client field. `eventForJSON` (`pkg/audit/file.go:205`) is the single serialization that the file sink, the retention rewrite, and the endpoint sink use.

The data exists at the edges and is discarded:

- `podium-mcp` parses the MCP `initialize` `clientInfo` `{name, version}` (`cmd/podium-mcp/main.go:1143-1152`) and reads only `Version`, for the §6.9 host-version floor (`main.go:1160`).
- `newRegistryRequest` copies every meta-tool argument except `harness` into the URL query (`main.go:2533-2540`). The only headers it sets are `Authorization` and `X-Podium-Tenant` (`main.go:2563-2575`).
- `podium sync` sends only `Authorization` (`pkg/sync/server.go:249-256`, `pkg/sync/watch_server.go:110-120`), although `Options.AdapterID` names the harness of the run (`pkg/sync/sync.go:62-65`).
- The CLI request helpers send only `Authorization` (`cmd/podium/main.go:1621-1631`, `cmd/podium/layer.go:673-687`, `cmd/podium/scope_preview.go:47-53`).
- The SDKs send only `Authorization` (`sdks/podium-py/podium/client.py:830-835`, `sdks/podium-ts/src/index.ts:997-1000`).
- No Go client sets a User-Agent on registry calls. The only User-Agent in Go code is on outbound webhooks (`pkg/webhook/webhook.go:416`). The registry reads no client value into `AuditMeta` (`pkg/registry/server/audit_context.go:30-41`, `:84-97`).

The `podium-mcp` local stream stamps the constant context `{"source": "mcp"}` on every event (`cmd/podium-mcp/main.go:701`, `:722`, `:767`). That value cannot distinguish Claude Code from Cursor, and it carries no version or harness. No spec section defines it. An operator therefore cannot report which artifacts are used from which tool.

### A self-reported value is attacker-controlled

Both sinks JSON-encode each record, so a control character cannot split or forge a record. Four risks remain:

- An oversized value bloats SIEM records and every concurrent local writer's append (§9.1).
- Decoded control or ANSI characters can mislead whoever reads the value.
- A NUL byte would make the NUL-separated §8.6 canonical body ambiguous (`pkg/audit/audit.go:201-206`).
- A reader can mistake the value for a trusted signal.

The precedent `X-Forwarded-User` is recorded verbatim and unbounded (`pkg/registry/server/audit_context.go:94`).

### The hash chain needs an absent value to contribute nothing

`canonicalBody` appends `caller_email`, `caller_groups`, and `caller_public_mode` unconditionally (`pkg/audit/audit.go:193-195`), `caller_network` only when non-nil (`:196-198`), and `tenant` only when non-empty (`:200-203`). The tenant guard is the spec-backed precedent: §8.1 states that an empty `tenant` adds nothing to the hashed body. `FileSink.Verify` and `Memory.Verify` recompute each hash from the stored fields (`pkg/audit/file.go:125-153`, `pkg/audit/audit.go:254-262`), so a new hashed attribute keeps existing logs verifiable only if an absent value contributes nothing.

### What an HTTP(S) audit target receives today

When `PODIUM_AUDIT_SINK` (`cmd/podium-mcp/main.go:675`) or `PODIUM_AUDIT_LOG_PATH` (`internal/serverboot/audit_anchor.go:41-47`, §13.12) is an `http` or `https` URL, `audit.EndpointSink` sends one POST per event (`pkg/audit/endpoint.go:55-86`). Each POST carries `Content-Type: application/json` and one JSON object, with no batching. The object has the same keys as one line of the file sink's JSON Lines: `type`, `timestamp` (RFC 3339 with nanoseconds, UTC), `trace_id`, `caller` (`identity`, `email`, `groups`, `network` with `source_ip` and `forwarded_user`, and `public_mode`), `target`, `context`, `tenant`, `resolved_layers`, `result_size`, `hash`, and `prev_hash`. `type`, `timestamp`, and `hash` are always present, and the other keys are omitted when empty. The sink computes the §8.6 chain in memory before it sends. Each POST has a 10-second client timeout (`endpoint.go:36`, `:49`). A transport error or a non-2xx response is returned as an error, which the registry discards (`internal/serverboot/audit_emitter.go:57`) and `podium-mcp` swallows, so the event is lost with no retry or buffer. `Verify` is a no-op (`endpoint.go:90`). No docs page states any of this; DOC-2a documents it, and this proposal adds `caller.client` to the object.

### Exported client spans carry unscrubbed query text

§13.8 defines span structure and export and places no limit on attribute content. §8.2 scopes query-text scrubbing to text written to audit. `podium-mcp`'s registry client is `&http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}` (`cmd/podium-mcp/main.go:590`), the only otelhttp client in production Go code. The registry's server handler (`internal/serverboot/serverboot.go:1793`) records `url.path` only.

otelhttp v0.70.0 (`go.mod:19`) sets `url.full` to `req.URL.String()` with only the userinfo removed (otelhttp `internal/semconv/client.go:114-123`) and adds it through `span.SetAttributes` after `tracer.Start` (otelhttp `transport.go:107`, `:142`). `podium-mcp` sends every meta-tool argument and `session_id` as query parameters (`cmd/podium-mcp/main.go:2527-2552`), and the same client fetches S3 SigV4 presigned large-resource URLs (`cmd/podium-mcp/large_resources.go:157-174`). With `PODIUM_TRACING=otlp`, or with `OTEL_EXPORTER_OTLP_ENDPOINT` set and `PODIUM_TRACING` unset (`pkg/tracing/tracing.go:72-86`), the trace backend therefore receives raw `search_artifacts` and `search_domains` text, `session_id`, and `X-Amz-Signature` and `X-Amz-Credential` values. §13.12 states that a SigV4 URL can be fetched by any holder until it expires, so the last of these is a live bearer credential. `pkg/tracing` installs only `WithBatcher(exp)` (`tracing.go:56-61`), with no attribute filtering.

A span processor cannot fix this. Its `OnStart` runs before otelhttp adds `url.full`, and `go.opentelemetry.io/otel/sdk` v1.45.0 offers no `OnEnding` hook; `OnEnd` receives an immutable `ReadOnlySpan`. The scrub has to happen at export.

## Decisions

- **D-1. Transport.** Clients send the attribute in one request header, `X-Podium-Client`. A query parameter is rejected for three reasons: it would share the meta-tool argument namespace (`cmd/podium-mcp/main.go:2533-2540`), it would be missing from the §7.6.2 `batchLoad` POST body path, and it would put the value back into `url.full`. User-Agent is rejected because browsers, proxies, and HTTP libraries own that header and its product-token grammar does not carry the structured members. The registry does not read User-Agent into audit.
- **D-2. Header format.** The value is an `application/x-www-form-urlencoded` string with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`, for example `harness=claude-code&host=claude-code&host_version=1.0.33&kind=mcp&name=podium-mcp&version=0.6.1`. Go encodes it with `url.Values.Encode`, which sorts the keys, and parses it with `url.ParseQuery`. Member order carries no meaning. The member names are lower snake_case, matching §7.2.1.
- **D-3. Who sends what.**
  - `podium-mcp` sends `kind=mcp`, `name=podium-mcp`, and `version=buildinfo.Version`. It sends `host` and `host_version` from the `initialize` `clientInfo`, which it holds for its process lifetime once negotiation succeeds; the serve loop is single-threaded (`cmd/podium-mcp/main.go:953-983`, `:1207-1208`). It sends `harness` as `cfg.harness` on every call, replaced by the call's `harness` argument on `load_artifact` alone (§6.7).
  - `podium sync` sends `kind=sync`, `name=podium`, `version=buildinfo.Version`, and `harness` set to the run's `AdapterID`. A run with the default adapter sends `harness=none`. A `kind: marketplace` render (§7.8) passes no `AdapterID` and omits `harness`. `podium sync --preview` sends the same value, with `harness` set to the run's resolved harness (`none` by default), on its `GET /v1/scope/preview` request.
  - The `podium` CLI sends `kind=cli`, `name=podium`, and `version=buildinfo.Version`, including on the scope-preview request of `podium status`.
  - `podium-py` sends `kind=sdk`, `name=podium-py`, and the version from `podium/_version.py`. `podium-ts` sends `kind=sdk`, `name=podium-ts`, and the version from its `package.json`. These names differ from the published package names (`podium-sdk`, `@lennylabs/podium-sdk`) on purpose: the D-6 character set would rewrite the npm scope's `@`.
  - A member with no value is omitted.
- **D-4. Where it is sent.** A first-party client attaches the header to every request to the §7.2 control-plane API (`/v1/...`). It does not attach it to a request that follows a §7.2 presigned link, whether to object storage or to the registry's `/objects` route, nor to `/healthz` or `/readyz`. No request in that excluded set raises an audit event, and a presigned URL can address a third-party store.
- **D-5. Advisory and untrusted.** `caller.client` records what the client reported. The registry verifies nothing about it and never uses it as an input to authentication, authorization, visibility (§4.6), quotas or rate limits (§4.7.8), audit sampling (§8.4), or tenant selection (§6.3.1). It is not an identity header: no identity provider reads it, and a trusted-headers gateway need not strip it, because §6.3.3 assigns the gateway only the `X-Podium-User-*` headers.
- **D-6. Bounds and sanitization.** These are fixed by the spec and are not operator-tunable. The registry ignores the whole header when it is longer than 1024 bytes or when `url.ParseQuery` returns an error. When a member repeats, the first value wins. Unknown members are ignored. In each member, every byte outside `A-Z`, `a-z`, `0-9`, space, `.`, `_`, `-`, `+`, `/`, `(`, and `)` is replaced with `_`, and the result is truncated to 64 bytes. The replacement covers NUL, control characters, ANSI escapes, non-ASCII bytes, `@`, `=`, `[`, and `|`. After sanitization, a value with no non-empty member records nothing. Only then is `kind` mapped: a kind that is absent or not exactly one of `mcp`, `sync`, `sdk`, or `cli` is recorded as `other`. The `podium-mcp` local stream applies the same normalization, because `clientInfo.name` is host-controlled. These bounds keep `caller.client` to at most about 420 bytes of JSON. They do not by themselves keep every local event under the 512-byte `PIPE_BUF` of macOS, which an event with two hashes, a timestamp, and a trace ID can already exceed.
- **D-7. Never fails a request.** An absent, oversized, or malformed header records no `caller.client`, and the request is served exactly as it would be without the header. No §6.10 error code, lint code, matrix cell, environment variable, or flag is added.
- **D-8. Audit placement.** The attribute is the nested object `caller.client` `{kind, name, version, host, host_version, harness}`, with every member omitted when empty. It sits beside `caller.network`, so SIEM consumers key on the dotted path as they do for `caller.public_mode`. The registry records it on read events (through the serverboot emitter), on write and administrative events (through `emitAuditEvent` and `LayerEndpoint.emitAudit`), and on `user.erased` on both erase paths. Every event the ingest pipeline raises records none, even when a reingest or webhook request triggered the ingest, because the reingest and webhook routes run outside the audit metadata middleware and `ingestAuditEmitter` (`internal/serverboot/reingest.go:184-199`) receives no request metadata. That set is `artifact.published`, `artifact.deprecated`, and `artifact.signed` (`pkg/registry/ingest/ingest.go:743`, `:749`, `:755`), `domain.published` (`ingest.go:511`), `freeze.break_glass` (`ingest.go:461`), and `layer.history_rewritten` and `layer.ingested` (`pkg/registry/ingest/orchestrator.go:140`, `:187`). It follows §8.1, which already groups ingest and signature events apart from the events a request raises for tenant labeling. Events raised without a request record none: the scheduler, anchoring, retention, and read-only transitions are examples. An event about the registry as a whole also records none, as it records no tenant under the §8.1 tenant paragraph (`spec/08-audit-and-observability.md:32`). That includes the `audit.anchored` that the erase handler appends while it serves the request: with anchoring on and a file sink, `eraseAudit` runs the re-anchor hook with the request context (`pkg/registry/server/layers.go:1137-1139`, `internal/serverboot/serverboot.go:1710-1715`, `internal/serverboot/audit_anchor.go:177-183`), and `audit.Anchor` builds the event with `Caller: "system:anchor"` and no request metadata (`pkg/audit/anchor.go:47-58`). No code change is needed for it.
- **D-9. Hash chain.** `caller.client` is part of the §8.6 hashed body. `canonicalBody` appends one part per non-empty member, `caller_client_<member>=<value>`, in the fixed order `kind`, `name`, `version`, `host`, `host_version`, `harness`, after the tenant part. An absent `caller.client` contributes no part, following the §8.1 tenant precedent, so every existing record keeps its hash and existing logs verify with no migration. Every sink's `Append` (`Memory`, `FileSink`, and `EndpointSink`) normalizes a non-nil `CallerClient` before it hashes, so the body stays NUL-free whatever the caller passed. `EraseUser` appends `user.erased` through `rewriteWithChain` (`pkg/audit/retention.go:399-411`), which hashes `canonicalBody()` with no `Append`, so `EraseUser` normalizes the client it sets on that event itself (CODE-1 step 8).
- **D-10. Local stream.** The `podium-mcp` local events (§8.3) record the client value `podium-mcp` builds for the call, which is the value it sends as `X-Podium-Client` whenever the call reaches the registry. A load served from the workspace overlay (`cmd/podium-mcp/main.go:2187`), or from an `offline-first` or `offline-only` cache hit (`:1363` into `cachedOrRefetch` at `:1581`, and `:1370`), sends no registry request and still records the value. A cache hit under the default `always-revalidate` mode (spec/06 §6.2 `PODIUM_CACHE_MODE` row, `main.go:256`) first sends a HEAD (`:1384`), and a cached `(id, version)` in any mode that reaches the network is revalidated with a conditional GET whose 304 answer is served from the cache (`:1410`, `:1414`). Both requests go through `newRegistryRequest` (`:2599`, `:2670`) and carry the header, and the registry answers both through `core.LoadArtifact` (`pkg/registry/server/server.go:1124`), which emits `artifact.loaded` (`pkg/registry/core/core.go:1522-1537`). The registry stream therefore records a revalidated cache hit with `caller.client`. The §7.4 degraded fallback (`main.go:1624`) serves the cache only after a request that carried the header failed to reach the registry. The constant context key `{"source": "mcp"}` is removed, because `caller.client.kind=mcp` carries the same fact. The removal is backward-incompatible for a consumer that filters on `context.source`, and it ships in a MINOR release with no shim, per the pre-1.0 policy.
- **D-11. Redaction, retention, and erasure.** `caller.client` is event metadata. The §8.2 query-text scrub, which applies to search text, does not reach it. §8.4 sampling reads only the event type (`pkg/audit/sampler.go:42-54`). §8.5 erasure leaves it unchanged, as `redactEvent` already leaves `caller.network` (`pkg/audit/retention.go:302-336`). No §8.4 or §8.5 text changes.
- **D-12. Exported span content.** §13.8 states two things. First, no span Podium exports carries a URL query component or fragment, the text of a `search_artifacts` or `search_domains` query, or a `session_id` in its name, attributes, events, links, or status description. Second, an attribute value that is an `http` or `https` URL is exported with its query and fragment removed. The rule keeps presigned-URL signatures out of exported traces. It is stated over every field, so no field is left outside it for a reviewer or an implementer.
- **D-13. Span scrub mechanism.** `pkg/tracing` wraps every exporter it builds in `scrubExporter`, which rewrites span attributes at export time. A string attribute value that starts with `http://` or `https://`, with the scheme compared case-insensitively, is cut at its first `?` or `#`. The rule keys on the value, so it covers `url.full` and any later attribute that carries an absolute URL without a key list. otelhttp v0.70.0 emits neither `http.url` nor `url.query`, and it removes userinfo before it sets `url.full` (otelhttp `internal/semconv/client.go:114-123`). An unexported `newProvider(exp, service)` builds the provider, and `Init` and the `pkg/tracing` tests share it.

  No other span field is rewritten, because no producer in either binary writes a URL query into one:
  - **Names.** The client transport names spans `HTTP <method>` (otelhttp `transport.go:81-82`). The registry handler names them by operation or by `method + " " + r.URL.Path` (`internal/serverboot/serverboot.go:1794-1799`). Podium's own spans are `mcp.<tool>`, `adapter.translate`, `materialize`, and `objectstore.fetch` (`cmd/podium-mcp/main.go:1236`, `:1979`, `:1997`; `cmd/podium-mcp/large_resources.go:36`, `:81`).
  - **Status descriptions.** otelhttp sets one only from the base round tripper's error or from a response-body read or write error (`transport.go:176`, `:252`, `:270`). That error names a host and port and no URL, because `http.Client.Do` adds the `*url.Error` above the transport (Go `net/http/client.go:622-637`; otelhttp `internal/semconv/client.go:294-297`). A status code maps to an empty description, or to `Invalid HTTP status code <n>` for a code outside 100 to 599 (`internal/semconv/client.go:48-56`, `server.go:70-78`).
  - **Events and links.** otelhttp adds `read` and `write` events, which carry integer byte counts, only under `WithMessageEvents` (`handler.go:131-156`). It adds a link, which carries no attributes, only under `WithPublicEndpointFn` (`handler.go:104-109`, `config.go:99`). Podium passes neither option (`serverboot.go:1793-1800`, `cmd/podium-mcp/main.go:590`), and the transport records no exception event.
  - **Podium's own spans.** No Podium code calls `SetAttributes`, `SetStatus`, `RecordError`, `AddEvent`, or `AddLink`.
  - **Server attributes.** The registry handler records `url.path` and `http.route`, which carry no query (`internal/semconv/server.go:202-215`).

  §13.8 binds those fields, the Watch-out list names the one way new code could break that (recording an `http.Client.Do` error on a span), and TEST-7 asserts the rule over every field. A per-transport fix would cover one client only. The export-time wrapper covers every span from every instrumentation in both binaries, and it applies to the stdout and OTLP exporters alike, because both read attributes through `ReadOnlySpan.Attributes()` (otlptrace `internal/tracetransform/span.go:109`, `sdk/trace/tracetest/span.go:82`).
- **D-14. Regression guard.** The guard is a binary-level integration test of `podium-mcp`. That binary holds the only outbound otelhttp client, and the test runs it through `tracing.Init`. It searches the raw OTLP request bodies for query markers on a successful call, a failed presigned fetch, and an unreachable registry. A provider built outside `pkg/tracing` in `podium-mcp`, or a dependency or code change that writes a query into any span field on those paths, fails it. No source-scan guard is added: the registry's spans carry no query-bearing value, and the binary test checks the bytes that leave the process.
- **D-15. Ordering.** The spec steps land first, in order, before any code. Next come `pkg/audit` (CODE-1), the registry recording (CODE-2 and CODE-3), the clients (CODE-4 to CODE-7), and the tracing scrub (CODE-8), each followed by its tests, and the docs and CHANGELOG last. DOC-2a documents current behavior and may land at any point. The tracing part (SPEC-4, CODE-8, and TEST-7) does not depend on the audit part.

## Spec amendment: §7.2.2 Client Identification

**SPEC-1.** `spec/07-external-integration.md`, new subsection §7.2.2.

**Rationale.** The header needs one normative definition: its format, who sends it, where it goes, and how the registry reads it. The body of §7.2 describes the `load_artifact` response, so a request-header contract inserted there would sit among response fields. A sibling subsection after §7.2.1, which already fixes snake_case member naming, keeps §7.2's structure and gives §8.1 a precise cross-reference. The kind comparison is stated as exact so that the `other` mapping of `MCP` has a spec basis. The harness member is stated per sender, because §6.7 grants the per-call `harness` override to `load_artifact` alone and the other meta-tools materialize nothing.

**Anchor.** Insert the new subsection after the last paragraph of §7.2.1, which begins "The convention binds every control-plane endpoint.", and before the heading `## 7.3 Host Integration`.

**Added text.**

> ### 7.2.2 Client Identification
>
> A request to the control-plane API can name the client software that sent it in the `X-Podium-Client` header. The value is an `application/x-www-form-urlencoded` string with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`, and the order of the members carries no meaning. `kind` is one of `mcp`, `sync`, `sdk`, or `cli`, compared exactly. `name` and `version` identify the client software and its release. `host` and `host_version` are the `clientInfo.name` and `clientInfo.version` of the MCP `initialize` request, and only the MCP server sends them. `harness` is the harness adapter value of the sending process: the MCP server's `PODIUM_HARNESS` (§6.2), replaced on a `load_artifact` request by that call's `harness` argument (§6.7), or the harness of a `podium sync` run (§7.5). A member with no value is omitted.
>
> The MCP server sends `kind=mcp` and `name=podium-mcp`, and it holds the host name and version it received at `initialize` for its process lifetime (§6.8). `podium sync` sends `kind=sync` and `name=podium`; a `podium sync` run that renders a `kind: marketplace` target (§7.8) omits `harness`, because it renders for a set of harnesses. The `podium` CLI sends `kind=cli` and `name=podium`. The Python and TypeScript SDKs (§7.6) send `kind=sdk` with `name=podium-py` and `name=podium-ts`. Each sends the header on every control-plane request and on no request that follows a presigned link (§7.2, §13.12).
>
> The registry reads the header as an advisory, self-reported value. It verifies nothing about the value. The value is never an input to authentication, authorization, visibility (§4.6), quotas (§4.7.8), audit sampling (§8.4), or tenant selection (§6.3.1). The registry ignores a value longer than 1024 bytes and a value that does not parse. When a member repeats, the registry uses the first occurrence, and it ignores unknown members. In each member, the registry replaces every byte other than an ASCII letter, a digit, a space, `.`, `_`, `-`, `+`, `/`, `(`, or `)` with `_`, and then truncates the member to 64 bytes. A value that leaves no non-empty member is treated as absent. Otherwise, a `kind` that is absent, or that is not exactly one of the values above, becomes `other`. An absent or ignored value never causes the request to fail. §8.1 states what audit records from it.

## Spec amendment: §8.1 Client in audit events

**SPEC-2.** `spec/08-audit-and-observability.md`, §8.1.

**Rationale.** §8.1 defines the caller attributes and how they hash. A caller attribute that §8.1 never names would leave CODE-1 to CODE-4 without a spec basis. The free-form `context` map is not a fit: no spec section defines it, and erasure tombstones any context value that matches a user alias (`pkg/audit/retention.go:325-331`). The paragraph names the events that record the attribute, including `user.erased`, which CODE-1 step 8 and CODE-2 (b) cover, and states that the events the ingest pipeline raises record none, because the reingest and webhook routes do not carry request metadata into the ingest pipeline. The ingest set is taken from the emit sites that D-8 cites, so it includes `artifact.deprecated`, `artifact.signed`, and `domain.published`, which a reingest or webhook request also raises. The paragraph also exempts events about the registry as a whole, as the tenant paragraph does, because the erase handler appends `audit.anchored` while it serves the erase request (D-8), and that event describes the chain head instead of the request. The local-stream sentences cover loads that send no registry request, which are workspace-overlay loads and `offline-first` or `offline-only` cache hits. A cache hit the MCP server revalidates with a HEAD or a conditional GET reaches the registry, so the registry also records it (D-10). No §8.4 or §8.5 text changes: §7.2.2 already excludes the value from sampling, the "Audit events (metadata)" retention row already covers it, and §8.5 states no such negative for `caller.network` either.

**Anchor.** Insert a new paragraph directly after the paragraph that begins "**Tenant in audit events.**" and ends "and an empty `tenant` adds nothing to that body.", and before the heading `## 8.2 PII Redaction`.

**Added text.**

> **Client in audit events.** An event the registry raises while serving a request that names its client (§7.2.2) records the normalized value as `caller.client`. The value is an object whose members `kind`, `name`, `version`, `host`, `host_version`, and `harness` each appear only when non-empty. Read events and the events of write and administrative requests, including `user.erased`, record it. An event the ingest pipeline raises (`artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`) records no `caller.client`, even when a request triggered the ingest, and neither does an event raised without a request. An event about the registry as a whole, such as `audit.anchored` (§8.6), records no `caller.client`, including the `audit.anchored` the registry appends after a §8.5 erasure while it serves the erase request. `caller.client` records what the client reported. It is not an identity attribute, and §7.2.2 lists the inputs it never feeds. The MCP server's local events (§8.3) record the `caller.client` value the MCP server builds for the call under §7.2.2, which is the value it sends to the registry when the call reaches the registry. They also record it when the call sends no registry request: a load served from the workspace overlay (§6.4), or from the content cache without revalidation in the `offline-first` or `offline-only` cache mode (§6.5). `caller.client` is part of the hashed event body (§8.6), and an absent `caller.client` adds nothing to that body.

## Spec amendment: §13.8 exported span content

**SPEC-4.** `spec/13-deployment.md`, §13.8, the **Tracing** bullet. (SPEC-3 was dropped in review; see Non-goals.)

**Rationale.** §13.8 defines spans and export and places no limit on their content, and §8.2 scrubbing covers only audit. Exported traces therefore carry raw query text, `session_id`, and presigned-URL signatures. The first sentence states the rule over every part of a span an exporter receives, so it binds `PODIUM_TRACING=stdout` and OTLP alike and leaves no field open to a later dispute. Naming the search text and `session_id` guards against a later field that carries them outside a URL. The second sentence fixes the one rewrite the implementation performs. Every other field meets the rule because no producer writes a URL query into it (D-13).

**Anchor.** Append two sentences to the end of the **Tracing** bullet, after "so trace context flows between the bridge and the registry even when export is off."

**Added text.**

> A span that Podium exports carries no URL query component or fragment, no text of a `search_artifacts` or `search_domains` query, and no `session_id` in its name, attributes, events, links, or status description. An attribute value that is an `http` or `https` URL is exported with its query component and fragment removed, so an HTTP client span records `url.full` as scheme, authority, and path, and the signature of a §7.2 presigned URL stays out of exported traces.

## Proposed solution

### CODE-1. `pkg/audit`: the client type, header codec, normalization, hashing, and wire form

Targets: `pkg/audit/client.go` (new), `pkg/audit/audit.go` (`Event`, `canonicalBody`, `Memory.Append`), `pkg/audit/file.go` (`jsonCaller`, `callerForJSON`, `eventFromJSON`, `FileSink.Append`), `pkg/audit/endpoint.go` (`EndpointSink.Append`), and `pkg/audit/retention.go` (`EraseUser`), with the compile-only `EraseUser` call-site updates in `pkg/registry/server/layers.go` (`eraseAudit`), `cmd/podium/admin.go:276`, and `pkg/audit/retention_test.go`.

`pkg/audit` owns the event schema and the hashed body, and `podium-mcp`, `pkg/sync`, `cmd/podium`, the registry, and serverboot all import it, so the codec and the sanitizer live here once. All sinks and the retention rewrite serialize through `eventForJSON` and `eventFromJSON`, so one change covers the file sink, the endpoint sink, `Verify`, and erasure. The free-form `Context` map is rejected as the carrier for the reasons SPEC-2 gives.

1. `client.go`, citing `// Spec: §7.2.2, §8.1`:

   ```go
   // ClientHeader is the §7.2.2 request header that names the client software.
   const ClientHeader = "X-Podium-Client"

   // CallerClient is the §8.1 caller.client attribute: the self-reported
   // client software. It is advisory and feeds no access decision (§7.2.2).
   type CallerClient struct {
       Kind, Name, Version, Host, HostVersion, Harness string
   }

   // Header encodes c as the X-Podium-Client value, omitting empty members.
   func (c CallerClient) Header() string

   // ParseClientHeader parses and normalizes an X-Podium-Client value. It
   // returns nil for an empty, oversized (over 1024 bytes), unparsable, or
   // all-empty value.
   func ParseClientHeader(v string) *CallerClient

   // NormalizeClient applies the §7.2.2 sanitization to c and returns nil
   // when no member is left non-empty.
   func NormalizeClient(c CallerClient) *CallerClient
   ```

   `ParseClientHeader` checks the length first, calls `url.ParseQuery`, takes the first value of each known member, ignores unknown members, and calls `NormalizeClient`. The member keys are `kind`, `name`, `version`, `host`, `host_version`, and `harness`.
2. `NormalizeClient` sanitizes and truncates each member first (D-6), returns nil when every member is empty, and only then maps `Kind`: a kind not exactly `mcp`, `sync`, `sdk`, or `cli`, including an empty one, becomes `other`. `kind=&name=` therefore returns nil. The 1024-byte, 64-byte, and character-set constants are unexported and cite `// Spec: §7.2.2`.
3. `Event` gains `CallerClient *CallerClient`, beside `CallerNetwork`.
4. `Memory.Append`, `FileSink.Append`, and `EndpointSink.Append` each run `if e.CallerClient != nil { e.CallerClient = NormalizeClient(*e.CallerClient) }` before computing the hash. Normalization is idempotent, so a value that `ParseClientHeader` already produced is unchanged.
5. `canonicalBody` appends, after the tenant part, one `caller_client_<member>=<value>` part per non-empty member in the order `kind`, `name`, `version`, `host`, `host_version`, `harness`. A nil `CallerClient` appends nothing. The comment states the reason: a record written before the attribute existed keeps its hash.
6. `jsonCaller` gains `Client *jsonClient` with the JSON key `client`. `jsonClient` carries `kind`, `name`, `version`, `host`, `host_version`, and `harness`, each `omitempty`. `callerForJSON` adds `e.CallerClient == nil` to its all-empty check and copies the members. `eventFromJSON` maps them back.
7. No other change to `EndpointSink`. Its POST body gains `caller.client` through `eventForJSON`.
8. `EraseUser` gains a `client *CallerClient` parameter before `scope`: `EraseUser(ctx context.Context, sink *FileSink, userID, salt, admin string, client *CallerClient, scope EraseScope) (int, error)`, citing `// Spec: §8.1, §8.5`. When `client` is non-nil, `EraseUser` sets `NormalizeClient(*client)` as `CallerClient` on the `user.erased` event it builds with `UserErasedEvent`, before `rewriteWithChain` hashes it (D-9). A nil `client` leaves the event as it is today. `EraseScope` (`pkg/audit/retention.go:173-179`) is unchanged and stays a pure record selector, so the zero-value guard (`retention.go:241-243`) keeps refusing a scope with no tenant and `Unlabeled` false whatever client the caller passes. The comment on the parameter states that reason. In this step every caller passes nil: `eraseAudit` (`pkg/registry/server/layers.go:1133`), the offline `podium admin erase --local` form (`cmd/podium/admin.go:276`), and the existing calls in `pkg/audit/retention_test.go`. CODE-2 (b) replaces the nil in `eraseAudit`. `UserErasedEvent` keeps its signature.

### CODE-2. `pkg/registry/server`: read the header into AuditMeta, and record it on write-handler events and `user.erased`

Targets: `pkg/registry/server/audit_context.go` (`AuditMeta`, `auditMetaFrom`, `emitAuditEvent`) and `pkg/registry/server/layers.go` (`eraseAudit`). The `pkg/audit/retention.go` change lands in CODE-1 step 8.

`AuditMeta` is the per-request carrier from the HTTP boundary to the registry emit paths. `withAuditMetaMiddleware` runs on `srv.Handler()` routes, and `LayerEndpoint.emitAudit` falls back to `auditMetaFrom(r, id)` on the layer routes, so parsing the header in `auditMetaFrom` reaches read events, admin and tenant events, and layer write events.

(a) `AuditMeta` gains `Client *audit.CallerClient`. `auditMetaFrom` sets it with `audit.ParseClientHeader(r.Header.Get(audit.ClientHeader))` for public and authenticated callers alike. `emitAuditEvent` sets `ev.CallerClient = m.Client`. Cite `// Spec: §7.2.2, §8.1`.

(b) `user.erased`. `eraseAudit` resolves the client from `AuditMetaFromContext(r.Context())`, falling back to `auditMetaFrom(r, e.caller(r))`. On the endpoint-sink branch it sets `ev.CallerClient` on the event `UserErasedEvent` returns. On the file-sink branch it passes the client as the `client` argument of `audit.EraseUser` (CODE-1 step 8), replacing the nil CODE-1 put there, and `EraseUser` sets it, normalized, on the `user.erased` event it appends. The scope stays `audit.EraseScope{Tenant: tenantID, Unlabeled: !e.multiTenant}` (`pkg/registry/server/layers.go:1132`). The offline `podium admin erase --local` form (`cmd/podium/admin.go:276`) passes nil, so its `user.erased` records none, which matches §8.1: that form serves no request. The re-anchor hook that `eraseAudit` runs after the file-sink rewrite (`pkg/registry/server/layers.go:1137-1139`) is unchanged, and `audit.Anchor` keeps building `audit.anchored` with no `CallerClient`, which matches §8.1 for an event about the registry as a whole (D-8).

The ingest paths are unchanged. `runIngestAndRespond` passes `r.Context()` to `reingestRunner` without request metadata (`pkg/registry/server/layers.go:1921-1929`), so ingest events record no `caller.client` (D-8).

### CODE-3. `internal/serverboot`: carry `caller.client` onto read events

Target: `internal/serverboot/audit_emitter.go` (`auditEmitterFor`).

Read events (`domain.loaded`, `domains.searched`, `artifacts.searched`, `artifact.loaded`) reach the sink through `auditEmitterFor`, which copies the `AuditMeta` fields one at a time and never calls `emitAuditEvent`. Inside the `if m, ok := server.AuditMetaFromContext(ctx); ok` block, add `ev.CallerClient = m.Client`. The `sampler.Keep` check stays first and stays keyed on the event type alone (D-11). `ingestAuditEmitter` (`internal/serverboot/reingest.go:182-199`) is not changed.

### CODE-4. `cmd/podium-mcp`: send the header and record `caller.client` on local events

Targets: `cmd/podium-mcp/main.go` (`mcpServer` fields, the `initialize` handler near `:1143-1172`, `newRegistryRequest` near `:2527-2575`, `auditMeta` near `:692`, `auditLoadArtifact` near `:713`, `auditSearch` near `:759`, and their call sites near `:1303`, `:1306`, `:1913`, and `:2187`), `cmd/podium-mcp/load_domain.go:63`, and the compile-only call-site updates in `cmd/podium-mcp/audit_sink_route_test.go`, `audit_redact_test.go`, `audit_trace_redact_test.go`, and `config_env_test.go`.

1. `mcpServer` gains `hostName` and `hostVersion`. The `initialize` handler assigns them from `initParams.ClientInfo` only after `negotiateProtocol` succeeds, so a refused handshake leaves no host values. The single-threaded serve loop needs no lock for them, as for `hostSupportsRoots`.
2. Add `func (s *mcpServer) client(harness string) *audit.CallerClient`, citing `// Spec: §7.2.2`, which returns `audit.NormalizeClient(audit.CallerClient{Kind: "mcp", Name: "podium-mcp", Version: buildinfo.Version, Host: s.hostName, HostVersion: s.hostVersion, Harness: harness})`.
3. Add `func (s *mcpServer) callHarness(path string, args map[string]any) string`. It returns `harnessFromArgs(s.cfg.harness, args)` when `path` is `/v1/load_artifact` and `s.cfg.harness` otherwise.
4. `newRegistryRequest` sets `req.Header.Set(audit.ClientHeader, s.client(s.callHarness(path, args)).Header())` after the tenant header. Every control-plane request passes through it (`fetchJSONConditional`, `fetchJSON`, and the HEAD revalidation). Presigned and `/objects` fetches (`cmd/podium-mcp/large_resources.go:158`) and the probes (`cmd/podium-mcp/health.go:80`, `:121`) build their own requests and stay unchanged (D-4).
5. `auditMeta`, `auditSearch`, and `auditLoadArtifact` take a `harness string` parameter and set `CallerClient: s.client(harness)` on the event. Each call site passes the same value `callHarness` computes for the call: `s.cfg.harness` for `search_artifacts`, `search_domains`, and `load_domain`, and `harnessFromArgs(s.cfg.harness, args)` for `load_artifact`, including the overlay path at `:2187` and the cache paths.
6. Remove `"source": "mcp"` from the three local events. `auditMeta` and `auditSearch` leave `Context` nil or carry only the scrubbed query, and `auditLoadArtifact` starts from an empty map that holds only `audit_redact` fields. Update the `auditLoadArtifact` doc comment (`:711-712`) to say the event carries an empty context unless `audit_redact` applies.

### CODE-5. `pkg/sync`: send `kind=sync` with the run's harness

Targets: `pkg/sync/server.go` (`fetchServerRecords`, `serverFetcher`, `httpGetJSON`, `httpGetBody`), `pkg/sync/watch_server.go` (`runServerWatch`, `streamServerEvents`, `readServerEventStream`), and `cmd/podium/interactive.go` (`runSyncOverrideInteractive`), with the compile-only call-site updates in any `pkg/sync` test file that calls `httpGetJSON`, `httpGetBody`, `streamServerEvents`, or `readServerEventStream`. No change to `sync.Options`, `OverrideOptions`, `RenderOptions`, `override.go`, `marketplace_run.go`, or any other `cmd/podium` call site in this step. CODE-6 makes the `podium sync --preview` branch call `ClientHeaderValue`.

`pkg/sync` may import `internal/buildinfo`; it already imports `internal/clock` (`pkg/sync/override.go:9`), and `internal/buildinfo` imports nothing.

```go
// ClientHeaderValue is the §7.2.2 value podium sync sends on control-plane
// requests. An empty adapterID (a marketplace render) omits harness. It is
// exported for the one podium sync request pkg/sync does not send: the
// podium sync --preview scope preview, which cmd/podium sends (CODE-6).
//
// Spec: §7.2.2
func ClientHeaderValue(adapterID string) string {
    return audit.CallerClient{Kind: "sync", Name: "podium", Version: buildinfo.Version, Harness: adapterID}.Header()
}
```

`fetchServerRecords` computes the value once from `opts.AdapterID`, passes it to `httpGetJSON` for `/v1/sync/manifest`, and stores it on `serverFetcher` for `/v1/load_artifact`. `fetchServerRecords` is the one place every server-source load passes through, so this covers `Run`, `FetchRecords` and `Render`, and `ResolveEffectiveView`. `httpGetJSON` and `httpGetBody` gain a `clientHeader string` parameter and set the header when it is non-empty. For `/v1/events`, `runServerWatch` (`pkg/sync/watch_server.go:24`) computes the value and the subscription only forwards it. `Watch` (`pkg/sync/watch.go:52`) starts `runServerWatch` for a server source (`watch.go:74`) and is itself unchanged. `runServerWatch` already receives the `WatchOptions`, so its signature is unchanged: it takes `opts.Sync.AdapterID`, substitutes `none` when it is empty, and passes `ClientHeaderValue(adapterID)` to `streamServerEvents` at its existing call (`watch_server.go:47`) as a new `clientHeader string` parameter. `streamServerEvents` (`watch_server.go:81`) receives only scalar fields and no options value, so it cannot read `AdapterID` itself. It passes `clientHeader` to each `readServerEventStream` call (`watch_server.go:95`), and `readServerEventStream` gains the same parameter and sets the header when it is non-empty, beside the `Authorization` header. `fetchBytes` is unchanged, because it follows presigned, `/objects`, and `manifest_body_url` links (D-4).

Harness values by path: `Run` defaults `AdapterID` to `none` (`pkg/sync/sync.go:219-221`) before calling `fetchServerRecords`, so it sends `harness=none` when no harness is set. `runServerWatch` starts the subscription itself (`pkg/sync/watch_server.go:47`), outside `Run`'s defaulting: its `Run(opts.Sync)` calls (`watch_server.go:35`, `:70`) pass the options by value, so `Run` defaults only its own copy. `runServerWatch` therefore applies the same `none` default when it computes the subscription value, and both streams of one watch report the same harness. `Render` passes no `AdapterID`, so a marketplace render omits `harness`. `ResolveEffectiveView` (`pkg/sync/effective_view.go:25`) reaches `fetchServerRecords` through `resolveRecords` (`pkg/sync/sync.go:503`) with no defaulting, so it sends the `AdapterID` its caller passes.

The interactive `podium sync override` (no `--add`, `--remove`, or `--reset`) is the one production `ResolveEffectiveView` caller (`cmd/podium/interactive.go:300-304`). Today it passes no `AdapterID` to the view and passes `resolveOverrideHarness(harnessFlag, target)` (`cmd/podium/main.go:901-917`, which falls back to `none`) to the `Override` apply step (`interactive.go:332`). Left as is, one invocation would record its view loads with no `harness` and its apply loads with one, although SPEC-1 exempts only a marketplace render from the member. `runSyncOverrideInteractive` therefore computes `harness := resolveOverrideHarness(harnessFlag, target)` once, before `ResolveEffectiveView`, and passes it as `AdapterID` to both the `ResolveEffectiveView` options and the `Override` options. `ResolveEffectiveView` reads `AdapterID` only on the way to `fetchServerRecords`, so the view it lists does not change.

### CODE-6. `cmd/podium` CLI: send `kind=cli` on control-plane requests

Targets: `cmd/podium/main.go` (`mustGetJSON` near `:1610-1640`, and the `--preview` branch of `syncCmd` near `:340-344`), `cmd/podium/layer.go` (`doJSON` near `:667-690`), `cmd/podium/scope_preview.go` (`fetchScopePreview` near `:44-74` and `runScopePreview` near `:80-81`), `cmd/podium/status.go` (the `fetchScopePreview` call near `:103`), the compile-only call-site updates in `cmd/podium/scope_preview_test.go` (`:33`, `:53`, and `:156`), and a new `cmd/podium/client_header_test.go`.

These three helpers are the only request sites in `cmd/podium` that reach `/v1/` routes. `fetchManifestDocument` (`cmd/podium/manifest_body.go:86-97`) follows a presigned link, `status.go` calls `/healthz` (`cmd/podium/status.go:80`), and `login.go` talks to the identity provider, so D-4 excludes all of them.

```go
// cliClientHeader is the §7.2.2 value the podium CLI sends on a
// control-plane request.
//
// Spec: §7.2.2
func cliClientHeader() string {
    return audit.CallerClient{Kind: "cli", Name: "podium", Version: buildinfo.Version}.Header()
}
```

`mustGetJSON` and `doJSON` call `req.Header.Set(audit.ClientHeader, cliClientHeader())` right after the `Authorization` block. `cmd/podium` already imports `internal/buildinfo` (`cmd/podium/main.go:26`).

`fetchScopePreview` serves two commands, so the caller names the client. `podium sync --preview` reaches it through `runScopePreview` (`cmd/podium/main.go:340-344`, `cmd/podium/scope_preview.go:80-81`), and `podium status` calls it directly (`cmd/podium/status.go:103`). §7.2.2 requires the first to identify as `podium sync` and the second as the CLI. `fetchScopePreview(registry, clientHeader string)` and `runScopePreview(registry string, asJSON bool, clientHeader string)` gain a `clientHeader` parameter, and `fetchScopePreview` sets it, when non-empty, right after the `Authorization` block. The `--preview` branch of `syncCmd` passes `sync.ClientHeaderValue(resolved.Harness)` (CODE-5). `resolved.Harness` falls back to `none` when no flag, `PODIUM_HARNESS`, or `sync.yaml` value names one (`pkg/sync/resolve.go:243`), so the preview sends the same `harness` a `podium sync` run with the same flags sends. `podium status` passes `cliClientHeader()`. The existing tests in `scope_preview_test.go` pass `cliClientHeader()`. The `kind=sync` value is built only in `pkg/sync` (CODE-5), and the `kind=cli` value only in `cliClientHeader`.

`client_header_test.go` (`// Spec: §7.2.2`) runs against an `httptest` server that records the path and `X-Podium-Client` of every request, and parses each received header with `audit.ParseClientHeader`. It sets `HOME` to a temporary directory and `PODIUM_HARNESS` to empty and runs inside `withCwd` on a temporary directory, so no ambient `sync.yaml` or harness reaches the resolution. It asserts:

- One `mustGetJSON` and one `doJSON` call each send `{Kind: cli, Name: podium, Version: buildinfo.Version}` with no `harness`.
- `statusCmd(nil)` with `PODIUM_REGISTRY` set to the stub sends that `kind=cli` value on `/v1/scope/preview` and no `X-Podium-Client` on `/healthz`.
- `syncCmd([]string{"--preview", "--registry", srv.URL, "--harness", "cursor"})` sends `{Kind: sync, Name: podium, Version: buildinfo.Version, Harness: cursor}` on `/v1/scope/preview`, and the same call without `--harness` sends `Harness: none`.
- `fetchManifestDocument` sends no `X-Podium-Client`.

### CODE-7. SDKs: send `kind=sdk` on control-plane requests only

Targets: `sdks/podium-py/podium/client.py` (`_headers` near `:830`, and the call sites near `:1522`, `:1585`, and `:1614`; the link fetcher near `:1456` is unchanged) and `sdks/podium-ts/src/index.ts` (`headers` near `:997`, and the call sites near `:1491`, `:1552`, and `:1600`; the manifest-link fetch near `:1442-1443` and the large-resource fetch near `:502` are unchanged).

Both SDKs share one header builder between API calls and non-SigV4 link fetches (`client.py:1456`, `index.ts:1443`), so the API sites need their own builder. Each SDK has exactly three `/v1` funnels.

Python:

- Resolve the version at module scope in `client.py`: `try: from ._version import __version__ as _SDK_VERSION` with `except ImportError: _SDK_VERSION = ""`. This mirrors `podium/__init__.py:13-19` and reuses the setuptools-scm `version_file`. Add no `importlib.metadata` lookup.
- Add the pure module-level function `_client_header(version: str) -> str`, which returns `urllib.parse.urlencode` over `{"kind": "sdk", "name": "podium-py"}` plus `version` when it is non-empty.
- Add `_api_headers(self, extra=None)` beside `_headers`. It returns `self._headers(extra)` plus `X-Podium-Client: _client_header(_SDK_VERSION)`.
- Switch exactly three sites to `_api_headers`: `_post_raw` (`batchLoad`), `/v1/events`, and `_get_raw`. Leave the link fetcher and `_oauth.py` unchanged.

TypeScript:

- Add the exported pure function `clientHeader(version: string): string`, which returns `new URLSearchParams({kind: "sdk", name: "podium-ts", ...(version ? {version} : {})}).toString()`.
- Add a cached async `sdkVersion(): Promise<string>`. It loads `node:fs/promises` through a guarded dynamic import, as `config.ts:274-276` does, reads `new URL("../package.json", import.meta.url)`, and returns its `version`. It resolves to `""` on any failure, so a non-Node runtime sends no `version` member and the request proceeds. Add no static `node:` import.
- Add `private async apiHeaders(extra?)`, which returns `{...this.headers(extra), "X-Podium-Client": clientHeader(await sdkVersion())}`. Switch exactly three sites to it: `batchLoad`, `events`, and `getRaw`, which are already async. Leave the manifest-link fetch, the large-resource fetch, and `oauth.ts` unchanged.

### CODE-8. `pkg/tracing`: scrub URL query components from every exported span

Targets: `pkg/tracing/tracing.go` (`Init`, new unexported `newProvider`) and `pkg/tracing/scrub.go` (new).

Every production tracer provider is built in `pkg/tracing` (`tracing.go:56-62`, the only non-test `NewTracerProvider` and `SetTracerProvider`). Wrapping the exporter there covers `podium-mcp`'s otelhttp client, the registry server handler, and any future instrumentation.

```go
// newProvider returns the tracer provider Init installs: a batch processor
// over exp, wrapped so exported spans carry no URL query (§13.8), under the
// named service.
func newProvider(exp sdktrace.SpanExporter, service string) *sdktrace.TracerProvider {
    return sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(scrubExporter{exp}),
        sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
    )
}
```

`Init` calls `newProvider` and keeps its `otel.SetTracerProvider` call.

`scrub.go`, citing `// Spec: §13.8`:

- `type scrubExporter struct{ sdktrace.SpanExporter }`. `ExportSpans(ctx, spans)` maps each span to `scrubbedSpan{ReadOnlySpan: s, attrs: scrubAttrs(s.Attributes())}` and delegates. `Shutdown` and `ForceFlush` delegate through the embedded exporter.
- `type scrubbedSpan struct { sdktrace.ReadOnlySpan; attrs []attribute.KeyValue }` overrides `Attributes()` only. Embedding the interface supplies the unexported `private()` method and every other field unchanged.
- A comment states three things. A span processor cannot do the scrub, because its `OnStart` runs before otelhttp sets `url.full` and `OnEnd` receives a read-only span. Only attributes are rewritten, because no producer writes a URL query into a span's name, status description, events, or links (D-13). An error returned by `http.Client.Do` must not be recorded on a span.
- `scrubAttrs` handles every attribute whose value is a string that starts with `http://` or `https://`, compared case-insensitively. It replaces that value with its prefix before the first `?` or `#`. It leaves every other attribute unchanged, including non-URL strings that contain `?`. In an absolute URL the first `?` or `#` begins the query or the fragment (RFC 3986 §3), so the cut needs no parse and has no failure branch.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text | Docs page |
|:--|:--|:--|:--|
| A request carries no `X-Podium-Client` (a third-party client, `curl`, the web UI) | Served normally; the event records no `caller.client` | §7.2.2 "An absent or ignored value never causes the request to fail." (SPEC-1) | `docs/reference/http-api.md` "Client identification" (DOC-1 (d)) |
| The header exceeds 1024 bytes, or `url.ParseQuery` fails on it | Served normally; no `caller.client` | §7.2.2 "The registry ignores a value longer than 1024 bytes and a value that does not parse." (SPEC-1) | `docs/reference/http-api.md` (DOC-1 (c)) |
| A member repeats, or an unknown member is present | The first occurrence is recorded; unknown members are dropped | §7.2.2 "When a member repeats, the registry uses the first occurrence, and it ignores unknown members." (SPEC-1) | `docs/reference/http-api.md` (DOC-1 (c)) |
| A member holds control bytes, ANSI escapes, NUL, non-ASCII, `@`, or more than 64 bytes | Each such byte becomes `_`, and the member is cut to 64 bytes | §7.2.2 "replaces every byte other than ... with `_`, and then truncates the member to 64 bytes" (SPEC-1) | `docs/reference/http-api.md` (DOC-1 (c)) |
| `kind` is absent, `MCP`, or `agent`, and another member is present | `caller.client.kind` is `other` | §7.2.2 "a `kind` that is absent, or that is not exactly one of the values above, becomes `other`" (SPEC-1) | `docs/reference/http-api.md` (DOC-1 (c)) |
| Every member is empty after sanitization (`kind=&name=`) | No `caller.client` | §7.2.2 "A value that leaves no non-empty member is treated as absent." (SPEC-1) | `docs/reference/http-api.md` (DOC-1 (c)) |
| A client reports a false name, host, or harness | Recorded as reported; no access, quota, sampling, or tenant decision changes | §7.2.2 "It verifies nothing about the value. The value is never an input to ..." (SPEC-1); §8.1 "`caller.client` records what the client reported. It is not an identity attribute" (SPEC-2) | `docs/reference/http-api.md` (DOC-1 (d)); `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| A client puts personal data in a member, using only the allowed characters | Recorded as sanitized; the §8.2 query scrub and §8.5 erasure do not rewrite it | §8.1 "records what the client reported" (SPEC-2); §8.2 scopes the scrub to search query text (unchanged) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| A reingest or webhook ingest request carries the header | `artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass` record no `caller.client` | §8.1 "An event the ingest pipeline raises (...) records no `caller.client`, even when a request triggered the ingest" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| An event raised without a request (scheduler, anchoring, retention, read-only transition) | No `caller.client` | §8.1 "and neither does an event raised without a request" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| An erase request with the header, on a file sink with anchoring on (`PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0) | `user.erased` records `caller.client`; the `audit.anchored` appended after the rewrite records none | §8.1 "An event about the registry as a whole, such as `audit.anchored` (§8.6), records no `caller.client`, including the `audit.anchored` the registry appends after a §8.5 erasure" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| `podium admin erase --local` writes `user.erased` | No `caller.client`, because no request is served | §8.1 "an event raised without a request" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| `podium-mcp` serves a load from the workspace overlay, or a cache hit in `offline-first` or `offline-only` mode | The local event records `caller.client`; the registry receives no request, and its stream has no event for that load | §8.1 "They also record it when the call sends no registry request: a load served from the workspace overlay (§6.4), or from the content cache without revalidation in the `offline-first` or `offline-only` cache mode (§6.5)." (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| `podium-mcp` serves a cache hit after revalidating it: a HEAD under the default `always-revalidate` mode, or a conditional GET answered 304 | The revalidation request carries `X-Podium-Client`; the registry records `artifact.loaded` with `caller.client`, and the local event records the same value | §8.1 "which is the value it sends to the registry when the call reaches the registry" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| `podium-mcp` runs `search_artifacts` with a `harness` argument | `harness` is `PODIUM_HARNESS`; only `load_artifact` honors the per-call argument | §7.2.2 "replaced on a `load_artifact` request by that call's `harness` argument (§6.7)" (SPEC-1) | `docs/reference/http-api.md` links to §7.2.2 (DOC-1 (f)) |
| A consumer filters local events on `context.source` | The key is gone; `caller.client.kind` is `mcp` | §8.1 local-stream sentence (SPEC-2) | CHANGELOG Changed entry (DOC-3) |
| An existing audit log written before the change | Verifies unchanged; a log that mixes old and new records verifies | §8.1 "an absent `caller.client` adds nothing to that body" (SPEC-2) | `docs/deployment/operator-guide.md` "Audit records" (DOC-2b) |
| A local event exceeds 512 bytes on macOS | Concurrent local writers may interleave a record, as they can today without `caller.client` | §9.1 `LocalAuditSink` row (unchanged); accepted and deferred | none; D-6 records the bound |
| The audit endpoint returns non-2xx or times out after 10 seconds | The event is dropped with no retry or buffer; the receiver's next record names a `prev_hash` it never received | Unchanged §8.3 "can be redirected to external SIEM"; accepted current behavior | `docs/deployment/operator-guide.md` "Audit records" (DOC-2a) |
| A process restarts, or several clustered replicas forward to one receiver | Each process starts a new chain whose first record has no `prev_hash`, and each replica sends its own chain | §8.6 (unchanged); accepted current behavior | `docs/deployment/operator-guide.md` "Audit records" (DOC-2a) |
| `PODIUM_TRACING=stdout` or OTLP export with search, `session_id`, or a presigned URL | `url.full`, and any other attribute that holds an `http` or `https` URL, carries scheme, host, and path only | §13.8 "A span that Podium exports carries no URL query component ..." (SPEC-4) | `docs/deployment/operator-guide.md` "Monitoring" (DOC-2b) |
| A registry call or a presigned fetch fails in transport (unreachable host, refused connection) | The client span ends with status `Error`. Its description is the transport error, which names the host and port and no URL query, and its `url.full` carries no query | §13.8 "... in its name, attributes, events, links, or status description" (SPEC-4); D-13 | `docs/deployment/operator-guide.md` "Monitoring" (DOC-2b) |
| `PODIUM_TRACING=stdout` in `podium-mcp` writes spans onto the MCP stdio channel | Unchanged; deferred (see Non-goals) | §13.8 "`PODIUM_TRACING=stdout` for local inspection" (unchanged) | none |
| `X-Forwarded-User` in `caller.network` is long or holds control bytes | Recorded verbatim, as today; deferred (see Non-goals) | §8.1 "any upstream `X-Forwarded-User` header preserved in `caller.network`" (unchanged) | none |

## Testing

Each test cites its spec section. CODE-6 carries its own `cmd/podium` unit test, described in CODE-6.

### TEST-1. `pkg/audit` unit tests for the codec, hashing, and wire form

Targets: `pkg/audit/client_test.go` (new), `pkg/audit/caller_fields_test.go`, `pkg/audit/endpoint_test.go`, and `pkg/audit/retention_test.go`. Extend the existing test functions in place; add no parallel functions.

- `client_test.go` (`// Spec: §7.2.2, §8.1`), table tests for `ParseClientHeader`, `NormalizeClient`, and `CallerClient.Header`:
  - `Header()` followed by `ParseClientHeader` round-trips every member.
  - A 1024-byte value parses, and a 1025-byte value returns nil.
  - `%zz` returns nil.
  - A repeated `kind` keeps the first value. An unknown member is ignored.
  - `host=%00%1b[31m%0a@x` normalizes to `___31m__x`, with no byte below 0x20 and no `@`, `=`, or `[`.
  - A 200-byte member is truncated to 64 bytes.
  - `kind=MCP`, `kind=agent`, and an absent kind with `host` present each record `other`.
  - `kind=&name=` returns nil, and `NormalizeClient(CallerClient{})` returns nil.
- `caller_fields_test.go`:
  - `TestCanonicalBody_IncludesCallerFields`: one row per member, from `caller_client_kind` to `caller_client_harness`; each row sets one member and must change the canonical body.
  - `TestEvent_UnlabeledHashUnchanged`: keep the existing `legacyUnlabeledLine` pin as the hash-compatibility guard. After its `Verify`, append one event with a `CallerClient` to the same `FileSink` and verify again, which covers a log that mixes old and new records.
  - `TestFileSink_CallerDottedKeysDecode` or `TestFileSink_CallerFieldsRoundTrip`: the key path `caller.client.host_version` is present, and `caller.client` is absent when `CallerClient` is nil.
  - A new case: an event whose only caller attribute is `CallerClient` still serializes `caller.client` and passes `Verify`. This guards the `callerForJSON` nil check.
  - `TestFileSink_TamperedCallerEmailBreaksChain`: add a table row that edits `caller.client.host` on disk and expects `ErrChainBroken`.
  - A new case: an event whose `CallerClient.Host` holds a NUL byte, appended through `FileSink.Append` and `Memory.Append`, stores the sanitized value and verifies (D-9).
- `endpoint_test.go` `TestEndpointSink_ForwardsJSON`: add `Caller.Client` to the decoded wire struct and set a `CallerClient` on one appended event. Add a row (`// Spec: §7.2.2, §8.1`) that appends, through `EndpointSink.Append`, an event whose `CallerClient` has `Kind: "MCP"`, a `Host` holding a NUL byte and an ESC byte, and a 200-byte `Name`. Assert that the POSTed body's `caller.client` has `kind` `other`, a `host` with no byte below 0x20, and a 64-byte `name`, and that the body's `hash` equals the hash of the sanitized event. This pins D-9 on the endpoint sink, which hashes and serializes in its own `Append` (`pkg/audit/endpoint.go:55-68`).
- `retention_test.go` (`// Spec: §8.1, §8.5`):
  - One `EraseUser` assertion that `caller.client` survives on a redacted record.
  - A `CallerClient` passed as `EraseUser`'s `client` argument, with a NUL byte in `Host`, appears sanitized on the appended `user.erased`, and the rewritten file passes `Verify`.
  - Extend `TestEraseUser_ZeroScopeRefused`: a call with `EraseScope{}` and a non-nil `client` still returns `ErrEraseScope`, and the file is unchanged. This pins that the client cannot widen the scope guard.

### TEST-2. Registry integration tests through the serverboot mux

Targets: `pkg/registry/server/audit_context_test.go`, `internal/serverboot/audit_emitter_enrich_test.go`, `internal/serverboot/erase_reanchor_test.go`, and `internal/serverboot/client_header_guard_test.go` (new). Cite `// Spec: §7.2.2, §8.1`.

- (a) `audit_context_test.go`: `auditMetaFrom` sets `Client` from a valid header for an authenticated and a public caller, and leaves it nil when the header is absent, 1025 bytes long, or `%zz`. `emitAuditEvent` copies `Client` onto the event.
- (b) `audit_emitter_enrich_test.go`: extend `newReadEventServer` and `drive` to send `X-Podium-Client: kind=mcp&host=claude-code&harness=cursor`. Assert that `artifacts.searched` carries `caller.client` beside the scrubbed query. With a 2 KB header and with `%zz`, assert status 200 and no `caller.client`.
- (c) Layer write event: drive a layer register or update through the serverboot mux (the `TenantRouted` `/v1/layers` mount) with the header, and assert that `layer.config_changed` or `layer.user_registered` carries `caller.client`. This covers the `auditMetaFrom` fallback in `LayerEndpoint.emitAudit`.
- (d) Reingest: configure a registry signer (`PODIUM_SIGN=registry-key` with `PODIUM_SIGN_KEY_PATH`, as `internal/serverboot/signpass_test.go:39-40` sets them) over a layer that holds a `DOMAIN.md` and one artifact. Drive `POST /v1/layers/reingest` through the same mux with the header, then add a new version of the artifact with `deprecated: true` and reingest again with the header. Assert that `artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, and `layer.ingested` carry no `caller.client`, and that each of those event types appeared at least once, so an empty stream cannot pass. This pins the §8.1 ingest sentence.
- (e) Erasure: drive `/v1/admin/erase` with the header against a registry with a file sink and against one with an endpoint sink. Assert that `user.erased` carries `caller.client` on both. Run the file-sink case with anchoring on by extending `TestRun_EraseReAnchorsTheRewrittenChain` (`internal/serverboot/erase_reanchor_test.go:140`), which sets `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0 and `PODIUM_AUDIT_SIGNING_KEY_PATH`: `postErase` (`:125`) sends the header, and `auditLine` (`:93`) gains the `caller.client` key. Assert that `user.erased` carries `caller.client` and that the `audit.anchored` record over the `user.erased` head, which the test already requires, carries none. This pins the §8.1 sentence on events about the registry as a whole.
- (f) `client_header_guard_test.go`: a source guard over `go list -f '{{.Dir}}{{range .GoFiles}} {{.}}{{end}}' github.com/lennylabs/podium/...`, which enumerates the whole module from the test's package directory. The guard first asserts that the enumeration contains `cmd/podium-mcp/main.go` and `pkg/registry/server/audit_context.go`, so an empty or narrowed scan fails. It then fails when a non-test file outside `pkg/audit` and `pkg/registry/server/audit_context.go` references `ParseClientHeader` or reads the header with `Header.Get(audit.ClientHeader)`. It pins D-5 structurally: no tenant, visibility, quota, or sampling path can read the value.

### TEST-3. `podium-mcp` header and local-stream tests

Targets: `cmd/podium-mcp/client_identification_test.go` (new), `cmd/podium-mcp/fetch_json_test.go`, and `cmd/podium-mcp/client_version_test.go` (generalize `initWithClient` to take a name and a version). Cite `// Spec: §7.2.2, §8.1, §8.3`.

- (1) Extend `TestFetchJSON_AttachesAuthAndTenant`. With no `initialize`, `X-Podium-Client` parses to `kind=mcp`, `name=podium-mcp`, `version=buildinfo.Version`, and `harness=cfg.harness`, with no `host` or `host_version`. A `/v1/load_artifact` request with `harness=cursor` in `args` sends `harness=cursor`. A `/v1/search_artifacts` request with `harness=cursor` in `args` sends `cfg.harness`. A HEAD built by `headContentHash` for `/v1/load_artifact` carries the same header as the GET, which pins the revalidated cache-hit row of the edge-case table.
- (2) In `client_identification_test.go`, drive `initialize` through `initWithClient` with `clientInfo` `{name: "claude-code", version: "1.0.33"}`, then call `search_artifacts` through `callTool` against an `httptest` stub that records headers. Assert `host=claude-code` and `host_version=1.0.33`. With `clientInfo.name` `evil\u001b[2J\n@x`, assert that the header member and the local `FileSink` `caller.client.host` hold no byte below 0x20 and no `@`.
- (3) An `initialize` that `negotiateProtocol` refuses leaves `hostName` empty, and a later call sends no `host`.
- (4) Local `FileSink` events from `search_artifacts`, `load_domain`, and `load_artifact` carry `caller.client` with the members the stub received, and none has a `context.source` key. An overlay `load_artifact` writes a local event with `caller.client` and sends no request to the stub.
- (5) A SigV4 presigned large-resource fetch (`getLargeResource`) and the `/readyz` probe send no `X-Podium-Client`.

`TestMCPCallTool_SearchQueryScrubbed` (`audit_redact_test.go:43`) and the harness-query assertion in `fetch_json_test.go:43` already pin the scrubbed query and the dropped `harness` query parameter, and they stay.

### TEST-4. `pkg/sync` header tests

Extend the existing header tests and reuse their stubs. Cite `// Spec: §7.2.2, §7.5`.

- (1) `server_auth_overlay_test.go`, beside `TestRun_ServerSource_ForwardsBearerToken`: a server-source `Run` with `AdapterID` `claude-code`. Parse the header received on `/v1/sync/manifest` and `/v1/load_artifact` with `audit.ParseClientHeader` and assert `{Kind: sync, Name: podium, Version: buildinfo.Version, Harness: claude-code}`. A second run with no `AdapterID` asserts `Harness: none`.
- (2) `watch_server_auth_test.go`, beside `TestWatch_ServerSource_SubscriptionCarriesBearerToken`: call `Watch()` with `AdapterID` `claude-code` and assert the `/v1/events` subscription header carries `Harness: claude-code`. Call it again with no `AdapterID` and assert `Harness: none`. This pins the default `runServerWatch` applies before `streamServerEvents`, which `Run`'s defaulting never reaches, through the public `Watch` entry point.
- (3) `server_source_test.go`, in the large-resource and `fetchBytes` tests: no `X-Podium-Client` reaches an `/objects/{hash}` link, a SigV4 presigned URL, or a `manifest_body_url` link.
- (4) A `Render` (through `FetchRecords` with no `AdapterID`) sends the header with no `harness` member.
- (5) `server_auth_overlay_test.go`, beside case (1): `ResolveEffectiveView` with `AdapterID` `claude-code`, against a stub built as `TestRun_ServerSource_ForwardsBearerToken` builds it (`pkg/sync/server_auth_overlay_test.go:17-33`), sends `Harness: claude-code` on `/v1/sync/manifest` and on every `/v1/load_artifact`. This pins that `pkg/sync` forwards a harness it is given; case (6) pins that the interactive `podium sync override` gives it one.
- (6) `cmd/podium/interactive_test.go`, beside `TestRunSyncOverrideInteractive_AddsAndMaterializes`, a new `TestRunSyncOverrideInteractive_SendsResolvedHarness` (`// Spec: §7.2.2, §7.5.5`). This case drives the CODE-5 edit to `runSyncOverrideInteractive`, which the existing interactive tests cannot observe because they use a filesystem registry (`cmd/podium/interactive_test.go:258-284`, `:292-310`) and send no HTTP request. An `httptest` registry stub serves `/v1/sync/manifest` with one context artifact and `/v1/load_artifact` with a record sealed by `testharness.SealDelivery`, as `pkg/sync/server_auth_overlay_test.go:17-33` builds them, and records the path and `X-Podium-Client` of every request. The test sets `PODIUM_VERIFY_SIGNATURES=never` so that `newSyncDeliveryCheck` (`cmd/podium/sync_delivery.go:20`) resolves without key material, sets `PODIUM_HARNESS` to empty and `HOME` to a temporary directory so that `resolveOverrideHarness` reads no ambient value, and runs inside `withCwd` on a temporary directory. It feeds `1\nsave\n` through `withInteractiveStdin(t, ..., true)` and calls `runSyncOverrideInteractive(target, srv.URL, "cursor", false)` against a target with no lock. It asserts exit status 0, at least two `/v1/sync/manifest` requests (one from `ResolveEffectiveView` and one from the `Override` re-materialization, `pkg/sync/override.go:156-158`), and that every recorded header parses with `audit.ParseClientHeader` to `Harness: cursor`, so the view requests sent before the apply carry it. A second case passes an empty harness flag and asserts `Harness: none` on every request, which is the `resolveOverrideHarness` fallback (`cmd/podium/main.go:901-917`) for a target with no lock.

### TEST-5. End-to-end: real binaries record `caller.client` in both audit streams

Targets: `test/e2e/client_identification_test.go` (new, feature-named) and `test/e2e/audit_sink_redirect_test.go`. Cite `// Spec: §8.1, §8.3, §8.6`.

- (a) `TestClientIdentification_MCPBothStreams`: start a registry with `startServerArgs(..., "serve", "--standalone", "--layer-path", reg)` and `PODIUM_AUDIT_LOG_PATH` set to a temporary file. Run `mcpExec` with `PODIUM_REGISTRY=srv.BaseURL`, `PODIUM_HARNESS=claude-code`, and `PODIUM_AUDIT_SINK` set to a temporary file, sending `initialize` with `clientInfo` `{name: "cursor", version: "2.0"}`, then `search_artifacts`, then `load_artifact`. Assert that the registry file's `artifacts.searched` and `artifact.loaded` records carry `caller.client` `{kind: mcp, name: podium-mcp, version: buildinfo.Version, host: cursor, host_version: "2.0", harness: claude-code}`, that the local records carry the identical object and no `context.source`, and that `audit.NewFileSink(path).Verify(ctx)` passes on both files.
- (b) `TestClientIdentification_SyncAndCLI`: against a standalone registry with a file audit log, run `podium sync --registry <url> --harness claude-code` and `podium search`. Assert one `artifact.loaded` with `caller.client.kind=sync` and `harness=claude-code`, and one `artifacts.searched` with `kind=cli` and no `harness`.
- (c) `TestAuditSink_RegistryEndpointRedirect`: send the `domain.loaded` read with `X-Podium-Client: kind=cli&name=podium`, and assert that the received POST body decodes as one JSON object whose `caller.client.kind` is `cli`.

Run `go test -v ./test/e2e/ -run 'ClientIdentification|AuditSink_RegistryEndpointRedirect'` and grep the output for `SKIP` before claiming verification.

### TEST-6. SDK header tests

Targets: `sdks/podium-py/tests/conftest.py`, `sdks/podium-py/tests/test_client.py`, and `sdks/podium-ts/src/index.test.ts`. Create no new test files and no new stub servers.

Python (each test annotated `# Spec: §7.2.2, §7.6`):

- Extend `_StubHandler` so `do_GET` and `do_POST` both append `self.headers.get("X-Podium-Client")` to `server.clients`, and extend `_ObjectHandler` to record the header beside `auths`.
- (a) `search_artifacts`, `load_artifact`, batch load (the POST path), and the `/v1/events` subscription each send a header that `parse_qs` reads as `kind=sdk`, `name=podium-py`, and `version` equal to `podium._version.__version__`. The subscription case extends `test_subscribe_sends_repeated_type_params` (`tests/test_client.py:759`), which already drives `/v1/events` through the stub.
- (b) `_client_header("")` carries no `version` member.
- (c) Reuse `_serve_manifest_body_url` to show that the non-SigV4 `/objects` link and the SigV4 link both record `None` for `X-Podium-Client`. The `/objects` case still sends `Authorization`, which shows the two builders differ.
- (d) A large-resource fetch through the `fetch=` hook receives no `X-Podium-Client`.

TypeScript (`// Spec: §7.2.2, §7.6`):

- Extend `objectRoute` to record `X-Podium-Client` beside `auths`, and add cases that mirror (a), (c), and (d) through the injected fetcher, asserting `name=podium-ts` and the `package.json` version. The (a) mirror covers the `/v1/events` subscription by extending the "subscribe sends repeated type params" test (`src/index.test.ts:499`) so its fetcher also records `X-Podium-Client` from `init.headers`.
- Unit-test that `clientHeader("")` omits `version`.
- (e) The version-lookup failure path. `sdkVersion` caches its result in module state, so the case calls `vi.resetModules()`, registers `vi.doMock("node:fs/promises", ...)` with a factory that spreads the real module and replaces `readFile` with one that rejects for a path ending in `package.json` and forwards every other path to the real `readFile`, and then loads a fresh `Client` through `await import("./index.js")`. Forwarding the other paths keeps the client's §4.7.9 verification resolution, which reads the sync.yaml scopes (`src/config.ts:95`) and the key file (`src/config.ts:253`) through the same module, unaffected. The case runs `searchArtifacts`, which reaches `getRaw` through `get` (`src/index.ts:1583-1584`), through an injected fetcher and asserts that the call resolves and that the recorded `X-Podium-Client` reads through `URLSearchParams` as `kind=sdk` and `name=podium-ts` with no `version` member. A `finally` block calls `vi.doUnmock("node:fs/promises")` and `vi.resetModules()` so later cases load the real module. The case pins the fail-open branch that keeps all three `/v1` funnels working when the version cannot be read; a rejecting `sdkVersion` would fail the call.

The existing `Authorization` tests (`test_requests_attach_bearer_token`, `test_no_token_sends_no_auth_header`, the SigV4 no-credential test, and `index.test.ts:258-263`) stay unchanged.

### TEST-7. Tracing: export-time scrub over every span field, on success and error paths

Targets: `pkg/tracing/scrub_test.go` (new) and `test/integration/mcp_trace_scrub_test.go` (new). Cite `// Spec: §13.8`. `cmd/podium-mcp/tracing_test.go` is unchanged.

- **`scrub_test.go`, unit table for `scrubAttrs`.**
  - `url.full` becomes `scheme://host/path` in each case: with a query, with a fragment, with `#` before `?`, and with a SigV4 query (`X-Amz-Credential`, `X-Amz-Signature`).
  - An `HTTPS://` value is cut.
  - A URL value under an unrelated key (`example.link`) is cut. This pins the key-agnostic rule.
  - A non-URL string that contains `?` and a non-string attribute are unchanged.
- **`scrub_test.go`, `TestProvider_ScrubsEveryExportedField`.** This is a provider-level test.
  - Setup: a provider from `newProvider` over `tracetest.NewInMemoryExporter`, shut down on cleanup, and an `http.Client` with `otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp))`. The option scopes the provider to this transport, so the test changes no global state.
  - Requests:
    1. A GET to an `httptest` server at `/v1/search_artifacts?query=QMARK&session_id=SMARK`, answered 200, with the body read and closed.
    2. The same path and query against a closed listener, so the transport fails. Use `httptest.NewServer`, record its URL, and `Close` it.
    3. A GET to a closed listener at `/obj?X-Amz-Credential=CMARK&X-Amz-Signature=SIGMARK`, which is the presigned-fetch error path.
  - Call `ForceFlush`. A helper collects every string from each `SpanStub`: the name, each string attribute value, the status description, each event name and string event attribute, and each string link attribute.
  - Assert that no collected string contains `QMARK`, `SMARK`, `CMARK`, `SIGMARK`, or `?`.
  - Positive controls: span 1 has `url.full` equal to `ts.URL + "/v1/search_artifacts"`. Spans 2 and 3 have status code `codes.Error` and a non-empty description, so the error path ran and its description was inspected.
- **`test/integration/mcp_trace_scrub_test.go`, `TestMCPTrace_OTLPExportCarriesNoQuery`.**
  - Build `podium-mcp`. Point `OTEL_EXPORTER_OTLP_ENDPOINT` at an `httptest` collector that records request bodies.
  - Run 1 points `PODIUM_REGISTRY` at a stub registry. The stub answers `search_artifacts` with an empty result. It answers `load_artifact` with a response whose `large_resources` map holds one entry with a `presigned_url` of `http://<closed listener>/obj?X-Amz-Credential=CMARK&X-Amz-Signature=SIGMARK`. `fetchLargeResources` runs before delivery verification (`cmd/podium-mcp/main.go:2052-2058`), so the response needs no sealed delivery record. Send `initialize`, a `search_artifacts` with `QMARK`, and that `load_artifact`. The presigned fetch fails after its three attempts, with about 0.75 s of backoff (`cmd/podium-mcp/large_resources.go:83-114`). Then close stdin so the deferred `shutdownTracing` flushes.
  - Run 2 points `PODIUM_REGISTRY` at a closed listener and sends `initialize` and a `search_artifacts` with `RMARK`.
  - Assert that no recorded body contains any marker as raw bytes. otlptracehttp sends uncompressed protobuf by default (`otlptracehttp/internal/otlpconfig/options.go:86`), and protobuf stores strings verbatim, so the search covers every field of every span with no field list.
  - Positive controls: decode each body as `collector/trace/v1.ExportTraceServiceRequest`. Run 1 must hold a client span whose `url.full` is the stub URL plus `/v1/search_artifacts`, and a client span with `STATUS_CODE_ERROR` for the presigned host. Run 2 must hold a client span with `STATUS_CODE_ERROR` and a non-empty message. A compressed body fails the decode, so the raw-bytes search cannot pass on bytes it could not read.
  - The import makes `go.opentelemetry.io/proto/otlp` a direct dependency in `go.mod`. It is already in the module graph at v1.11.0.

### Commands per step

- S1 to S3: `make coverage-gate`.
- S5, S6: `go build ./...` and `go test ./pkg/audit/`.
- S7 to S9: `go test ./pkg/registry/server/ ./internal/serverboot/`.
- S10, S11: `go test ./cmd/podium-mcp/`.
- S12, S13: `go test ./pkg/sync/ ./cmd/podium/`, which runs TEST-4 (6) in `cmd/podium`.
- S14: `go test ./cmd/podium/ -run 'ClientHeader|ScopePreview|SyncPreview|Status'`.
- S15, S16: `cd sdks/podium-py && pytest`, and `cd sdks/podium-ts && npm test`.
- S17: `GOCOVERDIR=$(mktemp -d) go test -v ./test/e2e/ -run 'ClientIdentification|AuditSink_RegistryEndpointRedirect'`, grep for `SKIP`, then `go tool covdata textfmt` and `go tool cover -func`.
- S18, S19: `go test ./pkg/tracing/ ./test/integration/ -run 'Scrub|Tracing|MCPTrace'`, then `go test ./cmd/podium-mcp/ -run Tracing` to confirm the unchanged tracing tests still pass.
- S20: run S104 by hand.
- S4, S21, S22: `cd site && npm run build`, and `make doccov-check` to confirm no page became runnable without a covering test.
- Closing step: `go test -coverpkg=./... -coverprofile=cover.out ./pkg/audit/ ./pkg/registry/server/ ./internal/serverboot/ ./pkg/sync/ ./pkg/tracing/ ./cmd/podium-mcp/ ./cmd/podium/ ./test/integration/`, then `go tool cover -func=cover.out`, and confirm that `ParseClientHeader`, `NormalizeClient`, `CallerClient.Header`, `canonicalBody`, `callerForJSON`, `eventFromJSON`, `auditMetaFrom`, `EraseUser`, `eraseAudit`, `ClientHeaderValue`, `cliClientHeader`, `fetchScopePreview`, `scrubAttrs`, and `scrubExporter.ExportSpans` reach 85%. Then `make lint` and `make coverage-gate`.

## Manual validation

**MV-1.** `test/manual-validation.md`: add S104 after S103, and its index row after the S103 row. The scenario number S104 and the ports 8208, 8905, 8906, and 8907 are unused today. Step 3 relies on nothing listening on 8907. If another proposal takes them first, take the next free number and ports and use them in the index row, the heading, and every reference.

```markdown
| S104 | The registry and the MCP server record the client that called them, and exported spans carry no query text | standalone | none | none | none |
```

````markdown
## S104: The registry and the MCP server record the client that called them, and exported spans carry no query text

**Goal.** Validate that the compiled `podium-mcp`, `podium sync`, and registry
record the self-reported client as `caller.client` in the forwarded registry
stream and in the local stream, that a malformed header changes nothing but
the attribute, and that the OTLP bytes `podium-mcp` exports hold no search
text, on a call the registry answers and on a call that fails because the
registry cannot be reached.

**Covers.** §7.2.2 `X-Podium-Client`, §8.1 `caller.client` in both audit
streams, §8.3, and the §13.8 exported-span-content rule, through `podium serve`,
`podium-mcp`, and `podium sync`.

**Why by hand.** An operator reads the raw JSON a SIEM receiver gets and the
raw OTLP bytes a collector gets. The scrub must hold on the bytes that leave
the machine, which a decoded in-process span does not show, and an empty
capture has to be told apart from a clean one. A forwarded event that names
`podium-mcp` without the host, or a capture that holds `alice` or `bob`, is the defect
this scenario catches.

**Steps.**

1. Run the isolation block, then start two receivers that record every POST
   body verbatim: one for OTLP on port 8905 and one for the audit stream on
   port 8906.

```bash
cat > "$WORK/sink.py" <<'EOF'
import http.server, sys
OUT, PORT = sys.argv[1], int(sys.argv[2])
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("content-length", 0))
        open(OUT, "ab").write(self.rfile.read(n) + b"\n")
        self.send_response(200); self.send_header("Content-Length", "0"); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", PORT), H).serve_forever()
EOF
python3 "$WORK/sink.py" "$WORK/otlp.bin" 8905 &
OTLP=$!
python3 "$WORK/sink.py" "$WORK/forwarded.jsonl" 8906 &
FWD=$!
sleep 1
```

2. Build one artifact and serve with the audit stream pointed at the receiver.

```bash
mkdir -p "$WORK/reg/team/budget-guide"
cat > "$WORK/reg/team/budget-guide/ARTIFACT.md" <<'EOF'
---
type: context
version: 1.0.0
description: how to prepare the quarterly budget
---

budget prose
EOF
PODIUM_AUDIT_LOG_PATH="http://127.0.0.1:8906/ingest" \
podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
  --bind 127.0.0.1:8208 > "$WORK/srv.log" 2>&1 &
SRV=$!
curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8208/healthz
server_alive "$SRV" "$WORK/srv.log"
export PODIUM_REGISTRY=http://127.0.0.1:8208
```

   **Expect.** `server_alive` prints nothing.

3. Drive `podium-mcp` over stdio with a named host, a search that holds an
   email address, and OTLP export pointed at the receiver. Then run a second
   search against port 8907, where nothing listens.

```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"s104-host","version":"9.9"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_artifacts","arguments":{"query":"alice@acme.com budget"}}}' \
  | PODIUM_HARNESS=claude-code PODIUM_AUDIT_SINK="$WORK/local.log" \
    OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:8905 \
    podium-mcp 2>"$WORK/mcp.log" > "$WORK/mcp.out"
sleep 2
wc -c < "$WORK/otlp.bin"
grep -c search_artifacts "$WORK/otlp.bin"
grep -c alice "$WORK/otlp.bin" || true
jq -cS 'select(.type=="artifacts.searched") | .caller.client' "$WORK/forwarded.jsonl"
jq -cS '[.caller.client, .context]' "$WORK/local.log"
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"s104-host","version":"9.9"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_artifacts","arguments":{"query":"bob@acme.com budget"}}}' \
  | PODIUM_REGISTRY=http://127.0.0.1:8907 \
    OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:8905 \
    podium-mcp 2>>"$WORK/mcp.log" > /dev/null
sleep 2
grep -c bob "$WORK/otlp.bin" || true
grep -c 'connection refused' "$WORK/otlp.bin"
```

   **Expect.**
   - The byte count is above 0 and the `search_artifacts` count is at least
     1. A 0 on either means no span was exported, and the next check proves
     nothing.
   - The `alice` count is `0`. Any other count means the search text reached
     the trace backend.
   - The forwarded `caller.client`, printed with its keys sorted, is
     `{"harness":"claude-code","host":"s104-host","host_version":"9.9","kind":"mcp","name":"podium-mcp","version":"<release>"}`
     with the release that the `initialize` result reports as `serverInfo.version` in `$WORK/mcp.out`. A missing `host` means
     the bridge discarded `clientInfo`.
   - The local log line shows the same object, and its context holds no
     `source` key.
   - The `bob` count is `0`, and the `connection refused` count is at least
     1. The second run sent its search to port 8907, where nothing listens, so
     its client span ended in a transport error whose description reached the
     receiver. A 0 for `connection refused` means the error span was not
     exported, and the `bob` check proves nothing. Any `bob` count above 0
     means a failed call exported its search text.

4. Sync against the registry with a different harness.

```bash
podium sync --registry "$PODIUM_REGISTRY" --target "$WORK/fs" --harness cursor; echo "exit=$?"
sleep 1
jq -c 'select(.type=="artifact.loaded") | .caller.client' "$WORK/forwarded.jsonl" | tail -1
```

   **Expect.** `exit=0`, and a `caller.client` with `kind` `sync`, `name`
   `podium`, and `harness` `cursor`.

5. Send a malformed header by hand.

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Podium-Client: %zz' \
  "$PODIUM_REGISTRY/v1/search_artifacts?query=x"
sleep 1
jq -c 'select(.type=="artifacts.searched") | .caller.client' "$WORK/forwarded.jsonl" | tail -1
```

   **Expect.** `200`, and `null` for the newest `artifacts.searched`. A 4xx
   means a bad header fails the request, which §7.2.2 forbids.

**Cleanup.** `kill "$SRV" "$OTLP" "$FWD"` and `rm -rf "$WORK"`.
````

## Open questions

- D-10 removes `context.source="mcp"` from `podium-mcp` local events, which breaks any consumer that filters on it. This proposal recommends removal, because `caller.client.kind=mcp` carries the same fact, nothing in the spec, docs, or tests reads the key, and the project adds no compatibility shims before 1.0. The reviewer may prefer to keep the key for one release.
- D-3 includes the `podium` CLI (`kind=cli`). Dropping CODE-6 would leave CLI calls and the `podium sync --preview` request with no `caller.client` and would change nothing else in the design. This proposal recommends keeping it, because the §7.6.1 read commands raise the same read events as the other clients.
- D-14 adds no source-scan guard over tracer-provider construction. The binary-level test in TEST-7 catches a provider built outside `pkg/tracing` in `podium-mcp`, which holds the only query-bearing producer, and a provider built outside `pkg/tracing` in the registry would go unnoticed. The registry's server handler records `url.path` and `http.route` only, so such a provider exports nothing query-bearing today. The reviewer may prefer to restore `pkg/tracing/provider_guard_test.go`, which also covers the registry and any later binary at the cost of a module-wide source scan with a positive control.
- The tracing part (SPEC-4, D-12 to D-14, CODE-8, TEST-7, the S3, S18, and S19 lines, the tracing edge-case rows, the S104 tracing checks, the DOC-2b "Monitoring" bullet, the DOC-3 Fixed entry, and the tracing Watch-out and Non-goals bullets) is self-contained. This proposal keeps it in 0065. The reviewer may prefer to move it to its own proposal, which would also remove "and strip the query string from every exported trace span" from the title.

## Non-goals

- Verifying or authenticating the client claim. A client can report any name, and the spec states that the value is advisory. Client attestation, signed client identity, and per-client authorization are out of scope.
- Reading User-Agent into audit, or falling back to it for callers that send no `X-Podium-Client`. Such callers record no `caller.client`.
- Sending the header from the web UI.
- Recording `caller.client` on ingest events. The reingest and webhook routes run outside `withAuditMetaMiddleware` (`internal/serverboot/serverboot.go:1529-1536`), so ingest events also record an empty caller and trace ID today (`pkg/registry/server/layers.go:1929`, `internal/serverboot/reingest.go:172-189`). Attaching request metadata in `runIngestAndRespond` would fix both, but it changes the caller and trace ID of existing ingest events and belongs in its own scoped change.
- Adding `caller.client` to the §7.3.2 outbound webhook bodies, the `/v1/events` stream actor, metrics labels, or span attributes.
- Bounding or sanitizing the existing `X-Forwarded-User` value in `caller.network`. It is a separate defect with the same pattern.
- Moving meta-tool arguments out of the URL query into a request body. The span scrub removes the trace exposure without changing the wire protocol.
- Fixing `PODIUM_TRACING=stdout` in `podium-mcp`, which writes spans to the process's stdout, the MCP stdio channel. That is a separate defect.
- An operator setting to disable `caller.client` recording or to change the 1024-byte and 64-byte bounds. The spec fixes the bounds.
- Any migration or rehash of existing audit logs. Records without `caller.client` keep their hashes (D-9).
- A §6.3.3 statement that `X-Podium-Client` is not an identity header (dropped SPEC-3). §6.3.3 lists a closed set of identity headers (`X-Podium-User-Sub`, `-Email`, `-Groups`, `-Org`, and `X-Podium-Proxy-Secret`), and the gateway's stripping duty covers only client-supplied `X-Podium-User-*` headers (`docs/deployment/gateway-delegated-identity.md:87`). `pkg/identity/trusted_headers.go:27-41` reads those headers by fixed name with no prefix scan. The `X-Podium-*` prefix is not reserved for identity: `podium-mcp` already sends `X-Podium-Tenant` (`cmd/podium-mcp/main.go:2574`), and the registry sets other `X-Podium-*` response headers (`pkg/registry/server/server.go:578`, `:584`). §7.2.2 (SPEC-1) and §8.1 (SPEC-2) already state that the value is advisory and not an identity attribute, so a third copy in §6.3.3 would add a statement to keep in sync with no new rule. D-5 records the gateway point.

## Documentation changes

### DOC-1. HTTP API reference: client identification

Target: `docs/reference/http-api.md`, a new top-level section `## Client identification`, inserted after the end of `### Session posture` and before `## SLO targets (server source)`. It stays outside `## Authentication`, because the header is not part of authentication (D-5). Content, following `doc-style.md`:

- (a) One sentence stating that a request to a `/v1/` control-plane route can name its client software in `X-Podium-Client`, an `application/x-www-form-urlencoded` value with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`, and that `kind` is `mcp`, `sync`, `sdk`, or `cli`.
- (b) One example in an `http` fence only, for example a `GET /v1/search_artifacts?query=x HTTP/1.1` line followed by `X-Podium-Client: kind=sdk&name=acme-tool&version=1.2.0`. Do not use a `text` or untagged fence holding a `curl` or `podium` line, because `tools/doccov/scan.go` classifies such a block as runnable.
- (c) The registry's handling: it ignores a value over 1024 bytes or one that does not parse, takes the first value of a repeated member, ignores unknown members, replaces bytes outside the stated character set with `_`, and truncates each member to 64 bytes. A value that leaves no non-empty member after sanitization is treated as absent and records no `caller.client`. Otherwise, the registry records `kind` as `other` when it is absent or unrecognized.
- (d) Two sentences on the guarantees: an ignored or absent value never fails a request, and the value is never an input to authentication, visibility, quotas, audit sampling, or tenant selection.
- (e) One sentence stating that the first-party clients do not send it on presigned links, `/objects` links, `/healthz`, or `/readyz`.
- (f) One cross-link to the operator guide's "Audit records" section for where the value appears as `caller.client`, and one to §7.2.2 for the values each first-party client sends.

### DOC-2a. Operator guide: audit records (current behavior)

Target: `docs/deployment/operator-guide.md`, a new section `## Audit records` after `## Monitoring` and before `## Alerting`. It documents behavior that exists today and lands first. It states:

- The file sink writes JSON Lines, one event per line.
- An `http://` or `https://` value of `PODIUM_AUDIT_LOG_PATH` (registry) or `PODIUM_AUDIT_SINK` (`podium-mcp`) sends one HTTP POST per event, with `Content-Type: application/json` and one JSON object in the body. Events are not batched. Each POST has a 10-second timeout.
- The keys are `type`, `timestamp` (RFC 3339, UTC), `trace_id`, `caller` (`identity`, `email`, `groups`, `network` with `source_ip` and `forwarded_user`, and `public_mode`), `target`, `context`, `tenant`, `resolved_layers`, `result_size`, `hash`, and `prev_hash`. `type`, `timestamp`, and `hash` are always present, and the other keys are omitted when empty.
- A transport error or a non-2xx response drops the event. The sender does not retry or buffer it.
- The sending process computes the hash chain in memory before each send, so a dropped event leaves the receiver with a `prev_hash` that names a record it never received.
- The chain belongs to the process. It restarts at each process start with no `prev_hash` on the first record, and each clustered replica sends its own chain.
- The registry does not verify a forwarded stream; verification belongs to the receiver.
- One `json`-fenced example record.

### DOC-2b. Operator guide: `caller.client` and tracing content

Target: `docs/deployment/operator-guide.md`.

- In "Audit records", add `caller.client` `{kind, name, version, host, host_version, harness}` to the key list and the example. State: "The client reports `caller.client` itself, and the registry does not verify it. The registry never uses it for authentication, visibility, quotas, or tenant selection." State that the events the ingest pipeline raises (`artifact.published`, `artifact.deprecated`, `artifact.signed`, `domain.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`) carry no `caller.client`, even when a reingest or webhook request triggered the ingest, and that events raised without a request carry none either. State that an event about the registry as a whole, such as `audit.anchored`, carries none, including the `audit.anchored` appended after an erasure. State that the MCP server's local events carry it for loads served from the workspace overlay or the cache, that a cache hit the MCP server revalidates with the registry (the default `always-revalidate` mode, or a 304 answer) also produces a registry `artifact.loaded` event with `caller.client`, and that records written before the release verify unchanged. State that `caller.client` is recorded as the client reported it after sanitization, and that neither the query-text PII scrub nor `podium admin erase` rewrites it, so a value a client placed in a member stays in the record after an erasure.
- In "Monitoring", add a "Tracing" paragraph. It states that tracing is off by default and that `PODIUM_TRACING=otlp`, `OTEL_EXPORTER_OTLP_ENDPOINT`, or `PODIUM_TRACING=stdout` enables it, then that no exported span carries a URL query component, search text, or `session_id` in any field, and that a span attribute holding an `http` or `https` URL, such as `url.full`, shows the scheme, host, and path only. State that a failed outbound call exports a status description that names the host and port and no URL.

Follow `doc-style.md` throughout; avoid the "suits X but not Y" construction.

### DOC-3. CHANGELOG `[Unreleased]` entries

Add under `## [Unreleased]`:

Added:

> **Audit records name the client software** (§7.2.2, §8.1): the MCP server, `podium sync`, the `podium` CLI, and the SDKs send the `X-Podium-Client` request header on control-plane requests. The registry audit log and the MCP server's local audit sink record its normalized value as `caller.client`, with the members `kind`, `name`, `version`, `host`, `host_version`, and `harness`. An `http(s)` audit target receives the same key in each POSTed event. The value is self-reported, bounded, and sanitized. The registry never uses it for authentication, authorization, visibility, quotas, sampling, or tenant selection, and an ignored or malformed value never fails a request. A record without `caller.client` keeps its hash, so existing audit logs verify unchanged.

Changed:

> **The MCP server's local audit events no longer carry `context.source`** (§8.1, §8.3): `caller.client.kind` is `mcp` on those events instead. A consumer that filtered on `context.source` filters on `caller.client.kind`. This is a backward-incompatible change and lands in a MINOR bump.

Fixed:

> **Exported trace spans no longer carry URL query strings** (§13.8): every exporter, including `PODIUM_TRACING=stdout` and OTLP, receives each span attribute that holds an `http` or `https` URL, `url.full` among them, with the query and fragment removed. This holds for calls that succeed and calls that fail. Search text, `session_id`, and presigned-URL signatures that the MCP server sent as query parameters no longer reach the trace backend.

## Resolved in adversarial review

Review rounds populate this section.

### Draft reconciliation (2026-11-11)

The challenge revisions of the draft disagreed in the places below. This document resolves each as stated.

- **Ingest events.** The CODE-2 revision attached request metadata in `runIngestAndRespond` so ingest events would record `caller.client`, and the CODE-3 revision restricted the change to read events and moved ingest attribution out of scope. The document follows CODE-3: that attachment also changes the caller and trace ID of existing ingest events. SPEC-2 states that ingest events record no `caller.client`, TEST-2 (d) pins it, and Non-goals records the existing defect. CODE-2 keeps its `user.erased` extension.
- **Where the `kind=sync` value is built.** The CODE-6 revision assumed a `cmd/podium` constructor shared with `sync.Options` call sites, and the CODE-5 revision builds the value inside `pkg/sync` with no `Options` field. The document follows CODE-5, so `cmd/podium` holds one constructor (CLI only), and TEST-4 drops the "zero client sends no header" case and asserts `buildinfo.Version`.
- **The harness member on non-load calls.** The CODE-4 revision allowed a per-call override on every meta-tool, and the SPEC-1 revision limits it to `load_artifact`. The document follows SPEC-1, which matches §6.7.
- **The Python version source in tests.** The TEST-6 revision monkeypatched `importlib.metadata`, which the CODE-7 revision removed. Both SDKs now expose a pure header function that takes the version, and the empty-version case is a unit test of that function.

### Pass 1 (2026-11-11, automated)

- **Ingest event set.** SPEC-2, D-8, the Summary, the ingest edge-case row, and DOC-2b named only `artifact.published`, `layer.ingested`, `layer.history_rewritten`, and `freeze.break_glass`, copied from the stale `ingestAuditEmitter` doc comment. The ingest pipeline also raises `artifact.deprecated`, `artifact.signed`, and `domain.published` through the same emitter (`pkg/registry/ingest/ingest.go:511`, `:749`, `:755`). Each of those sections now defines the exclusion as every event the ingest pipeline raises, even when a request triggered the ingest, and lists the full set. D-8 cites each emit site. TEST-2 (d) now configures a registry signer, publishes a `DOMAIN.md`, flips `deprecated: true` on a second reingest, and asserts that none of the observed ingest event types carries `caller.client` and that each appeared.
- **Erase scope guard.** CODE-2 (b) put the client on `audit.EraseScope`, which let a header-derived value make a no-tenant scope differ from the zero value and pass the `scope == (EraseScope{})` cross-tenant guard (`pkg/audit/retention.go:241-243`). It also hashed an unnormalized client, because `EraseUser` appends through `rewriteWithChain` rather than `Append`. CODE-1 step 8 now gives `EraseUser` a separate `client *CallerClient` parameter that it normalizes onto `user.erased`, and `EraseScope` stays unchanged. D-9, the Summary, and the Watch-out list state this. TEST-1 extends `TestEraseUser_ZeroScopeRefused` with a non-nil client and asserts a sanitized client on `user.erased`.
- **Erase test ordering.** TEST-1 (S6) asserted a field that CODE-2 (S7) created. The `pkg/audit/retention.go` change moved into CODE-1 (S5), whose step passes nil at every existing call site. CODE-2 (b) now only supplies the request's client. The checklist S5 line, the ordering constraint, the CODE-1 and CODE-2 targets, and the S5 command (`go build ./...`) reflect the move.
- **Source guards.** TEST-2 (f) and TEST-7 enumerated files with `go list ./...`, which a test runs from its package directory, so both guards scanned only their own subtree. Both now use the module pattern `github.com/lennylabs/podium/...` and assert a positive control on files outside their package. The Watch-out bullet states the reason.
- **Watch subscription harness.** CODE-5 had `streamServerEvents` read `opts.Sync.AdapterID`, which it never receives (`pkg/sync/watch_server.go:47`, `:81`). `runServerWatch` now applies the `none` default, computes the value, and passes it to `streamServerEvents` as a new `clientHeader` parameter that it forwards to `readServerEventStream`. The ordering constraint lists `streamServerEvents`, the CODE-5 targets list `runServerWatch`, and the stale `:52` citation is corrected. (Pass 2 corrected this entry, which first named `Watch`.)
- **Tracing test flush.** TEST-7 moved `TestTracing_CallToolRootSpanAndPropagation` to the batching `NewProvider` without a flush. The bullet now requires `tp.ForceFlush` after `srv.callTool(raw)` and keeps the `otel.SetTracerProvider` install.
- **Interactive override harness.** `podium sync override` with no batch flags resolved its view with no `AdapterID` and applied with one, so one invocation recorded loads with and without `harness`. CODE-5 now has `runSyncOverrideInteractive` (`cmd/podium/interactive.go:300-304`, `:332`) compute `resolveOverrideHarness` once and pass it to both the view and the apply step, and the "no `cmd/podium` call site" statement is narrowed. TEST-4 (5) pins `ResolveEffectiveView` sending the passed harness. The S12 line and the S12 and S13 command include `cmd/podium`.
- **DOC-1 (c) all-empty rule.** DOC-1 (c) stated the `other` mapping without its precondition. It now states that a value with no non-empty member after sanitization records no `caller.client`, and that the `other` mapping applies otherwise, matching SPEC-1.
- **Erasure and scrub leave `caller.client`.** The edge-case row cited DOC-2b for a fact DOC-2b did not carry. DOC-2b now states that neither the query-text PII scrub nor `podium admin erase` rewrites `caller.client`.
- **S104 key order.** `jq -c` keeps `jsonClient`'s declaration order, so the alphabetical Expect literal would not match a correct build. Step 3 now prints with `jq -cS`, and the Expect says the keys are sorted.
- **SDK subscription header.** TEST-6 (a) did not cover the `/v1/events` funnel that CODE-7 switches. The Python case extends `test_subscribe_sends_repeated_type_params`, and the TypeScript mirror extends the "subscribe sends repeated type params" test.
- **Endpoint sink normalization.** No test passed an unsanitized `CallerClient` to `EndpointSink.Append`. TEST-1 adds an `endpoint_test.go` row with a NUL and ESC host, a 200-byte name, and `kind=MCP`, and asserts the sanitized POST body and its hash.
- **Subscription call citation.** CODE-5 cited `pkg/sync/watch_server.go:97` for the `readServerEventStream` call in the `streamServerEvents` reconnect loop. Line 97 is the `return` inside the `ctx.Err()` guard that follows the call. The citation now names `:95`, where `readServerEventStream(ctx, stream, url, token, trigger)` appears.

### Pass 2 (2026-11-11, automated)

- **Cache-served loads and the registry stream.** D-10, the SPEC-2 local-stream text, and the edge-case table stated that a load served from the content cache sends no registry request and leaves no registry event. Under the default `always-revalidate` mode (spec/06 §6.2, `cmd/podium-mcp/main.go:256`) a cache hit first sends a HEAD (`main.go:1384`), and a conditional GET answered 304 is also served from the cache (`main.go:1410`, `:1414`). Both requests go through `newRegistryRequest` and carry the header, and the registry answers both through `core.LoadArtifact`, which emits `artifact.loaded` (`pkg/registry/server/server.go:1124`, `pkg/registry/core/core.go:1522-1537`). D-10 now limits the no-request case to overlay loads and `offline-first` or `offline-only` cache hits (`main.go:1363`, `:1370`, `:1581`), states that a revalidated cache hit is recorded in the registry stream with `caller.client`, and describes the `:1624` degraded fallback as following a failed request that carried the header. The SPEC-2 added text and its rationale name the offline cache modes (§6.5). The edge-case row is split into a no-request row and a revalidated-hit row, DOC-2b states the revalidated-hit fact, and TEST-3 (1) asserts that the `headContentHash` HEAD carries the header.
- **Watch subscription owner.** CODE-5 attributed the `/v1/events` subscription start to `Watch` and placed `Watch` in `pkg/sync/watch_server.go`. `Watch` is in `pkg/sync/watch.go:52` and only starts `runServerWatch` (`watch.go:74`), and the `streamServerEvents` call at `watch_server.go:47` is inside `runServerWatch` (`watch_server.go:24`). The CODE-5 targets and both CODE-5 paragraphs now name `runServerWatch`, which applies the `none` default and passes `syncClientHeader(adapterID)` with no signature change, and they state that `Run(opts.Sync)` (`watch_server.go:35`, `:70`) defaults only its copy of the options. TEST-4 (2) still drives the public `Watch`, and the Pass 1 entry is corrected.
- **Interactive override test.** No test drove the CODE-5 edit to `runSyncOverrideInteractive`. TEST-4 (5) calls `ResolveEffectiveView` directly, and the existing interactive tests use a filesystem registry and send no HTTP request. TEST-4 (6) adds `TestRunSyncOverrideInteractive_SendsResolvedHarness` in `cmd/podium/interactive_test.go`, which runs the interactive override against an `httptest` registry stub with `--harness cursor` and with an empty harness, and asserts `Harness: cursor` and `Harness: none` on every request, including the view requests sent before the apply. The S13 checklist line and the S12 and S13 command name it.

### Pass 3 (2026-11-11, automated)

- **`audit.anchored` after an erasure.** The SPEC-2 added text required `caller.client` on every event the registry raises while serving a request that names its client, except ingest events and events raised without a request. With anchoring on and a file sink, the erase handler runs the re-anchor hook with the request context (`pkg/registry/server/layers.go:1137-1139`, `internal/serverboot/serverboot.go:1710-1715`, `internal/serverboot/audit_anchor.go:177-183`), and `audit.Anchor` appends `audit.anchored` with `Caller: "system:anchor"` and no request metadata (`pkg/audit/anchor.go:47-58`), so the spec required a value that no step set. The SPEC-2 added text and its rationale, D-8, the Summary, DOC-2b, and CODE-2 (b) now exempt an event about the registry as a whole, such as `audit.anchored`, including the one that follows a §8.5 erasure, mirroring the §8.1 tenant paragraph (`spec/08-audit-and-observability.md:32`). The code keeps `audit.Anchor` unchanged. A Watch-out bullet and an edge-case row state it, and TEST-2 (e) extends `TestRun_EraseReAnchorsTheRewrittenChain` (`internal/serverboot/erase_reanchor_test.go:140`) to send the header and assert `caller.client` on `user.erased` and none on the `audit.anchored` over its head.

### Pass 4 (2026-11-11, automated)

- **Layer and webhook mounts.** The first Watch-out bullet attributed both `/v1/layers/` and `/v1/ingest/webhook/` to `srv.TenantRouted(layers.Handler())`. Only `/v1/layers` and `/v1/layers/` use that wrapper (`internal/serverboot/serverboot.go:1529-1530`). `/v1/ingest/webhook/` is mounted unwrapped through `layers.WebhookHandler()` (`serverboot.go:1531-1536`). The bullet now names each mount and its handler, and keeps the conclusion that all of them sit outside `srv.Handler()` and so outside `withAuditMetaMiddleware`.
- **TypeScript version-lookup failure.** TEST-6 covered only the `sdkVersion` happy path and the pure `clientHeader("")` encoder, so no test reached the fail-open branch that CODE-7 adds. TEST-6 TypeScript gains case (e), which resets the module cache, mocks `node:fs/promises` so that reading `package.json` rejects while every other read reaches the real module, and asserts that `searchArtifacts` still completes and sends `kind=sdk` and `name=podium-ts` with no `version` member. The case uses `vi.resetModules` and `vi.doMock` in the existing `src/index.test.ts`, so CODE-7 gains no test hook.
- **Case (e) read citations.** Case (e) cited `src/config.ts:253` as the `sync.yaml` read. That line is the key-file read in `resolveKeys`, which loads the file named by `PODIUM_SIGN_KEY_PATH` or the default key path. The `sync.yaml` read is `readDefaults` at `src/config.ts:95`, which `scopePaths` (`src/config.ts:102-112`) feeds. The case now cites both reads, which go through the mocked `node:fs/promises` and must both reach the real `readFile`.

### Redesign 1 (2026-11-11, automated)

- **Area redesigned.** The span scrub: SPEC-4, D-12 to D-14, CODE-8, TEST-7, the S3, S18, and S19 checklist lines, the S18 and S19 command, the tracing edge-case rows, S104 step 3, the DOC-2b "Monitoring" bullet, the DOC-3 Fixed entry, the Summary span-scrub bullet and fixed decisions, and the Watch-out list. The audit area is unchanged.
- **Span-scrub scope.** The review asked for the wrapper to cover status descriptions, events, and links, on the premise that a status description carries the full URL on error. The premise is false for otelhttp v0.70.0 on Go's `net/http`: the transport records the base round tripper's error, and `http.Client.Do` adds the `*url.Error` that holds the URL above it (otelhttp `internal/semconv/client.go:294-297`, Go `net/http/client.go:622-637`). D-13 now enumerates every producer of every span field, with citations. SPEC-4 states the rule over every field, so a span's name, attributes, events, links, and status description are all bound. The wrapper rewrites only attributes and keys on the value instead of a list of keys. TEST-7 asserts the rule over every field at the provider level and on raw OTLP bytes at the binary level, on success, a failed presigned fetch, and an unreachable registry. S104 step 3 adds the unreachable-registry run, and a Watch-out bullet forbids recording an `http.Client.Do` error on a span. A new edge-case row states the transport-failure outcome.
- **Reasons for the smaller mechanism.** A search of the otelhttp v0.70.0 module finds no emitter of `http.url` or `url.query`, so the key list named attributes that do not exist. otelhttp already removes userinfo before it sets `url.full` (otelhttp `internal/semconv/client.go:114-123`), so the `url.Parse` call and its userinfo clearing did no work. In an absolute URL the first `?` or `#` begins the query or the fragment (RFC 3986 §3), so a cut on the value needs no parse and has no failure branch.
- **What the redesign deleted.** The `http.url` rewrite, the `url.query` drop, the `url.Parse` call with its userinfo clearing and parse-failure branch, and the "returns the original slice when nothing changed" requirement in `scrubAttrs`. The exported `NewProvider`, which becomes the unexported `newProvider` because no caller outside `pkg/tracing` remains. `pkg/tracing/provider_guard_test.go` and the D-14 source-scan sentence. The `cmd/podium-mcp/tracing_test.go` rewrite of `TestTracing_CallToolRootSpanAndPropagation`, which builds its own `WithSyncer` provider, asserts no URL content, and stays unchanged. `ProviderGuard` from the S18 and S19 test run, with `./cmd/podium-mcp/` moved to a separate confirmation run. TEST-7 from the source-guard Watch-out parenthetical, which now names TEST-2 (f) alone.
- **Superseded history.** This entry supersedes the Pass 1 "Tracing test flush" entry and the TEST-7 part of the Pass 1 "Source guards" entry. Both stay as history.
- **Open decisions recorded.** Each has a default that the proposal text follows.
  - Provider source-scan guard. The default drops `pkg/tracing/provider_guard_test.go`, because the binary test covers `podium-mcp`, the only binary with a query-bearing producer, and the registry handler records `url.path` and `http.route` only. The alternative keeps the guard, which also covers the registry and any later binary at the cost of a module-wide source scan with a positive control. The Open questions section lists this choice.
  - Splitting the tracing part into its own proposal. The default keeps it in 0065. The tracing edits are self-contained, so a later split moves them unchanged. The Open questions section lists this choice.
  - Provider installation in `TestProvider_ScrubsEveryExportedField`. The default passes `otelhttp.WithTracerProvider(tp)` to the test transport, which changes no global state and lets the test run in parallel. The alternative installs the provider with `otel.SetTracerProvider` and restores it on cleanup, which exercises the global lookup path that the binary test already covers.

### Pass 5 (2026-11-11, automated)

- **`podium sync --preview` identity.** SPEC-1 and D-3 require `podium sync` to send `kind=sync` with the run's harness on every control-plane request, but `podium sync --preview` sends its one request, `GET /v1/scope/preview`, through `cmd/podium`'s `fetchScopePreview` (`cmd/podium/main.go:340-344`, `cmd/podium/scope_preview.go:44-47`, `:80-81`), which CODE-6 gave the `kind=cli` value. CODE-6 now gives `fetchScopePreview` and `runScopePreview` a `clientHeader` parameter. The `--preview` branch of `syncCmd` passes `sync.ClientHeaderValue(resolved.Harness)`, which falls back to `none` (`pkg/sync/resolve.go:243`), and `podium status` (`cmd/podium/status.go:103`) passes the new `cliClientHeader()`, which replaces `setCLIClientHeader`. CODE-5 exports its constructor as `ClientHeaderValue`, which replaces `syncClientHeader`; the Pass 2 entry keeps the old name as history. The CODE-6 test now drives `syncCmd` with `--preview`, with and without `--harness cursor`, and asserts `kind=sync` with `Harness: cursor` and `Harness: none`, and drives `statusCmd` and asserts `kind=cli` on `/v1/scope/preview` and no header on `/healthz`. SPEC-1 needs no change, because the code now meets it. The Summary, D-3, a Watch-out bullet, the CODE-5 targets sentence, the S14 line and its dependency on S12, the ordering constraints, the S14 command, the coverage list, and the CODE-6 open question reflect the change.
