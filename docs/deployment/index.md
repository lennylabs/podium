---
layout: default
title: Deployment
nav_order: 4
has_children: true
description: Pick a deployment shape, run it, scale up. Filesystem for solo, standalone for small teams, standard for organizations.
---

# Deployment

Pick a deployment shape based on the audience and the operational tolerance:

| Shape | Audience | What's running | Page |
|:--|:--|:--|:--|
| **Filesystem** | Individual developer; prototype; CI build step. | The `podium` CLI. No daemon, no port, no auth. Catalog is a folder. | [Solo / filesystem](solo-filesystem) |
| **Standalone server** | 3–10 person team; single VM behind a VPN; offline / air-gapped. | One binary (`podium serve --standalone`). Embedded SQLite + sqlite-vec + bundled embedding model. | [Small team](small-team) |
| **Standard** | 20+ people; multi-tenant; governed. | Replicated registry behind a load balancer; Postgres + object storage; OIDC. | [Organization](organization) |

Same artifacts, same author flow, same shared library. Migration between shapes is mechanical: `podium serve --standalone --layer-path /path/to/dir` against a filesystem catalog turns it into a server source without touching the authoring loop, and `podium admin migrate-to-standard` exports a standalone deployment to a standard one.

---

## Other pages

| Page | What it covers |
|:--|:--|
| [Progressive adoption](progressive-adoption) | An on-ramp from permissive standalone to enforced governance, in stages. Use it once you have a standalone deployment running and want to add identity, sensitivity labels, signing, freeze windows, etc. |
| [Operator guide](operator-guide) | Day-two operations for a standard deployment: capacity planning, monitoring, alerting, backup and restore, upgrades, security review checklist, common pitfalls. |
| [Extending](extending) | Plugin SPIs, the forward-compatibility constraints that keep out-of-process plugins on the table, and the external-extension patterns. |
| [OIDC cookbooks](oidc/) | Per-IdP setup recipes for Okta, Entra ID, Google Workspace, Auth0, and Keycloak. |
