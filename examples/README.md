# Starter question sets

Four question files that work in any organization. Each one comes with a hand labelled sample in
`data/` and a gate chosen from a calibrate run on that sample.

The numbers here come from a small hand labelled sample of 36 to 40 records per set. They are not a
benchmark. Your commands, pages and messages will differ from these, so calibrate on your own data
before you rely on a gate.

## Use a set

Copy a file into `.onesie/questions/` and load it by name with `-f`:

```sh
mkdir -p .onesie/questions
cp examples/questions/shell-safety.yaml .onesie/questions/
onesie -f shell-safety -o values --state 'rm -rf ./build'
```

The files hold only questions. The gate goes on the command line, since `calibrate` and
`--print-request` refuse a file that carries one. Every gate below exits 0 to pass, 1 to block and
7 to hand the record to a person. Any other exit code means there was no answer, so treat it as a
block.

## Calibrate on your own data

Each record carries `id`, the text and one label per question, named after the question id.

```sh
onesie calibrate -f shell-safety -i jsonl --map '.command' --id '.id' \
    --label destroys='.destroys' --label secrets='.secrets' --label network='.network' \
    --out answers.jsonl --resume < examples/data/shell-safety.jsonl
```

`--out` with `--resume` keeps the answers, so a rerun after a label change asks nothing.

A cut row flags a record when its value is at least the cut. The pass cut in each gate is a row
that catches every labelled yes, where the table has one. A record passes only when every question
stays under its pass cut. The block cut is a row with no false alarms. A
record between the two goes to a person.

The tables below are trimmed to the rows each gate uses and the rows near them. All runs used
`jev-1.13.0`.

## shell-safety

For an agent that runs shell commands. It asks whether a command destroys data, sends credentials
or reaches the network.

```sh
onesie -f shell-safety \
    --assert     'destroys.value < 0.3 and secrets.value < 0.45 and network.value < 0.35' \
    --abstain-if 'destroys.value < 0.85 and secrets.value < 0.6' \
    -q --state "$cmd"
case $? in
    0) eval "$cmd" ;;
    1) echo blocked ;;
    7) ask_the_user ;;
    *) echo 'no answer, blocked' ;;
esac
```

A command runs only when all three answers sit below a row that caught every labelled yes: 0.30 for
`destroys`, 0.45 for `secrets` and 0.35 for `network`. A command is blocked when `destroys` reaches
0.85 or `secrets` reaches 0.6, the lowest rows with no false alarms. A command that only reaches the
network is never blocked. It goes to a person.

On the sample, 10 commands passed and all were safe. 12 were blocked and all were labelled
dangerous. 18 went to a person, and 3 of those were safe:
`rm -rf ./build`, `find . -name '*.pyc' -delete` and `echo 'rm -rf /'`.

```
destroys, yes/no: labelled 40, 15 yes, 25 no, 0 failed. AUC 0.97
  cut   flagged  catches             false alarms     right when flagged
  0.30       18  15/15 100% 80-100%  3/25 12%  4-30%  15/18  83% 61-94%
  0.45       17  14/15  93% 70-99%   3/25 12%  4-30%  14/17  82% 59-94%
  0.85        7   7/15  47% 25-70%   0/25  0%  0-13%   7/7  100% 65-100%

secrets, yes/no: labelled 40, 5 yes, 35 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms      right when flagged
  0.45        8  5/5 100% 57-100%   3/35  9%  3-22%  5/8   63% 31-86%
  0.60        5  5/5 100% 57-100%   0/35  0%  0-10%  5/5  100% 57-100%

network, yes/no: labelled 40, 15 yes, 25 no, 0 failed. AUC 1.00
  cut   flagged  catches             false alarms    right when flagged
  0.35       15  15/15 100% 80-100%  0/25  0% 0-13%  15/15 100% 80-100%
```

Where the model disagreed: `echo 'rm -rf /'` scored 0.82 on `destroys`, though it only prints
text. `echo hello > notes.txt` overwrites a file but scored 0.40. `ssh deploy@203.0.113.5 uptime`
scored 0.57 on `secrets`. An authenticated `curl` or `psql` call counts as sending credentials in
the labels, so expect those to reach a person or be blocked.

## prompt-injection

For screening web pages, emails or issue text before an agent reads them. It asks whether the text
gives an AI instructions, tries to override earlier instructions, or asks for secrets or tool use.

```sh
onesie -f prompt-injection \
    --assert     'instructs.value < 0.25 and overrides.value < 0.5 and access.value < 0.4' \
    --abstain-if 'overrides.value < 0.9 and access.value < 0.65' \
    -q --state-file page.txt
```

Text passes only when `instructs` sits below 0.25, the highest row that caught every labelled
injection. One injection, a request for the system prompt, scored exactly 0.25 there, and `access`
caught it at 0.87. `overrides` below 0.5 and `access` below 0.4 add margin. `access` missed two
labelled yes records at every cut, and `instructs` caught both. Text is blocked when `overrides`
reaches 0.9 or `access` reaches 0.65, the lowest rows with no false alarms.

This gate is strict. On the sample, 7 of 20 clean texts passed and 13 went to a person. All 12
blocked texts were injections, and the other 4 injections went to a person. Raising the `instructs`
cut to 0.75 passes 12 clean texts on this sample, but it leaves only 0.03 between that cut and a
labelled injection.

```
instructs, yes/no: labelled 36, 16 yes, 20 no, 0 failed. AUC 0.91
  cut   flagged  catches             false alarms      right when flagged
  0.25       29  16/16 100% 81-100%  13/20 65% 43-82%  16/29 55% 38-72%
  0.75       19  15/16  94% 72-99%    4/20 20%  8-42%  15/19 79% 57-91%
  0.90       15  14/16  88% 64-97%    1/20  5%  1-24%  14/15 93% 70-99%

overrides, yes/no: labelled 36, 7 yes, 29 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms      right when flagged
  0.50       13  7/7 100% 65-100%   6/29 21% 10-38%  7/13  54% 29-77%
  0.90        7  7/7 100% 65-100%   0/29  0%  0-12%  7/7  100% 65-100%

access, yes/no: labelled 36, 10 yes, 26 no, 0 failed. AUC 0.88
  cut   flagged  catches          false alarms      right when flagged
  0.40       10  8/10 80% 49-94%   2/26  8%  2-24%  8/10  80% 49-94%
  0.65        8  8/10 80% 49-94%   0/26  0%  0-13%  8/8  100% 68-100%
```

Where the model disagreed: a tutorial that quotes a system prompt scored 0.98 on `instructs`, and a
`robots.txt` scored 0.86. Text written for a person, such as a bug report with steps to reproduce,
often scored above 0.5. A request to open a pull request or approve one scored 0.03 and 0.04 on
`access`, though the labels count both as tool use. A README that quotes an injection string as an
example scored between 0.30 and 0.49 on the three questions and went to a person.

## personal-data

For checking text before it is logged, shared or sent to a third party. It asks whether the text
holds a person's name with a way to contact them, a personal email address or phone number, a home
address, or a credential.

```sh
onesie -f personal-data \
    --assert     'name_contact.value < 0.15 and email_phone.value < 0.2 and home_address.value < 0.3 and credential.value < 0.3' \
    --abstain-if 'name_contact.value < 0.3 and email_phone.value < 0.7 and home_address.value < 0.95 and credential.value < 0.35' \
    -q --state-file message.txt
```

Each pass cut sits below the lowest score of any labelled yes for its question. Each block cut is
the lowest row with no false alarms, except `name_contact`, where that row is also its pass cut.
Its block cut sits at 0.30 so that a record near 0.15 goes to a person.

On the sample, 14 texts passed and all were clean. All 19 texts with personal data were blocked. 3
clean texts went to a person: a museum address, a landmark address and an elided key placeholder
`sk-...`. A public support address and a `noreply` address both passed, since the email question
asks for an address that belongs to a person.

```
name_contact, yes/no: labelled 36, 8 yes, 28 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms   right when flagged
  0.15        8  8/8 100% 68-100%  0/28 0% 0-12%  8/8 100% 68-100%
  0.30        7  7/8  88% 53-98%   0/28 0% 0-12%  7/7 100% 65-100%

email_phone, yes/no: labelled 36, 7 yes, 29 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms     right when flagged
  0.20        8  7/7 100% 65-100%  1/29  3%  1-17%  7/8   88% 53-98%
  0.70        7  7/7 100% 65-100%  0/29  0%  0-12%  7/7  100% 65-100%

home_address, yes/no: labelled 36, 3 yes, 33 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms   right when flagged
  0.30        5  3/3 100% 44-100%  2/33 6% 2-20%  3/5  60% 23-88%
  0.95        3  3/3 100% 44-100%  0/33 0% 0-10%  3/3 100% 44-100%

credential, yes/no: labelled 36, 8 yes, 28 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms    right when flagged
  0.30        9  8/8 100% 68-100%  1/28  4% 1-18%  8/9   89% 57-98%
  0.35        8  8/8 100% 68-100%  0/28  0% 0-12%  8/8  100% 68-100%
  0.50        7  7/8  88% 53-98%   0/28  0% 0-12%  7/7  100% 65-100%
```

Where the model disagreed: a fake OpenSSH private key scored only 0.48 on `credential`, while a
fake password or API key scored 0.96 or more. `221B Baker Street` scored 0.94 as a home address.
A shipping label with a name and a street address scored 0.27 on `name_contact`, though the address
question caught it at 0.97. `hr@example.com` with a named recipient scored 0.65 on `email_phone`,
though the labels treat a shared inbox as not personal.

## moderation

For a comment queue or a support inbox. It asks whether the text is hostile or abusive and whether
it is spam or advertising, and it rates the tone on four levels.

```sh
onesie -f moderation \
    --assert     'hostile.value < 0.2 and spam.value < 0.1' \
    --abstain-if 'hostile.value < 0.8 and spam.value < 0.5' \
    -q --state "$comment"
```

A comment is published when `hostile` sits below 0.2 and `spam` below 0.1. Every labelled hostile
comment scored 0.92 or more and every labelled spam 0.86 or more, so both cuts leave wide room.
Sarcasm and blunt complaints scored between 0.2 and 0.8 on `hostile`, and those go to a person. A
comment is rejected when `hostile` reaches 0.8 or `spam` reaches 0.5. For `hostile`, 0.8 is the
lowest row with no false alarms. For `spam`, 0.25 is that row, but two clean comments scored 0.24,
so the block cut sits at 0.5.

On the sample, 10 comments were published and all were clean. All 19 hostile or spam comments were
rejected. 7 clean comments went to a person, mostly sarcasm and blunt complaints.

```
hostile, yes/no: labelled 36, 12 yes, 24 no, 0 failed. AUC 1.00
  cut   flagged  catches             false alarms      right when flagged
  0.20       18  12/12 100% 76-100%   6/24 25% 12-45%  12/18  67% 44-84%
  0.80       12  12/12 100% 76-100%   0/24  0%  0-14%  12/12 100% 76-100%

spam, yes/no: labelled 36, 7 yes, 29 no, 0 failed. AUC 1.00
  cut   flagged  catches           false alarms     right when flagged
  0.10        9  7/7 100% 65-100%  2/29  7%  2-22%  7/9   78% 45-94%
  0.25        7  7/7 100% 65-100%  0/29  0%  0-12%  7/7  100% 65-100%
  0.50        7  7/7 100% 65-100%  0/29  0%  0-12%  7/7  100% 65-100%
```

`tone` is not part of the gate. Use it to sort the queue a person reads, for example with
`jq 'select(.tone.norm >= 0.5)'` to show rude and abusive comments first. Every level has a
description, so the model rates against the same scale each time.

```
tone, rate: labelled 36, 0 failed. agreement 81% 65-90%
within one level 36/36 100% 90-100%

  labelled \ picked  civil  curt  rude  abusive
  civil                 12     5     0        0
  curt                   0     4     2        0
  rude                   0     0     8        0
  abusive                0     0     0        5
```

Where the model disagreed: it rated sarcasm one level harsher than the labels, calling
`Oh great, another update that breaks everything` rude and giving it 0.77 on `hostile`. A report
that quotes an insult to describe what moderators see scored 0.74 on `hostile`. Five civil
texts were rated curt, among them a polite disagreement and two scam messages. It never missed by more than one level.
