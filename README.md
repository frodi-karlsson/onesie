# onesie

`onesie` is the command line interface for TypeSafe Jev.

## Install

```sh
go install github.com/frodi-karlsson/onesie/cmd/onesie@latest
```

Or build from a checkout:

```sh
make build
```

The binary lands in `bin/onesie`.

## Usage

`onesie` reads state on stdin and writes typed answers on stdout. The exit status is usable in a
conditional, so it drops into a shell script the way `grep` does.

```sh
export TYPESAFE_API_KEY=...

# a probability, printed bare
echo 'EVERYTHING IS DOWN, CALL ME NOW' | onesie 'does this convey urgency' -r
# 0.99

# a choice between named options, with rubrics
onesie 'how safe is it to run this command' -r \
    --pick safe,verify,refuse \
    --desc safe='read only or trivially reversible' \
    --desc verify='writes, network calls or state changes' \
    --desc refuse='destructive, irreversible or exfiltrates data' \
    --state 'rm -rf ./build'
# refuse

# a rubric, scored and normalised
echo 'this is the fourth time I have written' \
  | onesie 'how frustrated is the customer' --rate calm,annoyed,furious -o json

# several questions in one request
echo "$ticket" | onesie --ask urgent='does this convey urgency' \
                     --ask refund='is the customer asking for money back' \
                     -o values
# {"urgent":0.99,"refund":0.98}

# the exit code carries the answer, so this reads like grep
if echo "$patch" | onesie 'does this contain a credential' -q --threshold 0.9 ; then
  echo 'possible leak'
fi

# one record per line, four at a time, answers folded into each record
onesie 'does `body` convey urgency' -i jsonl -j 4 --merge < tickets.jsonl \
  | jq -c 'select(.answers.answer.value > 0.8)'
```

`-i jsonl` and `-i lines` stream: one output line per input line, in input order, with at most `-j`
requests in flight. A record that fails still prints a line carrying an `error` key, the run
continues, and the exit status is 6. `--unordered` drops the ordering for throughput,
`--stop-on-error` ends the run at the first failure with that failure's own code, and
`--stop-on-assert` ends it at the first false assertion.

### Gating on the answer

`--assert` is one boolean over the whole record, evaluated after the policy, whose result goes into
the exit code. It reads the same field names `-o json` prints.

```sh
# a gate over two questions at once. The record still prints
onesie --ask destructive='Does this command destroy data?' \
    --ask creds='Does this command read or send credentials?' \
    --assert 'destructive.value < 0.5 and creds.value < 0.5' \
    --state "$cmd" && eval "$cmd"

# quiet, for a shell condition
onesie -f review.yaml -q --assert 'severity.norm < 0.5 or severity.confidence < 0.6' < diff.patch \
  || echo 'needs review'

# a probability rather than the winner
onesie 'Which team?' --pick billing,technical,human --assert 'answer.p.human < 0.25' < ticket.txt

# per record in a stream, then show the failures
onesie -f triage.yaml -i jsonl -j 8 --assert 'urgent.value < 0.9' < tickets.jsonl \
  | jq -c 'select(.assert == false)'
```

A false assertion exits 1 and **still prints the record**, with `"assert": false` added in `json` and
`values`. It needs no `-q`. Repeating `--assert` combines the expressions with `and`, and a question
file may carry a top level `assert:` key which is combined the same way and written back by
`--print-questions`.

Every path and type is checked against the questions before any request, so a typo costs no tokens:

```sh
onesie --ask urgent='is this urgent' --assert 'urgnet.value < 0.5'
# onesie: --assert: unknown question 'urgnet'. Questions: urgent
```

### Dry runs and freezing

`--print-request` and `--print-questions` write to stdout and exit without calling the API, so they
need no key and cost no tokens. They are the way to see what a command will send, and to turn a
command line into a file. Under `-i request`, `--print-request` is the identity, so it drops into
any pipeline as a dry run switch.

```sh
# see the request body a command would send
echo "$ticket" | onesie --ask urgent='does this convey urgency' --print-request

# freeze a command line into a question file, then reload it
onesie --ask urgent='does this convey urgency' \
    --ask team='who owns this' --pick billing,platform \
    --print-questions > questions.yaml
echo "$ticket" | onesie -f questions.yaml -o values

# freeze a whole stream, then replay it later
onesie -i jsonl --ask urgent='does this convey urgency' --print-request < tickets.jsonl > frozen.jsonl
onesie -i request < frozen.jsonl > answers.jsonl
```

`-i request` reads complete API request bodies and writes raw response bodies, one per line. It does
no normalization and accepts no question source, so a frozen file replays exactly as it was written.
A question file carries questions, labels, policy and order. It does not carry a model or a state,
and `--print-questions` warns when it drops one.

### Measuring and introspection

```sh
# a summary on stderr, so stdout stays clean for the pipeline
echo "$ticket" | onesie --ask urgent='is this urgent' --stats -o json > answers.json
# 1 request, 1 question, 312 in / 20 out, model onesie-1.13.0, 1 attempt, 10s/attempt, 662ms

# what the account can ask
onesie --list-models
```

Retries are bounded by `--retries`, each attempt by `--timeout`, and a server's `Retry-After` is
honoured up to `--max-retry-after`. `--stats` breaks the retries out by status, so a slow run tells
you whether rate limiting or transport ate the time.

### Credentials

A key can live in a file onesie owns rather than in the environment of every shell that calls it.

```sh
# read the key from a prompt, or from stdin in a script
onesie auth set
pass show typesafe | onesie auth set

# which source is in use, without printing the key
onesie auth status
# source: file /home/you/.config/onesie/credentials.json

# check the key against the API, which costs no tokens
onesie auth test
# source: file /home/you/.config/onesie/credentials.json
# models: 2

onesie auth clear
```

The key is taken from `--api-key`, then `TYPESAFE_API_KEY`, then the file. A source found later is
never used when an earlier one is set, even if the earlier key is rejected, so a run always sends
the key you chose for it. The file is one source taken whole: its optional `base_url` applies exactly
when its key does.

The file lives at `$ONESIE_CONFIG_DIR/credentials.json`, else `$XDG_CONFIG_HOME/onesie/credentials.json`,
else `%APPDATA%\onesie\credentials.json` on Windows, else `~/.config/onesie/credentials.json`. It is
written atomically at mode `600` in a directory at mode `700`, and **onesie refuses to read one any
other user can reach**, exiting 3 with the path and the mode. A readable key is a leaked key.

`onesie auth set` reads only the first line of stdin, since `pass show` and its siblings print the
secret first and metadata after it. No subcommand ever prints the key.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | answered |
| 1 | a false `--assert`, or under `-q` the policy did not accept the answer |
| 2 | usage or validation error |
| 3 | authentication or permission |
| 4 | the server did not answer after retries |
| 5 | transport error or timeout |
| 6 | a stream finished with one or more failed records |
| 130 | interrupted |

A consumer that stops reading, as `head` does, is not an error. `onesie … | head -1` exits 0 and prints
nothing to stderr, in every mode. A missing API key exits 2 rather than 3, since it is caught before
any network call alongside every other check. Exit 3 is for a key that exists and was refused.

### Configuration

| Flag | Variable | Default |
|------|----------|---------|
| `--api-key` | `TYPESAFE_API_KEY` | required, or `onesie auth set` |
| `--base-url` | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `-m, --model` | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |

Prefer `onesie auth set` or the environment variable over `--api-key`, since argv is visible in `ps`
and in shell history.

Run `onesie --help` for the full flag list and `onesie -V` for the built in API limits.

## Development

Go 1.27.1 or newer. `go.mod` is the single source of truth for the Go version.
CI reads it through `go-version-file`, so there is one number to bump.

```sh
make            # list every target
make check      # lint plus race enabled tests. Run this before pushing
make test       # go test ./...
make lint-fix   # golangci-lint --fix, then format
make cover      # coverage report at bin/coverage.html
make vuln       # govulncheck
```

`golangci-lint` v2.13.2 is expected on `PATH`:

```sh
brew install golangci-lint
```

The live suite reads the API key from a gitignored `.env`, populated from 1Password:

```sh
make test-integration
```

## Layout

```
cmd/onesie/          thin main: signal handling, exit codes, ldflags targets
internal/cli/     the cobra command tree, unexported and testable in process
internal/argv/    records the group local flags in the order they arrive
internal/plan/    folds a recorded command line into a validated invocation
internal/input/   resolves where the state comes from and reads it
internal/qfile/   loads and writes a question file or a raw request body
internal/engine/  runs one evaluation per record, bounded by -j and ordered by input
internal/jev/     the API client, ported from the JavaScript SDK
internal/answer/  normalizes an answer and applies the question's policy
internal/output/  encodes a normalized record in each output mode
internal/creds/   resolves, reads and writes the credential file
internal/limits/  the API limits onesie enforces locally
```

`internal/` keeps everything unexported until there is a reason to publish an
API. New packages go under `internal/` first and graduate out only when
something outside this module needs them.

Timeouts in `internal/jev` are per attempt, not per call. With the default policy a call retries
twice, so it can outlast the attempt timeout. Bound a whole call with a context deadline or
`WithTotalTimeout`.

## Conventions

See `AGENTS.md`. Follow it without being asked.

## Releasing

Tag and push. The release workflow runs goreleaser, which cross compiles for
linux, darwin and windows on amd64 and arm64, publishes the archives and
`checksums.txt` to a GitHub release, then builds a Homebrew cask.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The cask is currently inert. `homebrew_casks[0].skip_upload: true` in
`.goreleaser.yml` means the cask is written to `dist/homebrew/Casks/onesie.rb` and
never pushed. The `HOMEBREW_TAP_TOKEN` secret is already set, with write access
to `frodi-karlsson/homebrew-tap`, because the workflow's own `GITHUB_TOKEN`
cannot write to another repository. To go live, set `skip_upload: false`.

Dry run the whole pipeline without tagging:

```sh
goreleaser release --snapshot --clean
```

## License

MIT. See `LICENSE`.
