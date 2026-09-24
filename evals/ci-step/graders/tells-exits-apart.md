---
type: llm
---

PASS if the step handles jev exit code 1, meaning the policy judged the description inadequate, differently from exit codes 2 through 5, meaning no answer arrived because of a usage error, a bad key, an exhausted retry or a transport failure, for example with a `case` on the exit code that prints a different message for each, and the build fails in both cases. Exit 6 means a stream finished with failed records, which cannot happen for one PR body, so the step need not handle it.
FAIL if the step treats every non zero exit the same way with no distinction, or if it only checks for exit 1 and lets exits 2 through 5 pass the build.
FAIL if no jev command is given.
