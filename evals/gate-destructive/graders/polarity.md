---
type: llm
---

PASS if the snippet only lets $cmd run when jev has judged it safe, either by asking whether the command is safe and running it on success, or by asking whether it is destructive and running it only when an assertion such as `danger.value < 0.5` holds.
FAIL if the snippet asks whether the command is destructive or dangerous with `-q` or `--threshold` and then runs $cmd when jev exits 0, since that runs exactly the destructive commands.
FAIL if the snippet combines `-q` with `--assert`.
FAIL if no jev command is given.
