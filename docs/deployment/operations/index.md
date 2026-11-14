---
title: Operations
nav_order: 4
description: "Day-two operations for a Podium registry: observability, signing keys, backup, upgrades, capacity, security review, outbound webhooks, and troubleshooting."
---

# Operations

The pages in this section cover running a registry after it is deployed: watching it, keeping its signing key safe, backing it up, upgrading it, sizing it, reviewing its security, connecting it to other systems through outbound events, and diagnosing failures. They apply to a [single-node](../modes/single-node) or [clustered](../modes/clustered) deployment. A page or section that applies to one mode only says so.

| Page | What it covers |
|:--|:--|
| [Audit and observability](observability) | Where the registry writes its audit records, the audit hash chain, Prometheus metrics, OpenTelemetry tracing, notifications, and alert rules. |
| [Signing keys](signing-keys) | What the registry signs, generating, distributing, backing up, and rotating the signing key, recovering from a compromised or lost key, the `sign-stored-rows` command, and signing failures. |
| [Backup and restore](backup-restore) | What to back up on each deployment mode, the default recovery objectives, and the restore drill. |
| [Upgrading](upgrading) | How migrations run, the upgrade procedure for a single node and for a rolling clustered upgrade, mixed-version replicas, releases that migrate stored values, and rollback. |
| [Upgrade notes](upgrade-notes) | The steps that belong to one release, newest first, including the Helm chart procedure for the 0.5.0 rewrite of stored content hashes. |
| [Capacity and availability](capacity) | The sizing baseline and scaling thresholds, and the read-only mode that keeps reads serving through a Postgres primary outage. |
| [Security review checklist](security-review) | The checks to run before a sensitive tenant goes live, signature enforcement, tamper checks, and the limits of stored-row admission. |
| [Outbound webhooks](outbound-webhooks) | The change events the registry sends to receivers, receiver registration and its URL policy, and integrations such as a CI relay. |
| [Troubleshooting](troubleshooting) | An index of problems by symptom and by error code, operational issues, startup refusals, public-mode misconfiguration, and when to escalate. |

The backing services the registry depends on, such as the metadata store, object storage, and the vector index, are covered in [Backing services](../services/).
