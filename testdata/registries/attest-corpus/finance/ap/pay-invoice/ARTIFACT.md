---
type: agent
version: 1.0.0
description: Pay an approved vendor invoice.
when_to_use:
  - "After AP has approved an invoice for payment."
tags: [finance, ap, payments]
sensitivity: medium
license: MIT
sbom:
  format: cyclonedx-1.5
  ref: ./sbom.json
---

# pay-invoice

Confirm AP approval, validate vendor identity, and submit the payment via the
finance warehouse MCP server.
