---
title: Troubleshooting
nav_order: 9
description: "Day-two operations for a clustered Podium deployment: capacity planning, backup and restore, signing key rotation, upgrades, read-only mode, outbound webhook receivers, security review, and troubleshooting."
---

# Troubleshooting

This page covers day-two operations for a [clustered](../modes/clustered) Podium deployment. It is for the operators who size, back up, upgrade, and secure a running registry. For a single machine running one binary, see [Single node](../modes/single-node).

Audit records, Prometheus metrics, tracing, notifications, and alert rules are covered in [Audit and observability](observability).

---

## Troubleshooting

### Operational issues

| Symptom | Cause | Fix |
|:--|:--|:--|
| Embedding requests are rate-limited during a bulk reingest. | OpenAI and Voyage rate-limit aggressively under bulk reingest. | Stagger `podium layer reingest` across layers, or switch to `ollama` pointed at a local model server for inference during reingest storms. |
| The pgvector index bloats. | Many embeddings have churned. | `REINDEX` the vector index quarterly, or set up `auto_vacuum` aggressively. |
| Stored embeddings come from a different provider or model than the one configured. | `PODIUM_EMBEDDING_PROVIDER`, the embedding model, or a self-embedding backend's hosted model changed. | Run `podium admin reembed` with no scoping flag in every tenant. On Pinecone, Weaviate, or Qdrant, first point the registry at a fresh index or collection, because those backends record no model per row and every tenant shares the index. [Switching backends on a running deployment](../services/vector-backends#switching-backends-on-a-running-deployment) gives the procedure. |
| Materialization latency on developer machines sits well above the SLO. | `PODIUM_CACHE_DIR` points at a cache on a network filesystem or a slow disk. | Keep the cache at the default `~/.podium/cache/` on local disk. |
| Ingests are missing after a read-only window. | The registry refuses webhook deliveries while it is read-only, and the Git host does not deliver them again on its own. GitHub, for example, does not automatically redeliver a failed delivery. | Trigger a manual `podium layer reingest` after recovery. |
| Ingest is rejected after a force-push to a Git source layer. | `force_push_policy: strict` is configured. The default policy is tolerant: it emits a `layer.history_rewritten` event and preserves prior commits in the content store. | Coordinate force-pushes with authors. |
| Intermittent `auth.token_expired` errors for tokens near expiry. | NTP drift on a registry node. The registry validates a token's expiry against its own clock with no configured leeway. | Keep registry hosts on NTP and monitor clock skew. |
| An OIDC group membership change has not taken effect. | Membership changes propagate through SCIM push from the IdP to `/scim/v2/`. The registry has no pull side and no sync command, so an IdP that does not push leaves group membership to update on the user's next login. | Trigger a push from the IdP's provisioning console when a membership change has to land immediately. |

### Startup refusals

#### The registry refuses to start with `config.scim_store_unavailable`

- **Cause.** The error names `PODIUM_SCIM_STORE_PATH` and the cause: a non-empty file that does not parse, a path that is a directory or has a non-directory parent, or a parent directory that cannot be created or written.
- **Fix.** Fix the permissions, or point the variable at a valid path. For a corrupt file, move it aside and start the registry, which loads a missing file as an empty directory. Then trigger a full push from the IdP's provisioning console to repopulate users and groups, as the SCIM entry under [Operational issues](#operational-issues) describes.

Unsetting the variable keeps the directory in memory, so it is lost on every restart.

When the store becomes unwritable after startup, the receiver answers a create or replace push with HTTP 400 `invalidValue`, and that record is served until restart and then lost. It answers a delete push with HTTP 500, and the deleted record returns on restart. In either case, fix the directory and trigger a full push.

#### Audit configuration refusals

The refusals `config.audit_sink_unavailable`, `config.audit_anchor_key_unavailable`, and `config.audit_anchor_key_shared` are described in [Troubleshooting](observability#troubleshooting) on the Audit and observability page.

### Public-mode misconfiguration

A misconfigured public-mode deployment is the most common security-relevant operational anomaly, because the registry serves correctly, to everyone.

**Detection:**

- `/healthz` returns `mode: public`.
- Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`.
- The startup banner shows the public-mode warning.
- `podium status` surfaces the flag.

**Mitigation:**

1. Confirm whether public mode was the intended deployment posture. If it was, no action is needed, because the audit log already records the intent.
2. If public mode was not intended, such as through a misconfigured environment variable, a copy-pasted CLI flag, or an accidental container image tag, stop the registry. Remove `--public-mode` or unset `PODIUM_PUBLIC_MODE`, and restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry, which the safety check prevents unless `--allow-public-bind` was set, treat it as a security incident. Rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the organization's incident-response procedure.

**Prevention.** Set `PODIUM_NO_AUTOSTANDALONE=1` in container-image and Helm-chart deployments, and use `--strict` to refuse anything but explicitly configured deployments. Make production CI templates fail fast when `PODIUM_PUBLIC_MODE` appears in an environment list.

---

## When to escalate

Escalate to support, or open an issue, in the following cases:

- **Audit chain gap.** The scheduled integrity pass records an `audit.gap_detected` event. Treat it as a security incident, and capture evidence before any cleanup.
- **Repeated signature or content-hash refusals.** Check each for an upgrade or key cause first, as the list below describes.
- **Unexplained latency.** Sustained latency degradation that does not track CPU, memory, or database load often indicates a query-plan regression after a Postgres major upgrade.
- **Out-of-band ingest events.** Artifacts that appear in the registry without a corresponding `artifact.published` outbound webhook indicate a webhook configuration or processing failure.

Check signature and content-hash refusals as follows before escalating:

- **A refusal the registry logs.** The registry logs the tenant, artifact ID, version, and reason of a `materialize.content_hash_mismatch`, `materialize.signature_invalid`, or `materialize.signature_missing` refusal. Compare the artifact ID and version against the per-row `rehash:` lines the rewrite logged. Those lines come from the first start's log only where the store is the SQLite store in the key file's directory. Otherwise they come from the `sign-stored-rows` run's log, or from the migrate Job's log on a chart deployment. A row that summary names is upgrade residue, repaired as the release's upgrade note states, and is not escalated. Compare the registry's key file with the backed-up key as well, because the registry refuses every row signed under a key outside its verification key set. Repair a mismatch as the signature-storm entry in `deploy/runbook.md` states.
- **A registry refusal for a row the summary does not name.** Under the key that signed the store, such a refusal means the row's stored bytes were altered, in the metadata store or in object storage, or an object-held body is missing. Read the refusal's logged reason, check the object store for the row's bodies, and then escalate.
- **A consumer-returned `materialize.content_hash_mismatch`.** This, or `materialize.fetch_failed` naming a large-resource `content hash mismatch`, means a response or an object-storage body was altered after the registry admitted the row. Escalate it.
- **A consumer-returned `materialize.signature_invalid` or `materialize.signature_missing`.** First confirm that the consumer's `PODIUM_SIGNATURE_PROVIDER` is `registry-managed`, that the key set it resolves contains the key the registry signs under, and that the registry does not run with `PODIUM_SIGN=none` under an `always` policy. With that configuration correct, escalate it as a forged or stripped signature.

For each case, capture the relevant log lines with their trace IDs, the affected tenant ID, the affected artifact IDs, and a brief timeline.
