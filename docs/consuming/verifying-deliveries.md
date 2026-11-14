---
title: Verifying deliveries
nav_order: 6
description: "The checks the registry and each consumer run on a delivered artifact, the signature settings and key set behind them, and how podium-mcp refuses a stale latest version."
---

# Verifying deliveries

This page describes the checks an artifact passes between the registry's store
and the consumer, and the settings that control them. Most of the page covers the
Podium MCP server, `podium-mcp`, and a section covers server-source
`podium sync`. It is for developers and platform engineers who configure a
consumer. [Configure your harness](configure-your-harness#set-up-the-mcp-server)
covers the rest of the MCP server setup.

Generating, distributing, backing up, and rotating the registry's signing key are
operator tasks that [Signing keys](../deployment/operations/signing-keys) covers.

---

## Checks before the registry serves

Before the registry serves an artifact's manifest, `SKILL.md`, or bundled
resources, it admits the stored row the response is built from:

1. It recomputes the artifact's content hash from the stored manifest,
   `SKILL.md`, and bundled-resource bytes, including bodies held in object
   storage, and checks each resource's stored hash and size. A row that fails
   is refused with `materialize.content_hash_mismatch`.
2. On a registry with signing on, it verifies the row's stored signature under
   its own verification key set. A row with no signature is refused with
   `materialize.signature_missing`, and a signature that does not verify with
   `materialize.signature_invalid`.

A refused `load_artifact` returns HTTP 500 with the code, and a refused batch
item carries the code in its error envelope. The refusal is not retryable.
[Stored-row admission refusals](../reference/error-codes#stored-row-admission-refusals)
lists the codes.

The registry then builds the delivery record, computes `delivery_hash` over it,
and, with signing on, signs that hash per response with its registry-managed key
as `delivery_signature`. The consumer checks that record.

---

## Checks on each load

For each artifact the registry serves, `podium-mcp` runs the following checks in
order. A failed check stops the load before anything is cached, written to disk,
or returned to the agent.

1. **Delivery hash.** The MCP server recomputes `delivery_hash` from the bytes it
   received and compares it with the value the registry served. A response with
   no `delivery_hash`, or with a value that differs, fails with
   `materialize.content_hash_mismatch`. Before that comparison, each resource
   body and manifest document fetched from a URL is checked against the content
   hash its link carries, and a body that does not match fails the load with the
   same code. This check runs under every signature policy and for every
   artifact type.
2. **Signature.** The MCP server applies its
   [signature policy](#signature-policy) to `delivery_signature`. Under
   `always`, a response with no signature fails with
   `materialize.signature_missing`, and a signature that does not verify under
   the [key set](#key-set-resolution) fails with `materialize.signature_invalid`.
3. **Stale `latest`.** For a `latest` load the registry answered, the MCP server
   refuses an older version than one it already accepted, with
   `materialize.stale_resolution`. [Stale `latest` protection](#stale-latest-protection)
   describes the check.
4. **Runtime and sandbox gates.** The MCP server checks the artifact's
   `runtime_requirements` and `sandbox_profile` against what the host
   advertises. [Handling artifact responses](handling-artifact-responses)
   covers both fields.

A record served from the MCP server's cache runs the delivery-hash and
signature checks again, as
[Cached records after a key rotation](#cached-records-after-a-key-rotation)
describes. A load served from the
[workspace overlay](../deployment/content/layers#workspace-local-overlay) comes
from the local filesystem and carries no delivery record, so it skips the
delivery-hash, signature, and stale `latest` checks.

[Verifying a response](../reference/http-api#verifying-a-response) states the
procedure every consumer follows, and
[Replay detection](../reference/http-api#replay-detection) describes what the
signature does not cover.

### Checks in `podium sync` and the SDKs

Against a server registry, `podium sync` runs the delivery-hash and signature
checks on every record it loads, including the check of each fetched manifest
document and resource body. It resolves the policy from
`PODIUM_VERIFY_SIGNATURES`, then `defaults.verify_signatures`, then `always`,
and the key set from `PODIUM_SIGNATURE_VERIFY_KEY`, then the key file at
`PODIUM_SIGN_KEY_PATH`. It verifies with the registry-managed verifier and does
not read `PODIUM_SIGNATURE_PROVIDER`.

- Under `always` with no usable key, the sync refuses with
  `config.signature_provider_unavailable`, sends no request, and exits with
  status 2.
- A record that fails a check fails its target with the check's code, and the
  sync writes no file and no lock for that target.
- A sync against a filesystem registry runs no delivery check and needs no key
  material.

[Delivery verification](../reference/cli#delivery-verification) in the CLI
reference gives the exit status for each run mode and the routes that resolve
the key set. The language SDKs run the same checks and resolve their policy as
[Verify deliveries](custom-via-sdk#verify-deliveries) describes. Neither keeps
the records behind stale `latest` protection, which only the MCP server runs.

---

## Signature verification

### Signature policy

`PODIUM_VERIFY_SIGNATURES` takes `always` or `never`. When the variable is
unset, the MCP server reads `defaults.verify_signatures` from the merged
`sync.yaml` files, and when that is unset too, the policy is `always`. Any other
value refuses the start.

- **`always`.** Every artifact the MCP server loads must carry a signature that
  verifies under the key set.
- **`never`.** The MCP server checks no signature, and the delivery-hash check
  still runs. This policy fits only a registry that runs with `PODIUM_SIGN=none`
  and therefore signs nothing.

When `defaults.verify_signatures: never` in a `sync.yaml` file turns
verification off, the MCP server writes a `WARN:` line to standard error that
names the file.

### Signature settings

The following variables are read from the environment only.

- **`PODIUM_SIGNATURE_PROVIDER`.** The verifier: `registry-managed` (default) or
  `noop`. Under `always`, `noop` refuses the start with
  `config.signature_provider_unavailable`, because it verifies nothing. Any
  other value, including `sigstore-keyless`, refuses the start with
  `config.invalid` under either policy. The registry signs every delivery
  record with its registry-managed key, and `sigstore-keyless` applies to
  `podium sign` and `podium verify` only.
- **`PODIUM_SIGNATURE_VERIFY_KEY`.** The verification key set: one base64
  Ed25519 public key, or a comma-separated list of them. A delivery signature
  that verifies under any key of the set is accepted. Set to the empty string,
  the variable counts as unset. A non-empty value is authoritative, and the key
  file is then not read. Under `always`, if any entry is empty, is not valid
  base64, or does not decode to a 32-byte Ed25519 public key, `podium-mcp`
  refuses to start with `config.signature_provider_unavailable` and names the
  variable.
- **`PODIUM_SIGN_KEY_PATH`.** The registry key file, by default
  `~/.podium/standalone/registry-signing.key`. When
  `PODIUM_SIGNATURE_VERIFY_KEY` is unset, the `public:` line and every `verify:`
  line of this file supply the verification key set. The MCP server reads the
  file and never writes it.

### Key set resolution

Under `always`, the MCP server resolves its verification key set once, at start.
It refuses to start with `config.signature_provider_unavailable` when it
resolves no usable key. A change to the key set therefore takes effect when the
harness restarts `podium-mcp`.

| Consumer | Variables to set |
|:--|:--|
| A consumer of a standard deployment | `PODIUM_SIGNATURE_PROVIDER` and `PODIUM_SIGNATURE_VERIFY_KEY`, as each recipe in the [harness reference](configure-your-harness#harness-reference) shows. |
| A standalone consumer running under the same user account as its registry | Neither. The set resolves from the registry's own key file. |
| A standalone consumer whose registry runs with a non-default `PODIUM_SIGN_KEY_PATH` | `PODIUM_SIGN_KEY_PATH`, set to the same path. |

[Distribute the verification key set](../deployment/operations/signing-keys#distribute-the-verification-key-set)
shows where the operator obtains the public key.

During a registry key rotation, add the new key to `PODIUM_SIGNATURE_VERIFY_KEY`
as a comma-separated list, and restart the MCP server, before the registry signs
under the new key.
[Rotating the signing key](../deployment/operations/signing-keys#rotating-the-signing-key)
gives the full sequence.

### Cached records after a key rotation

The MCP server delivers a record from its cache only after the record passes the
same signature check as a live response. A cached delivery signature fails that
check when a key the consumer no longer trusts made it. One example is a record
cached before a rotation whose retired key has since left the set.

| `PODIUM_CACHE_MODE` | Outcome for a cached record that fails the check |
|:--|:--|
| `always-revalidate` (default) | Cache miss. The MCP server refetches the artifact, verifies the response, and replaces the cached record. When the registry is unreachable, the load returns `network.registry_unreachable`. |
| `offline-first` | Cache miss, handled as above. When the registry is unreachable, the load returns the offline status with no artifact. |
| `offline-only` | Refused with `materialize.signature_invalid`. |

The MCP server never delivers the failing record. Before a retired key leaves the
set, an `offline-only` consumer either keeps the retired key in its set, or
clears its cache directory and refills it in `always-revalidate` or
`offline-first`.

---

## Stale `latest` protection

The MCP server refuses a `latest` load that resolves to an older version than one
it already accepted. The check stops a proxy, a cache, or another party between
the MCP server and the registry from serving an older version that still carries
a valid signature.

### How the check works

- Each response carries `artifact_revision`, the time the registry stored the
  served version.
- The MCP server keeps a mark per registry and artifact ID: the highest
  `artifact_revision` of a `latest` load it accepted. The marks live in the index
  database under `PODIUM_CACHE_DIR`. The registry is identified by its
  `PODIUM_REGISTRY` URL, with the scheme and host lowercased and a default port
  and a trailing slash removed.
- A later `latest` load whose `artifact_revision` is lower than the reference
  fails with `materialize.stale_resolution`. An equal value is accepted.
- Within one session, the reference is the first `latest` load of the artifact
  that the MCP server accepted in that session, including one served from the
  cache after the registry confirmed it. When the session has no reference for
  the artifact yet, the reference is the mark. A session pinned to an older
  version therefore still loads it.
- An answer read through `resources/read` records the reference for the MCP
  server's own session only, and it never advances the mark.
- Only `latest` loads the registry answered with a full response are compared.
  A load that names a version, a range, or a content hash is never compared, and
  neither is a load served from the cache.
- A backport ingested after a newer version, and a new deprecated version, leave
  every honest `latest` load at or above the mark.
- The first `latest` load under a cache directory with no mark is accepted. A
  load that a later gate refuses leaves the mark unchanged.

### Limits

- The marks are not kept per organization. Identities in different
  organizations that use one registry URL should use separate cache
  directories.
- When the MCP server cannot open the index database, for example because
  another MCP server holds it, it keeps its marks in memory for the life of the
  process and logs a warning.
- A stored mark that cannot be read is deleted with a warning.
- The marks give no protection against a party that can write
  `PODIUM_CACHE_DIR`, because such a party can also roll back the cache or
  delete a mark.
- Under `PODIUM_VERIFY_SIGNATURES=never`, a party between the MCP server and
  the registry can rewrite `artifact_revision` together with the delivery hash,
  so the check refuses only an unmodified replay.

### Resetting the marks

An honest registry can serve an older `latest` after the following events. Reset
the marks after any of them:

- The registry is restored from a backup.
- A lagging read replica is promoted to primary.
- The newest version is purged, erased, retired under the
  [no-inheritance rule](../deployment/content/layers#inheritance), or leaves the
  caller's view.
- An identity in another organization, using the same registry URL and cache
  directory, loads an artifact with the same ID.

To reset, stop every MCP server that uses the cache directory, then run
`podium cache reset-revisions`, optionally followed by the artifact IDs to
reset. The command fails while any MCP server holds the index database.
[`podium cache reset-revisions`](../reference/cli#podium-cache-reset-revisions)
lists its options and exit codes.

### Refusals that a new session clears

The following refusals need no reset, because a new session clears them.

- **Read-only lag.** While the registry is in read-only mode, a replica that lags
  the primary can serve an older `latest`. The MCP server refuses it, and the
  registry pins that session to the older version. The session stays refused
  for that artifact after the registry leaves read-only mode. A session that did
  not load the artifact during the lag loads the current `latest` once read-only
  mode ends.
- **Restart with a reused session.** The MCP server keeps session state in
  memory. After it restarts, a host that reuses a `session_id` whose pinned
  version is older than a version another session has since loaded is refused
  for that artifact.

A new session loads the current `latest`. The host starts one by passing a new
`session_id`, and a load that passes none gets a new session when `podium-mcp`
restarts. [`materialize.stale_resolution` details](../reference/error-codes#materializestale_resolution-details)
lists the error's fields.
