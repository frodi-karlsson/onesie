## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | The question was answered |
| 1 | `-q` ran and the policy did not accept the answer |
| 2 | A usage or validation error, including a server 422 |
| 3 | Authentication, permission or payment failed |
| 4 | The server did not answer after retries |
| 5 | A transport error or a timeout |
| 6 | A stream finished with one or more failed records |
| 7 | The gate could not decide: the assertion did not hold and `--abstain-if` did |
| 130 | The run was interrupted by a signal |

Only exit 1 means the policy said no. Exits 2 through 5 mean no answer arrived at all. Exit 6
means a stream finished and only the lines carrying `.error` failed, the rest answered normally.
Exit 7 is an answer too, neither a yes nor a no, so route it to whoever decides the middle
ground. A stream keeps the most severe code: 6 beats 1, and 1 beats 7.

## On exit 3

Run `onesie auth status` to see which source holds the key, then `onesie auth test` to check it
against the server.

The provider comes first: `--provider`, then `ONESIE_PROVIDER`, then `typesafe`. A key then
resolves for that provider in this order, first match wins:

1. `--api-key`. `auth status` and `auth test` reject it as unknown, since it is a root only flag.
2. The provider's variable: `TYPESAFE_API_KEY` for typesafe, `OPENROUTER_API_KEY` for openrouter.
   Neither provider falls back to the other's key.
3. The provider's entry in the credential file, at `$ONESIE_CONFIG_DIR`, then
   `$XDG_CONFIG_HOME/onesie`, then, on Windows, `%APPDATA%\onesie`, and otherwise
   `~/.config/onesie`. The entry holds the key, or says the key is in the OS keychain, which
   `auth status` reports as `source: keychain`.

`--base-url` outranks a base URL stored in the credential file. Under typesafe,
`TYPESAFE_BASE_URL` does too.

`onesie auth set` reads a key from a prompt or from stdin and stores it under the provider, in
the OS keychain when there is one and in the file otherwise. `--file` forces the file.
`onesie auth clear` removes the provider's entry and its keychain item. An entry pointing at a
keychain item that is gone, or a keychain that fails, exits 3. Run `onesie auth set` again. `auth status` prints the provider, then exits 3
and reports `source: none` when nothing resolves, and exits 0 naming the source otherwise.

A 402 also exits 3. OpenRouter sends it when the account is out of credits.

## On exit 2

| Message contains | What it means |
| --- | --- |
| `unknown flag` | a typo in a flag name, or a flag placed where cobra cannot see it, such as `--api-key` after a subcommand |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed |

Every validation error is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.
