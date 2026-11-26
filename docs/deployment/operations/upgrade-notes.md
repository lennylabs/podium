---
title: Upgrade notes
nav_order: 5
description: "Release-specific upgrade steps, newest release first: the 0.8.0 Git snapshot deadline, the refusal of a workspace .podium symbolic link, and the refusal of an empty or control-character visibility entry, the 0.7.0 layer credential columns, skill metadata fields in stored rows, catalog lint advisories and listing budgets, per-artifact manifest refusal and skill lint severities, the semantic overlap check, and command and context removal, the 0.6.0 extends removal, and the 0.5.0 rewrite of stored content hashes."
---

# Upgrade notes

This page lists the upgrade steps that belong to one release, newest release first. [Upgrading](upgrading) gives the general procedure that every upgrade follows. Each release's entry in `CHANGELOG.md` is the authoritative statement of its changes.

## 0.8.0 (unreleased)

The current release is 0.7.0. The notes in this section describe behavior on `main` that ships in 0.8.0.

### Git snapshot deadline

From 0.8.0, every ingest of a `git` layer clones under the deadline that `PODIUM_GIT_FETCH_TIMEOUT` or the `registry.yaml` key `git_fetch_timeout` sets, 10 minutes by default. 0.7.0 set no deadline.

- A clone that takes longer than the deadline fails that ingest with `ingest.source_unreachable`, and the message names the setting. The layer keeps serving the artifacts of its last completed ingest.
- The longest clone of a layer is the full-history clone that each ingest makes once the layer has a recorded reference, which the force-push check needs. Before the upgrade, compare that clone time for the largest `git` layer with the default, and set `PODIUM_GIT_FETCH_TIMEOUT` above it where it is longer.
- A value that is not a duration above zero, such as `0s` or `600`, refuses startup with `config.invalid`.
- A rollback to 0.7.0 needs no step. 0.7.0 does not read the variable and ignores the key.

### Workspace `.podium` that is a symbolic link

From 0.8.0, `podium sync` reads and writes its lock and `sync.yaml` only when the target's `.podium` is a directory rather than a symbolic link. A target whose `.podium` is a symbolic link, such as one that shares `sync.yaml` across Git worktrees, or whose `.podium` is not a directory, fails these commands before they write anything: `podium sync`, `podium sync override`, `podium sync save-as`, `podium profile edit`, and `podium init` in that workspace. The error names `.podium` and states that it must be a directory. 0.7.0 read and wrote through the link.

- Before the upgrade, find each target whose `.podium` is a symbolic link, replace the link with a directory, and copy `sync.yaml`, `sync.local.yaml`, and `sync.lock` into it.
- A `kind: marketplace` target whose repository commits `.podium` as a symbolic link fails its render the same way. Remove the link from the repository.
- A rollback to 0.7.0 needs no step. 0.7.0 reads the files from the directory.

### Visibility entries that are empty or hold a control character

From 0.8.0, a layer's `users` and `groups` entries are non-empty strings that contain no control character, such as a line feed, a carriage return, or a tab. 0.7.0 accepted any string, and the SQLite and Postgres stores read an entry containing a line feed back as several entries.

- Before the upgrade, check the `visibility:` block of each layer in `registry.yaml` and each `.layer-config` that a registry reads through `--layer-path` for an empty entry and for an entry with a control character. An entry written as `${ENV_VAR}` is empty when the variable is unset. A start that finds such an entry exits with `config.invalid`, and the message names the layer and the entry's position.
- A layer registration or update that carries such an entry, or an `owner` with a control character, is refused with `400 registry.invalid_argument`. A layer that 0.7.0 stored keeps its entries. Where its stored `users` or `groups` holds an empty entry, an update that sends that list back unchanged, as a client that returns the layer object it read does, is refused; send the list without the empty entry, or omit the member.
- White space around an entry is kept and matched as written, as in 0.7.0.
- A rollback to 0.7.0 needs no step.

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
