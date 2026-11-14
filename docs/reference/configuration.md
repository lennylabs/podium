---
title: Configuration
nav_order: 3
description: "The registry.yaml file layout, every server configuration key, and the environment variable that sets each one."
---

# Configuration

This page lists the configuration a registry process reads: the keys of `registry.yaml`, the `PODIUM_*` environment variables, and the `podium serve` flags that set some of them. It applies to `podium serve` and `podium-server` on the single-node and clustered deployment modes. Each table names the page that explains when to use the setting.

Client processes (`podium sync`, `podium-mcp`, and the SDKs) read `sync.yaml` and their own variables, which the [CLI reference](cli#environment-variables) lists. A client never reads `registry.yaml`, and the registry never reads `sync.yaml`.

## File layout

### Where the registry reads the file

The registry reads the file that `PODIUM_CONFIG_FILE` names. When the variable is unset, it reads `~/.podium/registry.yaml` in the home directory of the registry process, and it reads no other path.

- `podium serve --config <path>` sets `PODIUM_CONFIG_FILE` for the process, so the flag overrides a value inherited from the environment. `podium-server` has no `--config` option.
- When `PODIUM_CONFIG_FILE` names a file that does not exist, the registry refuses to start with `config file "<path>" does not exist`.
- When the default file does not exist, the registry starts from the environment variables and the defaults. A registry on the SQLite store then writes `~/.podium/registry.yaml` with its bind address and storage paths, unless `PODIUM_CONFIG_FILE` or `PODIUM_NO_AUTOSTANDALONE` is set. [`podium serve`](cli#podium-serve) describes the standalone fallback.
- When the file cannot be read or does not parse as YAML, the registry logs `warning: ignored registry.yaml:` with the cause and starts without any value from the file.

### The registry: mapping

Every server key nests under the top-level `registry:` mapping:

```yaml
# ~/.podium/registry.yaml, or the file PODIUM_CONFIG_FILE names
registry:
  endpoint: https://podium.acme.com
  bind: 0.0.0.0:8080

  store:
    type: postgres
    dsn: ${PODIUM_POSTGRES_DSN}

  object_store:
    type: s3
    bucket: acme-podium
    region: us-east-1

  identity_provider:
    type: oidc-jwt
    issuer: https://acme.okta.com/oauth2/default
    audience: https://podium.acme.com
```

The registry ignores every top-level key other than `registry:`, and it ignores unknown keys inside the mapping. A document that starts at `endpoint:`, `identity_provider:`, `layers:`, or any other server key therefore parses to an empty configuration. The registry reports no error for it and starts from the environment variables and the defaults.

### Environment interpolation

The registry replaces each `${NAME}` in the file with the value of the environment variable `NAME` before it parses the file. Only the brace form is replaced, so a bare `$` in a password survives. An unset variable becomes the empty string. A required backend value left empty that way refuses startup with an error that names the missing variable. Write secrets as `${NAME}` references rather than as literal values in the file.

### Precedence

A setting resolves in this order, highest first:

1. A `podium serve` flag. Each flag sets its environment variable for the process.
2. The environment variable.
3. The `registry.yaml` key.
4. The default.

A key with no environment variable is set in the file alone, and a variable with no key is set in the environment alone. The tables below mark both cases. `podium config show --server` prints each resolved value with its source and redacts API keys and DSNs. The names it prints are display names, and some differ from the file keys, such as `store.postgres_dsn` for `store.dsn`.

### Process configuration and tenant configuration

`registry.yaml` configures one registry process. Every replica of a clustered deployment reads its own copy, so give every replica the same file.

The layer list in `layers:` is copied into the metadata store at every start, as layer configuration of the default tenant. A layer registered at runtime with `podium layer register` or `POST /v1/layers` is stored in the metadata store under the caller's tenant and never appears in the file. On a multi-tenant registry, every provisioned tenant's layers are registered that way. Tenant settings such as quotas and the scope-preview gate are stored per tenant and changed with `podium admin tenant`, as [Tenants](../deployment/identity/tenants) describes. `podium admin migrate-to-standard` copies each tenant's stored layer configuration into the new store, as [Moving between modes](../deployment/modes/migrating) describes.

## Server

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `endpoint` | `PODIUM_PUBLIC_URL` | string | `http://<bind>` | Public base URL of the registry. The registry builds the URLs it returns from it, such as the object URLs of the filesystem object store. |
| `bind` | `PODIUM_BIND` | string | `127.0.0.1:8080` | Listen address. Flag `--bind`. |
| `public_mode` | `PODIUM_PUBLIC_MODE` | boolean | `false` | Bypass authentication and the visibility model. It cannot be combined with an identity provider. Flag `--public-mode`. [Single node](../deployment/modes/single-node#public-mode) describes it. |
| (none) | `PODIUM_ALLOW_PUBLIC_BIND` | boolean | `false` | Allow a non-loopback bind in public mode or under `trusted-headers`. Flag `--allow-public-bind`. |
| (none) | `PODIUM_STANDALONE` | boolean | `false` | Set by `--standalone`. It counts as explicit configuration, so `--strict` does not refuse the start and no first-run notice is printed. |
| (none) | `PODIUM_NO_AUTOSTANDALONE` | boolean | `false` | Refuse to start when no configuration is found, instead of entering the standalone configuration. Flag `--strict`. |

A boolean environment variable reads `true`, `1`, `yes`, and `on` as true, ignoring case, unless its row says otherwise.

## Metadata store

[Backing services](../deployment/services/#metadata-store) describes the choice between the stores.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `store.type` | `PODIUM_REGISTRY_STORE` | string | `sqlite` | `sqlite` or `postgres`. |
| `store.sqlite_path` | `PODIUM_SQLITE_PATH` | string | `~/.podium/standalone/podium.db` | SQLite database file. |
| `store.dsn` | `PODIUM_POSTGRES_DSN` | string | (none) | Postgres connection string. Required when the store is `postgres`. |

## Object storage

[Backing services](../deployment/services/#object-storage) describes the backends.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `object_store.type` | `PODIUM_OBJECT_STORE` | string | `filesystem` | `filesystem` or `s3`. |
| `object_store.filesystem_root` | `PODIUM_FILESYSTEM_ROOT` | string | `~/.podium/standalone/objects` | Root directory of the filesystem backend. |
| `object_store.bucket` | `PODIUM_S3_BUCKET` | string | (none) | Bucket name. Required for `s3`. |
| `object_store.region` | `PODIUM_S3_REGION` | string | (none) | Bucket region. Required for `s3`. |
| `object_store.endpoint` | `PODIUM_S3_ENDPOINT` | string | AWS S3 | URL of an S3-compatible service such as MinIO, R2, or GCS. An `http` URL turns TLS off, and an `https` URL or a bare host keeps it on. |
| `object_store.force_path_style` | `PODIUM_S3_FORCE_PATH_STYLE` | boolean | `false` | Path-style addressing, for MinIO and similar services. |
| `object_store.access_key_id` | `PODIUM_S3_ACCESS_KEY_ID` | string | (none) | Static access key. With neither static key set, the registry reads the `AWS_*` environment variables, then the AWS credentials file, then the instance's IAM role. |
| `object_store.secret_access_key` | `PODIUM_S3_SECRET_ACCESS_KEY` | string | (none) | Static secret key, set together with the access key. |
| `object_store.presign_ttl_seconds` | `PODIUM_PRESIGN_TTL_SECONDS` | integer | `3600` | Lifetime of an S3 presigned URL, in seconds. Flag `--presign-ttl-seconds`. |
| (none) | `PODIUM_MIGRATION_OBJECT_READ_TIMEOUT` | duration | `30s` | Deadline on each object-storage read made by the first-start rewrite of stored content hashes, by `sign-stored-rows`, and by the registry when it admits a stored row before serving it. An unset, unparsable, or non-positive value takes the default, so no configuration removes the bound. |

## Identity

[Identity providers](../deployment/identity/) describes the providers. [OIDC identity](../deployment/identity/oidc/) covers the `oidc-jwt` settings, and [Trusted headers](../deployment/identity/trusted-headers) covers the gateway settings.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `identity_provider.type` | `PODIUM_IDENTITY_PROVIDER` | string | (none) | `oidc-jwt`, `trusted-headers`, or `injected-session-token`. Unset, every caller is anonymous. `oauth-device-code` is a client value, and the registry refuses it with `config.identity_provider_unverified`. |
| `identity_provider.audience` | `PODIUM_OAUTH_AUDIENCE` | string or list | (none) | Audiences the registry accepts in a token's `aud` claim. Required under `oidc-jwt` and `injected-session-token`. The variable is a comma-separated list. The key takes a list, or a string that names one audience and is never split. The first value is the one the browser sign-in redirect sends. |
| `identity_provider.issuer` | `PODIUM_OAUTH_ISSUER` | string | (none) | OIDC issuer URL under `oidc-jwt`. It must use `https`. |
| `identity_provider.token_header` | `PODIUM_OAUTH_TOKEN_HEADER` | string | `Authorization` | Header that carries the forwarded JWT under `oidc-jwt`. |
| `identity_provider.subject_claim` | `PODIUM_OAUTH_SUBJECT_CLAIM` | string | `sub` | Claim read as the caller's subject under `oidc-jwt`. |
| `identity_provider.groups_claim` | `PODIUM_OAUTH_GROUPS_CLAIM` | string | `groups` | Claim read for group membership under `oidc-jwt`. |
| `identity_provider.jwks_cache_ttl_seconds` | `PODIUM_OAUTH_JWKS_CACHE_TTL_SECONDS` | integer | `300` | Maximum age of the cached issuer JWKS under `oidc-jwt`. |
| `identity_provider.authorization_endpoint` | `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` | string | (none) | Device-authorization endpoint of the device-code flow. The browser flow reads `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` instead. |
| (none) | `PODIUM_TRUSTED_PROXY_SECRET` | string | (none) | Shared secret the gateway sends in `X-Podium-Proxy-Secret` under `trusted-headers`. |
| (none) | `PODIUM_IDP_GROUP_MAPPING` | string | (none) | Comma-separated `<claim-value>=<group-name>` pairs that rewrite group claim values before visibility evaluation. A non-empty value that resolves to no pair refuses startup with `config.invalid_idp_group_mapping`. [Groups](../deployment/identity/groups) describes it. |
| (none) | `PODIUM_RUNTIME_KEYS_PATH` | string | (none) | JSON file of trusted runtime signing keys, written with `podium admin runtime register --keys-file`. See the notes below the table. |
| (none) | `PODIUM_SCIM_TOKENS` | string | (none) | Comma-separated bearer tokens the SCIM 2.0 receiver at `/scim/v2/` accepts. Each entry is trimmed, and blank entries are dropped. With no token listed, the registry mounts no receiver. Whether the pushed directory feeds layer `groups:` filters depends on the identity provider, as [Groups](../deployment/identity/groups) states. |
| (none) | `PODIUM_SCIM_STORE_PATH` | string | (none) | JSON file that persists the SCIM directory across restarts. Unset, the directory is held in memory and lost on restart. See the notes below the table. |

`PODIUM_RUNTIME_KEYS_PATH` notes:

- The registry reads the file before it binds a listener and never writes it. A key added to the file takes effect at the next process start.
- A file the registry cannot read or parse aborts startup with `config.runtime_keys_unavailable` under every identity provider.
- Under `injected-session-token`, an unset path, or a path naming a file that carries no key, aborts startup with the same code.

`PODIUM_SCIM_STORE_PATH` notes:

- The registry creates the parent directory at startup and the file on the first push. A missing or empty file loads as an empty directory.
- These conditions abort startup with `config.scim_store_unavailable`, under every identity provider and whether or not the receiver is mounted: a file the registry cannot read for a reason other than its absence, a non-empty file that does not parse, a parent directory it cannot create, and a parent directory in which it cannot create a file.

## Web UI

These settings are environment variables only. [Identity providers](../deployment/identity/) describes how the web UI resolves its caller, and [config.web_ui_auth_unconfigured requirements](error-codes#configweb_ui_auth_unconfigured-requirements) lists what the browser flow requires.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_WEB_UI` | boolean | `false` | Mount the web UI at `/app/` and redirect `GET /` to it. Flag `--web-ui`. |
| `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` | boolean | `false` | Allow the web UI on a non-loopback bind when an identity provider is configured. Flag `--web-ui-allow-public-bind`. |
| `PODIUM_WEB_UI_AUTH` | boolean | `false` | Sign the browser in through the registry with the OAuth authorization-code flow. Flag `--web-ui-auth`. |
| `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` | duration | `10m` | Sign-in window, which is the `Max-Age` of the pre-authorization cookie. Flag `--web-ui-auth-transaction-ttl`. |
| `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET` | string | (none) | OAuth client identifier and credential the registry presents to the IdP. Required under `PODIUM_WEB_UI_AUTH`. |
| `PODIUM_WEB_UI_REDIRECT_URI` | string | (none) | Callback URL the IdP returns the browser to. It must be an `https` URL or an `http` URL whose host is a loopback address. Required under `PODIUM_WEB_UI_AUTH`. |
| `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` | string | (none) | IdP endpoints the sign-in route redirects to and the callback exchanges the code at. Required under `PODIUM_WEB_UI_AUTH`. |
| `PODIUM_WEB_UI_OAUTH_SCOPES` | string | `openid profile email groups` | Space-delimited scope set the sign-in redirect sends. A token issued without the scope that carries the group claim narrows every group-scoped visibility decision for that caller. |
| `PODIUM_WEB_UI_OAUTH_EXCHANGE_TIMEOUT` | duration | `10s` | Deadline on the callback's token-endpoint request. An unset, unparsable, or non-positive value takes the default, so no configuration removes the bound. |

A configuration that sets `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` and leaves `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` empty aborts startup with `config.web_ui_auth_unconfigured` when the browser flow is on.

## Tenancy and administrators

[Tenants](../deployment/identity/tenants) describes the single-tenant and multi-tenant modes and the operator and tenant admin roles.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| (none) | `PODIUM_MULTI_TENANT` | boolean | `false` | Route each request to the tenant its verified organization names. See the notes below the table. |
| (none) | `PODIUM_OPERATOR_ADMINS` | string | (none) | Comma-separated identities granted the instance-operator role at boot. The operator role authorizes the `podium admin tenant` commands and the `/v1/admin/tenants` endpoints, and it confers no per-tenant `admin` rights. |
| (none) | `PODIUM_BOOTSTRAP_ADMINS` | string | (none) | Identities granted the tenant `admin` role on the default tenant at every start, separated by commas, spaces, or both. A repeated grant is a no-op. |
| `tenant.expose_scope_preview` | `PODIUM_EXPOSE_SCOPE_PREVIEW` | boolean | `true` | Scope-preview gate of the default tenant. The registry writes it when it first creates the default tenant in a store, and a later change has no effect on an existing store. With the gate off, `GET /v1/scope/preview` returns `config.scope_preview_disabled`. |

`PODIUM_MULTI_TENANT` notes:

- When `true` and the registry runs an identity provider it verifies, the registry routes each request to the tenant its organization names. The `podium admin tenant` commands and the `/v1/admin/tenants` endpoints are then available.
- When `true` with no provider, with a provider value the registry does not recognize, or in public mode with no provider, the registry fails to start with `config.multitenant_no_verifier`, unless another startup check refuses the configuration first. The providers the registry verifies are `injected-session-token`, `oidc-jwt`, and `trusted-headers`.
- When unset, every request binds to the single `default` org, and tenant management is rejected.

## Layers

[Connecting layer sources](../deployment/content/layer-sources) describes declaring layers, and [Access control](../deployment/content/access-control) describes visibility.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `layers` | (none) | list | (none) | Admin-defined layers, lowest precedence first. The registry writes each entry into the metadata store at every start and ingests a `local` source at startup. A `git` source is ingested on its first webhook or reingest. |
| `layer_path` | `PODIUM_LAYER_PATH` | string | (none) | Filesystem registry root that the registry ingests at startup. Flag `--layer-path`. [`podium serve`](cli#podium-serve) describes how the path resolves into layers. |
| `default_layer_visibility` | `PODIUM_DEFAULT_LAYER_VISIBILITY` | string | `public` without an identity provider, `private` with one | Visibility of a layer registered or declared without a `visibility:` block: `public`, `organization`, or `private`. |

Each entry of `layers` takes these keys:

| Key | Type | Description |
|:--|:--|:--|
| `id` | string | Layer identifier. Required. |
| `source.git.repo` | string | Repository URL. A `git` entry requires it. |
| `source.git.ref` | string | Tracked branch or tag. |
| `source.git.root` | string | Subdirectory of the repository that holds the layer. |
| `source.git.force_push_policy` | string | `tolerant` (the default) or `strict`. |
| `source.git.git_provider` | string | Provider whose signature scheme verifies the layer's inbound webhooks. Unset resolves to `github`. |
| `source.git.username`, `source.git.token` | string | Repository credential. Write the token as a `${NAME}` reference. [Declaring a credential in registry.yaml](../deployment/content/layer-sources#declaring-a-credential-in-registryyaml) gives the rules. |
| `source.local.path` | string | Directory the registry process reads. A `local` entry requires it. |
| `visibility.public`, `visibility.organization` | boolean | Visibility to everyone, or to every authenticated caller of the tenant. |
| `visibility.groups`, `visibility.users` | list of strings | Visibility to the named groups or users. |

An entry that sets both sources or neither, or that omits a required key, refuses startup.

### Discovery

The `discovery:` block sets the registry-wide defaults for rendering the domain tree. It has no environment variables. [Domains](../authoring/domains#discovery-rendering) describes each setting and the per-domain overrides in `DOMAIN.md`.

| Key | Type | Default |
|:--|:--|:--|
| `discovery.max_depth` | integer | `3` |
| `discovery.notable_count` | integer | `10` |
| `discovery.fold_below_artifacts` | integer | `0` |
| `discovery.fold_passthrough_chains` | boolean | `true` |
| `discovery.target_response_tokens` | integer | `4000` |
| `discovery.allow_per_domain_overrides` | boolean | `true` |

A value of `0` in an integer key leaves the default in force.

## Ingest

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `freeze_windows` | (none) | list | (none) | Windows during which ingest is refused with `ingest.frozen`. See the table below. |
| (none) | `PODIUM_INGEST_OFFLINE` | boolean | `false` | `true` skips the HEAD request that checks each URL in artifact prose at ingest. |
| (none) | `PODIUM_LINT_PER_FILE_SOFT_CAP_BYTES` | integer | `1048576` | Per-file soft cap on a bundled resource. An unset or non-positive value keeps the default. [Bundled resources](../authoring/bundled-resources) describes the caps. |
| (none) | `PODIUM_LINT_PER_PACKAGE_SOFT_CAP_BYTES` | integer | `10485760` | Per-package soft cap on bundled resources. An unset or non-positive value keeps the default. |
| (none) | `PODIUM_ENFORCE_SANDBOX_PROFILE` | boolean | `false` | `true` rejects at ingest an artifact whose `sandbox_profile` the host cannot honor, with `ingest.sandbox_profile_unenforceable`. |
| (none) | `PODIUM_HOST_SANDBOXES` | string | `unrestricted` | Comma-separated sandbox profiles the host can enforce. |

Each entry of `freeze_windows` takes these keys:

| Key | Type | Description |
|:--|:--|:--|
| `name` | string | Window name. |
| `start`, `end` | RFC 3339 timestamp | The window covers `start` up to, and excluding, `end`. |
| `blocks` | list of strings | Operations the window blocks. Unset, it blocks `ingest`. The registry enforces `ingest`, which covers webhook-driven and manual reingests. |
| `break_glass_role` | string | Accepted and not read in this release. |

[Progressive adoption](../deployment/progressive-adoption) describes rolling out freeze windows.

## Vector index and embeddings

[Backing services](../deployment/services/#vector-index) describes the defaults, and [Vector backends](../deployment/services/vector-backends) covers each managed backend and switching between backends.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `vector_backend.type` | `PODIUM_VECTOR_BACKEND` | string | `sqlite-vec` on SQLite, `pgvector` on Postgres | `sqlite-vec`, `pgvector`, `pinecone`, `weaviate-cloud`, `qdrant-cloud`, or `none`. |
| (none) | `PODIUM_PGVECTOR_DSN` | string | `PODIUM_POSTGRES_DSN` | Connection string of `pgvector`. It falls back to the `PODIUM_POSTGRES_DSN` variable and does not read `store.dsn`, so a deployment that sets the DSN in the file alone sets one of the variables as well. |
| `vector_backend.api_key` | `PODIUM_PINECONE_API_KEY`, `PODIUM_WEAVIATE_API_KEY`, `PODIUM_QDRANT_API_KEY` | string | (none) | API key of the selected managed backend. Required. |
| `vector_backend.index` | `PODIUM_PINECONE_INDEX` | string | (none) | Pinecone index. Required unless the host is set. |
| `vector_backend.host` | `PODIUM_PINECONE_HOST` | string | resolved from the index | Pinecone index host. |
| `vector_backend.namespace` | `PODIUM_PINECONE_NAMESPACE` | string | `default` | Pinecone namespace prefix. |
| `vector_backend.url` | `PODIUM_WEAVIATE_URL`, `PODIUM_QDRANT_URL` | string | (none) | Cluster URL of Weaviate Cloud or Qdrant Cloud. Required. |
| `vector_backend.collection` | `PODIUM_WEAVIATE_COLLECTION`, `PODIUM_QDRANT_COLLECTION` | string | (none) | Collection of Weaviate Cloud or Qdrant Cloud. Required. |
| `vector_backend.inference_model` | `PODIUM_PINECONE_INFERENCE_MODEL`, `PODIUM_WEAVIATE_VECTORIZER`, `PODIUM_QDRANT_INFERENCE_MODEL` | string | (none) | Hosted model that makes a managed backend embed the submitted text itself. Unset, the backend stores vectors the embedding provider computes. |
| `embedding_provider.type` | `PODIUM_EMBEDDING_PROVIDER` | string | `ollama` on SQLite, `openai` on Postgres | `openai`, `voyage`, `cohere`, `ollama`, or `none`. The empty string in the variable also disables embeddings. |
| `embedding_provider.model` | `PODIUM_EMBEDDING_MODEL` | string | per provider | Model for any provider. A per-provider variable takes precedence: `PODIUM_OPENAI_MODEL` (default `text-embedding-3-small`), `PODIUM_VOYAGE_MODEL` (`voyage-3`), `PODIUM_COHERE_MODEL` (`embed-v4`), or `PODIUM_OLLAMA_MODEL` (`nomic-embed-text`). |
| `embedding_provider.api_key` | `OPENAI_API_KEY`, `VOYAGE_API_KEY`, `COHERE_API_KEY` | string | (none) | API key of the selected provider. Required for `openai`, `voyage`, and `cohere`. |
| `embedding_provider.base_url` | `PODIUM_OPENAI_BASE_URL` | string | `https://api.openai.com/v1` | OpenAI API base URL, for Azure OpenAI or a proxy. |
| `embedding_provider.org` | `PODIUM_OPENAI_ORG` | string | (none) | OpenAI organization ID. |
| `embedding_provider.url` | `PODIUM_OLLAMA_URL` | string | `http://localhost:11434` | Ollama endpoint. |
| (none) | `PODIUM_NO_EMBEDDINGS` | boolean | `false` | Set the vector backend and the embedding provider to `none`, so search runs BM25 alone. Flag `--no-embeddings`. |

The `vector_backend` keys `api_key`, `url`, and `collection` apply to whichever managed backend `type` selects. A selected backend or provider whose required values are missing refuses startup with `missing required configuration for the selected backend(s)`, naming the variables.

These variables tune the outbox through which a managed backend receives vectors. They are environment variables only.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_VECTOR_OUTBOX_INTERVAL_SECONDS` | integer | `5` | Interval between drain passes. |
| `PODIUM_VECTOR_OUTBOX_BATCH` | integer | `50` | Rows drained per pass. |
| `PODIUM_VECTOR_OUTBOX_LAG_DEPTH` | integer | `100` | Outbox depth at which the registry records `vector.outbox_lagging`. |
| `PODIUM_VECTOR_OUTBOX_LAG_AGE_SECONDS` | integer | `300` | Age of the oldest row at which the registry records `vector.outbox_lagging`. |

## Signing

These settings are environment variables only. [Signing keys](../deployment/operations/signing-keys) covers generating, distributing, and rotating the key.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_SIGN` | string | `registry-key` | Ingest signing mode: `registry-key` or `none`. Any other value refuses startup with `config.invalid_sign_mode`. Flag `--sign`. |
| `PODIUM_SIGN_KEY_PATH` | string | `~/.podium/standalone/registry-signing.key` | Registry signing key file. The registry generates it on first run. A registry on Postgres, or on a SQLite file outside `~/.podium/standalone/`, refuses to start with signing on and this variable unset. Client processes read the same file, as the [CLI reference](cli#signing-and-verification) lists. |

## Audit

These settings are environment variables only. [Audit and observability](../deployment/operations/observability#audit-destinations) covers destination setup, and [Audit events](audit-events) is the format reference. `PODIUM_AUDIT_LOG_PATH` configures the registry stream. `podium-mcp` writes a separate local stream that `PODIUM_AUDIT_SINK` configures, as the [CLI reference](cli#local-audit-stream) describes.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_AUDIT_LOG_PATH` | string | `~/.podium/audit.log` | The registry stream's destination list: a comma-separated list of file paths, `http://` and `https://` URLs, `otlp+http://` and `otlp+https://` URLs, and the token `stdout`. A list names at most one file destination and names no element twice, and a file path that contains a comma cannot be listed. Unset or empty, the registry writes the single file destination `~/.podium/audit.log`. Two conditions refuse startup with `config.audit_sink_unavailable`, whatever the anchoring setting: a list that [Audit events](audit-events#destinations) does not admit, and a file destination the registry cannot open or cannot open for appending, even when the file is the only destination. The [error code catalog](error-codes#configaudit_sink_unavailable-causes) lists the causes. |
| `PODIUM_AUDIT_HEADERS_FILE` | string | (none) | File of request headers that every HTTP and OTLP audit destination of the process sends. Each line is one `Name: value` header, and blank lines and lines that begin with `#` are ignored. HTTP Basic authentication is supplied as an `Authorization: Basic` line. A file that cannot be read, a line without a colon, an empty name, and the names `Host`, `Content-Length`, and `Content-Type` refuse startup with `config.audit_sink_unavailable`. The error names the file and the line number and never a value. |
| `PODIUM_AUDIT_TLS_CA_FILE` | string | system trust store | PEM bundle of CA certificates that replaces the system trust store for the HTTP and OTLP audit destinations. A file that cannot be read or holds no certificate refuses startup with `config.audit_sink_unavailable`. |
| `PODIUM_AUDIT_TLS_CERT_FILE` | string | (none) | PEM client certificate the HTTP and OTLP audit destinations present for mutual TLS. It is set together with `PODIUM_AUDIT_TLS_KEY_FILE`. Only one of the two set, or a pair that cannot be loaded, refuses startup with `config.audit_sink_unavailable`. |
| `PODIUM_AUDIT_TLS_KEY_FILE` | string | (none) | PEM private key of `PODIUM_AUDIT_TLS_CERT_FILE`, under the same rules. |
| `PODIUM_AUDIT_QUEUE_MAX_BYTES` | integer | `67108864` (64 MiB) | Upper bound, in bytes of serialized records, on the queue of each HTTP and OTLP audit destination. A record that does not fit is dropped for that destination with reason `queue_full`, as [Audit events](audit-events#loss-accounting) states. An empty value takes the default. Any other value that is not a positive base-10 integer refuses startup with `config.audit_sink_unavailable`. |
| `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS` | integer | `0` | Interval between local chain-head anchors. `0` disables anchoring, and a negative or non-integer value is treated as `0`. See the notes below the table. |
| `PODIUM_AUDIT_VERIFY_INTERVAL_SECONDS` | integer | `3600` | Interval between verifications of the registry stream's file destination. `0` disables verification. A negative or non-integer value is ignored, so the default applies. Verification runs only when `PODIUM_AUDIT_LOG_PATH` lists a file destination. |
| `PODIUM_AUDIT_SIGNING_KEY_PATH` | string | `~/.podium/standalone/audit.key` | Anchor key file, generated when anchoring runs and the file is absent. Read only when anchoring runs. A file that cannot be read, parsed, or generated aborts startup with `config.audit_anchor_key_unavailable`. While registry signing is on, an anchor key that the `PODIUM_SIGN_KEY_PATH` file also carries as its `public:` key or a `verify:` key aborts startup with `config.audit_anchor_key_shared`. |
| `PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS` | integer | `86400` | Interval between scheduled retention passes over the file destination. `0` disables the scheduled pass. |
| `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS` | integer | `365` | Age beyond which a retention pass drops an event. `0` disables the scheduled pass. |
| `PODIUM_AUDIT_SAMPLE_RATES` | string | (none) | Comma-separated `<event-type>=<rate>` pairs, each rate between `0` and `1`. An entry that does not parse is ignored. |
| `PODIUM_PII_REDACTION` | boolean | `true` | Scrub common PII patterns from query text before it is recorded. Also set by the `pii_redaction.enabled` key. |

`podium-mcp` also reads the transport and queue variables (`PODIUM_AUDIT_HEADERS_FILE`, `PODIUM_AUDIT_TLS_CA_FILE`, `PODIUM_AUDIT_TLS_CERT_FILE`, `PODIUM_AUDIT_TLS_KEY_FILE`, and `PODIUM_AUDIT_QUEUE_MAX_BYTES`) when `PODIUM_AUDIT_SINK` is set. [Retention and erasure](../deployment/operations/observability#retention-and-erasure) and [Redaction and sampling](../deployment/operations/observability#redaction-and-sampling) describe the last group of rows.

Anchoring notes:

- Anchoring runs only when the registry stream named by `PODIUM_AUDIT_LOG_PATH` lists a file destination. A list without one logs that anchoring is disabled, and the registry starts.
- A file destination the registry cannot open, or cannot open for appending, refuses startup with `config.audit_sink_unavailable` whatever the anchoring setting, alone or beside another destination.
- The registry checks the destinations before it reads the anchor key, so a refused start reads and generates no anchor key.
- `PODIUM_AUDIT_SINK` does not affect anchoring.

The `pii_redaction:` block is the one audit setting with file keys:

| Key | Type | Default | Description |
|:--|:--|:--|:--|
| `pii_redaction.enabled` | boolean | `true` | Turn the query-text scrub on or off. `PODIUM_PII_REDACTION` takes precedence. |
| `pii_redaction.patterns` | list | (none) | Extra scrub rules appended to the built-in patterns. Each entry carries `regex` (required), `name` (default `custom`), and `replacement` (default `[redacted]`). A regular expression that does not compile refuses startup. |

## Store retention

These settings are environment variables only.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_STORE_RETENTION_INTERVAL_SECONDS` | integer | `86400` | Interval between store retention sweeps. `0` disables the sweeps. |
| `PODIUM_DEPRECATED_RETENTION_DAYS` | integer | `90` | Days after an artifact version is deprecated before a sweep purges it. |
| `PODIUM_LAYER_RECOVERY_DAYS` | integer | `30` | Days an unregistered layer stays restorable with `podium layer restore` before a sweep purges it. |

## Quotas

These settings are environment variables only. `podium admin tenant` sets the per-tenant values.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_QUOTA_SEARCH_QPS` | integer | `0` | Deployment default for the search QPS limit. It applies to every tenant whose own `search_qps` is zero, and on a multi-tenant registry to requests that resolve to no tenant. A request over the limit is refused with `quota.search_qps_exceeded`. `0` sets no deployment default, and a negative or non-integer value is treated as `0`. On a single-tenant registry it is the search QPS limit. |
| `PODIUM_QUOTA_MATERIALIZE_RATE` | integer | `0` | Deployment default for the materialization rate limit. It applies to every tenant whose own `materialize_rate` is zero, and on a multi-tenant registry to requests that resolve to no tenant. A `load_artifact` request over the limit is refused with `quota.materialize_rate_exceeded`. A bulk-load item over the limit is reported with that code inside the batch's 200 response. `0` sets no deployment default, and a negative or non-integer value is treated as `0`. On a single-tenant registry it is the materialization rate limit. |
| `PODIUM_QUOTA_AUDIT_VOLUME_PER_DAY` | integer | `0` | Deployment default for the daily audit-event cap. It applies to every tenant whose own `audit_volume_per_day` is zero. Once a tenant's count for the current UTC day reaches the limit, new ingest writes to that tenant's layers are refused with `quota.audit_volume_exceeded`, and reads continue to serve. `0` sets no deployment default, and a negative or non-integer value is treated as `0`. On a single-tenant registry it is the audit volume limit. |
| `PODIUM_MAX_USER_LAYERS` | integer | `0` | Deployment default for the per-identity user-defined-layer cap. It applies to every tenant whose own `max_user_layers` is zero. `0` selects 3. A negative or non-integer value is treated as `0`, so the variable cannot disable the cap. A single-tenant registry reads its tenant's stored `max_user_layers`, so a non-zero stored value applies there ahead of this variable. |

## Read-only mode

[Capacity](../deployment/operations/capacity#read-only-mode) describes the fallback to read-only mode when the metadata store primary is unreachable.

| Key | Variable | Type | Default | Description |
|:--|:--|:--|:--|:--|
| `read_only.probe_failures` | `PODIUM_READONLY_PROBE_FAILURES` | integer | `3` | Consecutive failed probes before the registry enters read-only mode. `0` disables the probe. Only the variable can set `0`, because the key ignores a value below `1`. |
| `read_only.probe_interval_seconds` | `PODIUM_READONLY_PROBE_INTERVAL` | integer | `5` | Interval between probes, in seconds. |

## Metrics, tracing, and logs

These settings are environment variables only. [Audit and observability](../deployment/operations/observability) describes the metric set and the spans.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_METRICS` | boolean | `true` | Serve the Prometheus endpoint at `/metrics` and record request metrics. `false` removes both. |
| `PODIUM_TRACING` | string | (none) | `otlp` exports spans over OTLP/HTTP, and `stdout` writes them to standard output. `off`, `none`, and `false` disable export. Any other value refuses startup. Unset, a set `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` turns OTLP export on. The exporter reads the standard `OTEL_EXPORTER_OTLP_*` variables. |
| `PODIUM_ACCESS_LOG` | boolean | `true` | Write one log line per served request with its operation, status, and elapsed time. `false` silences it. |

## Notifications

These settings are environment variables only. [Notifications](../deployment/operations/observability#notifications) describes each provider and its settings.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_NOTIFICATION_PROVIDER` | string | (none) | `noop`, `log`, `webhook`, `email` (or `smtp`), or `multi`. |
| `PODIUM_NOTIFICATION_WEBHOOK_URL`, `PODIUM_NOTIFICATION_WEBHOOK_SECRET` | string | (none) | Receiver URL and HMAC key of the `webhook` provider. |
| `PODIUM_NOTIFICATION_SMTP_HOST`, `PODIUM_NOTIFICATION_SMTP_PORT`, `PODIUM_NOTIFICATION_SMTP_FROM`, `PODIUM_NOTIFICATION_SMTP_TO`, `PODIUM_NOTIFICATION_SMTP_USERNAME`, `PODIUM_NOTIFICATION_SMTP_PASSWORD` | string | (none) | Relay, sender, recipients, and credentials of the `email` provider. |

## Outbound webhooks

These settings are environment variables only. [Outbound webhooks](../deployment/operations/outbound-webhooks) describes the receivers and the URL policy.

| Variable | Type | Default | Description |
|:--|:--|:--|:--|
| `PODIUM_WEBHOOK_ALLOWED_TARGETS` | string | (none) | Comma-separated hosts or CIDRs that the receiver URL policy permits in addition to public addresses. A malformed entry refuses startup. |
| `PODIUM_WEBHOOK_STORE_PATH` | string | (none) | JSON file that persists the registered receivers. Unset, receivers are held in memory and lost on restart. A file the registry cannot load logs a warning, and receivers stay in memory. |
| `PODIUM_WEBHOOK_MAX_FAILURES` | integer | `32` | Consecutive delivery failures after which a receiver is disabled. |
| `PODIUM_WEBHOOK_RETRY_BACKOFF` | string | `1s,2s,4s,8s,16s,30s,60s` | Comma-separated Go durations between delivery attempts. Entries that do not parse are dropped. |

## Settings in the Helm chart

The Helm chart configures the registry through environment variables and mounts no `registry.yaml`. A setting reaches a chart-deployed pod in one of these ways:

- **A chart value.** The chart renders some `config.*` values as `env:` entries, such as `config.bind` as `PODIUM_BIND` and `config.identityProvider.issuer` as `PODIUM_OAUTH_ISSUER`. [How values reach the pod](../deployment/modes/helm-chart#how-values-reach-the-pod) lists them.
- **The Secret.** The pod loads every key of the Secret that `existingSecret` names as an environment variable through `envFrom:`. Put DSNs, API keys, and other secrets there under the variable names on this page.
- **`extraEnv`.** Entries of `extraEnv` are appended to the container's `env:` list.

To set a `registry.yaml` key on the chart, set the variable in the same row of this page. An `env:` entry takes precedence over the same name arriving through `envFrom:`, so a value the chart renders overrides the Secret.

A key that has no variable, such as `layers`, `discovery`, `freeze_windows`, and `pii_redaction.patterns`, takes effect only from a file. The chart offers no value that mounts one, so such a key requires a `registry.yaml` mounted into the pod outside the chart and `PODIUM_CONFIG_FILE` set through `extraEnv`. [Helm chart reference](../deployment/modes/helm-chart) covers the chart values.
