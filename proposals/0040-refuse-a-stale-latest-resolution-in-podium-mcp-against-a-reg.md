# Proposal 0040: Refuse a stale latest resolution in podium-mcp against a registry-signed artifact revision

- Issue: (to be filed)
- Status: Applied to spec (2026-10-03). Approved on 2026-10-03, decided on the user's behalf under the overnight authorization, and signed off as staged. OQ-1: accept the protocol change to podium/delivery-record/2 with the signed ingest-time revision; /1 never shipped in a release, so no released consumer breaks, and the semver variant would refuse honest backports. OQ-2: keep the staged key (registry and artifact ID), because identity keying needs an organization the bridge cannot see under trusted-headers; an identity switch is covered by podium cache reset-revisions. Recorded as a possible refinement.
- Date: 2026-10-03

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §4.7.10: the delivery record gains the served version's ingest time, framed after `sensitivity` under the tag `podium/delivery-record/2`, and the replay paragraph states what the MCP server now refuses and what it still does not detect (SPEC-1). `pkg/version` frames the field and owns its encoding (CODE-1).
- Registry: `pkg/registry/core` sets the served record's ingest time on every load result, and `pkg/registry/server` serves it as `artifact_revision` on the `load_artifact` response and every `ok` batch entry, covered by `delivery_hash` and kept out of the entity tag (CODE-2, CODE-3, SPEC-4). In the same step, `podium-mcp` parses the served `artifact_revision`, frames it in its delivery-hash check, and carries it through the content cache (CODE-3), so the registry and the bridge frame the same `/2` record from the first step that serves a non-empty value.
- §6.5 and §6.6 step 2: `podium-mcp` keeps a revision mark per registry and artifact ID in its existing index DB and refuses a fresh `latest` answer whose ingest time is below the mark, or below the first answer accepted earlier in the same session (SPEC-2). The marks live in a new bucket owned by `cmd/podium-mcp/resolution_cache.go`, with shared key and path helpers in a new `internal/revmark` package (CODE-4). The check runs in `deliverLoadArtifact` and in the §5.0 resources mirror, and a revalidated cache delivery and a mirror answer record the session reference without advancing the mark (CODE-5).
- §2.2 and §6.1: the MCP server's state statements name the in-memory session references as its only per-session state, list the index DB and its revision marks among its local state, and state that processes over one `PODIUM_CACHE_DIR` share the persisted marks one process at a time (SPEC-5). The footer of `docs/assets/diagrams/load-artifact-sequence.svg` drops its no-per-session-state clause (DOC-1 h).
- §6.9 and the §6.10 matrix: a new non-retryable code `materialize.stale_resolution` (SPEC-3, CODE-7).
- CLI: `podium cache reset-revisions [--dir DIR] [<artifact-id>...]` deletes persisted marks (CODE-6).
- Tests, docs, changelog, and manual validation follow (TEST-1 to TEST-4, DOC-1, CL-1, MV-1).

**Fixed decisions.**

- The signed value is the served record's own stored ingest time. A maximum over the caller's candidate versions is rejected: it depends on the reader's view, it can disclose a version a §6.3.1 load scope withholds, and §8.4 retention lowers it on an honest registry.
- The wire name is `artifact_revision`, an RFC 3339 UTC timestamp written in the fixed layout `2006-01-02T15:04:05.000000Z` (six fractional digits, a literal `Z`), as §7.2.1 requires of every control-plane timestamp. A zero or pre-1970 ingest time is written as the epoch, `1970-01-01T00:00:00.000000Z`. The consumer parses it to Unix microseconds and compares it as a `uint64`.
- The framing tag becomes `podium/delivery-record/2`, with the ingest time framed directly after `sensitivity`. The change adds no dual tag and no fallback verifier.
- The §12 entity tag stays `loadArtifactETag(content_hash, extends_pin)`.
- A `latest` resolution is a `load_artifact` whose trimmed `version` is empty and whose ID the §6.4 overlay does not serve. Every non-empty `version` (exact, range, `sha256:`) is never compared.
- Only a fresh `200` body answering a `latest` request is compared. Cache-served records (offline modes, HEAD match, `304`, §7.4 degraded fallback) are neither compared nor advance the mark.
- Reference value: the ingest time accepted for the first `latest` answer of that artifact in the request's effective `session_id` within this process, when one exists, and otherwise the persisted mark. Refuse when served < reference. Equality is accepted. The session reference is recorded by a fresh answer that is delivered, by a cached record delivered on a HEAD match or a `304` (`NoteSession`), and by a resources-mirror answer that passes the check (`NoteSession`, under the bridge's own session only). The last two never advance the mark.
- Session references live in process memory. A session whose registry pin was recorded on a lookup this process did not accept (a lookup before a `podium-mcp` restart, a lost response, a gate refusal, a lookup the bridge refused) keeps that pin, and its pinned answer is refused while it stays below the session reference or, with none, the mark. The remedy is a new session: a new host `session_id`, or a `podium-mcp` restart when the load names none. No reset is needed. This includes §13.2.1 read-only lag: a lookup the lagging replica answered pins the session to the older version, and the pin outlives read-only mode (`pkg/registry/core/core.go:1688-1690`, `:1715`).
- An absent or non-canonical served `artifact_revision` on a `latest` answer is refused with `materialize.stale_resolution`.
- The comparison runs directly after `verifyServedArtifact` and before the audit event, the §4.4.1 gates, and every write. The mark advances only inside the BoltDB transaction that writes the `id@latest` entry, to `max(mark, served)`. When served < mark (the session comparison admitted the answer, or a concurrent load advanced the mark after this load's check, as in the concurrent-load edge row), the load is delivered and that transaction writes nothing.
- `materialize.stale_resolution` carries `retryable: false`, details `artifact_id`, `served_version`, `served_revision`, and `reference_revision` (the value the answer was compared with: the session reference or the mark), and a `suggested_action` that does not open with a retry and names the new-session remedy for read-only lag.
- An answer carrying `X-Podium-Read-Only: true` is checked like any other. The header is unsigned, so it grants no exemption and the bridge does not read it.
- Mark key: normalized `PODIUM_REGISTRY` URL and the artifact ID, joined by NUL. The key carries no tenant, because the registry selects the tenant from the authenticated identity (§6.3) and `podium-mcp` holds no value that names it. Identities in different organizations that share one registry URL and one cache directory share one mark per artifact ID. The existing resolution-index keys are not rekeyed.
- Index DB unavailable: marks live in an in-process map, with one logged warning. A stored mark that does not parse is deleted with a warning naming its key and treated as absent.
- `podium cache reset-revisions` takes only `--dir` and positional artifact IDs. It matches IDs across every registry prefix. It prints `cache: no revision marks under <dir>` when the index file is absent, the bucket is absent, or the bucket holds no key, and `cache: reset <n> revision mark(s)` otherwise. No environment variable disables the check.
- Shared helpers live in `internal/revmark`, never under `pkg/`. No exported signature in `internal/revmark` names a bbolt type.
- Only `podium-mcp` enforces marks. `podium sync` and the SDKs receive `artifact_revision` and check nothing.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each spec edit by its quoted current text.
- **The `/2` framing breaks or empties existing framing tests in `pkg/version/version_test.go`.** `TestDeliveryHash_MatchesTheSpecSerialization` and `TestDeliveryHash_RepartitioningChangesTheDigest` hard-code `podium/delivery-record/1` and fail. `TestDeliveryHash_DiffersFromContentHashForTheSameBytes` keeps passing but stops testing anything: its fixture reproduces a §4.7.6 stream only while the untagged delivery stream frames an even number of values, and the inserted ingest time makes that count odd, so no record's untagged `/2` stream can reproduce a §4.7.6 stream and no reassignment of the fixture values restores it. Under `/2` the tagged stream frames an even number of values, and it reproduces the §4.7.6 stream of a package whose `ARTIFACT.md` bytes equal the tag. The separation of the delivery digest from a content hash therefore rests on ingest refusing such a package. CODE-1 rewrites all three tests in the same commit.
- **Test fixtures that build their own delivery record or stub break in two steps.** From S7, the registry and `SealDelivery` frame a non-empty revision (`FormatArtifactRevision` writes the epoch even for a zero `IngestedAt`), so every party that recomputes the hash frames the served revision from that step. From S9, every `latest` stub that serves no canonical `artifact_revision` is refused by the fail-closed rule. The CODE-3 and CODE-5 IMPLEMENTOR'S CHOICE markers state the rule the fixture edits satisfy.
- **Deprecation never changes `latest` for a stored version.** `(artifact_id, version)` is immutable (§4.7), and every store refuses a re-ingest with different bytes (`pkg/store/postgres.go` `ON CONFLICT ... DO NOTHING` followed by `ErrImmutableViolation`). A test that "re-ingests 2.0.0 as deprecated" fails at ingest. Deprecate by ingesting a new version born `deprecated: true`.
- **Records written straight to the store carry a zero `IngestedAt`.** Only ingest stamps it (`pkg/registry/ingest/ingest.go`). A fixture built from `PutManifest` records serves the epoch `1970-01-01T00:00:00.000000Z` for every version, the equality rule accepts every replay, and a replay assertion then passes or fails for the wrong reason. Fixtures set strictly increasing `IngestedAt` values or go through ingest.
- **The bridge always sends a `session_id`.** `cmd/podium-mcp/main.go:2467-2469` adds the process's own session ID when the host passes none, and the registry pins `latest` per session in memory (`pkg/registry/core/core.go:1650-1659`). Inside one `podium-mcp` process, a second `latest` load of the same ID returns the pinned version even after a newer ingest. Tests that ingest between loads spawn a new `podium-mcp` per load, which is also how the persisted mark is exercised.
- **`writeResolution` runs after the audit event, the gates, and the content-cache write** (`main.go:1729-1755`). The refusal must not move into it. The only refusal point is `checkFreshness` directly after `verifyServedArtifact`.
- **The registry pins a session on a HEAD, a `304`, and a resources-mirror fetch too.** `pkg/registry/core/core.go:1688-1690` records the pin before `assemble` answers either request kind, and `newRegistryRequest` sends the session on every call. A path that delivers such an answer and records no session reference leaves the session's later pinned answer to be compared with the mark, which is why CODE-5 calls `NoteSession` on the HEAD-match, `304`, and mirror paths.
- **A test that switches the served record must keep one server URL.** The mark key holds the normalized `PODIUM_REGISTRY` URL, so a replay or a lower revision served from a second stub on another port meets an empty key and is accepted. TEST-4 and MV-1 serve every step from one URL.
- **`handleResourcesRead` never reaches `deliverLoadArtifact`.** It verifies through `verifyServedArtifact` alone (`cmd/podium-mcp/resources.go:76-110`), so it needs its own call to the check.
- **BoltDB holds an exclusive file lock for the life of `podium-mcp`.** A second `podium-mcp` sharing `PODIUM_CACHE_DIR` times out after 2 s and runs on in-memory marks. `podium cache reset-revisions` cannot reach those, and only a restart clears them.
- **`bolt.Open` creates a missing file.** `podium cache reset-revisions` stats the index path first and reports "no revision marks" without creating it.
- **The `/1` framing never shipped.** `git tag --contains fe2d1118` (the commit that introduced the delivery record) lists no tag, and its changelog entry sits under `[Unreleased]`. The changelog therefore records no `/1` to `/2` transition.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §4.7.10 adds the ingest time to the delivery record, defines it, moves the tag to `/2`, and rewrites the replay paragraph.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2, SPEC-5. §6.5 gains the revision-mark rules, the reset command, and the trust boundary; §6.6 step 2 orders the check; §2.2 and §6.1 restate the MCP server's local and per-session state in the same commit.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.9 gains the `materialize.stale_resolution` row.
      Levels: —. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §7.2 lists `artifact_revision` and keeps it out of the entity tag; §7.6.2 adds it to the batch example.
      Levels: —. Depends on: S1
- [ ] **S5 · code** — CODE-1. `pkg/version` frames `ArtifactRevision` under `/2`, adds `FormatArtifactRevision`, and rewrites the existing framing tests the CODE-1 bullets name, including the domain-separation test.
      Levels: unit. Depends on: S1
- [ ] **S6 · code** — CODE-2. `core.LoadArtifactResult` carries the served record's ingest time.
      Levels: unit. Depends on: S5
- [ ] **S7 · code** — CODE-3. The registry serves and attests `artifact_revision` on the single and batch paths, `internal/testharness` seals it, `podium-mcp` parses and frames it and carries it through the content cache, and the test fixtures that recompute or serve the hash frame it under the CODE-3 IMPLEMENTOR'S CHOICE constraint.
      Levels: unit, integration, e2e. Depends on: S4, S6
- [ ] **S8 · code** — CODE-4. `internal/revmark` lands, and the resolution cache gains the `revisions` bucket, `Reference`, the session values, `NoteSession`, and `PutLatestAdvancing`.
      Levels: unit. Depends on: S2, S5
- [ ] **S9 · code** — CODE-5. `podium-mcp` checks the revision on fresh `latest` loads and in the resources mirror, records session references on the revalidated and mirror paths, the bridge and e2e stub fixtures serve and frame a default revision, and `newSignedArtifactFixture` gains `setRecord`.
      Levels: unit, integration, e2e. Depends on: S3, S7, S8
- [ ] **S10 · test** — TEST-2, CODE-7. Bridge unit tests for every path, and the §6.10 matrix cell. Bundled because `matrix-audit` fails a cell that no `// Matrix:` test cites, and the citing test is in TEST-2.
      Levels: unit. Depends on: S9
- [ ] **S11 · code** — CODE-6. `podium cache reset-revisions`.
      Levels: unit, e2e. Depends on: S8
- [ ] **S12 · test** — TEST-1. Unit tests for the framing, the core field, the served field, `SealDelivery`, and `internal/revmark`.
      Levels: unit. Depends on: S7, S8
- [ ] **S13 · test** — TEST-3. Integration test of honest regressions and a replay against a booted registry and the spawned bridge.
      Levels: integration. Depends on: S9
- [ ] **S14 · test** — TEST-4. End-to-end test of persistence across processes, the envelope over stdio, and the CLI reset and lock paths.
      Levels: e2e. Depends on: S9, S11
- [ ] **S15 · docs** — DOC-1. Error-code, HTTP API, CLI, harness-configuration, and operator-guide pages, the read-only entry of `deploy/runbook.md`, and the `load-artifact-sequence.svg` footer.
      Levels: —. Depends on: S9, S11
- [ ] **S16 · docs** — CL-1. The `[Unreleased]` entries.
      Levels: —. Depends on: S9, S11
- [ ] **S17 · docs** — MV-1. Manual-validation scenario S82.
      Levels: manual. Depends on: S9, S11

**Ordering constraints.** The spec steps land first. S8 (bridge storage) needs S5, because `internal/revmark.ParseRevision` calls `version.FormatArtifactRevision`, which CODE-1 adds; S7 needs S4, because it serves the §7.2 field that SPEC-4 defines. S6 and S7 (registry side) and S8 are independent of each other. S7 lands the registry's revision and the bridge's framing and cache carry of it together, because from S7 the registry and `SealDelivery` frame a non-empty revision, and a bridge that framed none would fail every spawned-bridge load in `test/integration` and `test/e2e` with `materialize.content_hash_mismatch`; every step from S7 on is green at the levels it lists. S9 needs S7 and S8, because the check reads the revision S7 parses and the marks S8 stores. S10 bundles the matrix cell with its citing test.

## Current state and the gap

### Replay of a validly signed record

§4.7.10 binds the served record to the registry's delivery signature, but the record carries no ordering value and no nonce, so a record captured from an earlier response verifies when it is replayed (`spec/04-artifact-model.md:942`; the record's fields are listed at `:932`). A network attacker, a replaying proxy, or a compromised intermediary HTTP cache can answer a `load_artifact` that asks for `latest` with an older, validly signed version, and `podium-mcp` accepts it. Proposal 0027 left freshness out (OQ-2 option c, `proposals/0027-attest-the-served-record-and-close-the-hidden-parent-disclosures.md:598`, `:605`) and named the follow-up in its Non-goals (`:647`): a monotonic check in the MCP server that refuses a `latest` resolution older than one it already resolved for the same artifact. No monotonic, high-water, or rollback logic exists under `cmd/podium-mcp` or `pkg/`.

### Why the record needs a new signed value

0027 sketched the follow-up as needing "no clock and no protocol change". The record's current fields cannot carry it. §4.7.6 defines `latest` as "the most recently ingested non-deprecated version visible under the caller's effective view, at resolution time", and the registry orders it by ingest time (`pkg/version/version.go:143-180` `ResolveLatest`, used by `pkg/registry/core/core.go:1661-1681`). An honest registry serves a `latest` with a lower semver whenever a backport is ingested after a newer major line, and when every version is deprecated it falls back to the newest-ingested version of the full set (`core.go:1675-1679`). The record excludes the lifecycle fields (`spec/04-artifact-model.md:934`) and carries no ingest time, so a client-side order on semver refuses honest backports.

The served version's own ingest time follows the order `latest` is defined by. Within the non-deprecated set, the resolved `latest` is the version with the greatest ingest time, so a backport raises it, and a new deprecated version leaves it unchanged. A stored `(artifact_id, version)` is immutable (§4.7, `spec/04-artifact-model.md:725`), so the value does not depend on when or by whom the record is read, and the `:934` exclusion principle holds unamended. The honest decreases are a §4.7.6 session pin, a §8.4 retention purge of the newest deprecated version while every version is deprecated, a layer unregister or §8.5 erasure, a shrinking view, a registry restored from an earlier backup, and a read replica that lags the primary, either while the registry serves from it in §13.2.1 read-only mode (`spec/13-deployment.md:41`, `:47-48`) or after an operator promotes it (`deploy/runbook.md:25-27`). §6.5 names the remedy for each.

### Session pins

The registry pins the first `latest` lookup per `session_id` in an in-memory map per registry process (`core.go:78-79`, `:1650-1659`, `:1695-1716`). The bridge adds its own per-process session ID to every call that lacks one (`cmd/podium-mcp/main.go:485-492`, `:2467-2469`), and the registry records the pin on a HEAD and a conditional GET as well as on a full GET (`pkg/registry/server/server.go:1056`, `pkg/registry/core/core.go:1688-1690`). A host may pass its own `session_id`, and a pinned answer for that session can be older than an answer another session already accepted. Comparing every answer with one global mark would refuse that honest answer. Comparing a session's later answers with the first answer the bridge accepted in that session admits it, while the first answer in each session is still compared with the persisted mark.

### The consumer-side store

`podium-mcp` keeps the §6.5 resolution index in BoltDB at `${PODIUM_CACHE_DIR}/.resolutions/index.db` (`cmd/podium-mcp/resolution_cache.go:52-77`, wired at `cmd/podium-mcp/main.go:599`). Its keys hold the artifact ID and version only (`resolution_cache.go:92-97`), and the store fails open: an open failure or a lock held past the 2 s timeout leaves `db` nil and every read and write no-ops (`:58-74`, `:100`, `:119`). `podium cache prune` is the only cache command (`cmd/podium/cache.go:20-37`, `spec/06-mcp-server.md:245`), and it skips `.resolutions` (`cache.go:85-90`), so no command resets anything stored there. No §6.10 code describes a validly signed but stale record. Reusing `materialize.signature_invalid` would trigger the §6.5 cache-miss refetch rule (`spec/06-mcp-server.md:243`), and the §6.10 matrix axis (`tools/matrix/matrices.go:78-123`) holds no freshness code.

## Decisions

- **The registry signs the served version's ingest time.** The adversarial review replaced the draft's per-caller maximum over candidate versions. That maximum depended on the reading identity's view, contradicting the `:934` principle; its inclusion of deprecated versions made the §8.4 retention purge lower it on an honest registry; and a maximum over candidates that a §6.3.1 version-pinned load scope cannot load disclosed that a newer version exists. The served record's ingest time has none of these properties and needs no candidate-set computation.
- **Encoding.** The value is an RFC 3339 UTC timestamp with exactly six fractional digits and a literal `Z`, because §7.2.1 (`spec/07-external-integration.md:46`, `:48`) writes every control-plane timestamp in RFC 3339 UTC and binds every control-plane endpoint. The fixed layout gives one canonical string per instant, so a non-canonical value is detectable and the consumer can refuse it. Postgres `timestamptz` keeps microseconds, SQLite stores RFC3339Nano text (`pkg/store/sqlite.go`), and the memory store keeps nanoseconds, so truncating to microseconds gives every replica reading one store the same value. `pkg/version.FormatArtifactRevision` is the single encoder, and `internal/revmark.ParseRevision` is the single parser. The bridge compares and stores the parsed Unix microseconds; the stored mark is consumer-side state and is not a control-plane field.
- **Tag `podium/delivery-record/2`.** Inserting a framed value shifts every later field, so a new tag keeps a `/1` stream and a `/2` stream from framing to the same bytes. Pre-1.0 policy rules out a dual tag. No release ever served `/1`. Inserting the field also makes the tagged stream frame an even number of values, so the separation of the delivery digest from a §4.7.6 content hash now rests on ingest refusing an `ARTIFACT.md` without frontmatter: a package whose `ARTIFACT.md` bytes equal the tag has none. CODE-1 pins that refusal in the rewritten `TestDeliveryHash_DiffersFromContentHashForTheSameBytes`.
- **The entity tag is unchanged.** The value is a property of the stored version, so it cannot differ between two responses that share a content hash and an `extends_pin` value, and conditional requests and the bridge's HEAD and `304` paths keep working.
- **Session-scoped reference.** The bridge tracks, per process and per effective `session_id`, the ingest time of the first `latest` answer it accepted for each artifact. A later answer in that session is compared with that value; the first answer in a session is compared with the persisted mark. The registry records a session pin on every `latest` lookup that carries a `session_id`, including a HEAD and a conditional GET (`pkg/registry/core/core.go:1688-1690`), so the bridge records the reference on every path that delivers or returns such an answer: a fresh answer that is delivered, a cached record delivered on a HEAD match or a `304`, and a resources-mirror answer that passes the check. When the bridge accepted the session's pinned answer first, an honest registry later returns that pinned version or, after a lost pin on another replica or after a registry restart, the current `latest`, and neither is below the reference. The first value, and not the session maximum, is the reference, so a fresh answer from a replica that lost the pin also passes. When the registry recorded the pin on a lookup this process did not accept, the bridge holds no reference and compares the pinned answer with the mark. That happens after a `podium-mcp` restart under a host-supplied `session_id` (the registry's pins have no expiry, `core.go:77-78`, so persisting references would need an unbounded store), after a response that never arrived, after a §4.4.1 gate refusal, and when a HEAD on one replica pins the session and the following full GET is answered by another. The pinned answer is then refused once another session has advanced the mark past it. The remedy is a new host session, whose first answer is the current `latest`; no reset is needed, so the refusal lowers no protection.
- **Index consistency.** `PutLatestAdvancing` writes the mark and the `id@latest` entry in one BoltDB transaction and writes neither when the served value is below the stored mark. The index therefore names a `latest` only after a fresh answer at or above the mark, which is why cache-served deliveries need no comparison.
- **Fail-closed on an unparseable served value.** A `latest` answer whose `artifact_revision` is absent or not canonical is refused. The value is covered by the verified delivery hash, so an unparseable one is a registry defect or a forgery under `never`, and admitting it would let the check be skipped.
- **Fail-open on a corrupt stored mark.** A party that can corrupt the file can also delete it, so refusing on corruption buys nothing against that party and blocks honest users.
- **No environment variable.** A variable left set would silently disable the check. The CLI already owns cache maintenance (§6.5).
- **`internal/revmark` instead of `pkg/freshness`.** Only `cmd/podium-mcp` and `cmd/podium` use the concern, and only the bridge imports bbolt today. `code-best-practices.md` directs such a surface to `internal/`.
- **Reset by artifact ID only.** A registry switch already starts with no marks under the key layout, every honest regression concerns specific artifacts or all of them, and a registry filter would make the CLI repeat the bridge's URL normalization.
- **No tenant in the mark key.** `PODIUM_TENANT_ID` is absent from the §6.2 table (`spec/06-mcp-server.md:19-43`); its only spec occurrence is a Helm value (`spec/13-deployment.md:603`). The registry selects the tenant from the authenticated identity (`spec/06-mcp-server.md:75`), and no code under `pkg/` or `internal/` reads the `X-Podium-Tenant` header that `podium-mcp` sets from it (`cmd/podium-mcp/main.go:2491-2492`). A key component taken from it would name no served tenant. Identities in different organizations that share one registry URL and one cache directory therefore share one mark per artifact ID, which is accepted: a colliding ID is refused until reset, and a separate `PODIUM_CACHE_DIR` per organization avoids it. OQ-2 covers adding the identity, which carries the organization, to the key.
- **Not retryable.** Every other `materialize.*` refusal the bridge originates is `retryable: false`, and a retry hint would steer users away from the remedies SPEC-2 (b) names. A retry in the same session repeats the refusal, because the registry keeps answering that session with its pinned version (`pkg/registry/core/core.go:1715`), including after §13.2.1 read-only mode ends. The bridge cannot tell read-only lag apart: `X-Podium-Read-Only` is an unsigned response header that a party on the path can add, so a `retryable` value keyed on it would let that party steer every refusal toward retries. The remedy for read-only lag and for a pinned session is stated once, in the SPEC-2 (b) reset paragraph, and the `suggested_action` names it.
- **Spec first.** Per `spec-driven-development.md`, the spec edits land through `implement-proposal` before code.

## Spec amendment: §4.7.10 Delivery Attestation

**SPEC-1.** `spec/04-artifact-model.md`, §4.7.10 "Delivery Attestation" (heading at line 928 at the time of writing). Three edits in one commit.

(a) In the paragraph that opens "The **delivery record** is the artifact identity, type, and resolved version the response carries" (line 932), replace:

> the `content_hash` of the stored artifact the registry composed the response from; the served `sensitivity`; the served `ARTIFACT.md` document,

with:

> the `content_hash` of the stored artifact the registry composed the response from; the served `sensitivity`; the ingest time of the served version; the served `ARTIFACT.md` document,

and, in the same paragraph, replace:

> the framed ASCII tag `podium/delivery-record/1`, then the framed identity, version, type, content hash, and sensitivity in that order,

with:

> the framed ASCII tag `podium/delivery-record/2`, then the framed identity, version, type, content hash, sensitivity, and ingest time in that order,

(b) Insert a new paragraph directly after that paragraph, before the paragraph that opens "The serialization carries a resource's content hash rather than its body":

> The **ingest time** is the time at which the registry stored the served `(artifact_id, version)`, written as an RFC 3339 timestamp in UTC with exactly six fractional digits and the zone written `Z` (for example `2025-01-01T00:00:00.000000Z`), and as `1970-01-01T00:00:00.000000Z` when the registry holds no ingest time for the version or holds one before that instant. A stored version is immutable (§4.7), so its ingest time does not depend on when or by whom the record is read. A consumer compares it only with ingest times the same registry served earlier, so no consumer clock reads it. The HTTP response serves it as `artifact_revision` (§7.2).

(c) The exclusion paragraph that opens "The serialization carries a resource's content hash rather than its body" (line 934) is unchanged; the ingest time satisfies its rule.

(d) In the closing paragraph (line 942), replace:

> It carries no timestamp and no nonce, so a record captured from an earlier response verifies when it is replayed.

with:

> It carries no nonce, and its ingest time names when the served version was stored rather than when the response was made, so a record captured from an earlier response verifies when it is replayed. The MCP server refuses a record that answers a `latest` resolution with an ingest time below one it already accepted for that registry and artifact (§6.5). Within one session, it compares a `latest` answer with the first `latest` answer it accepted for that artifact in that session. A replayed record that answers a pinned request, and any record a consumer that does not verify receives, are not detected. An honest registry serves a `latest` ingest time below one it served earlier when it answers a §4.7.6 session pin, after it is restored from an earlier backup, after a purge or erasure removes the newest versions (§8.4, §8.5), when the caller's view loses them, and when it serves from a read replica that lags the primary, either in read-only mode or after that replica is promoted (§13.2.1). §6.5 states which of these the MCP server refuses and the remedy for each.

The rest of the paragraph, from "It binds what the registry served", is unchanged.

## Spec amendment: §6.5 Cache and §6.6 step 2

**SPEC-2.** `spec/06-mcp-server.md`. Three edits in one commit.

(a) §6.5 "Cache". Replace the line (line 245 at the time of writing):

> Index DB: BoltDB or SQLite. `podium cache prune` for cleanup.

with:

> Index DB: BoltDB or SQLite. `podium cache prune` removes content-cache entries and leaves the index DB unchanged.

(b) Insert three paragraphs directly after that line, before the paragraph that opens "In contexts where the home directory is ephemeral":

> **Revision marks.** The MCP server keeps a revision mark in its index DB for each registry and artifact ID: the greatest §4.7.10 ingest time it has accepted for a `latest` resolution of that artifact. The registry is identified by its `PODIUM_REGISTRY` URL with the scheme and host lowercased, a default port removed, and a trailing slash removed. The key names no tenant, so identities in different organizations that use one registry URL and one cache directory share a mark for an artifact ID. A `load_artifact` request whose `version` is empty and whose ID the §6.4 workspace overlay does not serve is a `latest` resolution. When the registry answers one with a full response body, the MCP server compares the served ingest time with a reference value and refuses the load with `materialize.stale_resolution` when the served value is lower. An equal value is accepted. The reference is the session reference for that artifact under the request's `session_id`, when one exists, and otherwise the mark. The session reference is the ingest time of the first `latest` answer for that artifact under that `session_id` that the MCP server accepted during its lifetime, where an accepted answer is a full response body that passed this check and was delivered, a cached record delivered on a revalidation match or a `304`, or a §5.0 resources-mirror answer that passed this check, which records the reference for the MCP server's own session because a resources read carries no `session_id`. The response to a `latest` request that carries `X-Podium-Read-Only` (§13.2.1) is compared the same way. A `latest` answer whose ingest time is absent or is not written as §4.7.10 states is refused the same way. A request that names a version, a range, or a content hash is never compared with the mark. A record served from the content cache, whether in `offline-first` or `offline-only`, on a revalidation match, on a `304`, or by the §7.4 degraded-network fallback, is neither compared nor advances the mark, because the index records a `latest` only after a fresh response passes this check. The resolution index is not keyed by registry, so after `PODIUM_REGISTRY` changes over one cache directory, a cache-served `latest` is not checked against the new key's mark. The check runs before the §4.4.1 gates, and the mark advances only in the index transaction that records the `latest` resolution, to the greater of the mark and the served value, so a load a later gate refuses leaves the mark and the session reference unchanged. A `latest` answer below the mark that the session comparison accepts is delivered and leaves both the mark and the index's `latest` entry unchanged. The §5.0 resources mirror runs the same comparison, records the session reference for the MCP server's own session when none exists, and never advances the mark. When the index DB cannot be opened, the MCP server holds its marks in memory for its lifetime. A stored mark that cannot be read is discarded and treated as absent.
>
> `podium cache reset-revisions` deletes the marks persisted under a cache directory, optionally filtered by artifact ID, across every registry. It changes only the marks persisted in the index DB, and it fails while any MCP server holds that DB. Every MCP server that uses the cache directory must be stopped first, because a process that could not open the index DB keeps its marks in memory until it exits, and every process keeps its session references in memory until it exits. An operator runs it after a registry is restored from an earlier backup, after a purge or erasure removes the newest versions of an artifact, after the caller's view loses them, after a read replica that lagged the primary is promoted to primary, and after an identity in another organization loads an artifact ID that collides under the shared key, because each of these lowers the ingest time an honest registry serves for `latest`. Two honest decreases need no reset, and both clear with a new session. A session whose §4.7.6 pin the registry recorded on a lookup this MCP server process did not accept, such as a lookup made before the MCP server restarted, a lookup whose response did not arrive, a load a §4.4.1 gate refused, or a lookup the MCP server refused, keeps that pin. Its pinned answer is compared with the session reference when one exists and with the mark otherwise, and it is refused while it stays below that value. While the registry serves from a lagging replica in read-only mode (§13.2.1), a `latest` below the reference is refused, and the registry records the replica's answer as the session's pin. That pin outlives read-only mode, so the session stays refused for that artifact after the registry serves from the primary again; a session that made no `latest` lookup of the artifact while the replica lagged loads the current `latest` once read-only mode ends. A new session's first answer is the current `latest`. The host starts one by passing a new `session_id`, and a load that passes no `session_id` gets one when the MCP server restarts, because the MCP server's own session lasts for its process.
>
> The mark protects against a party between the registry and the MCP server, such as a network attacker, a replaying proxy, or an intermediary HTTP cache, that serves an older, validly signed record for a `latest` resolution. It gives no protection against a party that can write `PODIUM_CACHE_DIR`, which can also roll back the cache or delete the mark, including in `offline-first` and `offline-only`, where the cache is the source of truth. The first `latest` answer for an artifact under a cache directory with no mark is accepted. Under `PODIUM_VERIFY_SIGNATURES=never`, a party on the path can rewrite the ingest time together with the delivery hash, so the check then refuses only a replay of an unmodified record. Only the MCP server keeps marks: server-source `podium sync` and the language SDKs receive the ingest time and compare nothing (§4.7.10), and rollback detection for `podium sync` is the §7.5.3 lock.

(c) §6.6 step 2 "**Verify.**" (line 254). Insert this sentence directly after the sentence that ends "The MCP server then applies the §4.7.9 verification policy to `(delivery_hash, delivery_signature)`.":

> For a `latest` resolution delivered from a registry response body, the §6.5 revision-mark check runs after the delivery-hash comparison and the §4.7.9 policy and before the MCP server writes the load's event to its local audit sink, before any §4.4.1 gate, and before anything is cached or written.

## Spec amendment: §6.9 Failure Modes

**SPEC-3.** `spec/06-mcp-server.md`, §6.9 "Failure Modes" table. Insert a row directly after the row whose first cell is "Delivery-hash mismatch at the §6.6 step-2 check":

> | Latest resolution older than the revision mark | Fail with `materialize.stale_resolution`; do not cache, index, or write to disk. The envelope names the artifact, the served version, the served artifact revision, and the reference revision the answer was compared with, which is the session reference or the mark (§6.5). A pinned request is never compared with the mark. |

§6.10 is unchanged: its namespace sentence already admits `materialize.*`, and the code enters through this row and the §6.10 matrix cell (CODE-7).

## Spec amendment: §7.2 integrity fields and §7.6.2 batch example

**SPEC-4.** `spec/07-external-integration.md`. Three edits in one commit.

(a) §7.2, "Integrity and reference fields" (line 34). Replace:

> The registry's HTTP `load_artifact` response carries three fields beside the manifest and the resources:

with:

> The registry's HTTP `load_artifact` response carries these fields beside the manifest and the resources:

and insert this bullet directly after the `delivery_signature` bullet:

> - `artifact_revision`: the §4.7.10 ingest time of the served version, an RFC 3339 UTC timestamp in the form §4.7.10 fixes, covered by `delivery_hash`. Present on every response that carries `delivery_hash`, including every `status: ok` batch entry.

(b) §7.2, the paragraph that opens "These are fields of the HTTP response" (line 40). Append to the end of the paragraph:

> The artifact revision does not enter the entity tag.

(c) §7.6.2 "Wire format" JSON example (lines 699-719). In the `ok` entry, insert `"artifact_revision": "2025-01-01T00:00:00.000000Z",` on its own line directly after `"delivery_signature": "...",`. The `error` entry is unchanged.

§7.2.1 is unchanged. The field is a timestamp written in RFC 3339 UTC, so its rule "A timestamp is RFC 3339 in UTC" (`spec/07-external-integration.md:46`) holds without an exception.

## Spec amendment: §2.2 and §6.1 MCP server state

**SPEC-5.** `spec/02-architecture.md` §2.2 and `spec/06-mcp-server.md` §6.1 "The Bridge". Lands in the SPEC-2 commit (S2), because SPEC-2 (b) gives the MCP server a per-session reference that decides whether a load is refused and persisted marks that a later process reads, and both sites state the opposite today. The sequence-diagram footer that repeats the claim is a docs file and is edited in DOC-1 (h).

(a) `spec/02-architecture.md`, the "**Podium MCP server**" paragraph (line 93 at the time of writing). Replace:

> Holds no per-session server-side state; local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

with:

> Its only per-session state is the §6.5 session reference, held in memory per `session_id` and artifact. Its other local state is a content-addressed disk cache, the §6.5 index DB with its revision marks (held in memory when the index DB cannot be opened), OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. MCP server processes share no in-memory state. Processes that use one `PODIUM_CACHE_DIR` share its content cache and, one process at a time, its index DB and the revision marks it holds (§6.5).

(b) `spec/06-mcp-server.md` §6.1, first paragraph (line 5 at the time of writing). Replace:

> It holds no per-session server-side state. Local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

with the same text as (a), whose "Its" refers to the MCP server named in the preceding sentences of the paragraph:

> Its only per-session state is the §6.5 session reference, held in memory per `session_id` and artifact. Its other local state is a content-addressed disk cache, the §6.5 index DB with its revision marks (held in memory when the index DB cannot be opened), OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. MCP server processes share no in-memory state. Processes that use one `PODIUM_CACHE_DIR` share its content cache and, one process at a time, its index DB and the revision marks it holds (§6.5).

"One process at a time" states the BoltDB lock that CODE-4 relies on: a second process times out on the open and keeps its marks in memory (§6.5, SPEC-2 b).

## Proposed solution

### CODE-1. `pkg/version`: frame the revision under `/2`

`pkg/version/version.go` (`deliveryRecordTag` :321, `DeliveryRecord` :327-347, `DeliveryHash` :373-392).

- `const deliveryRecordTag = "podium/delivery-record/2"`.
- `DeliveryRecord` gains `ArtifactRevision string` directly after `Sensitivity`, documented as the §4.7.10 ingest time of the served version.
- `DeliveryHash` frames `rec.ArtifactRevision` directly after `rec.Sensitivity`.
- New `func FormatArtifactRevision(t time.Time) string`: `t.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")`, with `t` replaced by `time.Unix(0, 0)` when `t.IsZero()` or `t.UnixMicro() < 0`, so the zero and pre-1970 cases write `1970-01-01T00:00:00.000000Z`. `// Spec: §4.7.10, §7.2.1`.
- The `DeliveryHash` doc comment (`:349-372`) keeps its rule that the digest does not depend on who reads the record or when, names the framed tag as `"podium/delivery-record/2"` (`:350`), and names the ingest time among the framed fields after the sensitivity. Its tag-separation paragraph (`:356-361`), which states that without the tag a stream could parse as both a delivery record and a stored package, is replaced with the `/2` rationale: the inserted ingest time gives every untagged stream an odd frame count, so none reproduces a §4.7.6 stream; the tagged stream reproduces only the content hash of a package whose `ARTIFACT.md` bytes equal the tag; and ingest refuses that package because `manifest.ParseArtifact` returns `manifest.ErrNoFrontmatter`. The paragraph keeps its statement that both digests share the framing, the hex encoding, and the registry-managed signing key.
- In the same commit, rewrite `TestDeliveryHash_MatchesTheSpecSerialization` (`version_test.go:429-479`) to frame `/2` with the revision after sensitivity as the amended §4.7.10 text states, and update `TestDeliveryHash_RepartitioningChangesTheDigest` (`:481-522`) to the `/2` tag. Do not add a second golden.
- In the same commit, rewrite `TestDeliveryHash_DiffersFromContentHashForTheSameBytes` (`:542-566`), whose fixture and comment ("Without the tag the two digests would coincide") assume the `/1` field count. Frames are length-prefixed, so two equal streams frame the same number of values. The untagged `/2` stream frames nine scalar values plus two per resource, an odd count, and a §4.7.6 stream frames an even count, so no untagged `/2` stream reproduces one. The rewritten test pins the separation that holds under `/2`, which is that the tag occupies the `ARTIFACT.md` slot of the coinciding §4.7.6 stream and no ingestible `ARTIFACT.md` equals the tag. It builds the record `ID "s"`, `Version "p1"`, `Type "v1"`, `ContentHash "p2"`, `Sensitivity "v2"`, `ArtifactRevision "p3"`, `Frontmatter "v3"`, `ManifestBody "p4"`, `SkillRaw "v4"`, and `Resources {"p5": "v5"}`, and asserts the following. As a fixture self-check, `sha256Hex(deliveryStream("podium/delivery-record/2", "s", "p1", "v1", "p2", "v2", "p3", "v3", "p4", "v4", "p5", "v5"))` equals `CanonicalContentHash([]byte("podium/delivery-record/2"), []byte("s"), {"p1": "v1", "p2": "v2", "p3": "v3", "p4": "v4", "p5": "v5"})`. `DeliveryHash(rec)` equals `"sha256:"` plus that value, which pins that the tag is the only frame standing in the `ARTIFACT.md` slot. `manifest.ParseArtifact([]byte("podium/delivery-record/2"))` returns an error that `errors.Is` matches to `manifest.ErrNoFrontmatter` (`pkg/manifest/parse.go:17`, `:41-46`), the error ingest turns into an invalid-artifact refusal (`pkg/registry/ingest/ingest.go:1119-1122`), so no stored version has that content hash. The comment states that the separation rests on that refusal. `pkg/manifest` imports no other Podium package, so the test adds no import cycle. `// Spec: §4.7.10, §4.7.6`.

### CODE-2. `pkg/registry/core`: the served record's ingest time

`pkg/registry/core/core.go` (`LoadArtifactResult` :1434, `assembleResult` :1728, `revalidationResult` :1745).

- `LoadArtifactResult` gains `ArtifactRevision string` with a doc comment citing §4.7.10.
- `assembleResult` sets `ArtifactRevision: version.FormatArtifactRevision(rec.IngestedAt)` from the resolved record, and `revalidationResult` sets the same value, so the content-hash, session-pin, `latest`, and pinned paths all carry it.
- The change adds no candidate-set computation and no new store query. `// Spec: §4.7.10, §4.7.6`.

### CODE-3. `pkg/registry/server`, `internal/testharness`, and the `podium-mcp` delivery check: serve, attest, and frame

- `pkg/registry/server/delivery.go` `deliveryRecordOf` (:28-44) sets `ArtifactRevision: res.ArtifactRevision`.
- `pkg/registry/server/server.go`: `LoadArtifactResponse` gains `ArtifactRevision string \`json:"artifact_revision"\`` with no `omitempty`, matching `delivery_hash`. It is filled at `:1087-1101` beside the other `res` fields, so it is set on every response.
- `pkg/registry/server/batch_load.go`: `BatchLoadEnvelope` gains `ArtifactRevision string \`json:"artifact_revision,omitempty"\`` (error entries carry no record), filled in the `env` literal at `:123-135`.
- `validatorFor` and `loadArtifactETag` (`server.go:1127-1148`) are unchanged.
- `internal/testharness/delivery.go` (`SealDelivery` :21-24 and its `deliveryRecordOf` :29-63) reads `artifact_revision` from the stub fields into `DeliveryRecord.ArtifactRevision`, writes `1970-01-01T00:00:00.000000Z` into the stub when the field is absent, and frames it.
- The bridge frames what the registry serves, in the same commit, because from this step every registry response and every `SealDelivery` stub frames a non-empty revision:
  - `loadArtifactResponse` (`cmd/podium-mcp/main.go:2132`) gains `ArtifactRevision string \`json:"artifact_revision"\``. `verifyDeliveryHash` (`:1934-1961`) frames it into the `DeliveryRecord` it builds at `:1938-1947`.
  - The cache carries the revision on both sides. `deliveryFiles` (`main.go:2796`) gains `ArtifactRevision`. `cacheVerifiedRecord` (`main.go:1658-1672`) sets `ArtifactRevision: resp.ArtifactRevision` in its `deliveryFiles` literal, and `putDelivery` (`:2809`) writes it to an `artifact_revision` file unconditionally, including when it is empty. `readDeliveryFiles` (`cmd/podium-mcp/resolution_cache.go:302`) adds `artifact_revision` to its required-file list (`:304`), so a missing file is an error and an empty file reads as the empty string, as for `frontmatter` and `body`; a record cached by an earlier build therefore reads as a cache miss. `loadArtifactFromCache` (`resolution_cache.go:244`) sets `ArtifactRevision: d.ArtifactRevision` in the `loadArtifactResponse` it builds (`:250-259`). Without these, `verifyDeliveryHash` frames an empty revision for every cache-served record of a registry that served one and fails it with `materialize.content_hash_mismatch`, which `test/integration/mcp_cache_offline_test.go` observes on its offline load at `:78` after the live load against `registryharness.New` at `:59-69`.
  - This step adds no comparison, so a response that serves no `artifact_revision` still verifies when its hash framed the empty string.
- **IMPLEMENTOR'S CHOICE:** which test fixtures to edit, and how, in this commit and in the CODE-5 commit — every Go site matched by `DeliveryRecord\{|deliveryFiles\{|SealDelivery\(|deliveryHashOf\(|sealDelivery\(|loadArtifactJSON\(|\.DeliveryHash\(|"delivery_hash"` frames and serves the same `artifact_revision` that the sealed or served record carries (the epoch `1970-01-01T00:00:00.000000Z` by default from S9). No test is deleted, skipped, or weakened, and each test's discriminating difference stays the only difference it asserts. Because `putDelivery` writes the `artifact_revision` file even when it is empty, a `deliveryFiles` literal without a revision still reads back as a cache hit (`cmd/podium-mcp/resolution_cache_test.go:142`), so such a literal fails the delivery-hash check only where the sealed record framed a non-empty revision. The step criteria below and in CODE-5 pass.
- `cmd/podium/sign.go:143-158` reads the served `delivery_hash` and does not recompute it, so it needs no change. The implementor confirms this before closing the step.
- The criterion for the step is that `go test ./pkg/... ./internal/... ./cmd/podium-mcp/ ./test/integration/ ./test/e2e/` passes.

### CODE-4. `internal/revmark` and the resolution cache: the mark store

New package `internal/revmark` (`revmark.go`):

- `const DirName = ".resolutions"`, `func IndexPath(cacheDir string) string` (`<cacheDir>/.resolutions/index.db`), and the bucket name `revisions` as an unexported constant with an exported accessor used by the bridge.
- `type Key struct{ Registry, ArtifactID string }`, `func NormalizeRegistry(raw string) string` (scheme and host lowercased, `:80` on `http` and `:443` on `https` removed, trailing `/` removed; an unparseable URL is returned trimmed), and `func (k Key) Encode() []byte` joining the two with `\x00`.
- `func ParseRevision(s string) (uint64, error)`: parses `s` with the layout `2006-01-02T15:04:05.000000Z`, and accepts it only when `version.FormatArtifactRevision` of the parsed time returns `s` unchanged and the time is not before the epoch; it returns the time's Unix microseconds. It rejects the empty string, a decimal integer, a value with fewer or more than six fractional digits, a numeric zone offset, and a time before 1970. A sentinel `ErrMalformedRevision` wraps each failure. `FormatArtifactRevision(time.UnixMicro(int64(n)))` is its inverse, and the bridge uses it to write `reference_revision`.
- `var ErrIndexLocked = errors.New("revmark: index DB is locked")`.
- `func Reset(ctx context.Context, cacheDir string, ids []string) (deleted int, found bool, err error)`: `found` reports whether the bucket held at least one mark before the call. It returns `(0, false, nil)` without creating anything when `IndexPath(cacheDir)` does not exist; otherwise opens it with `bolt.Options{Timeout: 2 * time.Second}`, maps bbolt's timeout error to `ErrIndexLocked`, and deletes every key in the bucket whose decoded artifact-ID component is in `ids`, or every key when `ids` is empty. A missing bucket and a bucket with no key return `(0, false, nil)`; a missing bucket is checked for before any `ForEach` or `Delete` and is not created. An `index.db` written by a released `podium-mcp` holds only `resolutionBucket` (`cmd/podium-mcp/resolution_cache.go:68-70`), so a reset run before this release's `podium-mcp` has opened the directory takes this branch. A bucket with at least one key returns `found` true and the number deleted, which is 0 when no key matches `ids`. No exported signature names a bbolt type. `// Spec: §6.5`.

`cmd/podium-mcp/resolution_cache.go`:

- `newResolutionCache` opens `revmark.IndexPath(cacheDir)` and creates the `revisions` bucket beside `resolutionBucket` in the same `Update`.
- `resolutionCache` gains `mem map[string]uint64` (marks when `db` is nil) and `sessions map[sessionMarkKey]uint64` (session references, always in memory), both guarded by the existing `r.mu`. The comment on `r.mu` names the invariant: it serializes every bucket access and both maps.
- When the open fails, the constructor logs one `log.Printf` warning naming the cache directory and stating that revision marks are held in memory for this process.
- Marks are stored in the bucket as the decimal Unix microseconds, written with `strconv.FormatUint` and read with `strconv.ParseUint(v, 10, 64)`. The stored mark is consumer-side state, so §7.2.1 does not govern it.
- `func (r *resolutionCache) Reference(k revmark.Key, session string) (uint64, bool)` returns the session reference for `(k, session)` when one exists, and otherwise the stored mark (from the bucket, or from `mem` when `db` is nil). A stored value that `strconv.ParseUint` rejects is deleted, a warning naming the encoded key (never the value) is logged, and the call reports no mark.
- `func (r *resolutionCache) NoteSession(k revmark.Key, session string, revision uint64)`: under `r.mu`, when `session` is non-empty and `sessions` holds no reference for `(k, session)`, records `revision` as that reference. It never reads or writes the bucket or `mem`, and it never changes an existing reference. Its callers are the bridge's revalidated-delivery path and the resources mirror (CODE-5). When it does not run on one of those paths, the session's later pinned answer is compared with the mark and can be refused, which TEST-2 (o) observes.
- `PutLatest` is replaced by `func (r *resolutionCache) PutLatestAdvancing(k revmark.Key, session, id, version, contentHash string, revision uint64, now time.Time)`. In one `db.Update` it reads the stored mark; when `revision >= mark` it writes `max(mark, revision)`, the `id@latest` entry, and the `id@semver` entry; when `revision < mark` it writes nothing. When `sessions` holds no reference for `(k, session)`, it records `revision` as that reference, whatever the comparison with the stored mark; an existing session reference is never changed, so it stays the session's first accepted answer. With `db` nil it applies the same rule to `mem`. It returns nothing, like `PutLatest` today.
- On a nil `*resolutionCache`, which most bridge tests build by leaving `mcpServer.resolutions` unset, `Reference` returns `(0, false)` and `NoteSession` and `PutLatestAdvancing` do nothing, matching the `r == nil || r.db == nil` guards on `putEntry` and `getEntry` (`resolution_cache.go:99-101`, `:118-120`). A nil cache therefore compares nothing and refuses only an absent or non-canonical `artifact_revision`, which `checkFreshness` rejects before it reads a reference.
- `cmd/podium/cache.go` `cachePrune` uses `revmark.DirName` in its dot-directory comment and keeps skipping every dot-prefixed directory.

**IMPLEMENTOR'S CHOICE:** the internal layout of `sessionMarkKey` and of the `mem` map keys. The constraint: two sessions, two registries, and two artifact IDs never share an entry, and the maps are only read and written under `r.mu`. TEST-2 (u) pins the artifact-ID part of that constraint for both maps, and the third-session step of TEST-2 (d) pins the session part for session references.

### CODE-5. `cmd/podium-mcp`: the check

`cmd/podium-mcp/main.go`, `cmd/podium-mcp/resources.go`, and `cmd/podium-mcp/error_envelope.go`. The served field, its framing in `verifyDeliveryHash`, and its content-cache carry land earlier, in CODE-3.

- The server builds one `revmark.Key` prefix at startup from `revmark.NormalizeRegistry(cfg.registry)`. `cfg.tenantID` is not part of the key.
- Extract `func (s *mcpServer) effectiveSessionID(args map[string]any) string`: the trimmed `session_id` argument when non-empty, otherwise `s.sessionID`. The query builder at `main.go:2467-2469` uses it, so the session the registry sees and the session the check uses are the same value.
- `resolutionWrite` (`main.go:1631`) gains `Session string`. Every literal that builds one sets it from `effectiveSessionID(args)`: `deliverFreshLoad` (`main.go:1474`) and the HEAD-match and `304` literals (`main.go:1398`, `:1425`).
- New `func (s *mcpServer) checkFreshness(resp loadArtifactResponse, session string) (uint64, map[string]any)`: parses `resp.ArtifactRevision` with `revmark.ParseRevision`, reads `s.resolutions.Reference(key(resp.ID), session)`, and returns the parsed value with a nil envelope when there is no reference or `served >= reference`. Otherwise, and on a parse failure, it returns `errorEnvelope("materialize.stale_resolution", msg, details, retryable, suggested)` with `details` holding `artifact_id`, `served_version`, `served_revision` (the raw served string), and `reference_revision` (the reference written with `version.FormatArtifactRevision(time.UnixMicro(int64(ref)))`, or empty when none existed), and `retryable`, `suggested` from `bridgeCodeMeta`. The check reads no response header, so an answer carrying `X-Podium-Read-Only: true` is checked like any other. `// Spec: §6.5, §4.7.10`.
- `deliverLoadArtifact` calls `checkFreshness` directly after `verifyServedArtifact` (`main.go:1729-1731`) when `o.resolution != nil && !o.resolution.RefreshOnly && o.resolution.Version == ""`, and returns the envelope on refusal. This precedes `auditLoadArtifact`, the §4.4.1 gates, `cacheVerifiedRecord`, and `writeResolution`.
- `writeResolution`'s `latest` branch calls `PutLatestAdvancing` with the key, `w.Session`, and the parsed revision that `deliverLoadArtifact` threads through from `checkFreshness`.
- `writeResolution`'s `RefreshOnly` branch (`main.go:1644-1647`), which runs only for a cached record delivered on a HEAD match or a `304`, keeps `RefreshLatest` and, when `w.Version == ""`, parses `resp.ArtifactRevision` with `revmark.ParseRevision` and calls `NoteSession(key(resp.ID), w.Session, parsed)`. A parse failure skips the call; the record passed the delivery check, and the cache path is not compared. The registry recorded a §4.7.6 pin for that lookup (`pkg/registry/core/core.go:1688-1690`), so this records the matching reference. The cached record is the index's `id@latest`, which `PutLatestAdvancing` writes only at the mark, so the reference it records equals the mark unless a reset removed the mark.
- `handleResourcesRead` (`resources.go:76`) calls `checkFreshness(resp, s.effectiveSessionID(loadArgs))` directly after its `verifyServedArtifact` call, unconditionally because the mirror always requests `latest`, and returns the envelope on refusal. On a pass it calls `NoteSession(key(resp.ID), s.effectiveSessionID(loadArgs), parsed)` before returning the text. It never calls `PutLatestAdvancing`. `resources/read` takes only a URI and `loadArgs` is `{"id": id}` (`resources.go:76-88`), so the session is always the bridge's own `s.sessionID`, which `fetchJSON` also sends (`main.go:2467-2469`); a mirror answer never records a reference for a host-supplied session.
- `bridgeCodeMeta` (`error_envelope.go:129`) gains `case "materialize.stale_resolution": return false, "A party between the registry and the MCP server served an older record for a latest load, or the registry served it from a lagging replica in read-only mode. A new session loads the current latest once the registry is out of read-only mode: pass a new session_id, or restart the MCP server when the load passes none. If the registry was restored from a backup, a lagging replica was promoted, or the newest version left the caller's view, stop every MCP server that uses this cache directory and run podium cache reset-revisions <id>."`.
- **IMPLEMENTOR'S CHOICE:** which bridge and e2e stub fixtures gain the epoch default, and where — the CODE-3 fixture constraint holds, and every stub that answers a `latest` load, including every stub the §5.0 resources mirror reads, serves a canonical `artifact_revision`.
- `test/e2e/signed_artifact_helpers_test.go`: `signedArtifactSpec` gains `ArtifactRevision`, defaulting to `1970-01-01T00:00:00.000000Z`, which `newSignedArtifactFixture` frames and serves. The fixture also gains `setRecord(version, revision string)`, which recomposes the frontmatter for `version`, recomputes the content and delivery hashes, re-signs, and swaps the served fields under `f.mu`, so a test serves a different record from the same URL. TEST-4 uses it.
- The criterion for the step is that `go test ./cmd/podium-mcp/ ./test/e2e/ ./test/integration/` passes.

### CODE-6. `cmd/podium`: `podium cache reset-revisions`

`cmd/podium/cache.go`.

- `cacheCmd` lists `{"reset-revisions", "Delete the revision marks podium-mcp keeps for latest loads."}` and dispatches to `cacheResetRevisions(args[1:])`.
- Extract the `--dir` resolution from `cachePrune` (`cache.go:42-56`) into `resolveCacheDir(dir string) (string, error)`, used by both subcommands.
- `cacheResetRevisions`: flags `--dir` only; positional arguments are artifact IDs. It calls `revmark.Reset(ctx, dir, ids)`.
  - Exit 0 and `cache: no revision marks under <dir>` on stdout when `Reset` returns `found` false: the index file is absent, the bucket is absent, or the bucket holds no key. A cache directory a `podium-mcp` of this release has opened always holds the bucket, so this is the output for one whose bucket is empty.
  - Exit 0 and `cache: reset <n> revision mark(s)` when `Reset` returns `found` true, including `n = 0` when no key matches the given IDs.
  - On `ErrIndexLocked`, exit 1 and `error: <index path> is in use by a running podium-mcp; stop every MCP server that uses this cache directory and retry. Marks a podium-mcp holds in memory are cleared when it exits.` on stderr.
  - Any other error: exit 1 with `error: <err>`. A flag parse error follows `parseExit`.

### CODE-7. `tools/matrix`: the §6.10 cell

`tools/matrix/matrices.go` §6.10 axis (:78-123): insert `"materialize.stale_resolution",` after `"materialize.content_hash_mismatch",`.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Backport ingested after a newer major line | `latest` moves to the backport with a higher ingest time; accepted | §4.7.10 (SPEC-1 b); `docs/consuming/configure-your-harness.md` (DOC-1 e) |
| New version ingested with `deprecated: true` | `latest` and its ingest time are unchanged; accepted by equality | §6.5 "An equal value is accepted" (SPEC-2 b); DOC-1 e |
| Every version deprecated | `latest` is the newest-ingested version of the full set; accepted while no purge removes it | §4.7.10 (SPEC-1 d); DOC-1 e |
| §8.4 retention purges the newest version while every version is deprecated | Ingest time drops; refused until `podium cache reset-revisions` | §4.7.10 (SPEC-1 d), §6.5 (SPEC-2 b); `docs/reference/error-codes.md` (DOC-1 a) |
| Layer unregister, §8.5 erasure, a shrinking view, or a new identity with a smaller view | Refused until reset; OQ-2 asks whether to key by identity | §6.5 (SPEC-2 b); DOC-1 a, DOC-1 e |
| Registry restored from an earlier backup | Refused on every `latest` whose newest version postdates the backup, until each consumer resets | §6.5 (SPEC-2 b); `docs/deployment/operator-guide.md` (DOC-1 f) |
| Registry in §13.2.1 read-only mode serving from a replica that lags the primary | A `latest` below the reference is refused like any other, including when it carries `X-Podium-Read-Only: true`. Recovery follows the read-only remedy in the SPEC-2 (b) reset paragraph; no reset is needed | §4.7.10 (SPEC-1 d), §6.5 (SPEC-2 b); `deploy/runbook.md` (DOC-1 g), DOC-1 a |
| Permanent failover that promotes a lagging replica | Refused on every `latest` whose newest version the replica lost, until each consumer resets | §6.5 (SPEC-2 b); `deploy/runbook.md` (DOC-1 g), DOC-1 a |
| Host-supplied `session_id` whose pinned answer is below the mark, after this process accepted the session's first answer | Delivered, compared with the session's first answer; mark and `id@latest` unchanged | §6.5 (SPEC-2 b); DOC-1 e |
| Session's first answer delivered from the cache on a HEAD match or a `304`, or returned by the resources mirror (the bridge's own session only) | `NoteSession` records the session reference; the session's later pinned answer is compared with it and delivered | §6.5 "an accepted answer is" (SPEC-2 b); DOC-1 e |
| Session whose registry pin was recorded on a lookup this process did not accept (before a `podium-mcp` restart, a lost response, a §4.4.1 gate refusal, a lookup the bridge refused, a HEAD on one replica and a full GET on another) | Refused while below the session reference or, with none, the mark; the new-session remedy is the one the SPEC-2 (b) reset paragraph states | §6.5 reset paragraph (SPEC-2 b); DOC-1 e |
| Session pin lost on a restarted or other replica | The current `latest`, at or above the session's first answer; accepted | §6.5 (SPEC-2 b); DOC-1 e |
| Replay of a record that answers a pinned request | Not detected | §4.7.10 (SPEC-1 d); `docs/reference/http-api.md` (DOC-1 c) |
| First `latest` load under an empty cache directory | Accepted with no comparison | §6.5 "The first `latest` answer ... is accepted" (SPEC-2 b); DOC-1 e |
| `PODIUM_REGISTRY` changes over one cache directory | New key starts with no mark; a cache-served `latest` is not checked against it | §6.5 (SPEC-2 b); DOC-1 e |
| Identities in different organizations on one registry URL and one cache directory | One mark per artifact ID; a colliding ID whose other-organization artifact has a lower revision is refused until reset; a separate `PODIUM_CACHE_DIR` per organization avoids it; OQ-2 | §6.5 (SPEC-2 b); DOC-1 e |
| A second `podium-mcp` on one cache directory | Index open times out; marks held in memory for that process; one warning | §6.5 (SPEC-2 b), CODE-4; `docs/reference/cli.md` (DOC-1 d) |
| `podium cache reset-revisions` while a `podium-mcp` holds the index | Exit 1 with the lock message; nothing deleted | §6.5 (SPEC-2 b); DOC-1 d |
| Stored mark that does not parse | Deleted with a warning naming its key; treated as absent | §6.5 (SPEC-2 b), CODE-4; DOC-1 e |
| Served `artifact_revision` absent or non-canonical on a `latest` answer | Refused with `materialize.stale_resolution` | §6.5 (SPEC-2 b); DOC-1 a |
| Party with write access to `PODIUM_CACHE_DIR` | No protection; can roll back the cache or delete marks | §6.5 (SPEC-2 b); DOC-1 e |
| `PODIUM_VERIFY_SIGNATURES=never` | A path attacker can rewrite the revision with the delivery hash; only unmodified replays are refused | §6.5 (SPEC-2 b); DOC-1 e |
| `podium sync` and the SDKs | Receive `artifact_revision`, compare nothing | §6.5 (SPEC-2 b), §4.7.10; DOC-1 c |
| A §4.4.1 gate refuses after the check passes | Mark and session reference unchanged | §6.5 (SPEC-2 b); DOC-1 e |
| Two concurrent `latest` loads at revisions 100 and 200 | The 200 load is delivered. The 100 load is delivered when its check precedes the 200 load's index commit and refused otherwise. In every interleaving the mark ends at 200 and `id@latest` names the 200 version | §6.5 "to the greater of the mark and the served value" (SPEC-2 b); not user-facing beyond DOC-1 e |
| Replayed stale record through the §5.0 resources mirror | Refused; the mirror never advances the mark and records no session reference on a refusal | §6.5 (SPEC-2 b); DOC-1 e |
| Cached record written by a build before this change (no `artifact_revision` file) | Read as a cache miss and refetched; `offline-only` answers with the §7.4 cache-miss error | §6.5 cache-miss behavior; not user-facing, because no release wrote delivery files |
| Mixed versions: a registry and a `podium-mcp` from different sides of this change | Every load fails with `materialize.content_hash_mismatch` | §4.7.10 tag `/2` (SPEC-1 a); `CHANGELOG.md` operator action (CL-1) |
| Records stored with a zero ingest time | Every version serves `1970-01-01T00:00:00.000000Z`; replays are not detected | §4.7.10 "as `1970-01-01T00:00:00.000000Z` when the registry holds no ingest time" (SPEC-1 b); DOC-1 c |

## Testing

Every test below carries `// Spec:` directly above its `func Test...` line.

**TEST-1 · unit.** Lands in S12. It extends existing files and adds no parallel pins.

- `pkg/version/version_test.go` (`// Spec: §4.7.10`): a case at the sensitivity/artifact-revision boundary in the repartitioning test; changing only `ArtifactRevision` changes the digest; `FormatArtifactRevision` returns `1970-01-01T00:00:00.000000Z` for the zero time and for a pre-1970 time, writes a non-UTC input in UTC with a `Z`, and truncates a fixed time with sub-microsecond nanoseconds to six fractional digits.
- `pkg/registry/core/latest_skips_deprecated_test.go` (`// Spec: §4.7.10, §4.7.6`), with fixtures that set strictly increasing `IngestedAt`: the served revision equals `version.FormatArtifactRevision(rec.IngestedAt)` for the resolved record; after a new version born `deprecated: true` is stored, `latest` and the revision are unchanged; in the all-deprecated fallback the revision is the newest-ingested version's; after a backport at a lower semver is stored, the revision rises; an exact-version load of 1.0.0 serves 1.0.0's own ingest time; after `PurgeDeprecatedManifests` removes a deprecated version while a non-deprecated one exists, the `latest` revision is unchanged.
- `pkg/registry/core/session_test.go` (`// Spec: §4.7.10, §4.7.6`): a session-pinned `latest` serves the pinned version's ingest time after a newer ingest.
- `pkg/registry/core` scope test (`// Spec: §4.7.10, §6.3.1`): a token scoped to `load:<id>@1.0.0` receives 1.0.0's ingest time on its load, unaffected by a later 2.0.0.
- `pkg/registry/server/delivery_test.go` (`// Spec: §4.7.10, §7.2`): the single response and the batch `ok` entry for one artifact serve the same `artifact_revision` and `delivery_hash`, and the revision is covered by the hash (recomputing with a different revision yields a different digest). A batch error entry carries no `artifact_revision`.
- `internal/testharness/delivery_test.go` (`// Spec: §4.7.10`): `SealDelivery` writes `1970-01-01T00:00:00.000000Z` into a stub with no `artifact_revision` and frames it, and frames an explicit revision unchanged.
- `pkg/registry/server/load_artifact_etag_test.go` (`// Spec: §7.2`): two versions with different ingest times but the same validator inputs publish ETags computed from `content_hash` and `extends_pin` alone; the response carries `artifact_revision` and the ETag string does not contain it.
- `internal/revmark/revmark_test.go` (new, `// Spec: §6.5`): `ParseRevision` accepts `"1970-01-01T00:00:00.000000Z"` (0) and `"2023-11-14T22:13:20.000000Z"` (1700000000000000) and rejects `""`, `"1700000000000000"`, `"2023-11-14T22:13:20Z"`, `"2023-11-14T22:13:20.0000001Z"`, `"2023-11-14T22:13:20.000000+00:00"`, and `"1969-12-31T23:59:59.999999Z"`; `ParseRevision(FormatArtifactRevision(time.UnixMicro(n)))` returns `n` for a sample of `n`; `NormalizeRegistry` folds scheme and host case, `:443` on `https`, `:80` on `http`, and a trailing slash, and keeps a non-default port and the path case; `Key.Encode` isolates registry and ID; `Reset` on a missing directory returns `(0, false, nil)` and creates no file; `Reset` on an index whose bucket is empty returns `(0, false, nil)`; `Reset` on an `index.db` created with `bolt.Open` and `CreateBucketIfNotExists` of the resolution bucket alone, as a released `podium-mcp` leaves it, returns `(0, false, nil)` without panicking, and the file afterwards holds no `revisions` bucket; `Reset` with IDs deletes only matching keys across two registries and returns `found` true; `Reset` with IDs that match no key in a non-empty bucket returns `(0, true, nil)`; `Reset` with no IDs deletes all; `Reset` returns `ErrIndexLocked` while the test holds a `bolt.Open` handle on the file.

**TEST-2 · unit, `cmd/podium-mcp/delivery_freshness_test.go` (new), `cmd/podium-mcp/resolution_cache_test.go`, and `cmd/podium-mcp/verify_order_test.go`.** Lands in S10 with CODE-7. Uses the existing delivery helpers (`sealDelivery` and `deliveryHashOf` in `cmd/podium-mcp/delivery_helpers_test.go`, and `loadArtifactJSON` in `cmd/podium-mcp/load_artifact_test.go`) with explicit `artifact_revision` values. A revision written below as a number `n` is the served string `version.FormatArtifactRevision(time.UnixMicro(n))`, and a `reference_revision` written as a number is formatted the same way.

- (a) A fresh `latest` load at revision 100 after a mark of 200 returns `isError` with code `materialize.stale_resolution`, `retryable` false, the `suggested_action` from `bridgeCodeMeta`, and details `artifact_id`, `served_version`, `served_revision` 100, and `reference_revision` 200; the content cache, the `id@latest` entry, the mark, the destination, and the local audit sink are unchanged. The refused load carries a fresh host `session_id` S that holds no session reference, and a second answer at 100 under S is then refused again with `reference_revision` 200, which pins that a refusal records no session reference. This test carries `// Matrix: §6.10 (materialize.stale_resolution)` and `// Spec: §6.5, §6.9`.
- (b) Equal revision is delivered; a higher revision is delivered and advances the mark.
- (c) Pinned loads (exact, `1.x`, `sha256:`) with a revision below the mark are delivered and leave the mark unchanged.
- (d) Session reference: with a host-supplied `session_id`, a first `latest` at 300 sets the mark; a second session's first answer at 200 is refused; within the first session, an answer at 300 is delivered; with the mark advanced to 400 by another session, the first session's answer at 300 is delivered and leaves the mark at 400 and `id@latest` naming the 400 version; a third session's first answer at 300 is then refused with `reference_revision` 400; within the first session an answer at 250 is refused with `reference_revision` 300. An implementation that keyed session references by registry and artifact without the session would deliver the third session's 300 answer against the first session's reference of 300.
- (e) Cache-served paths with a mark seeded above the cached record's revision: the HEAD match, the `304`, `offline-first`, `offline-only`, and the §7.4 degraded fallback each deliver and leave the mark unchanged.
- (f) The `cachedOrRefetch` refetch after a cached signature failure is compared: a refetched body below the mark is refused.
- (g) An ID the §6.4 overlay serves is not compared.
- (h) With the index locked by another handle, the bridge refuses a stale answer from its in-memory mark within the process and logs one warning naming the cache directory. The accepted load and the stale load carry different host-supplied `session_id` values, so the refusal comes from the `mem` mark and not from a session reference.
- (i) A `latest` answer with `artifact_revision` `""`, `"100"` (a decimal integer), or `"2025-01-01T00:00:00Z"` (no fractional digits) is refused with `materialize.stale_resolution`; a pinned answer with the same value is delivered.
- (j) An answer that passes the check and is refused by a §4.4.1 runtime gate leaves the mark and the session reference unchanged.
- (k) With a mark of 200 and no session reference for the bridge's own session, `resources/read` with a replayed stale body at 100 is refused and the mark is unchanged. A `load_artifact` with no `session_id` that then receives the same 100 record is also refused with `reference_revision` 200, which pins that a refused mirror answer records no session reference for the bridge's session. With a higher body it returns text, the mark is unchanged, and the session reference for the bridge's session is the served revision.
- (l) Concurrency, run under `-race` and stating so in its comment: two `latest` loads at revisions 100 and 200, under different host `session_id` values, run concurrently. The test asserts only what holds in every interleaving: the 200 load delivers, the 100 load either delivers or is refused with `materialize.stale_resolution`, the mark ends at 200, and `id@latest` names the 200 version. Both orders run sequentially elsewhere: (a) covers 200 then 100, and (b) covers 100 then 200.
- (m) `resolution_cache_test.go`: the mark persists across `Close` and reopen; a stored mark of `"x"` is deleted, the log line names the key and not the value, and `Reference` reports none; `PutLatestAdvancing` below the stored mark writes neither the mark nor `id@latest`; on a nil `*resolutionCache`, `Reference` returns `(0, false)` and `NoteSession` and `PutLatestAdvancing` return without panicking. With a session reference of 300 for `(k, S)` and the mark at 300, `NoteSession(k, S, 400)` and then `PutLatestAdvancing(k, S, ..., 500, ...)` leave `Reference(k, S)` at 300 while the stored mark advances to 500.
- (n) A cached record directory without the `artifact_revision` file reads as a cache miss, and a record cached through `cacheVerifiedRecord` and read back through `loadArtifactFromCache` carries the revision it was served with.
- (o) Session reference on the revalidated and mirror paths, with the persisted mark and the cached `id@latest` record at 300. On the revalidated path, under a host-supplied `session_id` S: S's first `latest` load is answered by a HEAD match and delivered from the cache; another session's fresh answer at 400 advances the mark; S's next `latest` load receives a full body at 300 (the registry's pin) and is delivered, leaving the mark at 400. The same sequence runs with a `304` as S's first answer. On the mirror path, which sends only the bridge's own session because `resources/read` carries no `session_id`: the first answer is `resources/read X` at 300; a `load_artifact` with a host-supplied `session_id` advances the mark to 400; a `load_artifact` for X with no `session_id` then receives a full body at 300 and is delivered, leaving the mark at 400.
- (p) Session reference after a restart: a new `resolutionCache` and `mcpServer` over the same cache directory, with the mark at 400 and no session reference for host session S, receives S's pinned answer at 300 and refuses it with `materialize.stale_resolution` and `reference_revision` 400; the same artifact under a new session S2 at 400 is delivered.
- (q) A fresh `latest` answer at 100 below a mark of 200 whose response carries `X-Podium-Read-Only: true` and `X-Podium-Read-Only-Lag-Seconds: 30` is refused with the same envelope as (a).
- (r) Read-only lag under the bridge's own session, with the stub answering as the registry does once it has pinned the session to the replica's answer. A first `latest` at 200 is delivered. The stub then serves 100 with `X-Podium-Read-Only: true`, and the load is refused with `reference_revision` 200. The stub then serves the same 100 record without the read-only headers, standing for the pinned answer after read-only mode ends, and the load in the same `mcpServer` is refused again. A new `mcpServer` over the same cache directory, whose `sessionID` differs, receives 200 and delivers it, and the mark stays at 200. The comment above the test cites `pkg/registry/core/core.go:1715` as the reason the stub keeps serving 100.
- (s) The session's first accepted answer is the reference, rather than the session maximum or the last accepted answer. `// Spec: §6.5`. Under a host-supplied `session_id` S, S's first `latest` answer at 300 is delivered. S then receives a fresh answer at 400, standing for a replica that lost the pin, which is delivered and advances the mark to 400. S then receives the pinned answer at 300 again, which is delivered; the mark stays at 400 and `id@latest` names the 400 version. S then receives 250, which is refused with `materialize.stale_resolution` and `reference_revision` 300. An implementation that raised the session reference on each accepted answer would refuse the second 300 answer with `reference_revision` 400.
- (t) `verify_order_test.go`, `// Spec: §6.6 step 2, §6.5`, beside the existing ordering tests (`TestDeliverLoadArtifact_ManifestGatesRunAfterVerification` at `verify_order_test.go:72` and its neighbours): the revision check runs after the delivery-hash comparison and the §4.7.9 policy. With a mark of 200, a fresh `latest` answer at revision 100 whose `delivery_hash` does not match the served record is refused with `materialize.content_hash_mismatch`; the same record with its `artifact_revision` removed after sealing is refused with `materialize.content_hash_mismatch`; and, under the `always` verification policy, the same answer at 100 with a matching hash and an invalid `delivery_signature` is refused with `materialize.signature_invalid`. No such answer reports `materialize.stale_resolution`, and the mark is unchanged after each.
- (u) Per-artifact isolation of session references and marks. `// Spec: §6.5`. Under a host-supplied `session_id` S, S's first `latest` answer for artifact A at 300 is delivered. A load under another host `session_id` S2 delivers artifact B at 500, which sets B's mark. S's first `latest` answer for B, a replayed record at 300, is then refused with `materialize.stale_resolution` and `reference_revision` 500. Over a fresh cache directory, after S's first answer for A at 300 is delivered, S's first `latest` answer for B at 100, with no mark for B, is delivered. The same pair runs a second time with the index locked by another handle, as in (h), so the marks come from `mem`. An implementation that keyed session references without the artifact ID would deliver the replayed B record at 300 against S's reference for A, and one that keyed session references or `mem` entries without the artifact ID would refuse the honest B answer at 100 against A's 300.

**TEST-3 · integration, `test/integration/delivery_freshness_test.go` (new).** Lands in S13. `// Spec: §4.7.10, §4.7.6, §6.5`.

A real `core.Registry` over `store.NewMemory` behind `server.New` with a test signing key, fronted by an `httptest` handler that can capture a `load_artifact` body and replay it. `podium-mcp` is built with `buildMCP` (`test/integration/mcp_test.go:391`) and each load is a separate spawned process (`loadArtifactOver`, `test/integration/mcp_cache_offline_test.go:16`) over one `PODIUM_CACHE_DIR`. Records are written through ingest, or with explicit strictly increasing `IngestedAt`.

1. Store 1.0.0, load `latest`, and capture the body. Store 2.0.0 and load `latest`: 2.0.0 is delivered.
2. Store 3.0.0 born `deprecated: true` and load `latest`: 2.0.0 is delivered at an unchanged revision.
3. Store backport 1.0.1 and load `latest`: 1.0.1 is delivered.
4. Switch the handler to replay the body captured in step 1: the result reports `materialize.stale_resolution`, and the index's `id@latest` still names 1.0.1.
5. Load `version: 1.0.0` through the replaying handler: delivered.

**TEST-4 · e2e, `test/e2e/delivery_freshness_test.go` (new).** Lands in S14. `// Spec: §6.5`, and `// Matrix: §6.10 (materialize.stale_resolution)` on the test that asserts the envelope.

Drives the binaries against a stub registry built on `newSignedArtifactFixture` (`test/e2e/signed_artifact_helpers_test.go:95`), whose CODE-5 extension serves a configurable `(version, artifact_revision)` record framed into `version.DeliveryHash` and signed with `sign.RegistryManagedKey`. One fixture serves every step, and `setRecord` switches its record between steps, so every load reaches the same URL and therefore the same mark key. A revision written below as a number `n` is `version.FormatArtifactRevision(time.UnixMicro(n))`. No registry is booted; TEST-1 and TEST-3 cover how the registry computes the value. Each `mcpExec` (`test/e2e/helpers_test.go:581`) is a new process over a fixed `PODIUM_CACHE_DIR`, under `PODIUM_VERIFY_SIGNATURES=always` with the fixture's verify key.

1. Fixture at 2.0.0 revision 200; a `latest` load succeeds.
2. `setRecord("1.0.0", 100)`; a `latest` load in a new process returns `isError`, code `materialize.stale_resolution`, `retryable` false, and the detail fields `artifact_id`, `served_version`, `served_revision` 100, and `reference_revision` 200, and the destination and `id@latest` are unchanged. This covers persistence across processes.
3. A load with `version: "1.0.0"` succeeds.
4. `podium cache reset-revisions --dir <dir> <id>` exits 0 and prints `cache: reset 1 revision mark(s)`; the stale `latest` load then succeeds.
5. The test process holds `<dir>/.resolutions/index.db` open with `bolt.Open`; `podium cache reset-revisions --dir <dir>` exits 1 and prints the lock message; the handle is then closed.
6. Against an empty directory the command exits 0, prints `cache: no revision marks under <dir>`, and creates no `index.db`.

No platform skip is needed; nothing in the test depends on certificates or the keychain.

## Manual validation

**MV-1.** Add scenario S82 to `test/manual-validation.md` after S81, following the existing conventions, and add this index row after the S81 row. Lands in S17.

```
| S82 | A replayed stale latest is refused, and an honest regression is not | standalone | none | none | none |
```

~~~~markdown
## S82: A replayed stale latest is refused, and an honest regression is not

**Goal.** Validate that `podium-mcp` refuses a replayed `latest` record signed by a live registry, accepts a pinned load of the same record, accepts a `latest` that stays put when a deprecated version is ingested, and loads again after `podium cache reset-revisions`.

**Covers.** §4.7.10 ingest time, the §6.5 revision mark, the §6.9 `materialize.stale_resolution` row, and the §6.10 envelope.

**Why by hand.** A person reads the bridge's error envelope and the reset command's output against a registry's own signed bytes, including the deprecation ingest that TEST-4 does not drive through the binaries.

**Steps.**

1. Run the isolation block.

2. Generate the key file, scaffold skill `fresh` at 1.0.0, serve it, capture its `latest` body, and load it once.

   ```bash
   podium admin signing-key generate --key-file "$PODIUM_SIGN_KEY_PATH" > /dev/null
   KEY="$(awk '/^public:/{print $2}' "$PODIUM_SIGN_KEY_PATH")"
   podium artifact scaffold --type skill --description "Fresh skill" "$WORK/reg/fresh" > /dev/null
   setver() { sed -i.bak "s/^version: .*/version: $1/" "$WORK/reg/fresh/ARTIFACT.md"; }
   setver 1.0.0
   serve() { podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
       --bind 127.0.0.1:8182 > "$WORK/srv$1.log" 2>&1 & SRV=$!
     curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8182/healthz
     server_alive "$SRV" "$WORK/srv$1.log"; }
   stop() { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
   serve 1
   REG=http://127.0.0.1:8182
   curl -s "$REG/v1/load_artifact?id=fresh" -o "$WORK/v1.body"
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   load_mcp() { ARGS="{\"id\":\"fresh\"${2:+,\"version\":\"$2\"}}"
     printf '%s\n%s\n' "$INIT" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"load_artifact\",\"arguments\":$ARGS}}" \
     | PODIUM_REGISTRY="$1" PODIUM_SIGNATURE_VERIFY_KEY="$KEY" PODIUM_CACHE_DIR="$WORK/cache" podium-mcp 2>/dev/null | tail -1 \
     | python3 -c 'import sys,json; r=json.load(sys.stdin)["result"]["structuredContent"]; print(json.dumps({k:r[k] for k in ("code","details") if k in r}) if r.get("code") else "loaded")'; }
   load_mcp "$REG"
   ```

   **Expect.** `loaded`, and `$WORK/v1.body` holds a JSON object whose `version` is `1.0.0` and which carries an `artifact_revision` written as an RFC 3339 UTC timestamp with six fractional digits.

3. Bump to 2.0.0, restart, and load `latest`.

   ```bash
   stop; setver 2.0.0; serve 2
   load_mcp "$REG"
   ```

   **Expect.** `loaded`.

4. Stop the registry, start a stub on the registry's own address that answers every `load_artifact` request with the captured 1.0.0 body, and load `latest` through it. The stub binds `127.0.0.1:8182`, so `PODIUM_REGISTRY` is unchanged and the load meets the mark steps 2 and 3 recorded.

   ```bash
   cat > "$WORK/stub.py" <<'EOF'
   import http.server, sys
   body = open(sys.argv[1], "rb").read()
   class H(http.server.BaseHTTPRequestHandler):
       def _head(self):
           self.send_response(200)
           self.send_header("Content-Type", "application/json")
           self.send_header("ETag", '"stub-no-match"')
           self.send_header("Content-Length", str(len(body)))
           self.end_headers()
       def do_HEAD(self): self._head()
       def do_GET(self): self._head(); self.wfile.write(body)
       def log_message(self, *a): pass
   http.server.HTTPServer(("127.0.0.1", 8182), H).serve_forever()
   EOF
   replay() { python3 "$WORK/stub.py" "$WORK/v1.body" & STUB=$!; sleep 1; }
   unreplay() { kill "$STUB" 2>/dev/null; wait "$STUB" 2>/dev/null; }
   stop; replay
   load_mcp "$REG"
   ```

   **Expect.** A JSON object with `"code": "materialize.stale_resolution"` and `details` naming `artifact_id` `fresh`, `served_version` `1.0.0`, and a `served_revision` earlier than `reference_revision`. A `loaded` here means the check is missing or ran on a cache path.

5. Load the pinned version through the stub.

   ```bash
   load_mcp "$REG" 1.0.0
   ```

   **Expect.** `loaded`, because a pinned request is never compared with the mark.

6. Stop the stub, ingest a new version 3.0.0 born deprecated, and load `latest` from the registry.

   ```bash
   unreplay; setver 3.0.0
   awk '{print} /^version: 3\.0\.0$/{print "deprecated: true"}' "$WORK/reg/fresh/ARTIFACT.md" > "$WORK/a.md" \
     && mv "$WORK/a.md" "$WORK/reg/fresh/ARTIFACT.md"
   serve 3
   load_mcp "$REG"
   ```

   **Expect.** `loaded`, with `latest` still resolving to 2.0.0. A `materialize.stale_resolution` here means a deprecated ingest lowered the served ingest time.

7. Replay once more to show the mark still refuses it, then reset the mark and replay again.

   ```bash
   stop; replay
   load_mcp "$REG"
   podium cache reset-revisions --dir "$WORK/cache" fresh; echo "exit=$?"
   load_mcp "$REG"
   unreplay
   ```

   **Expect.** First a JSON object with `"code": "materialize.stale_resolution"`, then `cache: reset 1 revision mark(s)`, then `exit=0`, then `loaded`. Every load in this scenario names `$REG`, so the cache holds one mark, and the count is 1. The refusal before the reset and the `loaded` after it, under the same URL and the same replayed body, show that the reset removed the mark.

Each `load_mcp` is a separate `podium-mcp` process, so step 4 already shows the mark persisting across processes. The lock case (exit 1 while `podium-mcp` holds the index) is covered by TEST-4 only, because a one-shot `podium-mcp` does not hold the lock long enough to observe by hand.
~~~~

## Documentation changes

**DOC-1.** Lands in S15. All prose follows `doc-style.md`.

(a) `docs/reference/error-codes.md`, the `materialize.*` table (lines 118-128). Insert after the `materialize.content_hash_mismatch` row:

> | `materialize.stale_resolution` | `podium-mcp` received a validly signed record for a `latest` load whose artifact revision is below one it already accepted for this registry and artifact, or whose artifact revision is absent or malformed. The load stops before anything is cached or written (§4.7.10, §6.5). The `reference_revision` detail is the value the answer was compared with: the first revision accepted in the same session, or the revision mark. A request that names a version, a range, or a content hash is never compared with the revision mark. The error is not retryable, and a retry in the same session repeats it. When the refusal came from a lagging replica while the registry was in read-only mode, or from a session pinned to an older version, a new session loads the current `latest` once the registry is out of read-only mode: pass a new `session_id`, or restart `podium-mcp` when the load passes none. After a registry is restored from an earlier backup, after a lagging replica is promoted, or after the newest version is purged, erased, or leaves the caller's view, stop every MCP server that uses the cache directory and run `podium cache reset-revisions <id>`. |

(b) `docs/reference/http-api.md`, the integrity fields (lines 232-238). Add `"artifact_revision": "2025-01-01T00:00:00.000000Z"` to the `load_artifact` JSON example beside `delivery_signature`. Add "ingest time" after "sensitivity" in the `delivery_hash` bullet's list of framed fields, and append to that bullet: "The record is framed under the tag `podium/delivery-record/2`, so `podium-mcp` and the registry must run matching releases, or every load fails with `materialize.content_hash_mismatch`." Insert a bullet after the `delivery_signature` bullet:

> - `artifact_revision` is the time the registry stored the served version, an RFC 3339 UTC timestamp with six fractional digits, such as `2025-01-01T00:00:00.000000Z`. `delivery_hash` covers it. It is present on every response and on every `ok` batch item, and it does not enter the entity tag.

At line 287, change "Each `ok` item carries `delivery_hash` and `delivery_signature`" to "Each `ok` item carries `delivery_hash`, `delivery_signature`, and `artifact_revision`".

(c) `docs/reference/http-api.md:240`. Replace the first two sentences of the paragraph ("The delivery record carries no timestamp and no nonce ... `(id, version, content_hash)`.") with:

> The delivery record carries no nonce, so a record captured from an earlier response verifies when it is replayed. `podium-mcp` refuses a replayed record that answers a `latest` load with an artifact revision below one it already accepted for that registry and artifact, and fails the load with `materialize.stale_resolution`. A replayed record that answers a pinned request is not detected, and neither is a record received by a consumer that does not verify, such as server-source `podium sync` or a language SDK. Rollback is detected for `podium sync` by the `sync.lock` pin of `(id, version, content_hash)`. A registry that stores no ingest time serves `1970-01-01T00:00:00.000000Z` for every version, and replays against it are not detected.

The hidden-parent sentences that end the paragraph are unchanged.

(d) `docs/reference/cli.md`, "Cache and quota" (lines 764-779). Insert after the `podium cache prune` subsection, before `### \`podium quota\``:

````markdown
### `podium cache reset-revisions`

Deletes the revision marks `podium-mcp` keeps for `latest` loads.

```
podium cache reset-revisions [--dir <path>] [<artifact-id>...]
```

| Flag or argument | Effect |
|:--|:--|
| `--dir <path>` | Cache directory. Defaults to `PODIUM_CACHE_DIR`, then `~/.podium/cache`. |
| `<artifact-id>...` | Delete only the marks for these artifact IDs, under every registry. With no IDs, delete every mark. |

When the cache directory holds no marks, the command prints `cache: no revision marks under <path>` and exits 0. Otherwise it prints `cache: reset <n> revision mark(s)` and exits 0, with `<n>` 0 when no mark matches the given IDs. While a running `podium-mcp` holds the cache's index, the command exits 1; stop every MCP server that uses the cache directory and run it again. A `podium-mcp` that could not open the index holds its marks in memory, and they are cleared when that process exits. `podium cache prune` does not touch revision marks.
````

The synopsis fence is untagged, as the `prune` synopsis is, so `tools/doccov/manifest.yaml` needs no entry.

(e) `docs/consuming/configure-your-harness.md`. Insert these paragraphs after the cache-verification paragraph near line 41 (the paragraph that opens "The MCP server delivers a record from its cache only after"):

> The MCP server records, per registry and artifact, the artifact revision of the newest `latest` load it accepted, in its index under `PODIUM_CACHE_DIR`. It refuses a later `latest` load whose revision is lower with `materialize.stale_resolution`, which stops a proxy or cache between the MCP server and the registry from serving an older signed version. Within one session, it compares a `latest` load with the first one it accepted in that session, including one served from the cache after the registry confirmed it, so a session-pinned version still loads. An answer read through `resources/read` records the first accepted answer for the MCP server's own session only. It compares only `latest` loads answered by the registry: a load that names a version, a range, or a content hash, and a load served from the cache, is never compared. A backport ingested after a newer version, and a new deprecated version, leave every honest `latest` load at or above the mark. The first `latest` load under an empty cache directory is accepted, and a registry change starts with no marks. The marks are not kept per organization, so identities in different organizations that use one registry URL should use separate cache directories. When another MCP server holds the index, the marks last only for the process, and the MCP server logs a warning. A stored mark that cannot be read is deleted with a warning. The mark gives no protection against a party that can write `PODIUM_CACHE_DIR`, and under `PODIUM_VERIFY_SIGNATURES=never` it refuses only unmodified replays. A load that a later gate refuses leaves the mark unchanged. Run `podium cache reset-revisions` after the registry is restored from a backup, after a lagging replica is promoted, or after the newest version is purged, erased, or leaves the caller's view.
>
> Two refusals need no reset, and a new session clears both. While the registry is in read-only mode, a replica that lags the primary can serve an older `latest`. The MCP server refuses it, and the registry pins that session to the older version, so the session stays refused for that artifact after the registry leaves read-only mode. A session that did not load the artifact during the lag loads the current `latest` once read-only mode ends. The MCP server keeps session state in memory, so after it restarts, a host that reuses a `session_id` whose pinned version is older than a version another session has since loaded is refused for that artifact. Starting a new session loads the current `latest`: the host passes a new `session_id`, and a load that passes none gets a new session when `podium-mcp` restarts.

(f) `docs/deployment/operator-guide.md`, the restore runbook (lines 96-110). Append a step to the numbered block:

```
7. Restoring to an earlier point lowers the artifact revision of every
   artifact whose newest version postdates the restore point. Each
   podium-mcp consumer then fails latest loads of those artifacts with
   materialize.stale_resolution until it stops its MCP servers and runs
   `podium cache reset-revisions`.
```

(g) `deploy/runbook.md`, "Read-only mode (Postgres primary outage)" (lines 6-27). Append to the **Impact.** paragraph:

> A read replica that lags the primary can serve an older `latest` than a podium-mcp consumer already loaded, and that consumer fails the load with `materialize.stale_resolution`. The registry pins the consumer's session to the older version, and the pin outlives read-only mode. After the registry leaves read-only mode, a consumer whose session loaded the artifact during the lag restarts `podium-mcp` or starts a new host session; no reset is needed.

and append to mitigation step 3:

> A promoted replica that lost recent commits serves an older `latest` for the artifacts those commits ingested. Each podium-mcp consumer fails those loads with `materialize.stale_resolution` until it stops its MCP servers and runs `podium cache reset-revisions`.

(h) `docs/assets/diagrams/load-artifact-sequence.svg`, the first footer line (line 154 at the time of writing; embedded at `spec/02-architecture.md:54`). Replace the text `The MCP server holds no per-session state; identity flows through the registry on every call.` with `Identity flows through the registry on every call.` The element, its class, and its position are unchanged, and the shorter string fits the canvas. The ASCII fallback below the embedding at `spec/02-architecture.md:56` carries no state statement and is unchanged. Render the diagram in both themes per `doc-diagram-style.md` after the edit.

**CL-1.** `CHANGELOG.md`, `[Unreleased]`. Lands in S16.

(a) Under `### Added`, add:

> - **`podium-mcp` refuses a stale `latest` load** (§4.7.10, §6.5): the registry serves `artifact_revision`, the time it stored the served version, and `podium-mcp` refuses a `load_artifact` that resolves `latest` when that revision is below one it already accepted for that registry and artifact ID. The refusal uses `materialize.stale_resolution` and writes nothing. Pinned versions are never compared. `podium cache reset-revisions` deletes the marks, and every MCP server that uses the cache directory must be stopped first.

(b) Edit the existing `[Unreleased]` delivery-attestation entry (lines 540-563). In the sentence that lists what each response and batch item carries, add `artifact_revision` after `delivery_signature`, and state that `delivery_hash` covers it and the entity tag does not include it. Replace "The delivery record carries no timestamp and no nonce, so a captured record verifies when it is replayed." with "A captured record still verifies when it is replayed, and `podium-mcp` refuses a replayed record that answers a `latest` resolution when its artifact revision is below the mark." The existing operator action to roll the registry and the consumers together already covers the framing change, so no separate bullet records it.

## Open questions

**OQ-1. Protocol change.** 0027 expected a check that needs no protocol change. This proposal adds the ingest time to the signed record and moves the tag to `podium/delivery-record/2`, so a registry and a `podium-mcp` on different sides of the change fail every load. No release served `/1`, so the break affects only unreleased builds. The only alternative without a protocol change orders by served semver, refuses every honest backport, and leaves the reset command as the routine remedy. The draft stages the protocol change; confirm it, or choose the semver variant with the refusal downgraded to a logged warning.

**OQ-2. Identity in the mark key.** Marks are keyed by registry and artifact ID and not by the signed-in identity or its organization. A user who signs in as an identity whose view lacks the newest version, or whose view loses a layer, or as an identity in another organization whose artifact shares an ID, gets a lower revision and a refusal until they reset. Keying by the token subject and organization removes those cases but requires `podium-mcp` to extract a stable subject and organization for every identity provider, including `trusted-headers`, where the bridge does not see the organization. Decide whether to add them to the key.

## Non-goals

- Detecting replay of a record that answers a pinned request (exact semver, range, or `sha256:`). An explicit older version stays loadable.
- Protecting against a party that can write `PODIUM_CACHE_DIR`, including a snapshot restore of the whole directory. The mark shares that trust domain. A keychain-held mark or an HMAC keyed from the keychain is not proposed.
- Revision checks in server-source `podium sync` or the language SDKs. They receive `artifact_revision` and verify nothing (§4.7.10). Sync rollback detection stays with the §7.5.3 lock. The SDK response types are not changed to expose the field.
- Consumer-clock freshness (a timestamp with skew tolerance) or a per-request nonce, both rejected by 0027 OQ-2.
- Changing the §12 entity tag, adding `Cache-Control` headers to `load_artifact` responses, or changing how an intermediary HTTP cache is expected to behave.
- Rekeying the existing §6.5 resolution-index entries by registry and tenant, or changing the resolution store's fail-open behavior beyond the in-memory fallback for marks.
- A `podium cache clear` command, or changing `podium cache prune` to touch `.resolutions`.
- Making `pkg/registry/core/domain_load.go` `latestRecord` (semver-ordered, :330-346) follow ingest order. It does not govern `load_artifact` resolution.
- Persisting §4.7.6 session pins across registry replicas or restarts, or persisting the bridge's session references across `podium-mcp` restarts. The registry's pins carry no expiry, so persisted references would need an unbounded store. A host session reused across a `podium-mcp` restart can be refused, and §6.5 names a new session as the remedy.
- A registry-signed maximum ingest time over the caller's candidate versions. It depends on the reader's view, discloses versions a §6.3.1 load scope withholds, and §8.4 retention lowers it on an honest registry.
- Clearing §4.7.6 session pins when the registry leaves §13.2.1 read-only mode. It would change `latest` mid-session for every session, including those the lag never touched, and a new session already clears the refusal.
- A registry filter on `podium cache reset-revisions`. It would duplicate the bridge's URL normalization and cover no case the artifact-ID filter misses.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and the deliverable that now owns its fix.

### Pass 1 (2026-10-03, automated)

- **Unstaged test fixtures that frame no revision.** The CODE-3 and CODE-5 IMPLEMENTOR'S CHOICE markers own the rule.
- **`PODIUM_TENANT_ID` in the mark key.** The "No tenant in the mark key" decision, SPEC-2 (b), and CODE-5 own the key, and OQ-2 owns adding the identity.
- **Unix microseconds against §7.2.1.** The Encoding decision, SPEC-1 (b), CODE-1 `FormatArtifactRevision`, and CODE-4 `ParseRevision` own the RFC 3339 encoding.
- **No session reference on HEAD-match, `304`, and mirror deliveries.** CODE-4 `NoteSession`, CODE-5, and TEST-2 (k) and (o).
- **Session references lost across a `podium-mcp` restart.** The SPEC-2 (b) reset paragraph and TEST-2 (p).
- **Nondeterministic concurrency outcome.** The concurrent-load edge row and TEST-2 (l).
- **Unstaged cache read and write sites.** CODE-3 (the cache-carry bullet) and TEST-2 (n).
- **MV-1 replay on another URL.** MV-1 steps 4 to 7, TEST-4 `setRecord` (CODE-5), and the one-URL Watch-out.
- **TEST-2 (h) could not detect a missing `mem` fallback.** TEST-2 (h).
- **Read-only lag and lagging-replica promotion.** SPEC-2 (b), the `Not retryable` decision, DOC-1 (g), and TEST-2 (q).
- **`Reset` could not report absence.** CODE-4 `Reset` and CODE-6.
- **`mark_revision` carried the session reference.** SPEC-3 and CODE-5 `checkFreshness` (`reference_revision`).
- **Mirror session in TEST-2 (o).** CODE-5 `handleResourcesRead` and TEST-2 (o).
- **Mirror test fixtures left red.** The CODE-5 IMPLEMENTOR'S CHOICE marker.
- **Nil resolution cache.** CODE-4 and TEST-2 (m).
- **TEST-1 revision assertion.** TEST-1.

### Pass 2 (2026-10-03, automated)

- **`TestPodiumMCP_MultiResourceContentHashRoundTrip` served no revision.** The CODE-3 IMPLEMENTOR'S CHOICE marker (S7).
- **Read-only lag does not clear by waiting.** The SPEC-2 (b) reset paragraph, the CODE-5 `suggested_action`, TEST-2 (r), and the Non-goals.

### Pass 3 (2026-10-03, automated)

- **`TestDeliveryHash_DiffersFromContentHashForTheSameBytes` became vacuous under `/2`.** CODE-1.
- **S8 depended on CODE-1 without declaring it.** The checklist and the Ordering constraints.
- **The `DeliveryHash` doc comment kept the `/1` tag-separation rationale.** CODE-1.

### Pass 4 (2026-10-03, automated)

- **`TestVerifyDeliveryHash_CacheServedSkillWithoutSkillRawFails` became vacuous.** The CODE-3 IMPLEMENTOR'S CHOICE marker, and the CODE-3 cache-carry bullet for the required `artifact_revision` file.
- **S7 left every spawned-bridge load red until S9.** CODE-3 lands the bridge's framing and cache carry in S7.
- **The `readDeliveryFiles` required-file anchor pointed one line low.** CODE-3.

### Pass 5 (2026-10-03, prune)

- **Over-specified fixture inventory and restated remedies.** The per-file fixture enumeration appeared in the Watch-out, CODE-3, and CODE-5, and its copies had drifted from each other and from this log. It is replaced by an IMPLEMENTOR'S CHOICE marker in CODE-3, with a CODE-5 marker for the S9 default, constrained by the fixture-site pattern, the no-weakening rule, and the step criteria. The `SealDelivery` case moved to TEST-1, and the `setRecord` extension that TEST-4 uses stays specified in CODE-5. The `Not retryable` decision keeps only its retryability rationale, and the read-only and pinned-session edge rows cite the SPEC-2 (b) reset paragraph as the single statement of the remedy. SPEC-2 (b) no longer fixes the stored-mark encoding or the warning text; CODE-4 owns them. The `:31` Fixed decision now admits the concurrent-load interleaving. The review log is collapsed to one line per finding naming the owning deliverable, which corrects the Pass 1 and Pass 2 entries that named CODE-5 for fixes that live in CODE-3.

### Pass 6 (2026-10-03, automated)

- **§2.2 and §6.1 still stated that the MCP server holds no per-session state and shares no state across processes.** SPEC-5 restates both sites in the SPEC-2 commit (S2): the in-memory session reference is the only per-session state, the index DB and its revision marks join the local state, and processes over one `PODIUM_CACHE_DIR` share the persisted marks one process at a time. DOC-1 (h) removes the matching clause from the `load-artifact-sequence.svg` footer. The Summary and the S2 and S15 checklist steps name both.
- **No test pinned that a refused `latest` answer records no session reference.** TEST-2 (k) follows a refused mirror answer with a `load_artifact` under the bridge's own session that receives the same record and is refused with `reference_revision` 200, and TEST-2 (a) follows its refusal with a second refused answer under the same fresh host `session_id`.

### Pass 7 (2026-10-03, automated)

- **No test distinguished the session's first accepted answer from the session maximum as the reference.** TEST-2 (s) has session S accept 300 and then 400, deliver a return to the pinned 300 with the mark left at 400, and refuse 250 with `reference_revision` 300. TEST-2 (m) asserts that `NoteSession` and `PutLatestAdvancing` called with a higher revision leave an existing session reference unchanged.

### Pass 8 (2026-10-03, automated)

- **No test pinned that the revision check runs after the delivery-hash and §4.7.9 verification.** TEST-2 (t) in `cmd/podium-mcp/verify_order_test.go`, with a mark of 200 and a served revision of 100, asserts `materialize.content_hash_mismatch` for a mismatched hash and for a revision removed after sealing, and `materialize.signature_invalid` for an invalid signature under the `always` policy. The TEST-2 heading names the file.
- **`Reset`'s missing-bucket branch was untested, and a released `podium-mcp` leaves an `index.db` without the `revisions` bucket.** CODE-4 states that `Reset` checks for the bucket before any `ForEach` or `Delete` and does not create it, citing `cmd/podium-mcp/resolution_cache.go:68-70`. TEST-1 adds a `Reset` case over an `index.db` holding only the resolution bucket that returns `(0, false, nil)` without panicking and leaves no `revisions` bucket.

### Pass 9 (2026-10-03, automated)

- **No test pinned per-artifact isolation of session references or in-memory marks.** TEST-2 (u) has session S accept artifact A at 300, has another session set B's mark to 500, and refuses S's replayed first answer for B at 300 with `reference_revision` 500. Over a fresh cache directory, S's first answer for B at 100 with no mark for B is delivered after S accepted A at 300. The pair runs again under a locked index so the `mem` keys are pinned. The CODE-4 implementor's choice on the key layouts names TEST-2 (u) and (d) as the tests of its constraint.
- **Correction: TEST-2 (d) did not pin the session part of the key constraint.** Every step of (d) passed under session references keyed by registry and artifact without the session, because the second session's 200 answer is below both the mark and the first session's reference. (d) now adds a step after the mark reaches 400: a third session's first answer at 300 is refused with `reference_revision` 400, which a session-agnostic key would deliver against the first session's reference of 300. The CODE-4 sentence now names that step as the test of the session part for session references.
