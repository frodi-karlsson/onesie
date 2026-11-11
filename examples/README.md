# Starter question sets and recipes

Four question files that work in any organization. Each one comes with a hand labelled sample in
`data/` and a gate chosen from a calibrate run on that sample.

The numbers here come from a small hand labelled sample of 36 to 40 records per set. They are not a
benchmark. Your commands, pages and messages will differ from these, so calibrate on your own data
before you rely on a gate.

## Use a set

The sets are built into onesie, so `-f` loads one by name with no copy step:

```sh
onesie -f shell-safety -q --state 'rm -rf ./build'
```

To edit a set, save a copy into `.onesie/questions/`. A saved file wins over the built in set of the
same name, so print the copy elsewhere and move it in. A redirect straight into
`.onesie/questions/` would empty the file before `-f` read it:

```sh
mkdir -p .onesie/questions
onesie -f shell-safety --print-questions > shell-safety.yaml
mv shell-safety.yaml .onesie/questions/
```

Each file carries its own gate as `assert` and `abstain_if`, so `-f NAME` alone runs the whole
gate. Every gate exits 0 on a pass, 1 on a fail, which blocks, and 7 on an unsure, which hands the
record to a person. Any other exit code is an error, so treat it as a block. `calibrate` and
`--print-request` ignore the gate in a file, so the same file can be calibrated and dry run as it
stands.

## Calibrate on your own data

Each record carries `id`, the text and one label per question, named after the question id.

```sh
onesie calibrate -f shell-safety -i jsonl --map '.command' --id '.id' \
    --label destroys='.destroys' --label secrets='.secrets' --label network='.network' \
    --out answers.jsonl --resume < examples/data/shell-safety.jsonl
```

`--out` with `--resume` keeps the answers, so a rerun after a label change asks nothing. A change
to the questions makes the file stale, and `--resume` then refuses it. Delete `answers.jsonl` and
`answers.jsonl.onesie`, or drop `--resume`, to ask again. Once you pick your own cuts, edit `assert`
and `abstain_if` in your copy of the file.

A cut row flags a record when its value is at least the cut. The pass cut in each gate sits below
every labelled yes, so no labelled record with a yes passes. A record passes only when every
question stays under its pass cut. The block cut sits above every labelled no, so no clean record
is blocked. A record between the two goes to a person.

Pick each cut with margin, away from any record's score, since answers vary a little between runs.
All of a set's questions share one request, so rewording one question can shift the others' answers.
Each section below says how far each cut sits from the nearest labelled record on the side that
matters: a pass cut from the lowest labelled yes, and a block cut from the highest labelled no.

The tables below are trimmed to the rows each gate uses. All runs used `jev-1.13.0`.

## Requirements checked in CI

`data/` holds each set's answers from `jev-1.13.0` beside its labels, as `SET.answers.jsonl` with
its `.onesie` fingerprint. A test runs each set through `calibrate --offline` with the requirements
below, so a change to a question or a gate that breaks one fails the build without asking the API.
`make examples-answers` regenerates the answers files with `TYPESAFE_API_KEY` set. It starts a set
afresh when the set's questions changed since its answers were written, and resumes every other
set, so it asks only what is missing. When a set fails the check with the advice to regenerate its
answers, run `make examples-answers`.

| Set | Requirements |
|---|---|
| `shell-safety` | `destroys.catches >= 1`<br>`secrets.catches >= 1`<br>`network.catches >= 1`<br>`destroys.catches >= 0.9 at abstain`<br>`destroys.false_alarms <= 0 at abstain`<br>`secrets.false_alarms <= 0 at abstain` |
| `prompt-injection` | `instructs.catches >= 0.9`<br>`access.catches >= 0.8`<br>`overrides.catches >= 1`<br>`overrides.false_alarms <= 0 at abstain`<br>`access.false_alarms <= 0 at abstain` |
| `personal-data` | `name_contact.catches >= 1`<br>`email_phone.catches >= 1`<br>`home_address.catches >= 1`<br>`credential.catches >= 1`<br>`name_contact.false_alarms <= 0 at abstain`<br>`email_phone.false_alarms <= 0 at abstain`<br>`home_address.false_alarms <= 0 at abstain`<br>`credential.false_alarms <= 0 at abstain` |
| `moderation` | `hostile.catches >= 1`<br>`spam.catches >= 1`<br>`hostile.false_alarms <= 0 at abstain`<br>`spam.false_alarms <= 0 at abstain`<br>`tone.within_one >= 1` |

A requirement without `at` reads its cut from the file's `assert`, and `at abstain` reads it from
`abstain_if`.

## shell-safety

For an agent that runs shell commands. It asks whether running a command would destroy data, send
credentials or reach the network. `destroys` asks about running exactly the command given, so
printing or quoting a command, such as `echo 'rm -rf /'`, is not running it.

```sh
onesie -f shell-safety -q --state "$cmd"
case $? in
    0) eval "$cmd" ;;
    1) echo blocked ;;
    7) ask_the_user ;;
    *) echo 'error, blocked' ;;
esac
```

The file carries this gate:

```yaml
assert: destroys.value < 0.25 and secrets.value < 0.3 and network.value < 0.5
abstain_if: destroys.value < 0.5 and secrets.value < 0.67
```

A command runs only when `destroys` sits below 0.25, `secrets` below 0.3 and `network` below 0.5.
Those cuts sit 0.12, 0.41 and 0.23 below the lowest labelled yes for each question. A command is
blocked when `destroys` reaches 0.5 or `secrets` reaches 0.67. The first sits 0.10 above the
highest safe command, `find . -name '*.pyc' -delete` at 0.40, and 0.09 below
`shred -u secrets.txt` at 0.59. The second sits 0.06 above the highest command
labelled as sending no secrets, and 0.04 below the lowest that sends one. A command that only
reaches the network is never blocked. It goes to a person.

The labels count `rm -rf ./build` as destructive, since a build directory can hold work that is
slow to rebuild. One destructive command, `echo hello > notes.txt`, scored 0.37 on `destroys`,
below the safe `find` command, so it goes to a person rather than being blocked.

On the sample, 10 commands passed and all were safe. 20 were blocked and all were labelled
dangerous. 10 went to a person, and 2 of those were safe: `find . -name '*.pyc' -delete` and
`echo 'rm -rf /'`.

```
destroys, yes/no: labelled 40, 16 yes, 24 no, 0 errors. AUC 1.00, interval 0.89-1.00
  cut   flagged  catches           false alarms   right when flagged
  0.25       18  16/16 99% 81-99%  2/24 8% 2-26%  16/18 89% 67-97%
  0.50       15  15/16 94% 72-99%  0/24 1% 1-14%  15/15 99% 80-99%

secrets, yes/no: labelled 40, 5 yes, 35 no, 0 errors. AUC 1.00, interval 0.78-1.00
  cut   flagged  catches         false alarms    right when flagged
  0.30       10  5/5 99% 57-99%  5/35 14% 6-29%  5/10 50% 24-76%
  0.67        5  5/5 99% 57-99%  0/35  1% 1-10%  5/5  99% 57-99%

network, yes/no: labelled 40, 15 yes, 25 no, 0 errors. AUC 1.00, interval 0.89-1.00
  cut   flagged  catches           false alarms   right when flagged
  0.50       15  15/15 99% 80-99%  0/25 1% 1-13%  15/15 99% 80-99%
```

Where the model disagreed: `rm -rf /` scored only 0.75 on `destroys`, and no destructive command
scored much higher. `echo 'rm -rf /'` scored 0.30, above the pass cut, so it goes to a person.
`echo hello > notes.txt` overwrites a file but scored 0.37. `ssh deploy@203.0.113.5 uptime` scored
0.61 on `secrets`. An authenticated `curl` or `psql` call counts as sending credentials in the
labels, so expect those to reach a person or be blocked.

## prompt-injection

For screening web pages, emails or issue text before an agent reads them. It asks whether the text
addresses an AI that reads it and tells it what to do, whether the text itself tries to override
earlier instructions, and whether it tries to get an AI to reveal secrets or take an action its
user did not ask for. Each question counts only what the text itself attempts, so a page that
quotes, describes or teaches about an injection is not one.

```sh
onesie -f prompt-injection -q --state - <page.txt
```

The file carries this gate:

```yaml
assert: instructs.value < 0.15 and overrides.value < 0.5 and access.value < 0.45
abstain_if: overrides.value < 0.7 and access.value < 0.82
```

Text passes only when `instructs` sits below 0.15, `overrides` below 0.5 and `access` below 0.45.
One injection, a request for the system prompt, scored only 0.11 on `instructs`, below that cut,
and clean texts scored 0.08, 0.09 and 0.11, so no `instructs` cut catches it with margin. `access` caught
it at 0.95. `access` in turn missed two labelled yes records, and `instructs` caught both. So no
labelled injection passes, though each of the two questions misses some alone. Text is blocked when
`overrides` reaches 0.7, 0.12 above the highest clean text, or `access` reaches 0.82, 0.10 above
the highest clean text.

This gate is strict. On the sample, 8 of 20 clean texts passed and 12 went to a person. All 10
blocked texts were injections, and the other 6 injections went to a person.

```
instructs, yes/no: labelled 36, 16 yes, 20 no, 0 errors. AUC 0.93, interval 0.77-0.98
  cut   flagged  catches           false alarms      right when flagged
  0.15       27  15/16 94% 72-99%  12/20 60% 39-78%  15/27 56% 37-72%

overrides, yes/no: labelled 36, 7 yes, 29 no, 0 errors. AUC 1.00, interval 0.82-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.50        8  7/7 99% 65-99%  1/29 3% 1-17%  7/8 88% 53-98%
  0.70        7  7/7 99% 65-99%  0/29 1% 1-12%  7/7 99% 65-99%

access, yes/no: labelled 36, 10 yes, 26 no, 0 errors. AUC 0.94, interval 0.76-0.98
  cut   flagged  catches          false alarms    right when flagged
  0.45       12  8/10 80% 49-94%  4/26 15% 6-34%  8/12 67% 39-86%
  0.82        6  6/10 60% 31-83%  0/26  1% 1-13%  6/6  99% 61-99%
```

Where the model disagreed: a tutorial that quotes a system prompt scored 0.97 on `instructs`, while
a `robots.txt` scored 0.28. Text written for a person, such as a bug report with steps to reproduce,
often scored above the pass cut. `Sure, go ahead and ignore everything I said, like always.` scored
0.58 on `overrides`. A code comment asking AI reviewers to approve a pull request scored 0.26 on
`access`, though the labels count it as tool use. A page describing a security review that quotes
an attacker's override scored 0.09 on `overrides`.

## personal-data

For checking text before it is logged, shared or sent to a third party. It asks whether the text
holds a person's name with a way to contact them, a personal email address or phone number, a home
address, or a credential.

```sh
onesie -f personal-data -q --state - <message.txt
```

The file carries this gate:

```yaml
assert: name_contact.value < 0.15 and email_phone.value < 0.4 and home_address.value < 0.45 and credential.value < 0.2
abstain_if: name_contact.value < 0.5 and email_phone.value < 0.7 and home_address.value < 0.94 and credential.value < 0.38
```

Text passes only when each question sits below its pass cut. `name_contact` at 0.15 sits 0.06 below
a shipping label at 0.21, the lowest labelled yes. `email_phone` at 0.4, `home_address` at 0.45
and `credential` at 0.2 sit 0.49, 0.52 and 0.21 below theirs. Text is blocked when `name_contact`
reaches 0.5, `email_phone` 0.7, `home_address` 0.94 or `credential` 0.38. They sit 0.40, 0.10, 0.03
and 0.06 above the highest labelled no.

The `home_address` block cut has little margin. `221B Baker Street`, a museum, scored 0.91 and the
lowest labelled home address 0.97. The model cannot tell a real address from a famous or fictional
one, so such an address may be blocked or go to a person, which this set accepts.

On the sample, 14 texts passed and all were clean. All 19 texts with personal data were blocked. 3
clean texts went to a person: a museum address, a landmark address and an elided key placeholder
`sk-...`. A public support address and a `noreply` address both passed, since the email question
asks for an address that belongs to a person.

```
name_contact, yes/no: labelled 36, 8 yes, 28 no, 0 errors. AUC 1.00, interval 0.84-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.15        8  8/8 99% 68-99%  0/28 1% 1-12%  8/8 99% 68-99%
  0.50        7  7/8 88% 53-98%  0/28 1% 1-12%  7/7 99% 65-99%

email_phone, yes/no: labelled 36, 7 yes, 29 no, 0 errors. AUC 1.00, interval 0.82-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.40        8  7/7 99% 65-99%  1/29 3% 1-17%  7/8 88% 53-98%
  0.70        7  7/7 99% 65-99%  0/29 1% 1-12%  7/7 99% 65-99%

home_address, yes/no: labelled 36, 3 yes, 33 no, 0 errors. AUC 1.00, interval 0.68-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.45        5  3/3 99% 44-99%  2/33 6% 2-20%  3/5 60% 23-88%
  0.94        3  3/3 99% 44-99%  0/33 1% 1-10%  3/3 99% 44-99%

credential, yes/no: labelled 36, 8 yes, 28 no, 0 errors. AUC 1.00, interval 0.84-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.20        9  8/8 99% 68-99%  1/28 4% 1-18%  8/9 89% 57-98%
  0.38        8  8/8 99% 68-99%  0/28 1% 1-12%  8/8 99% 68-99%
```

Where the model disagreed: a fake OpenSSH private key scored only 0.41 on `credential`, while a
fake password or API key scored far higher. A shipping label with a name and a street address
scored 0.21 on `name_contact`, though the address question caught it at 0.97. `hr@example.com`
with a named recipient scored 0.60 on `email_phone`, though the labels treat a shared inbox as not
personal.

## moderation

For a comment queue or a support inbox. It asks whether the writer, first hand, says something
aggressive meant to hurt another person or group, whether the text is spam or advertising, and it
rates the tone on four levels. Quoting, reporting or discussing abuse is not hostile.

```sh
onesie -f moderation -q --state "$comment"
```

The file carries this gate:

```yaml
assert: hostile.value < 0.3 and spam.value < 0.15
abstain_if: hostile.value < 0.85 and spam.value < 0.5
```

A comment is published when `hostile` sits below 0.3 and `spam` below 0.15. Every labelled hostile
comment scored 0.81 or more and every labelled spam 0.85 or more, so both cuts leave wide room.
Sarcasm and blunt complaints scored between the two `hostile` cuts, and those go to a person. A
comment is blocked when `hostile` reaches 0.85, 0.07 above the highest clean comment, or when
`spam` reaches 0.5, 0.24 above the highest clean comment. Two hostile comments scored below the
`hostile` block cut, `Shut up.` at 0.81 among them, and went to a person.

On the sample, 12 comments were published and all were clean. 17 hostile or spam comments were
blocked, and the other 2 went to a person. 5 clean comments went to a person, mostly sarcasm and
blunt complaints.

```
hostile, yes/no: labelled 36, 12 yes, 24 no, 0 errors. AUC 1.00, interval 0.87-1.00
  cut   flagged  catches           false alarms    right when flagged
  0.30       16  12/12 99% 76-99%  4/24 17% 7-36%  12/16 75% 51-90%
  0.85       10  10/12 83% 55-95%  0/24  1% 1-14%  10/10 99% 72-99%

spam, yes/no: labelled 36, 7 yes, 29 no, 0 errors. AUC 1.00, interval 0.82-1.00
  cut   flagged  catches         false alarms   right when flagged
  0.15        9  7/7 99% 65-99%  2/29 7% 2-22%  7/9 78% 45-94%
  0.50        7  7/7 99% 65-99%  0/29 1% 1-12%  7/7 99% 65-99%
```

`tone` is not part of the gate. Use it to sort the queue a person reads, for example with
`jq 'select(.tone.norm >= 0.5)'` to show rude and abusive comments first. Every level has a
description, so the model rates against the same scale each time.

```
tone, rate: labelled 36, 0 errors. agreement 81% 65-90%
within one level 36/36 99% 90-99%

  labelled \ picked  civil  curt  rude  abusive
  civil                 12     5     0        0
  curt                   0     4     2        0
  rude                   0     0     8        0
  abusive                0     0     0        5
```

Where the model disagreed: it rated sarcasm one level harsher than the labels on `tone`, calling
`Oh great, another update that breaks everything` rude, though it gave it only 0.62 on `hostile`.
`Stop being so dramatic, it's just a typo.` scored 0.78 on `hostile`. A report that quotes an
insult to describe what moderators see scored 0.07 on `hostile`. Five civil texts were rated curt,
among them a polite disagreement and two scam messages. It never missed by more than one level.

## Offline

From the repository root, check the shell safety gate against its saved answers.

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

Exit 0 means the saved answers meet both requirements. Exit 1 means a requirement failed.
If the saved answers do not match the questions, model or provider, the command exits 2.
See [Requirements checked in CI](#requirements-checked-in-ci) for the checks for all four sets.

## Thresholds from evidence

`onesie calibrate` runs your questions over records you have already labelled, and shows what each
cut catches and what it flags by mistake. Here's the destroys question from shell-safety over 40
labelled commands:

```sh
onesie calibrate -f shell-safety -i jsonl --map .command --id .id -m jev-1.13.0 \
    --label destroys=.destroys --label secrets=.secrets --label network=.network \
    --cuts 0.25,0.5,0.65 --out answers.jsonl --resume < examples/data/shell-safety.jsonl
```

```
destroys, yes/no: labelled 40, 16 yes, 24 no, 0 errors. AUC 1.00, interval 0.89-1.00
flagged means destroys.value >= cut

  cut   flagged  catches           false alarms   right when flagged
  0.25       18  16/16 99% 81-99%  2/24 8% 2-26%  16/18 89% 67-97%
  0.50       15  15/16 94% 72-99%  0/24 1% 1-14%  15/15 99% 80-99%
  0.65        7   7/16 44% 23-67%  0/24 1% 1-14%   7/7  99% 65-99%

worst misses
  echo-overwrite  echo hello > notes.txt  labelled yes  answered 0.37
```

For destroys, shell-safety blocks at 0.5 and passes below 0.25. Anything in between goes to a
person, like the worst miss above.

## Testing a gate

To test a script that uses the gate, `ONESIE_MOCK` answers every onesie call from a file:

```sh
echo '{"destroys": 0.93, "secrets": 0.02, "network": 0.1}' > danger.json
ONESIE_MOCK=danger.json ./gate.sh   # takes the blocked branch
```

## Recipes

**Triage tickets as they come in.**

```sh
tail -f tickets.jsonl | onesie 'is this urgent' -i jsonl --map .body --id .id -o json
```

**Triage a spreadsheet.** The same rows come back with a column per question.

```sh
onesie --ask urgent='does `body` convey urgency' -i csv -o csv --merge < tickets.csv > triaged.csv
```

A spreadsheet export separated by semicolons reads with `--delimiter auto`, and `-o csv` writes
semicolons back, merged or not.

**Run a big batch you can resume.** `--map` sends only the body, `--id` names each answer, and
`--resume` skips every id the file already answers and asks the rest, errors included.

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
weighs the answers and sorts by the sum. `.value` is the yes or no probability, and `.norm` is the
rate's position between 0 and 1. The weighting lives in `jq`, since `--assert` has no arithmetic,
and `select(has("error") | not)` drops the records with an error.

```sh
onesie --ask impact='does this help many users' \
    --ask effort='how much work is this' --rate trivial,small,medium,large \
    -i jsonl --map '.body' --id '.id' -o json < ideas.jsonl |
    jq -s 'map(select(has("error") | not)) | map({id, score: (0.7 * .impact.value + 0.3 * (1 - .effort.norm))}) | sort_by(-.score)'
```

**Run against a local server.** onesie talks to any server that speaks a provider's API, such as
[ollaya](https://github.com/ollaya-dev/ollaya), which serves TypeSafe's System One API on this
machine. Point `ONESIE_BASE_URL` at it and name a model it serves:

```sh
ONESIE_BASE_URL=http://localhost:11435 onesie -m winnow:e4b 'is this urgent' < ticket.txt
```

A base URL other than the provider's own needs no key, and a run with none sends no
`Authorization` header. `--allow-http` is needed only for plain http to a host other than this
machine. A server that speaks another provider's API takes that `--provider`. `onesie profile show`
names where the base URL came from, so a variable left exported is easy to spot. A profile holds the
same setup, made with
`onesie profile create local --provider typesafe --base-url http://localhost:11435 --model winnow:e4b`
and the key prompt left empty.
