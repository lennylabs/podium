---
title: Connecting layer sources
nav_order: 1
description: Connect Git repositories, private remotes, local paths, and Agent Skills repositories to a registry as layers, and keep each layer current.
---

# Connecting layer sources

A layer is one source of artifacts, such as a Git repository or a directory on the registry host. This page covers connecting sources to a server-backed registry, on a [single node](../modes/single-node) or a [clustered](../modes/clustered) deployment, and keeping each layer current after the connection. [Layered composition](layers) covers how the registry combines layers and which layer takes precedence.

On the [Local](../modes/local) deployment mode there is no server to register a source with. Each subdirectory of the registry path is a layer, as [Local → Directory layout](../modes/local#directory-layout) describes.

---

## Before you start

- A running registry. Every `podium layer` subcommand takes `--registry <url>`, which defaults to `PODIUM_REGISTRY`.
- The tenant `admin` role for an admin-defined layer and for any layer that names a path on the registry host. An authenticated caller without the role registers a user-defined layer for itself. A registry with no identity provider configured, or one in public mode, authenticates no caller and admits every request. [Registration rights](#registration-rights) states the rules.
- For a private repository, a credential the Git host issues. [Private repositories](#private-repositories) lists what each host expects.

---

## Source types

| Source type | What the registry reads | How it stays current |
|:--|:--|:--|
| `git` | A tracked ref of a Git repository, optionally rooted at a subdirectory. | A webhook from the Git host, `podium layer watch`, or `podium layer reingest`. |
| `local` | A filesystem path the registry process can read. | `podium layer reingest` or `podium layer watch`. |
| Custom | A source a `LayerSourceProvider` plugin implements, such as an S3 bucket or an OCI registry. | The trigger model the plugin declares. See [Extending](../services/extending). |

---

## Git repositories

A `git` layer mirrors a tracked ref. Declare it in `registry.yaml` when the deployment owns the layer list, or register it at runtime with `podium layer register`.

### Declare a layer in registry.yaml

Declare layers under `layers:` in `registry.yaml`. Every server-side key nests below the top-level `registry:` mapping. A single layer names its repository and ref:

```yaml
registry:
  layers:
    - id: team-shared
      source:
        git:
          repo: git@github.com:your-org/podium-team-artifacts.git
          ref: main
      visibility:
        organization: true
```

Several layers form an ordered list, lowest precedence first:

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

A document that starts at `layers:` parses to an empty config, and the registry ignores it without reporting an error.

The registry re-seeds every declared layer from `registry.yaml` at each start, in list order. The declaration is authoritative for the fields it carries, so a change made over the CLI or the API to a declared layer reverts at the next start. A declared `git` layer is seeded without an ingest. Run `podium layer reingest <id>` after the first start, or use one of the mechanisms under [Keeping layers current](#keeping-layers-current).

### Register a layer at runtime

`podium layer register` adds a layer to a running registry:

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

The command prints the registry's response. For a `git` layer, the response carries `webhook_url` and `webhook_secret`, which [Webhooks](#webhooks) uses.

An authenticated caller without the tenant `admin` role registers a user-defined layer whether or not it passes `--user-defined`. The registration must assert none of `owner`, `public`, `organization`, `groups`, and `users`. The registry resolves the layer class from the caller's identity. A registration that asserts one of those fields is refused with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, as [Who may set a layer's owner and visibility](#who-may-set-a-layers-owner-and-visibility) states.

### Git source options

The same options are available in a declaration and on the command line, with the exceptions the table notes.

| `registry.yaml` key under `source.git` | `podium layer register` flag | Meaning |
|:--|:--|:--|
| `repo` | `--repo` | The repository URL. An `ssh://` or scp-like remote keeps its login name in the URL. |
| `ref` | `--ref` | The tracked ref. |
| `root` | `--root` | A subdirectory of the repository that holds the layer, written without a trailing slash. |
| `force_push_policy` | `--force-push-policy` | `tolerant` (the default) keeps previously ingested commits and emits `layer.history_rewritten`. `strict` rejects an ingest whose history was rewritten. |
| `git_provider` | None | The Git host whose webhook signature scheme the registry applies. See [Choosing the Git provider](#choosing-the-git-provider). |
| `username`, `token` | `--repo-username`, `--repo-token-file` | The repository credential. See [Private repositories](#private-repositories). |

The full flag set is in the [CLI reference](../../reference/cli#layer-management).

### Choosing the Git provider

The registry verifies a `git` layer's inbound webhook deliveries against one Git provider's signature scheme. The default is `github`. The built-in providers are `github`, `gitlab`, and `bitbucket`, and a registry build can register more through the `GitProvider` SPI.

- A declared layer names the provider in the `source.git.git_provider` key, beside `repo` and `ref`. A value that names no registered provider aborts startup with an error that names the layer. Because the registry re-seeds every declared entry at each start, the declaration restores its value over any other.
- A layer registered at runtime names the provider in the `git_provider` field of the `POST /v1/layers` or `POST|PUT /v1/layers/update` request body, sent with an HTTP client such as `curl`. Neither `podium layer register` nor `podium layer update` carries the field, and no CLI flag sets it. The [HTTP API reference](../../reference/http-api#layer-management) documents the field.

---

## Private repositories

A `git` layer for a private HTTPS repository takes its credential in flags of its own, apart from `--repo`:

- `--repo-username` names the username.
- `--repo-token-file` names a file that holds the token. `--repo-token-file -` reads the token from standard input.

The command removes one trailing line ending from the token. No flag takes the token on the command line, where the process list and the shell history would record it.

```bash
podium layer register --id team-private \
  --repo https://git.acme.com/acme/private.git --ref main \
  --repo-username x-access-token --repo-token-file ./token
```

The registry joins the username and the token into the URL it clones with, and the remote receives them exactly as given. The registry does not try the credential at registration, so a wrong credential surfaces as `ingest.source_unreachable` at the next ingest.

An `ssh://` or scp-like remote takes no credential flag. Its login name, as in `ssh://git@git.acme.com/acme/x.git`, stays in `--repo`, and the registry connects as the operating-system user of the registry process.

### Credentials by Git host

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

### Declaring a credential in registry.yaml

A declared `git` layer names its credential in the `source.git.username` and `source.git.token` keys. Write the token as an environment variable reference:

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

The registry substitutes the variable's value as written before it parses the file. The single-quoted scalar therefore takes the token literally, and a token that contains `'` must hold that quote doubled in the variable. The token is not percent-encoded.

Startup aborts with an error that names the layer and the key in these cases:

- `username` or `token` is set together with userinfo in `source.git.repo`.
- `username` or `token` is set on a `source.git.repo` that takes no credential, such as an `ssh://` or scp-like remote.
- `username` is set without a token.
- The token is empty, which is what an unset variable yields.
- Either key holds a character the HTTP API refuses in `repo_username` or `repo_token`.

The declaration is the place to set, rotate, or remove a declared layer's credential. Each start writes the credential the declaration carries, or none where it carries none, so a credential set or removed with `podium layer update` reverts at the next start.

### Rotating and removing a credential

- `podium layer update --repo-token-file` replaces the token without the URL. It keeps the stored username unless the command also passes `--repo-username`.
- `--clear-repo-credential` removes the stored credential.
- GitHub states a lifetime of one hour for an installation token. The registry does not mint or refresh it, so rotating it is the operator's task, for example on a schedule with `podium layer update --id team-private --repo-token-file -`.

### What the registry reports

The [operator guide](../operations/upgrade-notes#rollback-floor-for-the-layer-credential-columns) states how the metadata store holds the credential. `podium layer list`, the register and update responses, the server log, and the `ingest.source_unreachable` message of a failed clone carry no part of it.

A `git` layer whose remote is an `http` or `https` URL, and is not listed as `[redacted]`, carries `repo_credential` in those responses. The field is `true` while a credential is stored. A layer on which the registry takes no credential, such as one on an `ssh://`, scp-like, or `[redacted]` remote, carries no `repo_credential`.

### URLs the registry refuses

A `--repo` that carries a credential in its URL, as in `https://user:token@host/x.git`, is refused with `registry.invalid_argument` carrying `details.constraint: "url_credential"`. The same refusal applies to a `--repo` with `@` after its host and to one with a scheme that does not parse as a URL. Percent-encode an `@` that belongs to the path.

[Edge cases](#edge-cases) covers re-registering a private layer and credentials stored by earlier releases.

---

## Keeping layers current

Each layer refreshes from its source independently. A webhook needs a Git host that can reach the registry. Polling and manual reingest need no inbound connection, and a manual reingest works for every source type.

| Mechanism | When to use it |
|:--|:--|
| Git webhook | A `git` layer whose host can reach the registry. The registry ingests on each push to the tracked ref. |
| `podium layer watch <id>` | A polling loop at an interval set with `--interval` (default 1m). Use it for `local` sources and for `git` sources with no webhook, such as on a developer machine without a public ingress. |
| `podium layer reingest <id>` | A manual or scheduled pull. Use it for offline mirrors, internal Git that cannot reach the registry, and any host without a public ingress. |

Each registered layer reports `last_ingested_at` in `podium layer list` once it has completed an ingest, so a monitor can detect a stale layer.

### Webhooks

Configure the webhook on the source repository with the URL and secret that `podium layer register` returned:

1. Register the layer and copy `webhook_url` and `webhook_secret` from the response.
2. When the host is not GitHub, set the layer's `git_provider`, as [Choosing the Git provider](#choosing-the-git-provider) describes.
3. In the repository's webhook settings at the Git host, set the payload URL to `webhook_url` and the secret to `webhook_secret`.
4. Push a commit to the tracked ref. The registry verifies the delivery, fetches the new commit, and ingests it.

The webhook URL has one of these forms:

| Registry | Webhook URL path |
|:--|:--|
| Single-tenant | `/v1/ingest/webhook/<layer-id>` |
| Multi-tenant | `/v1/ingest/webhook/<tenant-id>/<layer-id>` |

On a multi-tenant registry, the URL carries the tenant ID because a delivery carries no caller organization from which the registry could select the tenant. A delivery that names a tenant that is not provisioned and active is answered `404 registry.not_found`, as is a delivery that names no layer.

Each Git host sends the secret in its own way. The layer's `git_provider` selects which header the registry reads:

| Git host | `git_provider` | Secret field at the host | What the registry checks |
|:--|:--|:--|:--|
| GitHub | `github` (default) | Secret | The HMAC signature in `X-Hub-Signature-256` (`sha256=`), or in `X-Hub-Signature` (`sha1=`) |
| GitLab | `gitlab` | Secret token | That `X-Gitlab-Token` equals the secret |
| Bitbucket | `bitbucket` | Secret | The HMAC-SHA256 hex digest in `X-Hub-Signature` |

A delivery whose signature does not verify is answered `401 ingest.webhook_invalid` and never reaches the content store. The registry does not read the event type or the payload. Each verified delivery runs one ingest of the tracked ref, so a webhook limited to push events avoids ingests that find no change.

`podium layer update --id <id> --rotate-webhook-secret` issues a new secret and prints it. Register the new value at the Git host in place of the old one. Re-registering a layer's ID also issues a new secret.

> [!NOTE]
> A layer declared in `registry.yaml` is re-seeded at every start without a webhook secret, and a delivery to it fails verification. A secret issued with `--rotate-webhook-secret` lasts until the next start. Keep a declared `git` layer current with `podium layer watch` or a scheduled `podium layer reingest`, or register the layer at runtime when it needs a webhook.

Registering a `git` source without configuring the webhook leaves the layer at its initial commit until the first manual reingest.

### Polling with podium layer watch

`podium layer watch <id>` requests a reingest of the layer at a fixed interval and runs until it is interrupted:

- `--interval` takes a Go duration string such as `30s` or `1h`, and defaults to `1m`. A non-positive value is rejected.
- The command works for `local` sources and for `git` sources that have no webhook.
- Each tick is a reingest, so a loop over a source that names a host path is authorized as [Who may register a local-source layer](#who-may-register-a-local-source-layer) states. A caller the rule does not admit is refused once per tick.
- The command carries no exit status of its own.

### Manual reingest

`podium layer reingest <id>` forces a fresh snapshot of the layer's source, whatever its trigger model. A tenant admin may reingest any layer, and the owner of a user-defined layer may reingest that layer. Run it by hand after editing a `local` source, or on a schedule for a `git` host that cannot reach the registry.

During a freeze window, ingest is blocked unless the command passes `--break-glass` with `--justification` and two `--approver` identities. The command exits 1 when the cycle dropped at least one artifact. The [CLI reference](../../reference/cli#podium-layer-reingest) describes its output and exit status.

---

## Local paths

A `local` layer reads a directory on the registry host. It suits a single-node registry that runs next to its authors. The registry process reads the path with its own rights, so only a tenant admin may name one, as [Who may register a local-source layer](#who-may-register-a-local-source-layer) states.

A registry takes local layers in three ways:

- **At startup**, from `--layer-path`, `PODIUM_LAYER_PATH`, or the `registry.layer_path` key, as [Single node → Server side](../modes/single-node#server-side) describes. A directory whose `.registry-config` sets `multi_layer: true` yields one layer per subdirectory. Any other directory is one layer whose ID is the directory name. The layers from the path follow the declared layers in the layer list, so they take higher precedence.
- **In `registry.yaml`**, as a declared layer whose source is `source.local.path`. The registry ingests it at each start.
- **At runtime**, with `podium layer register --local <path>`, as the example under [Register a layer at runtime](#register-a-layer-at-runtime) shows.

The registry re-reads a local layer on demand. Start the registry on the path, and reingest the layer after editing its files:

```bash
podium serve --standalone --layer-path /var/podium/team-artifacts/
podium layer reingest team-artifacts
```

For continuous updates, run `podium layer watch team-artifacts`.

### Paths an ingest reads

An ingest of a local layer reads only within the directory the layer's configured path resolves to:

- A path that leaves that directory is not read, including one reached through a symbolic link stored inside it.
- A symbolic link inside the layer whose target is an absolute path is refused, whatever that target names, including a target inside the same layer. Rewrite such a link with a target relative to its own directory.
- A read the ingest requires and cannot satisfy fails that layer's ingest with `ingest.source_unreachable`. The artifacts served before the refusal stay in place.

A layer that relied on a link leaving its root must be restructured to hold the content inside the layer directory.

---

## Agent Skills repositories

A repository of Agent Skills directories registers as a layer without an `ARTIFACT.md` in any skill. A package with `SKILL.md` and no `ARTIFACT.md` is a `type: skill` artifact.

Discovery treats a directory below the layer root as a package in two cases:

- The directory directly contains `ARTIFACT.md`.
- The directory directly contains `SKILL.md` and sits inside no other package.

A repository whose skills sit under `skills/<name>/` registers as a `git` layer with `root: skills`, and the canonical IDs are `<name>`:

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

`root` names a subtree path inside the repository and is written without a trailing slash.

Discovery skips every directory below the layer root whose name begins with a dot. A repository whose skills sit under `.claude/skills/` or `.agents/skills/` names the dot directory in `root`. With `root: .claude/skills` the canonical IDs are `<name>`, and with `root: .claude` they are `skills/<name>`.

A `SKILL.md` without frontmatter in a directory outside any package fails discovery for the whole layer. Rename the file or add the Agent Skills frontmatter.

### A repository that is a single skill

A repository whose root directory is itself a single skill cannot register as a `git` layer. A manifest at the layer root has no canonical ID, and `root` cannot name a directory above the repository root, so a reingest of such a layer fails with `ingest.lint_failed`.

Register it as a `local` layer whose path holds a copy of the skill without its `.git/` directory. For example, fill the layer path with `git -C <clone> archive --prefix=<name>/ HEAD | tar -x -C <layer-path>`. Every file below a package's root, including the files under `.git/`, is a bundled resource of that package. A layer that holds the clone itself therefore ships its Git metadata as resources and changes the content hash with every Git operation.

---

## Managing registered layers

| Command | Effect |
|:--|:--|
| `podium layer list` | Prints the registered layers the caller can see, and their current state. `--deleted` lists the layers still inside the recovery window. |
| `podium layer update --id <id>` | Patches a layer's mutable fields, including the tracked ref, the source path, and the visibility. Only the flags supplied are applied. |
| `podium layer unregister <id>` | Removes a layer. |
| `podium layer restore <id>` | Recovers a layer unregistered inside the recovery window. |
| `podium layer reorder <id> [<id> ...]` | Changes precedence. See [Changing the order](layers#changing-the-order). |

The full flag set for each command is in the [CLI reference](../../reference/cli#layer-management).

### Which layers podium layer list shows

- A caller holding the tenant `admin` role sees every layer in the tenant, as does every caller on a registry that authenticates none.
- Any other authenticated caller sees the layers its identity admits, including its own user-defined layers.
- A caller the registry resolves as anonymous sees none.
- A caller whose credential fails verification is refused on the terms the [HTTP API reference](../../reference/http-api#list-layers) states. Whether presenting no credential is itself a verification failure is the configured identity provider's rule.

On a multi-tenant registry, `podium layer list` reads the tenant the caller's organization selects. A caller the registry rejects on its other endpoints with `401 auth.tenant_unknown`, because its verified organization names no provisioned tenant, receives the same rejection from `podium layer list`. Any other caller whose organization selects no tenant sees no layers.

### Narrowing an admin-defined layer's visibility

`podium layer update` narrows an admin-defined layer's visibility as well as widening it. The update endpoint applies the visibility members the patch carries and keeps the ones it omits:

- `--public=false` withdraws the public axis, and `--organization=false` withdraws the organization axis.
- `--group` and `--user` replace their stored list.
- `--clear-groups` and `--clear-users` empty their list.

Narrowing this way keeps the layer's order, its registration time, its ingest history, and its inbound webhook secret. Unregistering and re-registering the layer replaces all of them.

A layer whose stored record carries no visibility axis matches no visibility condition. On a registry that enforces the visibility evaluator, it is visible to no caller the registry resolves to a subject, reaches no composed view, and contributes no artifacts to a sync. A tenant admin still sees such a layer on `podium layer list`, and re-granting an axis restores it.

A registry started with no identity provider configured, and one started in public mode, authenticates no caller and keeps every layer visible. A withdrawal applied on such a registry takes effect once an identity provider is configured.

A declared layer is re-seeded from `registry.yaml` at every start, visibility included, so a withdrawal applied to it over the API or the CLI reverts at the next start. Change the declaration's `visibility:` block to withdraw a declared layer durably. [Access control](access-control) covers the visibility fields.

---

## Registration rights

[Access control → Who can change a layer](access-control#who-can-change-a-layer) states the layer write authorization rule. Two further rules apply when a layer is registered.

### Who may set a layer's owner and visibility

The registry reads `owner`, `public`, `organization`, `groups`, and `users` on a registration only from a caller holding the tenant `admin` role. `podium layer register` sets them with `--public`, `--organization`, `--group`, and `--user`, and with `--owner` on its `--user-defined` form.

- Any other caller that asserts one of these fields is refused with `auth.forbidden` carrying `details.constraint: "admin_only_fields"`, and the refusal names the asserted fields.
- A field is asserted by its value rather than by its presence. A false `public` or `organization`, an empty `groups` or `users`, an empty `owner`, and an `owner` naming the caller's own verified subject assert nothing.
- A deployment with no identity provider configured, and one in public mode, admits every caller on the admin arm, so the rule refuses nothing there. On such a deployment, `podium layer register --user-defined --owner` names a layer's owner.
- The registry evaluates this rule after the layer write authorization rule and after the local-source rule. A registration the layer write rule refuses keeps its bare `auth.forbidden` with no `details.constraint`. A registration also on the local-source arm keeps `details.constraint: "local_source"`. The `admin_only_fields` refusal is returned only where neither earlier rule refuses.

A user-defined layer's owner and visibility are fixed at the registration that created it. On `podium layer update` against such a layer, the registry compares the value each flag would store with the value the layer holds:

- A flag that would change the stored record is refused with `registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`. These flags are `--public`, `--organization`, `--group`, a `--user` naming anyone other than the owner, an `--owner` naming another subject, and `--clear-users`, which empties the stored `users: [<owner>]`.
- The refusal rejects the whole patch, so no other flag in the same command is applied.
- A flag whose value restates what the layer holds asserts nothing and is admitted. Examples are `--public=false`, `--organization=false`, `--user <owner>`, `--owner <owner>`, and `--clear-groups` against a layer storing no group.
- The rule reads the stored layer's class rather than the caller. It refuses the layer's owner, a tenant admin, and every caller on a deployment with no identity provider configured or one in public mode.

An administrator who needs the layer visible more widely re-registers its ID with `podium layer register` as an admin-defined layer carrying the wanted visibility flags. That re-registration replaces the stored record:

- The layer's registration time becomes the time of the re-registration.
- Its order is recomputed at the tail of the layer list.
- Its `last_ingested_ref` and `last_ingested_at` are emptied, so the next ingest reads the source afresh.
- A `git` source is issued a new inbound webhook secret, which must be registered at the Git host in place of the old one.
- The former owner regains a slot against the per-identity user-defined layer cap.
- The repository credential is not kept, because the class changes. [Private repositories](#private-repositories) states how to set it again.

An operator on a deployment with no identity provider who needs to correct an `--owner` value has the same recourse. Re-register the ID with `podium layer register --user-defined --owner <corrected-sub>`, which that deployment admits on the admin arm.

### Who may register a local-source layer

Registering a layer whose source names a filesystem path on the registry host, patching that path, restoring such a layer, and reingesting one are authorized to a caller holding the tenant `admin` role. Any other caller is refused with `auth.forbidden` carrying `details.constraint: "local_source"`.

- A `--repo` value that resolves to the Git file transport names a host path as well and takes the same arm.
- A deployment with no identity provider configured, and one in public mode, authenticates no caller. No caller can hold the `admin` role there, and each of these operations is admitted for every caller.
- The registry process reads the named path with its own rights rather than with the registrant's. That is why the path is admin-only while a Git ref is not.

The rule is evaluated on each operation rather than against the stored layer list. A `local` layer a non-admin registered before the rule was in force keeps serving its ingested artifacts, and it is refused at its next reingest, restore, or path patch. Re-register it under an admin identity, or grant the owner the `admin` role, to bring it back under a caller the rule admits.

Authoring rights are separate from registration rights. Whoever can merge to a layer's tracked Git ref publishes there, and whoever can write to a `local` layer's path publishes there. Branch protection and required reviewers stay in the Git host.

---

## Edge cases

### Re-registering a private layer

Re-registering a layer's ID replaces the stored record. The registry keeps the stored credential when all of the following hold:

- The stored layer is a `git` layer, which every `--repo` registration also is.
- The stored remote is an `http` or `https` URL that carries a credential, or is listed as `[redacted]`.
- `--repo` is the remote exactly as `podium layer list` reports it.
- The re-registration keeps the layer's class and, on a user-defined layer, its owner.
- The command passes neither `--repo-token-file` nor `--clear-repo-credential`.

The owner of an admin-defined layer does not enter the comparison, so a change of that owner alone does not prevent the keep.

A re-registration that changes the class, changes a user-defined layer's owner, or names a different remote stores only the credential it carries. The next ingest of a private `http` or `https` remote then fails with `ingest.source_unreachable` until the credential is set again with `--repo-username` and `--repo-token-file`, on the re-registration itself or with `podium layer update`. `--clear-repo-credential` stores the listed remote without the credential. A kept re-registration still resets the layer's order, its registration time, and its ingest history, and issues a new webhook secret.

### ssh:// remotes and the listed value

`podium layer list` reports an `ssh://` remote without its login name. A re-registration with that listed value stores the remote without the login name, and the registry then connects as the operating-system user of the registry process. A remote that expects the login name `git` then fails the next ingest. Re-register an `ssh://` layer with its full `--repo`, login name included.

Release 0.6.0 refused a re-registration with the listed value with `details.constraint: "redacted_repo"`. An scp-like remote such as `git@git.acme.com:acme/x.git` is listed as registered, login name included.

### Layers listed as [redacted]

A layer whose remote is listed as `[redacted]` reports a failed clone with a fixed message that carries no detail from the remote or the network. Re-register it with a `--repo` that parses as a URL:

- For an `http` or `https` remote, pass a `--repo` without userinfo together with `--repo-username` and `--repo-token-file`.
- For an `ssh://` remote, pass the full `ssh://` URL, login name included, with no credential flag.

Re-registering it with the listed `[redacted]` value keeps the stored remote on the terms above. The registry reads a `--repo` of `[redacted]` as a filesystem path, so [the local-source rule](#who-may-register-a-local-source-layer) applies to that re-registration. It refuses the owner of a user-defined layer who does not hold the tenant `admin` role with `details.constraint: "local_source"`. The layer write rule refuses any other caller without that role first, with `auth.forbidden` and no `details.constraint`. A deployment with no identity provider configured, and one in public mode, admits every caller on the admin arm, so neither rule refuses that re-registration there.

---

## Related pages

- [Layered composition](layers) covers precedence, the workspace overlay, and reading the composed result.
- [Access control](access-control) covers the visibility fields and layer write authorization.
- [Clustered](../modes/clustered) covers the per-tenant layer list.
- [HTTP API reference](../../reference/http-api#layer-management) documents the layer endpoints.
