---
title: Upgrade notes
nav_order: 5
description: "Release-specific upgrade steps, newest release first: the 0.8.0 Git snapshot deadline and bounded Git fetch, ingest verification of author attestations with several requirements, required types, a tenant trust floor, and key-based signers, the refusal of a workspace .podium symbolic link, the file hashes in the sync lock, the refusal of an empty or control-character visibility entry, the refusal of names that resolve to .git or .podium, the per-artifact refusal of a skill whose SKILL.md is missing or does not parse, the unreadable files of an unparseable package, an ARTIFACT.md below a dot-prefixed directory, symbolic-link layer paths, the removal of mcp-server artifacts from the MCP server, the refusal of an unrecognized identity provider value, the refusal of a start without the identity provider a registry ran with, the Sigstore keyless signature as a Sigstore bundle, and the package digest of stored versions, the 0.7.0 layer credential columns, skill metadata fields in stored rows, catalog lint advisories and listing budgets, per-artifact manifest refusal and skill lint severities, the semantic overlap check, and command and context removal, the 0.6.0 extends removal, and the 0.5.0 rewrite of stored content hashes."
---

# Upgrade notes

This page lists the upgrade steps that belong to one release, newest release first. [Upgrading](upgrading) gives the general procedure that every upgrade follows. Each release's entry in `CHANGELOG.md` is the authoritative statement of its changes.

## 0.8.0 (unreleased)

The current release is 0.7.0. The notes in this section describe behavior on `main` that ships in 0.8.0.

### Attestation check in podium-mcp

From 0.8.0, `podium-mcp` checks that every record it loads answers the request, and, under an [attestation policy](../../consuming/attestation-policy), that a trusted author signed it.

- The request binding and the well-formed rule run with no policy. A registry that follows the spec answers no request with a record of another ID or another pinned version, so these refusals (`materialize.request_mismatch`) appear only for a misbehaving registry or for a requested ID such as `GIT~1` that is not well formed.
- `podium-mcp` reads `~/.podium/sync.yaml` from the home directory that the account database records for the user, and no longer from `HOME`. A deployment that pointed `HOME` at another directory to give `podium-mcp` a `defaults.registry` moves that file to the account home, or sets `PODIUM_REGISTRY`. A container that runs under a user ID with no entry in `/etc/passwd` reads no user file, and each load result carries `config.attestation_policy_unavailable (constraint user_home)` in `attestation_warning`.
- `podium-mcp` reads `/etc/podium/sync.yaml`, or `podium\sync.yaml` under `ProgramData` on Windows, when root or an administrator owns it and no other user can write it. A file that fails that check is reported in every load result.
- A `~/.podium/sync.yaml` that does not parse now refuses the start of `podium-mcp` with `config.attestation_policy_unavailable`, also for a configuration that sets no attestation key, because the policy it may hold is unknown. So does a `.podium/sync.yaml` or `sync.local.yaml` in the working directory of `podium-mcp` that the user owns and that does not parse, or whose `defaults.attestation` block or policy file does not load. 0.7.0 ignored such files. A workspace file of an ancestor directory, a linked file, or one another user owns that fails these checks is ignored and reported as `config.attestation_policy_unavailable (constraint workspace_file)` in each load result. Repair or remove the file before the upgrade.
- `~/.podium/sync.yaml` and the machine file refuse a top-level key other than `defaults`, `listing_budgets`, `profiles`, and `targets`, and an unknown key under `defaults`, so a misspelled or misplaced `attestation` block is not dropped. Remove keys the schema does not name before the upgrade.
- The bundle fetch has a deadline of 30 seconds, which `PODIUM_ATTESTATION_FETCH_TIMEOUT` changes. `PODIUM_SIGSTORE_ROOT_REFRESH` of `podium-mcp` is at least `1m` and at least `PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT`.
- On Linux, `podium-mcp` takes the account home from `/etc/passwd` (the release binaries are built with the `osusergo` tag). An account that exists only in LDAP, SSSD, or another directory service gets the `user_home` notice in each load result, and `podium-mcp` reads none of its `~/.podium/sync.yaml`, including `defaults.registry`. It does not fall back to `HOME`. On such a host, move the protective policy to the machine file `/etc/podium/sync.yaml`, and set `PODIUM_REGISTRY` or `--registry` in the harness's server entry before the upgrade.
- Cached records from 0.7.0 carry no `attestations` member. Under a policy above `none` the first load of each is a cache miss and is fetched again; under `offline-only` it is refused until it is loaded online.
- A rollback to 0.7.0 needs no step. 0.7.0 ignores `defaults.attestation`, the bundle cache under `${PODIUM_CACHE_DIR}/attestations/`, and the root copy under `~/.podium/sigstore/public-good/`.

### Git snapshot deadline

From 0.8.0, every ingest of a `git` layer clones under the deadline that `PODIUM_GIT_FETCH_TIMEOUT` or the `registry.yaml` key `git_fetch_timeout` sets, 10 minutes by default. 0.7.0 set no deadline.

- A clone that takes longer than the deadline fails that ingest with `ingest.source_unreachable`, and the message names the setting. The layer keeps serving the artifacts of its last completed ingest.
- The deadline covers the clone and every deeper fetch of one ingest (*Bounded Git fetch* below). The longest ingest of a layer is one that fetches the complete history: after a force push, or when the last ingested commit lies far behind the tip. Before the upgrade, compare the full clone time of the largest `git` layer with the default, and set `PODIUM_GIT_FETCH_TIMEOUT` above it where it is longer.
- A value that is not a duration above zero, such as `0s` or `600`, refuses startup with `config.invalid`.
- A rollback to 0.7.0 needs no step. 0.7.0 does not read the variable and ignores the key.

### Sigstore trusted roots for author attestation

From 0.8.0, the registry loads the Sigstore instances and trusted roots of author attestation verification at its start (spec §4.7.11), and ingest verification uses them. [Configuration](../../reference/configuration#author-attestation) lists the settings.

- The refresh of the Sigstore public-good trusted root, a request to `https://tuf-repo-cdn.sigstore.dev` every 24 hours that writes `sigstore/public-good/` beside the SQLite store file, runs only on a registry that verifies attestations on the public-good instance: one with a layer whose policy resolves to `warn` or `require` and names a signer of that instance. A registry with no such layer makes no such request and creates no such directory. An air-gapped registry sets `PODIUM_ATTESTATION_TRUSTED_ROOT_FILE` to a trusted root file, which turns the refresh off, and a registry deployed with the Helm chart, whose root filesystem is read-only, points `PODIUM_SQLITE_PATH` at a mounted writable volume to keep the root copy across restarts.
- A start refuses with `config.invalid` when `PODIUM_ATTESTATION_TRUSTED_ROOT_FILE`, `PODIUM_SIGSTORE_ROOT_REFRESH`, `PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT`, `PODIUM_SIGSTORE_ROOT_MAX_AGE`, or a `sigstore.instances` entry of `registry.yaml` holds a value the registry cannot use, such as a trusted root file that cannot be read or a refresh interval below `1m`. 0.7.0 read none of them. Before the upgrade, check that any of them a deployment already sets holds a valid value.
- `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` keeps its meaning for `podium sign` and `podium verify`. The registry does not read it for attestations.
- A rollback to 0.7.0 needs no step. 0.7.0 ignores the settings and the `sigstore/` directory, which can be deleted.

### Layer trust policy

From 0.8.0, a layer carries a trust policy: `trust_mode`, `trust_requirements`, and `trust_layer_root` (spec §4.7.11). The registry stores and validates it, and ingest verifies attestations against it, as [Ingest verification of author attestations](#ingest-verification-of-author-attestations) describes. [Layer trust policy](../content/layers#layer-trust-policy) describes it.

- The SQLite and Postgres stores add the columns `trust_mode`, `trust_requirements`, and `trust_layer_root` to `layer_configs` at the first start, and every existing layer reads with an empty policy. No step is needed.
- With `PODIUM_LAYER_TRUST_DEFAULT` at `warn` or `require`, the layer endpoints refuse a registration, and an update that touches the trust policy, that leaves a layer with no `trust_mode` and no requirement. A deployment that sets the default registers each new layer with a policy or with `--trust none`.
- A start refuses with `config.invalid_layer_trust` when `PODIUM_LAYER_TRUST_DEFAULT` holds a value other than `none`, `warn`, or `require`, or when a layer declared in `registry.yaml` carries a trust policy that breaks a rule. 0.7.0 read neither, and it ignored the `trust_mode`, `trust_requirements`, and `trust_layer_root` keys of a declaration.
- A rollback to 0.7.0 starts on the migrated store, and 0.7.0 does not read the columns. On SQLite, a layer write under 0.7.0, such as an update, a reorder, or the re-seed of a declared or `--layer-path` layer at each start, replaces the row and clears its policy. On Postgres the columns keep their values. After a rollback and a return to 0.8.0, check each policy with `podium layer show <id>`.

### Ingest verification of author attestations

From 0.8.0, every ingest cycle of a layer whose trust mode resolves to `warn` or `require` verifies the layer's attestation bundles before it writes anything, and writes the layer's attestation record (spec §4.7.11). [Ingest verification](../content/layers#ingest-verification) describes the checks and the record.

- Set a layer's `trust_mode` to `warn` or `require`, and `PODIUM_LAYER_TRUST_DEFAULT` to either, only after every replica that shares the store runs 0.8.0. A 0.7.0 replica ignores the policy: its cycles of the layer run no attestation step, store unsigned content, and write no attestation record, so a rolling upgrade with mixed replicas accepts what 0.8.0 replicas refuse.
- A layer stored by 0.7.0 has an empty trust policy, which resolves to `PODIUM_LAYER_TRUST_DEFAULT`, `none` by default, so its cycles are unchanged. A deployment that sets the default to `warn` or `require`, or gives a layer such a policy, signs the layer first: under `require`, a cycle with no accepted bundle is refused with `ingest.attestation_missing`, and the versions already stored keep serving. Set `warn` while the signing workflow is set up, and read `podium layer show <id>` for the status and the next step.
- A start whose `PODIUM_LAYER_TRUST_DEFAULT` is `warn` or `require` and that bootstraps layers with `--layer-path` or `PODIUM_LAYER_PATH` refuses to start with `config.invalid_layer_trust` unless `PODIUM_LAYER_TRUST_REQUIREMENTS_FILE` names a requirements file, or each bootstrap layer stores a requirement.
- A startup ingest that an attestation step refuses no longer aborts the start. The start log names the layer and the code, and the layer's attestation record holds the refusal. Every other failure of a startup ingest still aborts it.
- An ingest whose package walk fails, such as one that meets an unreadable directory of a `local` layer, now writes no `DOMAIN.md` record before it fails. 0.7.0 wrote the domain records first.
- `PODIUM_ATTESTATION_MAX_BYTES` and `PODIUM_ATTESTATION_MAX_BUNDLES` are read at each start, and a value that is not an integer of at least 1 refuses the start with `config.invalid`.
- The SQLite and Postgres stores add the attestation record columns to `layer_configs` at the first start. No step is needed.
- A rollback to 0.7.0 starts on the migrated store and does not read the columns. On SQLite, 0.7.0 replaces a layer row on each layer write, such as the re-seed of a declared or `--layer-path` layer at each start, which clears the attestation mark and the recorded commit. After a return to 0.8.0, the first cycle of such a layer accepts any signed state, as a first ingest does. To restore the protection against a replayed older state, reset the mark to the current signed commit with `podium layer reingest <id> --reset-attestation-mark --to-commit <sha>`. On Postgres the columns keep their values.

### Several requirements and required types

From 0.8.0, a layer trust policy holds several requirements, an optional requirement, and the `trust_require_types` member (spec §4.7.11). [Several requirements](../content/layers#several-requirements) describes them.

- The SQLite and Postgres stores add the column `trust_require_types` to `layer_configs` at the first start, and every existing layer reads with no listed type. No step is needed.
- A requirement stores its `covers_layer` and `optional` members in the `trust_requirements` column, so no other migration applies.
- `podium layer update --trust-signer` on a layer whose policy holds several requirements exits 1 and sends nothing, because the flags would replace every requirement with one. Write such a policy with `--trust-requirements-file`.
- A rollback to 0.7.0 needs no step beyond the one of *Layer trust policy* above. 0.7.0 does not read the column.

### Tenant trust floor

From 0.8.0, a tenant carries a trust floor that every layer trust policy of the tenant meets (spec §4.7.11). [Tenant trust floor](../content/layers#tenant-trust-floor) describes it.

- The SQLite and Postgres stores add the columns `trust_floor_mode`, `trust_floor_require_types`, and `trust_floor_signers` to the tenants table (on Postgres, `public.tenants`) at the first start, and every existing tenant reads with no floor. No step is needed.
- A single-tenant registry reads `PODIUM_LAYER_TRUST_FLOOR_FILE` and the `registry.yaml` key `trust_floor` at each start. A value the registry cannot use refuses the start with `config.invalid_layer_trust`, and so does either setting on a multi-tenant registry. 0.7.0 read neither.
- A floor applies to the layers that exist when it is set. A layer whose policy it makes invalid keeps serving its stored versions, its `verified` versions are reported as `failed` with `ingest.attestation_policy_invalid`, and its ingest cycles are refused until its policy meets the floor. Before a floor is raised, review the layers the start log or the `invalid_layers` member of the floor update names, and update their policies.
- Raise a floor only after every replica that shares the store runs 0.8.0. A 0.7.0 replica does not read the floor and ingests a layer below it.
- A rollback to 0.7.0 leaves the columns in place and does not read them; 0.7.0 ignores the setting. A floor that the tenant endpoints set is kept for a return to 0.8.0.

### Key-based signers

From 0.8.0, a trust policy signer can name an ECDSA P-256 `public_key` in place of an issuer and an identity, and the registry verifies a key-signed bundle under it with no certificate, log entry, or timestamp (spec §4.7.11). [Key-based signers](../content/layers#key-based-signers) describes the policy and the signing recipe.

- The stored trust policy keeps the key in the existing `trust_requirements` column, so the stores need no migration step.
- The registry reads `PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE` at its start, `5m` by default. A value that is not a duration from `0s` to `24h` refuses the start with `config.invalid`. 0.7.0 does not read it.
- A key-signed layer subject must list the signed-time subject that `podium attest --signed-time` writes, and the registry refuses one whose signed time is later than its clock plus the allowance (`ingest.attestation_stale`, `signed_time_ahead`). Keep the clocks of the signing machines and of every replica synchronized.
- Register a key-based signer only after every replica that shares the store runs 0.8.0, because a 0.7.0 replica ingests without attestation steps.
- A rollback to 0.7.0 follows the steps of *Layer trust policy* above, and the key needs no step of its own.

### Attestation links and outcomes

From 0.8.0, the registry stores the attestation bundles it accepts, links them to the versions they cover, keeps an attestation outcome for each version, and serves both in `load_artifact` and in each `ok` bulk-load item (spec §4.7.11, §7.2). [Attestation links and outcomes](../content/layers#attestation-links-and-outcomes) describes them.

- The SQLite and Postgres stores add the `attestations` and `artifact_attestations` tables, the `attestation_roots` table (on Postgres, in the `public` schema), and the `attestation_outcome`, `attestation_failure`, and `attestation_evaluated_at` columns of `manifests` at the first start. No step is needed. Every version stored before the upgrade reads as `not_evaluated`, which `load_artifact` reports by omitting `attestation_outcome`, until a cycle of its layer under `warn` or `require` evaluates it.
- A version that a 0.7.0 replica stores during a rolling upgrade is `not_evaluated` and has no link until a 0.8.0 cycle finds it unchanged and evaluates it.
- A layer under `none` whose snapshot holds `.podium/attestations/` now gets `unverified` links to the bundles that verify under a configured instance, and those bundles count toward the tenant's storage quota.
- `podium layer update` and a re-registration that change a layer's trust policy now re-evaluate the layer's links before they answer. A link whose bundle the new policy no longer accepts becomes `unverified`, and its version's outcome becomes `failed` with the constraint `link_downgraded`. The versions keep serving.
- A rollback to 0.7.0 leaves the new tables and columns in place and does not read them. A version stored by 0.7.0 after the rollback reads as `not_evaluated` after a return to 0.8.0, until a cycle evaluates it.
- Each start re-evaluates the attestation links of every stored layer of every tenant before it serves, so a trust policy that a `registry.yaml` declaration, `PODIUM_LAYER_TRUST_DEFAULT`, or a replaced trusted root changed while the registry was stopped takes effect at the start. A start that cannot complete this, such as one whose store cannot be read or one that cannot list the tenants of a multi-tenant registry, fails with an error that names the step.
- A trust policy change through `podium layer update` or a re-registration also starts an ingest cycle of the layer in the background; a process keeps at most one such cycle of a layer waiting, and a shutdown waits for the running ones within its 15-second drain deadline.
- `podium admin migrate-to-standard` copies the outcome of each migrated version, its links, and the bundles they reference. It copies no root record, because each target process writes the record of the trusted roots it uses.

### Visibility of a `--layer-path` layer under `warn` and `require`

From 0.8.0, a layer that `--layer-path` or `PODIUM_LAYER_PATH` loads and whose trust mode resolves to `warn` or `require` no longer takes every change of its `.layer-config` at the next start (spec §4.7.11). [Visibility of a `--layer-path` layer](../content/layers#visibility-of-a---layer-path-layer) describes the rules.

- A `.layer-config` that admits fewer callers applies at the next start and in every ingest cycle. One that admits more callers applies only once a cycle passes the layer step over a signed state that holds it, and until then the layer object reports it in `visibility_pending`. An operator who widens such a layer before its signing is set up edits the file and runs `podium layer update` with the same visibility.
- A `.layer-config` that a later cycle cannot parse, or that holds an empty `groups` or `users` entry, makes the layer visible to no caller. 0.7.0 read the file at a start alone.
- A layer whose row a start creates is visible only to callers the deployment default admits. A deployment that relies on `.layer-config` alone to restrict layers sets `PODIUM_DEFAULT_LAYER_VISIBILITY=private` before the upgrade.
- A layer under the mode `none` is unaffected: each start applies its file, as in 0.7.0.
- The SQLite and Postgres stores add the `visibility_source` and `visibility_pending` columns of `layer_configs` at the first start. No step is needed. A row stored by 0.7.0 is read as `declared`, so its stored visibility bounds the next file.
- A read that cannot read the layer rows now uses the rows of the tenant's last successful read, and a tenant with no successful read sees no layer. 0.7.0 used the layers of the start for every tenant.
- `podium layer update` with a visibility flag on such a layer narrows it until the next cycle that passes the layer step, including the startup ingest of a restart over the same signed state, and prints a warning that says so. 0.7.0 kept such an update until the next start applied the file. Make a lasting narrowing in `.layer-config`.
- `podium layer reorder` and a `podium layer update` without a visibility flag no longer write the visibility of every layer they touch; they keep the stored one.
- A rollback to 0.7.0 leaves the new columns in place and does not read them, and its next start applies each file as written.

### Bounded Git fetch

From 0.8.0, each ingest of a `git` layer clones the tracked ref alone, with no tags, at the depth that `PODIUM_GIT_FETCH_DEPTH` or the `registry.yaml` key `git_fetch_depth` sets, 16 commits by default, and fetches again at four times the depth while the force-push check cannot decide from the history it holds. 0.7.0 cloned the complete history once a layer had a last ingested commit.

- A remote that cannot serve the history the check needs fails the ingest with `ingest.source_unreachable`. Such a remote is a shallow mirror whose history does not reach the layer's last ingested commit. 0.7.0 reported such an ingest as a rewritten history, which emitted `layer.history_rewritten` and, under `force_push_policy: strict`, refused the ingest. Before the upgrade, point each layer that tracks a shallow mirror at a repository with the full history, or deepen the mirror so that it holds the last ingested commit.
- A value of `PODIUM_GIT_FETCH_DEPTH` or `git_fetch_depth` that is not an integer from 1 to 2147483646 refuses startup with `config.invalid`.
- An ingest that reaches `PODIUM_GIT_FETCH_TIMEOUT` reports `details.constraint: "fetch_deadline"` in the reingest response, with the depth of the fetch that was running.
- A rollback to 0.7.0 needs no step. 0.7.0 does not read the variable and ignores the key.

### Workspace `.podium` that is a symbolic link

From 0.8.0, `podium sync` reads and writes its lock and `sync.yaml` only when the target's `.podium` is a directory rather than a symbolic link. A target whose `.podium` is a symbolic link, such as one that shares `sync.yaml` across Git worktrees, or whose `.podium` is not a directory, fails these commands before they write anything: `podium sync`, `podium sync override`, `podium sync save-as`, `podium profile edit`, and `podium init` in that workspace. The error names `.podium` and states that it must be a directory. 0.7.0 read and wrote through the link.

- Before the upgrade, find each target whose `.podium` is a symbolic link, replace the link with a directory, and copy `sync.yaml`, `sync.local.yaml`, and `sync.lock` into it.
- A `kind: marketplace` target whose repository commits `.podium` as a symbolic link fails its render the same way. Remove the link from the repository.
- A rollback to 0.7.0 needs no step. 0.7.0 reads the files from the directory.

### File hashes in the sync lock

From 0.8.0, `podium sync` records a `file_hash` for each file it writes whole in `<target>/.podium/sync.lock`, and the stale-file cleanup deletes an out-of-scope file only when its current bytes match that hash. A lock written by 0.7.0 records no `file_hash`, so the first sync after the upgrade deletes none of the out-of-scope files that lock records. The sync leaves each one in place, names it on standard error in a line that starts with `sync: stale-file cleanup: skipping` and states that the lock entry records no `file_hash`, and writes a lock without its entry, so later syncs do not name that file again. Every file the sync writes gets a `file_hash` in the new lock. The exception is an entry that a sync carries over unchanged: the previous output of an artifact whose manifest does not parse, which sync keeps under `on_unparseable: keep`. Such an entry keeps the 0.7.0 form without a `file_hash` until the artifact is written again, and sync names the file at the sync where the artifact leaves the scope, which can be later than the first sync after the upgrade.

- To have the cleanup remove the files of a planned scope change, run that sync with 0.7.0 before the upgrade.
- After the first sync with 0.8.0, delete by hand each file that the standard error output names and that the target no longer needs. A file that a later 0.8.0 sync writes again records a `file_hash`, and later cleanups remove it as usual.
- A `kind: marketplace` render fails instead of keeping such a file. The first render after the upgrade that would remove a file the committed 0.7.0 lock records exits non-zero before it writes or removes anything, pushes nothing, and names each file with the reason that the lock entry records no `file_hash`. Remove the named files from the marketplace repository by hand, or remove their entries from `.podium/sync.lock` for a file the render did not write, and commit the change. The failed render left the checkout as it was, so the next run renders the whole catalog change and publishes it. Each successful render writes a `file_hash` for every file it renders into the lock in the checkout. That lock reaches the repository with the next commit the publish phase makes, because the lock alone does not count as a change. Until then, a render that starts from a fresh checkout of the repository, such as one a CI job clones, still reads the committed 0.7.0 lock, and a render in it that would remove a file fails in the same way. A render that reuses a checkout reads the lock the previous render wrote there.
- A shared config file that sync merges entries into, such as `.mcp.json` or `AGENTS.md`, records no `file_hash`. The cleanup strips Podium's entries from it as before and never deletes it.
- A rollback to 0.7.0 needs no step. 0.7.0 ignores the `file_hash` key and removes out-of-scope files without checking them.

### Names that a filesystem resolves to `.git` or `.podium`

From 0.8.0, Podium refuses a `.git` path segment, and a segment that a filesystem resolves to `.git` or `.podium` although its bytes differ, on every platform. The refused forms follow Git's `core.protectHFS` and `core.protectNTFS` checks: a different letter case, a code point that HFS+ ignores, such as `.git` preceded by U+200C ZERO WIDTH NON-JOINER, the trailing spaces and periods that NTFS strips, an NTFS alternate data stream such as `.git::$INDEX_ALLOCATION`, and the NTFS short names. 0.7.0 refused none of these forms at write or at ingest.

- A layer that holds a package below a directory with such a name fails its ingest, and the reingest answers `ingest.lint_failed`. A filesystem-source `podium sync` and a workspace overlay with such a package fail the same way, and `podium import` refuses such a `--source` subdirectory. Before the upgrade, search each layer for directory names that name `.git` or `.podium` and rename them. Such a name is `GIT~1`, or `PODIUM~1` through `PODIUM~4`, in any letter case; an eight-character name of `PO`, up to four hexadecimal digits, `~`, and digits, such as `PO1A2B~1`; or `.git` or `.podium` with one of the ignored code points. Each form also matches when trailing spaces, periods, or a `:` and a stream name follow it. Package discovery already skipped a directory whose name begins with a dot.
- A directory or file named exactly `.git` inside a package, such as the repository of a skill directory that is a Git clone, is left out of the package, and its files enter no content hash. A package that bundles a file under another name that resolves to `.git`, such as `.GIT` or `GIT~1`, fails ingest with `lint.resource_path_reserved`. Before the upgrade, run `podium lint --registry <path>` on each filesystem layer, and on a checkout of each `git` layer, and rename or remove such directories.
- A version that 0.7.0 stored with resources under `.git/` cannot be written by the MCP server's `load_artifact` or by the SDKs' `materialize()`, which refuse the path, and `podium sync` leaves those files out with a warning. Reingest the layer to store the package without `.git`. The content hash changes, so an artifact that declares its version needs a new version: at the stored version, ingest rejects the new bytes with `ingest.immutable_violation`. A skill that declares no version receives a new registry-assigned version.
- `podium sync` and a `kind: marketplace` render do not write an output path with such a segment, and they skip a lock entry that names one, with a warning on standard error that names the path.
- The Python and TypeScript SDKs skip a workspace overlay that holds such a package, with a warning, and `materialize()` raises `MaterializeError` for a canonical ID or a resource path with a segment that names `.git`.
- A rollback to 0.7.0 needs no step.

### `mcp-server` artifacts through the MCP server

From 0.8.0, `mcp-server` artifacts are left out of the MCP server's results, as the specification states. The registry applies the filter to every request whose `X-Podium-Client` name is `mcp`, and the MCP server applies it again to workspace-overlay artifacts, to records its content cache serves, and to a registry that does not filter. `search_artifacts` and the `load_domain` notable list leave them out, and `load_artifact` refuses one with `registry.not_found` and writes nothing. 0.7.0 listed them and wrote each loaded server's entry into the harness configuration, such as `.mcp.json` or `.codex/config.toml`.

- A host or script that loads `mcp-server` artifacts through the MCP server receives `registry.not_found` after the upgrade. Deliver them with `podium sync`, which writes them into the harness configuration for the next session, or load them through an SDK.
- The registry records the refusal as it records a load of an ID that does not exist: an `artifact.loaded` audit event of result size 0 and no load count in the usage report. The MCP server records no local `artifact.loaded` event for it. A search from the MCP server ranks only the other artifacts, so its `total_matched`, its `artifacts.searched` `context.results`, and its usage-report search hits name no `mcp-server` artifact.
- The registry judges an artifact by the version a request resolves to. An artifact whose latest version is an `mcp-server` is left out of the MCP server's search and domain map even when an earlier version has another type, and a `load_artifact` pinned to that earlier version still loads it.
- `search_artifacts`, `load_domain`, and `load_artifact` responses carry `Vary: X-Podium-Client`. A caching proxy in front of the registry keys its entries on that header.
- The filter keys on the self-reported client name and is not access control. A program that sends `X-Podium-Client: name=mcp` loses `mcp-server` artifacts from its own results; send another name, such as `sdk`, to receive them.
- An entry that an earlier `load_artifact` wrote stays in the harness configuration file. A `podium sync` that writes into the same file rewrites Podium's entries there and keeps the entry only when the synced selection includes that artifact. Otherwise, delete the entry by hand.
- A rollback to 0.7.0 needs no step.

### Visibility entries that are empty or hold a control character

From 0.8.0, a layer's `users` and `groups` entries are non-empty strings that contain no control character, such as a line feed, a carriage return, or a tab. 0.7.0 accepted any string, and the SQLite and Postgres stores read an entry containing a line feed back as several entries.

- Before the upgrade, check the `visibility:` block of each layer in `registry.yaml` and each `.layer-config` that a registry reads through `--layer-path` for an empty entry and for an entry with a control character. An entry written as `${ENV_VAR}` is empty when the variable is unset. A start that finds such an entry exits with `config.invalid`, and the message names the layer and the entry's position.
- A layer registration or update that carries such an entry, or an `owner` with a control character, is refused with `400 registry.invalid_argument`. A registration is refused the same way when the caller's subject, which the registry stores as the owner and `users` entry of a user-defined layer or as the `users` entry that `PODIUM_DEFAULT_LAYER_VISIBILITY=users` grants, contains a control character.
- A layer that 0.7.0 stored keeps its entries. Where its stored `users` or `groups` holds an empty entry, an update that sends that list back unchanged, as a client that returns the layer object it read does, is refused; send the list without the empty entry, or omit the member. The web UI's Edit dialog sends both lists on every update of an admin-defined layer, so such a layer cannot have its ref changed or its webhook secret rotated there until the empty entry is removed. The dialog shows the entry as a token labelled `(empty entry)`; remove that token, then save.
- White space around an entry is kept and matched as written, as in 0.7.0.
- A rollback to 0.7.0 needs no step.

### Skills whose `SKILL.md` is missing or does not parse

From 0.8.0, an artifact whose `ARTIFACT.md` declares `type: skill` and whose directory holds no `SKILL.md` is refused on its own under the unparseable-manifest rule, as an artifact whose `SKILL.md` does not parse already was. 0.7.0 failed the whole layer instead.

- Ingest stores the other artifacts of the layer and lists the skill in `rejected` with `ingest.invalid_artifact` and `details.constraint: "manifest_unparseable"`. The reason names `<id>/SKILL.md` and states that the file is missing. A script that watched for the layer's failed reingest reads `rejected` instead.
- `podium lint` lists the skill among its refused artifacts and exits 1. A filesystem-source `podium sync` drops it, names it on standard error, writes the other artifacts, and exits 1, and by default keeps the output an earlier run wrote for it, as for any artifact whose manifest does not parse.

Stored-row admission no longer serves a skill row from its `ARTIFACT.md` alone when the row's `SKILL.md` does not parse. 0.7.0 served such a row's sensitivity, deprecation flag, and replacement from the manifest. 0.8.0 serves the values the row stores, as it does for a row whose manifest does not parse. For such a row, the served type, sensitivity, deprecation flag, replacement, and `audit_redact` key set come from stored columns that no content hash or signature covers. A skill's fields resolve from both documents, so a row whose `SKILL.md` does not parse has no resolved value to serve from its bytes. Ingest refuses such a skill, so only a row that an earlier release stored is affected.

- To find an affected skill, reingest each layer. A skill whose `SKILL.md` does not parse is listed in `rejected` with a reason that names its `SKILL.md`, and the registry keeps serving the version it stored.
- To serve resolved values again, correct the `SKILL.md` and publish the skill at a new version.
- A rollback to 0.7.0 needs no step.

### Files of an unparseable package that cannot be read

From 0.8.0, ingest, `podium lint`, and a filesystem-source `podium sync` read every file of a package whose manifest does not parse, because the package digest of every package in a layer covers its files. 0.7.0 refused such a package without reading its other files. A file of such a package that cannot be read, such as a symbolic link that leaves a `local` layer's directory, a dangling symbolic link, or a file without read permission, now fails the ingest of the whole layer, as an unreadable file of a package whose manifest parses already did in 0.7.0. 0.7.0 refused the unparseable artifact alone and ingested the rest of the layer. A symbolic link that leaves the directory fails with `ingest.source_unreachable`.

- Before the upgrade, run `podium lint --registry <path>` on each filesystem layer and fix or remove each package it refuses as `manifest_unparseable`, or remove the unreadable files from it.
- A rollback to 0.7.0 needs no step.

### `ARTIFACT.md` below a dot-prefixed directory of a package

From 0.8.0, a directory that carries an `ARTIFACT.md` below a dot-prefixed directory of a package, such as `.claude/ARTIFACT.md` or `.cache/tool/ARTIFACT.md`, belongs to the enclosing package, and its files are bundled resources. 0.7.0 left such a directory out of the enclosing package, and package discovery skipped it, so its files belonged to no artifact.

- The content hash of an affected package changes at its next ingest. A package that declares its version is refused with `ingest.immutable_violation` at the version 0.7.0 stored, so publish it at a new version. A skill that declares no version receives a new registry-assigned version.
- To keep such files out of the package, delete them before ingest. Podium reads no ignore file.
- A rollback to 0.7.0 changes the content hash back. A package that 0.8.0 stored at a version with the newly bundled files is refused with `ingest.immutable_violation` when 0.7.0 ingests it again at that version, because 0.7.0 leaves those files out. After a rollback, publish each affected package at a new version, or remove the dot-prefixed directory's `ARTIFACT.md`.

### Filesystem layer paths that are symbolic links

From 0.8.0, a filesystem-source `podium sync`, `podium lint`, and the podium-mcp workspace overlay resolve a layer or overlay path that is a symbolic link to a directory and walk the directory it names, for its artifacts and its `DOMAIN.md` files. 0.7.0 found no artifact and no domain under such a path, while registry ingest of the same path read it.

- A workspace whose overlay path is a symbolic link starts serving the overlay's artifacts and domains after the upgrade. Remove the link to keep the overlay empty.
- A rollback to 0.7.0 needs no step.

### Identity provider values the registry does not recognize

From 0.8.0, the registry starts only with a `PODIUM_IDENTITY_PROVIDER` or `identity_provider.type` of `oidc-jwt`, `trusted-headers`, `injected-session-token`, `external`, or `none`, or with the value unset. Any other value, such as a free-form label like `oidc` or a mistyped `oidc-jwy`, refuses startup with `config.identity_provider_unverified`, and the message names the value. 0.7.0 started with such a value, verified no caller, showed every layer to every caller, and skipped the loopback-bind refusal.

- Before the upgrade, check the value each registry sets. A registry that set a free-form label because a reverse proxy or gateway in front of it authenticates callers sets `PODIUM_IDENTITY_PROVIDER=external` instead. `external` behaves as the free-form label did in 0.7.0, apart from the bind rule below. [External authentication](../identity/#external-authentication) describes it.
- A registry with `external` refuses a non-loopback bind with `config.public_bind_refused` unless `PODIUM_ALLOW_PUBLIC_BIND=true` or `--allow-public-bind` is set. A registry behind a proxy that binds `0.0.0.0` sets the variable.
- A registry that set a mistyped provider name served with visibility unenforced. Correct the name; the registry then applies layer visibility, so a caller that saw every layer under 0.7.0 sees only the layers its identity admits.
- A multi-tenant registry with an unrecognized value now fails with `config.identity_provider_unverified` rather than `config.multitenant_no_verifier`.
- A rollback to 0.7.0 needs no step. 0.7.0 starts with `external` as it started with any free-form label.

### Start without the identity provider a registry ran with

From 0.8.0, each start under `oidc-jwt`, `trusted-headers`, or `injected-session-token` records in the metadata store that the registry runs with verified identity. A later start with no identity provider, with `PODIUM_IDENTITY_PROVIDER=external`, or in public mode exits with `config.identity_provider_removed` while the store holds the record. 0.7.0 started such a registry and showed every layer to every caller.

- The first 0.8.0 start under a verified provider writes the record. A registry that has never started under one, such as a standalone registry or a `--layer-path` bootstrap without a provider, or the Docker Compose evaluation stack, holds no record and starts as before.
- A deployment that intentionally drops its identity provider sets `PODIUM_ALLOW_IDENTITY_PROVIDER_REMOVAL=true`, or passes `--allow-identity-provider-removal` to `podium serve`, for one start. That start deletes the record and logs a warning that names the removal. Remove the setting after that start, because while it stays set every later start without a verifier proceeds without the refusal. A Helm install sets the variable through `extraEnv`, and a later start on which the entry has no effect logs a warning to remove it.
- The refused start writes nothing to the metadata store, so during a rolling update the pods that still run with the identity provider keep serving with their layer visibility unchanged.
- `podium admin migrate-to-standard` copies the record from its source to its target.
- A start that exits with the code after an upgrade, where no removal was intended, has lost an identity-provider setting. Restore it, as [Removing an identity provider](../identity/#removing-an-identity-provider) describes.
- A rollback to 0.7.0 needs no step. 0.7.0 ignores the record and the variable.

### Package digest of stored versions

From 0.8.0, the registry stores the package digest of each version it ingests and serves it in the `package_digest` member of a `load_artifact` response. The digest covers the path and SHA-256 digest of every file of the package and the `type`, `sensitivity`, `deprecated`, and `replaced_by` values its documents declare. `podium artifact show` prints it on standard error as `package_digest: <digest>` in its default output and carries it in its `--json` object. A batch item does not carry it, and no consumer check reads it. 0.7.0 stored and served no such value.

- The first 0.8.0 start whose listener binds computes the digest of every stored version, soft-deleted versions included, from the stored `ARTIFACT.md`, `SKILL.md`, and resource content hashes, and records completion in the metadata store, so later starts skip the computation. It reads no object from object storage. The start logs one summary line that begins with `package digest fill:`.
- The computed digest reflects the files that the earlier release stored for the version. For a package that the changes in this release to dot-prefixed directories inside a package and to symbolic-link layer paths affect, it can differ from the digest of the current tree, which `podium attest` computes. Ingesting such a package at a new version stores the digest of the current tree.
- A stored skill whose `SKILL.md` does not parse receives the digest of an unresolved package, which matches the stored values the registry serves for it, as [Skills whose `SKILL.md` is missing or does not parse](#skills-whose-skillmd-is-missing-or-does-not-parse) describes.
- A version whose stored files do not form a package, such as one with two resources under one path, receives no digest, and the start logs its ID. Ingest stores no such version.
- The computation does not require every 0.7.0 process to stop first. A version that a 0.7.0 process stores after the computation, during a rolling upgrade or after a rollback, has no digest, and a load of it carries no `package_digest` member. A 0.8.0 ingest that finds the version unchanged stores its digest, so `podium layer reingest <id>` on the layer fills it.
- When the write of one version's digest fails, the start logs it and continues, and the next start computes the digests again. A version whose content hash another replica rewrites during the computation is read again and computed from its current bytes. When its hash changes a second time, the next start computes the digests again.
- `podium admin migrate-to-standard` copies each version's digest and removes the target's completion record, so the target's next start computes the digest of each version that the source stored without one.
- A rollback to 0.7.0 needs no step. 0.7.0 ignores the `package_digest` column.

### Sigstore keyless signatures as Sigstore bundles

From 0.8.0, `podium sign --provider sigstore-keyless` prints a Sigstore bundle of version 0.3, and `podium verify --provider sigstore-keyless` verifies the bundle with `sigstore-go`. 0.7.0 printed a keyless envelope with `cert`, `signature`, `tlog`, and `timestamp` members. 0.8.0 refuses such an envelope with `materialize.signature_invalid`, and the message states that a keyless envelope from 0.7.0 or earlier is not accepted. 0.8.0 converts no envelope.

- The registry stores no keyless signature. It signs at ingest under its registry-managed key, `podium admin sign-stored-rows` re-signs stored rows under that key, and every delivery signature is registry-managed, so stored rows, delivery signatures, `podium-mcp`, server-source `podium sync`, and the language SDKs need no step.
- Sign again, with `podium sign --provider sigstore-keyless` under 0.8.0, each content hash whose 0.7.0 keyless envelope is kept outside Podium, and replace the envelope with the bundle.
- `podium verify` reads `PODIUM_SIGSTORE_TRUSTED_ROOT_FILE` with `sigstore-go`. When the file lists a certificate transparency log, the certificate must carry a signed certificate timestamp, which certificates from the public-good and staging Fulcio instances carry. A file whose `tlogs` entry has no `validFor.start`, or whose `tlogs` or `ctlogs` entry carries a key type the library does not read, no longer loads. The `trusted_root.json` that the Sigstore TUF repository publishes loads.
- The certificate checks are narrower. The subject alternative name holds exactly one email address or URI, which `PODIUM_SIGSTORE_CERT_IDENTITY` lists; the issuer comes from the Issuer (V2) extension alone; and the key is an ECDSA P-256 key. The certificates Fulcio issues to `podium sign` meet these rules.
- A rollback to 0.7.0 needs a step for each bundle that 0.8.0 printed and 0.7.0 must verify: 0.7.0 refuses the bundle, so sign the content hash again under 0.7.0.

## 0.7.0

### Rollback floor for the layer credential columns

From 0.7.0, the SQLite and Postgres metadata stores write a `git` layer's URL credential apart from the rest of its remote. The lowest version that runs against a database 0.7.0 has written to is 0.6.0, both for a rollback and for mixed-version replicas.

#### How 0.7.0 stores the credential

- For a `repo` that is a URL with a scheme and userinfo, and that a layer read does not report as `[redacted]`, `layer_configs.repo_userinfo` receives the userinfo and `layer_configs.repo` receives the rest of the URL.
- Every other `repo` is held whole in `repo`. This covers an scp-like remote, a filesystem path, and a value reported as `[redacted]`.
- `repo_userinfo` holds the credential unencrypted. A SQL export of `layer_configs` needs both columns to reproduce a layer's remote, and it carries a secret when it includes `repo_userinfo`.

Before it serves, each 0.7.0 start whose listener binds rewrites every row that holds such a URL whole in `repo` into that form, a soft-deleted layer's row included. It logs how many rows it rewrote.

- The same start sets to NULL a `repo_userinfo` that sits beside a `repo` held whole, such as an scp-like remote or a path, because the registry ignores that column when it reads such a row. The logged count includes those rows.
- The start changes no other column, and the layer is reported and cloned as before.
- A row that another process writes while the start runs is left as that process wrote it.
- The rewrite moves the credential between two columns of one row. It does not remove the credential from a backup, a replica, or storage the database has not yet reused.

The rewrite changes the stored form of a value that 0.6.0 already reads in both forms. It records no completion and needs no `sign-stored-rows` run. [Releases that migrate stored values](upgrading#releases-that-migrate-stored-values) does not apply: the canary phase runs as usual, and the store needs no backup restore to roll back.

#### Mixed 0.6.0 and 0.7.0 replicas

Replicas on 0.6.0 and on 0.7.0 that share a Postgres store read and clone identically. A 0.6.0 replica writes the whole URL to `repo` on every layer write it makes, so a row can change form in either direction during the roll. A row that a 0.6.0 replica writes after the last 0.7.0 replica started holds the whole URL until its next 0.7.0 write or the next 0.7.0 start. Restart a 0.7.0 replica after the last 0.6.0 replica has stopped to rewrite those rows at once.

A request that a 0.6.0 replica serves during the roll is answered as 0.6.0 answers it:

- 0.6.0 accepts a `repo` that carries the credential in its URL, which 0.7.0 refuses.
- 0.6.0 ignores the `repo_username`, `repo_token`, and `clear_repo_credential` members. A registration that carries those members therefore stores the remote with no credential. An update that carries them reports success and leaves the stored credential as it was.
- A registration that sends back the remote `podium layer list` reports for a layer that holds a credential is refused with `details.constraint: "redacted_repo"`.

Make every request that sets, rotates, or removes a credential, or that re-registers a layer holding one, after the last 0.6.0 replica has stopped. Repeat at that point any such request made during the roll.

#### Declared layers

0.6.0 and earlier ignore the `source.git.username` and `source.git.token` keys of a declared layer. 0.7.0 reads a credential either in the userinfo of `source.git.repo` or in the two keys.

- Keep a declared private layer's credential in `source.git.repo` through the rollout, and for as long as a 0.6.0 process may start on the configuration. Then move it to the two keys.
- A 0.6.0 process that starts on a configuration that uses the keys, on a rollback or as a replica that restarts during the roll, writes the declared layer with no credential. Its private remote then fails with `ingest.source_unreachable` on every replica that shares the store until a 0.7.0 process starts.
- Before rolling back to 0.6.0, move each declared credential back into the percent-encoded userinfo of `source.git.repo` and remove both keys.

#### Upgrading directly from 0.5.2 or earlier

An upgrade from 0.5.2 or earlier directly to 0.7.0 stops every replica before the first 0.7.0 replica starts, and its rollback target is 0.6.0. On a Helm chart deployment, such an upgrade passes neither `--rollback-on-failure` nor `--atomic`, because an automatic rollback reinstalls a binary below the floor. An upgrade through 0.6.0 has neither restriction.

#### Running 0.5.2 against a database 0.7.0 has written to

This combination is unsupported. A 0.5.2 process does not read `repo_userinfo`, with the following effects:

- **Layers registered through the API or the CLI.** After the first 0.7.0 start, every such layer whose `repo` is a URL with a scheme and userinfo holds its credential apart. 0.5.2 clones such a layer without the credential, so a private remote fails with `ingest.source_unreachable`. A registration that carries the layer's listed remote is not refused.
- **Layers declared with the credential in `source.git.repo`.** The layer is written from the configured URL at each start and clones as before.
- **Layers declared with `source.git.username` and `source.git.token`.** The layer is written with no credential, because 0.5.2 ignores both keys.

On SQLite, a 0.5.2 write of the layer clears `repo_userinfo`. Such a write is an update, a reorder, a re-registration, a start for a declared layer, or a completed ingest. The layer then needs its credential set again:

- On 0.6.0, by a registration with the full URL.
- On 0.7.0, for an `http` or `https` remote, with `podium layer update --repo-username <name> --repo-token-file <path>`. `--repo-username` names the username the provider expects, because the cleared row holds none.
- On 0.7.0, for an `ssh://` remote, by a registration with the full `ssh://` URL, login name included, because `podium layer update` takes no credential member on such a layer.

On Postgres, a 0.5.2 write leaves the column as it is. If a re-registration under 0.5.2 replaced the layer's `repo` with a different URL that carries no userinfo, the column still holds the earlier credential, and 0.6.0 or later joins it to the new remote when it reads the row. Before that layer is ingested on 0.6.0 or later, remove or replace the stale credential:

- **On 0.6.0.** Re-register the layer with the full URL of the new remote, or with the userinfo-free URL and `--force-repo-overwrite` (`force_repo_overwrite: true`). A re-registration with the userinfo-free URL and no override is refused with `details.constraint: "redacted_repo"`. The 0.6.0 CLI has neither update flag, and a 0.6.0 registry answers an update that carries either member with success and changes nothing.
- **On 0.7.0, for an `http` or `https` remote.** Remove the credential with `podium layer update --clear-repo-credential`, or replace it with `podium layer update --repo-username <name> --repo-token-file <path>`. The replacement names the username because an update that omits it keeps the stale one. A re-registration with the userinfo-free URL alone is the re-registration with the listed remote that [Private repositories](../content/layer-sources#private-repositories) describes, and it keeps the stored credential, here the stale one. One that also passes `--clear-repo-credential`, or `--repo-username` with `--repo-token-file`, stores only what it carries.
- **On 0.7.0, for an `ssh://` remote.** Re-register the layer with that URL as wanted, without userinfo or with its login name, which stores no stale credential, because `podium layer update` takes no credential member on such a layer.

An update does not change `repo`, so a layer that was only updated under 0.5.2 needs no re-registration. A layer declared in the registry configuration needs none either, because each start writes both columns from the declaration. 0.6.0 reads the declaration from `source.git.repo` alone, and 0.7.0 reads it from `source.git.repo` or from `source.git.username` and `source.git.token`.

### Credentials stored by an earlier release

0.7.0 sets a `git` layer's credential through separate username and token members, and it refuses a new `http` or `https` `repo` that carries URL userinfo. Credentials that 0.6.0 or earlier stored behave as follows:

- A layer whose stored remote carries its credential in the URL keeps cloning with it. `podium layer update --repo-token-file` or `--clear-repo-credential` replaces or removes that credential.
- A stored username that contains `:`, `/`, `"`, a space, or a control character cannot be kept. A rotation of such a layer passes `--repo-username` with `--repo-token-file`.
- A layer stored in the URL-token form `https://<token>@host` holds no username. A rotation of it passes `--repo-username` where the provider expects one.
- A declaration that sets neither `username` nor `token` is written as declared. A credential in the userinfo of `source.git.repo`, which is the form 0.6.0 and earlier read, keeps working. Each start logs a line that names the layer and asks for the credential to move to the two keys.

[Private repositories](../content/layer-sources#private-repositories) describes how to set, rotate, and remove a credential.

### Skill metadata fields in stored rows

0.7.0 reads `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, and `release_notes` from a skill's `SKILL.md` `metadata`, which earlier releases ignored. [Podium fields in SKILL.md metadata](../../authoring/frontmatter-reference#podium-fields-in-skillmd-metadata) gives the encodings and the precedence of `ARTIFACT.md`. The notes below cover a skill row stored before the upgrade whose `SKILL.md` `metadata` already holds one of these keys.

#### What a load serves

A load from 0.7.0 resolves the fields from the row's stored bytes. Such a row changes its served sensitivity, deprecation, replacement, and delivery_hash at its first load from 0.7.0. Its content hash and signature do not change.

#### What search reads

0.7.0 does not recompute stored rows. Such a row keeps the tags, sensitivity, search visibility, and deprecation flag that the earlier release stored, so search and load can disagree for it:

- A tag filter on a `metadata` tag misses the row.
- Search results report the stored `tags` and `sensitivity`.
- A `direct-only` value does not hide the row from search.
- A `metadata` deprecation neither removes the row from default search nor stops `latest` from resolving to it, while its load carries the `deprecation_warning`.
- The stored deprecation flag stays unset when only `metadata` deprecates the row, so the 90-day retention window for deprecated versions does not start for that version.
- Semantic search ranks the row by the vector that the earlier release built from the stored tags.

Lexical search reads the row's `name` and `when_to_use` from its stored bytes, so it matches a `metadata.when_to_use` in such a row.

A `podium sync` or a `podium-mcp` load for Claude Code after the upgrade writes a skill's `when_to_use` from `metadata` or `ARTIFACT.md` as a top-level `when_to_use` line in the materialized `SKILL.md` when the authored `SKILL.md` has none, so the first sync after the upgrade rewrites those files and a workflow that publishes on changes sees them as changed. The Claude marketplace output gains the same line.

When a later version of such a skill sets `deprecated`, the registry emits `artifact.deprecated` for it, even when the earlier version's `metadata` already carried `deprecated: "true"`, because the earlier row's stored flag is unset.

#### Publishing an affected skill at a new version

Re-ingesting the same bytes does not change the row, and `podium admin reembed` writes no column. Publish each affected skill at a new version, which stores the resolved values. The field to raise depends on the file layout:

- For a skill whose `ARTIFACT.md` declares `version`, raise that `version`. When `SKILL.md` also carries a `major.minor.patch` `metadata.version`, set it to the same value, because the `ARTIFACT.md` value is the version and lint refuses a mismatch.
- For a skill whose `ARTIFACT.md` declares no `version` and whose `metadata.version` is a `major.minor.patch` value, raise `metadata.version`.
- For a skill that declares no usable version, change its content, and the registry assigns the next version.

#### Emptying the `podium-mcp` content cache

A `podium-mcp` that cached such a skill before the upgrade keeps reporting the cached sensitivity and delivery record until its cache is emptied, because a revalidation confirms the unchanged content hash. After every registry replica runs 0.7.0, and again after a rollback, stop each `podium-mcp` and run `podium cache prune --days 0` on its cache directory before restarting it:

```bash
podium cache prune --dir ~/.podium/cache --days 0
```

#### Public mode

A public-mode registry refuses a new ingest that carries `metadata.sensitivity` at or above its floor and keeps serving rows that are already stored. A load from 0.7.0 serves a row at its `metadata.sensitivity`, even when that value is `medium` or `high`, when 0.6.0 stored the row while public mode was enabled or when a 0.6.0 replica stores it during the roll or after a rollback.

#### Rolling upgrade and rollback

The rolling upgrade from 0.6.0 and the rollback to 0.6.0 follow [Rollback floor for the layer credential columns](#rollback-floor-for-the-layer-credential-columns), and the store needs no backup restore for this change.

- A row that a 0.6.0 replica stores during the roll is treated as any row an earlier release stored.
- A load that a 0.6.0 replica serves during the roll ignores `metadata`. Until the last 0.6.0 replica stops, the sensitivity, deprecation, and replacement that a load serves for such a skill depend on the replica that answers.
- During the roll and after a rollback to 0.6.0, a row that 0.7.0 stored keeps the tags, sensitivity, search visibility, and deprecation flag that 0.7.0 resolved from `metadata`. 0.6.0 search reports and filters on those values, while a 0.6.0 load ignores `metadata`, so 0.6.0 search can report a sensitivity or a deprecation that a 0.6.0 load of the same row does not serve.

### Catalog lint advisories and listing budgets

0.7.0 adds catalog advisories to `podium lint` and a listing-budget check to `podium sync`. Neither changes what ingest accepts or what sync writes. The notes below cover the output and exit statuses an existing catalog and workspace can see after the upgrade.

#### `podium lint`

- `podium lint` now reports the catalog advisories `lint.thin_description`, `lint.colliding_descriptions`, and `lint.near_duplicate_content`, which registry ingest also runs over the layer it ingests. Each is a warning, so a catalog that linted clean can print new warnings and still exits 0 under the default `--fail-on error`.
- `lint.colliding_descriptions` now reports two descriptions whose normalized word sets have a similarity of 0.7 or higher, so descriptions that follow one template draw it.
- `lint.near_duplicate_content` compares prose bodies and prose resources, which are bundled `.md`, `.markdown`, `.txt`, and `.rst` files. It does not compare license files, scripts, or data files. A prose resource of 50 words or more that several artifacts bundle, such as a shared reference document, draws the warning in every artifact that bundles it.
- A CI pipeline that must stay green does not pass `--fail-on warning` until the catalog is clean. Clear the warnings by rewriting the descriptions, by raising a threshold through the `PODIUM_LINT_*` variables, or through `lint_suppress`, as [Skill lint checks](../../authoring/frontmatter-reference#skill-lint-checks) describes.

#### `podium sync`

- `podium sync` estimates the skill listing of each `kind: workspace` target and, by default, writes a warning on standard error when the estimate exceeds 8,000 characters for an adapter that writes skills. The warning leaves the exit status unchanged. Set `check: off` for the adapter under `listing_budgets` in `sync.yaml` to turn the check off, or raise `max_chars`, as [Listing budget](../../consuming/configure-your-harness#listing-budget) describes. A workspace whose Claude Code model has a 1,000,000-token context window sets `max_chars: 40000` for `claude-code`.
- For `claude-code`, `max_entry_chars` defaults to 1536, the length at which Claude Code shortens a skill's listing text, so the check also reports each skill whose description is longer than 1536 code points. Under `check: error`, such a description fails the sync. Shorten the description, or set `max_entry_chars` higher for `claude-code`.
- `listing_budgets` is a new top-level `sync.yaml` key. A 0.6.0 `podium sync` ignores it, and a 0.6.0 `podium sync save-as` drops it from the `sync.yaml` it rewrites.

### Per-artifact manifest refusal and skill lint severities

0.7.0 refuses an artifact whose manifest does not parse without stopping the rest of its layer, and it lowers the severity of several skill lint checks. A layer or a skill that 0.6.0 refused as a whole can ingest after the upgrade.

#### Unparseable manifests

In 0.6.0, one artifact whose `ARTIFACT.md` or `SKILL.md` frontmatter did not parse stopped its whole layer. Ingest stored no row from the cycle and answered `422 ingest.lint_failed`, `podium lint` reported the parse error without linting the other artifacts, and `podium sync` wrote nothing.

- Ingest refuses that artifact alone with `ingest.invalid_artifact` and `details.constraint: "manifest_unparseable"`, and it stores the other artifacts of the layer. The `POST /v1/layers/reingest` response lists the artifact in `rejected`, and `podium layer reingest` names it on standard error and exits non-zero. A script that watched for `422 ingest.lint_failed` on such a layer reads `rejected` instead.
- `podium lint` lists the artifact in `rejected`, lints the other artifacts, and exits 1.
- A filesystem-source `podium sync` drops the artifact, names it on standard error, writes the other artifacts and the lock file, and exits 1. By default it keeps the files and lock entry an earlier run wrote for the artifact, when the earlier run used the same harness, the artifact passes the run's scope and toggles, and no other artifact of the run takes them over by writing a file the kept output holds whole or a configuration-merge entry under the same key, such as an `mcpServers` server of the same name. Another artifact's entry in a shared file such as `.mcp.json` does not end the keep. `--on-unparseable remove`, `PODIUM_SYNC_ON_UNPARSEABLE=remove`, or `defaults.on_unparseable: remove` in `sync.yaml` removes them.
- Lock entries now record the artifact's `type`, which the keep rule reads for the scope type filter. An entry a 0.6.0 sync wrote has no `type` and fails any non-empty type filter, so under a type filter the output of an artifact whose manifest stops parsing is removed until a 0.7.0 sync has rewritten the entry.
- A manifest at a layer root has no canonical ID and still fails the whole layer.

[Unparseable manifests](../content/layers#unparseable-manifests) describes the rule.

#### Nested `SKILL.md` metadata

In 0.6.0, a sequence or a mapping under a `SKILL.md` `metadata` key made the frontmatter unparseable, and the skill was refused. Published skills nest values under keys of their own, such as `requires` and `openclaw`, so 0.6.0 refused them.

- A sequence or a mapping under a key Podium does not read parses. Podium keeps the bytes unchanged in every materialized and published copy, except where an adapter adds the user-only control to a frontmatter written as a flow mapping or opening with an explicit key, and re-encodes it with every value kept. Podium does not read the value, and lint reports the `lint.skill_metadata_nonstring` warning.
- A sequence or a mapping under a key Podium reads, such as `tags: [a, b]`, is ignored with the `lint.skill_metadata_field_ignored` warning. Under `version`, it is ignored, and the skill receives a registry-assigned version.
- A `name` or a `description` that is a mapping still makes the frontmatter unparseable.

A skill that 0.6.0 refused for nested metadata ingests at the next reingest of its layer. Neither warning fails `podium lint` under the default `--fail-on error`.

#### Skill lint severities

- A top-level `version` in `SKILL.md` is a `lint.skill_podium_only_field` warning, where 0.6.0 reported an error. Podium ignores the value and takes the version from `metadata.version` or `ARTIFACT.md`. Every other top-level Podium-only field stays an error.
- `lint.skill_version_assigned` is an info diagnostic, where 0.6.0 reported a warning. It fails `podium lint` under neither `--fail-on` value.
- A relative prose link that leaves the package, such as `../other-skill/SKILL.md`, is a warning, where 0.6.0 reported an error. The diagnostic recommends naming the other artifact by its canonical ID. A link to a bundled file the package does not contain stays an error.
- Lint reads inline links and images as CommonMark parses the body. It does not check link syntax inside a code span or a fenced or indented code block, including one nested in a list item or a block quote, an escaped bracket, a reference-style link, or a target with a scheme other than http or https, such as `mailto:`. It resolves a link with a fragment, such as `references/api.md#errors`, to the bundled file.
- Lint does not check a root-relative link, such as `[analytics](/features/analytics)`, which 0.6.0 reported as a missing bundled file.
- A link to a directory of the package, such as `[templates](assets/templates/)`, resolves when at least one bundled file lies below it, where 0.6.0 reported a missing bundled file.

### Semantic overlap check

0.7.0 adds the `lint.semantic_overlap` check, the catalog overlap report, and `podium admin rebuild-overlap`. [Finding overlapping artifacts](../../managing/overlap) describes the check and the report.

- Each 0.7.0 start creates the `semantic_overlap_records` and `semantic_overlap_computations` tables in the SQLite database and in each Postgres org schema if it is absent. The change is additive, and no existing table changes. A 0.6.0 process does not read the tables, so a rollback leaves them in place unused.
- The check is off by default, and a registry that leaves `PODIUM_LINT_SEMANTIC_OVERLAP` unset writes no records and runs no sweep.
- `GET /v1/overlap` and `podium overlap` are available whether or not the check is on. With the check off, they report `lint.thin_description` and `lint.colliding_descriptions` alone.

To turn the check on for an existing catalog:

1. Confirm that the registry has an embedding source. A registry with `PODIUM_LINT_SEMANTIC_OVERLAP=on`, no embedding provider, and a backend that does not embed text itself refuses to start with `config.invalid`.
2. On Pinecone, Weaviate Cloud, or Qdrant Cloud, confirm that the index or collection uses the cosine metric, as [Vector backends](../services/vector-backends#semantic-overlap) describes.
3. Set `PODIUM_LINT_SEMANTIC_OVERLAP=on` on every registry replica and restart them.
4. Wait for the startup pass, which each replica runs in the background as soon as its startup ingest completes and which computes every served artifact that has no computation entry. The periodic sweep does not need to run first. On a managed backend, artifacts whose vectors the outbox has not written are computed at the outbox write. `podium admin rebuild-overlap --registry <url>`, run in each tenant as an identity that holds the `admin` role, recomputes every artifact at once.
5. Read the result with `podium overlap --registry <url>`. The command warns while `computed` is below `total`. The default threshold follows the embedding model, 0.80 for `text-embedding-3-small`, so tune `PODIUM_LINT_SEMANTIC_SIMILARITY` against the catalog only when the reported pairs call for it.

### Usage report

0.7.0 counts loads, syncs, and search hits per artifact and serves them through `GET /v1/usage` and `podium usage`. [Seeing how the catalog is used](../../managing/usage#built-in-usage-report) describes the report.

- Each 0.7.0 start creates the `usage_daily` table in the SQLite database if it is absent. A Postgres org schema that an earlier release created gets the table when 0.7.0 first serves a request for that org, and the startup retention pass skips an org schema that does not have it yet. The change is additive, and no existing table changes.
- A 0.6.0 process neither reads nor writes `usage_daily`. A rollback to 0.6.0 leaves the stored counts in place, and a later 0.7.0 start adds to them.
- When a 0.6.0 process purges a soft-deleted layer at the end of its recovery window, after a rollback or during a rolling upgrade, the layer's rows stay in `usage_daily`. No 0.7.0 purge reaches them afterwards, and the daily retention pass removes them only as their days pass `PODIUM_USAGE_RETENTION_DAYS`. Until then, a layer registered later under the same identifier reports them as its own counts. Before registering a layer under such an identifier, delete its rows with SQL:
  - In the SQLite database, which holds the rows of every tenant, run `DELETE FROM usage_daily WHERE tenant_id = '<tenant-id>' AND layer = '<layer-id>'`. `podium admin tenant list` prints each tenant's ID, and `SELECT DISTINCT tenant_id FROM usage_daily WHERE layer = '<layer-id>'` lists the tenants that hold rows under the identifier.
  - On Postgres, run `DELETE FROM usage_daily WHERE layer = '<layer-id>'` in the tenant's org schema, which holds that tenant's rows alone.
- A 0.6.0 replica counts nothing. During a rolling upgrade from 0.6.0 and after a rollback, the report omits the requests that 0.6.0 replicas served, so the counts for those days are lower than the traffic.
- A 0.6.0 replica answers `GET /v1/usage` with 404, so `podium usage` against a registry with a 0.6.0 replica behind its load balancer can fail until every replica runs 0.7.0.
- `artifacts.searched` audit records that a 0.6.0 replica writes carry no `context.results` member.
- 0.6.0 clients send no `X-Podium-Client` header. A 0.7.0 registry counts the requests of a 0.6.0 `podium-mcp`, `podium` CLI, or SDK under client `unknown` and harness `unknown`, and records no `caller.client` for them. A 0.6.0 `podium sync` sends no `purpose=sync`, so a 0.7.0 registry counts its loads as loads rather than syncs. Upgrade the clients to 0.7.0 to have their requests counted under `mcp`, `cli`, and `sdk`, and syncs counted as syncs.
- A 0.7.0 client against a 0.6.0 registry sends the header, and the 0.6.0 registry ignores it.

The store needs no backup restore to roll back.

### Audit destinations and delivery

0.7.0 writes each audit record to a list of destinations and changes how an HTTP destination receives records. [Audit events](../../reference/audit-events) documents the destinations, delivery, and loss accounting.

- **Helm audit default.** The chart's `config.auditLogPath` defaults to `stdout`, where 0.6.0 left it empty, and the chart renders it as an `env:` entry. An `env:` entry overrides the same variable from `envFrom`, so a `PODIUM_AUDIT_LOG_PATH` set in the `existingSecret` stops applying on upgrade and the registry writes its audit records to the container log. Before upgrading, move that value to `config.auditLogPath`.
- **HTTP destination format.** An `http(s)` destination receives NDJSON batches (`application/x-ndjson`) with retry and backoff, where 0.6.0 sent one unretried `POST` per event. Update a receiver that parses each request body as one JSON object to split the body on newlines and parse each line as one record, and to deduplicate on `hash`. A receiver that accepts both forms keeps working through a rollback to 0.6.0.
- **Destination lists.** A comma in `PODIUM_AUDIT_LOG_PATH` or `PODIUM_AUDIT_SINK` separates destinations, so a file path that contains a comma cannot be named. 0.6.0 reads the value as one destination, so reduce it to one before rolling back.
- **New startup refusals.** An audit destination URL that carries userinfo refuses startup with `config.audit_sink_unavailable`; move the credential into an `Authorization` line of the `PODIUM_AUDIT_HEADERS_FILE` file. A file destination that cannot be opened for appending refuses startup whatever the anchoring setting, and so does an audit URL that does not parse, where 0.6.0 disabled audit. 0.6.0 ignores `PODIUM_AUDIT_HEADERS_FILE`, so restore the userinfo before rolling back if the receiver requires it.
- **MCP server local events.** `podium-mcp` local audit events no longer carry `context.source`, and `caller.client.name` is `mcp` on them instead. Change a consumer that filters on `context.source` to filter on `caller.client.name`. After a rollback, 0.6.0 writes `context.source` again.
- **Record schema.** Every record carries `schema_version: 1`. A consumer that rejects unknown members accepts it once it reads the published v1 JSON Schema.

### Audit chain verification after a rollback

0.7.0 writes a `schema_version` member on every audit record and a `caller.client` member when the request names its client, and it includes both in the record's hash. A 0.6.0 process reads neither member, so it cannot recompute the hash of a record that 0.7.0 wrote.

- After a rollback to 0.6.0, the scheduled integrity pass reports a chain break at the first record 0.7.0 wrote. The pass runs on `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` when `PODIUM_AUDIT_LOG_PATH` includes a file. It logs `audit integrity ALERT: hash-chain gap detected: audit: chain broken at index <n>: Hash mismatch`, appends an `audit.gap_detected` record, and repeats both on every pass until 0.7.0 starts again or a 0.6.0 rewrite of the file removes the break.
- The chain itself is intact. Each record 0.6.0 appends links to the record before it, and the next 0.7.0 start verifies the whole file, including the records 0.6.0 wrote and its `audit.gap_detected` records.
- While 0.6.0 runs, its pass stops at the break, so no record written after the first 0.7.0 record is verified until 0.7.0 starts again.
- 0.6.0 rewrites the whole audit file when its retention pass drops a record or ages the query text of a search record, and when it erases a user's records. The retention pass runs at startup and on every `PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS` tick, and the query text of a search record ages at 7 and at 30 days, so steady search traffic makes a rewrite likely within days of the rollback. 0.6.0 reads each record without `schema_version` and `caller.client`, so the rewrite removes both members from every record 0.7.0 wrote and chains the file again over what remains. From then on the integrity pass reports no break and the alert stops, and the `grep` below finds no record that 0.7.0 wrote before the rollback. The removal is permanent, and a later 0.7.0 start verifies the rewritten file without restoring the members.
- The copies that an HTTP, OTLP, or `stdout` destination received before such a rewrite keep both members, and their `hash` values no longer match the records in the file. To keep `caller.client` for the period 0.7.0 ran, copy the audit file before the rollback, or keep the records a shipped destination received.
- To confirm that a reported break is this boundary, compare its index with the first record that carries `schema_version`. `grep -n -m1 '"schema_version"' <audit log file>` prints that record's line number, which is the index plus one in a file with no blank lines. The rollback does not explain a break at any other index. Treat such a break as an audit chain gap, as [Troubleshooting](troubleshooting) describes.
- A receiver that checks the `prev_hash` links of a shipped stream is unaffected, because the links do not depend on the members a record carries.

### Operator token in public mode

A 0.7.0 registry in public mode refuses the following with `403 auth.forbidden` unless the request carries `PODIUM_OPERATOR_TOKEN`: `podium admin reembed`, `podium admin rebuild-overlap`, `podium admin erase`, and every layer write (`podium layer register`, `unregister`, `update`, `restore`, `reorder`, `reingest`, and `watch`). A 0.6.0 registry in public mode admitted all of them from any caller.

- Before the upgrade, generate a secret of at least 32 characters, for example with `openssl rand -hex 32`, and set it as `PODIUM_OPERATOR_TOKEN` on the registry. A shorter value, or one that contains whitespace or a control character such as the trailing newline of a secret file, refuses startup with `config.invalid`.
- Set the same value in the environment of whoever runs those commands. The CLI sends it when it is set.
- A public-mode registry with no token keeps serving every read, including the layer list, and serves the layers its startup configuration declares. It refuses every layer change over the API. The overlap recomputation the registry runs on its own does not go through these endpoints and is unaffected. A re-embed after a model change runs only through `POST /v1/admin/reembed`, which `podium admin reembed` calls, so it needs the token.
- The web UI sends no token, so in public mode it no longer offers layer controls.
- An inbound webhook delivery cannot carry the token, so in public mode a webhook-triggered reingest of a layer that names a filesystem path on the registry host is refused. A webhook for a `git` layer is unaffected.
- A registry with no identity provider and not in public mode does not read the variable.

### Non-loopback bind without an identity provider

A 0.7.0 registry with no identity provider, and not in public mode, refuses a non-loopback bind at startup with `config.public_bind_refused`, naming the address. Such a registry admits every caller as an admin, including layer writes, registrations that read a path on the registry host, and erasure. A 0.6.0 registry started on any address.

- When something other than the registry limits who can reach the address, such as a container whose port is published on the host's loopback interface or an authenticated reverse proxy, set `PODIUM_ALLOW_PUBLIC_BIND=true` or pass `--allow-public-bind`.
- Otherwise configure an identity provider, or bind `127.0.0.1`.
- The Docker Compose evaluation stack in the repository sets `PODIUM_ALLOW_PUBLIC_BIND: "true"`. A Helm install that empties `config.identityProvider.type` needs `config.allowPublicBind=true`.
- A rollback to 0.6.0 removes the refusal. The setting is harmless on 0.6.0, which reads it only in public mode and under `trusted-headers`.

### Unparseable registry.yaml

A 0.7.0 registry refuses to start with `config.invalid` when its `registry.yaml` exists but cannot be read or does not parse, and the message names the file and the line. A 0.6.0 registry logged `warning: ignored registry.yaml` and started on the defaults, without the identity provider, layers, and store settings the file declares. Before the upgrade, run `podium config show --server` with 0.7.0 against the file: a file that would be refused prints the same message as a warning.

### Artifacts removed from a layer's source

A 0.6.0 registry kept serving an artifact after its folder was deleted from the layer's source. A 0.7.0 registry soft-deletes such an artifact at the next completed ingest of its layer, and `podium layer reingest` prints `removed: <id>` for it. [Remove an artifact](../../managing/lifecycle#remove-an-artifact) describes the behavior.

- The first ingest of each layer after the upgrade removes every artifact whose folder was deleted while 0.6.0 served it. Review the `removed:` lines of that ingest, or the startup log's `removed=` count for a layer ingested at startup.
- Restore an artifact removed by mistake by putting its folder back and reingesting within `PODIUM_LAYER_RECOVERY_DAYS`, 30 days by default.
- A rollback to 0.6.0 keeps the soft-deleted versions hidden, because 0.6.0 also hides a soft-deleted row, and a 0.6.0 ingest does not restore them. Restore them with 0.7.0 before the window ends.
- A 0.7.0 process adds the `ingest_claim` column to `layer_configs` in the SQLite database and in each Postgres org schema if it is absent, and each ingest writes its claim on the layer there. The change is additive. A 0.6.0 process ignores the column and removes nothing, so a rollback, or a 0.6.0 replica during a rolling upgrade, leaves it unused.

### `command` and `context` removal

0.7.0 removes the `command` and `context` artifact types. Ingest rejects an `ARTIFACT.md` whose `type` is either of them with `ingest.invalid_artifact` and `details.constraint: "type_removed"`, and `podium artifact scaffold` refuses both types. A command becomes a skill whose `invocation` is `user`, and reference material moves into a skill or a `DOMAIN.md` body. [Artifact types](../../authoring/artifact-types#removed-types) describes the replacements. The notes below cover converting a package and the stored rows and caches that an earlier release wrote.

#### Retired command and context rows

The registry retires every stored row of either type, under the mechanism that retires a row whose `ARTIFACT.md` declares `extends:`:

- No read path serves a retired row. `latest` resolves to the newest other served version of the ID.
- An ID whose every row is retired answers `registry.not_found`.
- A retired row still counts toward quota and still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation`.

#### Converting a package by hand

Podium ships no conversion command. Convert each package whose `ARTIFACT.md` declares `type: command` or `type: context` with these steps:

1. Write a `SKILL.md` in the package directory. Its `name` is the directory name, its `description` and `license` are the `ARTIFACT.md` values, and its body is the `ARTIFACT.md` body, unchanged. A directory name that fails the [name constraints](../../authoring/frontmatter-reference#name-constraints) needs a rename, which changes the artifact ID.
2. Set `metadata.version` to the next minor version after the `ARTIFACT.md` `version`, so `1.2.3` becomes `1.3.0`. The retired row keeps its `(id, version)`, so ingest rejects other bytes at that version with `ingest.immutable_violation`.
3. For a former command, set `metadata.invocation: "user"`.
4. Move each of `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, and `release_notes` into `metadata`, in the encoding that [Podium fields in SKILL.md metadata](../../authoring/frontmatter-reference#podium-fields-in-skillmd-metadata) states. A value that the encoding cannot carry, such as a tag that contains a comma, stays in `ARTIFACT.md`.
5. Rewrite `ARTIFACT.md` as a skill overlay. Set `type: skill`, and delete `name`, `version`, `description`, `license`, the moved fields, and `expose_as_mcp_prompt`, which no Podium reader reads. Replace the body with the pointer comment `<!-- Skill body lives in SKILL.md. -->`. Every other field stays.
6. Delete `ARTIFACT.md` when it holds no field other than `type`, unless a directory between the package and the layer root is itself a package. In that case, keep `ARTIFACT.md` with `type: skill` and the pointer comment. Below another package, a directory that holds a `SKILL.md` and no `ARTIFACT.md` is a bundled resource of that package, so deleting the file removes the skill from the catalog and adds its files to the enclosing artifact. The enclosing artifact's content hash then changes, so re-ingesting it at an unchanged declared version fails with `ingest.immutable_violation`.
7. Leave the bundled resources at their paths. Run `podium lint` on the layer, and commit and re-ingest once lint exits 0.

#### Consumer upgrade order

Upgrade every `podium-mcp` and every `podium sync` consumer to 0.7.0 before ingesting converted user-invoked skills. The adapters run in the consumer, and a 0.6.0 consumer materializes such a skill without the user-only control. The registry version does not affect the control, because a 0.6.0 registry accepts the converted `SKILL.md` and serves its bytes unchanged.

#### Rolling back to 0.6.0

A 0.6.0 registry serves the retired command and context rows again. A 0.7.0 consumer still applies each converted skill's user-only control, because the adapter reads `invocation` from the served bytes. It materializes a served command or context row as an extension type: the `none` adapter writes the canonical layout, the `claude-code` adapter writes it under `.claude/podium/<artifact-id>/`, and the other adapters write nothing. A consumer rolled back to 0.6.0 materializes converted user-invoked skills as model-invocable skills.

#### Clients that loaded a retired command or context

Retiring an artifact's newest rows lowers the version `latest` resolves to. A `podium-mcp` that loaded a retired version as `latest` refuses the older one with `materialize.stale_resolution`.

1. Stop every MCP server that uses the cache directory.
2. Run `podium cache reset-revisions <id>` for each ID whose `latest` fell back to an older served row and was not re-ingested at a newer version.
3. For a `podium-mcp` in `offline-first` or `offline-only`, also run `podium cache prune --days 0`, because those modes keep serving the cached command or context body. That command leaves the revision marks in place.

#### Claude Cowork

The `claude-cowork` adapter writes nothing for any type. A `podium sync` or `load_artifact` that targets `claude-cowork` fails with `materialize.untranslatable` for every artifact, including a former `context` artifact, which 0.6.0 materialized to `.podium/context/<artifact-id>/`. A Cowork user obtains the artifacts by importing the published Claude marketplace.

## 0.6.0: `extends:` removal

0.6.0 removes `extends:`. Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, and every same-ID collision between registry-side layers is rejected with `ingest.collision`. The sections below apply to a store that holds rows an earlier release ingested from an `ARTIFACT.md` that declares `extends:`. This note calls such a row a retired row.

### What readers see

- No read path serves a retired row. `latest` resolves to the artifact's most recent other version.
- An ID whose retired row overlaid a lower layer's artifact serves the lower layer's copy when no other layer holds a served row of the ID.
- An artifact with no other version answers `registry.not_found`.
- A retired version's `delegates_to` and `mcpServers` references stay in impact analysis while another version of the artifact is served.

### Ingesting the ID again

A retired row is not a collision:

- When only one layer holds a served row of the ID, that layer ingests new versions of the artifact ID without `ingest.collision`.
- Any layer can ingest an ID with no served row without `ingest.collision`.
- The retired row still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation`.

### Copies admitted from several layers

An earlier release also admitted a plain copy of the ID from a further layer while an overlay row was stored. When served rows of the ID remain in two or more layers, `latest` serves the most recently ingested of them. Every later ingest of the ID from those layers is rejected with `ingest.collision`, including a re-ingest of unchanged bytes.

To recover, keep one of those layers:

1. Remove the declaration of each layer to drop from the registry configuration. A declared layer is re-seeded at each start.
2. Unregister all but one of those layers with `podium layer unregister`. Unregistering soft-deletes every row of each unregistered layer, so its other artifacts stop serving too.
3. Wait until the recovery window ends and the purge removes the soft-deleted rows.
4. Register a removed source again, without the artifact, and declare it again if it was declared.

Before the purge, the soft-deleted rows still hold their artifacts' versions. A registration under any layer ID then serves none of the source's artifacts at an unchanged version, even when the ingest reports them accepted, and it rejects a skill with no declared version as `auto_version_duplicate_content`.

### Clients that loaded a retired version

Retiring an artifact's newest rows lowers the version `latest` resolves to. A `podium-mcp` that loaded a retired version as `latest` refuses the older one with `materialize.stale_resolution`.

1. Stop every MCP server that uses the cache directory.
2. Run `podium cache reset-revisions <id>`.
3. For a `podium-mcp` in `offline-first` or `offline-only` that cached a merged artifact, also run `podium cache prune --days 0`. That command leaves the revision marks in place.

## 0.5.0: rewrite of stored content hashes

0.5.0 changes how the content hash is computed, so the stored content hash of every row changes. Every registry process on 0.4.0 stops before the first process on 0.5.0 starts, and the store is backed up after the stop. Restoring that backup is the only route back to 0.4.0.

Where the rewrite runs depends on the store:

- **The SQLite store in the key file's directory,** which is the default single-node layout under `~/.podium/standalone/`, is rewritten by the first start of 0.5.0. Stop the registry, back up the directory, replace the binary, and start it.
- **Every other store** is rewritten by `sign-stored-rows` before the first start, in either signing mode, as [Releases that migrate stored values](upgrading#releases-that-migrate-stored-values) describes. 0.4.0 stored every row unsigned by default, so with signing on, the dry run and the run pass `--include-unsigned` to attest the unsigned rows the dry run lists.
- **A Helm chart deployment** runs `sign-stored-rows` through the chart's migrate Job, as [Upgrading the chart from v0.4.0](#upgrading-the-chart-from-v040) describes.
- **A Docker Compose evaluation stack** follows [Upgrade a Docker Compose evaluation stack](#upgrade-a-docker-compose-evaluation-stack).

[Stores that need `sign-stored-rows` before a start](signing-keys#stores-that-need-sign-stored-rows-before-a-start) states which stores a start refuses and what a refused start leaves behind. The 0.5.0 entry of `CHANGELOG.md` also states the order in which consumers roll.

### Upgrading the chart from v0.4.0

This procedure applies to a clustered deployment whose store a v0.4.0 registry wrote. It moves the store onto this release through a migrate Job that runs before any pod of this release serves. A deployment already past this migration follows [Later upgrades](#later-upgrades).

#### How the upgrade works

A registry pod over a store that a v0.4.0 registry wrote, with no record that the rewrite of stored content hashes has completed, exits at start in either signing mode and names `sign-stored-rows` in its log. [Stores that need `sign-stored-rows` before a start](signing-keys#stores-that-need-sign-stored-rows-before-a-start) states what such a start leaves behind. A serving render over an unmigrated store therefore starts no pod that serves or rewrites, and its rollout does not progress. A store that holds no manifest row records the rewrite at its first start.

The rewrite requires every registry process on the previous release to be stopped first. The chart runs it as a Kubernetes Job while the Deployment is held at zero replicas, so no probe restarts the pass and no v0.4.0 pod writes beside it. The procedure runs the Job before any pod of this release starts, with signing on and with signing off. The Job runs `podium-server sign-stored-rows`, which performs the whole rewrite when the store holds no record of it, and records its completion. Registry pods that start afterwards find the record and skip the pass.

The chart renders the Job as a Helm `post-install` and `post-upgrade` hook when `migration.mode` is `dry-run` or `run`. Render-time checks, which read values only, refuse each step that would break the procedure:

- A `dry-run` or `run` render requires `replicaCount=0` and `migration.previousImage`.
- It refuses an image equal to `migration.previousImage`, because the v0.4.0 `podium-server` ignores its arguments, and a Job on that image would start a full v0.4.0 registry that never exits.
- With `signing.mode=none` it requires `migration.includeUnsigned=false`.
- A `run` render requires `migration.planDigest`, the value on the reviewed dry run's last line.

The chart reads nothing from the cluster, so the binding between the reviewed dry run and the run lives in the command. Given the plan digest, the run refuses with exit status 3 and writes nothing when any value the digest covers differs from the dry run's. The digest covers the completion-record mode, the `--include-unsigned` setting, the signing and verification `key_id`s, and each covered row's tenant, ID, version, stored hash, outcome, target, sign decision, and signature state. It covers every row except a row the dry run reports as `class=migrated`: one whose stored bytes reproduce its stored hash, whose signature is absent or verifies under the signing key (with signing off, whose signature is not checked), and that the run neither writes nor signs. The digest does not detect a store, DSN, bucket, or Secret change that leaves those values identical.

Nothing checks that registry processes are stopped, so step 2 waits for them. A registry process on the same store under another release name, in another namespace, or outside Kubernetes is outside that wait, so stop every such process before step 3. A process that writes a row the digest covers between the dry run and the run changes the plan, and the run refuses. A process that writes after the run builds its plan leaves its rows at the previous content hash, and admission refuses them with `materialize.content_hash_mismatch`.

Every step below passes the same values file with `-f` and sets its per-step values with `--set`. No step passes `--reuse-values`, so no per-step value carries into the next command. No step passes `--rollback-on-failure` (Helm v4) or `--atomic` (Helm v3), because a rollback reinstalls the v0.4.0 binary over a rewritten store. The only route back to v0.4.0 is the backup restore that [Rollback](#rollback) describes. Helm waits on a hook Job only up to `--timeout`, whose default is 5 minutes. Every step that runs the Job therefore passes a `--timeout` larger than the time the pass takes, and larger than `migration.activeDeadlineSeconds` when that value is set.

The commands assume the release `podium` in the current namespace, the signing Secret `podium-signing-key`, and the values file `podium-values.yaml`.

#### Upgrade with signing on

**Step 1.** Prepare the values file and the signing Secret.

```bash
helm get values podium -o yaml > podium-values.yaml
```

`helm get values` returns the v0.4.0 image. Edit `podium-values.yaml` as follows:

- Set `migration.previousImage` to that image, such as `ghcr.io/lennylabs/podium-server:v0.4.0`. A migration render refuses to run the Job on the image `migration.previousImage` names.
- Set `image.repository` and `image.tag` to this release's published image.
- Add `signing.secretName: podium-signing-key`.

Create the signing Secret as [Deploy the registry](../modes/clustered#2-deploy-the-registry) shows. With the chart's `stdout` default for `config.auditLogPath`, the `artifact.signed` audit events the run appends for every row it signs reach the Job's container log beside its `plan:`, `dry-run:`, and `rehash:` lines, and each audit line begins with `{`. An `http(s)` or `otlp+http(s)` value ships those events to a receiver instead.

Note the current revision, which is the v0.4.0 revision that [Rollback](#rollback) returns to, and lift Helm's revision history limit for the rest of the procedure:

```bash
helm history podium --max 1
export HELM_MAX_HISTORY=0
```

Helm keeps 10 revisions per release by default and prunes the oldest on each upgrade. Each step that renders records a revision, and so does a failed dry run or run, so a procedure with retries can prune the v0.4.0 revision before a rollback needs it. `HELM_MAX_HISTORY=0` sets the default of `--history-max` to no limit for every `helm upgrade` in the shell.

**Step 2.** Stop every registry process, then back up the store.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0
kubectl wait --for=delete pod --timeout=10m \
  -l app.kubernetes.io/name=podium,app.kubernetes.io/instance=podium \
  --field-selector=status.phase!=Succeeded,status.phase!=Failed
```

A scaled-down Deployment stops counting a pod while the pod is still in its termination grace period and can still write to the store, so the `kubectl wait --for=delete` line waits for the pods themselves. The field selector skips pods in a terminal phase, such as evicted pods, which a scale-down never deletes. Back up Postgres and the object store (the bucket or the objects volume) once the wait returns. That backup holds every write the v0.4.0 binary made.

**Step 3.** Run the dry run, capture its log, and review it.

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.mode=dry-run --timeout 2h &
helm_pid=$!
until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
  sleep 2
done
if kubectl get job podium-podium-migrate >/dev/null 2>&1; then
  kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > dry-run.log
fi
wait "$helm_pid"
```

The first line removes a Job a previous attempt left, so the capture reads the new Job's log. The loop also ends when `helm` exits, because a render check that refuses the step exits before any Job exists. The last line returns the exit status of `helm upgrade`. A non-zero status with a render check's refusal on the terminal means the step did not run. The log is streamed to a local file while the Job runs, because the kubelet rotates container logs (at 10 MiB by default), and a `kubectl logs` after the Job ends returns only the current file. Check that the capture is complete:

```bash
grep -cE '^dry-run: .* class=' dry-run.log
grep -E '^dry-run: [0-9]+ row\(s\) planned' dry-run.log
grep -E '^dry-run: .* class=' dry-run.log | grep -vc ' class=migrated '
grep -E '^dry-run: plan digest ' dry-run.log
```

A complete capture meets these conditions:

- It holds one row line per planned row, so the first count equals `N` in the `dry-run: N row(s) planned` line.
- It ends with the `dry-run: plan digest <digest> over <K> row(s)` line.
- The third count, which leaves out the `class=migrated` rows the digest does not cover, equals `K`.

When a count differs or the digest line is absent, rerun this step. Each row line reads `<tenant>/<artifact>@<version> class=<class> target=<hash> write=<bool> sign=<bool> signed_by=<state> stored=<hash>`, and the rows are listed in ascending order of tenant, artifact ID, and version. The `dry-run: plan mode=... include_unsigned=... signing_key=... verify_keys=...` line after them is the plan header the digest also covers. Then review `dry-run.log`:

- Each row with `sign=true` and `signed_by=unsigned` is an unsigned row that step 4 attests under `--include-unsigned`. Confirm that each such row is one the deployment stored. A row the operator cannot account for stops the upgrade.
- No row reports `class=body_unavailable`. Such a row means an object-store read failed, and the run would leave the completion record unset.
- No row reports `class=signature_unverified` or `signed_by=unverified`. Such a row means that the signing Secret does not hold the key that signed the stored rows, neither as its key nor as a `verify:` key. The run would leave every such row at its previous content hash, record completion, and exit 0, and the registries would then refuse those rows. Fix the Secret and rerun this step.
- No row reports `class=unreproducible` or `class=body_missing`. The run leaves such a row at its previous content hash and still records completion, so account for each one before step 4.
- The `would be signed` count in the summary line matches the rows reviewed.

Keep `dry-run.log` as the record of the reviewed plan. The `sha256:` value on its last line is the plan digest that step 4 passes as `migration.planDigest`.

`migration.includeUnsigned` is true by default, so the dry run and the run both pass `--include-unsigned`. A deployment that does not attest its unsigned rows sets `migration.includeUnsigned=false` for both steps. Those rows then stay unsigned and are refused with `materialize.signature_missing` until they are signed or republished. The run repeats the dry run's setting, because the plan digest covers it.

**Step 4.** Run the reviewed plan, and capture its log.

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0 \
  --set migration.mode=run --set migration.planDigest=<digest> --timeout 6h &
helm_pid=$!
until kubectl get job podium-podium-migrate >/dev/null 2>&1 || ! kill -0 "$helm_pid" 2>/dev/null; do
  sleep 2
done
if kubectl get job podium-podium-migrate >/dev/null 2>&1; then
  kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > run.log
fi
wait "$helm_pid"
```

Replace `<digest>` with the `sha256:` value from the last line of `dry-run.log`. The Job runs `sign-stored-rows --plan-digest=<digest>`, with `--include-unsigned` when `migration.includeUnsigned` is true. The log carries the `rehash:` summary line and, with `migration.includeUnsigned` true, a `rehash: 0 unsigned left` line. The command exits 0 only when the rewrite is complete and its completion is recorded.

When the plan the run builds has a digest other than `<digest>`, the command exits with status 3 before any write, and `helm upgrade` exits non-zero. It writes no row, no audit event, and no completion record. It prints its own plan as `plan:` lines in the dry-run format, one per planned row, followed by its header and a `plan: plan digest <digest> over <K> row(s)` line. The log is streamed to `run.log` while the Job runs for the same reason as step 3's. Check that `run.log` holds the `plan: plan digest` line, then compare the two plans:

```bash
grep -E '^plan: plan digest ' run.log
grep -E '^(dry-run|plan): plan mode=' dry-run.log run.log
diff <(sed -n 's/^dry-run: \(.* class=.*\)/\1/p' dry-run.log) <(sed -n 's/^plan: \(.* class=.*\)/\1/p' run.log)
```

The second command prints both plan headers, and the `diff` lists the rows that changed since the dry run. The two digest lines differ by construction, because the refusal fires only when they do. Account for each change, then rerun step 3 and run this step with the new digest. When the command exits with status 1, read [Recovering from a failed run](#recovering-from-a-failed-run).

**Step 5.** Serve.

```bash
helm upgrade podium ./deploy/helm/podium -f podium-values.yaml
```

The Deployment scales to `replicaCount`. The pods find the completion record and skip the pass, so the chart's startup budget covers their start. A pod over a store without the record exits at start and names `sign-stored-rows` in its log. In that case, return the release to zero replicas as step 2 shows, and rerun steps 3 and 4.

#### Upgrade with signing off

A deployment with `signing.mode=none` follows [Upgrade with signing on](#upgrade-with-signing-on), with `signing.mode: none` and `migration.includeUnsigned: false` in the values file in place of the signing Secret. The chart renders `PODIUM_SIGN=none` into the migrate Job, so the command rewrites without signing. The command refuses `--include-unsigned` with signing off, which is why the value is false.

The dry run reports every row with `sign=false`, a signed row with `signed_by=unchecked`, and a header with `signing_key=-`. A dry run that exits 1 naming `PODIUM_SIGN=none` means a signed row needs its hash rewritten. The deployment then supplies the key that signed it, by turning signing on with the signing Secret. As with signing on, a serving render that skips the migrate Job starts pods that exit at start and name `sign-stored-rows`.

#### Install over an existing v0.4.0 store

A new release name, a new namespace, or a reinstall after `helm uninstall` over a store v0.4.0 wrote is an upgrade from the store's point of view. The objects volume carries `helm.sh/resource-policy: keep`, and the bundled Postgres keeps its volume claim, so both survive `helm uninstall`.

Stop every v0.4.0 process that uses the store. Write `podium-values.yaml` and create the signing Secret as step 1 of [Upgrade with signing on](#upgrade-with-signing-on) describes, with two differences. Skip `helm get values`, because no release exists to read the values from, and set `migration.previousImage` to the image the v0.4.0 registry ran. With signing off, the file carries `signing.mode: none` and `migration.includeUnsigned: false` in place of the signing Secret. Install at zero replicas on this release's image:

```bash
helm install podium ./deploy/helm/podium -f podium-values.yaml --set replicaCount=0
```

Then follow [Upgrade with signing on](#upgrade-with-signing-on) from step 2, with either signing mode. An install at zero replicas starts no registry pod. Adding `--set migration.mode=dry-run` to the install, in place of the separate step, runs step 3's dry run as a `post-install` hook. Take the backup before such an install, and capture its log as step 3 does, with `helm install` in place of `helm upgrade`.

#### Recovering from a failed run

A run Job that fails leaves the completion record unset and the Deployment at zero replicas, and `helm upgrade` exits non-zero. Until a later run succeeds, a serving render starts pods that exit at start.

| Symptom | Cause | Fix |
|:--|:--|:--|
| The run exits with status 3, and its log carries `plan:` lines with `class=body_unavailable` rows and no `rehash:` summary or unsigned-left line. | An object-store outage began after a clean step-3 dry run and changed the plan. | Fix the cause and rerun steps 3 and 4. |
| A rerun of step 3 still lists `class=body_unavailable` rows while the object store's own status reports it healthy. | Object-store reads that do not finish within the migration read timeout, or an object-store fault its status does not report. | Raise `config.migrationObjectReadTimeout`, which sets `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` (default `30s`) on the Job pod, in `podium-values.yaml`. Otherwise restore the object store or correct its values or Secret. |
| A run whose digest matched exits with status 1. | The run held a row back or failed a write. | With signing on, follow the log's `rehash: N unsigned left; run sign-stored-rows --include-unsigned --dry-run, review it, and pass its plan digest to sign them` line, which names steps 3 and 4. When rows point at the wrong object-store root, correct the object-store values or Secret. |
| The Job runs out of memory or reaches `migration.activeDeadlineSeconds`. | The pass needs more memory or time. | Raise `migration.resources.limits.memory` or the deadline, and rerun steps 3 and 4. |
| Helm stops waiting at `--timeout` while the Job still runs. | The pass takes longer than `--timeout`. | Wait for the Job, as described below. |
| Registry pods exit at their next start after a backup restore that followed step 5. | The restored store lacks the completion record. | Hold the release at zero replicas, wait for its pods to be deleted as step 2 of [Upgrade with signing on](#upgrade-with-signing-on) shows, and rerun steps 3 to 5. A restore that keeps the v0.4.0 release instead follows [Rollback](#rollback). |

The command reads object storage only while it builds its plan. It logs the per-row `left at its stored content hash` lines, the `rehash:` summary line, and the unsigned-left line only while it writes. Neither the refused run's log nor the dry run's carries the per-row read error, so neither shows whether a read timed out. Each unavailable read still spends the object-store client's retry budget, so an outage lengthens the refused run by that budget for every object-held row. Each write is a compare-and-swap on the stored hash and signature, and a rerun plans every row again.

When Helm stops waiting while the Job still runs, the Job keeps running. Wait for it with `kubectl wait job/podium-podium-migrate --for=condition=Complete --timeout=6h`. When that wait times out, read `kubectl get job podium-podium-migrate -o jsonpath='{.status.failed}'`: a value of `1` or more means the run failed, and the rows above apply. Run step 5 only after the wait returns `condition met`.

#### Service mesh sidecars

On a service mesh that injects a sidecar into the Job pod, a sidecar that keeps running after the command exits holds the Job open until Helm times out. Native sidecars, on Kubernetes 1.28 or later with the mesh's native-sidecar mode, stop with the main container and need no change.

Otherwise, set the mesh's opt-out in `migration.podAnnotations`, such as `sidecar.istio.io/inject: "false"` or `linkerd.io/inject: disabled`, when the store is reachable without the mesh. Istio reads the `sidecar.istio.io/inject` pod label ahead of the annotation of the same name. When `podLabels` enables injection through that label, set the opt-out label in `migration.podLabels` instead. The Job pod takes `podLabels` and `podAnnotations`, and `migration.podLabels` and `migration.podAnnotations` win over them per key. The chart sets no opt-out by default, because a mesh that enforces mTLS to Postgres or to the object store would then block the Job.

#### Rollback

`helm rollback` after step 4 reinstalls the v0.4.0 binary over rows it cannot read, and the chart cannot block it. Roll back only through the step 2 backup:

1. Stop the registry.
2. Restore Postgres and the object store from that backup.
3. Run `helm rollback podium <revision>` with the v0.4.0 revision noted in step 1.
4. Delete the migrate Job:

```bash
kubectl delete job -l app.kubernetes.io/instance=podium,app.kubernetes.io/component=migrate
```

The hook Job is outside the release manifest, so it also remains after `helm uninstall`, and the same command removes it.

#### GitOps controllers

The chart reads nothing from the cluster, so a controller that renders it with `helm template`, such as Argo CD, renders the manifests `helm upgrade` renders from the same values. Argo CD runs the migrate Job, a Helm `post-install` and `post-upgrade` hook, as a PostSync hook, and applies `before-hook-creation` to it, so each sync that renders the Job replaces the previous one. Run the upgrade as the following value commits, and let each sync finish before the next commit:

1. Commit `replicaCount: 0` with this release's image, the signing Secret, or `signing.mode: none` with `migration.includeUnsigned: false`, and `migration.previousImage`. After the sync, run the `kubectl wait` of step 2 and take the backup.
2. Commit `migration.mode: dry-run`, and capture the Job's log to `dry-run.log` as step 3 does once the Job appears. Review it as step 3 states.
3. Commit `migration.mode: run` and `migration.planDigest: <digest>`, and stream the Job's log to `run.log`.
4. Commit the serving values, without `migration.mode` or `migration.planDigest`.

For the dry-run and run commits, run the first line below before pushing the commit, so the capture reads the new Job rather than the previous one. Run the other two lines after pushing it. The run commit writes the log to `run.log` in place of `dry-run.log`:

```bash
kubectl delete job podium-podium-migrate --ignore-not-found --wait=true
until kubectl get job podium-podium-migrate >/dev/null 2>&1; do sleep 2; done
kubectl logs -f job/podium-podium-migrate --pod-running-timeout=10m > dry-run.log
```

A sync that repeats a hook reruns the committed step. A repeated dry run writes nothing, and a repeated run after a successful one exits with status 3 and writes nothing, because the recorded completion changes its plan.

#### Later upgrades

A later upgrade runs `helm upgrade podium ./deploy/helm/podium -f podium-values.yaml` with the new image tag. No Job renders, and the default rolling update applies.

- A release whose changelog names a new stored-value migration follows this procedure again.
- A release whose changelog names a rollback floor rolls with the default rolling update from a version at or above the floor. From a version below the floor, scale the Deployment to zero replicas before the upgrade, and pass neither `--rollback-on-failure` nor `--atomic`. [Rollback floor for the layer credential columns](#rollback-floor-for-the-layer-credential-columns) states the floor for 0.7.0.

Key rotation runs `sign-stored-rows` on a serving pod with `kubectl exec`, as [Rotating the signing key across replicas](signing-keys#rotating-the-signing-key-across-replicas) shows. A rerun of the migration command after step 5 takes the same route, with a dry run first and its plan digest passed to the run:

```bash
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned --dry-run
kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows --include-unsigned --plan-digest=<digest>
```

Over the migrated store, the rerun rewrites no content hash. `--include-unsigned` attests every unsigned row the reviewed dry run lists, such as a row a failed run held back. Review each row the dry run lists with `signed_by=unsigned` and `sign=true`, and pass the `sha256:` value from its last line as `<digest>`. The run refuses with exit status 3, writing nothing, when its plan differs from the reviewed one. A deployment that set `migration.includeUnsigned=false`, including every deployment with `signing.mode=none`, drops `--include-unsigned` from both commands, and its run then takes `--plan-digest` optionally.

### Upgrade a Docker Compose evaluation stack

Signing off does not exempt the [Docker Compose evaluation stack](../modes/docker-compose#evaluating-with-docker-compose) from the stored-row migration of 0.5.0. Its store is Postgres, which a start does not rewrite, so a stack whose `postgres_data` volume holds rows a v0.4.0 registry wrote is refused at the upgraded registry's first start, as [Stores that need `sign-stored-rows` before a start](signing-keys#stores-that-need-sign-stored-rows-before-a-start) describes.

Upgrade such a stack by stopping the registry, rebuilding its image, running `sign-stored-rows` once through the registry service, and then starting the stack:

```bash
docker compose stop registry
docker compose build registry
docker compose run --rm registry sign-stored-rows --dry-run
docker compose run --rm registry sign-stored-rows --plan-digest=<digest>
docker compose up -d
```

Review the dry run's report before the run, and pass the `sha256:` value from its last line, `dry-run: plan digest <digest> over <K> row(s)`, as `<digest>`. The service environment carries `PODIUM_SIGN: "none"`, so the command rewrites without signing and needs no other setting. A stack started on a new volume holds no manifest row at its first start and records the migration then.

### Layers and webhooks registered before 0.5.0

From 0.5.0, each layer request on a multi-tenant registry acts in the tenant the caller's organization selects. Layers and webhooks registered before 0.5.0 need the following changes:

- **Layers.** On a multi-tenant registry, layers that a caller registered before 0.5.0 remain in the default tenant, because the registry previously stored every layer there. The owner registers the layer again from the owning tenant. An admin of the default tenant, such as a bootstrap admin whose organization is `default`, removes the stale row with `podium layer unregister`, because the owner's own requests no longer reach the default tenant.
- **Git webhooks.** On a multi-tenant registry, Git layers registered before 0.5.0 need their webhook re-registered on the source repository with the URL `/v1/ingest/webhook/<tenant-id>/<layer-id>`. This includes the default tenant's layers, whose segment is the default tenant's ID rather than the name `default`. Single-tenant registries keep `/v1/ingest/webhook/<layer-id>`.
- **Outbound webhook receivers.** A `PODIUM_WEBHOOK_STORE_PATH` file written before 0.5.0 keys every receiver under a key that matches no tenant, so after the upgrade those receivers are neither listed nor delivered to. Before upgrading, record each receiver's `url`, `event_filter`, and `debounce` from `GET /v1/webhooks`, and its secret from the store file, because the API returns the secret masked. After upgrading, register each receiver again with `POST /v1/webhooks`, passing the recorded `secret`. A deployment without `PODIUM_WEBHOOK_STORE_PATH` keeps receivers in memory and re-registers them after every restart in any case.
