# onesie reference

The details behind the [README](README.md). `onesie --help` lists every flag, and `onesie -V`
prints the built in limits.

## Gating

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
- `--abstain-if` and `--stop-on-assert` both need an assertion, from `--assert` or from the
  question file, and each exits 2 without one.
- `min`, `max`, `sum` and `avg` take numbers and nest. `==` rarely matches a `sum` or `avg`, so use
  `>=` or `<=`. There are no arithmetic operators, so weighting answers is a job for `jq`.
- A question file carries the same expressions as `assert` and `abstain_if`. `--print-request`
  checks a file's gate and then ignores it, so a gated file can be dry run. `--assert` or
  `--abstain-if` typed beside `--print-request` exits 2.

## Testing a gate

`--mock FILE`, or `ONESIE_MOCK=FILE` in the environment, answers from a file instead of the API. The
gate, the exit codes, every output mode, `--out` and `--resume` run as they do for real.

```sh
onesie -f examples/questions/shell-safety.yaml -q --mock answers.json --state 'rm -rf /'
ONESIE_MOCK=answers.json ./gate.sh
```

- The file takes one of two shapes. One JSON object keyed by question id answers every record, as
  in `{"destroys": 0.93, "secrets": 0.02, "network": 0.1}`, and may span several lines. A positional
  question has the id `answer`. Otherwise each line is one entry in the shape `-o json` writes, so the
  `--out` file of a real run replays. Lines match records by `id` under `--id`, and by position
  otherwise, so line 3 answers the third record whatever input line it came from. Blank lines are
  refused, apart from those at the end.
- A yes or no answer is a probability, or an object whose `value` is one. A pick or rate answer is a
  name, or an object with `value` and an optional `confidence`, which defaults to 1. A number whose
  text is a name is that name, so `4` rates `--rate 1,2,3,4,5`. A rate question from a request body
  takes its level index.
- The other keys `-o json` writes are accepted. A line's `model`, `usage`, `assert` and `abstain`
  are ignored, since the gate runs again. Inside an answer, `decision`, `fallback` and `legend` are
  ignored. A rate's `score` and `norm` are replayed, and so is a pick or rate's `p`, whose keys must
  be exactly the options or levels, whose values lie between 0 and 1, and whose most likely entry is
  the value. So a replay prints what the real run printed, and a gate that reads `p` decides the
  same way.
- An entry answers every question the run asks. The file is checked against the questions before
  any record, and an unknown question id, a missing answer, a value of the wrong shape or a name
  that is not an option exits 2, naming the line.
- `{"error": N}` with a status from 400 to 599 fails the record as that status does,
  `{"error": "timeout"}` as a timeout after `--timeout`, and `{"error": "connection"}` as a transport
  failure. An error object as `-o json` writes it replays by its `kind`. For one record, a 401, 402
  or 403 exits 3, a 5xx exits 4 and a timeout exits 5. In a stream a failed record is an error line
  and the run exits 6, apart from a 401, 402 or 403, which ends it with exit 3.
- A record the file does not answer exits 2 when the run reaches it, naming its input line and the
  missing entry. A replayed `kind: input` line answers nothing, so it counts as missing too. Output
  stops at that record, so it holds only the lines before it in input order, or under `--unordered`
  the lines that arrived before it. Under `--out --resume` the file holds the same lines, so a rerun
  with a full file asks that record and the ones after it.
- A mock run reads no key, opens no network, and ignores `--provider`, `ONESIE_PROVIDER`,
  `--base-url` and `-m`. It prints `model mock` wherever a model is shown, and `--usage` reports
  zero tokens.
- The `--out` fingerprint names the provider and model `mock`, so a real run refuses to resume a
  mock run's file and the other way round. The mock file's content is not part of it.
- `calibrate --mock` works, so a calibration can be tested too. `--mock` and `ONESIE_MOCK` are
  refused beside `--print-request`, `-i request`, `--list-models` and every `auth` subcommand. The
  flag wins over the variable, and an empty `ONESIE_MOCK` is ignored.

## Calibrating

`onesie calibrate --help` is the full reference, and its Labels paragraph lists what each shape
accepts as a label.

- `calibrate` is a subcommand name, so `onesie calibrate` runs it. To ask it as a question, write
  `onesie -- calibrate`.
- `--map` is required. Map only the text a person would read, since a `--map` that selects the
  label flatters the question.
- A question file's `assert`, `abstain_if`, `threshold`, `min_confidence` and `fallback` are
  ignored, since `calibrate` reports every cut. So a gated file calibrates as it stands. The same
  settings typed as flags exit 2.
- A pick or rate label matches an option or level by its text. A csv or tsv cell is text, so `4.0`
  does not match `--rate 1,2,3,4,5`, while a jsonl `4.0` is a number and does.
- `--resume` needs `--id`. A resumed `-o json` report differs from the fresh run's only in `asked`
  and `stored`.
- The answers file is `-o json` lines. A plain stream run can resume it, given the same questions,
  model, `-i`, `--map` and `--id`, with `-o json` and no gate or merge. Any other run is refused as
  changed.
- Under `--usage` the `-o json` report sums the tokens in `usage`, whose `records` counts the
  records it covers, failed ones that spent tokens included.
- `onesie -V` lists `max-calibrate-records`, the most records one run reads.

### Requirements

`--require` turns the report into a check. The report still prints, and the run exits 1 when a
requirement does not hold.

```sh
onesie calibrate -f examples/questions/prompt-injection.yaml -i jsonl --map .text --id .id \
    --label instructs=.instructs --label overrides=.overrides --label access=.access \
    --require 'instructs.catches >= 0.95' --require 'upper(overrides.false_alarms) <= 0.25 at 0.9' \
    < examples/data/prompt-injection.jsonl
```

- A requirement reads `[lower|upper](ID.MEASURE) OP NUMBER [at CUT]`. `MEASURE` is `catches`,
  `false_alarms`, `right_when_flagged` or `auc` for a yes/no question, `agreement` for pick and
  rate, and `within_one` for rate. `OP` is `>=`, `>`, `<=` or `<`. `NUMBER` and `CUT` are fractions
  between 0 and 1, so `95` exits 2 with `write 0.95, not 95`.
- `lower(...)` and `upper(...)` compare an end of the 95 percent interval instead of the value.
  `lower` takes only `>=` or `>`, and `upper` only `<=` or `<`. On a small sample they fail rather
  than pass, since 16 of 16 has a lower bound of 81 percent. `auc` has no interval.
- A yes/no measure other than `auc` is read at one cut. `at CUT` names it, and `at abstain` takes
  it from the `-f` file's `abstain_if`. Without `at`, the file's `assert` gives it, when it compares
  the question as `ID.value < X` or `ID.value >= X`. A gate that compares the question with `>` or
  `<=`, through a function such as `max()`, through `in`, or at two different cuts gives none, and
  the requirement then needs `at`. A missing cut exits 2 before any request, naming the question.
- `catches` is always the share of labelled yes records with `value >= cut`. A cut outside
  `--cuts` adds its row to that question's table only.
- A measure over no records, such as false alarms with no labelled no, does not hold, and says why.
- Requirements live only on the command line. A question file carries none, so a copy of a starter
  set calibrated on other data inherits no guarantee measured on ours.
- Each requirement that did not hold is named on stderr with its value, interval and cut. A failed
  record still exits 6, which wins over 1.
- The `-o json` report gains a `require` array, one entry per requirement:
  `{"expr":"...","held":false,"value":0.65,"interval":[0.43,0.82],"cut":0.25}`. `auc` has
  `"interval":null`, a pick or rate measure `"cut":null`, and a measure over no records
  `"value":null` with a `reason`.
- `--print-request` checks each requirement and its cut, then prints the requests as usual.

### Offline

`--offline`, with `--out` and `--resume`, answers every record from the answers file and asks
nothing. It needs no key, spends nothing and never rewrites the file, so a committed answers file
checks a gate in CI.

```sh
onesie calibrate -f examples/questions/shell-safety.yaml -i jsonl --map .command --id .id \
    -m jev-1.13.0 --provider typesafe \
    --label destroys=.destroys --label secrets=.secrets --label network=.network \
    --out examples/data/shell-safety.answers.jsonl --resume --offline \
    --require 'destroys.catches >= 1' --require 'destroys.false_alarms <= 0 at abstain' \
    < examples/data/shell-safety.jsonl
```

- A record the file does not answer exits 2, naming it. So does a stored error line, which a run
  without `--offline` would ask again.
- A missing answers file exits 2, naming it, and nothing is created.
- A file whose fingerprint does not match this run, because the questions, model, provider, `-i`,
  `--map` or `--id` changed, exits 2 with the advice to regenerate it. The model and provider are
  part of the match, so pin both with `-m` and `--provider`.
- `--offline` is refused beside `--mock` and `--print-request`.
- The starter sets in `examples/` commit their answers files, and `make check` runs each set
  offline with the requirements `examples/README.md` lists. `make examples-answers` regenerates
  them with a key.

## Streams

- A record that fails still prints a line with an `error` key, and the run exits 6.
  `--stop-on-error` ends at the first failure, and `--unordered` trades input order for throughput.
- `--map` is a jq expression whose result is the state. An object it builds has its keys sorted, and
  `onesie -V` lists the caps on its result. An empty or all whitespace string, an empty object and
  an empty array are refused as state before any request, whether they came from `--state`,
  `--state-file`, stdin, the `state` of a `-f` file, a stream line or `--map`. For one record it
  exits 2. In a stream each is an error line and the run goes on. `--skip-blank` drops blank lines,
  but a `{}`, `[]` or `""` line stays an error line. Under `-i request` a body is forwarded as it
  is, so its state is not checked.
- `--id` names each record on every output line. It runs one record at a time, so keep it a cheap
  lookup. Ids match by their text, so `7`, `7.0` and `"7"` are one id in jsonl.
- `--out` writes to a file with a fingerprint beside it, and a lock so two runs cannot share it. The
  fingerprint covers the questions and their policy flags, the provider, the model, the input and
  output modes, `--map`, `--id`, `--merge-key`, `--assert` and `--abstain-if`. It covers
  `--skip-blank` too when a resume counts lines, which is without `--id` on any input but csv and
  tsv.
- `--resume` with `--id` skips answered records and asks the rest, failed ones included. A finished
  run rewrites the file in input order and keeps answered ids the input no longer has, unless
  `--prune` drops them. A changed fingerprint refuses with exit 2. A record whose content changed
  but whose id did not keeps its old answer. Without `--id`, it carries on by line count. It does
  not ask a failed record again, apart from the `--stop-on-error` case below, so a skipped error
  line still counts as failed toward exit 6 and `--stats`, under `-i request` too. Either way, a
  skipped record keeps its stored `--assert` outcome, so it counts toward the exit code and
  `--stats` as if it were judged again.
- Under `--stop-on-assert`, a skipped false assertion ends the run where a fresh run would stop.
  Under `--stop-on-error`, a resume by line count whose last line is an error line drops that line,
  asks its record again and carries on. A run stopped by `--stop-on-error` leaves exactly that, so
  a rerun makes progress once the cause is gone. An error line with more lines after it was written
  without `--stop-on-error`, so the resume stops there before any request, with the code a fresh
  run exited with, rebuilt from the line's kind and status. A csv or tsv row keeps only the
  message, so there it exits 2. Either way, pass `--id` or drop `--stop-on-error` to carry on. The
  same holds under `-i request`. With `--id` the failed record is asked again, and stops the run
  only if it fails again.
- Raw output keeps no outcome, so `--resume` refuses `-o raw` and `-r` with exit 2. Use `-o values`
  or `-o json`.
- onesie writes csv cells exactly as they are, the ones `--merge` carries over included. A cell
  from untrusted input that starts with `=`, `+`, `-` or `@` can act as a formula when the file is
  opened in a spreadsheet.
- `-o markdown`, or `-o md`, is for people. A table cannot be read back into answers, so it refuses
  `--resume`, `--merge`, `-r` and `-q` with exit 2, while `--out` alone writes the table to a file.
  A run that ends early, by an interrupt, an abort or a stop flag, keeps the rows that finished, and
  its summary reads `stopped after` the records it counted.

## Keys and providers

```sh
onesie auth set                                    # prompts, or reads the key alone from stdin
pass show openrouter | head -n 1 | onesie --provider openrouter auth set
onesie auth status                                 # which provider and source, never the key
onesie auth test                                   # checks the key, costs no tokens
```

| Provider | Key variable | Chosen with |
|----------|--------------|-------------|
| `typesafe`, the default | `TYPESAFE_API_KEY` | nothing |
| `openrouter` | `OPENROUTER_API_KEY` | `--provider openrouter` or `ONESIE_PROVIDER=openrouter` |

- A key comes from the provider's variable, then the credential file. No provider falls back to
  another's key.
- `auth set` stores the key in the OS keychain when there is one, and otherwise in a file at mode
  `600` under `$ONESIE_CONFIG_DIR`, `$XDG_CONFIG_HOME/onesie` or `~/.config/onesie`. onesie refuses
  a file anyone else can reach. On Linux the key reaches the Secret Service over the session bus
  unencrypted, readable only by your own user.
- `jev-latest` works on both providers, but pinned ids differ: `jev-1.13.0` on TypeSafe,
  `typesafe/jev-1.13` on OpenRouter. On OpenRouter, `--usage` also reports the cost. A 200 whose
  answers onesie cannot use still spent its tokens, so under `--usage` its json error record
  carries them too, and `--stats` counts them. `-o values`, `-o raw`, `-r`, `-o csv` and `-o tsv`
  have no place for `--usage`, and `-q` writes nothing, so they all refuse it with exit 2.

## Exit codes

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

calibrate uses 0, 2, 3, 6 and 130, and 1 only under `--require`, when a requirement did not hold. A consumer that stops
reading, as `head` does, is not an error.

## More

- `--stats` writes a one line summary to stderr: requests, tokens, model, retries and time.
- `--list-models` shows what the account can ask, and `-m` picks one.
- `--base-url` points onesie at any server that speaks the System One API. A plain http URL to a
  host other than localhost prints a warning, and a redirect is refused rather than followed with
  the key.
- `-f NAME` loads `NAME.yaml`, `NAME.yml` or `NAME.json` from the nearest `.onesie/questions`, then
  from `questions` in the config dir, and `onesie questions` lists every name it finds. A question
  file is refused with exit 2 when it uses a YAML alias or is larger than `max-question-file-bytes`.
- `--retries`, `--timeout` and `--max-retry-after` bound how long a call can take.
- `onesie --help` lists every flag, and `onesie -V` prints the built in limits.
