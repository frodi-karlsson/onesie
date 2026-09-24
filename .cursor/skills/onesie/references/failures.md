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
over it.
Exit 7 is an answer too, neither a yes nor a no, so route it to whoever decides the middle
ground. A stream keeps the most severe code: 6 beats 1, and 1 beats 7.

A stream that stops early takes the code of what stopped it. An auth failure stops it with 3.
`--stop-on-error` stops it with the failing record's own code, such as 4 or 5, not 6.
`--stop-on-assert` stops it with 1.

## On exit 3

Run `onesie auth status` to see which source holds the key, then `onesie auth test` to check it
against the server. `auth status` only names the source. It never reads the keychain, so a
keychain item that is gone shows up in `auth test` or a real run, not in `auth status`.

The provider comes first: `--provider`, then `ONESIE_PROVIDER`, then `typesafe`. A key then
resolves for that provider in this order, first match wins:

1. `--api-key`. `auth status` and `auth test` reject it as unknown, since it is a root only flag.
2. The provider's variable: `TYPESAFE_API_KEY` for typesafe, `OPENROUTER_API_KEY` for openrouter.
   Neither provider falls back to the other's key.
3. The provider's entry in `credentials.json`, in `$ONESIE_CONFIG_DIR`, then
   `$XDG_CONFIG_HOME/onesie`, then, on Windows, `%APPDATA%\onesie`, and otherwise
   `~/.config/onesie`. The entry holds the key, or says the key is in the OS keychain, which
   `auth status` reports as `source: keychain`.

Exit 3 also covers these:

- A 401, a 403 or a 402. OpenRouter sends 402 when the account is out of credits.
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
| `unknown flag`, `unknown command` or `accepts at most 1 arg` | a typo in a flag name, a flag cobra cannot see, such as `--api-key` after a subcommand or any flag after `--`, or an unquoted question |
| a complaint about a flag you never typed | a question beginning with a dash was read as flags. Put the flags first, then `--`, then the question |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed |
| `no API key` | nothing resolved for the provider, see the order above. A real run and `auth test` exit 2 here, `auth status` exits 3 |
| `is being resumed by another onesie run` | another run holds the lock beside the `--out` file |
| `Drop --resume to start over` | the `--out` file was written by a different run, or has no fingerprint |
| `--resume with raw output` | raw output keeps neither the id nor the gate's outcome, so a resume refuses it |
| `the header` | the csv or tsv header is not valid, or has a blank or repeated column name |
| `a row is longer than the limit`, with no error line | a csv row over `max-line-bytes` from `onesie -V`. It stops the run, since csv cannot find the next row after it. In tsv it is an error line instead |
| `has the name of an output column` | an input column clashes with a question id or an `id`, `assert` or `error` column |
| `model '...' not found` | the provider does not serve that model id. Try `--list-models` |

Every flag and question error is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.
