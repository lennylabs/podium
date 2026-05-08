# 11. Verification

- **Unit tests**: registry HTTP handlers, layer composer, visibility evaluator, `DOMAIN.md` parser and glob resolver, ingest linter, manifest schema validator, MCP server forwarder, workspace local-overlay watcher and merge, content-addressed cache, atomic materialization, OAuth keychain integration, identity provider implementations, Git provider webhook signature verification, signature verification, hash-chain audit, freeze-window enforcement.

- **Managed-runtime integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=injected-session-token`, supply a stub signed JWT, exercise the meta-tool round-trip against a real registry, verify identity flows through and the layer composition resolves correctly for the caller's identity; verify rejection on unsigned token (`auth.untrusted_runtime`).

- **Developer-host integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=oauth-device-code` and `PODIUM_OVERLAY_PATH=${WORKSPACE}/.podium/overlay/`, complete the device-code flow via MCP elicitation, exercise the meta-tool round-trip, verify the workspace local overlay overrides registry-side artifacts and that hashes are exposed in `load_domain`.

- **Local search test**: `search_artifacts` returns workspace-local-overlay artifacts merged with registry results via RRF; removing the local file removes the artifact from search.

- **Search browse mode test**: `search_artifacts(scope=<path>)` with no query returns all visible artifacts in scope, ordered by default rank; `total_matched` reflects the true match count even when `top_k` truncates the returned `results`; `top_k > 50` is rejected with a structured `registry.invalid_argument` error (enforced both client-side in the SDK and server-side at the registry).

- **Domain search test**: `search_domains` returns domains with a `DOMAIN.md` ranked by relevance over the projection (`description` + `keywords` + truncated body); domains without a `DOMAIN.md` do not appear; `--scope` constrains results to a path prefix; visibility filtering excludes domains the caller can't see; updating `DOMAIN.md` re-embeds the projection and reflects in the next query.

- **Workspace local overlay precedence test**: confirm the workspace local overlay overrides every registry-side layer for a synthetic conflicting artifact, and that removing the overlay file restores the registry-side artifact.

- **Domain composition tests**: `DOMAIN.md` `include:` patterns surface matching artifacts; recursive `**` and brace `{a,b}` patterns resolve correctly; `exclude:` removes paths; `unlisted: true` removes a folder and its subtree from `load_domain` enumeration; `DOMAIN.md` from multiple layers merges per §4.5.4; remote-vs-local glob resolution asymmetry is correct.

- **Cross-layer import tests**: a `DOMAIN.md` ingested in one layer imports an artifact ingested in another; a caller who can see both layers sees the imported artifact; a caller who can see only the destination layer sees nothing for that import; imports that don't currently resolve produce an ingest-time warning, not an error.

- **Materialization test**: exercise `load_artifact` against artifacts with diverse bundled file types (Python script, Jinja template, JSON schema, binary blob, external resource); verify atomic write semantics; verify partial-download recovery; verify presigned URL refresh on expiry.

- **MaterializationHook chain test**: configure two hooks in order; the second receives the first's output; each hook can rewrite a file, drop a file, or emit a warning; the final atomic write reflects the chain's combined output. Sandbox-violation cases (network call, subprocess spawn, write outside destination) fail materialization with a structured error and no files are written. With no hooks configured, materialization is bit-identical to a build without hook support.

- **LayerSourceProvider built-ins test**: the bundled `git` and `local` source providers continue to satisfy §4.6 and §7.3.1: webhook ingest for `git`, manual reingest plus `podium layer watch` for `local`.

- **LayerSourceProvider extension test**: a synthetic plugin implementing the SPI registers a third source type; layers configured with that source type ingest, snapshot, and serve identically to built-in sources from a caller's perspective. Trigger model declared by the plugin (push or poll) is honored. Manual reingest works regardless of trigger model.

- **SPI wire-compatibility test**: every built-in plugin's exported method signatures conform to the §9.3 forward-compatibility constraints: context-aware, structurally serializable inputs/outputs, structured errors, no shared in-process state across calls. Static analysis fails the build on regressions.

- **`podium sync` lock-file test**: `podium sync` in an empty target writes `.podium/sync.lock` with the resolved profile, scope, and artifact list; re-running `podium sync` is idempotent (no spurious writes); the target dir auto-creates if missing; `--dry-run` prints the resolved set and writes nothing. Two `podium sync` invocations against different targets each maintain independent lock files.

- **Filesystem ↔ server equivalence test**: a `podium sync` against a filesystem-source registry and a `podium sync` against `podium serve --standalone --layer-path` pointed at the same directory produce byte-identical materialized output (manifest bodies, bundled resources, harness-adapter output, lock-file `artifacts:` list) for the same target and profile. Confirms that the shared Go library (§2.2 *Shared library code*) is the single behavioral surface across deployment modes.

- **Override + watch test**: `podium sync --watch` running against a target keeps `.podium/sync.lock`'s `toggles:` populated across registry change events. `podium sync override --add <id>` materializes the artifact and adds an entry to `toggles.add`; `podium sync override --remove <id>` removes it from disk and adds to `toggles.remove`. Manual `podium sync` (no `--watch`) clears `toggles:`. `podium sync override --reset` is equivalent to manual `podium sync`.

- **Save-as test**: after `podium sync override --add <id-a> --remove <id-b>`, `podium sync save-as --profile <name>` renders a profile in `.podium/sync.yaml` whose `include` / `exclude` reproduce the toggled state; the lock file's `toggles:` is cleared and the new profile becomes the active one. `--update` overwrites an existing profile; `--dry-run` prints the YAML diff and writes nothing.

- **Profile edit test**: `podium profile edit <name> --add-include <pattern>` rewrites `.podium/sync.yaml` preserving formatting and comments around untouched keys. The target directory and lock file are untouched; a subsequent `podium sync` picks up the change.

- **Signing test**: artifact signed at ingest; signature verified on materialization; tampered content rejected with `materialize.signature_invalid`; `podium verify <id>` matches.

- **Versioning and ingest tests**: pinned `<id>@<semver>` resolves exactly; `<id>@<semver>.x` resolves to highest matching; `<id>` resolves to `latest`; `session_id`-tagged calls return consistent `latest` resolution within the session; same-version-different-content ingests return `ingest.immutable_violation`; `extends:` parent version pinned at child ingest time; force-push on a `git`-source layer with the default tolerant policy preserves the previously-ingested bytes and emits `layer.history_rewritten`.

- **Harness adapter conformance suite**: for each built-in adapter, load a canonical fixture, produce harness-native output, install into a fresh harness instance, verify the harness can spawn an agent that uses the materialized artifact end-to-end. Includes negative tests for adapter sandbox contract (no network, no out-of-destination writes, no subprocess).

- **Adapter switching test**: the same MCP server binary, started with each `PODIUM_HARNESS` value, passes the conformance suite without recompilation. Per-call `harness:` overrides materialize a single artifact in a different format than the server's default.

- **Identity provider switching test**: the same MCP server binary, started with each identity provider, passes both integration tests above without recompilation.

- **Visibility tests**: a layer with `public: true` is visible to unauthenticated callers; `organization: true` is visible to any authenticated user in the org and no one else; `groups: [...]` matches OIDC group claims; `users: [...]` matches the listed identifiers; multiple visibility fields combine as a union; user-defined layers are visible only to the registrant; the user-defined-layer cap is enforced; `mcp-server` artifacts are filtered from MCP-bridge results; admins can override visibility for diagnostic purposes (and that override is audited).

- **Layer lifecycle tests**: `podium layer register` returns a webhook URL and HMAC secret; an ingest webhook with an invalid signature is rejected; a manual `podium layer reingest` succeeds and is idempotent; `podium layer reorder` re-sequences user-defined layers; `podium layer unregister` removes the layer immediately and the artifacts disappear from the caller's effective view; freeze windows block ingest; break-glass requires dual-signoff and justification.

- **Ingest workflow test**: an artifact merged into a tracked Git ref is ingested via webhook; manifest body and bundled resources are stored at the resolved content_hash; the artifact appears in `search_artifacts` for downstream callers; `artifact.published` and `layer.ingested` events are written to audit.

- **Failure-mode tests**: registry offline (cache serves; explicit "offline" status on miss), workspace overlay path missing (skip with warning), token expired under each identity provider, materialization destination unwritable, MCP protocol version mismatch, untrusted runtime, signature failure, runtime requirement unsatisfiable, source unreachable during ingest, webhook signature invalid.

- **Security tests**: a caller without matching visibility on a layer sees nothing from that layer; the MCP server requires OAuth-attested identity to reach the registry; redaction directives propagate to the registry audit stream and the local audit log; tokens stay in the OS keychain (or in the runtime-managed location for injected tokens); query text PII is scrubbed before audit; audit hash chain detects gaps; sandbox profile honored or refused per host capability.

- **Performance tests**: 1K QPS sustained for `search_artifacts`; 100 ingests/min; `load_artifact` p99 < SLO targets in §7.1; cold-cache vs warm-cache materialization budgets.

- **Soak tests**: 24h continuous load with mixed workload; no memory growth, no descriptor leaks, audit log integrity preserved across restarts.

- **Chaos tests**: Postgres failover during load, object-storage stalls, network partitions between MCP server and registry, IdP outage during refresh, full-disk on registry node.

- **Example artifact registry**: multi-domain demo with diverse types (skill, agent, context, command, rule, hook, mcp-server), diverse bundled file types, multiple layers exercising every visibility mode (`public`, `organization`, `groups`, `users`), at least one user-defined layer, signed artifacts at multiple sensitivities.

- **Rule mode adapter conformance**: each first-class harness adapter materializes a `type: rule` fixture across all four `rule_mode` values; supported modes (✓ in §6.7.1) produce the expected harness-native output; fallback modes (⚠) produce the documented degraded output and emit a lint warning; unsupported modes (✗) fail with a structured error and the artifact is not written.

- **Hook materialization**: a `type: hook` artifact materializes into the harness's native lifecycle-event location for adapters that mark `hook_event` as ✓ in §6.7.1; for ⚠ adapters, the hook lands in a documented fallback path with a warning; for ✗ adapters, lint rejects ingest unless `target_harnesses:` excludes the harness.
