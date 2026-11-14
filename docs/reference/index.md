---
title: Reference
nav_order: 5
description: Lookup pages for the CLI, the HTTP API, the registry configuration keys, error codes, audit events, and the glossary.
---

# Reference

The reference pages describe each surface of the Podium registry for lookup: commands, endpoints, configuration keys, error codes, and audit records. For task-oriented guides, start from [Authoring](../authoring/), [Consuming](../consuming/), or [Deployment](../deployment/).

| Page | What it covers |
|:--|:--|
| [CLI](cli) | Every `podium` subcommand with its flags, defaults, and exit codes: setup, server, sync, layer management, search, admin, and signing. Also lists the `PODIUM_*` environment variables. |
| [HTTP API](http-api) | The registry's wire surface: discovery endpoints, materialization, ingest webhooks, scope preview, and health. |
| [Configuration](configuration) | The `registry.yaml` file layout, every server configuration key, and the environment variable that sets each one. |
| [Error codes](error-codes) | The structured error envelope and the full namespace catalog. |
| [Audit events](audit-events) | The audit record format; the file, HTTP, OTLP, and stdout destinations; delivery and loss accounting; receiver checks; and the OTLP and ECS field mappings. |
| [Glossary](glossary) | The vocabulary used across the docs, with a link from each term to the page that covers it. |

The field-by-field frontmatter schema for `ARTIFACT.md` and `SKILL.md` lives in the Authoring section as the [frontmatter reference](../authoring/frontmatter-reference), and [Domains](../authoring/domains) lists the `DOMAIN.md` fields.
