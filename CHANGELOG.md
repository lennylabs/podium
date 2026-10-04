# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **A verification key set for the registry signing key** (§4.7.9, §13.4,
  §13.12): the key file at `PODIUM_SIGN_KEY_PATH` takes zero or more `verify:`
  lines, each a base64 Ed25519 public key trusted for verification only. The
  `public:` key and the `verify:` keys form the registry's verification key
  set. The registry signs every new envelope under the `private:` key and
  admits a stored row whose signature verifies under any key of the set, so a
  rotation that keeps the retired public key on a `verify:` line leaves every
  row the retired key signed loadable. A key file with no `private:` or
  `public:` line, a line that does not decode, or a `public:` line that is not
  the public half of the `private:` line refuses the registry start with
  `config.signature_provider_unavailable`, naming the file.
  `docs/deployment/operator-guide.md` gives the rotation procedure.
- **`sign-stored-rows`** (§13.4): `podium-server sign-stored-rows` and
  `podium admin sign-stored-rows` re-sign under the signing key every stored
  row that a `verify:` key verifies and whose stored bytes reproduce its stored
  hash or the previous release's digest, and report how many rows remain
  signed under each verification-only key. When the store records that the
  first-start rewrite has completed, the command may run while registries on
  this release serve the store. When that record is absent, the command
  performs the rewrite in place of the first start and records its completion,
  so it runs only while no registry process on the previous release serves the
  store; it binds no listen address and cannot detect such a process. It signs
  each unsigned row that first start would sign, and beyond those it signs an
  unsigned row only under `--include-unsigned`, which attests every unsigned
  row the reviewed dry run lists. `--dry-run` lists every write and makes
  none, prints each row with its stored content hash in ascending order of
  tenant, artifact ID, and version, and ends with a plan digest,
  `sha256:<hex>`. `--plan-digest=<digest>` binds a run to that reviewed plan:
  a run whose own plan has a different digest writes no row, no audit event,
  and no completion record, prints its plan as `plan:` lines, and exits with
  status 3. Outside `--dry-run`, `--include-unsigned` requires `--plan-digest`,
  and `--plan-digest` with `--dry-run`, a malformed digest, and a bare
  `--include-unsigned` are usage errors. The command never generates a key:
  with signing on, an absent key file refuses it with
  `config.signature_provider_unavailable`. With signing off
  (`PODIUM_SIGN=none`), the command rewrites without signing and refuses
  `--include-unsigned` given with `--dry-run` or `--plan-digest` with the same
  code. While no completion is recorded, a registry start in either signing
  mode over a store that holds a manifest row, other than the SQLite store in
  the key file's directory, refuses, rewriting no manifest row, signing no
  row, and recording no completion, and names this command.
- **The Helm chart runs the §13.4 stored-row migration as a Job** (§13.4,
  §13.12): with `migration.mode` set to `dry-run` or `run` and `replicaCount`
  0, `helm install` and `helm upgrade` render a `post-install` and
  `post-upgrade` hook Job, in both signing modes, that runs
  `podium-server sign-stored-rows` with the Deployment's image, environment,
  Secret references, and mounts, and with `--include-unsigned` when
  `migration.includeUnsigned` is true, its default. The `run` Job passes
  `migration.planDigest`, the digest on the reviewed dry run's last line, as
  `--plan-digest`. A render refuses a migration mode without `replicaCount=0`,
  without `migration.previousImage` or on the image it names, or with
  `signing.mode=none` and `migration.includeUnsigned=true`, and refuses a
  `run` without `migration.planDigest`. The chart reads nothing from the
  cluster, so `helm template`, `helm install`, and `helm upgrade` render one
  set of manifests from one set of values, and GitOps controllers such as
  Argo CD run the upgrade as value commits. In either signing mode, a registry
  pod over a store that holds manifest rows and no completion record exits at
  start, rewriting no manifest row, signing no row, and recording no
  completion, so a serving render over an unmigrated store starts no pod that
  serves. New values: the `migration` block (`mode`, `includeUnsigned`,
  `planDigest`, `previousImage`, `backoffLimit`, `activeDeadlineSeconds`,
  `resources`, `podAnnotations`, and `podLabels`),
  `config.migrationObjectReadTimeout` for `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT`,
  and `config.auditLogPath` for `PODIUM_AUDIT_LOG_PATH`. The release notes of a
  zero-replica render print the stop step's wait command, and those of each
  migration step print the next command. `make test-live-kind` runs
  the procedure from v0.4.0 on a kind cluster.
- **`podium admin signing-key generate|rotate`** (§4.7.9, §13.12): writes the registry
  key file named by the required `--key-file` flag, and reads no environment
  variable and no default path. `rotate` keeps the previous keys as `verify:`
  lines, and `--staged-out` also writes the intermediate file a multi-replica
  rotation deploys first. Both print the verification key set as the
  comma-separated list `PODIUM_SIGNATURE_VERIFY_KEY` takes, followed by one
  `key_id=<hex> role=signing|verify` line per key.
  `docs/deployment/clustered.md` generates the chart's signing key with
  `generate` in place of a standalone first start.
- **Non-blocking SDK login** (§6.3): the Python SDK gains
  `Client.start_login()` and `Client.finish_login()`, and the TypeScript SDK
  gains `client.startLogin()` and `client.finishLogin()`. The start call
  returns a single-use `PendingLogin` handle with the verification URL, the
  user code, the code lifetime, and the poll interval, and it neither prints
  nor polls. The finish call polls with a timeout and a cancellation input
  (`threading.Event`, `AbortSignal`) and installs the token in memory.
  `DeviceCodeError` gains a `reason` (`denied`, `expired`, `timeout`,
  `cancelled`, `consumed`, or `failed`). `Client.login()` keeps its signature
  and output and now composes the pair; when the device code expires before
  the timeout, it raises reason `expired` where it previously reported
  `login timed out`.
- **`podium-mcp` refuses a stale `latest` load** (§4.7.10, §6.5): the registry
  serves `artifact_revision`, the time it stored the served version, and
  `podium-mcp` refuses a `load_artifact` that resolves `latest` when that
  revision is below one it already accepted for that registry and artifact ID.
  The refusal uses `materialize.stale_resolution` and writes nothing. Pinned
  versions are never compared. `podium cache reset-revisions` deletes the
  marks, and every MCP server that uses the cache directory must be stopped
  first.

### Fixed

- **An admin check that cannot be evaluated no longer exposes store detail** (§4.7.2): when the
  admin-grant lookup fails, the admin-gated endpoints still refuse with `403 auth.forbidden`, and the
  message is now `admin authorization could not be evaluated`. The registry logs the store error with
  the caller and tenant. Previously the message carried the store error, which on a multi-tenant
  registry named the internal schema ID of a request that resolves to no tenant.
- **Layer endpoints act in the caller's tenant on a multi-tenant registry**
  (§6.3.1, §7.3.1, §7.3.4): on a registry started with
  `PODIUM_MULTI_TENANT=true`, the layer-management endpoints under `/v1/layers`
  read and write the layer list of the tenant §6.3.1 selects for the request,
  and they check the §4.7.2 admin role, apply the user-defined layer cap, and
  scope the change events they publish to that tenant. The
  `layer_capabilities.manage_any_layer` member of `GET /v1/ui/session` reports
  the admin role in the same tenant. Previously these endpoints read and wrote
  the default tenant's layers for every caller and checked the admin role
  against the unrouted tenant, so on a registry with an identity provider
  configured and public mode off every admin layer operation was refused,
  including those of bootstrap admins, and an admin's registration was stored as
  a user-defined layer. Layer rows that routed callers registered on a
  multi-tenant registry before this release remain in the default tenant; to
  move one, register it again from the owning tenant and have an admin of the
  default tenant unregister the stale row, because the owner's requests no
  longer reach the default tenant.
- **A layer request that resolves to no tenant is refused on a multi-tenant
  registry** (§7.3.1): a request the registry rejects on its other endpoints
  with `401 auth.tenant_unknown`, because its verified organization names no
  provisioned tenant, receives the same rejection on the layer endpoints. Any
  other request that resolves to no tenant lists no layers, is refused with
  `403 auth.forbidden` on every layer write, and reads `manage_any_layer` as
  false, including on a registry started in public mode or with no identity
  provider configured.
- **Webhook receivers are keyed per tenant** (§7.3.2): receiver CRUD reads and
  writes only the receivers of the tenant the request resolves to, and the
  registry delivers each event only to the receivers of the event's tenant.
  Previously every tenant on a multi-tenant registry shared one receiver pool,
  so one tenant's admin could list, update, and delete another tenant's
  receivers, and every receiver received the events of every tenant. An event
  with no tenant now reaches no receiver, and a `PODIUM_WEBHOOK_STORE_PATH`
  store refuses to write a receiver with no URL, tenant, or ID.

- **The web UI command palette no longer flashes "Nothing matched"**: on the
  render where the typed query settled, the palette drew the no-match message
  for one frame before the search for that query was sent, and issued an
  unneeded catalog read. The shared request hook now reports that render as
  loading, so no surface treats the previous result as the answer to new
  inputs.
- **`podium layer watch <id>` accepts the layer ID as a positional** (§7.3.1):
  the command accepted the layer only through `--id`, so the invocation the
  specification writes, `podium layer watch <id> [--interval <duration>]`,
  printed `error: --registry and --id are required` and exited 2. The
  positional now names the layer, and flags may follow it. `--id` still names
  the layer. A positional and an `--id` naming different layers are refused
  with `error: layer id "<positional>" conflicts with --id "<flag>"` and exit
  2, and a missing layer ID now reads `error: --registry and a layer id are
  required`.
- **The `podium cache prune --dry-run` summary**: a dry run deleted nothing,
  but its summary line read `cache: pruned N bucket(s) (<size> B), kept M`, which said
  the buckets were pruned. The summary under `--dry-run` now reads
  `cache: would prune N bucket(s) (<size> B), would keep M`. The summary of a run
  without `--dry-run` is unchanged.
- **`podium artifact show` prints the body once** (§7.6.1): the human output
  of a non-skill artifact printed its body twice, because the `frontmatter`
  field of the `load_artifact` response carries the whole `ARTIFACT.md` and
  the command printed it in full before `manifest_body`. The command now
  prints only the fenced frontmatter block from that field, followed by the
  body. The `--json` output is unchanged.
- **A workspace-overlay skill load returns the `SKILL.md` body** (§4.3.4,
  §6.4): `load_artifact` on a skill in the `podium-mcp` workspace overlay
  (`PODIUM_OVERLAY_PATH`) returned the skill's `ARTIFACT.md` body, which is
  empty or a `<!-- Skill body lives in SKILL.md. -->` pointer, as
  `manifest_body`. It now returns the `SKILL.md` prose body, the value the
  registry returns for the same skill. The materialized files were already
  correct.
- **The docker-compose evaluation stack pulls MinIO again**: MinIO removed
  `minio/minio` and `minio/mc` from Docker Hub in September 2026, and
  `quay.io/minio` requires authentication, so `docker compose up` on a machine
  without a cached image failed to pull the object store. The stack now runs
  the `pgsty/minio` and `pgsty/mc` community builds of the same server and
  client, pinned by digest. `scripts/install-dev-deps.sh` pulls those images,
  and its native Linux path, which downloaded binaries from `dl.min.io`, now
  stops with a message, because `dl.min.io` no longer serves them.
- **Signature verification in `podium-mcp`** (§4.7.9, §6.6): a response that
  declared `sensitivity: low` skipped the signature check whatever signature it
  carried, and the `noop` provider accepted `noop:<content_hash>`, which any
  party could mint from the `content_hash` every `load_artifact` response
  serves. `podium-mcp` now verifies the response's `delivery_signature` under
  `always`, which is the only policy other than `never` (the `Removed` entry
  covers `medium-and-above`). `noop` refuses every signature it is asked to
  verify, and a `podium-mcp` configured with `noop` under `always` refuses to
  start with `config.signature_provider_unavailable`. A missing signature under
  `always` fails with
  `materialize.signature_missing`, where it was reported as
  `materialize.signature_invalid`. The verification runs before the local read
  event, the sandbox and runtime gates, and the harness adapter, so a refused
  load records no local `artifact.loaded` event, and the `resources/read`
  mirror runs the same verification before it returns an artifact's text.
- **The recorded content hash of an artifact that declares `extends:`** (§4.7.6, §7.5.3): a `podium sync` against a filesystem registry recorded a different `content_hash` for such an artifact than a `podium sync` against a registry recorded for the same artifact, because the filesystem consumer hashed the manifest it had merged with the parent while the registry hashed the manifest the author wrote. Both now record the §4.7.6 digest over the child's authored `ARTIFACT.md`. A filesystem-source lock entry for such a child therefore no longer moves when only its parent changed.

  A registry excluded a file named `SKILL.md` at any depth inside a skill package from the bundled-resource set, while a filesystem-source consumer excluded only the copy at the package root, so a skill carrying a file such as `references/SKILL.md` carried a different content hash in each mode. Both now exclude the package root's copy alone, and that file is an ordinary bundled resource. A stored row for such a package is republished under a new `version:`, because a registry refuses an ingest of an existing `(artifact_id, version)` whose content hash differs from the stored one.
- **The lock file's recorded content hash across sync modes** (§11, §7.5.3, §14.11): a `podium sync` against a filesystem registry and a `podium sync` against `podium serve --standalone --layer-path` on the same directory now record the same `content_hash` for an artifact. The filesystem consumer hashed `SKILL.md` in place of the manifest for a skill, while the registry hashes the manifest, `SKILL.md`, and every bundled resource, so a frontmatter-only edit to `ARTIFACT.md` changed the materialized output while the recorded `content_hash` stood still, and the same artifact carried a different hash in each mode. Both consumers now derive the hash from the shared `version.CanonicalContentHash`. The content hash of every artifact moves in this release, and the `Changed` entry on the content-hash serialization states what moves and what an operator does about it.
- **The materialization order across sync modes** (§7.5, §11): both modes now materialize the resolved set in ascending canonical artifact ID order, so config-merge fragments and inject blocks compose identically in every deployment mode and the lock's `artifacts:` list follows. A merged target that previously composed in layer order is rewritten once with the same entries in a different order. Inside a shared target the order governs composition: fragments two artifacts contribute to one key of a JSON config-merge target are folded in that order and composed by value kind, and a marker-block target receives each artifact's own Podium-managed block in that order and merges no keys. In filesystem mode the composition inside a shared target can therefore differ from before, matching what server mode already did. Where two artifacts with distinct canonical IDs set the same scalar key of one JSON config-merge target, such as two `mcp-server` artifacts sharing a `name:`, the value comes from the artifact whose canonical ID sorts last, which can be the one from the lower-precedence layer where filesystem mode previously took the higher-precedence one. Re-read merged targets after the first sync on this version.
- **`$PODIUM_CHANGED` for a `kind: workspace` target reports a change to the files on disk** (§7.5.2, §7.8): a workspace target's `publish` phase now computes the variable as a marketplace target does, by comparing the bytes of every file the sync writes or removes before and after materialization. An edit to an artifact that changes the bytes Podium writes into a shared materialized path reads `true`, whichever artifact's lock entry survived before. A re-sync that rewrites files with a new adapter output format, restores Podium's entry order in a shared config file, or restores a hand-edited or deleted file also reads `true`, so a `skip_if_no_changes` command runs. A re-sync that leaves every file byte-identical reads `false`, including one without a prior `.podium/sync.lock`. The lock file is not compared.
- `podium sync --config` reads the `PODIUM_REGISTRY` environment variable when
  no `--registry` flag is set, ahead of `defaults.registry`, as the §7.5.2
  precedence requires. A CI job that supplies the registry only through
  `PODIUM_REGISTRY`, as the §7.8 scheduled-publish example does, no longer
  fails with `config.no_registry`, and a `PODIUM_REGISTRY` exported alongside
  a config that sets `defaults.registry` now takes precedence over it.
- **The change-event stream applies layer visibility** (§4.6, §7.6): the
  registry delivers an event on `GET /v1/events`, and therefore through the SDK
  `subscribe` helpers and the `podium sync` watcher, only when the subscriber's
  identity can see the layer the event names. For each event, the registry
  reads the layer visibility once, when a subscriber's delivery first needs it,
  and applies that read to every subscriber of the event. Artifact and domain
  events are also narrowed by the subscriber's §6.3.1 path scopes, and an event
  published for another tenant is withheld. A subscriber that loses visibility
  of a layer, or whose visible layer is unregistered, still receives the
  `layer.config_changed` that withdraws it. A reorder's event names only the
  reordered layers the subscriber can see. A holder of the §4.7.2 admin role
  receives only the events its own §4.6 view admits. Previously every
  subscriber received every event, which disclosed artifact IDs, versions,
  domain paths, and layer names from layers the subscriber could not see.
  Under public mode and with no identity provider configured, the evaluator
  still admits every layer, so a subscriber in those modes receives every event
  of its tenant that its scopes permit.
- **Sigstore-keyless verification checks the signer** (§4.7.9, §6.2):
  `podium verify --provider sigstore-keyless` accepts an envelope only when its
  certificate's email or URI subject alternative name appears in
  `PODIUM_SIGSTORE_CERT_IDENTITY` and its OIDC issuer equals
  `PODIUM_SIGSTORE_CERT_OIDC_ISSUER`, when its timestamp verifies under a
  timestamp authority in the trusted root, when its inclusion proof leads to a
  checkpoint signed by a log key in the trusted root, and when the log entry
  records the content hash, the signature, and the certificate. The certificate
  is checked at the timestamp's time, so an envelope keeps verifying after its
  Fulcio certificate expires. Verification makes no network call.
- **Search, materialization, and audit-volume quotas are charged per tenant**
  (§4.7.8): on a multi-tenant registry, each `search_domains`,
  `search_artifacts`, and `load_artifact` request is charged to the tenant the
  request resolves to, under that tenant's own `search_qps` and
  `materialize_rate`, and each audit event counts against the daily budget of
  the tenant its request resolves to. Previously every tenant shared one search
  bucket and one materialization bucket at the `PODIUM_QUOTA_*` values, and the
  per-tenant values set through `/v1/admin/tenants` had no effect. When
  `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY` was set, every tenant also shared one
  daily audit budget, so one tenant's traffic could block every tenant's
  reingest. `GET /v1/quota` now reports the search QPS, materialization rate,
  and audit-volume limits the registry enforces.

- **The bulk load is charged against the materialization rate** (§7.6.2,
  §4.7.8): each item of `POST /v1/artifacts:batchLoad` (`Client.load_artifacts`,
  `loadArtifacts`) now counts as one load against the materialization rate of
  the request's tenant, the budget `load_artifact` draws on. When the rate
  refuses an item, that item and every later item in the request come back as
  per-item `quota.materialize_rate_exceeded` errors with `retryable: true`, and
  the batch status stays 200. Previously the bulk load charged nothing, so a
  caller that `load_artifact` refused could keep loading through it. A client
  that loads above its tenant's rate now receives these per-item errors; back
  off and retry the refused items, or raise the tenant's `materialize_rate`.

### Changed

- **The inbound webhook URL carries the layer's tenant ID on a multi-tenant
  registry** (§7.3.1): on a multi-tenant registry, `podium layer register`
  returns `/v1/ingest/webhook/{tenant-id}/{layer-id}`, and the
  `/v1/ingest/webhook/{layer-id}` route is not served. Re-register the webhook
  URL on the source repository for every Git layer on a multi-tenant registry,
  default-tenant layers included; the default tenant's segment is its tenant ID.
  Single-tenant registries keep `/v1/ingest/webhook/{layer-id}`.
- **`POST /v1/admin/erase` is refused on a multi-tenant registry** (§8.5,
  §4.7.1): on a registry started with `PODIUM_MULTI_TENANT=true`, erasure
  answers `403 auth.forbidden` for every caller, including a caller whose
  verified organization names no provisioned tenant, and changes nothing,
  because the registry keeps one audit file for every tenant and a redaction
  would rewrite other tenants' records. A registry with an identity provider
  configured and public mode off already refused it; a registry in public mode
  or with no identity provider configured previously admitted it and redacted
  every tenant's records. Single-tenant registries are unchanged.
- **Persisted webhook receivers must be registered again** (§7.3.2): a
  `PODIUM_WEBHOOK_STORE_PATH` file written by an earlier release keys every
  receiver under `default`. That key matches no tenant ID, including the
  single-tenant `default` org, whose ID is a UUID, so after the upgrade those
  receivers are neither listed nor delivered to. Before upgrading, record each
  receiver's `url`, `event_filter`, and `debounce` from `GET /v1/webhooks`, and
  its secret from the store file, because the API returns the secret masked.
  After upgrading, register each receiver again with `POST /v1/webhooks`,
  passing the recorded `secret`. Removing the old file before the restart
  discards the stale rows. A deployment without `PODIUM_WEBHOOK_STORE_PATH`
  keeps receivers in memory and is unaffected beyond the usual re-registration
  after a restart. This is a backward-incompatible change and lands in a MINOR
  bump. No flag, environment variable, or configuration key restores the former
  keying.

- **SDK `materialize()` rejects a harness other than `none`** (§2.2, §7.6):
  `podium-py` and `podium-ts` write only the canonical layout, and
  `materialize()` on a loaded artifact or a batch item now raises an argument
  error (`ValueError` in Python, `Error` in TypeScript) before writing when
  `harness` is any value other than `none`. Previously the argument was
  accepted and ignored. Run `podium sync --harness <name>` for harness-native
  files.

- **The §4.7.6 content hash length-frames its parts** (§4.7.6, §13.4, §6.4,
  §6.5): the canonical serialization now prefixes every part with its length
  before the SHA-256, so every content hash Podium computes moves. This reaches
  every stored manifest row, every `@sha256:` pin recorded against one, every
  §4.7.9 signature envelope, every `content_hash` recorded in a committed
  `sync.lock`, and every `content_hash` a §7.3.2 webhook subscriber recorded
  from an event payload. Podium is pre-1.0, so the change carries no algorithm
  identifier, no negotiation, no dual computation, and no compatibility path for
  the previous digest. This entry is the release's upgrade note.

  **Rules every upgrade follows.** Stop every registry process that uses the
  store before the first process on the new version starts. A registry still
  running the previous binary ingests under the previous digest, and no
  automatic start rewrites a row it writes after the new version's first start
  has examined its tenant. A `sign-stored-rows` run moves such a row to the new
  digest, signing it when a key of the registry's verification key set
  verifies its envelope, and, for an unsigned row the operator attests, under
  `--include-unsigned` with the reviewed dry run's `--plan-digest`. Back up the
  registry store after the stop, so that the backup holds every write the
  previous binary made. Restoring that backup is the only route back to the
  previous binary, and it is the only way to make the whole rewrite run again
  once it has completed; `sign-stored-rows` re-applies the rewrite's rules to
  the stored rows on demand.

  **The start rule.** While no completion of the rewrite is recorded, a
  registry start over a store that holds a manifest row, including a
  soft-deleted one, is refused in either signing mode, unless the store is the
  SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or
  at the default key location `~/.podium/standalone/registry-signing.key` when
  that variable is unset. The refused start applies the additive schema and
  seeds the default tenant and the bootstrap grants, and it rewrites no
  manifest row, signs no row, and records no completion. A store that holds no
  manifest row records completion at its first start. Over every other store,
  a standard deployment included, the rewrite runs through `sign-stored-rows`
  while no registry process on the previous release serves the store: a
  `sign-stored-rows --dry-run`, a review of the rows it lists, and a
  `sign-stored-rows` run with `--plan-digest` set to the `sha256:` value on the
  dry run's last line. The run records completion, and the start that follows
  it proceeds. This entry calls that sequence the reviewed `sign-stored-rows`
  pass. Wherever this entry says that the rewrite runs again over such a store,
  it runs through that pass, and the start is refused until the pass records
  completion.

  **Upgrade a standalone registry.** These steps apply where the store is the
  SQLite store in the key file's directory, which is the zero-configuration
  standalone layout under `~/.podium/standalone/`.

  1. Stop the registry.
  2. Back up `~/.podium/standalone/`, which holds the SQLite database, the
     objects directory, and the signing key file `registry-signing.key`.
  3. Replace the binary.
  4. Start the registry. The first start rewrites every stored content hash
     before it ingests or serves anything, unless `sign-stored-rows` already
     did, re-signs each row it rewrites where signing is configured, which it
     is by default from this release, and attaches a first envelope to each
     stored row that carries none. Read the summary line it logs, as
     "Summary line and untouched rows" below describes.

  **Upgrade a registry on any other store.** These steps apply to a registry
  run as a binary or a container over Postgres or over a SQLite file outside
  the key file's directory.

  1. Set `PODIUM_SIGN_KEY_PATH` to a file on the store's persistent storage,
     or set `PODIUM_SIGN=none`. With signing on and `PODIUM_SIGN_KEY_PATH`
     unset, the start is refused unless the store is the SQLite store beside
     the default key. A deployment that starts more than one registry process
     with signing on provisions the one key file at `PODIUM_SIGN_KEY_PATH`
     before the start, because processes that each generate a key at one path
     overwrite each other's key.
  2. Stop every registry process that uses the store.
  3. Back up the store.
  4. Install the new binary or image.
  5. With signing on, where no file exists at `PODIUM_SIGN_KEY_PATH`, create
     it with `podium admin signing-key generate --key-file <path>` and keep it
     in the deployment's backup. The upgrade start is refused before it loads
     or generates a key, and `sign-stored-rows` never generates one. Where the
     store holds signed rows, point `PODIUM_SIGN_KEY_PATH` at the key that
     signed them instead.
  6. Run `podium-server sign-stored-rows --dry-run --include-unsigned`, review
     the rows it lists, and run
     `podium-server sign-stored-rows --include-unsigned --plan-digest=<digest>`
     with the `sha256:` value on the dry run's last line. v0.4.0 stored every
     row unsigned by default, and `--include-unsigned` attests the unsigned
     rows the reviewed dry run lists. With signing off, both commands drop
     `--include-unsigned`, which the command refuses with signing off, and run
     with `PODIUM_SIGN=none` in their environment.
     Both commands run with the registry's own configuration: the store DSN,
     the object store, and the key file at `PODIUM_SIGN_KEY_PATH`. For a
     container, run them in the new image, whose entrypoint is
     `podium-server`, as `<image> sign-stored-rows ...`. Run step 5's
     `podium admin signing-key generate` on the operator's machine, because
     the image ships only `podium-server`.
  7. Start the registry, and read the summary the run wrote to stdout.

  **Upgrade a Helm chart deployment.** The chart runs the rewrite as a migrate
  Job in either signing mode. The Job has no probe, and the registry pods start
  after the rewrite is recorded. The section "Upgrading the chart from v0.4.0"
  of `docs/deployment/clustered.md` gives the commands, the signing-off values,
  the GitOps commit order, and an install over an existing v0.4.0 store.

  1. Prepare the values file and the signing Secret: this release's image,
     `migration.previousImage` naming the v0.4.0 image, and
     `signing.secretName`, or `signing.mode=none` with
     `migration.includeUnsigned=false`, because the command signs no row with
     signing off.
  2. Run `helm upgrade` with `replicaCount=0`, wait for the registry pods to be
     deleted, and back up the store. The chart's default rolling update would
     otherwise start the new version beside the previous one.
  3. Run a `migration.mode=dry-run` upgrade with `replicaCount=0`, which
     lists the plan and its digest, and review the Job's log.
  4. Run a `migration.mode=run` upgrade with `replicaCount=0` and
     `migration.planDigest` set to that digest, which performs the rewrite and
     records it. The chart refuses to render a migration step without
     `replicaCount=0`. Pass a `--timeout` longer than the pass takes, because
     Helm waits 5 minutes for the hook by default.
  5. Run a final upgrade without the migration values, which serves.

  Do not pass `--rollback-on-failure` or `--atomic` to those upgrades, because
  a rollback reinstalls the previous binary over rewritten rows. A GitOps
  controller such as Argo CD runs the same steps as value commits.

  **Upgrade the docker-compose evaluation stack.** The stack's registry runs
  with `PODIUM_SIGN: "none"` over Postgres, so it takes the reviewed
  `sign-stored-rows` pass with signing off before the upgraded registry starts.
  The service environment carries `PODIUM_SIGN: "none"`, so the command needs
  no other setting.

  1. Stop the registry with `docker compose stop registry`, and back up the
     store.
  2. Update the checkout to this release, and rebuild the image with
     `docker compose build registry`. An image built from a v0.4.0 checkout
     ignores the `sign-stored-rows` argument and starts a v0.4.0 registry
     that never exits.
  3. Run `docker compose run --rm registry sign-stored-rows --dry-run`, and
     review its report.
  4. Run
     `docker compose run --rm registry sign-stored-rows --plan-digest=<digest>`
     with the `sha256:` value on the dry run's last line.
  5. Start the stack with `docker compose up -d`.

  **Roll the consumers.**

  1. Roll consumers to the new binary after the registry serves on it, inside
     the same maintenance window, because a consumer on the previous binary
     fails its loads against the upgraded registry.
  2. Clear each consumer's cache, as "Clearing each consumer's cache" below
     states.
  3. Set `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key on every
     consumer that is not on the standalone registry's machine. Without it
     such a consumer refuses to start, as the entry "The registry signs at
     ingest by default, and `podium-mcp` verifies every load" below states. A
     consumer of a registry running with `PODIUM_SIGN=none` sets
     `PODIUM_VERIFY_SIGNATURES=never` instead.
  4. Remove `PODIUM_SIGNATURE_KEY_ID`, the `medium-and-above` value of
     `PODIUM_VERIFY_SIGNATURES` or `defaults.verify_signatures`, and
     `PODIUM_PREFETCH` with the `prefetch` configuration key, as the `Removed`
     entries state.
  5. On a machine where a v0.4.0 standalone registry ran, remove
     `defaults.verify_signatures: never` from `~/.podium/sync.yaml` unless the
     registry runs with `PODIUM_SIGN=none`. v0.4.0's bootstrap wrote that
     line, and no start of this release removes it, so such a consumer
     otherwise verifies nothing.

  **Return to the previous binary.** Restore the backup the upgrade takes,
  revert the registry and every consumer together, and clear each reverted
  consumer's cache. Remove `PODIUM_SIGN=none` from the registry's
  configuration, because v0.4.0 refuses that value with
  `config.invalid_sign_mode`; signing is off by default in v0.4.0. On the
  docker-compose stack, revert the checkout together with the image. On a Helm chart deployment, the "Rollback" section of
  `docs/deployment/clustered.md` gives the procedure.

  **What the rewrite does.** Where the store is the SQLite store in the key
  file's directory, the first start performs the rewrite. For every other
  store, the reviewed `sign-stored-rows` pass performs it before the start; it
  re-signs each row it rewrites where signing is on, and it leaves an unsigned
  row unsigned unless the reviewed run attested it under `--include-unsigned`.
  The rewrite appends one `artifact.signed` event per signed row to the audit
  sink, with the manifest-declared §8.2 redaction ingest applies. It leaves the
  tenant's dependency rows, layer configs, admin grants, and tenants untouched,
  and it re-ingests nothing. A `podium layer reingest` is neither required nor
  sufficient, because a reingest reaches only the version each artifact
  directory currently declares. A registry that turns signing on after the
  rewrite signs no row it already stores on any later start, and a signing
  registry refuses each row stored unsigned to every reader with
  `materialize.signature_missing` until a reviewed `sign-stored-rows` pass with
  `--include-unsigned` signs it or a new version of the artifact is ingested.

  **Start refusals.** A start can be refused in place of the rewrite. Three
  refusals name `PODIUM_SIGN_KEY_PATH` as the setting to fix, and the message
  says which it is.

  - The key-persistence refusal says that the registry signing key would be
    generated under the process's home, outside the directory that holds the
    store. It fires when signing is on, the store is Postgres or a SQLite file
    outside `~/.podium/standalone/`, and `PODIUM_SIGN_KEY_PATH` is unset,
    whether or not the store holds a signed row. Set `PODIUM_SIGN_KEY_PATH` to
    a file on the store's persistent storage, or set `PODIUM_SIGN=none`. With
    `PODIUM_SIGN=none`, a store other than the SQLite store beside the default
    key that holds rows and no completion record then meets the
    unmigrated-store refusal.
  - A refusal saying that stored rows carry a signature and need their content
    hash rewritten while no signer is configured means that the store holds
    signed rows. It comes from a start only over the SQLite store in the key
    file's directory, and otherwise from `sign-stored-rows` and its dry run.
    Remove `PODIUM_SIGN=none` and point `PODIUM_SIGN_KEY_PATH` at the key that
    signed them.
  - A refusal saying that `PODIUM_SIGN_KEY_PATH` names no file means that the
    store holds signed rows and the start would generate a key. Point the
    variable at the key that signed them. The Helm chart mounts the key from
    the Secret `signing.secretName` names, so on a chart deployment this
    refusal means the Secret does not hold that key under `signing.key`. Where
    no copy of the key survives, no start configuration clears the refusal
    against the deployment's own store. Write a persistent key file first with
    `podium admin signing-key generate --key-file <path>`. Where the store is
    the SQLite store in the key file's directory, start the deployment with
    `PODIUM_SIGN_KEY_PATH` naming that file. Over any other store, run the
    reviewed `sign-stored-rows` pass with `PODIUM_SIGN_KEY_PATH` naming that
    file before the start.

  The unmigrated-store refusal is the start rule above, and it names
  `sign-stored-rows`. When the key-persistence refusal, or the refusal for a
  signing key that did not exist before the start, also applies, the start
  reports that refusal first. The message names the store, one stored row, and
  the key location. Repair it with the reviewed `sign-stored-rows` pass, then
  start the registry. With signing on, the run repeats the dry run's
  `--include-unsigned` setting, and where no file exists at the key location,
  `podium admin signing-key generate --key-file <path>` creates it before the
  dry run, because neither the refused start nor `sign-stored-rows` generates a
  key. With signing off, both commands run with `PODIUM_SIGN=none` in their
  environment.

  A start over any store other than the SQLite store in the key file's
  directory, including an empty one, is also refused before it ingests
  anything when the rewrite leaves no completion record: its listener did not
  bind, a row held the record back, or the record write failed. The start
  stores no manifest row. Where the listener did not bind, the start reports
  the bind error (`serve: bind <addr>: <err>`); in the other two cases its
  message tells the operator to read the `rehash:` lines. Over an empty store, fix the cause and start again. Over a
  store with a held row, the next start meets the unmigrated-store refusal,
  and the reviewed `sign-stored-rows` pass records completion.

  **Summary line and untouched rows.** Where the store is the SQLite store in
  the key file's directory, the first start logs the rewrite's summary line;
  for any other store, the `sign-stored-rows` run writes it to stdout, which
  on a Helm chart deployment is the migrate Job's log. It carries the counts of rows
  rewritten, rows already migrated, and rows left untouched by class, and the
  rewrite logs one line per untouched row naming that row and its class.

  Rows the summary names as untouched keep their stored hash, and the
  registry's stored-row admission (§13.4) refuses each such row before any
  consumer check runs, to every reader, the SDKs and `podium sync` included,
  whatever the consumer's policy. The code and the repair depend on the row's
  class.

  - Every `unreproducible` row, and a `signature_unverified` row still at the
    previous release's digest, is refused with
    `materialize.content_hash_mismatch`.
  - An `unreproducible` row is repaired by publishing a new version of the
    artifact, because the record of the rewrite is set and no later rewrite
    examines it again.
  - A `signature_unverified` row at the framed digest is refused on a signing
    registry with `materialize.signature_invalid`. A `signature_unverified`
    row whose signing key is gone is repaired by listing that key's public
    half on a `verify:` line of the key file, restarting the registry, and
    running `sign-stored-rows`, which moves the row to the new digest and
    re-signs it. Where no copy of that public half survives, publish a new
    version of the artifact.
  - A `body_missing` row is refused with `materialize.content_hash_mismatch`
    and repaired by publishing a new version of the artifact.
  - A `body_unavailable` row is refused with `registry.unavailable` while an
    object read fails or times out. It is repaired by making the object
    readable and raising `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` where the read
    timed out. The rewrite left its record unset, so it runs again: at the next
    start where the store is the SQLite store in the key file's directory, and
    otherwise through the reviewed `sign-stored-rows` pass.
  - A row the wrong-root guard reclassified as `body_unavailable`, and every
    object-held row while the object store reports its body absent, are
    refused with `materialize.content_hash_mismatch`. Point the object store
    at the right root or bucket. That class holds the record of the rewrite
    back, so the rewrite runs again: at the next restart where the store is
    the SQLite store in the key file's directory, and otherwise through the
    reviewed `sign-stored-rows` pass.
  - A row stored unsigned is refused on a signing registry with
    `materialize.signature_missing`, which a reviewed `sign-stored-rows` pass
    with `--include-unsigned` repairs.

  Where a `rehash:` line reports that no object-storage read returned a body, or
  reports rows that hold the record of the rewrite back, that record stays
  unset and the rewrite runs over those rows again, as the start rule states.
  Where the summary reports every signed row as `signature_unverified` because
  the configured key is a different key from the one that signed those rows,
  restore the backup the upgrade takes and start again with the key that
  signed them, where the store is the SQLite store in the key file's
  directory. Over any other store, run the reviewed `sign-stored-rows` pass
  under that key against the restored store before its start.

  **Repairs on a Helm chart deployment.** The rewrite runs in the migrate Job
  rather than at a start, in either signing mode. Correct the values or the
  referenced Secrets and rerun the dry-run and run steps of
  `docs/deployment/clustered.md`. Raise `config.migrationObjectReadTimeout`
  when the object store reports itself healthy and a rerun dry run still lists
  `class=body_unavailable` rows. A run refused with exit status 3, such as one
  whose object-store reads failed after a clean dry run, wrote nothing and is
  repaired by rerunning the dry-run and run steps. After a backup restore,
  hold the Deployment at zero replicas and rerun steps 3 to 5.

  **Migrating with `podium admin migrate-to-standard`.** The command copies rows
  as the source stores them and clears the target store's record of the
  rewrite, so the rewrite runs again over the copied rows under the signing
  key that signed the source's rows: at the target's next start where the
  target is the SQLite store in the key file's directory, and otherwise
  through the reviewed `sign-stored-rows` pass against the target store, as
  the start rule states. No registry process runs on the target store while
  the command runs, so a target registry that is already running, such as one
  installed ahead of the migration, is stopped before the command's first run.
  The target registry is started, or restarted, only after a run of the
  command that exits 0. Recreate the target store empty before the command
  runs again when a run failed with the immutability error at a copied row, or
  when a registry started on the target store, or the source registry started
  on the new version, at any point after the command's first run against it
  began. On a Helm chart deployment, run the command before any chart release
  serves the target store, install or upgrade at zero replicas with
  `migration.previousImage`, and run the target's rewrite in the migrate Job,
  as the "Migration from single node" section of
  `docs/deployment/clustered.md` states.

  **Why the consumers roll with the registry.** The registry's stored-row
  admission (§13.4) refuses a row the rewrite has not rewritten to every
  reader, whatever binary the reader runs. For a registry-served load, an
  upgraded `podium-mcp` recomputes no §4.7.6 digest: it checks the §4.7.10 delivery record the registry serves,
  and only a filesystem-source `podium sync` recomputes the §4.7.6 digest. A
  consumer still on the previous binary, against the upgraded registry,
  receives neither `signature` nor `raw_frontmatter`, so it fails every load
  of a rewritten row with `materialize.content_hash_mismatch`, or earlier with
  its signature refusal wherever its policy requires a signature. Loads fail
  for the consumers that have not yet rolled, between the registry's first
  start on the new binary and the last consumer rolling. A reverted consumer
  refuses each cached bucket the new binary wrote with
  `materialize.content_hash_mismatch`, or earlier with
  `materialize.signature_invalid` wherever its policy requires a signature,
  because the new binary's buckets carry no bucket-level `signature` side file
  and the previous binary checks the signature before the content hash.

  **Clearing each consumer's cache.** The caches do not repair themselves, and
  the clear applies on every binary change in either direction, on the upgrade
  and on a return to the previous binary. On an upgraded consumer a
  pre-upgrade bucket carries no per-ID delivery side files and is a cache
  miss, so no unverified record is served from it: `offline-first` and a
  reachable `always-revalidate` fetch the artifact live, `offline-only` returns
  its offline cache-miss error, and the §7.4 degraded-network fallback
  surfaces `network.registry_unreachable`. The clear removes the pre-upgrade
  `raw_frontmatter` side files, which hold the IDs of parent artifacts, and the
  pre-upgrade `id@<semver>` resolution pins, which the §6.5 resolution index
  keeps across a binary change and never expires. The cache directory is
  `$PODIUM_CACHE_DIR` when that variable is set and `~/.podium/cache` when it
  is not. Remove that directory wholesale, or run `podium cache prune --days 0`
  together with `rm -rf <cache dir>/.resolutions`, because prune resolves the
  same default but is age-based and skips the resolution index. Object storage
  is keyed per blob and is unaffected. A `podium sync` rewrites the
  `content_hash` of every entry in its lock file on its first run against the
  new registry. That run reports a target as changed only when the bytes of its
  materialized files change, so a `skip_if_no_changes` command is otherwise
  skipped.

  **Workspace overlays.** A §6.4 workspace overlay now serves the canonical
  hash over its whole package, so an overlay skill's `content_hash` moves when
  its `SKILL.md` or one of its bundled resources changes.
- **The Helm chart's strategy rendering** (§13.4): the Deployment renders
  `strategy.rollingUpdate.maxSurge` and `strategy.rollingUpdate.maxUnavailable`
  explicitly under `RollingUpdate`, 25% each by default, and omits
  `rollingUpdate` under `Recreate`, so a release installed with server-side
  apply can switch to `Recreate`. The registry pod's selector labels take
  precedence over a `podLabels` entry with the same key, which previously
  rendered a duplicate key.
- **`load_artifact` serves a delivery attestation, and the response fields
  change** (§4.7.10, §6.6, §7.2, §7.6.2): the `load_artifact` response and each
  `artifacts:batchLoad` item carry `delivery_hash`, a digest over the record the
  registry served, which for an artifact that declares `extends:` is the merged
  record, `delivery_signature`, the registry's signature over it, minted per
  response with the registry-managed key, and `artifact_revision`, the time the
  registry stored the served version. `delivery_hash` covers
  `artifact_revision`, and the entity tag does not include it. The
  `load_artifact` response also carries `extends_pin`, the parent pin the child
  resolved at ingest, present only when the caller can see the parent record.
  The response no longer carries `raw_frontmatter`, `manifest_merged`, or
  `signature`, and a merged manifest above the inline cutoff is served through
  `manifest_body_url` like any other.
  A client that read any of the removed fields reads the new ones:
  `podium-mcp`, server-source `podium sync`, and the Python and TypeScript
  SDKs recompute `delivery_hash` on every registry-served load, independent of
  `PODIUM_VERIFY_SIGNATURES` and of sensitivity, and fail a mismatch with
  `materialize.content_hash_mismatch` before they apply their signature policy
  to `delivery_signature`. Each `ok` `artifacts:batchLoad` item also carries
  `sensitivity`, absent when the artifact declares none, so a client rebuilds
  the item's delivery record from the item alone, and the SDKs return a batch
  item that fails the check with `status: "error"` while the other items load.
  `podium-mcp` now reports a fetched body that fails the §4.7.10 step 6 check,
  which it reported as `materialize.fetch_failed`, and a `resources_base64`
  value that is not canonical base64, which it reported as
  `materialize.invalid_base64`, as `materialize.content_hash_mismatch`.
  Server-source `podium sync` and the SDKs frame `artifact_revision` into the
  record they verify and do not compare it with an earlier value, so
  `materialize.stale_resolution` remains a `podium-mcp` refusal. The Python SDK
  reads a `large_resources` link's URL from its `presigned_url` key alone, so a
  caller-built `LoadedArtifact` link keyed `url`, or given as a bare string,
  raises `MaterializeError`. `podium verify <artifact>` verifies the delivery pair,
  and with `--signature` it verifies that envelope over the content hash. The
  `load_artifact` entity tag folds in the `extends_pin` value the caller is
  served. A captured record still verifies when it is replayed, and
  `podium-mcp` refuses a replayed record that answers a `latest` resolution
  when its artifact revision is below the mark.

  Operator actions: roll the registry and the consumers together, as the
  content-hash entry above states, because a consumer on the previous binary
  refuses every load from the upgraded registry. Configure each consumer that
  is not on a standalone registry's machine with the registry's public key in
  `PODIUM_SIGNATURE_VERIFY_KEY`, and leave `PODIUM_SIGNATURE_PROVIDER` at its
  `registry-managed` default, because the delivery signature is a
  registry-managed envelope whatever key model signed the artifact at ingest;
  `podium-mcp` refuses to start under `sigstore-keyless` with `config.invalid`.
  A registry running with `PODIUM_SIGN=none` serves each delivery record
  unsigned. A registry process signs under a rotated key from its next response
  onward; rotate the key as the "Rotating the signing key" procedure in
  `docs/deployment/operator-guide.md` states, which adds the new key to each
  consumer's set before the registry signs under it.
- **`PODIUM_SIGNATURE_VERIFY_KEY` takes a verification key set** (§4.7.9,
  §6.2): the variable takes one base64 Ed25519 public key or a comma-separated
  list of them, and `podium-mcp` and `podium verify` accept a delivery
  signature that verifies under any key of the set. When the variable is unset
  they read the key file's `public:` line and every `verify:` line. An entry
  that is empty or does not decode fails `podium verify`, and under a policy
  above `never` refuses the `podium-mcp` start, with
  `config.signature_provider_unavailable`, naming the variable.
- **Every registry-managed envelope carries a `key_id`** (§4.7.9): the
  lowercase hex of the first 8 bytes of the SHA-256 digest of the signing
  public key, including an envelope `podium sign` mints. A verifier tries the
  named key first and then every other key of its set, and the `key_id` never
  on its own refuses an envelope a trusted key verifies.
- **`podium-mcp` recovers a cached delivery signature a rotation retired**
  (§4.7.10, §6.5, §7.4): a cached record whose delivery signature fails under
  the consumer's current key set is a cache miss in `always-revalidate` and
  `offline-first`, on the revalidation match, the 304 response, the
  `offline-first` cache hit, and the degraded-network fallback. The bridge
  refetches the artifact and replaces the cached record, and when the registry
  is unreachable the load returns the cache-miss outcome for the mode and never
  delivers the failing record. An `offline-only` consumer refuses such a record
  with `materialize.signature_invalid`, so it keeps the retired key in its set,
  or clears and refills its cache in another mode, before the key leaves the
  set.
- **Hidden parents are withheld on more read surfaces** (§4.6, §4.7.3):
  `GET /v1/dependents` returns an edge only when the caller can see both of its
  endpoints, testing the parent record an `extends` edge pinned, and answers
  `200 {"edges":[]}` for a target the caller cannot see. A search result for a
  child carries no `frontmatter` block when that block still names an ancestor
  in the child's pinned chain or when the chain cannot be resolved. The
  cross-layer collision rejection names the artifact and the `extends:` remedy
  and no longer names the layer that already contributes the ID. The web UI
  draws the extends rail from the served `extends_pin`.
- **The registry signs at ingest by default, and `podium-mcp` verifies every
  load** (§4.7.9, §6.2, §13.10, §13.12): `PODIUM_SIGN` defaults to
  `registry-key`, and `PODIUM_SIGN=none` or `--sign none` turns ingest signing
  off. `PODIUM_SIGNATURE_PROVIDER` defaults to `registry-managed` and
  `PODIUM_VERIFY_SIGNATURES` to `always`. `podium-mcp` resolves its
  verification key set once at start, from `PODIUM_SIGNATURE_VERIFY_KEY`, one
  key or a comma-separated list, or, when that variable is unset, from the
  `public:` line and every `verify:` line of the registry key file at
  `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`),
  and refuses to start with `config.signature_provider_unavailable` when a
  policy above `never` resolves none. A standalone consumer on the registry's
  machine resolves the registry's own key file. A consumer of any other
  deployment sets `PODIUM_SIGNATURE_VERIFY_KEY` to the registry's public key,
  which `docs/deployment/clustered.md` shows how to extract, and a consumer of
  a registry with `PODIUM_SIGN=none` sets `PODIUM_VERIFY_SIGNATURES=never`.
  `podium verify` resolves the verification key set the same way, and
  `podium sign` takes the key file's `private:` line. Roll the registry before
  the consumers, in the window the upgrade note above states, because an
  upgraded consumer refuses every response that carries no `delivery_hash`,
  and a registry on the previous release serves none. On a signing registry,
  the stored-row admission refuses every row stored unsigned to every reader,
  so the rows are signed before the registry serves. The first start signs
  them for the SQLite store in the key file's directory. For every other store, the
  pre-start `podium-server sign-stored-rows --dry-run --include-unsigned`
  review, followed by a run with `--include-unsigned --plan-digest=<digest>`,
  signs them, as the upgrade note states, and a start over any such store that
  holds rows is refused in either signing mode until that run records
  completion. A registry with signing on and no `PODIUM_SIGN_KEY_PATH` refuses to start unless its
  store is the SQLite store beside the default key. The Helm chart requires
  `signing.secretName` naming a Secret that holds the key file, unless
  `signing.mode=none`, and `docker-compose.yml` pins `PODIUM_SIGN: "none"`.
  The standalone bootstrap no longer writes `defaults.verify_signatures: never`
  into `~/.podium/sync.yaml`, and no start removes that line from a machine an
  earlier release wrote it on. Such a machine keeps verifying nothing, also
  when it is later pointed at another registry, until the line is removed;
  `podium-mcp` announces a `never` that a `sync.yaml` file supplied with a
  startup line naming the file. `defaults.verify_signatures` resolves across
  the `sync.yaml` scopes, and `podium config show` reports it.
  Server-source `podium sync` resolves its policy and verification key set by
  the `podium-mcp` rules and defaults and reads no `PODIUM_SIGNATURE_PROVIDER`,
  so a sync against a remote registry with no key material exits with status 2
  and `config.signature_provider_unavailable`, and a keyed sync against a
  registry that serves no `delivery_signature` fails with
  `materialize.signature_missing`. A standalone sync on the registry's machine
  resolves the registry's key file with no setup. The Python SDK takes the
  `verify_signatures` and `verify_keys` arguments, and the TypeScript SDK takes
  the `verifySignatures` and `verifyKeys` options. Each SDK defaults to
  `always` only when a verification key is configured, and otherwise to
  `never`. The Python SDK verifies signatures through the `podium-sdk[verify]`
  extra, which installs `cryptography`. Roll the registry before its SDK
  consumers, because a registry on the previous release serves no batch
  `sensitivity`, and a batch item for an artifact that declares one then fails
  with `materialize.content_hash_mismatch`.
- **The registry admits each stored row before it serves the row's content**
  (§13.4): a full `load_artifact` and each `artifacts:batchLoad` item recompute
  the row's content hash, check each bundled resource's stored hash and size
  against its bytes, and, on a signing registry, verify the stored signature,
  for every row of the artifact's `extends:` chain. A row that fails is refused
  to every reader, `podium sync` and the SDKs included, with
  `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or
  `materialize.signature_missing`, so on a signing registry a row altered in
  the store is refused whatever the consumer's policy. A refused
  `load_artifact` is returned as HTTP 500, and a refused `artifacts:batchLoad`
  item as an error envelope inside the 200 batch response. Each refusal is not
  retryable and carries a `suggested_action` naming the operator's repair.
  Turning signing on after the upgrade makes every row stored unsigned
  unloadable until a `sign-stored-rows --include-unsigned --dry-run` review
  and a `--include-unsigned --plan-digest=<digest>` run sign it, or a new
  version of it is ingested. During an object-storage outage a full load is
  refused with `registry.unavailable` when any row of the artifact's
  `extends:` chain, parents included, holds an object-held body, while a HEAD
  revalidation and a matching conditional GET still answer from the stored
  content hash.
- **`artifacts:batchLoad` returns small resources inline** (§7.6.2): on a
  deployment with an object store, an item carries each bundled resource at or
  below the 256 KB inline cutoff in the reference's `inline` field,
  base64-encoded with `inline_base64` when it is binary, where it carried a
  `presigned_url`. A client that reads only `presigned_url` must read `inline`
  as well. The Python and TypeScript SDKs already do.
- **`replaced_by` on every load** (§4.7.4): on a SQLite or Postgres store,
  `load_artifact` and each `artifacts:batchLoad` item return an artifact's
  declared `replaced_by` whether or not the artifact is deprecated, where they
  returned it only for a deprecated artifact, which matches the in-memory
  store.
- `podium layer reingest` reports the ingest outcome in its exit status. It
  exits 1 when the cycle dropped at least one artifact, which covers a
  same-version content conflict, a lint failure, and a rejection such as the
  public-mode sensitivity floor or a cross-layer collision, and it exits 0
  otherwise. On a cycle the registry answers 200, each conflicted and each
  rejected artifact is printed on standard error with its identifier, its error
  code, and its reason, including on a cycle that accepted nothing, where the
  command previously printed the response body to standard output and exited 0.
  A lint drop is reported as the number of diagnostics the cycle raised, and
  `podium lint` against the source names the artifacts behind them. A
  non-blocking advisory and a failed embedding call are reported without
  changing the exit status. A pipeline that gates on this command's exit status
  will now fail a reingest that lost work; pre-1.0, no flag restores the
  previous behavior.
- Under the `trusted-headers` identity provider, a layer's `groups:` filter is
  satisfied by the `X-Podium-User-Groups` value alone. The registry no longer
  expands a `groups:` filter against the directory pushed to the SCIM receiver
  under that provider. This is backward-incompatible and lands in a MINOR bump,
  which the pre-1.0 policy permits: a caller whose access rested on a
  SCIM-pushed membership loses it, and the operator provisions that membership
  at the gateway or moves the deployment to `oidc-jwt`.
- When the SCIM receiver is mounted, which `PODIUM_SCIM_TOKENS` controls, the
  registry reports the configuration at startup with the line
  `SCIM group expansion in layer visibility: <bool> (identity provider "<name>")`.
  A registry that mounts no receiver writes no such line.
- `GET /v1/admin/show-effective` reports
  `user matches layer.groups through the SCIM directory` for a layer admitted
  through the SCIM-pushed directory, which is distinct from
  `user matches layer.users or layer.groups` for one admitted on the caller's
  own group claim.
- The SCIM receiver keeps its mount on every identity provider. Setting
  `PODIUM_SCIM_TOKENS` mounts the endpoint, and the receiver accepts, persists,
  and serves pushes as before.
- A non-empty `PODIUM_IDP_GROUP_MAPPING` that does not resolve to a group
  mapping table fails startup with `config.invalid_idp_group_mapping`, under
  every identity provider. This covers a value carrying an entry without a `=`
  or with an empty name on either side, and a value of separators or whitespace
  alone. The registry previously logged the parse failure, dropped the whole
  table, and started, so every group claim value reached §4.6 visibility
  unmapped. A deployment whose setting carries a typo now refuses to start where
  it previously started with no mapping, which is backward-incompatible and
  lands in a MINOR bump. An unset or empty value still configures no table and
  startup proceeds. Pre-1.0, no flag, environment variable, or configuration key
  restores the previous behavior.
- **Filesystem-source `podium sync` applies the §4.6 collision rule** (§4.6,
  §7.3.1, §13.11.3): when two registry layer directories contribute the same
  artifact ID and the higher-precedence copy declares no `extends:` on that ID,
  sync drops the higher-precedence copy and materializes the lower-precedence
  one. That is the copy `podium serve --standalone --layer-path` serves on the
  same directory. Sync prints `rejected: <id> (ingest.collision): <reason>` on
  standard error for each dropped artifact, materializes every other artifact,
  writes the lock file without the dropped artifact, and exits 1. `--dry-run`
  reports the drop and exits 1. A `--config` target that dropped an artifact
  counts as a failed target, including a `kind: workspace` target under
  `--check`. `podium sync --watch` exits 1 on interrupt when any cycle it
  reported dropped an artifact, and `podium sync override` reports the drops of
  its re-materialization and exits 1. A sync that previously kept the
  higher-precedence copy and exited 0 now keeps the lower-precedence copy and
  exits 1. To keep overriding the lower copy, declare `extends: <id>` on the
  higher copy. A server-source `podium sync` and the workspace overlay are
  unchanged. This is a backward-incompatible change and lands in a MINOR bump.
  No flag restores the previous behavior.
- **SCIM store refusal** (§6.3.1, §13.12): a non-empty `PODIUM_SCIM_STORE_PATH`
  that the registry cannot read for a reason other than the file's absence, a
  non-empty file that does not parse as the directory document, a parent
  directory the registry cannot create, and a parent directory in which it
  cannot create a file each refuse startup with `config.scim_store_unavailable`.
  The refusal applies under every identity provider and whether or not
  `PODIUM_SCIM_TOKENS` mounts the receiver. A missing file and an empty file
  still load as an empty directory. Previously a read or parse failure logged
  `warning: SCIM persistence disabled` and kept the directory in memory, and an
  unwritable directory surfaced only on the first push. In both cases pushed
  records were lost on restart.
- **Audit anchor key refusals** (§8.6, §13.12): with
  `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0 and a file audit sink, an
  anchor key file that cannot be read, parsed, or generated refuses startup with
  `config.audit_anchor_key_unavailable`. Previously it logged a warning and the
  registry ran unanchored. While registry signing is on (`PODIUM_SIGN` or
  `podium serve --sign` resolves to `registry-key`), an anchor public key equal
  to the registry signing key or one of its `verify:` keys refuses startup with
  `config.audit_anchor_key_shared`, which names the `key_id`. Previously that
  configuration started with no warning. With the interval at 0, the registry
  does not read the anchor key.
- **Audit sink refusal while anchoring** (§8.6, §13.12): with
  `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` above 0, a file `PODIUM_AUDIT_LOG_PATH`
  that the registry cannot open refuses startup with
  `config.audit_sink_unavailable`. Previously the registry logged
  `warning: audit sink disabled` and ran with no audit sink and no anchoring. An
  `http(s)` value still disables anchoring with a warning, and with the interval
  at 0 an unopenable file sink is still logged and the registry starts. The
  warning text is now `warning: audit sink disabled: <cause>`.
- A deployment carrying one of these configurations stops booting after the
  upgrade. This is a backward-incompatible change and lands in a MINOR bump. No
  flag, environment variable, or configuration key restores the in-memory SCIM
  fallback or the unanchored start.
- **Every harness adapter derives the skill `compatibility` field** (§4.3.4,
  §6.7, §7.8): when a skill's `SKILL.md` omits `compatibility`, the Cursor,
  Codex, Gemini, OpenCode, and Pi adapters and the Claude, Codex, Cursor, Pi,
  and Hermes marketplace emitters now derive it from `runtime_requirements` and
  `sandbox_profile`, as the Claude Code adapter already did. An authored value
  is kept unchanged, and the `none` adapter still writes `SKILL.md` unchanged. A
  re-sync or re-publish rewrites the affected `SKILL.md` files. Lock-file
  `content_hash` values do not change, because they hash the authored artifact
  bytes rather than adapter output.
- `ingest.EventEmitter` and `Server.PublishEvent` take a `core.EventScope`
  that names the event's tenant, its layers, the artifact ID or domain path,
  and, on a `layer.config_changed`, the layer's visibility before the change.
  Every caller is updated, and no compatibility form remains.
- **The Sigstore-keyless envelope carries its log proof and a timestamp**
  (§4.7.9): the top-level `log_index` is replaced by a `tlog` object with
  `log_index`, `body`, `hashes`, and `checkpoint`, and a `timestamp` field
  carries an RFC 3161 token. Envelopes from earlier releases are refused.
- **`podium sign --provider sigstore-keyless` uses Rekor v2 and a timestamp
  authority** (§6.2): `PODIUM_SIGSTORE_FULCIO_URL`,
  `PODIUM_SIGSTORE_REKOR_URL`, and the new `PODIUM_SIGSTORE_TSA_URL` default to
  the Sigstore public-good instances, and a Rekor v1 URL fails the signing.
- **The Sigstore trust root is `trusted_root.json`** (§6.2):
  `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` names Sigstore's `trusted_root.json`, and
  its certificate authorities, transparency-log keys, and timestamp authorities
  apply only within their validity windows.
- A tenant quota value of zero for `search_qps`, `materialize_rate`, or
  `audit_volume_per_day` no longer disables the budget (§4.7.8). It takes the
  deployment default from `PODIUM_QUOTA_SEARCH_QPS`,
  `PODIUM_QUOTA_MATERIALIZE_RATE`, or `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY`, so a
  tenant set to 0 is throttled whenever the matching variable is positive. Set
  the tenant value to a negative number to exempt that tenant. A single-tenant
  registry keeps its limits from these variables and does not read its tenant
  record's values for these budgets. On a multi-tenant registry, requests that
  resolve to no tenant share one budget at the deployment defaults.

- On a multi-tenant registry, a tenant's positive `max_user_layers` is now
  enforced ahead of `PODIUM_MAX_USER_LAYERS` (§4.7.8, §7.3.1), in the same order
  as the search QPS, materialization rate, and audit-volume budgets.
  `PODIUM_MAX_USER_LAYERS` applies to every tenant whose value is zero, and a
  negative tenant value disables the cap. A deployment that relied on the
  variable to override larger tenant values sets those tenants'
  `max_user_layers` to 0, or to the intended cap, with `podium admin tenant
  update`. A single-tenant registry still reads its stored tenant record for
  this cap, unlike the three rate budgets. When that record holds a non-zero
  `max_user_layers`, the record's value now applies ahead of
  `PODIUM_MAX_USER_LAYERS`. `/v1/admin/tenants` is unavailable on a
  single-tenant registry, so the operator cannot change that value there.

### Removed

- **`PODIUM_SIGNATURE_KEY_ID`** (§4.7.9, §6.2): `podium-mcp` and
  `podium verify` no longer read the variable, because the consumer's
  verification key set decides which envelopes it accepts. Remove it from a
  consumer's configuration; a value left in place is ignored.

- **`PODIUM_VERIFY_SIGNATURES=medium-and-above`** (§4.7.9): the policy takes
  `never` or `always`, and no value reads an artifact's `sensitivity`. A
  consumer configured with `medium-and-above`, in the environment or in
  `defaults.verify_signatures` in `sync.yaml`, refuses to start with a message
  naming `never` and `always`. Pick `always`, or `never` against a registry
  running with `PODIUM_SIGN=none`, because on a signing registry the stored-row
  admission refuses every row stored unsigned to every reader whatever the
  consumer's policy.

- **The MCP server's startup cache warm-up** (§7.6.2): `podium-mcp` no longer
  calls `/v1/artifacts:batchLoad` at startup, and it no longer reads
  `PODIUM_PREFETCH` or the `prefetch` configuration key. Remove both from a
  consumer's configuration; a value left in place is ignored.

- **`DeviceCodeRequired`** from the Python SDK (§6.3). No code raised it.
  Catch `DeviceCodeError` instead.

- **The `harness` argument of `load_artifacts` and `loadArtifacts`, and the
  `harness` field of `POST /v1/artifacts:batchLoad`** (§7.6.2): the registry
  never read the field and runs no harness adapter (§2.2), so every `ok` item
  already carried the canonical manifest. Remove the argument from calls.
  Python raises `TypeError` when `harness=` is passed. TypeScript rejects
  `harness` in an options object literal at compile time, and `loadArtifacts`
  never sends the key. The registry decodes the request body as it decodes
  every other JSON body, so a `harness` key that an older SDK still sends is
  ignored.

- **`PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE`** (§6.2). It is no longer read. Set
  `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` instead.

- **`podium-mcp` no longer accepts
  `PODIUM_SIGNATURE_PROVIDER=sigstore-keyless`** (§6.2): the start refuses with
  `config.invalid`, naming `registry-managed`. Every delivery signature is
  registry-managed (§4.7.10), so the provider verified no load, and it accepted
  a keyless envelope from any OIDC identity. `podium sign` and `podium verify`
  keep `sigstore-keyless`.
- `server.WithTenant` removed (library API).

### Documentation

- HTTP API reference: layer tenant selection, the tenant-qualified webhook
  route, the multi-tenant erasure refusal, the per-tenant `manage_any_layer`
  posture member, and the change-event stream's delivery of public-layer events
  to a caller with no verified subject.
- Deployment pages: the per-tenant layer model, the tenant-qualified webhook
  URL, and the pre-release layer rows left in the default tenant.
- **Embedding-model switch on managed vector backends** (§4.7): per-row model
  versioning, query-time model filtering, and the stale-row purge apply to the
  collocated stores, pgvector and sqlite-vec. The vector-backends page gives the
  fresh-index procedure for Pinecone, Weaviate, and Qdrant and describes query
  results during the re-embed, and the operator guide links to it.
- §13.12 gains a row for `PODIUM_IDP_GROUP_MAPPING`, giving its syntax, its
  absence from the config file, the pass-through of a claim value with no entry,
  and the startup failure. §6.3.1 names the variable as the source of the
  `IdpGroupMapping` table and names `config.invalid_idp_group_mapping`.
- The Codex rows of `docs/consuming/configure-your-harness.md` name the artifact
  ID as the key Podium reconciles a `.codex/config.toml` entry by. The hook row
  previously named the native event, which selects the TOML table the entry
  lands in rather than identifying the entry, so a reader could expect two hook
  artifacts on one event to merge into a single entry. Each artifact keeps its
  own marker block, and the `mcp-server` row states the matching consequence for
  a name already present in the file.
- §13.12 now states the `registry.yaml` lookup. The registry reads the file
  that `PODIUM_CONFIG_FILE` names, and reads `~/.podium/registry.yaml` when the
  variable is unset. `podium serve --config <path>` sets the variable for the
  process. The spec no longer names `/etc/podium/registry.yaml`, which no code
  reads. The CLI reference gains a `PODIUM_CONFIG_FILE` row.
- §4.6 and §4.7.6 now state that an `extends:` child picks up a newer parent
  when it is published at a new `version:`, and that a re-ingest of a stored
  child version's unchanged bytes is idempotent and keeps the stored pin and
  folded fields. This corrects the spec text and changes no behavior.
- §6.2, the CLI reference, and `docs/authoring/bundled-resources.md` list the
  runtime-gate variables `podium-mcp` reads: `PODIUM_HOST_PYTHON`,
  `PODIUM_HOST_NODE`, `PODIUM_HOST_PACKAGES`,
  `PODIUM_ENFORCE_RUNTIME_REQUIREMENTS`, and
  `PODIUM_IGNORE_RUNTIME_REQUIREMENTS`.
- §13.12 gains rows for `PODIUM_SCIM_TOKENS` and `PODIUM_SCIM_STORE_PATH` and an
  Audit anchoring table for `PODIUM_AUDIT_LOG_PATH`,
  `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS`, and `PODIUM_AUDIT_SIGNING_KEY_PATH`.
  §6.3.1 states how the SCIM receiver is mounted and persisted, and §8.6
  specifies local chain-head anchoring and the key-separation rule.
  `docs/reference/cli.md` and `docs/reference/error-codes.md` list the variables
  and the new codes.
- §7.5.2 defines the variables Podium injects into a `kind: workspace` target's
  workflow: `$PODIUM_WORKDIR`, the absolute target directory;
  `$PODIUM_TARGET_ID`, the target entry's `id`; and `$PODIUM_REGISTRY`, the
  registry source. The `publish` phase also receives `$PODIUM_CHANGED`, and
  each `on_error` list receives the variables of its phase. A workspace
  workflow runs no command under `--dry-run` or `--check`. §7.8 and
  `docs/consuming/publishing.md` describe `$PODIUM_REGISTRY` as a URL or a
  filesystem path. The variable names and phases are unchanged; the Fixed entry
  above records the registry-source precedence change.
- §4.7 names the flagless `podium admin reembed` as the full re-embed pass, in
  place of an `--all` flag the CLI never had.
- §7.6 states the change-event stream visibility rule, and §4.6, §7.3.1, and
  §7.5.4 point to it. §7.3.2 states that the receiver fan-out applies no layer
  visibility filter and that a receiver's event filter is the only narrowing
  applied to it. `docs/consuming/configure-your-harness.md` no longer states
  that the stream applies no per-caller filtering, and
  `docs/reference/http-api.md` describes the filtered stream and the receiver
  delivery scope.
- §4.7.8 states which tenant each search, load, and audit event is charged to,
  how unrouted requests are charged, and how a tenant value relates to the
  deployment default. §13.12 documents `PODIUM_QUOTA_SEARCH_QPS`,
  `PODIUM_QUOTA_MATERIALIZE_RATE`, and `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY`.
  `docs/reference/http-api.md`, `docs/reference/cli.md` (the `podium quota`
  description, the `podium admin tenant` flag table, and the
  environment-variable table), `docs/reference/error-codes.md`, and
  `docs/deployment/clustered.md` follow, and the `podium admin tenant` flag help
  in `cmd/podium/admin_tenant.go` states the same zero and negative rule.

- §7.6.2 states how each bulk-load item is charged and reported, §4.7.8 names
  the bulk load as a materialization charge site and states the user-layer cap
  order, and §13.12 documents `PODIUM_MAX_USER_LAYERS` and states in the
  `PODIUM_QUOTA_MATERIALIZE_RATE` row that an over-limit bulk-load item is
  reported inside the batch's 200 response. `docs/reference/http-api.md`,
  `docs/consuming/custom-via-sdk.md`, `docs/reference/error-codes.md`,
  `docs/reference/cli.md` (the `--max-user-layers` row, the
  `PODIUM_QUOTA_MATERIALIZE_RATE` environment row, and the new
  `PODIUM_MAX_USER_LAYERS` environment row), and `docs/deployment/clustered.md`
  follow, and the `podium admin tenant` flag help states the cap's zero and
  negative rule.
- §4.7.10 states the delivery verification procedure every consumer follows,
  from the JSON rule and the exact-name members through canonical base64, the
  byte-compared resource paths, the record framing, and the fetched-body check,
  and §4.7.9 states the registry-managed envelope format.
  `docs/reference/http-api.md` gains a "Verifying a response" section with the
  procedure and the envelope format, `docs/reference/cli.md` describes the
  delivery check in server-source `podium sync`, and
  `docs/consuming/custom-via-sdk.md` gains a "Delivery verification" section
  for the SDKs.

## [0.4.0] - 2026-09-05

This release tightens authorization on the layer surface and fixes the
control-plane JSON field names. Registering a layer that names a filesystem path
on the registry host now requires the tenant administrator role, ingest is
confined to the directory a layer's path resolves to, and `GET /v1/layers`
reports only what the calling identity may read. A patch that asserts an owner
or a visibility field is now applied or refused rather than silently discarded,
and an admin-defined layer's visibility can be narrowed as well as widened. The
control-plane records that reached the wire under their Go field names now carry
the snake_case names the specification fixes. The bundled web UI is replaced by
an application covering the domain browser, search, the artifact viewer, and the
layer panel, and the registry acts as the OAuth client for a browser sign-in
flow. Runtime signing keys are read from operator configuration rather than
registered over HTTP, which closes a path to identity forgery.

Several of these are backward-incompatible and land in this MINOR bump, which
the pre-1.0 policy permits. A client reading a layer, receiver, quota, or admin
response by its Go field name must be updated, a reverse-proxy rule or bookmark
naming the web UI's `/ui/` path must be changed to `/app/`, a deployment running
`injected-session-token` must write its runtime keys to
`PODIUM_RUNTIME_KEYS_PATH` before the registry will start, and a deployment
where a non-administrator registers `local` layers must grant those callers the
tenant administrator role or move the layers to a network Git source. A
`values.yaml` for the Helm chart must be updated for the renamed and removed
keys named below.

### Added

- **Web UI and browser sign-in** (§13.10, §6.3.4, §7.3.4): a registry started with `--web-ui` serves an application covering the domain browser, search with the type, scope, and tag filters, the artifact viewer with the manifest body rendered as markdown and its merged frontmatter as a property table, and the layer panel with register, edit, reorder, reingest, unregister, and restore. It replaces the read-only page the flag mounted before, which covered the domain browser and a raw-text artifact view. The registry is the OAuth client for the browser: `GET /v1/ui/auth/sign-in` starts the authorization-code flow, `GET /v1/ui/auth/callback` performs the exchange server-side and returns the identity provider's token in an `HttpOnly` `__Host-podium_session` cookie, `GET /v1/ui/auth/sign-out` clears it, and `GET /v1/ui/session` reports the signed-in caller's posture. The cookie is a second accepted location for the credential §6.3.3 already defines, so the flow adds no credential kind and the registry stores no session state. A deployment that configures an identity provider registers its `/v1/ui/auth/callback` URL as a redirect URI with that provider. §6.10 gains `auth.csrf_invalid` for a callback whose state does not match and `auth.exchange_failed` for a code exchange the provider refuses. Writes on `/v1/layers` require the caller to own the target layer or to hold the §4.7.2 admin role, where any authenticated caller could previously patch, reingest, or unregister another caller's user-defined layer.
- **`git_provider` on a layer** (§7.3.1): a layer names the git provider whose signature scheme verifies its inbound webhook deliveries. `POST /v1/layers` and `POST|PUT /v1/layers/update` accept `git_provider` in the request body, and a layer declared in the `registry.yaml` `layers:` block sets it under `source.git.git_provider`, beside the existing `force_push_policy`. The field was assigned nowhere before, so every inbound delivery was verified under GitHub's signature scheme and a GitLab or Bitbucket source could not be configured to deliver at all. A value that names no registered provider, and any value on a `local` source, are refused with `400 registry.invalid_argument` naming the field; an unregistered value in the declared block aborts the boot with an error naming the layer and the value. The declared key is authoritative for a declared layer, because the boot re-seeds every declared entry from the configuration, so a value set over HTTP on such a layer reverts at the next start. An empty value resolves to `github` at the point of use, so a layer stored before this change verifies as it did and no migration is required. No CLI flag sets the field.
- **`layer_capabilities` in the session posture read** (§7.3.4): `GET /v1/ui/session` reports an object naming what the requesting caller may do on the §7.3.1 layer operations on this deployment. It carries `manage_any_layer`, a boolean reporting whether the deployment's layer endpoints admit this caller on the §4.7.2 admin arm, which is the arm that decides a write on a layer the caller does not own and every operation the local-source authorization rule governs. The object and its member are always present, and the member is `false` wherever the deployment determines no capability for the request. A registry started with no identity provider configured, or one started in public mode, admits every caller on that arm and reports the member as `true` there. The value is a snapshot taken when the read was answered, so an operation a client offers on the strength of it can still be refused, and the §6.10 envelope the operation's own endpoint returns remains the authority.
- **`email` in the session posture read** (§7.3.4): `GET /v1/ui/session` reports the requesting caller's own email as the configured identity provider recorded it. The key is present only where an email resolves and is non-empty, and absent otherwise, and it belongs to the caller that asked and to no other caller. The web UI's account cluster renders that email as the signed-in reader's identity and falls back to the subject where the read carries none, so a deployment whose subject is an opaque provider identifier no longer draws a UUID there. The cluster still appears only where the read reports a subject.
- **Helm chart templates and values** (`deploy/helm/podium`): the chart renders an Ingress from `ingress.enabled` and `ingress.host`, a bundled Postgres StatefulSet and Service from `postgresql.enabled`, and a PersistentVolumeClaim for the filesystem object store from `objects.enabled`. `runtimeKeys.enabled` with `runtimeKeys.secretName` mounts the `PODIUM_RUNTIME_KEYS_PATH` key file for the `injected-session-token` identity provider. The deployment renders the S3 and filesystem object-store settings, `PODIUM_OAUTH_GROUPS_CLAIM`, `PODIUM_IDP_GROUP_MAPPING`, `PODIUM_BOOTSTRAP_ADMINS`, and `PODIUM_DEFAULT_LAYER_VISIBILITY` as environment variables, and accepts `nodeSelector`, `tolerations`, `affinity`, `podAnnotations`, `podLabels`, `extraEnv`, `service.annotations`, and a `RollingUpdate` strategy. `config.publicMode` and `config.allowPublicBind` travel together, because the chart renders a `0.0.0.0` bind for the kubelet probes and public mode refuses a non-loopback bind on its own.

### Changed

- **Runtime signing keys come from operator configuration** (§6.3.2, §13.12): `POST` and `GET /v1/admin/runtime` are removed and answer `404`. That endpoint registered a token-verification signing key under no authorization at all, and under `injected-session-token` the same key store backed the verifier, so an unauthenticated caller could install a key and then mint a token for any identity, a tenant administrator included. The registry reads its runtime public keys at startup from the file named by `PODIUM_RUNTIME_KEYS_PATH`. Under `injected-session-token` a path that is missing, unreadable, or carries no key aborts the boot with `config.runtime_keys_unavailable`. `podium admin runtime register` writes that file and requires `--keys-file`, and it calls no registry. `podium admin runtime list` is removed, because the target is a local JSON file. The §13.1.1 evaluation stack in `docker-compose.yml` no longer selects `injected-session-token` and publishes its port on loopback. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the HTTP registration surface.
- **Re-embed authorization** (§4.7, §4.7.2, §13.2.1): `POST /v1/admin/reembed` requires the per-tenant admin role and is refused on a read-only registry, which is what every sibling admin write already did. It previously checked the request method alone, so any authenticated caller could trigger a full-catalog re-embed, which bills the configured embedding provider and purges the tenant's vector rows that do not match the current model. A registry started with no identity provider configured, and one started in public mode, authenticate no caller and admit the call as before.
- **Visibility patches on an admin-defined layer** (§7.3.1, §4.6, §8.1): `POST|PUT /v1/layers/update` applies each visibility member the request body carries and preserves each member the body omits, so `"public": false` withdraws that axis, `"organization": false` withdraws that one, and `"groups": []` and `"users": []` empty those lists. The endpoint previously granted on each axis and withdrew on none: a body carrying a false boolean or an empty array left the stored value in place and answered `200` with a record reporting the visibility the patch tried to replace, so an admin-defined layer's visibility could be widened and never narrowed. JSON `null` on a member is that member's empty value and withdraws on the same terms as `[]`, and an emptied list is stored as absent, so the layer object reports it as `null`. `owner` keeps its value reading: a non-empty value replaces the stored owner and an empty one leaves it. A stored record that sets no visibility field matches no §4.6 condition and reaches no composed view; it remains readable through `GET /v1/layers` by a caller holding the §4.7.2 admin role, and a registry started with no identity provider configured, one started in public mode, and one started with no request-time identity verifier admit every caller as before. A layer declared in the `registry.yaml` `layers:` block is re-seeded from that block at every start, so a narrowing applied to a declared layer over HTTP reverts at the next start, on the same terms as `git_provider`. `podium layer update` builds its request body from the flags the operator set rather than from the flags holding a non-zero value, so `--public=false` and `--organization=false` express a withdrawal where the value was previously dropped; `--clear-groups` and `--clear-users` empty their member, which the repeatable `--group` and `--user` flags cannot express, and each is refused with exit 2 when combined with its repeatable flag. The web UI's Edit dialog offers a checkbox for each visibility axis and a removable token per stored group and user on an admin-defined layer, where it previously stated them as fixed. A patch that would store what the layer already holds writes no record, emits no §8.1 audit event, and wakes no watcher, and it answers `200` with the unchanged layer object as before. `POST /v1/layers/reorder` compares the tenant's precedence sequence before and after the write, so a reorder naming layers in the order they already hold emits nothing; it previously emitted on any non-empty order. A webhook-secret rotation, a `force_push_policy` change, and a `git_provider` change record their `layer.config_changed` audit event and wake no `podium sync --watch` subscriber, because none of them can alter what a profile resolves. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the grant-only application or the unconditional emission.
- **Control-plane JSON field names** (§7.2.1, §7.3.1, §7.3.2): the control-plane response records that reached the wire under their Go field names now carry the lower snake_case names §7.2.1 fixes. The layer object, returned by `POST /v1/layers`, `POST|PUT /v1/layers/update`, `GET /v1/layers`, `GET /v1/layers?deleted=true`, and `POST /v1/layers/reorder`, renames `ID` to `id`, `SourceType` to `source_type`, `Repo` to `repo`, `Ref` to `ref`, `Root` to `root`, `LocalPath` to `local_path`, `Order` to `order`, `UserDefined` to `user_defined`, `Owner` to `owner`, `Public` to `public`, `Organization` to `organization`, `Groups` to `groups`, `Users` to `users`, `GitProvider` to `git_provider`, `LastIngestedRef` to `last_ingested_ref`, `CreatedAt` to `created_at`, and `DeletedAt` to `deleted_at`. `force_push_policy` and `last_ingested_at` already carried snake_case names and are unchanged. The receiver object, returned by `GET` and `POST /v1/webhooks` and by `GET` and `PUT /v1/webhooks/{id}`, renames `ID` to `id`, `URL` to `url`, `Secret` to `secret`, `EventFilter` to `event_filter`, `Disabled` to `disabled`, `FailureCount` to `failure_count`, `LastDelivery` to `last_delivery`, `LastFailure` to `last_failure`, `CreatedAt` to `created_at`, and `Debounce` to `debounce`. That rename is a projection over the stored receiver, so the operator's `PODIUM_WEBHOOK_STORE_PATH` file keeps its format and an existing file is read as it was. `GET /v1/quota` reports the limits under `limits` under the names `GET /v1/admin/tenants` already reported, renaming `StorageBytes` to `storage_bytes`, `SearchQPS` to `search_qps`, `MaterializeRate` to `materialize_rate`, `AuditVolumePerDay` to `audit_volume_per_day`, and `MaxUserLayers` to `max_user_layers`. Each element of `GET /v1/admin/show-effective`'s `layers` array renames `LayerID` to `layer_id`, `Visible` to `visible`, and `Reason` to `reason`. The bulk arm of `POST /v1/admin/reembed` renames `Total` to `total`, `Succeeded` to `succeeded`, and `Failed` to `failed`, and each failure entry renames `ArtifactID` to `artifact_id`, `Version` to `version`, and `Reason` to `reason`; the endpoint's single-artifact arm was already lowercase and is unchanged. The layer object no longer carries the tenant identifier, which was the registry's own stored tenant repeated on every record of a read that is not admin-gated, and the receiver object no longer carries its `TenantID` for the same reason. `GET /v1/quota`'s top-level `tenant_id` and the `id` of each element of `GET /v1/admin/tenants` name the tenant that is the record's own subject and are unchanged. The receiver object's `debounce` is now the duration string the request body accepts, so a receiver created with `"debounce": "60s"` reads back as `"1m0s"` and that value can be sent to `PUT /v1/webhooks/{id}` without conversion, where the member was a nanosecond integer no request accepts; it is omitted on a receiver that sets no window. A read is not replayed whole, because `secret` is reported masked as `***` and the update stores whatever secret the body names, so a replayed read would overwrite the receiver's secret with the mask. The receiver object's `created_at`, `last_delivery`, and `last_failure` are emitted in UTC, where a registry process running in a non-UTC zone emitted them with that zone's offset. The layer object's `created_at` is emitted in UTC on a Postgres deployment whose session time zone is not UTC, where the driver returned it in the session zone and the standard and standalone deployments reported the same field in two forms. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, configuration key, or content negotiation restores the Go-cased keys. A client reading any of these bodies by its Go field name must be updated to the name above.
- **Admin-only registration fields** (§7.3.1): `POST /v1/layers` refuses a registration that asserts `owner`, `public`, `organization`, `groups`, or `users` from a caller the §4.7.2 admin arm does not admit, with `403 auth.forbidden` carrying `details.constraint: "admin_only_fields"` and a message naming the asserted fields. Such a registration previously discarded the assertion, stored the layer as user-defined with the implicit `users: [<registrant>]` visibility, and answered `201`. A field is asserted by its value rather than by its presence: `public` or `organization` set to true, a non-empty `groups` or `users`, and an `owner` naming a subject other than the caller's own. A false boolean, an empty array, an empty string, and an `owner` naming the caller's own subject assert nothing, so a registration that asserts none of the fields is unaffected and still answers `201`. The rule keys on the caller's admin arm rather than on the resolved layer class, so it reaches a re-registration of a stored layer the caller owns on the same terms, and a registry started with no identity provider configured, or one started in public mode, admits every caller on that arm and refuses nothing. Where a registration is on both this rule's arm and the local-source authorization rule's arm, the local-source refusal is the one returned. Operators should note that automation registering a layer as a non-admin with `--public`, `--organization`, `--group`, `--user`, or `--user-defined --owner` naming another subject exited `0` on a registration that applied none of it and now fails with a non-zero exit and that envelope. The registration succeeds once the caller holds the tenant `admin` role, or once the flag is dropped. This is a backward-incompatible change and lands in a MINOR bump. No flag, configuration key, or environment variable restores the discard.
- **Local-source layer authorization** (§7.3.1): registering a layer that names a filesystem path on the registry host, patching a stored layer's filesystem path, restoring such a layer, and reingesting one now require the tenant `admin` role. Any other caller is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`, and the refusal names no filesystem path. A `git` source whose repository string resolves to go-git's file transport names a repository path on the registry host, so it is on the same arm and a non-admin registering one is refused; a repository string naming a network endpoint is not. The classification fails closed: a repository string the registry cannot place as a network endpoint is treated as naming a host path and is on the same arm, so a stored `git` layer whose repository string go-git rejects is refused for a non-admin on register, restore, reingest, and every inbound webhook delivery until an admin takes it or its repository string is corrected to a network endpoint. A `git` source is classified on its repository string alone, so a filesystem path stored beside one places nothing on the arm. `unregister` and `reorder` name no path and are unaffected. An inbound webhook delivery triggers a reingest and takes the same arm, so on a registry that authenticates its callers a delivery to a `git` layer whose repository string names a path on the registry host is refused. A delivery to a `local` layer is rejected earlier, with `400 registry.invalid_argument`, because the webhook path accepts a `git` source alone. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so no caller holds the admin role and every operation the rule governs is admitted there as before. The default is closed: a deployment where a non-admin registers `local` layers today must grant those callers the tenant `admin` role (`PODIUM_BOOTSTRAP_ADMINS` or a per-tenant grant), or move those layers to a `git` source over a network transport, or have an admin register and reingest them. This is a backward-incompatible change and lands in a MINOR bump. No flag, configuration key, or environment variable restores the prior behavior.
- **Layer panel controls** (§13.10): the web UI renders a control that would take a §7.3.1 layer write only where the caller's reported capabilities and the target layer's own fields admit it. A control the prediction refuses is absent rather than disabled, and a control it admits is still disabled by the read-only marker as before. The reordering affordance is the exception: a move names every layer in the moved row's class block and the registry refuses the request whole, so the handles stay present and disabled and the label names the reason, `Precedence — reordering these layers requires the administrator role`. The registry remains the authority, so a refusal the panel could not predict is still reported as the endpoint's error envelope.
- **A set of accepted audiences** (§6.3.3): `PODIUM_OAUTH_AUDIENCE` and the config-file key `identity_provider.audience` configure a set of audience values the registry answers to rather than a single value. The environment variable takes a comma-separated list, and the config-file key takes a string or a list of strings; a string is one audience verbatim and is not split on any separator. Entries are trimmed, blank entries are dropped, and repeated entries are collapsed keeping the first occurrence. A token is accepted when its `aud` claim carries at least one configured value, under `oidc-jwt` and under `injected-session-token` alike, and a caller admitted under any configured value has the same effective view, the same grants, and the same audit identity. A token carrying no `aud`, an empty `aud` list, or an `aud` that is the empty string is still rejected, and a setting that resolves to no entry still fails startup with `config.oidc_jwt_audience_unset` or `config.injected_token_audience_unset`. The first configured value is canonical and is the audience the §6.3.4 browser sign-in redirect asks the identity provider for, so a deployment enabling that flow lists the value its browser client is issued first. The `oidc-jwt` startup log now names the accepted audiences beside the accepted issuers. `podium config show --server` reports the set joined with commas under `oauth_audience` and attributes the row to `PODIUM_OAUTH_AUDIENCE` when the environment variable set it and to `registry.yaml` otherwise, where it previously reported `default`. A registry configured with one audience behaves as it did.
- **`--local` usage strings**: the `--local` flag help on `podium layer register` and `podium layer update` names the administrator role the registry now requires for a filesystem path.
- **Helm chart defaults** (`deploy/helm/podium`): three defaults each stopped a `helm install` on their own. `config.identityProvider.type` defaulted to `oauth-device-code`, which the registry refuses at startup with `config.identity_provider_unverified`, and is now `oidc-jwt`. `config.embeddingProvider.type` defaulted to `openai`, which exits without `OPENAI_API_KEY`, and is now `none` with hybrid search as an opt-in. `config.bind` was declared and rendered by no template, so the registry kept its loopback bind and the kubelet probes could not reach the pod; it is rendered as `PODIUM_BIND`. `config.identityProvider.authorizationEndpoint` is renamed to `config.identityProvider.issuer`, which is the key `oidc-jwt` reads, and `config.identityProvider.audience` defaults to empty so a value supplied through `existingSecret` is not overridden by a placeholder. The `config.discovery.*` values were reachable by no route, because §13.12 discovery is config-file-only and this chart renders no ConfigMap, and the inert `ingress:` and `serviceMonitor:` value blocks are removed. `envFrom` renders only when `existingSecret` names a secret, where an empty value previously produced a manifest the API server rejects. A `values.yaml` naming the removed or renamed keys must be updated.
- **Web UI path**: the bundled web UI is served at `/app/` instead of `/ui/`, and a registry started with `--web-ui` redirects `GET /` to it. `/ui/` is no longer served and no alias replaces it, so a reverse-proxy rule or a bookmark naming the old path must be updated. The browser-flow routes are unchanged: `/v1/ui/auth/sign-in`, `/v1/ui/auth/callback`, `/v1/ui/auth/sign-out`, and `/v1/ui/session` keep their paths, so no identity-provider client configuration and no registered redirect URI changes. A registry started without `--web-ui` answers `GET /` exactly as before.

### Fixed

- **Owner and visibility patches on a user-defined layer** (§7.3.1, §4.6): `POST|PUT /v1/layers/update` refuses a patch that asserts `owner`, `public`, `organization`, `groups`, or `users` against a stored user-defined layer, with `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"` and a message naming the asserted fields. Such a patch previously discarded the assertion, left the stored record's owner and visibility unchanged, and answered `200`, so a caller was told a widening applied that never did. A field is asserted by the value the patch would store rather than by its presence: `public` or `organization` set to true, a non-empty `groups`, a `users` differing from the layer's stored `users`, and an `owner` naming a subject other than the layer's stored owner each assert the field. A member asserts when the value the patch would store differs from the stored one, so an absent member, a false boolean, an empty `groups`, an empty string, and a restatement assert nothing while emptying a stored `users` asserts and is refused, and a client that reads a layer object and sends it back verbatim is still admitted; the layer object carries `owner` and `users` on every layer, and a stored user-defined layer holds `users: [<owner>]`. The comparison against a stored value is exact, element for element and byte for byte, so a value differing only in element order or in surrounding whitespace asserts the field. The rule reads the stored layer's class rather than the requesting caller, so it binds every caller the layer write rule authorizes, a tenant admin included, and a registry started with no identity provider configured and one started in public mode refuse on the same terms. The refusal rejects the whole request: a `rotate_webhook_secret` carried in the same body mints no secret, no other field the same body carries is applied, no record is written, and no §8.1 audit event is emitted. A patch the layer write rule or the local-source rule refuses keeps its own `auth.forbidden` envelope, because this rule is evaluated after both. An administrator who needs the layer visible more widely re-registers its ID through `POST /v1/layers` as an admin-defined layer carrying the visibility they declare; that re-registration replaces the stored record, so the registration time becomes the time of the re-registration, the order is recomputed at the tail of the layer list, `last_ingested_ref` and `last_ingested_at` are emptied, a `git` source is issued a new inbound webhook secret that must be registered at the Git host, and the former owner regains a slot against the per-identity user-defined layer cap. A request that received `200` before receives `400` after, which reaches `podium layer update --owner`, `--group`, and `--user`, and `--public` and `--organization` where the flag is set true, against a user-defined layer, and an operator on a deployment with no identity provider who corrected an `--owner` typo that way now reads the refusal and re-registers instead. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the discard.
- **Unconfined local layer path**: no authorization governed the filesystem path a layer named, so an authenticated non-admin could register a layer pointing at any directory readable by the registry process and have its contents ingested and served back. The local-source authorization rule above closes that path, and the ingest confinement below bounds what a layer that is admitted may read.
- **Ingest reads through a symbolic link leaving the layer root** (§7.3.1): an ingest that reads a layer's configured filesystem path as a directory now reads only within the directory that path resolves to, whichever caller declared the layer and in every deployment mode, including a layer the operator declares in the registry's own configuration. The confinement is not configurable. A relative symbolic link that resolves within the directory is read. A symbolic link whose target is written as an absolute path is refused whatever it resolves to, including a target inside the same layer, and such a link is repaired by rewriting it with a relative target. A read the ingest requires and cannot satisfy fails that layer's whole ingest, which reports `ingest.source_unreachable`, so no artifact and no bundled resource from that snapshot is accepted while the artifacts served before the refusal stay in place until the layer is restructured. A `DOMAIN.md` the refused cycle read before the failing read is still committed, and it emits its `domain.published` event only where that file was added or changed since the previous ingest, so a cycle the confinement refuses repeatedly over an unchanged domain emits no further event.
- **Layer list visibility**: `GET /v1/layers` reports what the calling identity may read, on both the live and the `?deleted=true` arm, and the reorder response reports the same set. A caller holding the tenant `admin` role still receives the tenant's whole layer list. Any other authenticated caller receives the layers visible to that identity. A caller the registry resolves as anonymous receives an empty list, and a caller whose credential fails verification is refused with the same `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` envelope the registry's other routes already answer for that credential. A layer outside that set is absent from the response rather than refused, and no error code reports the narrowing. A registry started with no identity provider configured, and one started in public mode, return the whole layer list to every caller as before. On upgrade, a signed-in non-admin sees fewer rows in `podium layer list` and in the web UI layer panel, and an unauthenticated caller against a registry that configures an identity provider sees none. A caller presenting a stale or forged token to `GET /v1/layers` now receives that refusal where it previously received the full list, and the same caller receives it from `POST /v1/layers/reorder` in place of the `403 auth.forbidden` the write gate answered before, because the registry now reports that it could not verify the credential before it evaluates whether that caller may write.
- **Search over an `extends:` child**: an artifact that inherited `description`, `tags`, `sensitivity`, or `search_visibility` from its parent was indexed and filtered under its own authored values, so `search_artifacts` could not find it by the description `load_artifact` served for it. Ingest now folds the pinned parent into those four indexed columns, and a search result carries the resolved sensitivity.
- **Parent disclosure in a search result**: the `frontmatter` block of a `search_artifacts` result carried the child's `extends:` line, which names a parent the caller may not be able to see. The key is now removed before the block is returned, and the block is empty when the stored frontmatter cannot be decoded.
- **Public-mode sensitivity floor over an inherited value**: a registry in public mode evaluated the floor against the sensitivity an artifact declared itself. An artifact declaring `extends:` and no `sensitivity:` was admitted and then stored at the parent's level, which is the level the floor refuses. The floor is now evaluated again on the inherited value, and such an artifact is rejected with `ingest.public_mode_rejects_sensitive`.
- **`podium sync --watch` against an authenticating registry**: the watcher built its `/v1/events` subscription with no `Authorization` header while the one-shot fetch attached the caller's token, and `/v1/events` is not exempt from identity verification, so a registry configuring an identity provider answered `401` and the watcher reconnected every 500ms without ever triggering a run. The non-2xx response returned silently into the reconnect loop, which made a rejected subscription indistinguishable from an idle one. The subscription carries the caller's bearer token, and a non-2xx subscription response logs its status.
- **`layer.config_changed` reached no subscriber** (§7.5.4): `podium sync --watch` subscribes to `artifact.published`, `artifact.deprecated`, and `layer.config_changed`, and nothing published the third. The layer endpoint recorded the §8.1 audit event on register, unregister, and reorder and stopped there, so an administrative layer change left every watcher serving a stale profile until an unrelated artifact event happened to wake it. Those operations publish the event, which reaches the `/v1/events` stream a watcher holds and any §7.3.2 outbound webhook receiver whose filter matches. A personal layer emits `layer.user_registered` and is excluded, because waking every watcher in the tenant for one caller's own composition resolves nothing.
- **Startup guard recommendation**: the guard that refuses a registry whose identity provider has no request-time verifier told the operator that only `injected-session-token` is verified server-side. `oidc-jwt` and `trusted-headers` have request-time verifiers, and the message pointed at the one provider that requires a runtime signing key before it verifies anything, so an operator fronting the registry with a gateway was sent to build a runtime-key onboarding flow instead of setting `trusted-headers`. The message is built from the boot path's own list of verified providers.
- **Hook subtype events materialized without a tool matcher** (§4.3.5): an artifact declaring a tool-category hook event such as `pre_shell_execution` materialized on a harness that emits only the generic tool event as a bare generic entry with no tool-name matcher, so its action ran ahead of every tool call rather than the category the author named. A formatter declared for file edits ran on shell and MCP calls, and the action succeeded each time, so nothing reported the misfire. The Claude Code, Codex, and Gemini adapters write the harness's own tool-name matcher beside the generic native event: `^Bash$` for shell, `^mcp__` or `^mcp_` for MCP, and on Claude Code `^Read$`, `^(Edit|Write|NotebookEdit)$`, and `^Task$` for the read, edit, and subagent categories. A generic event, a permission-category event, and `post_tool_use_failure` name no tool category and stay unnarrowed. Cursor emits the subtypes natively and is unchanged. Materialized output for an affected hook changes at the next `podium sync`.
- **Signature and body on an `extends:` child**: the merged record an `extends:` child was served carried the child's content hash beside the root parent's signature envelope, so verification compared a signature over one hash against a different hash and could never succeed. Materialization refuses a signed artifact whose signature fails, which made signing and `extends:` mutually exclusive with no way to author around it. The child's own signature is carried. The body was carried only where the child authored a non-empty one, so a child with no prose was served the root parent's, which reaches a requester who may hold no access to the parent's layer; the body is carried unconditionally, matching `manifest.MergeExtends` and the filesystem resolver.
- **Undeclared frontmatter keys through an `extends:` merge** (§4.6): both extends resolvers serialized the merged manifest by marshalling a closed struct, which dropped every key it does not declare, including the extension-type fields §4.6 names among the keys a child inherits. Serialization runs through one implementation that the server and filesystem resolvers both call, so the two deployment modes serve the same bytes for the same artifact. Restored keys are authored text, so a child whose `extends` reference arrives through a YAML merge key presents an operative `extends` value in the reassembled block and is refused with `registry.invalid_argument`, where the closed round-trip previously dropped that mapping and served a clean block. Comments on restored nodes are cleared before the block is served, because a comment on a parent's key can name the parent to a requester who cannot see the parent's layer.
- **Inherited `audit_redact` on the read event** (§8.2): the §8.2 emitter derived its redaction key set from the stored leaf record, so a child that inherits its parent's `audit_redact` directive carried no keys of its own and the directive named nothing. The emitter reads the record the caller was served, with the folded directive and the value source it names.
- **An `extends:` pin onto a deprecated parent** (§4.6, §4.7.6): a range or unpinned `extends:` reference filters deprecated versions out of the candidate set, so a re-ingest re-pins onto the live version rather than onto a parent deprecated after the child was first ingested. An exact or content-hash pin naming a deprecated version is refused with `ingest.invalid_artifact`, because the author named that version. A reference whose every stored candidate version is deprecated is refused with a message naming deprecation, so the report does not state that the parent was never published. A child that inherited `deprecated: true` was reported deprecated by `load_artifact` and reported live by `search_artifacts`, `podium sync`, and impact analysis, and closing the ingest path removes the state those readers disagreed on. The same change corrects `latest` in the resolver to the most recently ingested non-deprecated version, which is the definition §4.7.6 fixes and which the read path already used, where the resolver previously selected the highest semantic version and read no deprecation flag. An existing child is not repaired, because `(tenant, artifact_id, version)` is immutable, and republishing the child at a new version takes the corrected resolution.
- **Artifact description on the Frontmatter tab** (§13.10): the artifact viewer's header dropped its description paragraph when the Frontmatter tab was open and the manifest declared the field, so switching tabs made the sentence vanish and the page reflow around it. The header stands on every tab. The top bar's appearance control is drawn as its icon alone and carries an accessible name in place of the visible label.
- **Embedding projection drift**: ingest and `podium admin reembed` composed the embedding text differently for a record holding an empty `when_to_use` or `tags` entry. Both now use one implementation, which drops the empty entry.

Operators upgrading should note that a version already ingested keeps its unfolded `description`, `tags`, `sensitivity`, and `search_visibility`. Re-ingesting unchanged bytes is counted idempotent and skipped before the write, and `podium admin reembed` reads the stored columns, so neither repairs an existing row. The repair is publishing a new version of the child, which is ingested as a new row and takes the fold. A child of a `direct-only` parent stops being indexed once republished, which brings search into agreement with what layer resolution and `load_artifact` already report for that artifact.

### Documentation

- Corrected the specification, the HTTP API reference, and the operator runbook where they offered `oauth-device-code` as a registry-process identity provider. It is client-side acquisition, the registry ships no request-time verifier for it, and a registry configured with it refuses startup with `config.identity_provider_unverified`. The `registry.yaml` example in §13 selects `oidc-jwt` and names `issuer` rather than `authorization_endpoint`, the read-only write set in §13.2.1 drops the claim that the registry issues tokens against a local session table, and the web-UI paragraph stops presenting the device-code flow as an authentication mode of a standard deployment.

[Unreleased]: https://github.com/lennylabs/podium/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/lennylabs/podium/releases/tag/v0.4.0

## [0.3.1] - 2026-08-18

The documentation site is now built by a generator in this repository rather
than by Jekyll, and the documentation it publishes was corrected against the
code and the specification.

### Changed

- **Documentation site**: the published site is generated from the markdown under `docs/` by the package in `site/`, replacing the Jekyll and Just-the-Docs build. Frontmatter is a closed key set, and the build gate rejects an unresolvable link, a link to a missing anchor, an unknown code-fence language, an image with no alt text, a heading outline that skips a level, and a text token that misses the WCAG AA contrast ratio on the surface it renders on. Every page is complete HTML, and the browser bundle adds client navigation over it.
- **Changelog page**: the site publishes this file directly, through an `include:` key in the page's frontmatter, so the page follows the file the release process edits.

### Fixed

- **Evaluation stack startup**: `docker-compose.yml` selected the Postgres store and left the embedding configuration unset. A Postgres-backed registry defaults its embedding provider to `openai`, which then requires `OPENAI_API_KEY`, so the registry exited at boot with `missing required configuration for the selected backend(s): OPENAI_API_KEY`. The stack now sets `PODIUM_NO_EMBEDDINGS` and comes up with `docker compose up -d` alone, serving keyword search. Remove that variable and supply a provider with its key to exercise hybrid search.

### Documentation

- Corrected the documentation against the code across two audits, each finding independently confirmed before it was applied. Among them: the default `SignatureProvider` is `noop` rather than Sigstore-keyless; no notification provider is wired unless the operator names one; `registry.yaml` is read from `PODIUM_CONFIG_FILE` or `$HOME/.podium/`, never from `/etc/podium/`; model-versioned vector rows and the stale-row purge are a capability only the collocated backends implement; a resource exactly at the inline cutoff travels inline; and an `extends` pin takes `major.minor.patch` or `major.minor.x`, so the `@1.2` examples could not resolve.
- Resolved contradictions between pages in different sections: SCIM push, freeze windows, and the hash-chained audit log are available on a single node; `podium login` is a no-op only for a filesystem registry or a loopback default; `podium sync` performs no signature or content-hash verification; `mcp-server` is the built-in extension type rather than a first-class type; and public mode enforces a sensitivity floor at ingest.
- Redrew the `podium sync --watch` diagrams. Both described a per-event incremental pipeline that does not exist: the watcher reads only the event type from a newline-delimited JSON stream, holds one debounce timer, and reruns the whole sync.
- Added the mobile layout for the site, at the breakpoints the design defines.

[0.3.1]: https://github.com/lennylabs/podium/releases/tag/v0.3.1

## [0.3.0] - 2026-08-15

AD FS compatibility for the `oidc-jwt` identity provider: the registry accepts the discovery document's `access_token_issuer` as a second token issuer, reads the subject and group claims under operator-named claim names, and reads a group claim emitted as a single string.

### Added

- **Second accepted token issuer under `oidc-jwt`** (§6.3.3): the registry accepts a forwarded token whose `iss` matches the configured `identity_provider.issuer` or the `access_token_issuer` the same discovery document publishes. The second value is read once, when that document resolves, and it is compared as a string and never dereferenced, so the signing keys still come from the `jwks_uri` in the configured issuer's `https` document. A document that publishes no `access_token_issuer` leaves the configured issuer as the sole accepted value. When the two values differ, the startup log names both. AD FS is the deployment this rule covers: it serves discovery under `https://<host>/adfs` and stamps the federation-service identifier `http://<host>/adfs/services/trust` on the access token.
- **`PODIUM_OAUTH_SUBJECT_CLAIM` and `PODIUM_OAUTH_GROUPS_CLAIM`** (§6.3.3, §13.12): the config-file keys `identity_provider.subject_claim` and `identity_provider.groups_claim` name the claim the registry reads as the caller's subject and the claim it reads for group membership. When `subject_claim` is set the registry reads that claim alone and rejects a token that does not carry it with `auth.untrusted_token`, with no fallback to `sub`. Both settings are unset by default and are read only under `oidc-jwt`. The recorded subject keys `users:` layer visibility, user-defined layer ownership, per-tenant admin grants, and the instance-operator grant, so a deployment that sets `subject_claim` lists values of the named claim in `PODIUM_OPERATOR_ADMINS` and `PODIUM_BOOTSTRAP_ADMINS`.

### Changed

- **Group-claim encoding** (§6.3.1): the `oidc-jwt` and `injected-session-token` verifiers both read a group claim in the multi-value form and in the single-string form an IdP emits for a caller in exactly one group. The single-string form yields one group whose name is the whole claim value, and it is not split on any separator. A deployment whose IdP emits that form previously resolved to an empty group list without an authentication error, so group-scoped layers that were invisible to such a caller become visible on upgrade.

### Fixed

- **Keychain entries larger than the backend limit**: the macOS keychain backend rejects a payload over roughly 3000 bytes, and an AD FS refresh token runs to about 4.7 KB, so `podium login` failed at the save step. The credential store now splits a token larger than 2500 bytes across numbered entries and records the chunk count in a marker under the bare label, reassembles the chunks on load, and clears the previous save's entries before a re-save so a later `podium logout` removes the whole token.
- **`owner` in the Claude and Cursor marketplace manifests** (§7.8): the root `marketplace.json` carried only `name` and `plugins`, and the Claude schema requires `owner.name`, so Claude Desktop refused to import a Podium-published marketplace repository. The Claude and Cursor manifests now emit `owner.name` set to the marketplace name. The Codex manifest, whose format documents no `owner`, is byte-identical to the released output.

[0.3.0]: https://github.com/lennylabs/podium/releases/tag/v0.3.0

## [0.2.1] - 2026-06-30

Webhook hardening: the outbound webhook receiver endpoints are admin-gated, receiver URLs are validated against an SSRF policy, and a receiver can coalesce a burst of events into one batched delivery.

### Added

- **Receiver SSRF policy** (§7.3.2): the registry validates a webhook receiver URL at registration and re-checks it at delivery. By default it requires the `https` scheme and rejects a URL that resolves to a loopback, link-local, or private address, and it does not follow a redirect to such a target. `PODIUM_WEBHOOK_ALLOWED_TARGETS` (§13.12) allowlists hosts or CIDRs for a legitimately-internal receiver and overrides the address rejection, not the `https` requirement. A rejected target returns `registry.invalid_argument` naming the disallowed host.
- **Per-receiver debounce window** (§7.3.2): a receiver with `debounce` set coalesces the events it matches in a trailing window into one batch delivery, deduplicated by event type and key, sent with the same retry, backoff, concurrency limit, and HMAC signing as a single-event delivery. The batch envelope is additive; the single-event body is unchanged for a receiver without a window.

### Changed

- **Receiver authorization** (§7.3.2): the `/v1/webhooks` receiver CRUD endpoints (`GET`, `POST`, `PUT`, and `DELETE`) require the per-tenant admin role and return `auth.forbidden` for a non-admin caller, alongside the existing read-only rejection for the mutating methods. This closes the gap where any authenticated caller, or an unauthenticated standalone bind, could register a receiver and point the registry at an internal endpoint.

[0.2.1]: https://github.com/lennylabs/podium/releases/tag/v0.2.1

## [0.2.0] - 2026-06-29

Marketplace publishing: a `podium sync` target of `kind: marketplace` renders the catalog into a harness-native git-repo marketplace and runs an operator-configured workflow to push it to a remote.

### Added

- **Marketplace publishing** (§7.5.2, §7.8): a `podium sync` target of `kind: marketplace` renders the effective view into a harness-native git-repo distribution and runs an operator-configured `workflow` of shell commands to clone, commit, and push it to a git remote. One repository carries the Claude (Code, Desktop, Cowork), Codex, and Cursor plugin-marketplace manifests at their fixed locations, while Gemini (extension), Pi (package), and Hermes (tap) take their own repository. The `plugins:` list groups artifacts by scope filter, and the publishing `identity:` governs the visibility-filtered effective view that reaches the marketplace. Podium renders to a folder and never holds a git push credential. `podium sync --config` runs the prepare, render, and publish pipeline per target, and `--check` and `--dry-run` write nothing.
- The `HarnessAdapter` `Source` carries a plugin descriptor, so an adapter can render an artifact into a named plugin (§6.7, §9.1).

### Changed

- **`claude-cowork` is publish-only** (§6.7, §6.7.1): the cowork adapter no longer materializes the plugin-layout artifact types (skill, agent, command, rule, hook, and mcp-server) through `podium sync`; they reach Claude Cowork through a `kind: marketplace` marketplace instead. A `type: context` artifact still materializes to `.podium/context/` under `podium sync`. The §6.7.1 capability cells for `claude-cowork` are regraded to unsupported for the affected rows.
- `podium sync` enforces the §6.9 untranslatable rule: a target whose harness cannot represent a selected artifact fails rather than silently skipping it, matching `load_artifact`.

### Documentation

- Added a marketplace-publishing guide and a publish-flow diagram, and reframed the harness, CLI, and error-code references onto the `kind: marketplace` sync target.

[0.2.0]: https://github.com/lennylabs/podium/releases/tag/v0.2.0

## [0.1.6] - 2026-06-17

The `podium-mcp` stdio server returns a spec-compliant MCP `CallToolResult`, so hosts that render `result.content` show meta-tool output instead of an empty result.

### Fixed

- **MCP `tools/call` result format** (§6.1.1): `podium-mcp` returns each meta-tool result as an MCP `CallToolResult`. The domain object is carried in `structuredContent` and mirrored as a `content[]` text block, and a §6.10 error envelope sets `isError`. Hosts that render `result.content` (Claude Code, Claude Desktop, Cursor, and VS Code) previously received no content and showed an empty result for `search_artifacts`; they now display the output. The meta-tool fields move from the result top level to `structuredContent`.

### Documentation

- Documented the `tools/call` result format in §6.1.1, and corrected the §5 `load_artifact` description so it states materialization writes the adapted body as a harness-native file in addition to any bundled resources.

[0.1.6]: https://github.com/lennylabs/podium/releases/tag/v0.1.6

## [0.1.5] - 2026-06-10

A standalone server pointed at a filesystem registry now honors per-layer `.layer-config` visibility at boot, instead of stamping one deployment default on every layer.

### Fixed

- **Standalone bootstrap** (§4.6, §13.11.1): a `PODIUM_LAYER_PATH` filesystem registry served by a standalone server applies each layer's declared `.layer-config` visibility. A layer that declares a non-empty visibility boots with it; a layer with no `.layer-config`, or one whose `visibility:` block is empty, falls back to the deployment default (`PODIUM_DEFAULT_LAYER_VISIBILITY`), matching how a declarative `layers:` entry resolves an empty block.

### Documentation

- Documented the optional per-layer `.layer-config` file and its `visibility:` schema in the filesystem-registry directory layout (§13.11.1) and the solo/filesystem deployment guide.

[0.1.5]: https://github.com/lennylabs/podium/releases/tag/v0.1.5

## [0.1.4] - 2026-06-08

Multi-tenancy and gateway-delegated authentication. Two design proposals land: server-side request authentication for a registry behind an identity-aware gateway (proposal 0001), and runtime tenant provisioning through an operator-authorized API and CLI (proposal 0002). The boot-time `PODIUM_TENANTS` environment variable is replaced by the runtime provisioning path.

### Added

- **Server-side request authentication** (§6.3.3, proposal 0001): the `oidc-jwt` and `trusted-headers` identity providers authenticate each caller from a gateway-forwarded token or trusted request headers, selected by `PODIUM_IDENTITY_PROVIDER`. The caller's organization comes from the verified `org_id` claim or the `X-Podium-User-Org` header.
- **Per-request multi-tenant routing** (§6.3.1): a registry started with `PODIUM_MULTI_TENANT` resolves each request to the tenant its organization names, and rejects an organization that names no provisioned tenant with `auth.tenant_unknown`. A single-tenant registry binds every request to its sole tenant and does not consult the organization value.
- **Runtime tenant provisioning** (§7.3.3, proposal 0002): the operator-authorized `/v1/admin/tenants` API and the `podium admin tenant` CLI create, list, update, and deactivate tenants on a live multi-tenant registry. The instance-operator role is seeded with `PODIUM_OPERATOR_ADMINS` and is distinct from the per-tenant `admin` role. Per-tenant quotas and the §3.5 scope-preview gate are set at create or update, and create is idempotent. Deactivation is soft: a deactivated tenant stops resolving while its data persists, and reactivation restores it.

### Changed

- `podium domain analyze` takes the path as a positional argument (`podium domain analyze <path>`), matching `podium domain show` and `podium domain search`.

### Removed

- The boot-time `PODIUM_TENANTS` environment variable and the boot-time tenant-provisioning path. A multi-tenant deployment seeds its first operator with `PODIUM_OPERATOR_ADMINS` and provisions tenants at runtime through the API or CLI.
- The `lint.hook_generic_and_subtype` lint rule, which rejected a hook that declared both a generic tool-call event and a subtype event. The rule could not be enforced across independently authored layers, and declaring both events is a legitimate pattern.

### Fixed

- **SDKs** (§7.2): `load_artifact` content above the 256 KB inline cutoff on a single load is fetched from the presigned manifest-body URL instead of failing (`podium-py`, `podium-ts`).
- **Store** (§4.7.1): `Memory.CreateTenant` is idempotent, matching the SQLite and Postgres backends, so re-creating an existing tenant leaves the stored row unchanged.
- **Registry**: graceful shutdown runs through a single server lifecycle context.

### Documentation

- Clarified what `load_artifact` returns inline versus what materializes to disk, for the MCP server and the SDKs (§6.6, §6.7).
- Corrected the CLI, HTTP API, error-code, and authoring references against the implementation.

[0.1.4]: https://github.com/lennylabs/podium/releases/tag/v0.1.4

## [0.1.3] - 2026-06-04

Spec-conformance and reliability release. The bulk of the work reconciles the implementation with the specification across the registry, CLI, MCP bridge, and SDKs, and builds out the test infrastructure that verifies it (live integration lanes for Postgres, S3, and the managed vector backends; spec, doc, and matrix coverage gates; and a hand- and agent-runnable end-to-end validation suite). The user-facing changes are grouped below by area; the internal test and CI work is omitted.

### Added

- **Managed vector backends**: Pinecone, Weaviate Cloud, and Qdrant Cloud, alongside the existing `sqlite-vec` and `pgvector`, with both externally-computed embeddings and backend-side integrated inference.
- **Observability** (§13.8): an opt-in Prometheus `/metrics` endpoint on the registry and the MCP bridge, and OpenTelemetry trace export with W3C context propagation.
- **Per-tenant daily audit-volume quota** (§4.7.8) and **reverse-dependency in-degree ranking** in search (§4.7.3).
- **Transactional vector outbox** with a drain worker, and **per-row embedding-model versioning** with a mixed-model query restriction (§4.7, §4.7.2).
- **Consumer-side `verify_signatures` default** read from `sync.yaml` for standalone deployments (§13.10), and **config-merge / managed-marker materialization ops** (§6.7).

### Changed

- `podium status` and `podium config show` resolve the registry and harness from the merged `sync.yaml` (the flag, then the environment, then the config), not only from environment variables; `config show` hints when no configuration is in scope and surfaces effective server settings under `--server`.
- The MCP bridge negotiates down to an older MCP protocol version, rejects a filesystem-source registry, and refuses an incompatible client version (§6.1, §6.9).

### Fixed

- **Artifact model, ingest, and lint** (§4.1–§4.4): the type system and sizing lint, canonical IDs and the resource boundary, manifest schema parsing, skill and hook ingest lint, prose artifact-reference resolution, document-source provenance, URL status checks, the seccomp baseline, DOMAIN.md body-size lint, and configurable bundled-resource caps; binary inline resources are base64-encoded and served without an object store.
- **Domains** (§4.5): `DOMAIN.md` composition is ingested and applied at `load_domain`, with discovery rendering, tenant config, and imports.
- **Layers, visibility, and versioning** (§4.6, §4.7): extends-merge / collision / visibility composition, the per-identity user-defined layer cap, runtime layer resolution, embedding projection and version resolution, `replaced_by` recovery on load for the SQL backends, and extends-pinned-parent protection from deprecated-version purge. A same-ID `extends` overlay from a lower-precedence layer is no longer rejected as a self-extends cycle.
- **Meta-tools and MCP bridge** (§5, §6): verbatim §5.1 tool descriptions and input schemas, the §6.6 materialization pipeline (content-hash verification, hook script path, rule fidelity), the §6.5 resolution cache (TTL, HEAD revalidation, prune safety), the §6.4 workspace overlay (watch / re-index, fused `total_matched`), per-harness materialization targets (§6.7 — codex hooks into `config.toml`, cowork buckets, config-merge ownership so gemini accepts `mcpServers`), the §6.2 server config env vars, and the §6.10 structured error envelope. The content cache now persists `skill_raw` and the sensitivity/signature envelope, fixing a `content_hash_mismatch` and a skipped signature check on cache hits. `search_artifacts` `total_matched` counts vector-only hits, and the hybrid BM25 half indexes only the §4.7 searchable projection (name, description, when_to_use, tags) with stopword filtering, so a paraphrased query ranks by vector similarity.
- **External integration and sync** (§7): §7.2 bundled-resource delivery and the presigned manifest-body channel above the inline cutoff, §7.3 inbound webhook and reingest pipeline (`last_ingested_at`, `force_push_policy`, break-glass, webhook-secret rotation and redaction), §7.4 degraded-network cache-mode fallback across the bridge / sync / SDKs, §7.5.2 sync honoring `PODIUM_HARNESS` with profile / scope and lock provenance, §7.6 read CLI and SDK `--json` schemas and caller-credential propagation, and §7.7 onboarding (`init` walk-up / wizard / hints, login resolution). `cache prune --days 0` is accepted as the "older than now" boundary.
- **Identity and scope preview** (§6.3, §3.5): injected-session-token verification, device-code, scope and group mapping, `aud` enforcement, and token watch; scope-preview endpoint correctness and the tenant gate, surfaced in `status` / `sync` / MCP.
- **Audit and observability** (§8, §12, §13.7, §13.9): registry audit events under dotted `caller.*` keys, §8.2 PII redaction, §8.4 sampling / retention / re-anchor, §8.5 right-to-be-forgotten erasure (purge, redaction, tombstone, salt guard), §8.6 gap-detection scheduling, immutable `Cache-Control` on content-addressed reads, §13.9 health and readiness probes, and §12 offline status / ETag revalidation / learn-from-usage rerank.
- **Deployment and config** (§13, §14): the §13.1.1 evaluation compose stack (registry, Dex, bootstrap-admin seeding), §13.2 read-only write rejection / public-mode bind guard / sensitivity ceiling / read-only probe and recovery, §13.4 `migrate-to-standard` short-form flags and standalone-tenant resolution, §13.10 standalone zero-flag and first-run `~/.podium/sync.yaml` auto-bootstrap, §13.11 fsnotify watch and filesystem `extends`, and §14.9 / §14.10 enterprise-layer register-class inference and `layer watch --interval`.
- **Retrieval and SPIs** (§3.2, §3.3, §9): hybrid domain search with vector-only fusion, description-quality advisories with MCP session correlation, the §9.1 operational notification on ingest failure, context-first SPI signatures, and a structured SPI error envelope.

### Security

- The `/objects/{content_hash}` data-plane route was exempt from identity verification and served restricted bytes to any caller. Visibility is now enforced on that route, and S3 presigned URLs no longer embed credentials.

[0.1.3]: https://github.com/lennylabs/podium/releases/tag/v0.1.3

## [0.1.2] - 2026-05-11

Distribution-channel additions. No changes to the CLI surface; the binaries themselves are bit-identical to v0.1.1 (modulo the embedded version string).

### Added

- **Per-platform archives** alongside the individual binaries on each GitHub Release: `podium-<os>-<arch>.tar.gz` (Linux + macOS) and `podium-windows-amd64.zip`, each containing `podium`, `podium-server`, and `podium-mcp` with their canonical un-suffixed names. The individual binaries are still attached; the archives are additive for package-manager consumers.
- **Homebrew tap and Scoop bucket update job** in `release.yml`. On each tag push, the workflow patches `Formula/podium.rb` in `lennylabs/homebrew-tap` and `bucket/podium.json` in `lennylabs/scoop-bucket` so `brew install podium` / `scoop install podium` track the latest release. Both auxiliary repos are org-wide — one repo per package manager, one file per Lenny Labs project.
- **`TAP_BUCKET_TOKEN`** repo secret requirement, documented in OPERATIONS.md.

[0.1.2]: https://github.com/lennylabs/podium/releases/tag/v0.1.2

## [0.1.1] - 2026-05-11

Release-pipeline fixes. The v0.1.0 tag was created but never produced
published artifacts (PyPI, npm, GHCR) because of a sequence of CI
configuration failures; v0.1.1 is the first version where the release
workflow runs end-to-end. The behavior of the code itself is unchanged
from what v0.1.0 was supposed to ship — see the [0.1.0] section below
for the feature list.

### Release-pipeline fixes since v0.1.0

- Container builder switched from alpine/musl to debian/glibc;
  sqlite-vec.c uses BSD type names that musl doesn't provide.
- Cross-compile matrix now uses CGO_ENABLED=1 with per-target
  toolchains (gcc on linux/amd64, gcc-aarch64-linux-gnu on
  linux/arm64, mingw on windows/amd64, native clang on darwin/arm64).
- Windows binary build moved to a windows-latest runner with a
  workflow step that fetches sqlite3.h from the SQLite amalgamation.
- npm package gains `repository` / `homepage` / `bugs` / `keywords`
  fields so npm provenance verification accepts the publish.
- Postgres schema gained the `signature` column that the store
  queries already referenced.
- MinIO service swapped from the now-vanished bitnami tag to the
  official `minio/minio` image, with bucket creation via `mc mb` in
  a workflow step.
- A flaky scheduler test that raced with `t.TempDir` cleanup now
  waits for the goroutine to finish on cancel.

[0.1.1]: https://github.com/lennylabs/podium/releases/tag/v0.1.1

## [0.1.0] - 2026-05-11

Initial release. Covers the full v1 surface described in the project specification, across three binaries (`podium`, `podium-server`, `podium-mcp`) and two SDKs (`podium-sdk` on PyPI, `@lennylabs/podium-sdk` on npm).

### What's included

- **Filesystem mode**: `podium sync` materializes an effective view from a local artifact directory through the configured `HarnessAdapter`. Built-in adapters: `none`, `claude-code`, `claude-desktop`, `claude-cowork`, `cursor`, `codex`, `gemini`, `opencode`, `pi`, `hermes`.
- **Server mode**: `podium serve` runs the registry HTTP API. Standalone bootstrap uses embedded SQLite + `sqlite-vec`; standard deployment wires Postgres + `pgvector` + S3-compatible object storage + an OIDC identity provider.
- **`LayerComposer`** with visibility filtering across `public` / `organization` / OIDC `groups` / explicit `users`.
- **Domain composition**: `DOMAIN.md` parsing, glob resolution, cross-layer merge, `extends:` resolution, discovery rendering.
- **Versioning and immutability**: semver, content-hash cache keys, `latest` resolution with `session_id` consistency, tolerant force-push handling.
- **Workspace overlay** with local BM25 search alongside the registry's hybrid retrieval.
- **MCP server**: `podium-mcp` exposes `search_artifacts`, `load_artifact`, `search_domains`, `load_domain` with materialization through the configured adapter.
- **Identity**: OAuth device-code flow with OS keychain storage; injected-session-token flow for service runtimes.
- **SCIM 2.0** + OIDC group claim mapping.
- **Audit log** with hash-chain integrity, retention policies, and GDPR right-to-be-forgotten.
- **Signing**: Sigstore keyless by default; pluggable `SignatureProvider`.
- **Dependency graph**: cross-type reverse index + impact analysis CLI.
- **SDKs**: `podium-sdk` (Python) and `@lennylabs/podium-sdk` (TypeScript) as thin HTTP clients.
- **Plugin surface**: every SPI documented in `docs/deployment/extending.md`, including `LayerSourceProvider`, `GitProvider`, `IdentityProvider`, `HarnessAdapter`, `MaterializationHook`, `SignatureProvider`, `NotificationProvider`, plus search and embedding providers.

[0.1.0]: https://github.com/lennylabs/podium/releases/tag/v0.1.0
