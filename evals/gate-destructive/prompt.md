---
description: A gate that blocks a destructive command must use neither broken gate form.
max_turns: 10
allowed_tools: [Read, Glob, Grep, Skill]
---

I have a bash script where an automation hands me a shell command in the variable $cmd. Before
the script runs it, I want to use the jev CLI to block the command if it is destructive. Write me
the snippet. jev is installed and has a key.
