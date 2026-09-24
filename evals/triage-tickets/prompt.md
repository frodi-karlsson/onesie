---
description: Routing a file of tickets must check .error before reading answers and treat exit 6 as partial success.
max_turns: 10
allowed_tools: [Read, Glob, Grep, Skill]
---

I have tickets.jsonl, one JSON object per line with a `text` field. A nightly bash job should use
the onesie CLI to route every ticket to billing, shipping or support, and append each routed ticket
with its team to routed.jsonl. Write the job script. onesie is installed and has a key.
