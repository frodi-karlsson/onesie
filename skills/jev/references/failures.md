## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The question was answered |
| 1 | `-q` ran and the policy did not accept the answer |
| 2 | A usage or validation error, including a server 422 |
| 3 | Authentication or permission failed |
| 4 | The server did not answer after retries |
| 5 | A transport error or a timeout |
| 6 | A stream finished with one or more failed records |
| 130 | The run was interrupted by a signal |

Only exit 1 means the policy said no. Every other non zero exit means no answer arrived at all, so
treat exits 2 through 6 the same way you treat an outage, not the same way you treat a no.

## Decoding a validation message

| Message names | What it means |
| --- | --- |
| `--rate levels must all be described or all bare` | `--desc` covered some levels of a `--rate` question but not every one |
| `--min-confidence needs --fallback` | a fallback value is required whenever `--min-confidence` gates a pick or a rate |
| `-q needs a single question` | `-q` only reads one answer, so it rejects a command that asked more than one question, naming them |
| `-q reads one record` | `-q` has no way to carry one answer per record, so it rejects every streaming input mode |
| `--assert: unknown question` | the id used in `--assert` does not match any `--ask` id or the default `answer` key, and the message names the real ids |
| `unknown flag` | a typo in a flag name, or a flag placed where cobra cannot see it, such as `--api-key` after a subcommand |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed |

Every one of these is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.

## The `auth` flow

`jev auth set` reads a key from a prompt or from stdin and writes it to the credential file.
`jev auth status` reports which source holds the key, without building a client, so a bad
`--base-url` never gets in the way of the answer. `jev auth test` calls the models endpoint with
the resolved key, at no cost in tokens. `jev auth clear` deletes the credential file.

`auth status` exits 3 and reports `source: none` when nothing resolves. It exits 0 and names the
source otherwise.

A key resolves in this order, first match wins:

1. `--api-key`. Not reachable from `auth status` or `auth test`, since cobra keeps it a root only
   flag, so it is only ever the ordinary run path's source.
2. `TYPESAFE_API_KEY` in the environment.
3. The credential file, at `$JEV_CONFIG_DIR`, then `$XDG_CONFIG_HOME/jev`, then, on Windows,
   `%APPDATA%\jev`, and otherwise `~/.config/jev`.

`--base-url` and `TYPESAFE_BASE_URL` outrank a base URL stored in the credential file the same way.
