# Semantic actions for the shell

Design for review. Deliver `filter` first. Follow with `find`, which shares its match rule, then `rank`, which orders a finite input. Each command must be useful on its own. No change to model APIs is required.

| User intent | Command | Output |
|---|---|---|
| Keep records that mean something specific | `filter` | Matching records in input order |
| Identify files whose contents match | `find` | Matching paths in argument order |
| Choose the most relevant records within a reading budget | `rank` | Up to the requested number of records with their scores |

## Command names and questions

Keep `onesie 'is this urgent'` as the short form for asking a question. A command name only takes that meaning as its own argument in command position. The shell passes `'find signs of fraud'` as one argument. It stays a question. Quoting `'find'` alone passes the same bytes as typing `find`.

Adding a command reserves its exact name. The existing typo check can also suggest it for a nearby single word. Document the literal form alongside the new commands: `onesie -- "$question"`. Root flags precede the separator. This form works for command names, near misses and text beginning with a dash. Keep the existing named question syntax. Do not add `ask` or require a migration of ordinary positional questions.

All three actions take exactly one question argument. Quote it when it contains spaces. An action uses the existing positional yes or no question with the id `answer`. Its result is the probability of yes. Root question files, pick and rate questions, gates, fallbacks, merged output and answer file resume stay with the root command. An action rejects unsupported flags before model access. Register command names as each ships, with help and completion reflecting only available commands.

## Filter

A support team wants the original refund tickets to pass to its existing queue importer.

```sh
onesie filter 'the customer wants their money back' < messages.txt
onesie filter 'the customer wants their money back' -i jsonl --map .body < tickets.jsonl
onesie filter 'the customer wants their money back' -i csv --map .body < tickets.csv
```

The default input is lines, one record per line. Accept `-i lines`, `jsonl`, `csv` and `tsv`. Use the existing rules for `--map`, `--delimiter` and `--skip-blank` with those formats. Evaluate the mapped state using the same request a root yes or no question sends. Output the original record once when its probability is at least `--threshold`. The default is 0.5. An explicit threshold must be in the existing inclusive range from 0 to 1. Neither output nor exit status depends on whether stdout is a terminal.

| Input | Output contract |
|---|---|
| Lines | Original line content followed by a newline. Keep spaces and duplicates. |
| JSONL | Original JSON record followed by a newline. Preserve fields, numeric precision and input key order. Mapping affects only what the model sees. |
| CSV and TSV | Write the validated original header once, even with no matches. Write matching rows with the original field values, column order and delimiter. Quoting and line endings may be normalized. Add no answer or error column. |

An empty input writes nothing. A valid header with no data writes the header. Both exit 1. Stream matches in order as soon as earlier decisions are known, even while stdin is open. Bound outstanding work with the existing jobs setting. Two equal records can share an evaluation, but two matches still produce two records.

Support the existing model, provider, profile, endpoint, mock, cache, retry, timeout and jobs controls with their current meanings and defaults. Support the existing transport options needed by those controls. Draw flag registration, validation and descriptions from the settings rows. Input flags and `--threshold` retain their normal spellings. Keep output fixed by the command. The first version needs no new root flag.

### Exit behavior

| Exit | Filter and find | Rank |
|---|---|---|
| 0 | At least one match, no failure. | All records scored and the result was written, including empty input. |
| 1 | All records judged successfully and none matched. | Unused. |
| 2 | Invalid invocation, configuration or malformed input header before processing. | Same. |
| 3 | Credential setup or keychain failure before record processing. | Same. |
| 6 | One or more record or file evaluations failed. Takes precedence over both a match and no matches. | An incomplete ranking. Print diagnostics and no ranked records. |
| 130 | Interrupted. Already emitted records can remain in the pipe. | Interrupted. Emit no list before scoring finishes. |

Use the existing stream failure classification for errors outside those cases and the existing handling of stdout closing early. Record failures name the line or path on stderr and never appear on stdout as matches. Continue past a malformed record or a failed evaluation as the stream runner does. Preflight failure produces no data. Neither an error nor an interruption becomes a negative model answer.

### Filter acceptance

Use mock answers for repeatable checks through the real CLI. Give three JSONL tickets distinct ids, an unrelated account field and a body. With probabilities 0.49, 0.50 and 0.90, expect the last two original records, with their ids, in order, at the default threshold. Inspect the request to verify that only the body was judged. Repeat with a delayed first result and multiple jobs. A separate live pipe check must observe an early match before closing stdin.

Cover a line with leading spaces, duplicates, an unterminated final line, a JSON integer beyond exact floating point precision, CSV with a quoted multiline field, TSV and a detected semicolon delimiter. Check no matches, header only, malformed header and a bad row between two good ones. For the bad row, emit good matches and exit 6 with a diagnostic. Test that help, invalid arguments and a bad threshold spend nothing. Exercise clean early pipe closure and interruption with the same harness used for existing streams.

## Find

```sh
onesie find 'the incident involved a failed rollback' reports/*.md
onesie find --print0 'the incident involved a failed rollback' reports/*.md
```

Take a question followed by one or more explicit file paths. The shell expands globs. Read each regular text file as one state using the existing state file size and text validation rules. Judge the contents and print only the supplied path for each match. Reuse the filter threshold, connection controls, match ordering and failure rules. A duplicate path is a duplicate input. The path is never interpreted as a shell command.

Start with explicit paths. No recursive walk, directory enumeration, ignore language or implicit stdin. Follow a symlink only to a regular file. Reject a directory, device, pipe, unreadable file or invalid state as a file failure with its path on stderr. Missing path arguments are a usage error. A literal unmatched glob reaching the program is an unreadable path. Support `--` for operands beginning with a dash.

Default output is one path per line. Refuse paths containing a newline in this mode before making requests, with a hint for `--print0`. That mode terminates each original path with a NUL for use with `xargs -0`. Test spaces, newline names, symlinks, an oversized file, a read failure beside a match, duplicate paths and out of order responses. Use temporary files and verify that the content reaches the request and the original path reaches stdout.

## Rank

```sh
onesie rank 'this issue affects many customers' --top 10 -i jsonl --map .body < backlog.jsonl
```

Score each record independently with the same yes or no question and model. The score is the unrounded probability of yes. Ask about an observable property, such as widespread customer impact. A question phrased as a comparison with other records would require context the model never sees. The CLI ranks the returned scores. It makes no claim that a model is calibrated on a particular backlog.

Require a positive integer `--top N`. Accept the same record formats and mapping as filter, without a match threshold. Wait for end of input, then emit up to N records in descending score order. Resolve equal scores by original input position, including ties at the cutoff. Fewer than N successful inputs produce all of them on a successful run. Keep only the best N candidates plus bounded in flight work. Never allocate N slots up front from unchecked user input.

Use JSONL consistently for the ranked output. Each line holds `score` and `record`. The record is the original text string, JSON value or delimited row object with its original field names and string values. Nesting keeps user fields named `score` or `record` intact. For example, one returned line could be `{"score":0.92,"record":{"id":17,"body":"All checkout requests fail"}}`. This first version ranks a finite collection and does not support an unbounded sort, pairwise comparisons or multiple weighted questions.

Check mock scores 0.2, 0.9 and 0.9 with `--top 1` and `--top 2`. The earlier 0.9 wins the tie. Verify output with reversed completion order, fewer records than requested, a record with its own score, an empty stream and a failed record. On failure, a caller must never mistake a partial list for the best records across its whole input.

## Shared implementation and plugin

Reuse the current settings rows, question builder, provider selection, input readers, ordered stream execution and model answer. The action needs the original input, the evaluated score or error, and a destination writer. Keep any record projection for `--map` separate from rendering the original. Check that the CSV reader exposes a valid header at end of input even with zero data rows. Add the smallest reader access needed for that case. Do not parse the root command's JSON output, invoke `jq` internally or introduce a second settings object.

Each milestone includes runnable help examples, command completion, exit codes and regression checks for root question routing. Check `onesie -- find`, a near miss after `--`, `onesie 'find signs of fraud'`, and discovery through `onesie help filter` as the commands ship.

Teach the available actions in one plugin skill. Use filter when every matching record matters, find when the caller wants paths and rank when the caller has a reading budget. Start the skill with filter and extend it as the other commands land. Examples must pass the normal generated skill checks and include a mock run. An MCP implementation is a separate decision if a host needs these operations without a shell.
