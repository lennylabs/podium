# 0. Quickstart

A minimal artifact:

```
finance/close-reporting/run-variance-analysis/
└── ARTIFACT.md
```

```markdown
---
type: skill
name: run-variance-analysis
version: 1.0.0
description: Flag unusual variance vs. forecast after month-end close.
when_to_use:
  - "After month-end close, when reviewing financial performance"
tags: [finance, close, variance]
sensitivity: low
---

Compare actuals vs. forecast for the most recent close period. For each line
item, flag variances above the threshold defined in your team's policy doc.
Output a markdown table sorted by absolute variance.
```

Ingest:

```bash
$ git add ARTIFACT.md && git commit -m "Add run-variance-analysis@1.0.0"
$ git push    # opens or updates a PR; CI runs `podium lint`; reviewers approve; merge.
# The Git provider's webhook fires; the registry ingests automatically.
# If the webhook was missed, an admin (or the layer owner) can reingest manually:
$ podium layer reingest org-defaults
artifact: finance/close-reporting/run-variance-analysis@1.0.0   layer: org-defaults
```

In an agent session, the host has the Podium MCP server configured. The agent calls:

```
load_domain("finance/close-reporting")
→ {domains: [...], artifacts: [{id: "finance/close-reporting/run-variance-analysis", ...}]}

load_artifact("finance/close-reporting/run-variance-analysis")
→ {manifest: <prose body>, materialized_at: "/workspace/.podium/runtime/.../ARTIFACT.md"}
```

The agent now has the skill in its working set. Done.
