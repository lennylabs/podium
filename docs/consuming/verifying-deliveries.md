---
title: Verifying deliveries
nav_order: 6
description: "How podium-mcp verifies the signature of each artifact it loads and refuses a stale latest version, and the settings that control both checks."
---

# Verifying deliveries

## Signature verification

Under the default `PODIUM_VERIFY_SIGNATURES=always` policy, every artifact the
MCP server loads must carry a signature that verifies. A load of an unsigned
artifact fails with `materialize.signature_missing`. The `never` policy checks
nothing and fits only a registry running with `PODIUM_SIGN=none`.

### Signature settings

- **`PODIUM_SIGNATURE_PROVIDER`.** Under a policy above `never`, `noop` refuses
  the start, because it verifies nothing. Any other value, including
  `sigstore-keyless`, refuses the start with `config.invalid`. The registry
  signs every delivery record with its registry-managed key, and
  `sigstore-keyless` applies to `podium sign` and `podium verify` only.
- **`PODIUM_SIGNATURE_VERIFY_KEY`.** The `registry-managed` provider verifies
  with this key set. A delivery signature that verifies under any key of the set
  is accepted. Set to the empty string, the variable counts as unset. A
  non-empty value is authoritative, and under a policy above `never` an entry
  that is empty or does not decode refuses the start.
- **`PODIUM_SIGN_KEY_PATH`.** When `PODIUM_SIGNATURE_VERIFY_KEY` is unset, the
  `public:` line and every `verify:` line of this key file supply the
  verification key set. The MCP server reads the file and never writes it.

### Key set resolution

Under the default `always` policy, the MCP server resolves its verification key
set once at start. It refuses to start with
`config.signature_provider_unavailable` when it resolves none.

| Consumer | Variables to set |
|:--|:--|
| A consumer of a standard deployment | `PODIUM_SIGNATURE_PROVIDER` and `PODIUM_SIGNATURE_VERIFY_KEY`, as each recipe shows. |
| A standalone consumer running under the same user account as its registry | Neither. The set resolves from the registry's own key file. |
| A standalone consumer whose registry runs with a non-default `PODIUM_SIGN_KEY_PATH` | `PODIUM_SIGN_KEY_PATH`, set to the same path. |

During a registry key rotation, add the new key to `PODIUM_SIGNATURE_VERIFY_KEY`
as a comma-separated list before the registry signs under it.

### Cached records after a key rotation

The MCP server delivers a record from its cache only after the record passes the
same signature check as a live response. A cached delivery signature fails that
check when a key the consumer no longer trusts made it. One example is a record
cached before a rotation whose retired key has since left the set.

| `PODIUM_CACHE_MODE` | Outcome for a cached record that fails the check |
|:--|:--|
| `always-revalidate` | Cache miss. The MCP server refetches the artifact, verifies the response, and replaces the cached record. When the registry is unreachable, the load returns `network.registry_unreachable`. |
| `offline-first` | Cache miss, handled as above. When the registry is unreachable, the load returns the offline status with no artifact. |
| `offline-only` | Refused with `materialize.signature_invalid`. |

The MCP server never delivers the failing record. Before a retired key leaves the
set, an `offline-only` consumer either keeps the retired key in its set, or
clears its cache directory and refills it in `always-revalidate` or
`offline-first`.

---

## Stale `latest` protection

The MCP server refuses a `latest` load that resolves to an older revision than
one it already accepted. This stops a proxy or cache between the MCP server and
the registry from serving an older signed version.

### How the check works

- The MCP server records, per registry and artifact, the artifact revision of
  the newest `latest` load it accepted. The record lives in its index under
  `PODIUM_CACHE_DIR`.
- A later `latest` load whose revision is lower fails with
  `materialize.stale_resolution`.
- Within one session, the MCP server compares a `latest` load with the first one
  it accepted in that session, including one served from the cache after the
  registry confirmed it. A session-pinned version therefore still loads.
- An answer read through `resources/read` records the first accepted answer for
  the MCP server's own session only.
- Only `latest` loads answered by the registry are compared. A load that names a
  version, a range, or a content hash is never compared, and neither is a load
  served from the cache.
- A backport ingested after a newer version, and a new deprecated version, leave
  every honest `latest` load at or above the mark.
- The first `latest` load under an empty cache directory is accepted, and a
  registry change starts with no marks. A load that a later gate refuses leaves
  the mark unchanged.

### Limits

- The marks are not kept per organization. Identities in different
  organizations that use one registry URL should use separate cache
  directories.
- When another MCP server holds the index, the marks last only for the process,
  and the MCP server logs a warning.
- A stored mark that cannot be read is deleted with a warning.
- The mark gives no protection against a party that can write
  `PODIUM_CACHE_DIR`. Under `PODIUM_VERIFY_SIGNATURES=never`, it refuses only
  unmodified replays.

### Resetting the marks

Run `podium cache reset-revisions` after any of the following events:

- The registry is restored from a backup.
- A lagging replica is promoted.
- The newest version is purged, erased, retired under the
  [no-inheritance rule](../deployment/content/layers#inheritance), or leaves the
  caller's view.

### Refusals that a new session clears

Two refusals need no reset, and a new session clears both.

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
restarts.

---
