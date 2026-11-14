---
title: Hints
nav_order: 10
description: The advisory effort_hint and model_class_hint fields, which record authoring intent about reasoning budget and model capability.
---

# Hints

This page covers the hint fields, which record the runtime resources an artifact ideally consumes. Hints apply to `agent`, `skill`, and `command` artifacts. No built-in adapter currently translates these fields, so a hint has an effect only for a custom SDK consumer that reads it from the manifest and routes accordingly.

```yaml
effort_hint: low | medium | high | max
model_class_hint: nano | small | medium | large | frontier
```

Both fields are advisory, and Podium does not enforce them. A consumer can translate them to host-native settings, such as a model selector, an extended-thinking flag, or a retry budget. Consumers that have no such setting ignore them. Ingest lint warns when a hint is set on any other type.

---

## effort_hint

`effort_hint` describes the reasoning budget the artifact ideally consumes:

| Value | Meaning |
|:--|:--|
| `low` | Quick, single-pass. No extended thinking. Tight token budget. |
| `medium` | Standard. May involve a small amount of extended thinking. Moderate token budget. |
| `high` | Deep reasoning. Extended thinking encouraged. Generous token budget. |
| `max` | Maximum effort. Extended thinking, retry loops, validator strictness. |

Use `low` for skills such as greeting handlers or simple lookups, where time-to-first-token matters more than depth. Use `max` for agents that do open-ended investigation or multi-step planning, or wherever the right answer matters more than throughput.

---

## model_class_hint

`model_class_hint` describes the model capability tier the artifact ideally runs on:

| Value | Approximate tier (industry-standard) |
|:--|:--|
| `nano` | Smallest tier (Haiku, GPT-4o-mini, Gemini Flash, etc.). |
| `small` | Cost-optimized tier. |
| `medium` | Standard mid-tier (Sonnet, GPT-4o, Gemini Pro mid-tier). |
| `large` | Higher-capability tier. |
| `frontier` | Top-of-line model (Opus, GPT-5, Gemini Ultra, etc.). |

The tiers are author-facing labels. The mapping to specific models depends on the harness and the deployment, and deployment configuration decides which model name backs each tier.

---

## Advisory framing

Hints record author intent without enforcement:

- Authors set them based on what the artifact ideally needs.
- Consumers (the harness adapter or an SDK caller) decide whether and how to honor them.
- Hosts without a notion of model class or effort ignore the fields.
- Podium never validates that a deployment has a model matching the hint.

For example, an artifact with `model_class_hint: frontier` still loads when a deployment lacks a frontier-tier model. The host runtime makes the routing decision, and the hint is one signal among several, alongside cost budget, availability, and user override.

The fields are advisory because authors do not know what models a consuming deployment has. An author at one company cannot tell whether the consuming team has access to frontier models, whether its cost budget allows them, or whether its runtime respects per-call model selection. Recording intent without enforcement keeps the artifact portable across deployments. Consumers with the relevant runtime settings can honor the hint, and consumers without them ignore it. The artifact works in both cases.

---

## Lint behavior

- Setting `effort_hint` or `model_class_hint` on a `context`, `rule`, `hook`, or `mcp-server` artifact draws a lint warning ("hints apply only to agent / skill / command").
- Both fields are optional, and omitting them produces no warning.
