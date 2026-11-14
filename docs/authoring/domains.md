---
title: Domains
nav_order: 6
description: How folders and subfolders define the catalog hierarchy, and how DOMAIN.md adds descriptions, keywords, featured artifacts, imports, and discovery settings.
---

# Domains

This page explains how directories organize a catalog into domains, and how a `DOMAIN.md` file adds metadata to a domain. It is for authors who organize more than a handful of artifacts.

A **domain** is a directory in the registry. It groups related artifacts and gives them shared context. For example, `finance` is a top-level domain, `finance/ap` is a subdomain, and `finance/ap/pay-invoice` is the canonical path of an artifact under it.

Every subdirectory of the registry path is a domain by default, with no configuration. An optional `DOMAIN.md` file at the root of the directory adds metadata:

- A description and a long-form prose body that an agent reads when it navigates to the domain.
- Keywords and featured artifacts for discovery.
- `include:` patterns that pull in artifacts from elsewhere, and `exclude:` patterns that remove paths.
- Discovery rendering settings for the subtree.

---

## Domains without DOMAIN.md

For a small or in-progress catalog, nest folders and add nothing else. The directory tree is the domain hierarchy, and each artifact's canonical ID is the directory path under the registry root.

```
~/podium-artifacts/
└── personal/
    ├── hello/
    │   └── greet/                 # type: skill
    │       ├── SKILL.md
    │       └── ARTIFACT.md
    └── deploy/
        └── check-config/          # type: command
            └── ARTIFACT.md
```

In this tree, `load_domain("personal")` returns the two subdomains, and `load_domain("personal/hello")` returns the `greet` artifact.

A domain without a `DOMAIN.md` has no projection to embed, so it never appears in `search_domains` results. It stays reachable through `load_domain` enumeration.

---

## When to add a DOMAIN.md

Add a `DOMAIN.md` when the domain needs any of the following:

- A description in `load_domain` output.
- Keywords that surface in `search_domains`.
- Artifacts from another domain that appear in this domain.
- Paths excluded from this domain.
- An unlisted folder, hidden from enumeration and still reachable by ID.
- Discovery rendering overrides for the subtree.

`DOMAIN.md` lives at the root of a domain directory:

```
~/podium-artifacts/
└── team-finance/
    └── finance/
        └── ap/
            ├── DOMAIN.md          ← the metadata file
            ├── pay-invoice/       # type: skill
            │   ├── SKILL.md
            │   └── ARTIFACT.md
            └── reconcile-invoice/ # type: skill
                ├── SKILL.md
                └── ARTIFACT.md
```

---

## DOMAIN.md schema

```markdown
---
unlisted: false
description: "AP-related operations"

discovery:
  max_depth: 4
  fold_below_artifacts: 5
  featured:
    - finance/ap/pay-invoice
  deprioritize:
    - finance/ap/_archive/**
  keywords:
    - invoice
    - remittance
    - reconciliation
    - 1099
    - vendor master

include:
  - finance/ap/pay-invoice
  - finance/ap/payments/*
  - finance/refunds/**
  - _shared/payment-helpers/*
  - _shared/regex/{ssn,iban,routing-number}

exclude:
  - finance/ap/internal/**
---

# Accounts Payable

Operations and artifacts for the AP function: invoice processing,
vendor remittance, payment reconciliation, and 1099 reporting. This
domain applies to tasks involving money flowing out of the company to
vendors.

For inbound payments and AR, see `finance/ar/`.
```

| Field | Description |
|:--|:--|
| `unlisted` | When `true`, removes the folder and its subtree from `load_domain` enumeration. See [Unlisted folders](#unlisted-folders). Default `false`. |
| `description` | One-line summary, used wherever this domain appears as a child or sibling in another `load_domain` response. |
| `discovery.*` | Per-domain overrides of the discovery rendering rules. See [Discovery rendering](#discovery-rendering). |
| `include` | List of glob patterns and artifact IDs to import into this domain. Imported artifacts keep their canonical home and appear here as well. See [Imports](#imports). |
| `exclude` | List of paths to remove, applied after `include`. |

The prose body below the frontmatter is long-form context. `load_domain` returns it only when this domain is the requested path.

---

## Descriptions

A domain has two slots for prose:

| Slot | Length | Returned when |
|:--|:--|:--|
| Frontmatter `description` | One line | This domain appears as a child or sibling in another `load_domain` response. |
| Prose body | Long form | This domain is the requested path. |

For the requested domain, `load_domain` resolves the description in this order:

1. The prose body, if present.
2. The frontmatter `description`.
3. A synthesized fallback: the directory basename, title-cased and de-slugged.

For example, a `load_domain("finance", depth=2)` call returns the body of `finance`, because `finance` is the requested path. It also returns a two-level subtree of subdomains, each with its short `description` only. To read another domain's body, the agent calls `load_domain` on that domain directly.

Keep the body at or below 2000 tokens. Lint warns above that length.

---

## Keywords

`discovery.keywords` is an author-curated list of terms that agents should associate with the domain, such as synonyms, jargon, and distinguishing terminology. `load_domain` returns them verbatim for the domain. `search_domains` indexes them alongside the description and the truncated body.

Keywords are the cheapest way to make a domain findable. For example, if the AP domain is the right place for "1099" questions but the description does not mention 1099, add `1099` to `keywords`.

---

## Featured artifacts

`discovery.featured` is an author-curated list of canonical artifact IDs that surface first in the domain's notable list:

```yaml
featured:
  - finance/ap/pay-invoice
  - finance/ap/reconcile-invoice
```

Featured entries appear before any signal-driven entries in the notable list. If the `featured:` list alone exceeds the configured `notable_count` cap, the registry truncates it in author-supplied order and adds no signal entries.

`discovery.deprioritize` works in the opposite direction. It is a list of glob patterns. Children that match rank last and are excluded from the notable list unless space permits.

---

## Imports

`include:` brings artifacts from other domains into this one for discovery purposes. The glob syntax is `*` for one segment, `**` for a recursive match, and `{a,b,c}` for alternatives.

```yaml
include:
  - finance/ap/pay-invoice                    # exact match
  - finance/ap/payments/*                     # one-level wildcard
  - finance/refunds/**                        # recursive
  - _shared/payment-helpers/*                 # cross-domain
  - _shared/regex/{ssn,iban,routing-number}   # alternation
```

`exclude:` removes paths after the registry computes the include set.

Imports have these properties:

- **Canonical paths stay unchanged.** An artifact has exactly one canonical home: the directory where its `ARTIFACT.md` lives, plus `SKILL.md` for a skill. Imports add appearances under other domains. `search_artifacts` returns the artifact once, with its canonical path and, optionally, the list of domains that import it.
- **Imports are dynamic.** When an artifact is added at a path a glob matches, the domain picks it up automatically, with no re-ingest of `DOMAIN.md`.
- **Authoring rights.** Editing a domain's `include:` requires write access to the layer that contains the destination `DOMAIN.md`. Importing requires no rights in the source path. The artifact only has to resolve under some layer the registry has ingested.
- **Visibility.** The registry enforces visibility per layer at read time. A caller who cannot see the source layer does not see the imported artifact under this domain either.

![DOMAIN.md include: a DOMAIN.md declares `include:` glob patterns; at request time, artifacts from elsewhere appear under the including domain while keeping their canonical IDs.](../assets/diagrams/domain-imports.svg)

<!--
ASCII fallback for the diagram above (DOMAIN.md include:):

  finance/DOMAIN.md                            source layer: _shared/
    description: Finance team artifacts.         _shared/payment-helpers/routing-validator/
    discovery:                                   _shared/payment-helpers/swift-bic-parser/
      keywords: [ap, ar, treasury]             (canonical IDs stay rooted in _shared/)
    include:
      - _shared/payment-helpers/**
      - treasury/risk-models/**

                              |
                              v  (resolved at request time)

  effective view of finance/ for the caller:
    finance/ap/pay-invoice                                native
    finance/close-reporting/run-variance-analysis         native
    _shared/payment-helpers/routing-validator             imported, appears under finance/
    _shared/payment-helpers/swift-bic-parser              imported, appears under finance/

  Imported artifacts can show under multiple domains; their
  canonical ID is what they keep. Glob patterns expand against
  the visible layer set; included artifacts the caller cannot see are
  silently dropped.
-->

---

## Unlisted folders

Setting `unlisted: true` removes a folder and its entire subtree from `load_domain` enumeration. The setting propagates to the whole subtree. Artifacts inside an unlisted folder keep these behaviors:

- `load_artifact(<id>)` reaches them if the host has visibility.
- They appear in `search_artifacts` results normally, subject to the per-artifact `search_visibility:` field.
- Other domains can import them with `include:`.

A typical use is a `_shared/` folder of reusable helpers. Mark it unlisted so it stays out of `load_domain` output, then import its contents into the domains that need them:

```markdown
---
unlisted: true
---
```

---

## Discovery rendering

Configurable rules govern how `load_domain` renders its output. Tenant defaults live in `registry.yaml`, and per-domain overrides live in the `discovery:` block of `DOMAIN.md`.

| Setting | Type | Effect |
|:--|:--|:--|
| `max_depth` | int (≥1) | Caps the depth of the rendered subtree below the requested path. Default 3. |
| `fold_below_artifacts` | int (≥0) | A subdomain whose visible artifact count (recursive) is below this threshold collapses into its parent's leaf set. Default 0 (no folding). |
| `fold_passthrough_chains` | bool | Collapses single-child intermediate domains. Default `true`. |
| `notable_count` | int (≥0) | Caps the notable list per domain. Default 10. |
| `target_response_tokens` | int | Soft budget per `load_domain` response. The renderer tightens depth and notable count to fit. Default 4000. |
| `featured` | list of canonical artifact IDs | Surfaced first in the notable list. |
| `deprioritize` | list of glob patterns | Matching children rank last and are excluded from the notable list unless space permits. |
| `keywords` | list of strings | Author-curated terms. Per-domain only, with no tenant default. |

---

## Checking a domain

These commands call registry HTTP endpoints. Each requires a running registry server, passed with `--registry <url>` or the `PODIUM_REGISTRY` environment variable.

A [Local](../deployment/local) deployment reads the catalog from disk and runs no server. These commands therefore need a [Single node](../deployment/single-node) or [Clustered](../deployment/clustered) deployment. `podium serve --standalone --layer-path <dir>` starts one against the same catalog directory.

| Command | Use |
|:--|:--|
| `podium domain show <path>` | Prints the `load_domain` output for a path. Use it to confirm that a `DOMAIN.md` ingested correctly. |
| `podium domain search <query>` | Runs `search_domains` from the CLI. Use it to confirm that `keywords` and `description` make the domain findable for the intended queries. |
| `podium domain analyze [<path>]` | Reports sparsity per node, pass-through chains, and candidates for split or fold. The command is operator-facing, useful at operational scale, and outside the day-to-day authoring loop. |

---

## DOMAIN.md across layers

When several [layers](../deployment/layers) contribute a `DOMAIN.md` for the same path, the registry merges them field by field:

| Field | Merge |
|:--|:--|
| `description` and prose body | Last-layer-wins. |
| `include` | Additive across layers. |
| `exclude` | Additive across layers; applied after the merged include set. |
| `unlisted` | Most-restrictive-wins. |
| `discovery.max_depth`, `discovery.notable_count`, `discovery.target_response_tokens` | Most-restrictive-wins (lowest value). |
| `discovery.fold_below_artifacts` | Most-restrictive-wins (highest value). |
| `discovery.fold_passthrough_chains` | Most-restrictive-wins (`true` over `false`). |
| `discovery.featured`, `discovery.deprioritize`, `discovery.keywords` | Append-unique. |

When a workspace-local-overlay `DOMAIN.md` is involved, the consumers that expose `load_domain` apply the merge client-side. The MCP server and the language SDKs do this after the registry returns its result for the registry-side layers. `podium sync` materializes by the `sync.yaml` scope rather than by domain enumeration, so it does not apply this merge.
