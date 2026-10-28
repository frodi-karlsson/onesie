# onesie reference

The details behind the [README](README.md). `onesie --help` lists every flag, and `onesie -V`
prints the built in limits.

## Asking

- A yes or no question answers with how likely the yes is. `-r` prints only the answer. Without it,
  the table for a pick or a rate also shows the model's confidence and the probability of each
  option or level. Under `--threshold`, `-o table` and `-o markdown` write the decision as `yes`
  or `no` beside the probability.
- Every output format writes each number of an answer as `-o json` does. A number the provider
  sent prints as sent, never rounded. A number onesie computes itself, such as `norm` or a mock's
  `p` and `score`, is rounded to four places. A printed number is the one a gate compares.
- `--pick` and `--rate` split their names on the comma, or on `--sep`, and trim the spaces around
  each one, so `--pick 'yes, no'` names `yes` and `no`. A repeated `--pick` or `--rate` adds its
  names after the earlier ones. An empty name exits 2. A question file's names are kept as
  written, and an empty or all whitespace one exits 2 too.
- An empty or all whitespace question exits 2 before any request, the way an empty state does,
  whether it came from the positional argument, `--ask`, a question file's `ask` or the
  `instructions` of a `-f` request body. So does a null or missing question, one that is a number
  or `true` or `false`, and an empty mapping or sequence. A `-f` body question that carries `ask`
  in place of `instructions` exits 2 and says so. An `-i request` body is forwarded as it is and not
  checked, apart from a `model` that is missing, empty, all whitespace or null, which onesie fills
  in.
- A pick or rate answer carries `confidence`, a number from 0 to 1 the API computes from `p`.
  `--min-confidence` cuts on it. A yes or no answer carries no confidence. A response that leaves
  `confidence` out reads as 0, so `--min-confidence` falls back and a gate on `confidence` does not
  pass.
- Every answer is checked against the question asked before it is printed, gated or cached, on a
  plain run, in a stream, under `-q` and `--assert`, in `calibrate` and when the cache is read. A
  yes or no value, each `p` and each `confidence` lies in [0,1], a pick names one of its options, a
  rate's `score` lies between 0 and its top level index, the keys of `p` are exactly the options,
  or the level indexes from 0 for a rate, and `p` adds up to 1 within 0.02. An answer of the wrong
  type fails too. Any of these exits 4 with `200 response could not be used:` and names the
  question and the value, as in `question 'urgent' answered 1.5, which lies outside [0,1]`. In a
  stream the record is an error line of kind `response`. Its tokens still count toward `--usage`
  and `--stats`, it is never stored in the cache, and it is not retried.
- A rate answer also carries `score` and `norm`. `score` is the level index averaged over `p`, with
  0 for the lowest level, so it can land between levels. `norm` is `score` over the top index, from
  0 to 1, so `2.02` on four levels is a `norm` of `0.67`.
- A question that is one bare word close to a subcommand name exits 2 and names the closest
  subcommand, as `onesie questoins` does. A word after `--` is asked, as in `onesie -- questoins`,
  and so is a question of more than one word or one beside `--ask`.
- A question that starts with a dash needs `--` before it, with every flag placed first, as in
  `onesie -o json -- '-is this urgent'`. When such a question holds a space and was read as flags,
  the error says `was read as flags`. A flag typed after `--` is part of the question, so the
  error says `flags go before --`.
- `--desc KEY=TEXT` describes one option, one level, or `yes` or `no`, which steers the answer. A
  rate needs every level described, or none.
- `--box` makes a question a box, answered with `left`, `top`, `right` and `bottom`, each a fraction
  of the image from its top left corner, and a `confidence`. It needs an image, as in Images below,
  and a provider that takes it, TypeSafe or OpenRouter, as on `typesafe/jev-1.14`. Cloudflare,
  Levanto and Berget refuse it before any request. On TypeSafe and OpenRouter the model must serve
  it too: one that does not answers 400 `Invalid request`, and onesie then names `-m jev-preview`,
  which does. It takes no value, so `--box=true` exits 2 as `--box=1` does, and no `--desc`,
  `--threshold`, `--min-confidence` or `--fallback`.
  `-r` prints the four edges in that order, and `-o csv` writes them as four columns, `ID_left`,
  `ID_top`, `ID_right` and `ID_bottom`.
  A box always returns a box, even when the target is not there, so pair it with a yes or no
  question that asks whether it is.

## Gating

`--assert` is one boolean over the record, and it sets the exit code. It reads the field names
`-o json` prints, and every path is checked before any request. So is every string compared with a
pick or rate `value` by `==`, `!=` or `in`, which must name one of its options or levels:

```sh
onesie --ask urgent='is this urgent' --assert 'urgnet.value < 0.5'
# onesie: --assert: unknown question 'urgnet'. Questions: urgent
```

- A question id may hold any character, but a path reaches it by its bare name only when it is
  letters, digits and `_`, starts with a letter or `_`, and is not one of `and`, `or`, `not`, `in`,
  `true` and `false`. Write any other id in brackets and double quotes, as in
  `["needs review"].value` or `["a-b"].value`, and an option or level the same way after `p`, as in
  `team.p["on call"]`. An id written bare that only the bracket form reaches exits 2 and shows the
  bracket form. So does `a_b.value` written for the id `a-b`.
- `true` and `false` are expressions of their own, so `--assert true` always holds and
  `--abstain-if false` never does. A question file's `assert: true` reads the same. A constant gate
  gives `calibrate` no cut, so a `--require` that reads its cut from one says so and needs
  `at CUT`.
- A false assertion exits 1 and still prints the record with `"assert": false`. `-q` prints nothing
  and leaves the exit code to the assertion. Repeated flags combine with `and`.
- `--abstain-if` is checked only when the assertion fails. When it holds, the record is an unsure:
  exit 7, and `"abstain": true` in place of `"assert": false`.
- `--abstain-if` and `--stop-on-assert` both need an assertion, from `--assert` or from the
  question file, and each exits 2 without one.
- A box is gated on its edges or its confidence, as in `mug.confidence >= 0.6`, since it carries no
  policy. `-q` on a box needs `--assert`.
- `--fallback` on a pick or rate may be any value, so low confidence can route outside the options,
  as `human` does.
- `min`, `max`, `sum` and `avg` take numbers and nest. `==` rarely matches a `sum` or `avg`, so use
  `>=` or `<=`. There are no arithmetic operators, so weighting answers is a job for `jq`.
- A question file carries the same expressions as `assert` and `abstain_if`. `--print-request`
  checks a file's gate and then ignores it, so a gated file can be dry run. `--assert` or
  `--abstain-if` typed beside `--print-request` exits 2.

## Testing a gate

`--mock FILE`, or `ONESIE_MOCK=FILE` in the environment, answers from a file instead of the API. The
gate, the exit codes, every output mode, `--out` and `--resume` run as they do for real.

```sh
onesie -f shell-safety -q --mock answers.json --state 'rm -rf /'
ONESIE_MOCK=answers.json ./gate.sh
```

- The file takes one of two shapes. One JSON object keyed by question id answers every record, as
  in `{"destroys": 0.93, "secrets": 0.02, "network": 0.1}`, and may span several lines. A positional
  question has the id `answer`. Otherwise each line is one entry in the shape `-o json` writes, so
  the `--out` file of a real run replays. Lines match records by `id` under `--id`, and by position
  otherwise, so line 3 answers the third record whatever input line it came from. Blank lines are
  refused, apart from those at the end.
- A file of one line whose object carries a key `-o json` writes on a line, `id`, `line`, `model`,
  `usage`, `assert`, `abstain` or an `error` object, is the line shape. So the `--out` file of a one
  record run answers record 1 alone, and record 2 exits 2 with a message naming the key. An object
  over several lines that carries one of those keys is refused.
- A yes or no answer is a probability, or an object whose `value` is one. A pick or rate answer is a
  name, or an object with `value` and an optional `confidence`, which defaults to 1. With no `p`,
  onesie fills one in where the value is the most likely and the others share the rest evenly. A
  rate's `score` is the level index averaged over `p`, unless the entry gives one. A number whose
  text is a name is that name, so `4` rates `--rate 1,2,3,4,5`. A rate question from a request body
  takes its level index.
- The other keys `-o json` writes are accepted. A line's `model`, `usage`, `line`, `assert` and
  `abstain` are ignored, since the gate runs again. Inside an answer, `decision`, `fallback` and
  `legend` are ignored. A rate's `score` and `norm` are replayed, and so is a pick or rate's `p`,
  whose keys must be exactly the options or levels, whose values lie between 0 and 1 and add up to
  1 within 0.02, and whose most likely entry is the value. So a replay prints the answers the real
  run printed, and a gate that reads `p` decides the same way. The `model` it prints is the one the
  mock run resolved, not the one the record holds.
- An entry answers every question the run asks. The file is checked against the questions before
  any record, and an unknown question id, a missing answer, a value of the wrong shape or a name
  that is not an option exits 2, naming the line.
- Each entry is what the server sends, and onesie's own client reads it, so a 408, 429 or 5xx
  status, a timeout and a dropped connection are retried as a live one is, at once, with no wait.
  `{"error": N}` with a status from 400 to 599 fails the record as that status does with no body,
  as in `onesie: 503 status code, no body`, `{"error": "timeout"}` as a timeout the connection
  reports, as in `onesie: request to mock.invalid:443 timed out`, and `{"error": "connection"}` as
  a transport failure, as in `onesie: could not reach mock.invalid: mock connection error`. An
  error object as `-o json` writes it replays by its `kind`, and a `response` one is a 200 with no
  answers, which fails as
  `200 response could not be used: the response is not a System One result, since it has no answers`.
  An `http` one may carry the JSON `body` the server sends, as in
  `{"error": {"kind": "http", "status": 400, "body": {"detail": "Unknown model: jev-latest"}}}`,
  and then fails and advises as a live response with that body does. A string `body` is the raw
  response text, so `"body": "<html>nope</html>"` reads as a live HTML page does, with the advice
  for a body that is not JSON. A `null` one is no body. An `auth` one fails as the provider's own
  refusal of the key: a 401, 402 or 403 on any provider, or a 400 on cloudflare, which names a
  refused token by its code. Any other status in an `auth` one, or a 400 on another provider, exits
  2 when the file loads. For one record, a 401, 402, 403 or `auth` exits 3, a 5xx or a `response`
  exits 4 and a timeout exits 5. In a stream a failed record is an error line and the run exits 6,
  apart from a 401, 402, 403 or `auth`, which ends it with exit 3.
- A record the file does not answer exits 2 when the run reaches it, naming its input line and the
  missing entry. A replayed `kind: input` line answers nothing, so it counts as missing too. Output
  stops at that record, so it holds only the lines before it in input order, or under `--unordered`
  the lines that arrived before it. Under `--out --resume` the file holds the same lines, so a rerun
  with a full file asks that record and the ones after it.
- A mock run chooses its profile and resolves the provider and the model as a real run does, and
  refuses what the real run would before it answers from the file. So a misspelled `--provider`, a
  `--profile` that does not exist, a Box question on cloudflare or a body openrouter cannot take
  fails as it would for real. It reads `profiles.json` unless `--provider` or `ONESIE_PROVIDER`
  chooses the run. A test can point `ONESIE_CONFIG_DIR` at a fixture of its own, at mode `600`,
  whose profile holds a dummy `api_key`, since every profile must hold a key. A mock run uses no
  key, opens no network, and ignores `--base-url` and the cloudflare account. It prints the model
  the run resolved wherever a model is shown, and `--usage` reports zero tokens. `--stats` counts
  the attempts and retries as a live run does, with zero tokens and no cost, as in
  `2 records answered from the mock, 2 questions asked, 0 in / 0 out tokens, model jev-latest, 2 attempts, 10s/attempt, 0s`.
  A record the file fails counts as failed, never as answered from the mock.
- The `--out` fingerprint marks the run as a mock, so a real run refuses to resume a mock run's
  file and the other way round. The mock file's content is not part of it.
- `calibrate --mock` works, so a calibration can be tested too. Its `-o json` report carries
  `"mocked":true`. An entry may also answer the questions `calibrate` skips, so one mock file for a
  whole question file serves any set of labels. `--mock` and `ONESIE_MOCK` are refused beside
  `--print-request`, `-i request`, `--list-models` and every `profile` subcommand. The flag wins
  over the variable, and an empty `ONESIE_MOCK` is ignored.

## Calibrating

`onesie calibrate --help` is the full reference, and its Labels paragraph lists what each shape
accepts as a label.

- `calibrate` is a subcommand name, so `onesie calibrate` runs it. To ask it as a question, write
  `onesie -- calibrate`.
- `--map` is required. Map only the text a person would read, since a `--map` that selects the
  label flatters the question.
- A question with no `--label` is skipped. It is left out of the requests and the report, and one
  line on stderr names every skipped question, such as
  `onesie: skipping 'team' and 'tone', which have no --label`. So `-f` calibrates one question of
  a file with one `--label`.
- At least one question needs a `--label`, or the run exits 2. A `--label` naming a question the
  run does not have exits 2 as well, so a mistyped name is never read as a skip. So does a
  `--require` that reads a skipped question.
- A question file's `assert`, `abstain_if`, `threshold`, `min_confidence` and `fallback` are
  ignored, since `calibrate` reports every cut. So a gated file calibrates as it stands. The same
  settings typed as flags exit 2.
- A pick or rate label matches an option or level by its text. A csv or tsv cell is text, so `4.0`
  does not match `--rate 1,2,3,4,5`, while a jsonl `4.0` is a number and does.
- A pick or rate table has a row per name. `found` is the share of the records labelled with that
  name that were picked as it, and `right when picked` the share of the records picked as it that
  carry it as their label. Each shows the count, the share and its 95 percent interval.
- `--resume` needs `--id`. A resumed `-o json` report differs from the fresh run's only in `asked`
  and `stored`.
- The answers file is `-o json` lines. A plain stream run can resume it, given the same questions,
  model, `-i`, `--map` and `--id`, with `-o json` and no gate or merge. Any other run is refused as
  changed.
- The answers file and its fingerprint cover the questions a run asks, so a run that skips
  questions writes a file of the labelled ones, and a resume with other labels is refused as
  changed. A resume into a file that answers every question keeps it whole instead: the skipped
  questions are still asked of each record the file lacks, and the report leaves them out.
- Under `--usage` the `-o json` report sums the tokens in `usage`, whose `records` counts the
  records it covers, failed ones that spent tokens included.
- `onesie -V` lists `max-calibrate-records`, the most records one run reads.
- Each worst miss shows its id and the start of its mapped text, one line of about 40 characters
  with control characters dropped. In the `-o json` report each miss carries that line as `text`.
- The model is not deterministic, so an answer near a cut can land on either side of it from one run
  to the next. `--abstain-if` sends that band to a person, and calibrate's worst misses show which
  records sit in it.

### Requirements

`--require` turns the report into a check. The report still prints, and the run exits 1 when a
requirement does not hold.

```sh
onesie calibrate -f prompt-injection -i jsonl --map .text --id .id \
    --label instructs=.instructs --label overrides=.overrides --label access=.access \
    --require 'instructs.catches >= 0.95' --require 'upper(overrides.false_alarms) <= 0.25 at 0.9' \
    < examples/data/prompt-injection.jsonl
```

- A requirement reads `[lower|upper](ID.MEASURE) OP NUMBER [at CUT]`. `MEASURE` is `catches`,
  `false_alarms`, `right_when_flagged` or `auc` for a yes or no question, `agreement` for pick and
  rate, and `within_one` for rate. `OP` is `>=`, `>`, `<=` or `<`. `NUMBER` and `CUT` are fractions
  between 0 and 1, so `95` exits 2 with `write 0.95, not 95`.
- `lower(...)` and `upper(...)` compare an end of the 95 percent interval instead of the value.
  `lower` takes only `>=` or `>`, and `upper` only `<=` or `<`. On a small sample they fail rather
  than pass, since 16 of 16 has a lower bound of 81 percent. `auc` has no interval.
- A yes or no measure other than `auc` is read at one cut. `at CUT` names it, and `at abstain` takes
  it from the `-f` file's `abstain_if`. Without `at`, the file's `assert` gives it, when it compares
  the question as `ID.value < X` or `ID.value >= X`. A gate that compares the question with `>` or
  `<=`, through a function such as `max()`, through `in`, or at two different cuts gives none, and
  the requirement then needs `at`. A missing cut exits 2 before any request, naming the question.
- `catches` is always the share of labelled yes records with `value >= cut`. A cut outside
  `--cuts` adds its row to that question's table only.
- A measure over no records, such as false alarms with no labelled no, does not hold, and says why.
- Requirements live only on the command line. A question file carries none.
- Each requirement that did not hold is named on stderr with the four place numbers it compared,
  its value, interval and cut, as in
  `'u.catches >= 0.9' did not hold: 3/4 is 0.75, interval 0.3006 to 0.9544, at cut 0.50`, or
  `AUC 0.9993` for `auc`. A failed record still exits 6, which wins over 1.
- The `-o json` report gains a `require` array, one entry per requirement:
  `{"expr":"u.catches >= 0.9 at 0.5","held":false,"value":0.6667,"interval":[0.2077,0.9385],"cut":0.5}`,
  for 2 of 3 caught. A requirement compares the four place number the `-o json` report prints. A
  rate, interval end or `auc` that is not exactly 0 or 1 never prints as 0 or 1, so 29999 of 30000
  is `0.9999`. The table report shows each rate and interval end as a whole percent between 1 and
  99, so `1/1000` reads `1% 1-1%` and `4/4` reads `99% 51-99%`, and `auc` to 2 places. `auc` has
  `"interval":null`, a pick or rate measure `"cut":null`, and a measure over no records
  `"value":null` with a `reason`.
- `--print-request` checks each requirement and its cut, then prints the requests as usual.

### Offline

`--offline`, with `--out` and `--resume`, answers every record from the answers file and asks
nothing. It needs no key, spends nothing and never rewrites the file, so a committed answers file
checks a gate in CI.

```sh
onesie calibrate -f shell-safety -i jsonl --map .command --id .id \
    -m jev-1.13.0 --provider typesafe \
    --label destroys=.destroys --label secrets=.secrets --label network=.network \
    --out examples/data/shell-safety.answers.jsonl --resume --offline \
    --require 'destroys.catches >= 1' --require 'destroys.false_alarms <= 0 at abstain' \
    < examples/data/shell-safety.jsonl
```

- The line on stderr reads `onesie: reading N records from FILE`. A run that asks writes
  `onesie: asking N of M records`.
- A record the file does not answer exits 2, naming it. So does a stored error line, which a run
  without `--offline` would ask again.
- A missing answers file exits 2, naming it, and nothing is created.
- A file that answers every question reads under any set of labels, so one committed answers file
  checks each question of a starter set on its own.
- A file whose fingerprint does not match this run exits 2, naming the cause, such as the parts of
  the run that changed, or a fingerprint a newer onesie wrote. To regenerate the file, run
  again without `--offline` and `--resume`. The model and provider are part of the match, so pin
  both with `-m` and `--provider`.
- `--offline` is refused beside `--mock`, `--print-request` and `--prune`.
- The starter sets in `examples/` commit their answers files, and `examples/README.md` lists the
  requirements each one holds.

## Streams

- A record that fails prints a line with an `error` key, and the run exits 6. `--stop-on-error`
  ends at the first failure, and `--unordered` trades input order for throughput.
- The `error` object holds `kind`, `status` and `message`. Its `kind` is `input` for a record that
  never became a request, `transport` for a connection that failed or timed out, `response` for a
  2xx response onesie could not use, `auth` for a key or account the provider refused, and `http`
  for any other status outside 2xx. An `auth` failure a run meets ends the run with exit 3, and so
  does a stored one a `--stop-on-error` resume stops at. A resume by line count without
  `--stop-on-error` skips a stored `auth` line as it skips any failed one, so it exits 6.
- A run `--stop-on-error` ends exits with the failed record's own code, as in
  [Exit codes](#exit-codes). A record that never became a request, such as a line onesie could not
  read or an empty state, has no code of its own, so it exits 6 as it does without the flag, and its
  error line is the only report.
- Use `--map` to drop the fields that do not bear on the question, such as timestamps and ids,
  since they add noise to the answer. `--map 'del(.ts, .order_id)'` sends the rest of a record.
- `--map` is a jq expression whose result is the state. An object it builds has its keys sorted. The
  result may nest `max-map-depth` levels deep and encode to `max-line-bytes`, both of which
  `onesie -V` lists. An empty or all whitespace string, an empty object and an empty array are
  refused as state before any request, whether they came from `--state`, `--state-file`, stdin, the
  `state` of a `-f` file, a stream line or `--map`. For one record it exits 2. In a stream each is
  an error line and the run goes on. `--skip-blank` drops blank lines, but a `{}`, `[]` or `""` line
  stays an error line. Under `-i request` a body is forwarded as it is, so its state is not checked.
- Under `-i lines`, each `-o json` and `-o values` line carries `line`, the record's input line
  number from 1. A blank line `--skip-blank` drops still counts, so the numbers match the file.
  Under `--merge` it sits in the answers object, beside `model`. `-o csv`, `-o tsv`, `-o markdown`
  and raw output leave it out, and `line` is a reserved question id under `-i lines`.
- A resume with `--id` keeps each stored line as written, so a skipped record keeps the line
  number it was answered at, as it keeps its answer.
- `--id` names each record on every output line. It runs one record at a time, so keep it a cheap
  lookup. Ids match by their text, so `7`, `7.0` and `"7"` are one id in jsonl.
- Under `-i csv` or `-i tsv`, a run where every record's `--id` was null says on stderr after the
  run that the delimiter may not match the file, since `-i tsv` reads a comma separated file as one
  column. `calibrate` says so beside the first record's error.
- `--out` writes to a file with a fingerprint beside it, and a lock so two runs cannot share it.
- `--resume` refuses with exit 2 when a change would alter what a record is asked or what its line
  holds: the questions or their policy flags, the provider, the model, the images, long context,
  `--mock`, the input or output format, `--map`, `--id`, `--merge-key`, `--assert` or
  `--abstain-if`. `--skip-blank` counts too when a resume counts lines, which is without `--id` on
  any input but csv and tsv. The refusal names each of them that changed, as in
  `the model and -o changed since answers.jsonl was written`.
- The fingerprint keeps one sum for each of them. A fingerprint that holds a single sum resumes when
  nothing changed, and the run then writes one sum for each part. A change there reads
  `the questions, model or flags changed`, since a single sum cannot tell which. A fingerprint in
  the format of an older or a newer onesie is refused with exit 2, as
  `an older onesie wrote answers.jsonl.onesie, so this one cannot tell which run wrote answers.jsonl`.
  A file another onesie version wrote resumes when its fingerprint is in a format this one reads,
  apart from a csv or tsv file without the fallback columns, below. `-j`, `--timeout`, `--retries`,
  `--cache` and `--unordered` can change between runs.
- `--resume` with `--id` skips answered records and asks the rest, failed ones included. A finished
  run rewrites the file in input order and keeps answered ids missing from the input, unless
  `--prune` drops them. A record whose content changed but whose id did not keeps its old answer.
  Without `--id`, it carries on by line count. It does not ask a failed record again, apart from the
  `--stop-on-error` case below, so a skipped error line counts as failed toward exit 6 and
  `--stats`, under `-i request` too. Either way, a skipped record keeps its stored `--assert`
  outcome, so it counts toward the exit code and `--stats` as if it were judged again.
- Under `--stop-on-assert`, a skipped false assertion ends the run where a fresh run would stop.
  Under `--stop-on-error`, a resume by line count whose last line is an error line drops that line,
  asks its record again and carries on. A run stopped by `--stop-on-error` leaves exactly that, so
  a rerun makes progress once the cause is gone. An error line with more lines after it was written
  without `--stop-on-error`, so the resume stops there before any request and exits with the failed
  record's own code, as a fresh run under `--stop-on-error` would. A csv or tsv row keeps only the
  message, so there it exits 2. Either way, pass `--id` or drop `--stop-on-error` to carry on. The
  same holds under `-i request`. With `--id` the failed record is asked again, and stops the run
  only if it fails again.
- Records whose request is identical are asked once in a run. Identical means the same state after
  `--map`, the same questions and the same model, and under `--mock` the same answer from the file.
  A JSON line's whitespace does not matter, but its key order does. A record the mock file does not
  answer is never shared. Every record still gets its own line, with its own `id` and its own input
  under `--merge`, in input order or as answers arrive under `--unordered`. `--stats` counts the
  saved requests, as in `12 records, 3 deduplicated, 9 requests`. Under `--usage` only the first
  line written for a request carries its tokens, and its other lines carry
  `{"input_tokens":0,"output_tokens":0}`, so a sum over the output stays true. An error line that
  carried no usage still carries none. A failed request fails every record that shares it, and is
  not asked again within the run. With `--id`, `--resume` judges each line on its own, so a later
  run asks those records again. Without `--id` it carries on by line count, which asks no failed
  record again. A run holds up to `max-dedup-requests` distinct requests, which `onesie -V` lists.
  Past that it says so once on stderr, still shares the requests it holds and asks every new one.
  Only a stream deduplicates: calibrate, `-i request` and `--print-request` ask or print every
  record. `--no-dedup` asks every record.
- Raw output keeps no outcome, so `--resume` refuses `-o raw` and `-r` with exit 2. Use `-o values`
  or `-o json`.
- `-o csv` and `-o tsv` write one column per question, named by its id, then an `assert` column
  under a gate and an `error` column. A question with a fallback gets an `ID_fallback` column right
  after its own, as in `team,team_fallback`. It holds `low_confidence` or `error` when the fallback
  replaced the answer, the same reason `-o json` writes as `fallback`, and is empty otherwise. Under
  `--merge` the input columns come first, and one named like an output column exits 2. `--resume`
  refuses a csv or tsv file without the fallback columns, and says to regenerate it or write to a
  new `--out`.
- onesie writes csv cells exactly as they are, the ones `--merge` carries over included. A cell
  from untrusted input that starts with `=`, `+`, `-` or `@` can act as a formula when the file is
  opened in a spreadsheet.
- `-o markdown`, or `-o md`, is for people. A table cannot be read back into answers, so it refuses
  `--resume`, `--merge`, `-r` and `-q` with exit 2, while `--out` alone writes the table to a file.
  A run that ends early, by an interrupt, an abort or a stop flag, keeps the rows that finished, and
  its summary reads `stopped after` the records it counted.

## Images

```sh
onesie --image photo=examples/images/square.png 'is there a red square in `photo`'
onesie --image photo=examples/images/square.png --ask where='the red square in `photo`' --box -m jev-preview -r
ls photos/*.jpg | onesie -i lines --image-field photo 'is there a dog in `photo`' -o values
```

- `--image NAME=PATH` puts a PNG or JPEG in the state under `NAME`, and a question names it in
  backticks. It is repeatable.
- For one record, stdin is read beside `--image` only through `--state -`, so text joins the image
  only from `--state`, `--state-file` or `--state -`. Text goes under the key `text`, a JSON object
  gains the image keys, and a key clash or an array state exits 2. `-i text` or `-i json` beside
  `--image` with neither `--state` nor `--state-file` exits 2 and names `--state -`.
- A stream, `-i jsonl`, `-i lines`, `-i csv` or `-i tsv`, reads its records from stdin as usual
  beside `--image`, and adds the same image to every record.
- `--image-field NAME` reads each record's `NAME` as a path, in a stream or in `calibrate`. Under
  `-i lines` the line is the path, and only one `--image-field` is taken. A missing or unreadable
  file is an input error line, and the run goes on and exits 6.
- onesie checks the format, the file size and the sides of each image before any request, and
  `onesie -V` lists the limits as `max-image-bytes`, `image-side`, `image-pixels` and
  `max-body-bytes`. An `--image` refused there exits 2 and names the limit, and an `--image-field`
  one is an input error line. The API ignores EXIF rotation, so rotate a phone photo upright first.
- The cache, deduplication and `--resume` see the same image file as the same record.
- A request with an image waits up to 60 seconds per attempt, unless `--timeout` says otherwise.
  `onesie -V` lists it as `asset-attempt-timeout`, beside `attempt-timeout`. Lower `-j` for large
  images, since each record in flight holds its image bytes.
- `--map` runs before the images are attached, so it sees the text state and the paths, never the
  references. With `--image` and no text there is nothing to map.
- In a stream, `--merge` writes the input record as it was read, so an `--image-field` holds its
  path. For one record whose state was built from images, it writes that state, references included.
- `--print-request` writes onesie's request format: the System One body plus the settings that
  change the answer, each image as base64 under `assets`. `-i request` reads lines up to
  `max-request-line-bytes`, so a printed body replays. A printed body holding `long_context` is
  refused by TypeSafe when sent to it directly, outside `-i request`.
- A printed body names its model, the provider's default unless `-m` gave one, so it replays on a
  provider that serves that model. To print for another provider, pass `-m`.
- `--print-request` refuses what a send to the run's provider refuses before it prints anything,
  such as an image or a Box question on a provider that takes none. A body printed with no state
  holds only the questions, and gets only that provider refusal.
- Under `-i request` onesie fills a blank `model` and checks each body as a send would, before it
  sends anything: the provider must take what the body asks for, each image must pass the image
  limits, `long_context` must be true or false, and no question may pass `max-choice-options` or
  `max-score-levels`. A body that fails is an error line of kind `input`, and nothing is sent for
  it. `--print-request` refuses the same bodies with the same error lines. It also checks each 200
  that comes back before printing it. A response that is not JSON, that is not a System One result
  with an `answers` object, or whose answers do not decode, is an error line of kind `response`
  with `200 response could not be used:`, and the run exits 6. A response that is not JSON also
  names the base URL to check. When the body's `questions` read, each answer is also checked
  against its question's type, options and levels as on any other run, so a missing answer or a
  value out of its range fails the same way. A body whose questions do not read gets only the
  first checks. The tokens of a refused response still count toward `--stats`.
- A malformed image body, such as one whose asset nothing references, is refused before any
  request on every provider, with the same message.
- Providers read images differently:
  - TypeSafe takes images and Box questions.
  - OpenRouter takes each image where its reference stands. An image named in several places is
    sent once per place, and counts toward the body size each time.
  - Cloudflare takes images and no Box questions. Clef takes up to 4 images, and a question names
    an image as it does on TypeSafe.
  - Levanto and Berget take no images or Box questions, and a run that would send either exits 2
    before any request. Under `-i request` such a body is an error line instead.
- onesie's image limits, which `onesie -V` lists, are TypeSafe's. They also apply on Cloudflare,
  where they are not measured for Clef.
- `--image`, `--image-field` and `--box` are refused under `-i request`, whose bodies carry their
  own state and questions.
- Images and Box are experimental, and less reliable than text.

## Caching

```sh
onesie --cache -f shell-safety --state 'rm -rf /'
ONESIE_CACHE=1 ./gate.sh   # every run in the script reads and fills the cache
onesie cache               # 212 entries, 40 of them expiring since their model is an alias, 1.4 MB in DIR
onesie cache clear
```

`--cache`, or `ONESIE_CACHE=1`, stores each successful response on disk and answers a repeated
request from it. Without either, nothing is stored and nothing is read. `ONESIE_CACHE` takes the
values the flag takes, such as `1`, `true`, `0` and `false`, and anything else exits 2 in every run
and `calibrate`, `--mock` and `--print-request` included.
`--cache=false` turns the cache off for one run under `ONESIE_CACHE=1`, so that run asks the API
again.

- A stored answer is used only for the same request. A change to the state, the questions, the
  model, long context, the provider, the base URL or any image asks the API again. `--timeout`,
  `--retries`, `--assert`, `--abstain-if`, the policy flags and the output flags never change the
  answer, so a run that differs only in those still hits.
- A hit prints what the live run printed, byte for byte, except that `--usage` reports
  `{"input_tokens":0,"output_tokens":0}` and no cost. `--stats` counts a hit as a record and not a
  request, as in `1 record, 1 cached, 0 requests`. Gates, exit codes, `--out` and `--resume` work as
  they do without the cache.
- Only a successful response is stored. An error, a timeout or a response onesie cannot use is asked
  again next time.
- On the `typesafe` provider, a model name that ends in a version, such as `jev-1.13.0`, is pinned,
  and its answers live until they are evicted. Every other model is an alias, such as `jev-latest`,
  and so is every model on any other provider, `typesafe/jev-1.13` on OpenRouter and
  `Qwen/Qwen3.5-2B` on Berget and `levanto-sage-v1.4` on Levanto included. An alias's answers expire
  after 24 hours, since the model behind it can move. `ONESIE_CACHE_TTL` sets that lifetime as a
  duration, such as `90m` or `72h`, and `0` stops reading and storing answers for an alias. A
  negative or malformed value exits 2, in every run whether or not it uses the cache, in
  `onesie cache` and in `onesie cache clear`. A short lifetime in one run never deletes answers
  other runs still read.
- The cache lives in `$ONESIE_CACHE_DIR`, or else `$XDG_CACHE_HOME/onesie`, or else:
  - `~/Library/Caches/onesie` on macOS
  - `%LOCALAPPDATA%\onesie` on Windows, or `AppData\Local\onesie` under the home directory when
    `LOCALAPPDATA` is unset
  - `~/.cache/onesie` on Linux and every other system
- Its files are mode `600` in directories of mode `700`. `--cache` refuses a cache directory others
  can reach with exit 2, before any request, apart from on Windows. onesie writes a `CACHEDIR.TAG`
  file, so backup tools skip the cache. `--cache` refuses an existing directory with no
  `CACHEDIR.TAG` that holds anything else with exit 2, before any request, and names the file it
  found. The files a desktop leaves in a folder, `.DS_Store`, `Thumbs.db`, `desktop.ini` and any
  starting with `._`, do not count. onesie counts, evicts and clears only its own entries, and
  parallel runs can share one cache.
- The cache holds about `max-cache-bytes`, 100 MB, which `onesie -V` lists beside `cache-ttl`. Past
  that it evicts the least recently used answers first, and a hit counts as a use.
- `--mock` answers from its file and never reads or fills the cache. `--print-request` sends
  nothing, `calibrate --offline` asks nothing, and a run that `--resume` finds fully answered asks
  nothing, so none of them opens the cache. `-i request` and `--list-models` never go through it, so
  `--cache` beside either exits 2. `--cache=false` beside them is fine, and `ONESIE_CACHE=1` is
  ignored there.
- A cached run still needs a key, even though a hit sends nothing. Under `--cache`, the no key
  message says so.
- Any other cache error in a run, such as a full disk or a home directory onesie cannot write, turns
  the cache off for the rest of that run with one warning on stderr, and the run carries on against
  the API.
- Deduplication covers repeats within one run, the cache covers repeats across runs, and `--resume`
  skips the ids an `--out` file already answers. A group of identical records asks the cache once,
  so a group whose first record hits counts one cached record and the rest deduplicated. A cached
  line in `--out` carries zero usage like any other hit. Turning the cache on or off never refuses
  a resume, so a file written with the cache resumes without it, and the other way round.
  `calibrate --cache` works too.
- `onesie cache` prints how many entries the cache holds, how many of them expire since their model
  is an alias, their size and the directory, or `0 entries in DIR` when there is none. It creates
  nothing, and names on stderr a directory `--cache` refuses, with the reason `--cache` gives.
  `onesie cache clear` removes every entry, keeps the directory and its `CACHEDIR.TAG`, and prints
  how many it removed, or `no cache in DIR, so nothing was removed` when the directory does not
  exist. It refuses a directory with no `CACHEDIR.TAG` with exit 2, so a mistyped
  `ONESIE_CACHE_DIR` never empties a directory onesie did not make. Both ignore `ONESIE_MOCK` and
  `ONESIE_CACHE`, and neither takes `--mock`. A one word question `cache` needs `--ask`, or `--`
  before it.
- Cached answers sit on disk until they expire, are evicted or are cleared. An entry holds the
  answers and the model, never the state. Its name is a sha256 of the request, so anyone who can
  read the directory and guess a request can confirm it was asked.

## Install script

The install script puts the binary in `~/.local/bin`. `ONESIE_INSTALL_DIR` names another directory,
and `ONESIE_VERSION` a release other than the latest. It checks the download against the release's
`checksums.txt`, and against its build provenance when `gh` is logged in.

The darwin binaries are signed and notarized, so an archive downloaded from the releases page in a
browser runs too.

## Profiles and providers

```sh
onesie profile create work --provider openrouter   # prompts, or reads the key alone from stdin
pass show openrouter | head -n 1 | onesie profile create work --provider openrouter
onesie profile list                                # every profile, with * on the one a run uses
onesie profile show                                # what a run would use, never the key
onesie profile test work                           # checks the key
onesie profile use work                            # makes work the default
```

A profile is a named connection: a provider, its key in the OS keychain or in `profiles.json`, and
optionally a model and a base URL or a Cloudflare account id. Two profiles may share a provider, so
a work key and a personal key for OpenRouter sit side by side. `--provider` runs without a profile,
on the provider's key variable alone.

| Provider | Key variable | Default model |
|----------|--------------|---------------|
| `typesafe` | `TYPESAFE_API_KEY` | `jev-latest` |
| `openrouter` | `OPENROUTER_API_KEY` | `jev-latest` |
| `berget` | `BERGET_API_KEY` | `systemone` |
| `levanto` | `LEVANTO_API_KEY` | `sage-latest` |
| `cloudflare` | `CLOUDFLARE_API_TOKEN` | `typesafe/jev` |

### Commands

| Command | What it does |
|---------|--------------|
| `profile create NAME --provider P [--model M] [--base-url U] [--account-id A] [--file]` | stores a new profile and reads its key. The first profile becomes the default |
| `profile update NAME [--provider P] [--model M] [--base-url U] [--account-id A] [--key] [--file]` | changes only the members named, and an empty value clears one, as in `--model ""`. `--key` reads a new key. `--provider` needs `--key`, since a key belongs to one provider, and `--file` needs `--key` too |
| `profile delete NAME` | removes the profile and its keychain item. Deleting the default leaves no default, and deleting the last profile removes `profiles.json` |
| `profile use NAME` | makes the profile the default |
| `profile list` | lists each profile's provider, key store and model, with `*` on the one a run would use now. When a run would use none, a stderr line says why, as in `onesie: active: ONESIE_PROVIDER=openrouter, no profile` |
| `profile show [NAME]` | prints what a run would use now, or what the named profile holds, never the key |
| `profile test [NAME]` | lists the provider's models with the key, which costs no tokens, and prints `profile`, `provider`, `key` and `models` lines |

- A name is 1 to 64 lowercase letters, digits, `-` or `_`, does not start with `-`, and is not
  `none`. There is no rename, so delete a profile and create it again.
- The key is read from a hidden prompt on a terminal, and otherwise from the first line of stdin,
  which must hold the key alone. A second line that is not blank exits 2, so pipe a password manager
  entry through `head -n 1`. The key is never a flag, since a flag lands in shell history.
- `update` with a member set to the value it already has, or cleared when it is already unset,
  leaves the file alone, says so on stderr and exits 0. `update` with no member at all exits 2.
- `create` and `update --key` put the key in the OS keychain, or in `profiles.json` with `--file`. A
  keychain that does not answer in time exits 3 and names `--file`. Any other keychain error stores
  the key in the file instead, with a warning. On Linux the key reaches the Secret Service over the
  session bus unencrypted, readable only by your own user.
- `create` and `update` check the rules below, the base URL and the account id before they read the
  key, so a typo costs nothing. A bad value from a flag is named by its flag, as in
  `--account-id must be 32 hexadecimal characters`.
- `show` never reads the keychain or prints a key. `key:` is `keychain` or `file`, with
  `, ends in XXXX` when the key has 16 characters or more, `env` and the key variable for a run
  without a profile, or `none`. A converted keychain key shows no ending until
  `onesie profile update NAME --key`. `chosen by:` is `--profile`, `ONESIE_PROFILE`, `the default`,
  `--provider`, `ONESIE_PROVIDER` or `nothing`, and `not active` for a named profile a run would not
  use. `model:` and `base_url:` give the value a run would use, in the order a run picks it, then
  where it comes from. The model comes from `profile`, `env TYPESAFE_DEFAULT_MODEL` or `the
  provider's default`, as in `model: jev-latest, the provider's default`. The base URL comes from
  `the profile's base_url`, `TYPESAFE_BASE_URL`, `the profile's account_id`, `CLOUDFLARE_ACCOUNT_ID`
  or `the provider's default`, the words a run's messages use. `base_url:` is `none` on cloudflare
  with no account, and a base URL a run would refuse exits 2 with the run's message. An
  `allow_http: needed` line follows it when the base URL is plain http to another host, since every
  run then needs `--allow-http`. `account:` names `account_id`, `base_url`, `CLOUDFLARE_ACCOUNT_ID`
  or `none`. `show` exits 2 when `key` or `account` is `none`, as a real run would, and prints the
  hint a run prints on stderr. It exits 2 with the run's message when `CLOUDFLARE_ACCOUNT_ID` is
  not 32 hexadecimal characters, and after the report lines when `ONESIE_CACHE`,
  `ONESIE_LONG_CONTEXT` or `ONESIE_CACHE_TTL` holds a value a run refuses.
- A key the server refuses exits 3, on a run or on `test`. On a profile the message names
  `onesie profile update NAME --key`, and without one it names the provider's key variable. A run
  with no key and no profile for its provider names
  `onesie profile create NAME --provider PROVIDER`.
- `test` proves the key on every provider. On berget it also asks one tiny question with the
  profile's model, which spends a few tokens. On openrouter it also reads `/v1/key` under the base
  URL, which costs nothing, so a proxy set as an openrouter base URL must serve `/v1/key` too.
- `--profile` exits 2 beside `create`, `update`, `delete`, `use` and `list`, and beside `show` or
  `test` given a name. So does `--provider`, apart from `create` and `update`, where it is the
  profile's own provider. `show` and `test` with no name take both as a run does. Every `profile`
  command refuses `ONESIE_MOCK` and takes no `--mock`.
- `profile` is a subcommand name, so a one word question `profile` needs `--ask`, or `--` before it.

### Choosing a profile

| Choice | Strength |
|--------|----------|
| `--profile NAME` or `--provider NAME` | flag |
| `ONESIE_PROFILE` or `ONESIE_PROVIDER` | environment |
| the file's `default`, which `profile use` sets | file |

- The strongest choice wins, so a flag beats a variable, and a variable beats the default.
- Two choices of the same strength exit 2, naming both. So `--profile` with `--provider` exits 2,
  and so do `ONESIE_PROFILE` and `ONESIE_PROVIDER` set together.
- An empty or whitespace only variable counts as unset. A flag that names a value, such as a file,
  a model or a profile, exits 2 on an empty or whitespace only one, so `--mock "$UNSET"` never
  reaches the API. `profile update` is the exception, where an empty value clears a member.
- `--provider` or `ONESIE_PROVIDER` runs without a profile.
- With no choice and no profiles, a run uses no profile and the `typesafe` provider.
- With no choice, and profiles but no default, a run exits 2 and names `onesie profile use NAME`,
  `--profile NAME` and `--provider NAME`.
- A profile that does not exist exits 2, naming the profiles there are. A `--provider` that names a
  profile rather than a provider exits 2 with a hint to pass `--profile`. A `--profile`,
  `ONESIE_PROFILE` or `profile show`, `test` or `use` name that is a provider and no profile exits 2
  with a hint to pass `--provider`.

### What a run uses

| Value | With a profile | Without a profile |
|-------|----------------|-------------------|
| key | the profile's key. The provider's key variable is not read | the provider's key variable |
| model | `-m`, then the `model` of a `-f` request body, then the profile's `model`, then `TYPESAFE_DEFAULT_MODEL` on typesafe, then the provider's default | `-m`, then the `model` of a `-f` request body, then `TYPESAFE_DEFAULT_MODEL` on typesafe, then the provider's default |
| base_url | `--base-url`, then the profile's `base_url`, then `TYPESAFE_BASE_URL` on typesafe, then the provider's own | `--base-url`, then `TYPESAFE_BASE_URL` on typesafe, then the provider's own |
| account_id, on cloudflare | the profile's `account_id`, then `CLOUDFLARE_ACCOUNT_ID` | `CLOUDFLARE_ACCOUNT_ID` |

- A flag beats the profile, the profile beats the environment, and the environment beats the built
  in default. No provider falls back to another's key.
- A request body that names no model takes the profile's `model`, then the order above.
- On cloudflare the account counts only when no base URL is chosen, since a Cloudflare base URL
  holds the account. A profile's `account_id` comes right after `--base-url` and the profile's
  `base_url`, so it beats every variable, and `CLOUDFLARE_ACCOUNT_ID` comes after them. A cloudflare
  run with no account exits 2 and names `CLOUDFLARE_ACCOUNT_ID`. On a profile it also names
  `onesie profile update NAME --account-id ACCOUNT_ID`, and without one
  `onesie profile create NAME --provider cloudflare --account-id ACCOUNT_ID`.
- A run that `--provider` or `ONESIE_PROVIDER` chooses, and that succeeds, never opens
  `profiles.json`, so CI that names its provider does not depend on any file on the machine. Only a
  run that then fails for a missing key or an unknown provider reads it, to name a profile in the
  hint. Under `ONESIE_PROVIDER` with no profile for that provider, the hint names the key variable
  and says a stored profile is not used while the variable is set.

| Run | Reads `profiles.json` |
|-----|-----------------------|
| `profile show`, `profile list` and `profile test` | always, whatever chooses |
| `--print-questions` and `--print-schema` | never |
| any other run, `--print-request` and `--mock` included | yes, so a `profiles.json` others can reach exits 3 there too |

- Reading `profiles.json` never opens the keychain. Only a run that sends a request reads a keychain
  key.

### profiles.json

```json
{
  "default": "work",
  "profiles": {
    "work": {
      "provider": "openrouter",
      "store": "keychain",
      "account": "work@3c9a1f2b7e10",
      "key_end": "3f9a",
      "model": "typesafe/jev"
    },
    "cf": {
      "provider": "cloudflare",
      "api_key": "YOUR_CLOUDFLARE_API_TOKEN",
      "account_id": "0123456789abcdef0123456789abcdef",
      "model": "clef-flash"
    }
  }
}
```

The format is stable, and the file may be written by hand. It lives in the config dir:
`$ONESIE_CONFIG_DIR`, then `$XDG_CONFIG_HOME/onesie`, then `%APPDATA%\onesie` on Windows, then
`~/.config/onesie`.

| Member | Meaning |
|--------|---------|
| `default` | the profile a run uses when nothing else chooses one. Optional |
| `profiles` | name to profile. May be empty |
| `provider` | `typesafe`, `openrouter`, `berget`, `levanto` or `cloudflare`. Required |
| `api_key` | the key, held in the file |
| `store`, `account` | `keychain` and the keychain item's name, for a key held in the keychain |
| `key_end` | the last four characters of a keychain key of 16 characters or more, so `profile show` tells two keys apart without reading the keychain |
| `base_url` | the API root the profile's runs use |
| `account_id` | cloudflare only, the account, 32 hexadecimal characters |
| `model` | the model a run uses when it names none |

- A file that does not parse or breaks a rule exits 2, naming the rule and the profile, never a key.
  A file cut short or holding a syntax error is `not valid JSON`, and valid JSON of another shape
  is `not a JSON object of profiles`. A `credentials.json` waiting to be converted is checked the
  same way.
  Each key is held in exactly one place, a non empty `api_key` or `store` set to `keychain` with an
  `account`. An unknown member, such as a misspelled `modle`, is refused, not dropped. `account_id`
  appears only on cloudflare, and a profile holds `base_url` or `account_id`, not both. `default`
  names a profile that exists.
- onesie writes the file atomically at mode `600`, and makes a directory it creates at mode `700`.
  On every system but Windows, a file whose mode gives the group or others any access exits 3. The
  file is capped at 1 MiB.

### The keychain

- A key goes to the OS keychain under the service `onesie`: the macOS Keychain, the Secret Service
  on Linux, or the Windows Credential Manager. The item is named `PROFILE@HASH`, where the hash
  stands for the config dir, so two config dirs never share one item. The profile's `account`
  holds the name. A converted profile keeps its item name until `onesie profile update NAME --key`.
- `profile delete` and `profile update --key` delete the replaced item after the file is saved,
  unless another profile names the same item. A failure warns and names the item left behind.

### Converting credentials.json

When the config dir holds `credentials.json` and no `profiles.json`, the first command that reads
the config dir converts it once:

- Each provider entry becomes a profile named after its provider. A key in the file stays in the
  file. A key in the keychain keeps its item, which is neither read nor moved, so no keychain prompt
  appears. A stored base URL stays a `base_url`.
- `typesafe` becomes the default when there is a typesafe entry. Otherwise no profile is the
  default, so a run with no choice exits 2, even with `TYPESAFE_API_KEY` set, and names
  `onesie profile use NAME`, `--profile NAME` and `--provider NAME`.
- onesie writes `profiles.json`, removes `credentials.json` and says so on stderr, as in
  `onesie: converted credentials.json into profiles.json, with profiles 'levanto' and 'typesafe'. 'typesafe' is the default`.
  Under `ONESIE_PROVIDER` a second line says the run uses no profile, and how to use a stored key.
- A `credentials.json` whose mode is too open exits 3, and one whose entries cannot become profiles
  exits 2, naming the entry. Nothing converts either way.
- When `profiles.json` cannot be written, as in a read only config dir, the run uses the converted
  profiles for this run only, warns, and converts again next time.

### Servers and CI

One connection needs no file. Set the provider's key variable and pass `--provider`, or set
`ONESIE_PROVIDER`:

```sh
onesie --provider openrouter -f shell-safety -q --state "$cmd"   # OPENROUTER_API_KEY is set
```

Several connections make profiles on the spot from the key variables, in a config dir of the job's
own, and choose one per run with `--profile`:

```sh
export ONESIE_CONFIG_DIR="$RUNNER_TEMP/onesie"
onesie profile create prod --provider openrouter --file <<< "$OPENROUTER_API_KEY"
onesie profile create cf --provider cloudflare --account-id "$CLOUDFLARE_ACCOUNT_ID" --file <<< "$CLOUDFLARE_API_TOKEN"
onesie --profile cf -f shell-safety -q --state "$cmd"
```

- `--file` keeps the key out of a keychain the runner may not have. `<<<` is a bash here string, so
  under a plain `sh` pipe the key in with `printf '%s\n' "$OPENROUTER_API_KEY" |` instead.
- A server mounts a `profiles.json` with `api_key` inline, at a mode such as `0400`, and points
  `ONESIE_CONFIG_DIR` at its directory.
- No variable holds the whole file.

### Provider notes

- The Cloudflare token needs `Account` > `Workers AI` > `Read`. Its default model, `typesafe/jev`,
  is TypeSafe's Jev through AI Gateway, billed from AI Gateway credits, with a 32,000 token
  context and no choice of version. `clef`, `clef-flash` and `clef-omni` are Cloudflare's own
  models, run on the free daily Workers AI allowance, with contexts of 65,536, 24,576 and 65,536
  tokens. `--list-models` lists them all.
- Cloudflare takes no long context, so `--long-context` there exits 2 before any request.
- A printed body is onesie's, not Cloudflare's envelope, so it replays through `-i request` with
  `--provider cloudflare`, not by curl.
- `jev-latest` works on typesafe and openrouter, but pinned ids differ: `jev-1.13.0` on TypeSafe,
  `typesafe/jev-1.13` on OpenRouter. Berget serves its own models and not `jev-latest`. Its default
  is the `systemone` alias, and `--list-models` there lists only the System One models, each with
  its aliases. Levanto also serves its own models and not `jev-latest`. Its default is the
  `sage-latest` alias. On OpenRouter, `--usage` also reports the cost, which the `-o table` header
  and `--stats` show too. A 200 whose answers onesie cannot use still spent its tokens, so under
  `--usage` its json error record carries them too, and `--stats` counts them. `-o values`,
  `-o raw`, `-r`, `-o csv` and `-o tsv` have no place for `--usage`, and `-q` writes nothing, so
  they all refuse it with exit 2.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | answered |
| 1 | a false `--assert`, or under `-q` alone the policy did not accept the answer |
| 2 | usage or validation error, including a missing key, or a plain http base URL to another host without `--allow-http` |
| 3 | the key was refused, the account is out of credits, the keychain failed, or `profiles.json` or `credentials.json` is exposed |
| 4 | the server did not answer after retries, or it answered 200 with a response onesie could not use, which is never retried |
| 5 | transport error or timeout |
| 6 | a stream finished with one or more failed records, or `--stop-on-error` stopped it at a record that never became a request |
| 7 | the gate could not decide: the assertion failed and `--abstain-if` held |
| 130 | interrupted |

calibrate uses 0, 2, 3, 6 and 130, and 1 only under `--require`, when a requirement did not hold.
A consumer that stops reading, as `head` does, is not an error. `onesie --help` lists this
table, and `onesie calibrate --help` lists the codes calibrate uses.

## Environment

| Variable | What it does |
|----------|--------------|
| `TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`, `BERGET_API_KEY`, `LEVANTO_API_KEY`, `CLOUDFLARE_API_TOKEN` | the key for each provider on a run without a profile, as in Profiles and providers |
| `CLOUDFLARE_ACCOUNT_ID` | the Cloudflare account when no flag or profile names one, as in Profiles and providers |
| `TYPESAFE_DEFAULT_MODEL` | the model on typesafe when neither `-m`, a request body nor the profile names one |
| `TYPESAFE_BASE_URL` | the API root on typesafe when neither `--base-url` nor the profile's `base_url` names one |
| `ONESIE_PROFILE` | the profile a run uses when neither `--profile` nor `--provider` is given |
| `ONESIE_PROVIDER` | runs without a profile on this provider when neither `--profile` nor `--provider` is given. Set beside `ONESIE_PROFILE`, it exits 2 |
| `ONESIE_CONFIG_DIR` | where `profiles.json` and the saved question files live |
| `ONESIE_MOCK` | a file to answer from, as `--mock` |
| `ONESIE_CACHE` | `1` turns the response cache on, as `--cache` |
| `ONESIE_CACHE_DIR` | where the response cache lives |
| `ONESIE_CACHE_TTL` | how long a cached answer for a model alias lives, `24h` unless set |
| `ONESIE_LONG_CONTEXT` | `1` or `true` raises the context limits, as `--long-context`. It takes the values the flag takes, and anything else exits 2 |

## More

- `--stats` writes a one line summary to stderr, as in
  `6 records, 2 skipped by --resume, 1 cached, 3 requests, 3 questions asked, 412 in / 9 out tokens, model jev-latest, 4 attempts (1 retry: 429×1), 10s/attempt, 2.1s`.
  The records lead with every one the run read, and the counts after them are parts of that whole:
  those `--resume` skipped, the cache answered, deduplication shared, that failed, and the requests
  sent. A run of one record that became one request leads with `1 request` alone. The questions
  are those the requests asked, so a cache hit asks none. The tokens and the cost OpenRouter reports
  are summed over the requests, and the per attempt timeout is left out of a run that made no
  attempt.
- A notice onesie writes to stderr starts with `onesie: `, and a warning with `onesie: warning: `.
  The `--stats` summary and the `API key: ` prompt keep their own form.
- `--list-models` shows what the account can ask, and `-m` picks one. Each line holds a model, its
  description and its release date as `YYYY-MM-DD`, which is blank when the provider gives none.
- `--base-url` points onesie at any server that speaks the System One API. A redirect is refused
  rather than followed with the key.
- A plain `http://` base URL to a host other than this machine exits 2 before any request, since
  the key would cross the network unencrypted. `--allow-http` sends it anyway, with a warning. The
  rule holds whether the URL comes from `--base-url`, `TYPESAFE_BASE_URL` or a profile's
  `base_url`, and `profile test` follows it too. `profile create` and `profile update` refuse such
  a `--base-url` unless given `--allow-http`, and the file does not record that choice, so every
  run on the profile needs `--allow-http` as well, and `profile show` prints `allow_http: needed`.
  There is no variable for it.
- onesie adds the API path itself, such as `/v1/systemone`, so a base URL that already ends in the
  start of that path, such as `/v1`, prints one warning naming where it came from, the segment and
  the URL questions go to. `--list-models` calls its own path, which the warning does not name. It
  is not refused, since a proxy may use that prefix. On cloudflare the path starts with `/run`
  instead, so a cloudflare base URL ending in `/v1` prints no warning, and one ending in `/run`
  does.
- A request that never reaches the server exits 5 and names the host and the innermost cause, as
  in `onesie: could not reach api.example.com: no such host`. The host keeps its port, and the
  message never holds a URL, a query or a proxy address.
- An error response whose body is not JSON, such as the HTML page a web server sends for a wrong
  path, prints its status and says the body is not JSON, as in
  `onesie: 405 Method Not Allowed, with a response body that is not JSON. Check that the base URL points at the System One API`.
  A page is left out, and only the first line of a plain text body is kept. Only a 404 or a 405
  adds the hint to check the base URL. A body sent as JSON that holds only a string is read as a
  plain text body is: its first line is kept, a page is left out, and an empty one prints the
  status text.
- A base URL that holds a user or a password, or any `@`, `?` or `#`, or that does not parse, exits
  2 before any request, whether it comes from `--base-url`, `TYPESAFE_BASE_URL` or a profile, so a
  key never travels in a URL or reaches a message. So does one with no host or with a scheme other
  than http or https. The message names where the URL came from, `--base-url`, `TYPESAFE_BASE_URL`
  or `the profile's base_url`, and shows at most its scheme and host, as in
  `onesie: --base-url https:// names no host`. `profile create` and `profile update` refuse one too,
  and `profile show` refuses a profile that holds one. `--print-request` sends nothing and does not
  check it.
- `-f VALUE` reads VALUE as a file when one exists at that path. Otherwise a bare name loads
  `NAME.yaml`, `NAME.yml` or `NAME.json` from the nearest `.onesie/questions`, then from `questions`
  in the config dir, and last takes a starter set built into onesie: `moderation`, `personal-data`,
  `prompt-injection` or `shell-safety`. A saved file wins over a built in set of the same name, so a
  copy to edit is printed elsewhere first, as in
  `onesie -f shell-safety --print-questions > shell-safety.yaml`, then moved with
  `mv shell-safety.yaml .onesie/questions/`. A redirect straight into `.onesie/questions` empties
  the file before onesie runs, and `-f` then finds that empty file ahead of the built in set.
  `onesie questions` lists every name `-f` finds, with a built in set shown as `built in` and an
  empty saved file marked `empty`, or `"empty":true` under `-o json`. A question file is refused
  with exit 2 when it uses a YAML alias or when it is larger than `max-question-file-bytes`, and
  every error in a file names its path. A file that holds no question, such as an empty one, `{}` or
  only comments, adds nothing beside `--ask`, and on its own it exits 2, naming the file. One that
  hides a built in set always exits 2, naming how to reach the set.
- For one record, onesie reads the state from stdin when stdin is not a terminal and no other
  source gave one. When nothing arrives there for 2 seconds, it prints
  `onesie: waiting for text on stdin. Pass --state, or close stdin.` to stderr once and keeps
  waiting. Under `--state -` it prints `onesie: waiting for text on stdin. Close stdin when the text
  is complete.` instead. A stream never prints it, since a slow producer is normal there and
  `--state` does not apply to a stream.
- `-f` also takes a request body, as `--print-request` writes it, a positional question's body keyed
  `answer` included. For one record, a body that holds a `state` sends that state, and onesie then
  never reads stdin. `--state` or `--state-file` beside such a body exits 2, naming both.
  `-i request` refuses both flags the same way, since each body there holds its own state. A stream
  and `calibrate` read their records from stdin as usual and send each one as its state, so they use
  the body only for its questions, and say on stderr that its state is ignored. A body's `assets`
  are sent with it. When its state is ignored, an asset only that state named is dropped, and the
  warning counts it.
- `--print-schema` prints the JSON Schema for a question file. The first line `--print-questions`
  writes is `# yaml-language-server: $schema=URL`, which an editor running the YAML language server
  reads, such as VS Code with the Red Hat YAML extension, to check each key and value as you type.
  The URL names the release tag the binary was built from, and `main` for any other build. A JSON
  question file names the schema in a top level `"$schema"` key, which onesie accepts and ignores,
  so `$schema` is a reserved question id. The schema accepts any request body as it is.
- An editor flags a `threshold` outside 0 to 1 or a pick with one option. It accepts a file that
  holds only a gate, an `abstain_if` with no `assert`, or a `min_confidence` with no `fallback`,
  since a flag can supply the rest, and onesie refuses such a file at run time when the flag is
  missing.
- `--retries`, `--timeout` and `--max-retry-after` bound how long a call can take.
- `--long-context`, or `ONESIE_LONG_CONTEXT=1`, raises the context limits to 262k tokens, which is
  slower and less accurate. It is written into a printed body as `long_context`, so a replay keeps
  it, and a `-f` body's `long_context: true` turns it on with no flag. A body's `false` does not
  turn off the flag or the variable. The flag wins over the variable, so `--long-context=false`
  turns it off for one run under `ONESIE_LONG_CONTEXT=1`, as `--cache=false` does for the cache.
  It does not turn off a body's `long_context: true`, from `-f` or under `-i request`, since the
  body asks for it itself. Set that key to `false` in the body, or remove it, to run without it.
  `-i request` follows the same order for each body it replays, and under `--print-request` it
  writes `"long_context":true` into a body the flag or the variable turns it on for. A provider
  that takes no long context refuses the run with exit 2, `--print-request` included, and when
  `ONESIE_LONG_CONTEXT` turned it on, the message says so. It is experimental.
- `onesie --help` lists every flag, and `onesie -V` prints the version, the commit, the build date
  and the built in limits. `onesie -V -o json` and `onesie version -o json` print them as one JSON
  object, each limit a string as `-V` prints it. Any other `-o` beside either exits 2.
- A flag onesie does not know exits 2, naming the closest one when one is near. A flag that belongs
  to a run, given to a subcommand, says so. `-V` takes no question, so a word beside it exits 2.
  `onesie help calibrate` prints a subcommand's help, and a topic that names no subcommand exits 2.
