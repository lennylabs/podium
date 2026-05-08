---
layout: default
title: Hints
parent: Authoring
nav_order: 9
description: Advisory metadata fields — effort_hint and model_class_hint — that capture authoring intent about reasoning budget and model capability.
---

# Hints

Advisory fields capture authoring intent about the runtime resources an artifact ideally consumes:

```yaml
effort_hint: low | medium | high | max
model_class_hint: nano | small | medium | large | frontier
```

Both are advisory only. Podium does not enforce them. Adapters and SDK consumers translate to host-native knobs (model selector, extended-thinking flag, retry budgets) where supported; otherwise ignored.

Applicable to types `agent`, `skill`, and `command`. Ingest lint warns when set on other types.

---

## effort_hint

Hint about the reasoning budget the artifact ideally consumes:

| Value | Meaning |
|:--|:--|
| `low` | Quick, single-pass. No extended thinking. Tight token budget. |
| `medium` | Standard. May involve a small amount of extended thinking. Moderate token budget. |
| `high` | Deep reasoning. Extended thinking encouraged. Generous token budget. |
| `max` | Maximum effort. Extended thinking, retry loops, validator strictness. |

Use `low` for skills like greeting handlers or simple lookups, where time-to-first-token matters more than depth. Use `max` for agents that do open-ended investigation, multi-step planning, or anything where the right answer matters more than throughput.

---

## model_class_hint

Hint about the model capability tier:

| Value | Approximate tier (industry-standard) |
|:--|:--|
| `nano` | Smallest tier (Haiku, GPT-4o-mini, Gemini Flash, etc.). |
| `small` | Cost-optimized tier. |
| `medium` | Standard mid-tier (Sonnet, GPT-4o, Gemini Pro mid-tier). |
| `large` | Higher-capability tier. |
| `frontier` | Top-of-line model (Opus, GPT-5, Gemini Ultra, etc.). |

The mapping to specific models is harness- and deployment-dependent. The tiers are author-facing labels; deployment configuration decides which model name backs each tier.

---

## Advisory framing

These fields are advisory:

- Authors set them based on what the artifact ideally wants.
- Consumers (harness adapter, SDK caller) decide whether and how to honor them.
- Hosts without a notion of "model class" or "effort" ignore the fields.
- Podium itself never validates that a deployment has a model matching the hint.

Concretely: an artifact with `model_class_hint: frontier` does not fail to load when a deployment lacks a frontier-tier model. The host runtime makes the routing decision; the hint is one signal among many (cost budget, availability, user override).

---

## Adapter support

No built-in adapter currently translates these fields. Custom SDK consumers that build their own routing logic can read the hints from the manifest and route accordingly. The capability matrix in [§6.7.1 of the spec](https://github.com/lennylabs/podium/blob/main/spec/06-mcp-server.md#671-the-authors-burden) tracks adapter support; adapter-level honoring lands as adapters add the capability.

---

## Why these fields are advisory

Authors don't know what models the consumer's deployment has. A skill author at one company doesn't know whether the consuming team has access to frontier models, whether their cost budgets allow it, or whether the runtime they use respects per-call model selection.

Recording author intent without enforcement keeps the artifact portable across deployments. Consumers with the relevant runtime knobs can opt to honor the hint; consumers without those knobs ignore it without breaking. The artifact still works either way.

---

## Lint behavior

- `effort_hint` or `model_class_hint` on a `context`, `rule`, `hook`, or `mcp-server` artifact: lint warning ("hints apply to types: agent, skill, command").
- An invalid value (outside the enum): ingest error.
- Both fields are optional. Missing fields produce no warnings.
