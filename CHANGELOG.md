# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`git_provider` on a layer** (§7.3.1): a layer names the git provider whose signature scheme verifies its inbound webhook deliveries. `POST /v1/layers` and `POST|PUT /v1/layers/update` accept `git_provider` in the request body, and a layer declared in the `registry.yaml` `layers:` block sets it under `source.git.git_provider`, beside the existing `force_push_policy`. The field was assigned nowhere before, so every inbound delivery was verified under GitHub's signature scheme and a GitLab or Bitbucket source could not be configured to deliver at all. A value that names no registered provider, and any value on a `local` source, are refused with `400 registry.invalid_argument` naming the field; an unregistered value in the declared block aborts the boot with an error naming the layer and the value. The declared key is authoritative for a declared layer, because the boot re-seeds every declared entry from the configuration, so a value set over HTTP on such a layer reverts at the next start. An empty value resolves to `github` at the point of use, so a layer stored before this change verifies as it did and no migration is required. No CLI flag sets the field.
- **`layer_capabilities` in the session posture read** (§7.3.4): `GET /v1/ui/session` reports an object naming what the requesting caller may do on the §7.3.1 layer operations on this deployment. It carries `manage_any_layer`, a boolean reporting whether the deployment's layer endpoints admit this caller on the §4.7.2 admin arm, which is the arm that decides a write on a layer the caller does not own and every operation the local-source authorization rule governs. The object and its member are always present, and the member is `false` wherever the deployment determines no capability for the request. A registry started with no identity provider configured, or one started in public mode, admits every caller on that arm and reports the member as `true` there. The value is a snapshot taken when the read was answered, so an operation a client offers on the strength of it can still be refused, and the §6.10 envelope the operation's own endpoint returns remains the authority.
- **`email` in the session posture read** (§7.3.4): `GET /v1/ui/session` reports the requesting caller's own email as the configured identity provider recorded it. The key is present only where an email resolves and is non-empty, and absent otherwise, and it belongs to the caller that asked and to no other caller. The web UI's account cluster renders that email as the signed-in reader's identity and falls back to the subject where the read carries none, so a deployment whose subject is an opaque provider identifier no longer draws a UUID there. The cluster still appears only where the read reports a subject.

### Changed

- **Control-plane JSON field names** (§7.2.1, §7.3.1, §7.3.2): the control-plane response records that reached the wire under their Go field names now carry the lower snake_case names §7.2.1 fixes. The layer object, returned by `POST /v1/layers`, `POST|PUT /v1/layers/update`, `GET /v1/layers`, `GET /v1/layers?deleted=true`, and `POST /v1/layers/reorder`, renames `ID` to `id`, `SourceType` to `source_type`, `Repo` to `repo`, `Ref` to `ref`, `Root` to `root`, `LocalPath` to `local_path`, `Order` to `order`, `UserDefined` to `user_defined`, `Owner` to `owner`, `Public` to `public`, `Organization` to `organization`, `Groups` to `groups`, `Users` to `users`, `GitProvider` to `git_provider`, `LastIngestedRef` to `last_ingested_ref`, `CreatedAt` to `created_at`, and `DeletedAt` to `deleted_at`. `force_push_policy` and `last_ingested_at` already carried snake_case names and are unchanged. The receiver object, returned by `GET` and `POST /v1/webhooks` and by `GET` and `PUT /v1/webhooks/{id}`, renames `ID` to `id`, `URL` to `url`, `Secret` to `secret`, `EventFilter` to `event_filter`, `Disabled` to `disabled`, `FailureCount` to `failure_count`, `LastDelivery` to `last_delivery`, `LastFailure` to `last_failure`, `CreatedAt` to `created_at`, and `Debounce` to `debounce`. That rename is a projection over the stored receiver, so the operator's `PODIUM_WEBHOOK_STORE_PATH` file keeps its format and an existing file is read as it was. `GET /v1/quota` reports the limits under `limits` under the names `GET /v1/admin/tenants` already reported, renaming `StorageBytes` to `storage_bytes`, `SearchQPS` to `search_qps`, `MaterializeRate` to `materialize_rate`, `AuditVolumePerDay` to `audit_volume_per_day`, and `MaxUserLayers` to `max_user_layers`. Each element of `GET /v1/admin/show-effective`'s `layers` array renames `LayerID` to `layer_id`, `Visible` to `visible`, and `Reason` to `reason`. The bulk arm of `POST /v1/admin/reembed` renames `Total` to `total`, `Succeeded` to `succeeded`, and `Failed` to `failed`, and each failure entry renames `ArtifactID` to `artifact_id`, `Version` to `version`, and `Reason` to `reason`; the endpoint's single-artifact arm was already lowercase and is unchanged. The layer object no longer carries the tenant identifier, which was the registry's own stored tenant repeated on every record of a read that is not admin-gated, and the receiver object no longer carries its `TenantID` for the same reason. `GET /v1/quota`'s top-level `tenant_id` and the `id` of each element of `GET /v1/admin/tenants` name the tenant that is the record's own subject and are unchanged. The receiver object's `debounce` is now the duration string the request body accepts, so a receiver created with `"debounce": "60s"` reads back as `"1m0s"` and that value can be sent to `PUT /v1/webhooks/{id}` without conversion, where the member was a nanosecond integer no request accepts; it is omitted on a receiver that sets no window. A read is not replayed whole, because `secret` is reported masked as `***` and the update stores whatever secret the body names, so a replayed read would overwrite the receiver's secret with the mask. The receiver object's `created_at`, `last_delivery`, and `last_failure` are emitted in UTC, where a registry process running in a non-UTC zone emitted them with that zone's offset. The layer object's `created_at` is emitted in UTC on a Postgres deployment whose session time zone is not UTC, where the driver returned it in the session zone and the standard and standalone deployments reported the same field in two forms. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, configuration key, or content negotiation restores the Go-cased keys. A client reading any of these bodies by its Go field name must be updated to the name above.
- **Admin-only registration fields** (§7.3.1): `POST /v1/layers` refuses a registration that asserts `owner`, `public`, `organization`, `groups`, or `users` from a caller the §4.7.2 admin arm does not admit, with `403 auth.forbidden` carrying `details.constraint: "admin_only_fields"` and a message naming the asserted fields. Such a registration previously discarded the assertion, stored the layer as user-defined with the implicit `users: [<registrant>]` visibility, and answered `201`. A field is asserted by its value rather than by its presence: `public` or `organization` set to true, a non-empty `groups` or `users`, and an `owner` naming a subject other than the caller's own. A false boolean, an empty array, an empty string, and an `owner` naming the caller's own subject assert nothing, so a registration that asserts none of the fields is unaffected and still answers `201`. The rule keys on the caller's admin arm rather than on the resolved layer class, so it reaches a re-registration of a stored layer the caller owns on the same terms, and a registry started with no identity provider configured, or one started in public mode, admits every caller on that arm and refuses nothing. Where a registration is on both this rule's arm and the local-source authorization rule's arm, the local-source refusal is the one returned. Operators should note that automation registering a layer as a non-admin with `--public`, `--organization`, `--group`, `--user`, or `--user-defined --owner` naming another subject exited `0` on a registration that applied none of it and now fails with a non-zero exit and that envelope. The registration succeeds once the caller holds the tenant `admin` role, or once the flag is dropped. This is a backward-incompatible change and lands in a MINOR bump. No flag, configuration key, or environment variable restores the discard.
- **Local-source layer authorization** (§7.3.1): registering a layer that names a filesystem path on the registry host, patching a stored layer's filesystem path, restoring such a layer, and reingesting one now require the tenant `admin` role. Any other caller is refused with `403 auth.forbidden` carrying `details.constraint: "local_source"`, and the refusal names no filesystem path. A `git` source whose repository string resolves to go-git's file transport names a repository path on the registry host, so it is on the same arm and a non-admin registering one is refused; a repository string naming a network endpoint is not. The classification fails closed: a repository string the registry cannot place as a network endpoint is treated as naming a host path and is on the same arm, so a stored `git` layer whose repository string go-git rejects is refused for a non-admin on register, restore, reingest, and every inbound webhook delivery until an admin takes it or its repository string is corrected to a network endpoint. A `git` source is classified on its repository string alone, so a filesystem path stored beside one places nothing on the arm. `unregister` and `reorder` name no path and are unaffected. An inbound webhook delivery triggers a reingest and takes the same arm, so on a registry that authenticates its callers a webhook-triggered reingest of a layer that names a filesystem path is refused. A registry started with no identity provider configured, or one started in public mode, authenticates no caller, so no caller holds the admin role and every operation the rule governs is admitted there as before. The default is closed: a deployment where a non-admin registers `local` layers today must grant those callers the tenant `admin` role (`PODIUM_BOOTSTRAP_ADMINS` or a per-tenant grant), or move those layers to a `git` source over a network transport, or have an admin register and reingest them. This is a backward-incompatible change and lands in a MINOR bump. No flag, configuration key, or environment variable restores the prior behavior.
- **Layer panel controls** (§13.10): the web UI renders a control that would take a §7.3.1 layer write only where the caller's reported capabilities and the target layer's own fields admit it. A control the prediction refuses is absent rather than disabled, and a control it admits is still disabled by the read-only marker as before. The reordering affordance is the exception: a move names every layer in the moved row's class block and the registry refuses the request whole, so the handles stay present and disabled and the label names the reason, `Precedence — reordering these layers requires the administrator role`. The registry remains the authority, so a refusal the panel could not predict is still reported as the endpoint's error envelope.
- **A set of accepted audiences** (§6.3.3): `PODIUM_OAUTH_AUDIENCE` and the config-file key `identity_provider.audience` configure a set of audience values the registry answers to rather than a single value. The environment variable takes a comma-separated list, and the config-file key takes a string or a list of strings; a string is one audience verbatim and is not split on any separator. Entries are trimmed, blank entries are dropped, and repeated entries are collapsed keeping the first occurrence. A token is accepted when its `aud` claim carries at least one configured value, under `oidc-jwt` and under `injected-session-token` alike, and a caller admitted under any configured value has the same effective view, the same grants, and the same audit identity. A token carrying no `aud`, an empty `aud` list, or an `aud` that is the empty string is still rejected, and a setting that resolves to no entry still fails startup with `config.oidc_jwt_audience_unset` or `config.injected_token_audience_unset`. The first configured value is canonical and is the audience the §6.3.4 browser sign-in redirect asks the identity provider for, so a deployment enabling that flow lists the value its browser client is issued first. The `oidc-jwt` startup log now names the accepted audiences beside the accepted issuers. `podium config show --server` reports the set joined with commas under `oauth_audience` and attributes the row to `registry.yaml` when the value came from the config file, where it previously reported `default`. A registry configured with one audience behaves as it did.
- **`--local` usage strings**: the `--local` flag help on `podium layer register` and `podium layer update` names the administrator role the registry now requires for a filesystem path.
- **Web UI path**: the bundled web UI is served at `/app/` instead of `/ui/`, and a registry started with `--web-ui` redirects `GET /` to it. `/ui/` is no longer served and no alias replaces it, so a reverse-proxy rule or a bookmark naming the old path must be updated. The browser-flow routes are unchanged: `/v1/ui/auth/sign-in`, `/v1/ui/auth/callback`, `/v1/ui/auth/sign-out`, and `/v1/ui/session` keep their paths, so no identity-provider client configuration and no registered redirect URI changes. A registry started without `--web-ui` answers `GET /` exactly as before.

### Fixed

- **Owner and visibility patches on a user-defined layer** (§7.3.1, §4.6): `POST|PUT /v1/layers/update` refuses a patch that asserts `owner`, `public`, `organization`, `groups`, or `users` against a stored user-defined layer, with `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"` and a message naming the asserted fields. Such a patch previously discarded the assertion, left the stored record's owner and visibility unchanged, and answered `200`, so a caller was told a widening applied that never did. A field is asserted by its value rather than by its presence: `public` or `organization` set to true, a non-empty `groups`, a non-empty `users` differing from the layer's stored `users`, and an `owner` naming a subject other than the layer's stored owner each assert the field. A false boolean, an empty array, an empty string, and a value restating what the layer stores assert nothing, so a client that reads a layer object and sends it back verbatim is admitted; the layer object carries `owner` and `users` on every layer, and a stored user-defined layer holds `users: [<owner>]`. The comparison against a stored value is exact, element for element and byte for byte, so a value differing only in element order or in surrounding whitespace asserts the field. The rule reads the stored layer's class rather than the requesting caller, so it binds every caller the layer write rule authorizes, a tenant admin included, and a registry started with no identity provider configured and one started in public mode refuse on the same terms. The refusal rejects the whole request: a `rotate_webhook_secret` carried in the same body mints no secret, no other field the same body carries is applied, no record is written, and no §8.1 audit event is emitted. A patch the layer write rule or the local-source rule refuses keeps its own `auth.forbidden` envelope, because this rule is evaluated after both. An administrator who needs the layer visible more widely re-registers its ID through `POST /v1/layers` as an admin-defined layer carrying the visibility they declare; that re-registration replaces the stored record, so the registration time becomes the time of the re-registration, the order is recomputed at the tail of the layer list, `last_ingested_ref` and `last_ingested_at` are emptied, a `git` source is issued a new inbound webhook secret that must be registered at the Git host, and the former owner regains a slot against the per-identity user-defined layer cap. A request that received `200` before receives `400` after, which reaches `podium layer update --owner`, `--public`, `--organization`, `--group`, and `--user` against a user-defined layer, and an operator on a deployment with no identity provider who corrected an `--owner` typo that way now reads the refusal and re-registers instead. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the discard.
- **Unconfined local layer path**: no authorization governed the filesystem path a layer named, so an authenticated non-admin could register a layer pointing at any directory readable by the registry process and have its contents ingested and served back. The local-source authorization rule above closes that path, and the ingest confinement below bounds what a layer that is admitted may read.
- **Ingest reads through a symbolic link leaving the layer root** (§7.3.1): an ingest that reads a layer's configured filesystem path as a directory now reads only within the directory that path resolves to, whichever caller declared the layer and in every deployment mode, including a layer the operator declares in the registry's own configuration. The confinement is not configurable. A relative symbolic link that resolves within the directory is read. A symbolic link whose target is written as an absolute path is refused whatever it resolves to, including a target inside the same layer, and such a link is repaired by rewriting it with a relative target. A read the ingest requires and cannot satisfy fails that layer's whole ingest, which reports `ingest.source_unreachable`, so no artifact and no bundled resource from that snapshot is accepted while the artifacts served before the refusal stay in place until the layer is restructured. A `DOMAIN.md` the refused cycle read before the failing read is still committed, and it emits its `domain.published` event only where that file was added or changed since the previous ingest, so a cycle the confinement refuses repeatedly over an unchanged domain emits no further event.
- **Layer list visibility**: `GET /v1/layers` reports what the calling identity may read, on both the live and the `?deleted=true` arm, and the reorder response reports the same set. A caller holding the tenant `admin` role still receives the tenant's whole layer list. Any other authenticated caller receives the layers visible to that identity. A caller the registry resolves as anonymous receives an empty list, and a caller whose credential fails verification is refused with the same `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` envelope the registry's other routes already answer for that credential. A layer outside that set is absent from the response rather than refused, and no error code reports the narrowing. A registry started with no identity provider configured, which includes public mode, returns the whole layer list to every caller as before. On upgrade, a signed-in non-admin sees fewer rows in `podium layer list` and in the web UI layer panel, and an unauthenticated caller against a registry that configures an identity provider sees none. A caller presenting a stale or forged token to `GET /v1/layers` now receives that refusal where it previously received the full list, and the same caller receives it from `POST /v1/layers/reorder` in place of the `403 auth.forbidden` the write gate answered before, because the registry now reports that it could not verify the credential before it evaluates whether that caller may write.
- **Search over an `extends:` child**: an artifact that inherited `description`, `tags`, `sensitivity`, or `search_visibility` from its parent was indexed and filtered under its own authored values, so `search_artifacts` could not find it by the description `load_artifact` served for it. Ingest now folds the pinned parent into those four indexed columns, and a search result carries the resolved sensitivity.
- **Parent disclosure in a search result**: the `frontmatter` block of a `search_artifacts` result carried the child's `extends:` line, which names a parent the caller may not be able to see. The key is now removed before the block is returned, and the block is empty when the stored frontmatter cannot be decoded.
- **Public-mode sensitivity floor over an inherited value**: a registry in public mode evaluated the floor against the sensitivity an artifact declared itself. An artifact declaring `extends:` and no `sensitivity:` was admitted and then stored at the parent's level, which is the level the floor refuses. The floor is now evaluated again on the inherited value, and such an artifact is rejected with `ingest.public_mode_rejects_sensitive`.
- **Embedding projection drift**: ingest and `podium admin reembed` composed the embedding text differently for a record holding an empty `when_to_use` or `tags` entry. Both now use one implementation, which drops the empty entry.

Operators upgrading should note that a version already ingested keeps its unfolded `description`, `tags`, `sensitivity`, and `search_visibility`. Re-ingesting unchanged bytes is counted idempotent and skipped before the write, and `podium admin reembed` reads the stored columns, so neither repairs an existing row. The repair is publishing a new version of the child, which is ingested as a new row and takes the fold. A child of a `direct-only` parent stops being indexed once republished, which brings search into agreement with what layer resolution and `load_artifact` already report for that artifact.

[Unreleased]: https://github.com/lennylabs/podium/compare/v0.3.1...HEAD

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
