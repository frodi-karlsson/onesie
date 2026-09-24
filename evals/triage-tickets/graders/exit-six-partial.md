---
type: llm
---

PASS if the script treats jev exit code 6 as a partial success, meaning the run finished and only some records failed, so the routed records are kept and the failed ones are reported or retried, while other non zero exit codes are treated as a failed run.
FAIL if the script ignores the exit code of jev, treats exit 6 the same as a total failure, or never mentions exit 6.
FAIL if no jev command is given.
