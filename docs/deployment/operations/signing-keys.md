---
title: Signing keys
nav_order: 2
description: "Generate, distribute, back up, and rotate the registry signing key, recover from a lost or compromised key, and sign stored rows."
---

# Signing keys

## Signing key operations

### Rotating the signing key across replicas

The [Operator guide](#rotating-the-signing-key) gives the rotation procedure. Across replicas, every replica trusts the new key before any replica signs under it, so the chart's Secret changes twice:

1. Copy the current key file out of the Secret into a scratch directory, with `kubectl get secret podium-signing-key -o jsonpath='{.data.registry-signing\.key}' | base64 -d`, and write it with mode `0600`.
2. Run `podium admin signing-key rotate --key-file <copy> --staged-out <staged>`. The staged file is the previous key file plus a `verify:` line for the new key. The copy becomes the rotated file: the new signing key plus a `verify:` line for each previous key. Add the new key to every consumer's `PODIUM_SIGNATURE_VERIFY_KEY`, for example as the comma-separated list the command prints on its first line.
3. Replace the Secret's content with the staged file, with `kubectl create secret generic podium-signing-key --from-file=registry-signing.key=<staged> --dry-run=client -o yaml | kubectl apply -f -`. Restart the Deployment with `kubectl rollout restart deployment/podium-podium`, and wait for the restart to finish with `kubectl rollout status deployment/podium-podium`. Every replica still signs under the previous key and now trusts the new one. Start step 4 only after the rollout status command returns, because a pod from before the restart does not trust the new key and refuses the rows a pod signing under it ingests.
4. Replace the Secret's content with the rotated file, restart the Deployment again with `kubectl rollout restart deployment/podium-podium`, and wait with `kubectl rollout status deployment/podium-podium`. Every replica now signs under the new key and trusts the previous one. Start step 5 only after the rollout status command returns, because a pod from before this restart still signs under the previous key, and a row it signs after a step 5 run is not counted by that run.
5. Run `kubectl exec deployment/podium-podium -- /usr/local/bin/podium-server sign-stored-rows`, which re-signs the stored rows under the new key while the replicas serve. The command may take `--plan-digest=<digest>` from a preceding `sign-stored-rows --dry-run` run, which binds the run to the reviewed plan and refuses it with exit status 3, writing nothing, when the plan changed. The digest is optional here, because the run passes no `--include-unsigned`.
6. For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the previous key in its set, or clear its cache directory and refill it in `always-revalidate` or `offline-first`, before the previous key leaves that consumer's set. An `offline-only` consumer refuses a cached delivery signature made under a key it no longer trusts.
7. Remove the previous key only after a run of step 5 that exits 0 reports `0 row(s) still signed under it` for its `key_id`. Delete its `verify:` line from the key file, replace the Secret's content, restart the Deployment with `kubectl rollout restart deployment/podium-podium`, wait with `kubectl rollout status deployment/podium-podium`, and remove the key from every consumer's set.

Back up the rotated file, and delete the scratch copies once the Secret holds it.

---

## Losing the signing key

The registry signing key belongs in the backup. What happens when it is missing depends on whether the release's first-start rewrite of stored content hashes has completed:

- Before that rewrite has completed, a start with the key file missing is refused when the store holds signed rows.
- Once that rewrite has completed, a start with the key file missing generates a fresh keypair and proceeds. The registry then refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy.

To repair, stop the registry, restore the key file from the backup, and start it again. The registry loads its signing key once at start. Restoring the file leaves refused every row the fresh key signed in the meantime. To admit those rows, list the fresh key's public half on a `verify:` line of the restored file and restart the registry.

When only the lost key's public half survives, list it on a `verify:` line of the key file the registry now holds and restart the registry, which admits those rows. Then run `podium admin sign-stored-rows`, which re-signs them under the current key. [Signing failures](#signing-failures) states both repairs.

When the store holds no record that the first-start rewrite of stored content hashes has completed, `sign-stored-rows` performs that rewrite. It must therefore run only while no registry process on the previous release serves the store, and it cannot detect such a process ([The `sign-stored-rows` command](#the-sign-stored-rows-command)).

## Rotating the signing key

The key file at `PODIUM_SIGN_KEY_PATH` holds the registry's signing material:

- A `private:` line and a `public:` line carry the signing keypair.
- Zero or more `verify:` lines each carry a base64 Ed25519 public key trusted for verification only.
- The `public:` key and the `verify:` keys form the registry's verification key set.

The registry signs every new envelope under the `private:` key. It admits a stored row whose signature verifies under any key of the set, so a rotation that keeps the retired public key on a `verify:` line leaves every row the retired key signed loadable. Each envelope names its key in a `key_id` field, the lowercase hex of the first 8 bytes of the SHA-256 digest of the public key.

A consumer accepts a delivery signature under any key of its own set. It reads the set from `PODIUM_SIGNATURE_VERIFY_KEY`, as one key or a comma-separated list, or otherwise from the key file's `public:` and `verify:` lines.

### Rotate the key of a single registry process

1. **Generate the new key.** Run `podium admin signing-key rotate --key-file <path>` against the key file the registry reads.
   - The command reads no environment variable and no default path, so `--key-file` is required.
   - It writes a new signing keypair and moves the previous public key, and every previous `verify:` key, onto `verify:` lines.
   - It prints the new verification key set on its first line as a comma-separated list, signing key first. One `key_id=<hex> role=signing` or `key_id=<hex> role=verify` line per key follows.
   - The running registry keeps signing under the previous key, because it loads the key file only at start.
2. **Give consumers the new key set.** Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the printed list, so each consumer holds the new key before the registry signs under it. A consumer that reads the key file itself resolves its set only when it starts. Restart each such consumer after step 1 and before step 3, so it reads the new `public:` line and the retired key's `verify:` line before the registry signs under the new key.
3. **Restart the registry.** It signs under the new key from its first response and admits every row the previous key signed.
4. **Re-sign the stored rows.** Run `sign-stored-rows` against the registry's configuration: as `podium admin sign-stored-rows` on a standalone host, or as `podium-server sign-stored-rows` where only the server binary is installed.
   - It re-signs under the new key every stored row that a `verify:` key verifies and whose stored bytes reproduce its stored hash.
   - It prints `rehash: verify key <key_id>: <n> row(s) still signed under it` for each verification-only key.
   - The run may take `--plan-digest=<digest>` from a preceding `sign-stored-rows --dry-run`, which binds it to the reviewed plan. The digest is optional here, because the run passes no `--include-unsigned`. [The `sign-stored-rows` command](#the-sign-stored-rows-command) describes the command.
5. **Prepare offline-only consumers.** For each consumer that runs with `PODIUM_CACHE_MODE=offline-only`, keep the retired key in its set, or clear its cache directory and refill the cache in `always-revalidate` or `offline-first`, before the retired key leaves that consumer's set. An `offline-only` consumer never calls the registry, so it refuses a cached delivery signature made under a key it no longer trusts with `materialize.signature_invalid`. A consumer in `always-revalidate` or `offline-first` treats such a record as a cache miss and refetches it, so its cache needs no clear.
6. **Remove the retired key.** Remove it only after a run of `sign-stored-rows` that exits 0 reports `0 row(s) still signed under it` for its `key_id`. A run that exits non-zero leaves that count incomplete: repair what its log names and run it again. Then delete the retired key's `verify:` line from the key file, restart the registry, and remove the key from every consumer's set. A row still signed under a key that has left the registry's set is refused with `materialize.signature_invalid`.

### Rotate the key across several registry processes

A deployment that runs more than one registry process adds the new public key to every process's set before any process signs under it.

1. Run `podium admin signing-key rotate --key-file <path> --staged-out <staged>`. Besides the rotated file, the command writes a staged file: the previous key file plus a `verify:` line for the new key.
2. Deploy the staged file to every process and restart each.
3. Set every consumer's `PODIUM_SIGNATURE_VERIFY_KEY` to the list the rotate command prints, as step 2 of the single-process procedure states, before the rotated file reaches any process.
4. Deploy the rotated file and restart each process.
5. Follow steps 4 through 6 of the single-process procedure.

[Rotating the signing key across replicas](#rotating-the-signing-key-across-replicas) gives the commands for the Helm chart.

### Compromised keys

A `verify:` line trusts every envelope its key made, including an envelope made after the key was compromised. `sign-stored-rows` also re-signs a row such a key verifies under the current signing key. Remove a compromised key from every set at once, with no `verify:` line kept for it, and restore the rows it signed from a backup or re-ingest them at a new version.

### The `sign-stored-rows` command

`sign-stored-rows` reads the configuration a registry start reads, opens the same store and object storage, binds no listen address, and exits. It never generates a key.

#### Refusals

- **Signing on.** The command refuses with `config.signature_provider_unavailable`, writing nothing, when the key file at `PODIUM_SIGN_KEY_PATH` is absent. A key file with no `private:` or `public:` line, a line that does not decode, or a `public:` line that is not the public half of the `private:` line refuses it with the same code.
- **Signing off (`PODIUM_SIGN=none`).** The command rewrites without signing and signs no row. It refuses `--include-unsigned` given with `--dry-run` or `--plan-digest` with `config.signature_provider_unavailable`, writing nothing. `--include-unsigned` given alone stays a usage error.
- **Unpersisted key.** The command is subject to the refusal for a signing key that is not persisted with the store, as a registry start is.

#### Running beside a serving registry

When the store records that the first-start rewrite of stored content hashes has completed, the command may run while registry processes on this release serve the store. Each write replaces a row only when the row's stored content hash and stored signature are still the ones the command read.

When that record is absent, the command performs the rewrite in place of the first start and records its completion. It then runs only while no registry process on the previous release serves the store. The command binds nothing and cannot detect such a process, so stop every one before it runs. In that case the command also signs each unsigned row the first start would sign, which it does only when the store is the SQLite store in the key file's directory.

While no completion is recorded, a registry start over any other store that holds a manifest row is refused in either signing mode. That start rewrites no manifest row, signs no row, and records no completion, and its error names this command, which performs the rewrite in place of that start. A store that holds no manifest row records completion at its first start.

#### Signing unsigned rows

Beyond the rows it signs in place of the first start, the command signs a row that carries no signature only under `--include-unsigned`. The flag is the operator's attestation for every unsigned row the reviewed dry run lists, because an unsigned row carries no evidence of who stored it.

| Flag | Effect |
|:--|:--|
| `--dry-run` | Lists every write the command would make, with each row's current signature state and stored content hash, and makes none. The output ends with a `dry-run: plan digest sha256:<hex> over <K> row(s)` line. The dry run shows the unsigned rows before `--include-unsigned` attests them. |
| `--include-unsigned` | Signs rows that carry no signature. Outside `--dry-run`, it requires `--plan-digest=<digest>` with the value from the dry run. |
| `--plan-digest=<digest>` | Binds the run to a reviewed dry run. The command computes the digest of the plan it builds before any write. When that digest differs, it writes no row, no audit event, and no completion record, prints its own plan as `plan:` lines in the dry-run format, and exits with status 3. |

The plan digest has these consequences:

- An unsigned row stored between the dry run and the run refuses the run.
- A row stored after the run builds its plan is left as it is, unsigned, until a later reviewed run.
- A change to the key file's `verify:` lines or signing key after the dry run also refuses the run.

The command never signs a row whose envelope fails under every key of the verification key set.

#### Output and exit status

After the rewrite's summary line, with signing on, the command prints `rehash: <n> unsigned left` and one `still signed under it` line per verification-only key. With signing off, it prints only the summary line.

| Exit status | Meaning |
|:--|:--|
| 0 | Success. |
| 1 | A row holds the completion record back, a write fails, or the command is refused. |
| 2 | A usage error. |
| 3 | The plan differs from the one `--plan-digest` names. |

---

## Signing failures

### `podium-mcp` refuses to start with `config.signature_provider_unavailable`

Under a policy above `never`, the bridge resolves its verification material once at start. It reads `PODIUM_SIGNATURE_VERIFY_KEY` or, when that variable is unset, the `public:` line and every `verify:` line of the registry key file at `PODIUM_SIGN_KEY_PATH` (default `~/.podium/standalone/registry-signing.key`).

- **Cause.** The usual cause is a missing key: a consumer on a machine other than the registry's, with no `PODIUM_SIGNATURE_VERIFY_KEY`. A `PODIUM_SIGNATURE_VERIFY_KEY` with an entry that is empty or does not decode, and `PODIUM_SIGNATURE_PROVIDER=noop`, refuse the start the same way.
- **Fix.** Supply the key set, or set `PODIUM_VERIFY_SIGNATURES=never` against a registry that does not sign.

A bridge whose set lacks the key the registry signs under starts, and refuses each load with `materialize.signature_invalid` instead.

### Loads fail with `materialize.signature_missing`

- **Cause.** The served artifact carries no signature and the policy is `always`. Either the registry runs with `PODIUM_SIGN=none`, or it stored the row unsigned before signing was on, in which case the registry itself refuses the row.
- **Fix for an unsigned registry.** Set `PODIUM_VERIFY_SIGNATURES=never` on its consumers.
- **Fix for rows stored before signing.** Run `sign-stored-rows --include-unsigned --dry-run` and review the rows it lists. Then run `sign-stored-rows --include-unsigned --plan-digest=<digest>` with the digest on its last line, as [The `sign-stored-rows` command](#the-sign-stored-rows-command) states. Alternatively, ingest a new version of the artifact.

A load the signature check refuses records no local `artifact.loaded` event. A load whose inline-resource decode or large-resource fetch fails records none either. An object-storage outage therefore leaves the local stream silent for every large-resource load, where it previously carried one line each. A load the sandbox or runtime gate refuses still records one.

### The key file is present but is not the key that signed the stored rows

- **Cause.** The registry's key file holds a different key from the one that signed the stored rows.
- **Fix on the SQLite store in the key file's directory.** The start is not refused. The first start's rewrite of stored content hashes leaves every such row at its stored hash, with one log line per row classified `signature_unverified`. Restoring the right key alone does not repair a row still at the previous content hash. List the public half of the key that signed the rows on a `verify:` line of the key file, restart the registry, and run `sign-stored-rows`. It moves each such row to the new content hash and re-signs it under the current key.
- **Fix on any other store.** The start is refused while no completion is recorded and the store holds rows, and the `sign-stored-rows` dry run classifies such rows `signature_unverified`. List the signing key's public half on a `verify:` line of the key file, run the dry run, and run `sign-stored-rows` with its `--plan-digest`, repeating the dry run's `--include-unsigned` setting.
- **Fix from a backup.** On the SQLite store in the key file's directory, restoring the backup the upgrade procedure takes and starting with the key that signed the rows also repairs them, at the cost of every write made since that backup. Over any other store, in either signing mode, the restored store holds no completion record. Its start is then refused until a `sign-stored-rows` dry run and a run with its `--plan-digest`, under that key, record completion.

A new version of each affected artifact is the repair only where no copy of that key's public half survives.

### The key file is lost after the first-start rewrite has completed

- **Symptom.** The registry starts, but refuses every row the retired key signed with `materialize.signature_invalid`, to every reader and under any consumer policy. Setting `PODIUM_VERIFY_SIGNATURES=never` does not make those rows load.
- **Cause.** The start is not refused, because the generated-key guard returns once the rewrite's completion record is set. Such a start generates a fresh keypair, signs new ingests under it, and logs nothing about the retired key.
- **Fix.** Stop the registry, restore the key file from the backup, and start it again, because the registry loads its signing key once at start. Restoring the file leaves refused every row the fresh key signed in the meantime. List the fresh key's public half on a `verify:` line of the restored file and restart the registry to admit those rows.
- **Fix when only the public half survives.** The lost key's public half can survive, for example, in a consumer's `PODIUM_SIGNATURE_VERIFY_KEY`. List it on a `verify:` line of the key file the registry now holds and restart the registry, which admits those rows. Then run `sign-stored-rows`, which re-signs them under the current key, so the line can be removed as [Rotating the signing key](#rotating-the-signing-key) states.

### The registry refuses to start because the signing key would be generated under the process's home

- **Cause.** Signing is on and `PODIUM_SIGN_KEY_PATH` is unset, and the store is not the SQLite store beside the default key. The error names `PODIUM_SIGN_KEY_PATH` and `PODIUM_SIGN=none`, and says the key would be generated under the process's home, outside the directory that holds the store. It fires whether or not the store holds a signed row.
- **Fix.** Set `PODIUM_SIGN_KEY_PATH` to a file on the store's persistent storage, or set `PODIUM_SIGN=none`. With `PODIUM_SIGN=none`, a store other than the SQLite store beside the default key that holds rows and no completion record is then refused by the unmigrated-store refusal below, whose repair is `sign-stored-rows`.

The published container image run with its default store keeps the key and the store in the same directory, so it starts.

Two other refusals name `PODIUM_SIGN_KEY_PATH` and mean that the store holds signed rows. One says the rows need their content hash rewritten while no signer is configured. The other says `PODIUM_SIGN_KEY_PATH` names no file. The first comes from a start only over the SQLite store in the key file's directory, and otherwise from the `sign-stored-rows` dry run. Each calls for the key that signed those rows.

### The registry refuses to start and names `sign-stored-rows`

- **Cause.** No completion of the rewrite of stored content hashes is recorded, and the store holds a manifest row, including a soft-deleted one. The store is also not the SQLite store in the directory of the key file at `PODIUM_SIGN_KEY_PATH`, or at the default key location when that variable is unset. The refusal applies in either signing mode. The error names the store, a stored row, and the key location.
- **What the refused start does.** It applies the additive schema and seeds the default tenant and the bootstrap grants. It rewrites no manifest row, signs no row, and records no completion.
- **Other refusals first.** When the key-persistence refusal above, or the refusal for a signing key that did not exist before the start, also applies, the start reports that refusal first.

A co-located SQLite store runs the rewrite at its first start, and a store that holds no manifest row records completion at its first start. For any other store, repair it as follows:

1. Stop every registry process on the previous release.
2. With signing on, when no file exists at the key location, create it with `podium admin signing-key generate --key-file <path>`. Neither the refused start nor `sign-stored-rows` generates a key.
3. Run `sign-stored-rows --dry-run` and review its report. With signing on, add `--include-unsigned` to attest the unsigned rows. With signing off, run it with `PODIUM_SIGN=none` in its environment, because `podium serve --sign none` sets the mode only for its own process.
4. Run `sign-stored-rows` with `--plan-digest` set to the digest on the report's last line, with the same `--include-unsigned` setting and environment.
5. Start the registry again.

On a Helm chart deployment, the migrate Job runs both commands, as [Upgrading the chart from v0.4.0](upgrade-notes#upgrading-the-chart-from-v040) shows.

### The registry or `sign-stored-rows` refuses with `config.signature_provider_unavailable`, naming the key file

- **Cause.** The key file has no `private:` line or no `public:` line, one of its lines does not decode, or its `public:` line is not the public half of its `private:` line.
- **Fix.** Restore the file from the backup, or repair the line the message names.
