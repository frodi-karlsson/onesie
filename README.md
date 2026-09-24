# onesie

Possibly the most expressive Jev CLI.

onesie asks TypeSafe's Jev model typed questions about text from the shell: a probability, a pick
from named options, or a level on a rubric. State goes in on stdin, typed answers come out on
stdout, and the exit code is usable in a conditional, so it drops into a script the way `grep` does.

```sh
echo 'EVERYTHING IS DOWN, CALL ME NOW' | onesie 'does this convey urgency' -r
# 0.99
```

## Install

```sh
brew install frodi-karlsson/tap/onesie
onesie auth set
```

Or with Go, `go install github.com/frodi-karlsson/onesie/cmd/onesie@latest`.

In Claude Code, the plugin can come first and walk you through the rest:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

## Features

- **All three shapes.** Yes or no, `--pick` and `--rate`, with a description per option or level.
- **Many questions in one request.** Jev answers them in parallel. Thirteen questions in one call
  measured about 9 times cheaper and 13 times faster than thirteen separate calls.
- **A typed gate language.** `--assert` takes `and`, `or`, `not`, `in` and `min`, `max`, `sum`,
  `avg` over paths like `team.p.human`, checked before any request, so a typo costs no tokens.
- **Three way gates.** `--abstain-if` turns a no into an unsure with its own exit code, for the
  middle ground a person should decide.
- **Choose what the model reads.** `--map` runs jq on each record, so the model sees `.body`, not the
  customer's name.
- **Streams that resume.** JSONL, lines, CSV and TSV with `-j` in flight, one output record per input
  record in input order. With `--id`, an interrupted run picks up where it stopped, and a changed
  question refuses to mix old answers with new.
- **Freeze and replay.** A command line becomes a question file, a run becomes its exact request
  bodies, and either replays unchanged.
- **Exit codes that tell a no from an outage.** Nine of them, separating a no, an unsure, a bad key,
  a server that never answered and a stream where only some records failed.
- **Two providers, keys in the keychain.** TypeSafe directly or through OpenRouter, with keys in the
  OS keychain.
- **Agent skills.** A plugin for Claude Code and Codex, with the same skills for Cursor and Gemini.

## Usage

**Triage a support ticket.** Several questions cost about what one does.

```sh
onesie --ask urgent='does this convey urgency' \
       --ask refund='is the customer asking for money back' \
       --ask team='who should handle this' --pick billing,shipping,technical \
       -o values < ticket.txt
# {"urgent":0.97,"refund":0.99,"team":"billing"}
```

**Score on a rubric.** A description per level steers the answer.

```sh
onesie 'how frustrated is the customer' -o json \
    --rate calm,annoyed,furious \
    --desc calm='polite, no complaint' \
    --desc annoyed='complains but stays civil' \
    --desc furious='threatens to leave or escalate' < ticket.txt
# {"model":"jev-1.13.0","answer":{"value":"annoyed","confidence":0.8,...}}
```

**Guard an agent's shell commands.** Run it, block it, or ask a person.

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

**Fail a CI step on a pull request without a reason.** Tell a no from an outage in the log.

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

**Triage a spreadsheet.** The same rows come back with a column per question.

```sh
onesie --ask urgent='does `body` convey urgency' -i csv -o csv --merge < tickets.csv > triaged.csv
```

**Run a large batch you can resume.** Send only the body, name each answer by ticket, and pick up
after an interrupt without paying twice.

```sh
onesie 'is this urgent' -i jsonl -j 8 --map '.body' --id '.id' \
    --out answers.jsonl --resume < tickets.jsonl
jq -c 'select(.answer.value > 0.8)' answers.jsonl
```

**Try it before spending anything.** A dry run needs no key, and a frozen stream replays exactly.

```sh
onesie --ask urgent='does this convey urgency' --state 'the site is down' --print-request
onesie -i jsonl --ask urgent='does this convey urgency' --print-request < tickets.jsonl > frozen.jsonl
onesie -i request < frozen.jsonl > answers.jsonl
```

**Keep a question set in a file.** A command line freezes into one, and the file reloads as is.

```sh
onesie --ask urgent='does this convey urgency' --ask team='who owns this' --pick billing,platform \
    --assert 'urgent.value < 0.9' --print-questions > triage.yaml
onesie -f triage.yaml -o values < ticket.txt
# {"assert":false,"urgent":0.97,"team":"billing"}, and exit 1, since this ticket is urgent
```

## Reference

### Gating

`--assert` is one boolean over the record, and it sets the exit code. It reads the field names
`-o json` prints, and every path is checked before any request:

```sh
onesie --ask urgent='is this urgent' --assert 'urgnet.value < 0.5'
# onesie: --assert: unknown question 'urgnet'. Questions: urgent
```

- A false assertion exits 1 and still prints the record with `"assert": false`. `-q` prints nothing
  and leaves the exit code to the assertion. Repeated flags combine with `and`.
- `--abstain-if` is checked only when the assertion fails. When it holds, the record is an unsure:
  exit 7, and `"abstain": true` in place of `"assert": false`.
- `min`, `max`, `sum` and `avg` take numbers and nest. `==` rarely matches a `sum` or `avg`, so use
  `>=` or `<=`. There are no arithmetic operators, so weighting answers is a job for `jq`.
- A question file carries the same expressions as `assert` and `abstain_if`.

### Streams

- A record that fails still prints a line with an `error` key, and the run exits 6.
  `--stop-on-error` ends at the first failure, and `--unordered` trades input order for throughput.
- `--map` is a jq expression whose result is the state. An object it builds has its keys sorted, and
  `onesie -V` lists the caps on its result.
- `--id` names each record on every output line. It runs one record at a time, so keep it a cheap
  lookup. Ids match by their text, so `7`, `7.0` and `"7"` are one id in jsonl.
- `--out` writes to a file with a fingerprint of the questions, provider, model, `--map` and `--id`
  beside it, and a lock so two runs cannot share it.
- `--resume` with `--id` skips answered records and asks the rest, failed ones included. A finished
  run rewrites the file in input order and keeps answered ids the input no longer has, unless
  `--prune` drops them. A changed fingerprint refuses with exit 2. A record whose content changed
  but whose id did not keeps its old answer. Without `--id`, it carries on by line count.

### Keys and providers

```sh
onesie auth set                                    # prompts, or reads the first line of stdin
pass show openrouter | onesie --provider openrouter auth set
onesie auth status                                 # which provider and source, never the key
onesie auth test                                   # checks the key, costs no tokens
```

| Provider | Key variable | Chosen with |
|----------|--------------|-------------|
| `typesafe`, the default | `TYPESAFE_API_KEY` | nothing |
| `openrouter` | `OPENROUTER_API_KEY` | `--provider openrouter` or `ONESIE_PROVIDER=openrouter` |

- A key comes from `--api-key`, then the provider's variable, then the credential file. No provider
  falls back to another's key.
- `auth set` stores the key in the OS keychain when there is one, and otherwise in a file at mode
  `600` under `$ONESIE_CONFIG_DIR`, `$XDG_CONFIG_HOME/onesie` or `~/.config/onesie`. onesie refuses
  a file anyone else can reach. On Linux the key reaches the Secret Service over the session bus
  unencrypted, readable only by your own user.
- `jev-latest` works on both providers, but pinned ids differ: `jev-1.13.0` on TypeSafe,
  `typesafe/jev-1.13` on OpenRouter. On OpenRouter, `--usage` also reports the cost.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | answered |
| 1 | a false `--assert`, or under `-q` alone the policy did not accept the answer |
| 2 | usage or validation error, including a missing key |
| 3 | the key was refused, the account is out of credits, or the credential file is exposed |
| 4 | the server did not answer after retries |
| 5 | transport error or timeout |
| 6 | a stream finished with one or more failed records |
| 7 | the gate could not decide: the assertion failed and `--abstain-if` held |
| 130 | interrupted |

A consumer that stops reading, as `head` does, is not an error.

### More

- `--stats` writes a one line summary to stderr: requests, tokens, model, retries and time.
- `--list-models` shows what the account can ask, and `-m` picks one.
- `--base-url` points onesie at any server that speaks the System One API.
- `--retries`, `--timeout` and `--max-retry-after` bound how long a call can take.
- `onesie --help` lists every flag, and `onesie -V` prints the built in limits.

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
