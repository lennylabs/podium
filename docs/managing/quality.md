---
title: Keeping the catalog lean
nav_order: 4
description: Run catalog lint in CI, act on the checks that matter most to curators, and keep each workspace inside its harness's skill listing budget.
---

# Keeping the catalog lean

A lean catalog has descriptions that a harness can tell apart, bodies and packages within the sizes lint accepts, and a skill listing that fits each harness's budget. This page covers running catalog lint in CI, the checks that matter most to curators, and the listing budget that `podium sync` checks.

## Who can do this

| Task | Access |
|:--|:--|
| Run `podium lint` locally or in CI | Any user with a checkout of the layer's source. |
| Change a lint threshold | Whoever sets the `PODIUM_LINT_*` variables: the CI configuration for `podium lint`, and the operator for registry ingest. |
| Set a listing budget | Whoever edits a `sync.yaml` scope that applies to the workspace: the user-global file, the committed `<workspace>/.podium/sync.yaml`, or the gitignored `<workspace>/.podium/sync.local.yaml`. |

---

## Catalog lint in CI

Run `podium lint` as a required check on pull requests to each layer's source repository. It walks every artifact of every layer under the root it is given and runs the rules that registry ingest runs, the `DOMAIN.md` checks, and the catalog advisories.

```yaml
# A CI step on the layer's source repository
- name: Lint the catalog
  run: podium lint --registry . --fail-on warning
```

- `--fail-on` sets the lowest severity that fails the run, `error` by default or `warning`. An `info` diagnostic, such as `lint.skill_version_assigned`, fails the run under neither value.
- Start with the default, which fails only on errors, and move to `--fail-on warning` once the catalog lints clean. An existing catalog that adopts the catalog advisories usually prints warnings at first.
- `--json` writes one object, `{registry, diagnostics, rejected, counts}`, on standard output. Each diagnostic carries `artifact_id`, `code`, `severity`, and `message`, so a CI step can count or filter by code. An artifact whose manifest does not parse, or whose `ARTIFACT.md` declares `extends:` or a removed type, appears in `rejected` and makes the run exit 1.
- The registry and `podium lint` read the same size and word-similarity `PODIUM_LINT_*` variables, so set any change to those thresholds in both places to keep CI and ingest in agreement. The `PODIUM_LINT_SEMANTIC_*` variables apply to the registry alone, because `podium lint` does not run the semantic check.

To see which checks a catalog draws most often, count the codes in the JSON output:

```text
jq -r '.diagnostics[].code' lint.json | sort | uniq -c | sort -rn
```

[`podium lint`](../reference/cli#podium-lint) gives the output format and exit codes, and [Lint](../reference/configuration#lint) lists the threshold variables.

## Checks that matter to curators

| Check | Severity | What it reports | What to do |
|:--|:--|:--|:--|
| `lint.thin_description` | Warning | A description with fewer than 15 code points or fewer than 3 words. | State what the artifact does and when to use it. |
| `lint.colliding_descriptions` | Warning | Two descriptions whose normalized word sets have a similarity of 0.7 or higher. | Resolve the pair as [Finding overlapping artifacts](overlap#resolve-an-overlap) describes. |
| `lint.near_duplicate_content` | Warning | Two prose bodies or prose resources of different artifacts that are near copies. | Move the shared text into one artifact and reference it by ID, or keep the copies in step and suppress the code. |
| `lint.skill_ref_validate` | Warning | A `SKILL.md` that fails `skills-ref validate`, including a description longer than 1024 characters. | Bring the file within the Agent Skills standard. |
| `lint.manifest_size` | Warning above 5,000 estimated tokens or 500 lines of `SKILL.md` body, and an error above 20,000 estimated tokens of manifest content | A `SKILL.md` body longer than the Agent Skills specification recommends, or manifest content above the cap. The error fails ingest. | Move reference material into `references/`, a separate skill, or a `DOMAIN.md` body. |
| `lint.bundled_resource_size` | Warning above 1 MB for one file, and an error above 10 MB for the package | A bundled file or a package above its size cap. The error fails ingest. | Move large files to external resources. |
| `lint.resource_path_reserved` | Error | A bundled file under `.git` or under a name that a filesystem resolves to `.git`, such as `.GIT` or the NTFS short name `GIT~1`. The error fails ingest, and `lint_suppress` does not silence it. | Remove the directory from the package, or rename it. |
| `lint.skill_metadata_nonstring` | Warning | A sequence or a mapping under a `SKILL.md` `metadata` key that Podium does not read. Podium keeps the bytes and does not read them. | No change is needed for a key another tool reads. `lint_suppress` does not silence the warning. |
| `lint.skill_metadata_field_ignored` | Warning | A `SKILL.md` `metadata` value for a key Podium reads, such as `tags` or `deprecated`, that does not follow its encoding. Podium ignores the value. | Rewrite the value in its encoding. `lint_suppress` does not silence the warning. |

The thresholds in the table are defaults, and each has a `PODIUM_LINT_*` variable. [Skill lint checks](../authoring/frontmatter-reference#skill-lint-checks) lists every skill check, [Size thresholds](../authoring/bundled-resources#size-thresholds) and [Manifest size lint](../authoring/bundled-resources#manifest-size-lint) give the size limits, and [Podium fields in SKILL.md metadata](../authoring/frontmatter-reference#podium-fields-in-skillmd-metadata) gives each encoding.

---

## Harness listing budget

Some harnesses place the name and description of each installed skill in a listing in the model's context and cap the listing's size. Above the cap, a harness can shorten descriptions or omit entries without reporting an error. `podium sync` estimates the listing each `kind: workspace` target contributes and compares the estimate with the budget that `listing_budgets` in `sync.yaml` sets for the target's adapter.

### What the estimate means

Each artifact the run materializes whose type is in `types`, `[skill]` by default, is one entry. An entry costs the code points of its name, the code points of its description, and `entry_overhead_chars`. A skill whose `invocation` is `user` is not an entry on any adapter. The listing can also hold skills from outside the target, such as user-level skill directories and plugins, so the estimate is a lower bound.

The check never changes what `podium sync` writes. By default it writes a warning on standard error for each excess and each oversized entry:

```text
warning: listing: claude-code listing estimate is 9120 characters against a configured budget of 8000 (64 entries)
warning: listing: finance/close/reconcile description is 1610 characters, above max_entry_chars 1536
```

### Defaults

| Key | Default |
|:--|:--|
| `max_chars` | `8000` code points, which matches the Claude Code listing for a model with a 200,000-token context window. |
| `entry_overhead_chars` | `5` |
| `max_entry_chars` | `1536` for `claude-code`, the length at which Claude Code shortens an entry, and unset for every other adapter. |
| `types` | `[skill]` |
| `check` | `warning`. `off` computes no estimate, and `error` fails the run after it writes the target. |

A workspace whose Claude Code model has a 1,000,000-token context window sets a larger budget:

```yaml
# sync.yaml
listing_budgets:
  claude-code:
    max_chars: 40000
```

### Bringing a workspace under budget

- Narrow what the workspace receives with include and exclude globs, type filters, and profiles, as [Selective materialization](../consuming/selective-materialization) describes.
- Mark a skill that a person starts by hand with `invocation: user`, which removes it from the estimate.
- Shorten long descriptions. Claude Code shortens a description above 1536 code points in its listing, so put the text that says when to use the skill first.
- Remove overlapping skills, as [Finding overlapping artifacts](overlap) describes.

[Listing budget](../consuming/configure-your-harness#listing-budget) gives the full rules for the estimate, the keys, and choosing a budget per harness, and [Listing budget](../reference/cli#listing-budget) in the CLI reference gives the `--json` member and the exit codes.

---

## Writing descriptions

The description is the text a harness reads to decide whether an artifact matches a prompt. These pages cover how to write one:

- [Step 1 of Your first skill](../authoring/your-first-skill#step-1-sharpen-the-description) rewrites a vague description into one that names the situations that call for the skill.
- [Universal fields](../authoring/frontmatter-reference#universal-fields) defines `description` and `when_to_use`, and [`when_to_use` and search](../authoring/frontmatter-reference#when_to_use-and-search) states which fields lexical search reads.
- [Authoring affects discovery quality](../consuming/browsing-the-catalog#authoring-affects-discovery-quality) describes how descriptions, `when_to_use`, and domain `keywords` change what an agent finds.
- [Domains](../authoring/domains) covers the `DOMAIN.md` description and `keywords` that `search_domains` reads.
