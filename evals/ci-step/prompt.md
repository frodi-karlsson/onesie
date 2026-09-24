---
description: A jev step in CI must tell exit 1 apart from exits 2 through 5.
max_turns: 10
allowed_tools: [Read, Glob, Grep, Skill]
---

Add a step to our GitHub Actions workflow that uses the jev CLI to fail the build when the pull
request description does not explain why the change is needed. The description is available in
the step as the environment variable PR_BODY, and TYPESAFE_API_KEY is set from a secret. Show me
the step.
