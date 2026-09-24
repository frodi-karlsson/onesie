---
type: llm
---

PASS if the onesie command measures risk as a degree with `--rate` and an ordered list of levels, such as `--rate low,medium,high`, and sorts on the returned level or score.
FAIL if the script asks a yes or no question such as 'is this diff risky' and sorts on the returned probability as if it were a risk scale.
FAIL if no onesie command is given.
