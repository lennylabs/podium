# Unblock proposals for the remaining items

Each remaining item carries a concrete proposal: the blocker, the
specific approach, an effort estimate, and any decision the user
needs to make first.

The numbering matches `IMPLEMENTATION_PROGRESS.md`'s "What remains"
list.

---

## 1. Phase 8 — fold_below_artifacts + fold_passthrough_chains

**Blocker.** Multi-level tree builder is missing; the current
LoadDomain returns immediate subdomains only.

**Proposal.**

- Add a recursive subtree walker in `pkg/registry/core/discovery.go`.
- Pre-compute a per-domain visible-artifact count (recursive).
- `fold_below_artifacts`: for each subdomain, if its recursive count
  is below the threshold, drop the subdomain entry from
  `Subdomains` and append its artifacts to the parent's `Notable`
  list with a `folded_from: <subpath>` annotation in the descriptor.
- `fold_passthrough_chains`: walk single-child intermediate chains
  (`a/b/c/d` where `b` and `c` each have one direct child) and
  represent the deepest non-passthrough as the rendered subdomain.
- `target_response_tokens`: estimate response size (rough byte count
  of marshaled JSON), tighten depth then notable count to fit, and
  surface the truncation via the existing rendering note.

**Effort.** Medium, ~300 lines + ~6 tests. No new dependencies.

**Decisions.** None.

**Can ship now.** Yes.

---

## 2. Phase 12 — MCP / sync overlay integration + BM25 local index

**Blocker.** The overlay provider exists but isn't wired into the
consumer surfaces.

**Proposal.**

- `cmd/podium-mcp`:
  - Read `PODIUM_OVERLAY_PATH` (or fall back to `<workspace>/.podium/overlay/`).
  - Open via `overlay.Filesystem`.
  - For `load_domain`, `search_artifacts`, `search_domains`: query
    overlay records; RRF-merge with registry results client-side
    before returning.
  - For `load_artifact`: prefer overlay over registry when both
    have the same canonical ID (highest-precedence per §4.6).
- `pkg/sync`:
  - Open overlay before walking the registry.
  - Merge overlay records as the highest-precedence layer for
    materialization.
- BM25 local index: reuse `pkg/registry/core.scoreBM25` against
  overlay records; same RRF fusion code path.
- fsnotify watcher reindexes on overlay change.

**Effort.** Medium, ~250 lines + ~5 tests. `fsnotify` already a
dependency.

**Decisions.** None.

**Can ship now.** Yes.

---

## 3. Phase 9 — force-push tolerance

**Blocker.** Ingest doesn't track the last-ingested commit per
layer, so it can't detect a force-push.

**Proposal.**

- Add `LastIngestedRef string` to `store.LayerConfig` (already has
  the layer config table).
- `pkg/registry/ingest.Ingest` records the new commit SHA after
  each successful ingest by writing back to the store.
- `pkg/layer/source.Git.Snapshot` accepts a `priorRef` and uses
  `go-git Repository.IsAncestor(priorRef, newRef)` to detect a
  force-push (newRef does not include priorRef in its history).
- On force-push: emit `layer.history_rewritten` audit event,
  preserve previously-ingested bytes (already true via
  immutability invariant), proceed with new bytes.
- Add `ForcePushPolicy: strict|tolerant` to `LayerConfig`. Strict
  mode rejects with `ingest.history_rewritten`.

**Effort.** Medium, ~250 lines + ~4 tests. go-git already imported.

**Decisions.** None.

**Can ship now.** Yes.

---

## 4. Phase 1 — real Sigstore integration

**Blocker.** Fulcio + Rekor HTTP clients aren't written; needs
either `sigstore/sigstore-go` or hand-rolled HTTP clients.

**Proposal.**

- Add `github.com/sigstore/sigstore-go` (canonical client). Falls
  back to `github.com/sigstore/cosign/v2/pkg/oci` for verification
  if needed.
- `pkg/sign/sigstore.go` `SigstoreKeyless.Sign`:
  - Acquire OIDC token (already shipped via `pkg/identity/oauth_devicecode`).
  - POST to Fulcio `/api/v2/signingCert` with the OIDC token to get
    an ephemeral signing certificate.
  - Sign the content hash with the cert's private key.
  - POST to Rekor `/api/v1/log/entries` to record the signature.
  - Return `(content_hash, cert, signature, log_index)` envelope.
- `Verify`: fetch the Rekor entry by log_index, verify cert chain
  to the Sigstore root, verify signature against content hash.
- Tests against a containerized `sigstore-stack` via
  `testcontainers-go`, or use vendored Sigstore root + recorded
  Rekor entries for unit tests.

**Effort.** Large, ~500 lines + integration tests with the Sigstore
stack. Adds `sigstore/sigstore-go` and its transitive cosign /
rekor / fulcio deps (~30 MB binary increase).

**Decisions.**
1. `sigstore/sigstore-go` vs hand-roll? Recommend `sigstore-go`
   because the cert chain validation is non-trivial.
2. Test infrastructure: spin up Sigstore stack via Docker Compose,
   or use recorded fixtures? Recorded fixtures are simpler, less
   accurate.

**Can ship now.** Yes after decision; the stub interfaces are in place.

---

## 5. Phase 17 — real CVE feed adapters

**Blocker.** Each feed has its own JSON shape; no fetcher.

**Proposal.**

- `pkg/vuln/feeds/` directory with per-feed adapters.
- **OSV** (broadest coverage):
  - Periodic GET against `https://api.osv.dev/v1/query` per
    SBOM component PURL.
  - Or batch via `query/batch` endpoint.
  - Schema: `github.com/ossf/osv-schema/bindings/go/osvschema`.
- **NVD**:
  - GET `https://services.nvd.nist.gov/rest/json/cves/2.0` with
    incremental `lastModStartDate`/`lastModEndDate`.
  - Persistent cursor in store.
- **GHSA**:
  - GitHub GraphQL `securityAdvisories`. Requires a GitHub PAT
    (env `PODIUM_GHSA_TOKEN`).
- All three normalize to the existing `vuln.CVE` struct.
- Scheduler in `cmd/podium-server` runs periodic ingest;
  per-tenant on/off via tenant config.

**Effort.** Large, ~600 lines for all three. Per-feed scheduler
adds ~150 lines.

**Decisions.**
1. Which feeds to ship first? Recommend OSV (most permissive
   licensing, broadest coverage, no API key).
2. Periodic ingest cadence? 1 hour default.
3. Where does the PAT come from for GHSA? Tenant config.

**Can ship now.** OSV adapter standalone; NVD + GHSA after
decisions.

---

## 6. Phase 5 — Postgres backend

**Blocker.** No `pgx`-backed Store implementation; tests need real
Postgres.

**Proposal.**

- `pkg/store/postgres.go`: implement the Store interface using
  `github.com/jackc/pgx/v5/pgxpool`.
- Schema same as SQLite, with `CREATE SCHEMA IF NOT EXISTS
  tenant_<id>` for §4.7.1 schema-per-tenant isolation.
- Connection string from `PODIUM_POSTGRES_DSN`.
- pgvector tables added in the same commit (extension already
  available in stock Postgres images).
- Conformance suite (already in `pkg/store/storetest`) runs against
  Postgres via `testcontainers-go` so SQLite + Memory + Postgres
  all pass identical assertions.
- Migrations: `golang-migrate/migrate` for forward/backward
  migrations; runs on startup.

**Effort.** Medium-large, ~400 lines for the backend + ~150 lines
for migrations + integration tests.

**Decisions.** None — `pgx` and `testcontainers-go` already approved.

**Can ship now.** Yes.

---

## 7. Phase 16 — retention + GDPR erasure + transparency anchoring

**Blocker.** Three separate features; the in-memory + file audit
sinks land but there's no operational layer.

**Proposal.**

- **Retention** (small):
  - `pkg/audit/retention.go`: `Enforce(ctx, sink, policies)`
    walks the JSON Lines log and drops events older than
    `policies[<event-type>]`.
  - Default policies from §8.4 (1 year audit, 30 days query
    text, etc.).
  - Cron in `cmd/podium-server` runs daily.
- **GDPR erasure** (small):
  - `podium admin erase <user_id>`.
  - Walks `manifests`, `audit`, `admin_grants` for the user.
  - Replaces `user_id` with `sha256(user_id + salt)`. Hash chain
    survives because event bodies stay byte-identical (only the
    caller string changes; the change is itself an audited event).
  - Drops user-defined layers owned by the user.
- **Transparency anchoring** (depends on Phase 1):
  - Periodic POST of the audit chain head to a Sigstore-style
    transparency log.
  - Reuse Phase 1's Rekor client.

**Effort.** Retention + erasure: medium, ~300 lines + ~5 tests.
Anchoring: small after Phase 1, ~100 lines.

**Decisions.** None for retention + erasure. Anchoring depends on
Phase 1's Sigstore decision.

**Can ship now.** Retention + erasure yes; anchoring after Phase 1.

---

## 8. Phase 3 — `--watch` mode for sync

**Blocker.** No fsnotify watcher in `pkg/sync`.

**Proposal.**

- `pkg/sync/watch.go`: long-running loop that watches the registry
  root + overlay path via fsnotify.
- Debounce events (200 ms window) to coalesce bursts.
- On debounce flush: re-run `sync.Run` honoring the existing lock
  toggles (do not reset).
- Subscribe to registry change events when the source is a server
  (use the TS SDK's `subscribe` analog from §7.6).

**Effort.** Small, ~150 lines + ~3 tests.

**Decisions.** None — fsnotify already a dependency.

**Can ship now.** Yes.

---

## 9. Phase 2 — presigned URLs above the inline cutoff

**Blocker.** No object-storage integration; everything is inline.

**Proposal.**

- `pkg/objectstore/` SPI with two backends:
  - `s3`: `github.com/aws/aws-sdk-go-v2/service/s3` for
    real S3 / MinIO / R2 / GCS via path-style URLs.
  - `filesystem`: writes to `~/.podium/standalone/objects/`,
    serves via the registry's HTTP server with HMAC-signed
    timestamped URLs.
- At ingest:
  - Resource bytes ≤ 256 KB stay inline in the manifest record.
  - Above the cutoff, stored in object storage; the manifest
    holds the SHA-256 + size + URL placeholder.
- At `load_artifact`:
  - Below cutoff: inline as today.
  - Above: presigned URL with `PODIUM_PRESIGN_TTL_SECONDS`
    expiry (default 3600, §6.2).

**Effort.** Large, ~500 lines including both backends + tests.
Adds `aws-sdk-go-v2/service/s3` (and friends).

**Decisions.**
1. `aws-sdk-go-v2` dependency? Heavy (~3 MB of code) but the
   canonical S3 client.
2. Filesystem backend's HMAC URL signing — straightforward but
   adds a new HTTP route the registry serves.

**Can ship now.** Yes after AWS SDK approval.

---

## 10. Vector store + embedding pipeline

**Blocker.** sqlite-vec extension loading + embedding inference.

**Proposal.**

- **API-only path** (recommended starting point):
  - `pkg/embedding/openai.go`, `voyage.go`, `cohere.go`, `ollama.go`:
    thin HTTP clients to each provider's embedding endpoint.
  - Selected via `PODIUM_EMBEDDING_PROVIDER`.
- **sqlite-vec path** (standalone vector store):
  - Load the sqlite-vec extension via the mattn SQLite driver
    (registers a SQL function returning the embedding distance).
  - `vec_artifacts` table with `embedding F32_BLOB[384]` column.
  - At ingest, compute embedding via the configured provider and
    insert.
  - At search, query with cosine distance + RRF fusion against
    BM25 ranks.
- **embedded-onnx** (for air-gapped / offline):
  - `github.com/yalue/onnxruntime_go` (~30 MB binary footprint).
  - Bundled bge-small-en-v1.5 model (384-dim).
  - Behind a build tag (`onnx`) so default builds stay light.

**Effort.** Large.
- API providers: ~300 lines for all four + tests.
- sqlite-vec: ~250 lines + tests.
- ONNX: ~400 lines + integration tests; binary footprint cost.

**Decisions.**
1. Ship API-only first, defer sqlite-vec + ONNX? Recommended for
   non-air-gapped users.
2. ONNX runtime acceptable as a build-tagged option? It's a
   sensible default for standalone but adds dependency weight.

**Can ship now.** API providers + sqlite-vec yes after decisions.
ONNX path needs build-tag wiring.

---

## Suggested execution order

If you want to hand off the longest tail of work to a follow-up
session, the order that minimizes session-to-session blocking is:

1. **Phase 8 fold rendering** (no decisions, pure function).
2. **Phase 9 force-push** (no decisions, uses existing go-git).
3. **Phase 3 --watch** (no decisions, uses existing fsnotify).
4. **Phase 12 overlay integration** (no decisions).
5. **Phase 5 Postgres backend** (no decisions).
6. **Phase 16 retention + erasure** (no decisions; anchoring waits).
7. **Phase 17 OSV adapter** (NVD + GHSA after decisions).
8. **Phase 2 presigned URLs** (after AWS SDK decision).
9. **Vector store** (after decisions on ONNX + sqlite-vec).
10. **Phase 1 Sigstore** (largest, last; after Sigstore-go decision).

Items 1–6 don't need any decisions — they can be picked up by a
follow-up session today. Items 7–10 each carry a single library /
infrastructure choice the user needs to confirm.
