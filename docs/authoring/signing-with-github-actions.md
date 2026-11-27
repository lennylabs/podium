---
title: Signing with GitHub Actions
nav_order: 11
description: Set up a GitHub Actions workflow that signs a layer's artifacts on each push and publishes the signed state to a branch the registry tracks, and register the layer with a trust policy that accepts those signatures.
---

# Signing with GitHub Actions

A layer whose trust policy requires author attestations needs a signing step in its repository. This page covers the workflow that `podium attest init` writes, the registration with `--trust-preset github-actions`, and the variants for several signers, an organization-wide workflow, private repositories, and other CI systems.

The workflow signs on each push to the default branch and publishes the signed state to the `podium/signed` branch. The registry tracks `podium/signed`, so the content it ingests carries its signatures in the same tree.

> [!NOTE]
> The registry stores and validates a layer's trust policy in this release, and no ingest step verifies attestations against it yet. The statements below about what the registry refuses describe that verification.

---

## Setup

Run `podium attest init` at the top of the repository's working tree. The command reads the `origin` remote, requests the repository's record from the GitHub API, and writes `.github/workflows/podium-attest.yml`:

```bash
podium attest init --root skills
```

```
Wrote .github/workflows/podium-attest.yml
Commit the file and push it to main. Then register the layer:
  podium layer register --id <id> --repo https://github.com/acme/finance-skills --root skills --trust-preset github-actions
```

Commit the file and push it to the default branch. The first run creates `podium/signed`. Then register the layer with the printed command, with the layer's ID in place of `<id>`:

```bash
podium layer register --id finance --repo https://github.com/acme/finance-skills --root skills --trust-preset github-actions
```

- Each `--root` names one layer root of the repository, and the workflow signs every root in one run. Without `--root`, the top of the repository is the layer root. Each segment of a root holds only ASCII letters, digits, `.`, `_`, and `-`.
- The command refuses to replace an existing workflow file. `--force` replaces it.
- `GH_TOKEN` or `GITHUB_TOKEN` supplies a token for the request. A public repository needs none. `PODIUM_GITHUB_API_TIMEOUT` sets the deadline of the request, 30 seconds by default.
- When the request fails, the command writes the workflow and states that it could not read the repository's visibility.
- The workflow installs the `podium` release whose version wrote it. The release binaries carry build provenance attestations from 0.8.0 on, and the workflow verifies them, so a workflow written by an earlier version, such as a build from source, installs 0.8.0. Releases before 0.8.0 cannot be installed by the workflow.

### The registration preset

`--trust-preset github-actions` requests the repository's record from `https://api.github.com` and registers the layer at the ref `podium/signed` with mode `require` and one signer:

| Member | Value |
|:--|:--|
| `issuer` | `https://token.actions.githubusercontent.com` |
| `identity` | `https://github.com/<owner>/<repo>/.github/workflows/podium-attest.yml@refs/heads/<default branch>` |
| `source_repository` | `https://github.com/<owner>/<repo>` |
| `source_repository_id` | The repository's numeric identifier |
| `source_repository_owner_id` | The owner's numeric identifier |
| `source_repository_ref` | `refs/heads/<default branch>` |

The command prints the resolved policy on standard error before it registers. A repository or owner name can be registered again by another party after a rename or a deletion, and an identifier is not assigned twice, so the identifier members refuse a certificate from such a party.

`--trust` and `--ref` replace the mode and the ref. Each `--trust-signer` replaces the issuer and the identity and keeps the binding members. The other trust flags are refused beside the preset. The preset registers nothing, and prints the explicit trust flags in its place, in these cases:

- The `--repo` is not on `github.com`. The command then sends no request, so the token reaches no other host.
- The request fails, reaches its deadline, or receives a status other than 200. A private repository answers 404 without a token that reads it.
- The repository was renamed, or the letter case of `--repo` differs from its name. The message names the current name.
- The repository is private or internal. See [Private repositories](#private-repositories).

After a rename, register the layer again under its ID with the new URL and the preset. The identifier members stay the same, and the preset rebuilds the identity and the name member. The re-registration replaces the stored layer, so restore its order with `podium layer reorder` and register its new webhook secret on the repository.

---

## The workflow

`podium attest init --root skills` in a repository whose default branch is `main` writes this file:

```yaml
# Signs the Podium layers of this repository on each push to main and
# publishes the signed state to the podium/signed branch. A registry
# tracks that branch.
# Written by `podium attest init` (podium v0.8.0).
name: podium-attest

on:
  push:
    branches: [main]
    paths-ignore:
      - 'skills/.podium/attestations/**'
  workflow_dispatch:
    inputs:
      recover:
        description: Rebuild the attestation directories from this run's bundles alone
        type: boolean
        default: false

concurrency:
  group: podium-attest-${{ github.ref }}
  cancel-in-progress: false

permissions: {}

env:
  PODIUM_VERSION: v0.8.0
  PODIUM_ROOTS: skills
  PODIUM_MAIN_BRANCH: main
  PODIUM_RECOVER: ${{ github.event_name == 'workflow_dispatch' && inputs.recover }}
  PODIUM_SIGNED_REF: podium/signed

jobs:
  subjects:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    outputs:
      sign: ${{ steps.guard.outputs.sign }}
      files: ${{ steps.subjects.outputs.files }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - name: Guard
        id: guard
        run: |
          set -euo pipefail
          stop() {
            echo "::notice::podium-attest did not sign: $1"
            echo "podium-attest did not sign: $1" >> "$GITHUB_STEP_SUMMARY"
            echo "sign=false" >> "$GITHUB_OUTPUT"
            exit 0
          }
          if [ -z "$PODIUM_MAIN_BRANCH" ] || [ "$GITHUB_REF" != "refs/heads/$PODIUM_MAIN_BRANCH" ]; then
            stop "the run is on $GITHUB_REF, and only the branch ${PODIUM_MAIN_BRANCH:-(unknown)} is signed"
          fi
          tip=$(git ls-remote origin "refs/heads/$PODIUM_MAIN_BRANCH" | awk -v ref="refs/heads/$PODIUM_MAIN_BRANCH" '$2 == ref { print $1 }')
          if [ "$tip" != "$GITHUB_SHA" ]; then
            stop "commit $GITHUB_SHA is not the tip of $PODIUM_MAIN_BRANCH (${tip:-none}); the run for the tip signs the current state"
          fi
          echo "sign=true" >> "$GITHUB_OUTPUT"
      - name: Install podium
        if: steps.guard.outputs.sign == 'true'
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail
          case "$(uname -m)" in
            x86_64) arch=amd64 ;;
            aarch64 | arm64) arch=arm64 ;;
            *) echo "::error::no podium release for $(uname -m)"; exit 1 ;;
          esac
          dir="$RUNNER_TEMP/podium-bin"
          mkdir -p "$dir"
          gh release download "$PODIUM_VERSION" --repo lennylabs/podium --pattern "podium-linux-$arch" --dir "$dir"
          gh attestation verify "$dir/podium-linux-$arch" --repo lennylabs/podium \
            --signer-workflow lennylabs/podium/.github/workflows/release.yml
          mv "$dir/podium-linux-$arch" "$dir/podium"
          chmod +x "$dir/podium"
          echo "$dir" >> "$GITHUB_PATH"
      - name: Write subjects
        id: subjects
        if: steps.guard.outputs.sign == 'true'
        run: |
          set -euo pipefail
          read -r -a roots <<< "$PODIUM_ROOTS"
          if [ "${#roots[@]}" -eq 0 ]; then
            echo "::error::no layer root is named"
            exit 1
          fi
          files=()
          i=0
          for root in "${roots[@]}"; do
            i=$((i + 1))
            listed=$(podium attest --commit HEAD --root "$root" --out "$RUNNER_TEMP/podium-subjects/$i")
            while IFS= read -r path; do
              files+=("$i/$(basename "$path")")
            done <<< "$listed"
          done
          echo "files=$(printf '%s\n' "${files[@]}" | jq -R . | jq -cs .)" >> "$GITHUB_OUTPUT"
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        if: steps.guard.outputs.sign == 'true'
        with:
          name: podium-subjects
          path: ${{ runner.temp }}/podium-subjects/
          if-no-files-found: error
          retention-days: 1
          overwrite: true

  sign:
    needs: subjects
    if: needs.subjects.outputs.sign == 'true'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
      attestations: write
    strategy:
      fail-fast: true
      matrix:
        file: ${{ fromJSON(needs.subjects.outputs.files) }}
    steps:
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: podium-subjects
          path: ${{ runner.temp }}/podium-subjects
      - id: attest
        uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
          subject-checksums: ${{ runner.temp }}/podium-subjects/${{ matrix.file }}
      - name: Keep the bundle
        env:
          BUNDLE: ${{ steps.attest.outputs.bundle-path }}
          FILE: ${{ matrix.file }}
        run: |
          set -euo pipefail
          mkdir -p "$RUNNER_TEMP/podium-bundles/$(dirname "$FILE")"
          cp "$BUNDLE" "$RUNNER_TEMP/podium-bundles/$FILE.jsonl"
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: podium-bundle-${{ strategy.job-index }}
          path: ${{ runner.temp }}/podium-bundles/
          if-no-files-found: error
          retention-days: 1
          overwrite: true

  publish:
    needs: [subjects, sign]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - name: Install podium
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail
          case "$(uname -m)" in
            x86_64) arch=amd64 ;;
            aarch64 | arm64) arch=arm64 ;;
            *) echo "::error::no podium release for $(uname -m)"; exit 1 ;;
          esac
          dir="$RUNNER_TEMP/podium-bin"
          mkdir -p "$dir"
          gh release download "$PODIUM_VERSION" --repo lennylabs/podium --pattern "podium-linux-$arch" --dir "$dir"
          gh attestation verify "$dir/podium-linux-$arch" --repo lennylabs/podium \
            --signer-workflow lennylabs/podium/.github/workflows/release.yml
          mv "$dir/podium-linux-$arch" "$dir/podium"
          chmod +x "$dir/podium"
          echo "$dir" >> "$GITHUB_PATH"
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          pattern: podium-bundle-*
          merge-multiple: true
          path: ${{ runner.temp }}/podium-bundles
      - name: Publish
        run: |
          set -euo pipefail
          read -r -a roots <<< "$PODIUM_ROOTS"
          dirs=()
          for root in "${roots[@]}"; do
            r="${root#/}"
            r="${r%/}"
            dirs+=("${r:+$r/}.podium/attestations")
          done
          git config user.name "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
          main=$(git rev-parse HEAD)
          for attempt in 1 2 3 4 5; do
            git read-tree "$main"
            rm -rf -- "${dirs[@]}"
            prev=""
            listed=$(git ls-remote origin "refs/heads/$PODIUM_SIGNED_REF")
            if [ -n "$(printf '%s\n' "$listed" | awk -v ref="refs/heads/$PODIUM_SIGNED_REF" '$2 == ref { print $1 }')" ]; then
              git fetch --no-tags --depth=1 origin "+refs/heads/$PODIUM_SIGNED_REF:refs/podium/signed-tip"
              prev=$(git rev-parse refs/podium/signed-tip)
            fi
            if [ -n "$prev" ] && [ "$PODIUM_RECOVER" != "true" ]; then
              for d in "${dirs[@]}"; do
                if git cat-file -e "$prev:$d" 2> /dev/null; then
                  git checkout "$prev" -- "$d"
                fi
              done
            fi
            i=0
            for root in "${roots[@]}"; do
              i=$((i + 1))
              podium attest import --root "$root" --commit "$main" "$RUNNER_TEMP/podium-bundles/$i/"*.jsonl
            done
            git add -A -f -- "${dirs[@]}"
            tree=$(git write-tree)
            if [ -n "$prev" ]; then
              commit=$(git commit-tree "$tree" -p "$prev" -p "$main" -m "podium-attest: sign $main")
            else
              commit=$(git commit-tree "$tree" -p "$main" -m "podium-attest: sign $main")
            fi
            tip=$(git ls-remote origin "refs/heads/$PODIUM_MAIN_BRANCH" | awk -v ref="refs/heads/$PODIUM_MAIN_BRANCH" '$2 == ref { print $1 }')
            if [ "$tip" != "$main" ]; then
              echo "::notice::podium-attest did not publish: commit $main is no longer the tip of $PODIUM_MAIN_BRANCH (${tip:-none}); the run for the tip publishes the current state"
              echo "podium-attest did not publish: $main is no longer the tip of $PODIUM_MAIN_BRANCH" >> "$GITHUB_STEP_SUMMARY"
              exit 0
            fi
            if git push origin "$commit:refs/heads/$PODIUM_SIGNED_REF"; then
              echo "podium-attest signed $main and pushed $commit to $PODIUM_SIGNED_REF" >> "$GITHUB_STEP_SUMMARY"
              exit 0
            fi
            sleep $((attempt + RANDOM % (2 * attempt)))
          done
          echo "::error::the push to $PODIUM_SIGNED_REF was refused 5 times"
          exit 1
```

The workflow runs these steps:

1. A push to `main` starts a run. The path filter ignores the attestation directory. The concurrency group, one per ref, keeps two runs of `main` from overlapping, and a run on another ref cannot displace a pending run of `main`. A manual run takes a `recover` input.
2. The guard stops the run without signing unless its commit is the current tip of `main` on the remote. A full re-run of an old workflow run stops here, and the run for the tip signs the current state. A stopped run ends successfully with a notice.
3. The `subjects` job downloads the `podium` release that wrote the file, verifies the binary's build provenance attestation with `gh attestation verify --signer-workflow lennylabs/podium/.github/workflows/release.yml`, and runs `podium attest --commit HEAD` for each root. A binary that does not verify fails the job. Reading the commit gives the digests the registry computes, whatever line-ending or Git LFS filters a checkout applies.
4. The `sign` job runs `actions/attest-build-provenance` once for each subjects file. Each file holds at most 1024 subjects, the limit of the action.
5. The `publish` job fetches the tip of `podium/signed` and restores each attestation directory from it, so the bundles of another signer stay. It runs `podium attest import` on the new bundles, which prunes the bundles they replace.
6. It commits the tree of the signed commit with the attestation directories, with the tip of `podium/signed` and the signed commit as parents. Before it pushes, it checks again that the signed commit is the tip of `main`, and otherwise publishes nothing and ends with a notice. A re-run of the `publish` job alone of an old run reuses the guard's earlier result, and this check keeps it from moving `podium/signed` back to an older state. It pushes the commit to `podium/signed`. The push is a fast-forward, so the branch is append-only. When another writer moved the branch first, the job repeats the fetch, the restore, and the commit, up to 5 attempts.

Each action is pinned to a commit hash, and only the `sign` job holds the `id-token: write` and `attestations: write` permissions. Only the `publish` job can write to the repository.

### Failed runs and recovery

A failed run leaves `podium/signed` where it was, and the registry keeps serving the previous state. Run the workflow again on the tip of `main`.

A manual run with `recover` set skips the restore, so the new commit's attestation directories hold only that run's bundles. Use it when a commit on `podium/signed` left a directory over the limits of `PODIUM_ATTESTATION_MAX_BYTES` or `PODIUM_ATTESTATION_MAX_BUNDLES`, or holding files the registry refuses. A second signer attests again after a recovery run.

A rewrite of `main` needs no change to the workflow. The next run signs the new tip, and its commit on `podium/signed` has the previous tip and the new `main` as parents, so the push stays a fast-forward.

### Protecting the signed branch

The integrity of ingest does not depend on who can push to `podium/signed`: a commit that another party pushes there carries no valid signature for changed content, and the registry refuses it. A party who can push there can still stall ingest with a refused commit. Restrict pushes to `podium/signed` to the workflow with a repository ruleset.

---

## Several signers

A second signer, such as a scanner, signs its own statements over the same subjects. It runs as a job of this workflow before the `publish` job, or in a workflow of its own that appends a commit with its bundles to the tip of `podium/signed`. Such a commit has the tip as its only parent, and its push repeats the fetch on a refusal, as step 6 does. A separate workflow uses a concurrency group of its own, because a group keeps one pending run and a shared group would let a scanner run cancel a pending signing run.

A second signer that runs as a job of this workflow receives the certificate identity of the build, so its statements use a predicate type of their own. `podium attest import` leaves the other signer's bundles in place.

---

## Organization-wide workflow

An organization can keep the signing steps in one shared workflow that every layer repository calls. Write it into the organization's shared repository, such as `acme/.github`:

```bash
podium attest init --reusable
```

In each layer repository, write the workflow that calls it:

```bash
podium attest init --root skills --shared-workflow acme/.github
```

The caller workflow calls the shared workflow at `main`. The printed registration command adds the shared workflow's identity as an explicit `--trust-signer`, and the preset keeps the binding to the layer's repository and default branch:

```bash
podium layer register --id finance --repo https://github.com/acme/finance-skills --root skills \
  --trust-preset github-actions \
  --trust-signer 'https://token.actions.githubusercontent.com=https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main'
```

Every repository that may call the shared workflow obtains a certificate with its identity, so the binding members are what restrict a signature to the layer's repository and branch. The shared workflow's guard also stops a run unless the calling ref is the default branch of the calling repository, which it reads from the event's repository record. A caller cannot change that record or edit the shared workflow.

---

## Private repositories

GitHub signs for a private or internal repository with its own Sigstore instance, which publishes no repository name to a public transparency log. Signing such a repository needs GitHub Enterprise Cloud. The preset covers public repositories, so `podium attest init` prints the explicit trust flags with `--trust-instance github` for a private repository.

The registry's operator configures the `github` instance in `registry.yaml` with a trusted root file that holds the line of GitHub's instance from `gh attestation trusted-root`:

```bash
gh attestation trusted-root | grep fulcio.githubapp.com > github-trusted-root.json
```

[Configuration → Author attestation](../reference/configuration#author-attestation) covers the `sigstore.instances` entry.

---

## Other environments

`podium attest init` refuses a repository whose `origin` is not on `github.com`. In another CI system, sign the statements that `podium attest --format statement` writes with a keyless signing tool, such as `cosign attest-blob --statement <file> --bundle <file>`, and import the bundles with `podium attest import`. A recipe written by hand meets two conditions:

- The signed commit is the commit the registry tracks or an ancestor of it. A recipe that publishes to a separate branch gives each published commit the signed commit as a parent, as the workflow above does.
- The statements of the covering signer together list every artifact of the layer. A recipe that signs every file `podium attest` writes meets it.

[`podium attest`](../reference/cli#podium-attest) describes the subject files.
