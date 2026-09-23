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

Only exit 1 means the policy said no. Exits 2 through 5 mean no answer arrived at all. Exit 6
means a stream finished and only the lines carrying `.error` failed, the rest answered normally.

## On exit 3

Run `jev auth status` to see which source holds the key, then `jev auth test` to check it
against the server.

A key resolves in this order, first match wins:

1. `--api-key`. `auth status` and `auth test` reject it as unknown, since it is a root only flag.
2. `TYPESAFE_API_KEY` in the environment.
3. The credential file, at `$JEV_CONFIG_DIR`, then `$XDG_CONFIG_HOME/jev`, then, on Windows,
   `%APPDATA%\jev`, and otherwise `~/.config/jev`.

`--base-url` and `TYPESAFE_BASE_URL` outrank a base URL stored in the credential file the same
way.

`jev auth set` reads a key from a prompt or from stdin and writes it to the credential file.
`jev auth clear` deletes it. `auth status` exits 3 and reports `source: none` when nothing
resolves, and exits 0 naming the source otherwise.

## On exit 2

| Message contains | What it means |
| --- | --- |
| `unknown flag` | a typo in a flag name, or a flag placed where cobra cannot see it, such as `--api-key` after a subcommand |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed |

Every validation error is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.
