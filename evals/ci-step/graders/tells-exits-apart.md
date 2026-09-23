---
type: llm
---

PASS if the step handles jev exit code 1, meaning the policy judged the description inadequate, differently from exit codes 2 through 6, meaning jev itself failed through a usage error, an auth failure, an outage or a transport error, for example with a `case` on the exit code that prints a different message for each, and the build fails in both cases.
FAIL if the step treats every non zero exit the same way with no distinction, or if it only checks for exit 1 and lets exits 2 through 6 pass the build.
FAIL if no jev command is given.
