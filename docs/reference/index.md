---
title: Reference
nav_order: 5
description: CLI commands, HTTP API endpoints, frontmatter schema, error codes, audit events, and glossary.
---

# Reference

The reference pages describe each surface of the Podium registry for lookup: commands, endpoints, file formats, error codes, and audit records. For task-oriented guides, start from [Authoring](../authoring/), [Consuming](../consuming/), or [Deployment](../deployment/).

| Page | What it covers |
|:--|:--|
| [CLI](cli) | Every `podium` subcommand with its flags, defaults, and exit codes: setup, server, sync, layer management, search, admin, and signing. Also lists the `PODIUM_*` environment variables. |
| [HTTP API](http-api) | The registry's wire surface: discovery endpoints, materialization, ingest webhooks, scope preview, and health. |
| [Frontmatter schema](../authoring/frontmatter-reference) | Field-by-field schema for `ARTIFACT.md`, `SKILL.md` (for skills), and `DOMAIN.md`. |
| [Error codes](error-codes) | The structured error envelope and the full namespace catalog. |
| [Audit events](audit-events) | The audit record format; the file, HTTP, OTLP, and stdout destinations; delivery and loss accounting; receiver checks; and the OTLP and ECS field mappings. |
| [Glossary](glossary) | Vocabulary used across the docs. |
