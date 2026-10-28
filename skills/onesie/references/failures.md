## Exit codes

The [exit code table](https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#exit-codes)
gives each code by outcome, and `onesie --help` lists the same table.

Only exit 1 means the policy said no. Exits 2 through 5 mean no answer arrived at all. Exit 6
means a stream finished and only the lines carrying an error failed, the rest answered normally.
A resume without `--id` skips a stored error line instead of asking again, and still exits 6
over it. Under `--stop-on-error` a trailing error line is the exception, as below.
Exit 7 is an answer too, neither a yes nor a no, so route it to whoever decides the middle
ground. A stream keeps the most severe code: 6 beats 1, and 1 beats 7.

A stream that stops early takes the code of what stopped it. An auth failure stops it with 3.
`--stop-on-error` stops it with the failing record's own code, such as 4 or 5, not 6. A record
that never became a request, such as a line onesie could not read, has no code of its own, so it
stops the run with 6.
`--stop-on-assert` stops it with 1, or with 6 when an earlier record failed.
A resume under `--stop-on-assert` stops at a skipped false assertion the same way.
A resume without `--id` under `--stop-on-error` drops a trailing error line and asks its record
again, since a run stopped by `--stop-on-error` writes nothing after it. So rerunning the same
command makes progress once the cause is gone. An error line with more lines after it came from a
run without `--stop-on-error`. The resume stops there with the code that run exited with, or exits
2 for a csv or tsv row, which keeps only the message. Pass `--id` or drop `--stop-on-error` to
carry on. With `--id` the failed record is asked again instead.

## On exit 3

Run `onesie profile show` to see what a run would use, then `onesie profile test` to check the key
against the server. `profile show` only names where the key lives. It never reads the keychain, so
a keychain item that is gone shows up in `profile test` or a real run, not in `profile show`.

A run uses one profile, or none. [Choosing a profile][choosing] gives the order a run picks it in,
[What a run uses][uses] where the key, the model and the base URL come from, and
[profiles.json][file] where the file lives. No provider falls back to another's key.

Exit 3 also covers these:

- A refused key, where the message names `onesie profile update NAME --key` on a profile run, and
  the provider's key variable on a run without a profile.
- An account out of credits.
- A `profiles.json` that others can reach. The message names the file and its mode. Run
  `chmod 600` on it.
- A keychain that refuses, holds no item for the profile, or does not answer within 10 seconds,
  usually because a prompt is waiting. Answer the prompt, or store the key again with
  `onesie profile update NAME --key`, adding `--file` to skip the keychain.

[choosing]: https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#choosing-a-profile
[uses]: https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#what-a-run-uses
[file]: https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#profilesjson

## On exit 2

| Message contains | What it means |
| --- | --- |
| `there is no flag`, `has no flag`, `has no subcommand`, `takes no argument` or `expected one question` | a typo in a flag name, a flag the command does not take there, such as `--print-request` after a subcommand or any flag after `--`, or an unquoted question. `The closest is` names the flag onesie thinks was meant |
| `is not a subcommand. The closest is` | a one word question is close to a subcommand name, such as `questoins`. Fix the typo, or put the word after `--` to ask it |
| a complaint about a flag you never typed, or `was read as flags` | a question beginning with a dash was read as flags. Put the flags first, then `--`, then the question |
| `flags go before --` | a flag was typed after `--`, where every word is the question. Move it before `--` |
| `no state given` | nothing arrived on stdin and neither `--state` nor `--state-file` was passed. `since stdin was empty` means stdin, or `--state -`, was read and held nothing |
| `both give a state` | `--state` or `--state-file` was passed beside a `-f` request body that holds its own `state`. Drop one |
| `an empty state is a request the model cannot answer` | `--state`, `--state-file`, stdin, the `state` of a `-f` file, a stream line or `--map` gave an empty or all whitespace string, an empty object or an empty array. In a stream it is an error line instead, and `--skip-blank` does not drop it. An `-i request` body is forwarded as it is and not checked |
| `an empty question is one the model cannot answer` | the positional question, an `--ask`, a question file's `ask` or a `-f` body's `instructions` is empty, all whitespace or null |
| `has no 'instructions'` or `names its question 'instructions'` | a `-f` request body question has no `instructions`, or carries a question file's `ask` in its place |
| `no API key` | nothing resolved for the provider, in the order [Choosing a profile][choosing] gives. A real run and `profile test` exit 2 here. `profile show` exits 2 too, after printing `key: none`, with the same message on stderr. Under `--provider` or `ONESIE_PROVIDER` the message names a profile with that provider when there is one, and otherwise `onesie profile create NAME --provider PROVIDER` |
| `no profile is the default` | there are profiles, none is the default and nothing chose one. Run `onesie profile use NAME`, or pass `--profile` or `--provider` |
| `which does not exist` | `--profile` or `ONESIE_PROFILE` names a profile that is not in `profiles.json`. The message lists the profiles there are, and says to pass `--provider` when the name is a provider |
| `is not valid JSON` | `profiles.json` or `credentials.json` is cut short or holds a syntax error. `is not a JSON object of profiles` means valid JSON of another shape |
| `is in use by another onesie run` | another run holds the lock beside the `--out` file |
| `which onesie did not write, and no CACHEDIR.TAG` | `--cache` found the file the message names in a cache directory onesie did not make. Point `ONESIE_CACHE_DIR` at a new or empty directory. `onesie cache` prints the same line and exits 0 |
| `Drop --resume to start over` | the `--out` file was written by a different run, or has no fingerprint. `changed since` names what differs, such as `the model` or `--map` |
| `--resume needs output that keeps each record's outcome` | `-o raw` and `-r` keep no id, failure or gate outcome, so a resume refuses them. Use `-o values` or `-o json` |
| `is followed by more rows` | a resume without `--id` under `--stop-on-error` reached a stored csv or tsv error row with more rows after it, and a row keeps only the message, not the code to stop with. Pass `--id` or drop `--stop-on-error` |
| `the header` | the csv or tsv header is not valid, or has a blank or repeated column name |
| `every record's --id was null` | `-i csv` read a tab separated file, or `-i tsv` a comma separated one, so the header is one column and `--id` finds nothing. Pass the other `-i`. A mistyped `--id` also gives null ids, but then each record fails with `got null` and no hint. Check that `--id` names a header column |
| `a row is longer than the limit`, with no error line | a csv row over `max-line-bytes` from `onesie -V`. It stops the run, since csv cannot find the next row after it. In tsv it is an error line instead |
| `--usage does not apply to` | `-o values`, `-o raw` and `-r` write only the answers, so the message says to use `-o json`. `-o csv` and `-o tsv` have no column for it, and `-q` suppresses output |
| `--merge-key does not apply to` | `-o csv` and `-o tsv` put the answers in columns, so they refuse `--merge-key` even beside `--merge` |
| `has the name of an output column` | under `--merge`, an input column clashes with a question id or the `error` column, or with the `assert` column under a gate. An input `id` column never clashes |
| `model '...' not found` | the provider does not serve that model id. Try `--list-models` |
| `each side must be`, `an image must be PNG or JPEG` or `an image is at most` | onesie refused an `--image` file before any request, naming the limit `onesie -V` lists. Resize it to at most 1568 pixels a side, or convert it to PNG or a smaller JPEG. An `--image-field` file is an error line instead |
| `needs an image in the request` | a `--box` question was asked with no `--image` or `--image-field` to look in |
| `long_context` and `must be true or false` | a request body's `long_context` is not a boolean. Under `-i request` it is an error line of kind `input`, and `--print-request` refuses it the same way |
| `and at most` and `are allowed` | an `-i request` body holds a choice question with more options, or a score question with more levels, than `onesie -V` lists. It is an error line of kind `input`, and nothing is sent for it |
| `takes no images`, `takes no box questions` or `takes no long context` | the provider cannot take what the run asks for. The message names the providers that can, so ask with one of them through `--provider` |
| `no cloudflare account id` | cloudflare has a token and no account. Set `CLOUDFLARE_ACCOUNT_ID`, or store one in the profile with `onesie profile update NAME --account-id ACCOUNT_ID`, or in a new one with `onesie profile create NAME --provider cloudflare --account-id ACCOUNT_ID` |
| `is plain http to a host other than this machine` | the base URL the message names, `--base-url`, `TYPESAFE_BASE_URL` or `the profile's base_url`, is `http://` to a host other than this machine, so the key would travel unencrypted. Use https, or pass `--allow-http` to send it anyway. A profile stored with an http `base_url` needs `--allow-http` on every run |
| `must not hold @, ? or #` | the base URL the message names holds `@`, `?` or `#`, as credentials, a query or a fragment do. Pass the API root alone, and keep the key in a profile or the provider's key variable |
| `names no host`, `is not an http or https URL` or `does not parse` | the base URL the message names is not an absolute http or https URL with a host. Write it as `https://HOST`, with a path after it when the server needs one |
| `Insufficient balance` | Cloudflare's AI Gateway has no credits for Jev. Add credits, or ask Clef with `-m clef-flash`, which runs on the free allowance |
| `No such model` | Workers AI does not serve that model id. Clef's are `clef`, `clef-flash` and `clef-omni`. Try `--list-models` |
| `A box question needs a model that serves it` | the model refused a `--box` question on TypeSafe or OpenRouter. Pass `-m jev-preview` |
| `max_tokens_exceeded` | the state and questions are over the model's context limit. Trim the state with `--map`, or pass `--long-context`, which is slower and less accurate |

Every flag and question error is caught before any network call. Reach for `--print-request` or
`--print-questions` first and a typo costs nothing.
