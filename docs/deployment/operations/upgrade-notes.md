---
title: Upgrade notes
nav_order: 5
description: "Release-specific upgrade steps, newest release first: the 0.7.0 layer credential columns, the 0.6.0 extends removal, and the 0.5.0 rewrite of stored content hashes."
---

# Upgrade notes

This page lists the upgrade steps that belong to one release, newest release first. [Upgrading](upgrading) gives the general procedure that every upgrade follows. Each release's entry in `CHANGELOG.md` is the authoritative statement of its changes.

## 0.7.0 (unreleased)

The current release is 0.6.0. The notes in this section describe behavior on `main` that ships in 0.7.0.

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
