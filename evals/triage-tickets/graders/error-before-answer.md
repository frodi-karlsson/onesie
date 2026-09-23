---
type: llm
---

PASS if the script checks each output record for an `.error` field, for example with `select(.error == null)` or by routing error records elsewhere, before it reads the team answer out of the record.
FAIL if the script reads the team answer from every output line without checking `.error` first.
FAIL if no jev command is given.
