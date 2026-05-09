---
type: rule
version: 1.0.0
description: Apply finance-team coding style and disclosure rules.
sensitivity: low
rule_mode: glob
rule_globs: "src/finance/**/*.ts,src/finance/**/*.py"
---

When working on finance code:

- Always round monetary values to 2 decimal places.
- Use the company-glossary for terminology.
- Flag any forecast variance > 5% in PR comments.
