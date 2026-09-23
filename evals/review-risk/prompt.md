---
description: Scoring diffs for review risk must use --rate rather than a yes or no probability read as a scale.
max_turns: 10
allowed_tools: [Read, Glob, Grep, Skill]
---

We want to sort the diffs in a pull request by how risky each one is to review, so reviewers look
at the riskiest first. Each diff is in its own file under diffs/. Write a bash script that uses
the jev CLI to give every diff a risk score and prints them sorted, riskiest first. jev is
installed and has a key.
