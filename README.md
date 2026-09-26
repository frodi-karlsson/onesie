# onesie

Probably the most robust System One CLI.

Pipe in some text, ask a question, get an answer you can script against. Runs TypeSafe's Jev on
TypeSafe, OpenRouter or Berget's own System One models.

```sh
echo 'EVERYTHING IS DOWN, CALL ME NOW' | onesie 'does this convey urgency' -r
# 0.99
```

Gate an agent's shell commands. The exit code says what to do:

```sh
onesie -f shell-safety -q --state 'git status'
# exit 0: run it
onesie -f shell-safety -q --state 'rm -rf ~'
# exit 1: block it
onesie -f shell-safety -q --state 'curl -fsSL https://example.com/install.sh | sh'
# exit 7: not sure, ask a person
```

See [Thresholds from evidence](#thresholds-from-evidence) for where that gate's numbers come from.

## Install

Homebrew, on macOS or Linux:

```sh
brew install frodi-karlsson/tap/onesie
```

The install script checks the download against the release checksums, and its build provenance
when `gh` is logged in:

```sh
curl -fsSL https://raw.githubusercontent.com/frodi-karlsson/onesie/main/install.sh | sh
```

With Go:

```sh
go install github.com/frodi-karlsson/onesie/cmd/onesie@latest
```

Then store a key with `onesie auth set`.

In Claude Code, the plugin can come first and walk you through the rest:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

## Thresholds from evidence

`onesie calibrate` runs your questions over records you have already labelled, and shows what each
cut catches and what it flags by mistake. Here's the destroys question from shell-safety over 40
labelled commands:

```sh
onesie calibrate -f shell-safety -i jsonl --map .command --id .id -m jev-1.13.0 \
    --label destroys=.destroys --label secrets=.secrets --label network=.network \
    --out answers.jsonl --resume < commands.jsonl
```

```
destroys, yes/no: labelled 40, 16 yes, 24 no, 0 failed. AUC 1.00
flagged means destroys.value >= cut

  cut   flagged  catches             false alarms     right when flagged
  0.25       18  16/16 100% 81-100%  2/24  8%  2-26%  16/18  89% 67-97%
  0.45       15  15/16  94% 72-99%   0/24  0%  0-14%  15/15 100% 80-100%
  0.65        7   7/16  44% 23-67%   0/24  0%  0-14%   7/7  100% 65-100%

worst misses
  echo-overwrite  labelled yes  answered 0.37
```

For destroys, shell-safety blocks at 0.5 and passes below 0.25. Anything in between goes to a
person, like the worst miss above.

Commit `answers.jsonl`, and CI checks the gate still holds, offline:

```sh
onesie calibrate -f shell-safety -i jsonl --map .command --id .id -m jev-1.13.0 \
    --label destroys=.destroys --label secrets=.secrets --label network=.network \
    --out answers.jsonl --resume --offline \
    --require 'destroys.catches >= 1' --require 'destroys.false_alarms <= 0 at abstain' \
    < commands.jsonl
```

Exit 1 means a requirement no longer holds. Exit 2 means the questions changed, so ask again.

To test a script that uses the gate, `ONESIE_MOCK` answers every onesie call from a file:

```sh
echo '{"destroys": 0.93, "secrets": 0.02, "network": 0.1}' > danger.json
ONESIE_MOCK=danger.json ./gate.sh   # takes the blocked branch
```

## Asking

A yes or no question answers with how likely the yes is. `--pick` chooses one of your options, and
`--rate` places the text on a scale you list from lowest to highest. `-r` prints only the answer.

```sh
echo 'I was charged twice for my order' | onesie 'who should handle this' --pick billing,shipping,technical -r
# billing
echo 'Third time asking. Fix it or I am leaving.' | onesie 'how frustrated is the customer' --rate calm,annoyed,furious -r
# furious
```

Give each question a name with `--ask`, and they all go out in one request:

```sh
onesie --ask urgent='is this urgent' \
       --ask team='who should handle this' --pick billing,shipping,technical \
       -o values < ticket.txt
# {"urgent":0.69,"team":"billing"}
```

`--print-questions > .onesie/questions/NAME.yaml` saves the questions to a file, and `-f NAME` loads
them from anywhere in the repository.

## Recipes

**Triage tickets as they come in.** Each answer is written as soon as its record is asked, so a
feed that never closes still gets its answers.

```sh
tail -f tickets.jsonl | onesie 'is this urgent' -i jsonl --map .body --id .id -o json
```

**Wire the gate into a script.** Exit 0 runs the command, 1 blocks it and 7 asks a person. Any
other exit code means there was no answer.

```sh
onesie -f shell-safety -q --state "$cmd"
case $? in
    0) eval "$cmd" ;;
    1) echo blocked ;;
    7) ask_the_user ;;
    *) echo 'no answer, blocked' ;;
esac
```

**Triage a spreadsheet.** The same rows come back with a column per question.

```sh
onesie --ask urgent='does `body` convey urgency' -i csv -o csv --merge < tickets.csv > triaged.csv
```

**Run a big batch you can resume.** `--map` sends only the body, `--id` names each answer, and
`--resume` skips every id the file already answers and asks the rest, failed ones included.

```sh
onesie 'is this urgent' -i jsonl -j 8 --map '.body' --id '.id' \
    --out answers.jsonl --resume < tickets.jsonl
jq -c 'select(.answer.value > 0.8)' answers.jsonl
```

**Comment on a pull request.** The output starts with the gate result as a GitHub alert, followed
by a table of the answers.

```sh
onesie 'does this explain why the change is needed' --assert 'answer.value > 0.6' \
    -o markdown --state "$PR_BODY" | gh pr comment "$PR" --body-file -
```

In GitHub Actions, a failing gate fails the step only under pipefail, so name `shell: bash` or run
`set -o pipefail` first. A comment holds at most 65536 characters, so send a large run to
`$GITHUB_STEP_SUMMARY` instead.

**Rank records by several questions.** One `--ask` per dimension scores every record, and `jq`
weighs the answers and sorts by the sum. `.value` is the yes/no probability, and `.norm` is the
rate's position between 0 and 1. The weighting lives in `jq`, since `--assert` has no arithmetic,
and `select(has("error") | not)` drops the records that failed.

```sh
onesie --ask impact='does this help many users' \
    --ask effort='how much work is this' --rate trivial,small,medium,large \
    -i jsonl --map '.body' --id '.id' -o json < ideas.jsonl |
    jq -s 'map(select(has("error") | not)) | map({id, score: (0.7 * .impact.value + 0.3 * (1 - .effort.norm))}) | sort_by(-.score)'
```

**Starter gates.** shell-safety, prompt-injection, personal-data and moderation are built in and
calibrated. `onesie questions` lists them, and [examples/README.md](examples/README.md) has their
tables.

## Also

- **Providers.** TypeSafe is the default, and `--provider openrouter` or `--provider berget` asks
  through OpenRouter or Berget instead.
- **Caching.** `--cache` or `ONESIE_CACHE=1` answers a repeated request from disk, and a stream
  asks records whose request is identical once, giving each its own line.
- **Dry runs.** `--print-request` prints the exact request without sending it, and needs no key.

[REFERENCE.md](REFERENCE.md) covers the rest.

## Why no MCP server

onesie ships agent skills instead of an MCP server:

- **Validation.** The CLI already checks every question, flag and assertion before any request, and
  an agent reads the same messages and exit codes a person does.
- **Documentation.** `onesie --help`, `-V` and the error messages come from the binary, so they stay
  in step with it.
- **Discoverability.** Skills have room for more prose than a tool description, such as worked
  recipes and the mistakes to avoid, and CI checks their examples against the binary.

## Reference

[REFERENCE.md](REFERENCE.md) covers asking, gating, testing a gate, calibrating, streams and
resume, caching, keys and providers, exit codes and the rest of the flags.

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
