---
title: Finding overlapping artifacts
nav_order: 1
description: Find artifacts whose descriptions or prose duplicate each other, with podium lint before a merge and the catalog overlap report on a running registry, and resolve each overlap.
---

# Finding overlapping artifacts

Two artifacts overlap when their descriptions claim the same situations or their prose repeats the same text. This page covers why overlap matters, the checks that find it, how to run them on a repository and on a running registry, how to resolve what they report, and how an operator turns on the semantic check.

## Who can do this

| Task | Access |
|:--|:--|
| Check a layer's source with `podium lint` | Any user with a checkout of the source. |
| Read the catalog overlap report with `podium overlap`, `GET /v1/overlap`, or the web UI's Similarity page | Any caller that can search the registry. The report names only artifacts the caller can see. |
| Resolve an overlap by editing, deprecating, or suppressing | An author with write access to the layer's source repository. |
| Recompute the semantic overlap records with `podium admin rebuild-overlap` | Per-tenant `admin` role. |
| Turn on the semantic check and set its threshold and sweep interval | Operator, through environment variables on every registry replica. |

---

## Why overlap matters

- A harness picks an artifact by its description. When two descriptions claim the same situations, an agent that sees both cannot tell which one to load.
- `search_artifacts` and `load_domain` return both artifacts for the same queries, so each takes a result position that a distinct artifact could fill.
- Some harnesses place the name and description of every installed skill in a listing with a size limit. Each duplicate entry spends part of that limit, and above it a harness can shorten descriptions or omit entries. [Keeping the catalog lean](quality#harness-listing-budget) covers the listing budget.

## Kinds of overlap

| Kind | Example | Check | Where it runs |
|:--|:--|:--|:--|
| Copied or templated descriptions | "Use when the user asks to create a pdf" and "Use when the user asks to merge a pdf" | `lint.colliding_descriptions`: the normalized word sets of two descriptions have a similarity of 0.7 or higher. | `podium lint`, registry ingest, and the overlap report |
| Duplicated prose | Two near-identical skill bodies, or one `references/style-guide.md` bundled in several artifacts | `lint.near_duplicate_content`: two prose units of different artifacts have five-word shingle sets with a similarity of 0.8 or higher. | `podium lint` and registry ingest |
| The same meaning in different words | "Pay a vendor invoice from accounts payable" and "Settle a supplier bill" | `lint.semantic_overlap`: the embedding vectors of two artifacts have a cosine similarity at or above the threshold, whose default depends on the embedding model. | The registry alone, when an operator turns the check on |

The word-based checks compare words, so they find copies and descriptions built from one template. They miss two descriptions that share few words and mean the same thing, which is what `lint.semantic_overlap` finds. The overlap report also carries `lint.thin_description`, which flags a description too short to tell a harness when to use the artifact.

Every overlap diagnostic is a warning. It never blocks an ingest. [Skill lint checks](../authoring/frontmatter-reference#skill-lint-checks) defines the word-based checks and their thresholds, and [Near-duplicate content](../authoring/bundled-resources#near-duplicate-content) defines the prose units that `lint.near_duplicate_content` compares.

---

## Check a repository before merge

`podium lint` runs the word-based checks over every artifact of every layer under the root it is given, so a CI job on a layer's source repository reports an overlap before it reaches the registry. Overlap diagnostics are warnings, and warnings exit 0 under the default `--fail-on error`. Pass `--fail-on warning` to fail the job on any warning once the catalog lints clean. [Catalog lint in CI](quality#catalog-lint-in-ci) gives a CI step and the JSON output.

`podium lint` reads no search index, so it does not run `lint.semantic_overlap`. A pull request that adds a description with the same meaning as an existing one in other words passes lint, and the registry reports the pair after ingest when the semantic check is on.

## Check the live catalog

The catalog overlap report runs across every layer the caller can see on a running registry. Read it with `podium overlap --registry <url>`, `GET /v1/overlap`, or the web UI's Similarity page.

The report covers the current served version of every artifact the caller can see. It contains `lint.thin_description` and `lint.colliding_descriptions`, and `lint.semantic_overlap` when the operator turned the semantic check on. It omits `lint.near_duplicate_content`, which needs the prose of each artifact and runs only in `podium lint` and at ingest. The endpoint reads stored records and descriptions and runs no index query, so a request costs no embedding call.

### Reading the lines

Without `--json`, `podium overlap` prints one line per diagnostic in the `podium lint` format, `[<severity>] <artifact-id>: <message> (<rule-code>)`:

```text
[warning] finance/ap/pay-invoice: description overlaps finance/ap/submit-invoice (80%); give each artifact a distinct summary so search and load_domain can tell them apart (lint.colliding_descriptions)
[warning] finance/ap/pay-invoice: name, description, when_to_use, and tags are semantically similar to finance/payments/settle-bill (91%); make them distinct so search and load_domain can tell the artifacts apart (lint.semantic_overlap)
```

Each message names the overlapping artifacts in order of descending similarity, with each similarity as a percentage, and abbreviates a list of more than 10 to the first 10 and a count of the rest. A pair appears under both of its artifacts unless one of them suppresses the code. A report with no diagnostic prints `overlap: no issues.`.

### Reading the structured report

`podium overlap --json` prints the response of `GET /v1/overlap`. Its members carry what the lines abbreviate:

- `semantic_overlap.state` is `"on"` or `"off"`, and `semantic_overlap.threshold` and `semantic_overlap.model` give the threshold the read applied and the configured embedding model. The report includes only records computed with that model.
- `semantic_overlap.computed` counts the visible artifacts with a current computation, and `semantic_overlap.total` counts the visible artifacts.
- Each diagnostic carries the artifact's `description`, `type`, and `layer`, so a client can show the artifact without loading it.
- Each `lint.colliding_descriptions` and `lint.semantic_overlap` diagnostic carries `peers`, the full list of overlapping artifacts as `{artifact_id, description, type, layer, similarity}` objects with `similarity` from 0 to 1. The list is never abbreviated.
- A `lint.semantic_overlap` peer also carries `computed_at`, the time the registry computed the pair, because the registry stores semantic scores and recomputes them on its own schedule.
- `counts` counts the diagnostics by severity.

A `lint.colliding_descriptions` similarity is the word-set similarity of two normalized descriptions. A `lint.semantic_overlap` similarity is the cosine similarity of two embedding vectors, limited to the range 0 to 1. The two scales differ, so compare a score only with other scores of the same code.

### Incomplete coverage

A `computed` below `total` means some artifacts are not computed yet, or their last computation failed, so the report can miss their overlap. `podium overlap` then prints a warning on standard error that names both numbers, and `--fail-on warning` exits 1 on it. The gap closes when the startup pass, the sweep, or `podium admin rebuild-overlap` computes those artifacts, and the registry log names each failed computation.

### Narrowing the report

- The `artifact` query parameter of `GET /v1/overlap` keeps only the diagnostics of one artifact, and `counts` counts those. The CLI has no equivalent flag.
- `--threshold <n>`, or the `threshold` query parameter, replaces the operator's semantic threshold for one read, as a number from 0.5 to 1.0. A higher value reports fewer semantic pairs, and a lower value reports more. It does not change the `lint.colliding_descriptions` threshold.
- `--fail-on warning` makes `podium overlap` exit 1 when the report contains any diagnostic or `computed` is below `total`, so a scheduled job can alert on new overlap.

### Reading the report in the web UI

A registry started with `--web-ui` shows the report on the Similarity page, which the sidebar lists for every caller who can search, an anonymous caller included. The sidebar row states how many artifacts have overlap at the registry's threshold.

- The Similarity page lists each artifact with overlap once, with its `lint.semantic_overlap` and `lint.colliding_descriptions` diagnostics combined, and the strongest score of each kind beside it. Selecting an artifact shows its description, the registry's messages verbatim, and a table of the artifacts it overlaps. The page does not list `lint.thin_description`.
- The artifact viewer carries a Similarity tab that reads the report for the artifact on screen. The tab's count is the number of artifacts it overlaps. Selecting a row in its table sets the two descriptions side by side. The tab also states whether the description is long enough, from `lint.thin_description`.
- The rail beside every tab of the artifact viewer carries a Similarity section that summarizes the same read and links to the Similarity page.
- The threshold control ranges from 0.5 to 1.0 and starts at the threshold the registry applied. Moving it re-reads the report with the `threshold` parameter, which changes only the semantic peers, so word-overlap entries stay listed at any setting.
- Similarity is drawn as an orange bar with a decimal, and word overlap as a grey bar with a percentage. The two scores sit in separate columns and are never sorted against each other. A missing score is an em dash.
- When the semantic check is off, the page and the tab show no threshold control and no Similarity column.

The web UI reads the report with the caller's identity, so it shows only what [What the caller sees](#what-the-caller-sees) describes. An artifact the caller cannot see answers the same empty report as an artifact with no overlap, and the Similarity tab renders the two the same way. The web UI has no control for `podium admin rebuild-overlap`.

### What the caller sees

The report names only artifacts the caller can see. A semantic record with an artifact the caller cannot see is not reported and is not counted in an abbreviated peer list, so the report names only artifacts the caller can load. Two callers with different views therefore read different reports. Run the report as an identity whose view covers the layers under review.

[`podium overlap`](../reference/cli#podium-overlap) gives the command's exit codes, and [Catalog overlap report](../reference/http-api#catalog-overlap-report) gives every response field and error.

---

## Resolve an overlap

Choose one of the following for each reported pair.

| Resolution | When it fits | How |
|:--|:--|:--|
| Merge the artifacts | Both artifacts do the same job. | Move the content into one artifact. Deprecate the other with `replaced_by` naming the survivor, as [Deprecate an artifact](lifecycle#deprecate-an-artifact) describes. Run [`podium impact`](lifecycle#check-dependents-before-a-change) first to find the artifacts that reference the one being retired. |
| Sharpen the descriptions | The artifacts do different jobs, and the descriptions do not say how. | Rewrite one or both descriptions to state what the artifact does and the situation that calls for it rather than the other. [Step 1 of Your first skill](../authoring/your-first-skill#step-1-sharpen-the-description) shows the pattern. |
| Deprecate one | One artifact supersedes the other. | Publish a new version of the older artifact with `deprecated: true` and `replaced_by`, as [Deprecate an artifact](lifecycle#deprecate-an-artifact) describes. |
| Accept the overlap | The overlap is deliberate, such as a shared reference file kept in step across artifacts. | List the code under `lint_suppress` in the artifact's `ARTIFACT.md`. |

`lint_suppress` silences `lint.colliding_descriptions`, `lint.near_duplicate_content`, `lint.semantic_overlap`, and `lint.thin_description` for the artifact that lists them. Suppression is one-sided: the other artifact of the pair still draws a warning that names this one, so list the code in both artifacts to clear the pair. A skill with no `ARTIFACT.md` cannot suppress a code, so add an overlay:

```markdown
---
type: skill
lint_suppress: [lint.colliding_descriptions, lint.semantic_overlap]
---
<!-- Skill body lives in SKILL.md. -->
```

A catalog whose descriptions follow one template on purpose can raise `PODIUM_LINT_DESCRIPTION_SIMILARITY` instead. Set the variable in the CI environment and on the registry, because `podium lint` and registry ingest read it separately. [Lint](../reference/configuration#lint) lists the variables.

---

## Turn on the semantic check

This section is for the operator who turns the check on and for tenant admins who recompute its records.

`lint.semantic_overlap` compares the embedding vectors that search already computes for each artifact. A vector embeds the artifact's name, description, `when_to_use`, and tags, so two artifacts with identical descriptions and different names score below 1. Two artifacts whose vectors have a cosine similarity at or above the threshold are peers, and the warning names each peer with its similarity as a percentage.

### Requirements

- The check runs in the registry alone and is off by default. Turn it on with `PODIUM_LINT_SEMANTIC_OVERLAP=on` on every registry replica.
- The registry needs a search index and an embedding source: an embedding provider, or a vector backend that embeds text with its own hosted model. A registry with the check on, `PODIUM_EMBEDDING_PROVIDER` empty or `none`, and a backend that does not embed text itself refuses to start with `config.invalid`.
- A managed index (Pinecone, Weaviate Cloud, or Qdrant Cloud) must use the cosine metric. The registry takes one minus the reported distance as the similarity, which is the cosine similarity only under that metric. [Vector backends](../deployment/services/vector-backends#semantic-overlap) describes each backend.
- sqlite-vec, and any index that orders candidates by Euclidean distance, can miss a close cosine neighbour when the embedding model does not normalize its vectors. The registry computes the similarity from the stored vectors, but the index chooses the candidates.

The check uses the backend and the embedding model that search uses, so it adds no second index and no second embedding of the catalog. The registry queries with the artifact's stored vector on every backend that can return it, a self-embedding backend included, so scores are symmetric and identical texts score 1. A self-embedding backend that cannot return a stored vector is queried with the artifact's projection text, and its scores are asymmetric. On any other backend that cannot return a stored vector, the computation fails with an error, keeps the artifact's existing records, and leaves the artifact to the next sweep.

### When the registry computes

The registry stores the similarity of each artifact to its nearest neighbours as overlap records and applies the threshold when it reads them. A computation of artifact A finds the `PODIUM_LINT_SEMANTIC_NEIGHBORS` other artifacts whose current served versions lie nearest to A in the index. The index keeps a row for every ingested version, so the computation widens its query past rows of earlier versions, up to 50 times one more than `PODIUM_LINT_SEMANTIC_NEIGHBORS` rows and no more than the backend returns in one query. A failed widened query keeps the peers already found. A computation replaces only the records A owns, and a pair found from both sides is reported once, with the similarity of the more recently computed record. The registry also records the version and model of each artifact's last successful computation. A computation stores nothing when its artifact gains a newer served version first, and two concurrent computations of one artifact store one result. The registry computes an artifact at these points:

- **At ingest.** On a collocated backend (pgvector or sqlite-vec), an ingest computes each artifact it stores as a new version within the cycle and reports the warning among its non-blocking advisories. On a managed backend, the registry computes when the vector reaches the index, after the ingest answers, once per outbox pass for the vectors that pass wrote, so the ingest reports no `lint.semantic_overlap` diagnostic. That computation uses the vector the outbox row carries when it has one, and retries a stored vector the backend does not return yet on the next five outbox passes. The startup ingest computes no overlap, so startup does not wait on it.
- **Through a startup pass.** Once the startup ingest completes, the registry runs one sweep pass in the background, whether or not the periodic sweep is on. After a restart the pass computes only artifacts whose computation is missing or stale. The startup pass does not run when the check is off or no search index is configured.
- **Through a periodic sweep.** Every `PODIUM_LINT_SEMANTIC_REFRESH_INTERVAL`, 24 hours by default, the sweep computes each served artifact whose last computation is missing or was for another version or embedding model, and each artifact that owns a record naming a version that is no longer served. An artifact whose computation found no peer is current and is not recomputed. The sweep deletes the records and computation entries of artifacts that are no longer served. Each replica delays its periodic passes by a random offset from zero up to a tenth of the interval, drawn once at startup, so replicas started together do not compute at the same time. The sweep does not run when the check is off or no search index is configured. The startup pass and the sweep skip an artifact whose vector the outbox has not written, because the outbox write computes it.
- **After a full re-embed.** A tenant-wide `podium admin reembed` pass recomputes every artifact it re-embeds. A pass scoped with `--only-missing` or `--since` recomputes no records.
- **On demand.** `podium admin rebuild-overlap --registry <url>` recomputes every served artifact of the caller's tenant, including one whose vector is not written yet. It needs the per-tenant `admin` role, is refused in read-only mode, and answers `registry.unavailable` with `retryable: false` when the check is off or no search index is configured, because the request cannot succeed until the configuration changes. It prints `computed` and `failed` counts and exits 1 when `failed` is not zero.

An ingest diagnostic names a peer only when the peer is in the ingested layer or in a layer whose visibility includes `public` or `organization`, so a reingest response never names an artifact of a layer restricted to groups or users. A reingest that a source webhook triggers names only peers in the ingested layer, because the git host records the response. The overlap report shows the stored result across every layer the caller can see.

### Tune the threshold

Similarity values depend on the embedding model, so the default threshold follows the configured model: 0.80 for `text-embedding-3-small`, 0.86 for `nomic-embed-text`, 0.90 for `multilingual-e5-large`, and 0.85 for any other model. The model name is matched in lowercase, without a provider prefix up to the last `/` and without an Ollama tag from the first `:`, so `nomic-embed-text:latest` takes 0.86. The values come from an evaluation over 413 published skills, in which the pairs reported at 0.80 on `text-embedding-3-small` had a precision of about 0.97. To tune the threshold for a catalog:

1. Run `podium overlap --registry <url> --threshold <n>` at a few values, such as 0.80, 0.85, and 0.90. The threshold applies when records are read, so a new value needs no recomputation.
2. Read the pairs each value reports, and choose the value whose pairs are artifacts that do the same job.
3. Set `PODIUM_LINT_SEMANTIC_SIMILARITY` to that value on every replica. The registry reads it at startup.

`PODIUM_LINT_SEMANTIC_NEIGHBORS` sets how many nearest peer artifacts one computation finds and stores records for, 10 by default. A read with a lower threshold shows only peers among those stored, so raise the count before reading at low thresholds. [Lint](../reference/configuration#lint) in the configuration reference lists every variable and its range, and [Semantic overlap check](../deployment/operations/upgrade-notes#semantic-overlap-check) in the upgrade notes gives the steps for an existing catalog.
