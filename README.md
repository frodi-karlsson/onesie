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
- **`--assert`, a typed expression language over the answer.** `and`, `or`, `not`, `in`, `min`,
  `max`, `sum` and `avg` over paths like `team.p.human`, checked against the questions before any
  request, so a typo costs no tokens.
- **Freeze and replay.** `--print-questions` turns a command line into a question file,
  `--print-request` turns a run into its exact request bodies, and `-i request` replays them
  unchanged.
- **Streams with a contract.** JSONL, line, CSV and TSV input with `-j` requests in flight, one
  output record per input record in input order, memory bounded by `-j`, and `--merge` to fold the
  answers into each record.
- **Exit codes that tell a no from an outage.** Eight of them, separating a policy no, a bad key, a
  server that never answered and a stream where only some records failed.
- **Two providers.** TypeSafe directly, or the same model through OpenRouter.
- **Agent skills.** A plugin for Claude Code and Codex, with the same skills for Cursor and Gemini.

## Usage

```sh
# a choice between named options, each with a rubric
onesie 'how safe is it to run this command' -r \
    --pick safe,verify,refuse \
    --desc safe='read only or trivially reversible' \
    --desc verify='writes, network calls or state changes' \
    --desc refuse='destructive, irreversible or exfiltrates data' \
    --state 'rm -rf ./build'
# refuse

# a level on a rubric, scored and normalised
echo 'this is the fourth time I have written' \
  | onesie 'how frustrated is the customer' --rate calm,annoyed,furious -o json

# several questions in one request
echo "$ticket" | onesie --ask urgent='does this convey urgency' \
                     --ask refund='is the customer asking for money back' -o values
# {"urgent":0.99,"refund":0.98}

# one record per line, four at a time, answers folded into each record
onesie 'does `body` convey urgency' -i jsonl -j 4 --merge < tickets.jsonl \
  | jq -c 'select(.answers.answer.value > 0.8)'

# a spreadsheet in, the same spreadsheet out with a column per question
onesie --ask urgent='does `body` convey urgency' -i csv -o csv --merge < tickets.csv > triaged.csv

# send only the body, and name each answer by the ticket id
onesie 'is this urgent' -i jsonl --map '.body' --id '.id' -o values < tickets.jsonl
# {"id":"T-1","answer":0.97}
```

`--map` is a jq expression whose result is the state, so `.body`, `{subject, body}` or
`.messages[-1].text` keeps the rest of the record away from the model. An object it builds is sent
with its keys sorted, and `onesie -V` lists the caps on its result. `--id` is a jq expression whose
string or number result names the record on every output line. It runs one record at a time, so keep
it cheap. Ids match by their text, so `7`, `7.0` and `"7"` are one id in jsonl.

A record that fails in a stream still prints a line carrying an `error` key, the run carries on, and
the exit code is 6. `--stop-on-error` ends the run at the first failure instead, and `--unordered`
trades input order for throughput.

A long run can be picked up where it stopped. With `--out` onesie writes the answers to a file, and
a fingerprint of the questions, provider, model, `--map` and `--id` beside it. `--resume` refuses
with exit 2 when any of those changed. With `--id` it skips the records the file already answers,
asks the rest, including the ones that failed, and appends each answer as it arrives. Once the run
completes it rewrites the file in input order, keeping answered ids the input no longer has after
the rest unless `--prune` is given. Only the id is compared, so a record whose content changed but
whose id did not keeps its old answer. Every `--out` run holds a lock beside the file, so two cannot
run into the same file at once. Without `--id`, `--resume` counts the complete lines already in the file and
carries on from the next record.

```sh
onesie 'is this urgent' -i jsonl -j 8 --map '.body' --id '.id' --out answers.jsonl --resume < tickets.jsonl
```

### Gating

`--assert` is one boolean over the whole record, and its result is the exit code. It reads the same
field names `-o json` prints.

```sh
# run the command only if both answers are low
onesie --ask destructive='Does this command destroy data?' \
    --ask creds='Does this command read or send credentials?' \
    --assert 'destructive.value < 0.5 and creds.value < 0.5' \
    --state "$cmd" && eval "$cmd"

# a tool guard with a middle ground: 0 runs it, 1 blocks it, 7 asks
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

# a probability rather than the winner
onesie 'Which team?' --pick billing,technical,human --assert 'answer.p.human < 0.25' < ticket.txt

# every path is checked before any request, so a typo costs nothing
onesie --ask urgent='is this urgent' --assert 'urgnet.value < 0.5'
# onesie: --assert: unknown question 'urgnet'. Questions: urgent
```

A false assertion exits 1 and still prints the record, with `"assert": false` added. Add `-q` to
print nothing. Beside an assertion it only silences the output, and the assertion alone sets the
exit code.
Repeated `--assert` flags combine with `and`.

`--abstain-if` needs an assertion and is only checked for a record that answered and failed it.
When it holds the record is an unsure: one record exits 7, and json and values carry
`"abstain": true` instead of `"assert": false`. A stream keeps its most severe code, so a failed
record or a no outranks an unsure. Deny wins in the tool guard above, since one high answer fails
both expressions. A question file carries the same expression as `abstain_if`.

`min`, `max`, `sum` and `avg` take one or more numbers and nest, so `max(d.value, c.value) < 0.2`
says every risk is low in one term. `==` rarely matches the result of `sum` or `avg`, since sums of
fractions are inexact, so compare them with `>=` or `<=`. There are no arithmetic operators, so weighting
answers or reading a whole probability map is a job for `jq` on the record.

### Dry runs and replay

`--print-request` and `--print-questions` exit without calling the API, so they need no key and cost
nothing.

```sh
# the request body a command would send
echo "$ticket" | onesie --ask urgent='does this convey urgency' --print-request

# freeze a command line into a question file, then reuse it
onesie --ask urgent='does this convey urgency' --ask team='who owns this' --pick billing,platform \
    --print-questions > questions.yaml
echo "$ticket" | onesie -f questions.yaml -o values

# freeze a whole stream, then replay it exactly
onesie -i jsonl --ask urgent='does this convey urgency' --print-request < tickets.jsonl > frozen.jsonl
onesie -i request < frozen.jsonl > answers.jsonl
```

A question file carries questions, labels, policy and order, but not a model or a state.
`--print-questions` warns when it drops one.

### Keys and providers

```sh
onesie auth set                              # prompts, or reads the first line of stdin
pass show openrouter | onesie --provider openrouter auth set
onesie auth status                           # which provider and source, never the key
onesie auth test                             # checks the key, costs no tokens
```

| Provider | Key variable | Chosen with |
|----------|--------------|-------------|
| `typesafe`, the default | `TYPESAFE_API_KEY` | nothing |
| `openrouter` | `OPENROUTER_API_KEY` | `--provider openrouter` or `ONESIE_PROVIDER=openrouter` |

A key comes from `--api-key`, then the provider's variable, then the provider's entry in the
credential file, and no provider falls back to another's key.

`auth set` stores the key in the OS keychain when there is one: the macOS Keychain, the Secret
Service on Linux, or the Windows Credential Manager. The credential file then only records that
the key is there. Without a keychain, or with `auth set --file`, the key goes in the file itself.
On Linux the key reaches the Secret Service over the session bus unencrypted, readable only by
your own user.
The file lives under `$ONESIE_CONFIG_DIR`, `$XDG_CONFIG_HOME/onesie` or `~/.config/onesie`, is
written at mode `600`, and onesie refuses to read it when anyone else can reach it.

`jev-latest` works on both providers, but pinned model ids differ: TypeSafe uses `jev-1.13.0` and
OpenRouter `typesafe/jev-1.13`. On OpenRouter, `--usage` also reports the cost in USD.

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

- `--stats` writes a one line summary to stderr: requests, tokens, model, retries by status and time.
- `--list-models` shows what the account can ask, and `-m` picks one.
- `--base-url` points onesie at any server that speaks the System One API.
- `--retries`, `--timeout` and `--max-retry-after` bound how long a call can take.
- `onesie --help` lists every flag, and `onesie -V` prints the built in API limits.

## Contributing

See `CONTRIBUTING.md`.

## License

MIT. See `LICENSE`.
