# 0. Quickstart

A minimal skill artifact has both a `SKILL.md` (per the [agentskills.io specification](https://agentskills.io/specification)) and an `ARTIFACT.md` (per Podium's schema):

```
finance/close-reporting/run-variance-analysis/
├── SKILL.md
└── ARTIFACT.md
```

`SKILL.md` (agent-facing instructions and the spec's required frontmatter):

```markdown
---
name: run-variance-analysis
description: Flag unusual variance vs. forecast after month-end close. Use after the close period when reviewing financial performance.
license: MIT
---

Compare actuals vs. forecast for the most recent close period. For each line
item, flag variances above the threshold defined in your team's policy doc.
Output a markdown table sorted by absolute variance.
```

`ARTIFACT.md` (Podium structured frontmatter; no body):

```markdown
---
type: skill
version: 1.0.0
when_to_use:
  - "After month-end close, when reviewing financial performance"
tags: [finance, close, variance]
sensitivity: low
---

<!-- Skill body lives in SKILL.md. -->
```

Ingest:

```bash
$ git add SKILL.md ARTIFACT.md && git commit -m "Add run-variance-analysis@1.0.0"
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
→ {manifest: <SKILL.md body>, materialized_at: "/workspace/.podium/runtime/.../SKILL.md"}
```

The agent now has the skill in its working set. Done.
