---
title: Layered composition
nav_order: 5
description: Compose one catalog from several independent sources with deterministic merge and explicit precedence. Registering layers, ordering them, and reading the composed result.
---

# Layered composition

A catalog usually draws on several sources. An org keeps shared defaults in one repo, a team keeps its own artifacts in another, and an individual keeps work in progress on their own machine. Layered composition merges those sources into one view per caller, in an order the deployment states explicitly.

[Concepts → Layer](../getting-started/concepts#layer) defines the model. This page covers the operational task: putting layers in place, ordering them, and confirming what a caller sees.

---

## Precedence

Layers form an ordered list. Two registry-side layers holding the same artifact ID is a collision that Podium rejects rather than resolving silently: a server rejects the second contribution at ingest with `ingest.collision`, and `podium lint` reports the same collision on a local catalog. The rejection names the artifact and directs the author to rename it or to remove the other copy. Replacing an artifact that another layer contributes is an explicit two-step operation: remove the lower-precedence copy or rename the higher-precedence one, then ingest the replacement. The workspace local overlay is merged on the client and replaces a registry-side artifact that carries the same ID (see [Workspace local overlay](#workspace-local-overlay)).

The order is, from lowest precedence to highest:

1. **Admin-defined layers**, in the order the deployment's layer list gives them.
2. **User-defined layers**, personal layers an authenticated user registers for themselves. The default cap is 3 per identity and it is configurable per tenant.
3. **The workspace local overlay** at `<workspace>/.podium/overlay/`, always highest.

Every tier applies the same rules. What differs is where the ordered list lives and whether visibility filters it.

| Tier | Where the layer list lives | Visibility filtering |
|:--|:--|:--|
| [Local](local) | `layer_order:` in `<registry-path>/.registry-config`, defaulting to alphabetical order by subdirectory name | None. Every layer composes. |
| [Single node](single-node) | `registry.layers:` in `~/.podium/registry.yaml`, plus layers registered at runtime | Applied when an identity provider is configured |
| [Clustered](clustered) | The tenant's layer config | Always applied |

---

## Workspace local overlay

The workspace local overlay (`<workspace>/.podium/overlay/`) is an exception to the collision rule. An overlay artifact replaces the registry-side artifact with the same ID in `podium-mcp`, in `podium sync`, and in the SDKs, and no consumer reports the replacement. The consumer applies no ingest rule to an overlay artifact and serves it as authored. An overlay `ARTIFACT.md` that carries an `extends` key is served as authored too, and the key has no effect.

The overlay is per-developer and is never ingested or shared, so a replacement affects only the developer who wrote it. Copying the artifact into a shared layer subjects it to the collision rule and to the [no-inheritance rule](#inheritance). `podium lint` reports an `ARTIFACT.md` that the no-inheritance rule refuses. `podium lint` contacts no registry, so it does not report a collision with an artifact that only a registry holds.

---

## Inheritance

No artifact inherits frontmatter, prose, or resources from another artifact.

- Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, whatever its value, with `ingest.invalid_artifact` and `details.constraint: "extends_removed"`. The refusal runs before lint, so it reports the artifact whatever lint rule the artifact also fails. A filesystem-source `podium sync` drops such an artifact and exits 1, and `podium lint` reports it as an error.
- Another artifact of the same reingest whose prose links to a refused artifact fails `lint.prose_reference`, because the refused artifact is not stored.
- An `extends` key in a skill's `SKILL.md` is an ordinary key and is not refused.
- A refused artifact collides with no other artifact, so the other copy of its ID is materialized or ingested, and no `ingest.collision` line is printed for it.
- A row an earlier release stored from an `ARTIFACT.md` that declares `extends:` is served by no read path. `latest` resolves to the artifact's most recent other version, an ID whose retired row overlaid a lower layer's artifact serves the lower layer's copy when no other layer holds a served row of the ID, and an artifact with no other version answers `registry.not_found`.
- Such a stored row is not a collision. When only one layer holds a served row of the ID, that layer ingests new versions of the artifact ID without `ingest.collision`, and an ID with no served row is ingested by any layer without it. The retired row still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation`.
- An earlier release also admitted a plain copy of the ID from a further layer while an overlay row was stored. When served rows of the ID remain in two or more layers, `latest` serves the most recently ingested of them, and every later ingest of the ID from those layers is rejected with `ingest.collision`, including a re-ingest of unchanged bytes. Unregister all but one of those layers with `podium layer unregister`, which soft-deletes every row of each unregistered layer, so its other artifacts stop serving too. Register a removed source again, without the artifact, only after the recovery window ends and the purge removes its soft-deleted rows. Before the purge, the soft-deleted rows still hold their artifacts' versions, so a registration under any layer ID serves none of the source's artifacts at an unchanged version, even when the ingest reports them accepted, and rejects a skill with no declared version as `auto_version_duplicate_content`. Remove a layer's declaration from the registry configuration before unregistering it, because a declared layer is re-seeded at each start, and declare it again only after the purge.
- A retired version's `delegates_to` and `mcpServers` references stay in impact analysis while another version of the artifact is served.
- Retiring an artifact's newest rows lowers the version `latest` resolves to, so a `podium-mcp` that loaded a retired version as `latest` refuses the older one with `materialize.stale_resolution`. Stop every MCP server that uses the cache directory and run `podium cache reset-revisions <id>`. A `podium-mcp` in `offline-first` or `offline-only` that cached a merged artifact also needs `podium cache prune --days 0`, which leaves the revision marks in place.

---

## Composing from a directory

On the local tier, each subdirectory of the registry path is a `local`-source layer, and the layer ID defaults to the subdirectory name. `.registry-config` states the order:

```yaml
# ~/podium-artifacts/.registry-config
multi_layer: true        # required; opts the directory into filesystem-registry mode
layer_order:             # optional; lowest-precedence first
  - team-shared
  - personal
```

[Local](local#directory-layout) covers the full directory layout.

The same directory serves a single-node deployment unchanged. `podium serve --standalone --layer-path ~/podium-artifacts/` ingests each subdirectory as a layer and keeps the same order.

---

## Registering layers against a server

A server-backed deployment registers each layer with its own source. Declare them in `registry.yaml`, under the top-level `registry:` mapping that every server-side key nests below:

```yaml
registry:
  layers:
    - id: org-defaults
      source:
        git:
          repo: git@github.com:acme/podium-org-defaults.git
          ref: main
          root: artifacts/
      visibility:
        organization: true

    - id: team-finance
      source:
        git:
          repo: git@github.com:acme/podium-finance.git
          ref: main
      visibility:
        groups: [acme-finance]
```

A document that starts at `layers:` parses to an empty config and the registry ignores it without reporting an error.

A `git` layer's inbound webhook deliveries are verified against one Git provider's signature scheme, which is GitHub unless the layer names another. A declared layer names it in the `source.git.git_provider` key, alongside `repo` and `ref` in the block above, with a value such as `gitlab` or `bitbucket`; a value naming no provider the registry has registered aborts startup with an error naming the layer. The declaration is the setter for a declared layer, because the registry re-seeds every declared entry from `registry.yaml` at each start and so restores the declared value over any other. A layer registered at runtime names the provider in the `git_provider` field of the `POST /v1/layers` and `POST|PUT /v1/layers/update` request body, which is sent with an HTTP client such as `curl`. Neither `podium layer register` nor `podium layer update` carries the field, and no CLI flag sets it. The field is documented in the [HTTP API reference](../reference/http-api#layer-management).

A declared `git` layer names its credential in the `source.git.username` and `source.git.token` keys, with the token written as an environment variable reference:

```yaml
registry:
  layers:
    - id: team-private
      source:
        git:
          repo: https://git.acme.com/acme/private.git
          ref: main
          username: podium-ingest
          token: '${GITLAB_TOKEN}'
```

The registry substitutes the variable's value as written before it parses the file, so the single-quoted scalar takes the token literally, and a variable whose token contains `'` holds that quote doubled. The token is not percent-encoded. A declaration that sets neither key is written as declared, so a credential in the userinfo of `source.git.repo`, which is the form 0.6.0 and earlier read, keeps working, and each start logs a line that names the layer and asks for the credential to move to the two keys. Startup aborts with an error that names the layer and the key when `username` or `token` is set together with userinfo in `source.git.repo`, or on a `source.git.repo` that takes no credential, such as an `ssh://` or scp-like remote. It also aborts when `username` is set without a token, when the token is empty, which is what an unset variable yields, and when either key holds a character the HTTP API refuses in `repo_username` or `repo_token`. The declaration is the place to set, rotate, or remove a declared layer's credential: each start writes the credential the declaration carries, or none where it carries none, so a credential set or removed with `podium layer update` reverts at the next start.

Or register them at runtime:

```bash
# A git-source layer. The registry returns a webhook URL and HMAC secret
# to configure on the source repo. On a multi-tenant registry the webhook
# URL carries the tenant ID. Setting a visibility flag requires the
# tenant admin role.
podium layer register --id org-defaults \
  --repo git@github.com:acme/podium-org-defaults.git --ref main \
  --organization

# A local-source layer, read from a path the registry process can see.
# Registering one requires the tenant admin role.
podium layer register --id team-artifacts \
  --local /var/podium/team-artifacts/ --group acme-engineering

# A personal layer. The registry derives the owner from the authenticated
# caller and gives the layer implicit users:[<owner>] visibility.
podium layer register --id alice-personal \
  --repo git@github.com:alice/podium-personal.git --ref main --user-defined
```

An authenticated caller without the tenant `admin` role registers a user-defined layer whether or not `--user-defined` is passed, where the registration asserts none of `owner`, `public`, `organization`, `groups`, and `users`. The registry resolves the class from the caller's identity. A registration that asserts one of those fields is refused with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, as the section below states.

### Using an Agent Skills repository as a layer

A repository of Agent Skills directories registers as a layer with no `ARTIFACT.md` in any skill. Discovery treats a directory below the layer root as a package when it directly contains `ARTIFACT.md`, and when it directly contains `SKILL.md` and sits inside no other package. A package with `SKILL.md` and no `ARTIFACT.md` is a `type: skill` artifact. A repository whose skills sit under `skills/<name>/` registers as a git layer with `root: skills`, and the canonical IDs are `<name>`:

```yaml
registry:
  layers:
    - id: acme-skills
      source:
        git:
          repo: git@github.com:acme/agent-skills.git
          ref: main
          root: skills
```

Discovery skips every directory below the layer root whose name begins with a dot. A repository whose skills sit under `.claude/skills/` or `.agents/skills/` names the dot directory in `root`. With `root: .claude/skills` the canonical IDs are `<name>`, and with `root: .claude` they are `skills/<name>`.

`root` names a subtree path inside the repository and is written without a trailing slash.

A repository whose root directory is itself a single skill cannot register as a git layer. A manifest at the layer root has no canonical ID, and `root` cannot name a directory above the repository root, so a reingest of such a layer fails with `ingest.lint_failed`. Register it as a `local` layer whose path holds a copy of the skill without its `.git/` directory, for example one filled by `git -C <clone> archive --prefix=<name>/ HEAD | tar -x -C <layer-path>`. Every file below a package's root, including the files under `.git/`, is a bundled resource of that package, so a layer that holds the clone itself ships its Git metadata as resources and changes the content hash with every Git operation.

A `SKILL.md` without frontmatter in a directory outside any package fails discovery for the whole layer. Rename the file or add the Agent Skills frontmatter.

### Private remotes

A `git` layer for a private HTTPS repository takes its credential in flags of its own, apart from `--repo`: `--repo-username` names the username, and `--repo-token-file` names a file that holds the token. `--repo-token-file -` reads the token from standard input. The command removes one trailing line ending from the token, and no flag takes the token on the command line, where the process list and the shell history would record it.

```bash
podium layer register --id team-private \
  --repo https://git.acme.com/acme/private.git --ref main \
  --repo-username x-access-token --repo-token-file ./token
```

The registry joins the username and the token into the URL it clones with, and the remote receives them exactly as given. The [operator guide](operator-guide#rollback-floor-for-the-layer-credential-columns) states how the metadata store holds the credential. `podium layer list`, the register and update responses, the server log, and the `ingest.source_unreachable` message of a failed clone carry no part of it. Each `git` layer those responses report whose remote is an `http` or `https` URL not listed as `[redacted]` carries `repo_credential`, which is `true` while a credential is stored. A layer on which the registry takes no credential, such as one on an `ssh://`, scp-like, or `[redacted]` remote, carries no `repo_credential`. The registry does not try the credential at registration, so a wrong one surfaces as `ingest.source_unreachable` at the next ingest.

The following table lists the username each provider expects.

| Provider and credential | `--repo-username` | Token |
|:--|:--|:--|
| GitHub personal access token | Any non-empty name, such as the account name | The token |
| GitHub App installation token | `x-access-token` | The installation token |
| GitLab personal, project, or group access token | Any non-empty name | The token |
| GitLab deploy token | The deploy token's generated username | The token |
| Bitbucket Cloud API token | The account username, or `x-bitbucket-api-token-auth` | The API token |
| Bitbucket repository, project, or workspace access token | `x-token-auth` | The access token |
| Azure DevOps personal access token | Empty (`--repo-username ''`) or omitted | The token |
| AWS CodeCommit HTTPS Git credentials | The generated Git username | The generated password |

GitHub states a lifetime of one hour for an installation token. The registry does not mint or refresh it, so rotating it is the operator's task, for example on a schedule with `podium layer update --id team-private --repo-token-file -`.

`podium layer update --repo-token-file` replaces the token without the URL, and it keeps the stored username unless the command also passes `--repo-username`. `--clear-repo-credential` removes the stored credential. A `--repo` that carries a credential in its URL, as in `https://user:token@host/x.git`, is refused with `registry.invalid_argument` carrying `details.constraint: "url_credential"`. So is a `--repo` with `@` after its host, or one with a scheme that does not parse as a URL; percent-encode an `@` that belongs to the path. An `ssh://` or scp-like remote takes no credential flag, and its login name, as in `ssh://git@git.acme.com/acme/x.git`, stays in `--repo`.

Re-registering a layer's ID replaces the stored record, and the registry keeps the stored credential when each of the following holds: the stored layer is a `git` layer, which every `--repo` registration also is; the stored remote is an `http` or `https` URL that carries a credential, or is listed as `[redacted]`; `--repo` is the remote exactly as `podium layer list` reports it; the re-registration keeps the layer's class and, on a user-defined layer, its owner; and the command passes neither `--repo-token-file` nor `--clear-repo-credential`. The owner of an admin-defined layer does not enter the comparison, so a change of that owner alone does not prevent the keep. A re-registration that changes the class, changes a user-defined layer's owner, or names a different remote stores only the credential it carries, and the next ingest of a private `http` or `https` remote fails with `ingest.source_unreachable` until the credential is set again with `--repo-username` and `--repo-token-file`, on the re-registration itself or with `podium layer update`. `--clear-repo-credential` stores the listed remote without the credential. A kept re-registration still resets the layer's order, its registration time, and its ingest history, and issues a new webhook secret.

`podium layer list` reports an `ssh://` remote without its login name, and a re-registration with that listed value stores the remote without it. The registry then connects as the operating-system user of the registry process, so a remote that expects the login name `git` fails the next ingest. Re-register an `ssh://` layer with its full `--repo`, login name included. Release 0.6.0 refused a re-registration with the listed value with `details.constraint: "redacted_repo"`. An scp-like remote such as `git@git.acme.com:acme/x.git` is listed as registered, login name included.

A layer whose stored remote carries its credential in the URL, from an earlier release, keeps cloning with it, and `podium layer update --repo-token-file` or `--clear-repo-credential` replaces or removes that credential. A stored username that contains `:`, `/`, `"`, a space, or a control character cannot be kept, so a rotation of such a layer passes `--repo-username` with `--repo-token-file`. A layer stored in the URL-token form `https://<token>@host` holds no username, so a rotation of it passes `--repo-username` where the provider expects one.

A layer whose remote is listed as `[redacted]` reports a failed clone with a fixed message that carries no detail from the remote or the network. Re-register it with a `--repo` that parses as a URL. For an `http` or `https` remote, pass a `--repo` without userinfo together with `--repo-username` and `--repo-token-file`. For an `ssh://` remote, pass the full `ssh://` URL, login name included, with no credential flag. Re-registering it with the listed `[redacted]` value keeps the stored remote on the terms above. The registry reads a `--repo` of `[redacted]` as a filesystem path, so the [local-source rule](#who-may-register-a-local-source-layer) applies to that re-registration and refuses it with `details.constraint: "local_source"` from the owner of a user-defined layer who does not hold the tenant `admin` role. The layer write rule refuses any other caller without that role first, with `auth.forbidden` and no `details.constraint`. A deployment with no identity provider configured, and one in public mode, authenticates no caller and admits every caller on the admin arm, so neither rule refuses that re-registration there.

### Who may set a layer's owner and visibility

`owner`, `public`, `organization`, `groups`, and `users`, which `podium layer register` sets with `--public`, `--organization`, `--group`, and `--user`, and with `--owner` on its `--user-defined` form, are read on a registration by a caller holding the tenant `admin` role alone. Any other caller that asserts one of them is refused with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, and the refusal names the asserted fields. A field is asserted by its value rather than by its presence, so a false `public` or `organization`, an empty `groups` or `users`, an empty `owner`, and an `owner` naming the caller's own verified subject assert nothing. A deployment with no identity provider configured, and one in public mode, authenticates no caller and admits every caller on the admin arm, so the rule refuses nothing there, and `podium layer register --user-defined --owner` is the mechanism that names a layer's owner on such a deployment. This rule is evaluated after the layer write authorization rule and after the local-source rule below, so a registration the layer write rule refuses keeps its bare `auth.forbidden` with no `details.constraint`, a registration also on the local-source arm keeps `details.constraint: "local_source"`, and the `admin_only_fields` refusal is returned only where neither earlier rule refuses.

A user-defined layer's owner and visibility are fixed at the registration that created it. On `podium layer update` against such a layer, the refusal reads the value each flag would store against the value the layer holds. A flag that would change the stored record is refused with `registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`: `--public`, `--organization`, `--group`, a `--user` naming anyone other than the owner, an `--owner` naming another subject, and `--clear-users`, which empties the stored `users: [<owner>]`. The refusal rejects the whole patch, so no other flag the same command carries is applied. A flag whose value restates what the layer holds asserts nothing and is admitted, including `--public=false`, `--organization=false`, `--user <owner>`, `--owner <owner>`, and `--clear-groups` against a layer storing no group. The rule reads the stored layer's class rather than the caller, so it refuses whoever runs the command: the layer's owner, a tenant admin, and every caller on a deployment with no identity provider configured or one in public mode. An administrator who needs the layer visible more widely re-registers its ID with `podium layer register` as an admin-defined layer carrying the visibility flags they want. That re-registration replaces the stored record: the layer's registration time is the time of the re-registration, its order is recomputed at the tail of the layer list, its `last_ingested_ref` and `last_ingested_at` are emptied so the next ingest reads the source afresh, a `git` source is issued a new inbound webhook secret that must be registered at the Git host in place of the old one, and the former owner regains a slot against the per-identity user-defined layer cap. A re-registration that changes a layer's class, or a user-defined layer's owner, does not keep its repository credential, and [Private remotes](#private-remotes) states how to set it again. An operator on a deployment with no identity provider who needs to correct an `--owner` value has the same recourse: re-register the ID with `podium layer register --user-defined --owner <corrected-sub>`, which that deployment admits on the admin arm.

### Who may register a local-source layer

Registering a layer whose source names a filesystem path on the registry host, patching that path, restoring such a layer, and reingesting one are authorized to a caller holding the tenant `admin` role. Any other caller is refused with `auth.forbidden` carrying `details.constraint: "local_source"`. A `--repo` value that resolves to the Git file transport names a host path as well and takes the same arm. A deployment with no identity provider configured, and one in public mode, authenticates no caller, so no caller can hold the `admin` role and each of these operations is admitted there for every caller. The registry process reads the named path with its own rights rather than with the registrant's, which is why the path is admin-only while a Git ref is not.

The rule is evaluated on each of those operations rather than against the stored layer list, so a `local`-source layer a non-admin registered before the rule was in force keeps serving its ingested artifacts and is refused at its next reingest, restore, or path patch. Re-register it under an admin identity, or grant the owner the `admin` role, to bring it back under a caller the rule admits. `podium layer watch` reingests on every tick, so a watch loop over such a layer is refused once per tick for a caller the rule does not admit.

An ingest of a local-source layer reads only within the directory the layer's configured path resolves to. A path that leaves that directory, including one reached through a symbolic link stored inside it, is not read, and a read the ingest requires and cannot satisfy fails that layer's ingest with `ingest.source_unreachable` while the artifacts served before the refusal stay in place. A symbolic link inside the layer whose target is written as an absolute path is refused whatever that target names, including a target inside the same layer, so rewrite such a link with a target relative to its own directory. A layer that relied on a link leaving its root is restructured to hold the content inside the layer directory.

`podium layer list` prints the registered layers the caller can see, and their current state. A caller holding the tenant `admin` role, and every caller on a registry that authenticates none, sees every layer in the tenant. Any other authenticated caller sees the layers that caller's identity admits, including that caller's own user-defined layers. A caller the registry resolves as anonymous sees none, and a caller whose credential fails verification is refused on the terms the [HTTP API reference](../reference/http-api#list-layers) states. Whether presenting no credential is itself a verification failure is the configured identity provider's rule. On a multi-tenant registry, `podium layer list` reads the tenant the caller's organization selects. A caller the registry rejects on its other endpoints with `401 auth.tenant_unknown`, because its verified organization names no provisioned tenant, receives the same rejection from `podium layer list`. Any other caller whose organization selects no tenant sees no layers. The visibility flags are covered in [Access control](access-control), and the built-in source types are covered in [Server-side integrations](integrations#layer-sources).

---

## Changing the order

`podium layer reorder <id> [<id> ...]` re-sequences the named layers. The argument order is precedence, lowest to highest:

```bash
podium layer reorder alice-scratch alice-personal
```

A caller reorders their own user-defined layers without special rights. An argument list that names an admin-defined layer requires the tenant `admin` role, and the registry answers `auth.forbidden` otherwise. A caller whose credential fails verification under the configured identity provider's rule is refused with `auth.token_expired`, `auth.untrusted_token`, or `auth.untrusted_runtime` before either arm is evaluated, so on this operation `auth.forbidden` names a caller the registry verified and did not authorize; the other layer write operations answer such a caller `auth.forbidden` as before. A deployment with no identity provider has no authenticated callers, so the local operator reorders any layer.

A layer declared in the `registry:` `layers:` list is re-seeded from that file in list order at every restart. Change its position in `registry.yaml` to make a new order durable.

---

## Keeping layers current

Each layer refreshes from its source independently.

| Mechanism | When to use it |
|:--|:--|
| Git webhook | A `git`-source layer whose host can reach the registry. The registry ingests on each merge to the tracked ref. Register the webhook URL that `podium layer register` returned. On a multi-tenant registry that URL carries the layer's tenant ID. |
| `podium layer reingest <id>` | A manual or scheduled pull. Covers offline mirrors, internal Git that cannot reach the registry, and any host without a public ingress. |
| `podium layer watch <id>` | A polling loop against the layer's source at an interval set with `--interval` (default 1m). Works for `local` sources and for `git` sources with no webhook. Each tick reingests, so a loop over a source naming a host path is authorized as [the local-source rule](#who-may-register-a-local-source-layer) states. |

`podium layer update --id <id>` patches a registered layer's mutable fields, including the tracked ref, the source path, and the visibility. Only the flags supplied are applied.

An admin-defined layer's visibility narrows over the same command. The update endpoint applies the visibility members the patch carries and keeps the ones it omits, so `--public=false` withdraws the public axis, `--organization=false` withdraws the organization axis, `--group` and `--user` replace their stored list, and `--clear-groups` and `--clear-users` empty it. Narrowing over this path keeps the layer's order, its registration time, its ingest history, and its inbound webhook secret, which unregistering and re-registering the layer all replace. A layer whose stored record carries no visibility axis matches no visibility condition, so on a registry that enforces the visibility evaluator it is visible to no caller the registry resolves to a subject, reaches no composed view, and contributes no artifacts to a sync. A registry started with no identity provider configured, and one started in public mode, authenticates no caller and keeps every layer visible there, so a withdrawal applied on such a registry takes effect once an identity provider is configured. A tenant admin still sees a layer carrying no axis on `podium layer list`, and re-granting an axis restores it. A layer declared in the `registry:` `layers:` list is re-seeded from `registry.yaml` at every start, visibility included, so a withdrawal applied to a declared layer over the API or the CLI reverts at the next start. Change the declaration's `visibility:` block to withdraw a declared layer durably.

`podium layer unregister <id>` removes a layer, and `podium layer restore <id>` recovers one that was unregistered inside the recovery window. `podium layer list --deleted` shows what is still recoverable.

The full flag set for each command is in the [CLI reference](../reference/cli#layer-management).

---

## Reading the composed result

`podium sync --dry-run` prints the artifact set the current identity would materialize, without writing anything. `podium sync --preview` prints the aggregate counts instead.

On a server-backed deployment, `podium admin show-effective [--group <g>]... <user-id>` surfaces the per-layer result for any identity, which answers questions about why a given artifact did or did not appear. See [Access control](access-control#debugging-an-effective-view).

---

## Merge behavior worth knowing

- **Same-ID collisions across registry-side layers are rejected rather than shadowed.** A server rejects the second contribution at ingest with `ingest.collision`. `podium lint` reports the same collision on a local catalog. `podium sync` against a local catalog drops the higher-precedence copy, names it on standard error, materializes the rest, and exits 1, so it materializes the copy a server started on the same directory serves. The same layer list and the same identity always produce the same view.
- **Layer order is declared, never inferred.** There is no fixed `org / team / user` hierarchy. The ordering is whatever the layer list says.
- **Authoring rights are separate.** Whoever can merge to a layer's tracked Git ref publishes there, and whoever can write to a `local`-source layer's path publishes there. Branch protection and required reviewers stay in the Git host. That scope statement is about writing content into a source the registry already reads. Which caller may declare a layer that makes the registry read a given filesystem path is governed by the local-source rule under [Who may register a local-source layer](#who-may-register-a-local-source-layer).
