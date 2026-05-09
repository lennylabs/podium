---
type: mcp-server
version: 1.0.0
description: Read-only access to the finance data warehouse.
sensitivity: low
license: MIT
server_identifier: npx:@company/finance-warehouse-mcp
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]
---

The finance data warehouse exposes read-only views of GL, AP, AR,
and forecast tables.
