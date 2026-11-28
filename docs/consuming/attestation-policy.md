---
title: Attestation policy
nav_order: 7
description: "The consumer trust policy that podium-mcp applies to author attestations: where it reads the policy, which sources a repository can set, the machine file, the trusted roots, and the refusals and warnings it reports."
---

# Attestation policy

This page describes the consumer trust policy of the Podium MCP server,
`podium-mcp`: the settings that tell it which authors' signatures an artifact
must carry before the server returns or writes it. It is for developers and
platform engineers who configure the MCP server. The registry side of author
attestation, which verifies signatures when a layer is ingested, is covered in
[Layers](../deployment/content/layers). [Verifying deliveries](verifying-deliveries)
covers the delivery signature, which the server checks first.

---

## What the server checks

On every `load_artifact` call, every `resources/read`, and every delivery from
its content cache, `podium-mcp` runs these checks after the delivery check:

- **Well-formed request.** The requested ID is a sequence of portable names
  separated by `/`, with no `.` or `..` segment, no segment that begins with
  `.`, and no name that Windows or macOS resolves to another one, such as
  `GIT~1`. The server refuses a malformed ID before it sends the request.
- **Request binding.** The record the registry returned has the requested ID
  and meets the requested version. The server trims surrounding white space
  from the `id` and `version` arguments before it sends them.
- **Attestation check.** When the policy resolves the requested ID to `warn` or
  `require`, the server fetches the bundles the record references from
  `GET /v1/attestations/{digest}`, verifies them under the signers of the
  matching rules, and checks that a bundle lists the record's package digest.

The first two checks run under every policy, including no policy. A failure of
the attestation check refuses the load under `require`. Under `warn` the
server delivers the record and places a warning in the `attestation_warning`
member of the result, for example
`finance/close-books: materialize.attestation_untrusted (constraint signer)`.

The server keeps each fetched bundle under `${PODIUM_CACHE_DIR}/attestations/`,
and it checks a cached record again on each delivery. Under
`PODIUM_CACHE_MODE=offline-only`, a record whose bundles are not cached is
refused with `materialize.attestation_missing`.

## The policy file

A policy file is JSON. Rules are matched in file order against the requested
ID, and the first rule whose pattern matches applies. In a pattern, `*` matches
one ID segment and `**` matches zero or more.

```json
{
  "version": 1,
  "instances": [
    {
      "name": "github",
      "trusted_root_file": "github-trusted-root.json",
      "transparency_log": "none",
      "certificate_transparency": "none"
    }
  ],
  "rules": [
    {"ids": ["personal/**"], "mode": "none"},
    {
      "ids": ["**"],
      "mode": "require",
      "requirements": [
        {
          "signers": [
            {
              "issuer": "https://token.actions.githubusercontent.com",
              "identity": "https://github.com/acme/.github/.github/workflows/podium-attest.yml@refs/heads/main",
              "source_repository_owner": "https://github.com/acme",
              "source_repository_ref": "refs/heads/main",
              "instance": "github"
            }
          ]
        }
      ]
    }
  ]
}
```

A relative `trusted_root_file` resolves against the directory of the policy
file. The `public-good` instance is built in, and no file defines it.

## Where the server reads the policy

Every source contributes, and none replaces another. The strictest default
mode among the sources applies to an ID that no rule matches.

| Source | Who can set it | Settings |
|:--|:--|:--|
| `~/.podium/sync.yaml` under the account home | The user | `defaults.attestation.policy`, `policy_file`, `trusted_root_file`, `root_max_age`, `overlay` |
| The machine file | An administrator | The same keys |
| Flags and the configuration file of `podium-mcp` | A repository | `--attestation-policy`, `--attestation-policy-file` |
| The environment of `podium-mcp` | A repository | `PODIUM_ATTESTATION_POLICY`, `PODIUM_ATTESTATION_POLICY_FILE` |
| The workspace `.podium/sync.yaml` and `sync.local.yaml` | A repository | `defaults.attestation.policy`, `policy_file` |

Harnesses read the flags and the environment of `podium-mcp` from project files
that a repository commits, such as a project-level `.mcp.json`, so every
setting of the server itself counts as a repository source. A repository
source can make the policy stricter. It cannot lower the mode that the user's
own `sync.yaml` and the machine file give an ID, and it cannot meet a user's
`require` rule with signers of its own. A user who needs a policy that a
cloned repository cannot lower names the policy file in `~/.podium/sync.yaml`:

```yaml
defaults:
  attestation:
    policy: require
    policy_file: acme-policy.json
```

A relative `policy_file` resolves against the directory of the `sync.yaml` that
names it, here `~/.podium/`. In a workspace `sync.yaml`, the server ignores
`trusted_root_file`, `root_max_age`, and `overlay` and names them on standard
error.

The user's file and the machine file accept only the keys of the `sync.yaml`
schema: `defaults`, `listing_budgets`, `profiles`, and `targets` at the top
level, and the documented keys under `defaults`. An unknown top-level key, or
an unknown key under `defaults`, refuses the start and names the key, so a
block written as a top-level `attestation:` or as `defaults.attestations:` is
not dropped without an error.

### The account home

`podium-mcp` reads `~/.podium/sync.yaml`, including `defaults.registry`, from
the home directory that the account database records for the user of the
process. It does not read `HOME`, `USERPROFILE`, or an `XDG_*` variable, which
a project `.mcp.json` can set. The Linux release binaries read `/etc/passwd`
for the lookup, so a user that only a directory service lists has no account
home for `podium-mcp`. When the account database has no entry for the
user, as in a container that runs under an unlisted user ID, the server reads
no user file, names the failure on standard error, and adds
`config.attestation_policy_unavailable (constraint user_home)` to the
`attestation_warning` member of every load result.

### The machine file

The machine file is `/etc/podium/sync.yaml` on Linux and macOS, and
`podium\sync.yaml` under the `ProgramData` folder on Windows. It holds the same
`defaults.attestation` keys and lets a container image or a managed machine
carry a policy for every user. The server reads it only when no other user
can change it:

- On Linux and macOS, root owns the file, each directory of its resolved path,
  and each symbolic link on the way, and no other user can write any of them.
  A group write bit is accepted only for gid 0. Keep the file and its
  directories free of POSIX ACLs that grant write access, because the check
  does not read ACLs.
- On Windows, `SYSTEM` or `Administrators` owns the `podium` folder and the
  file, their DACLs grant write rights only to `SYSTEM`, `Administrators`, and
  `TrustedInstaller`, and the folder's DACL is protected from inheritance. An
  inherit-only entry that grants a write right to another SID also fails the
  check, although it grants nothing on the file itself; remove such entries.

A machine file that exists and fails the check is ignored, named on standard
error, and reported as `config.attestation_policy_unavailable (constraint
machine_file)` in every load result. An absent file is not reported.

### The workspace overlay

A repository can commit `.podium/overlay/`. The server does not apply an
overlay package whose ID the user's file or the machine file resolves to
`require`: it loads that ID from the registry under the attestation check and
reports `materialize.attestation_untrusted (constraint overlay)` in the
result. `overlay: allow` in `~/.podium/sync.yaml` or the machine file applies
the overlay for every ID, and `overlay: deny` applies it for none. When both
files set the key, `deny` wins. The server skips an overlay package whose ID or
resource path is not well formed, under every policy.

## Trusted roots

A rule of a user-source file verifies the `public-good` instance against the
root that its own `sync.yaml` names in `trusted_root_file`, or against the
root that the server refreshes through the Sigstore TUF repository into
`~/.podium/sigstore/public-good/` under the account home, with the maximum age
that `root_max_age` sets (90 days by default). `PODIUM_ATTESTATION_TRUSTED_ROOT_FILE`,
`PODIUM_SIGSTORE_ROOT_MAX_AGE`, `PODIUM_SIGSTORE_ROOT_REFRESH`, and
`PODIUM_SIGSTORE_ROOT_REFRESH_TIMEOUT` apply only to the rules of
repository-source files, so a repository cannot name a certificate authority
it holds for a rule the user wrote. With no account home, the server keeps
the refreshed root in memory and writes nothing. The copy on disk is used
only when the TUF metadata cached beside it verifies it.

Each bundle fetch has a deadline of 30 seconds, which
`PODIUM_ATTESTATION_FETCH_TIMEOUT` changes.

## Start refusals

`podium-mcp` refuses to start with `config.attestation_policy_unavailable`
when a policy file that a source names cannot be read or is invalid, when
`~/.podium/sync.yaml` does not parse or holds an unknown key, when a
workspace `sync.yaml` in its working directory that the user owns does not
parse, when a `sync.yaml` holds a `defaults.attestation` block with an unknown
key or a value of the wrong type, when the machine file passes
its check and does not parse, and, under a policy above `none`, when
`PODIUM_ATTESTATION_TRUSTED_ROOT_FILE` cannot be loaded or a root setting is
not a valid duration. A workspace file of an ancestor directory, or one that
another user owns, that does not parse is ignored and reported as
`config.attestation_policy_unavailable (constraint workspace_file)` in each
load result, because another local user can place such a file, as in
`/tmp/.podium/`. [Error codes](../reference/error-codes#materialize) lists
the refusals of a load.
