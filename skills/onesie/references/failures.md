## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The question was answered |
| 1 | The answer was a no: `-q` rejected it, or an assertion was false on the record or on any record of a stream |
| 2 | A usage or validation error, a missing key, a refused resume, or a 4xx from the server other than 401, 402, 403, 408 and 429 |
| 3 | Authentication, permission or payment failed, or the credential store could not be used |
| 4 | The server did not answer after retries: a 429, a 5xx, or a 2xx body onesie cannot use |
| 5 | A transport error or a timeout, including a 408 |
| 6 | A stream finished with one or more failed records |
| 7 | The gate could not decide: the assertion did not hold and `--abstain-if` did |
| 130 | The run was interrupted by a signal |

Only exit 1 means the policy said no. Exits 2 through 5 mean no answer arrived at all. Exit 6
means a stream finished and only the lines carrying an error failed, the rest answered normally.
A resume without `--id` skips a stored error line instead of asking again, and still exits 6
over it. Under `--stop-on-error` a trailing error line is the exception, as below.
Exit 7 is an answer too, neither a yes nor a no, so route it to whoever decides the middle
ground. A stream keeps the most severe code: 6 beats 1, and 1 beats 7.

A stream that stops early takes the code of what stopped it. An auth failure stops it with 3.
`--stop-on-error` stops it with the failing record's own code, such as 4 or 5, not 6.
`--stop-on-assert` stops it with 1, or with 6 when an earlier record failed.
A resume under `--stop-on-assert` stops at a skipped false assertion the same way.
A resume without `--id` under `--stop-on-error` drops a trailing error line and asks its record
again, since a run stopped by `--stop-on-error` writes nothing after it. So rerunning the same
command makes progress once the cause is gone. An error line with more lines after it came from a
run without `--stop-on-error`. The resume stops there with the code that run exited with, or exits
2 for a csv or tsv row, which keeps only the message. Pass `--id` or drop `--stop-on-error` to
carry on. With `--id` the failed record is asked again instead.

## On exit 3

Run `onesie auth status` to see which source holds the key, then `onesie auth test` to check it
against the server. `auth status` only names the source. It never reads the keychain, so a
keychain item that is gone shows up in `auth test` or a real run, not in `auth status`.

The provider comes first: `--provider`, then `ONESIE_PROVIDER`, then `typesafe`. A key then
resolves for that provider in this order, first match wins:

1. The provider's variable: `TYPESAFE_API_KEY` for typesafe, `OPENROUTER_API_KEY` for openrouter,
   `BERGET_API_KEY` for berget. No provider falls back to another's key.
2. The provider's entry in `credentials.json`, in `$ONESIE_CONFIG_DIR`, then
   `$XDG_CONFIG_HOME/onesie`, then, on Windows, `%APPDATA%\onesie`, and otherwise
   `~/.config/onesie`. The entry holds the key, or says the key is in the OS keychain, which
   `auth status` reports as `source: keychain`.

Exit 3 also covers these:

- A 401, a 403 or a 402. OpenRouter sends 402 when the account is out of credits, and Berget when
  the key has no subscription.
- A credential file that others can reach. The message names the file and its mode. Run
  `chmod 600` on it.
- A keychain that refuses, holds no item for the entry, or does not answer within 10 seconds,
  usually because a prompt is waiting. Answer the prompt, or run `onesie auth set` again, with
  `--file` to skip the keychain.
- `auth status` when nothing resolves, which prints `source: none`.

`--base-url` outranks a base URL stored in the credential file. Under typesafe,
`TYPESAFE_BASE_URL` does too.

## On exit 2

| Message contains | What it means |
| --- | --- |
| `unknown flag`, `unknown command` or `accepts at most 1 arg` | a typo in a flag name, a flag cobra cannot see, such as `--print-request` after a subcommand or any flag after `--`, or an unquoted question |
| a complaint about a flag you never typed | a question beginning with a dash was read as flags. Put the flags first, then `--`, then the question |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed |
| `an empty state is a request the model cannot answer` | `--state`, `--state-file`, stdin, the `state` of a `-f` file, a stream line or `--map` gave an empty or all whitespace string, an empty object or an empty array. In a stream it is an error line instead, and `--skip-blank` does not drop it. An `-i request` body is forwarded as it is and not checked |
| `no API key` | nothing resolved for the provider, see the order above. A real run and `auth test` exit 2 here, `auth status` exits 3 |
| `is in use by another onesie run` | another run holds the lock beside the `--out` file |
| `Drop --resume to start over` | the `--out` file was written by a different run, or has no fingerprint |
| `--resume needs output that keeps each record's outcome` | `-o raw` and `-r` keep no id, failure or gate outcome, so a resume refuses them. Use `-o values` or `-o json` |
| `is followed by more rows` | a resume without `--id` under `--stop-on-error` reached a stored csv or tsv error row with more rows after it, and a row keeps only the message, not the code to stop with. Pass `--id` or drop `--stop-on-error` |
| `the header` | the csv or tsv header is not valid, or has a blank or repeated column name |
| `a row is longer than the limit`, with no error line | a csv row over `max-line-bytes` from `onesie -V`. It stops the run, since csv cannot find the next row after it. In tsv it is an error line instead |
| `--usage does not apply to` | `-o values`, `-o raw` and `-r` write only the answers, so the message says to use `-o json`. `-o csv` and `-o tsv` have no column for it, and `-q` suppresses output |
| `--merge-key does not apply to` | `-o csv` and `-o tsv` put the answers in columns, so they refuse `--merge-key` even beside `--merge` |
| `has the name of an output column` | under `--merge`, an input column clashes with a question id or the `error` column, or with the `assert` column under a gate. An input `id` column never clashes |
| `model '...' not found` | the provider does not serve that model id. Try `--list-models` |

Every flag and question error is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.
