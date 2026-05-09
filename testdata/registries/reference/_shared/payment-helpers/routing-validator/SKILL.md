---
name: routing-validator
description: Validate US routing numbers via the ABA checksum.
license: MIT
---

Check a 9-digit US routing number against the ABA checksum:
sum = 3*(d1 + d4 + d7) + 7*(d2 + d5 + d8) + (d3 + d6 + d9). The
number is valid when sum mod 10 == 0.
