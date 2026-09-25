# onesie

Possibly the most expressive Jev CLI.

onesie asks TypeSafe's Jev model questions about a piece of text and gives back a typed answer: how
likely a yes is, which of your options fits, or where it sits on a scale. Text goes in on stdin, the
answer comes out on stdout, and the exit code reports the result.

```sh
echo 'EVERYTHING IS DOWN, CALL ME NOW' | onesie 'does this convey urgency' -r
# 0.99
```

## Install

Homebrew, on macOS or Linux:

```sh
brew install frodi-karlsson/tap/onesie
```

The install script, which checks the download against the release's `checksums.txt`, and against
its build provenance when `gh` is logged in:

```sh
curl -fsSL https://raw.githubusercontent.com/frodi-karlsson/onesie/main/install.sh | sh
```

It installs to `~/.local/bin`. Set `ONESIE_INSTALL_DIR` for another directory, and `ONESIE_VERSION`
for a release other than the latest.

With Go:

```sh
go install github.com/frodi-karlsson/onesie/cmd/onesie@latest
```

macOS blocks a onesie binary downloaded through a browser, so install it with one of these three.

Homebrew and the install script need a published release. Until the first one, use `go install`.
Then store a key with `onesie auth set`.

In Claude Code, the plugin can come first and walk you through the rest:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

## Features

- **Three kinds of question.** Yes or no, pick one of your options, or rate on a scale.
- **Several questions per request.** Five questions cost about as much as one.
- **Answers as exit codes.** `--assert` turns an answer into pass or fail for a script or CI, with a
  separate unsure result for a person to decide.
- **Thresholds from data.** `onesie calibrate` shows what each threshold catches and misses on
  examples you have already labelled.
- **Unix pipes.** Reads stdin, writes answers to stdout and errors to stderr, so it works with
  `jq`, `grep` and `xargs`.
- **Streams that resume.** Reads JSONL, CSV or TSV a record at a time, several in parallel, and
  writes one answer per record in order. Rerun it with `--resume` after an interruption. With
  `--id` it asks only the records the file holds no answer for, and without it carries on after the
  last complete line.
- **Output for programs and people.** JSON, CSV, a terminal table, or markdown for a PR comment.
- **Free dry runs.** See the exact request before spending anything, with no key needed.
- **Agent skills.** A plugin for Claude Code and Codex, and the same skills for Cursor and Gemini.

## Usage

### Ask a question

A yes or no question answers with how likely the yes is.

```sh
echo 'The site is down and customers cannot pay' | onesie 'is this urgent'
# model jev-1.13.0
#
# answer  0.9700
```

`-r` prints only the answer.

```sh
echo 'The site is down and customers cannot pay' | onesie 'is this urgent' -r
# 0.96
```

### Pick from options

```sh
echo 'I was charged twice for my order' | onesie 'who should handle this' \
    --pick billing,shipping,technical -r
# billing
```

Without `-r`, the table also shows how sure the model is of each option.

### Rate on a scale

List the levels from lowest to highest.

```sh
echo 'Third time asking. Fix it or I am leaving.' | onesie 'how frustrated is the customer' \
    --rate calm,annoyed,furious -r
# furious
```

`--desc` describes an option or a level, which steers the answer. A scale needs every level
described, or none.

### Ask several questions at once

Give each question a name with `--ask`. They go out in one request.

```sh
onesie --ask urgent='is this urgent' \
       --ask team='who should handle this' --pick billing,shipping,technical \
       -o values < ticket.txt
# {"urgent":0.69,"team":"billing"}
```

### Use the answer in a script

`--assert` sets the exit code: 0 when it holds, 1 when it does not. `-q` prints nothing.

```sh
onesie 'is this urgent' --assert 'answer.value > 0.8' -q < ticket.txt && page_on_call
```

Any other exit code means there was no answer, such as a bad key or a server that did not reply.
[REFERENCE.md](REFERENCE.md#exit-codes) lists them.

## Recipes

**Guard an agent's shell commands.** Exit 0 runs the command, 1 blocks it and 7 asks a person.

```sh
onesie --ask d='does this destroy data' --ask c='does this send credentials' \
    --assert     'max(d.value, c.value) < 0.2' \
    --abstain-if 'max(d.value, c.value) < 0.8' \
    -q --state "$cmd"
case $? in
    0) eval "$cmd" ;;
    1) echo blocked ;;
    7) ask_the_user ;;
    *) echo 'no answer, blocked' ;;
esac
```

**Fail a CI step when a pull request does not say why.** Exit 1 means the description does not
say why, and any other code means there was no answer.

```sh
status=0
onesie 'does this explain why the change is needed' --assert 'answer.value > 0.6' \
    -q --state "$PR_BODY" || status=$?
case $status in
    0) ;;
    1) echo 'the description does not say why'; exit 1 ;;
    *) echo "onesie gave no answer, exit $status"; exit 1 ;;
esac
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

**Triage a spreadsheet.** The same rows come back with a column per question.

```sh
onesie --ask urgent='does `body` convey urgency' -i csv -o csv --merge < tickets.csv > triaged.csv
```

**Run a large batch you can resume.** `--map` sends only the body, `--id` names each answer, and
`--resume` skips every id the file already answers and asks the rest, failed ones included.

```sh
onesie 'is this urgent' -i jsonl -j 8 --map '.body' --id '.id' \
    --out answers.jsonl --resume < tickets.jsonl
jq -c 'select(.answer.value > 0.8)' answers.jsonl
```

**Pick a threshold from labelled tickets.** Each row shows how many urgent tickets that threshold
catches and how many it flags by mistake.

```sh
onesie calibrate --ask urgent='is this urgent' -i jsonl --map '.body' \
    --label urgent='.is_urgent' --id '.id' < labelled.jsonl
# urgent, yes/no: labelled 8, 4 yes, 4 no, 0 failed. AUC 1.00
# flagged means urgent.value >= cut
#
#   cut   flagged  catches           false alarms    right when flagged
#   ...
#   0.90        4  4/4 100% 51-100%  0/4  0%  0-49%  4/4 100% 51-100%
onesie 'is this urgent' --assert 'answer.value >= 0.9' -q --state "$body" && page_on_call
```

**Try it before spending anything.** `--print-request` prints the request without sending it and
needs no key. `-i request` later sends those requests unchanged.

```sh
onesie 'is this urgent' --state 'the site is down' --print-request
onesie -i jsonl 'is this urgent' --print-request < tickets.jsonl > frozen.jsonl
onesie -i request < frozen.jsonl > answers.jsonl
```

**Keep a question set in a file.** `--print-questions` writes the questions to a file, and
`-f NAME` loads it from any directory in the repository.

```sh
mkdir -p .onesie/questions
onesie --ask urgent='is this urgent' --ask team='who owns this' --pick billing,platform \
    --assert 'urgent.value < 0.9' --print-questions > .onesie/questions/triage.yaml
onesie -f triage -o values < ticket.txt
# {"assert":false,"urgent":0.97,"team":"billing"}, and exit 1, since this ticket is urgent
```

[examples/README.md](examples/README.md) has starter sets for shell safety, prompt injection,
personal data and moderation. Each file carries a calibrated gate. Copy one into
`.onesie/questions` and `-f shell-safety` runs the whole gate, or pass the path, as in
`-f examples/questions/shell-safety.yaml`.

## Why no MCP server

onesie ships agent skills instead of an MCP server:

- **Validation.** The CLI already checks every question, flag and assertion before any request, and
  an agent reads the same messages and exit codes a person does.
- **Documentation.** `onesie --help`, `-V` and the error messages come from the binary, so they stay
  in step with it.
- **Discoverability.** Skills have room for more prose than a tool description, such as worked
  recipes and the mistakes to avoid, and CI checks their examples against the binary.

## Reference

[REFERENCE.md](REFERENCE.md) covers gating, calibrating, streams and resume, keys and providers,
exit codes and the rest of the flags.

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
