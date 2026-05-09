---
type: hook
version: 1.0.0
description: Audit log every payment-submit tool call.
sensitivity: medium
hook_event: pre_tool_use
hook_action: |
  jq -r 'select(.tool_name == "payment-submit") | .arguments' < /dev/stdin >> /var/log/podium/payment-audit.log
sbom:
  format: cyclonedx-1.5
  ref: ./sbom.json
---

Append every payment-submit tool invocation to the local audit log
for downstream SIEM ingestion.
