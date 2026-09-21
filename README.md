# jev-cli

`jev` is the command line interface for TypeSafe Jev.

## Install

```sh
go install github.com/frodi-karlsson/jev-cli/cmd/jev@latest
```

Or build from a checkout:

```sh
make build
```

The binary lands in `bin/jev`.

## Usage

`jev` reads state on stdin and writes typed answers on stdout. The exit status is usable in a
conditional, so it drops into a shell script the way `grep` does.

```sh
export TYPESAFE_API_KEY=...

# a probability, printed bare
echo 'EVERYTHING IS DOWN, CALL ME NOW' | jev 'does this convey urgency' -r
# 0.99

# a choice between named options, with rubrics
jev 'how safe is it to run this command' -r \
    --pick safe,verify,refuse \
    --desc safe='read only or trivially reversible' \
    --desc verify='writes, network calls or state changes' \
    --desc refuse='destructive, irreversible or exfiltrates data' \
    --state 'rm -rf ./build'
# refuse

# a rubric, scored and normalised
echo 'this is the fourth time I have written' \
  | jev 'how frustrated is the customer' --rate calm,annoyed,furious -o json

# several questions in one request
echo "$ticket" | jev --ask urgent='does this convey urgency' \
                     --ask refund='is the customer asking for money back' \
                     -o values
# {"urgent":0.99,"refund":0.98}

# the exit code carries the answer, so this reads like grep
if echo "$patch" | jev 'does this contain a credential' -q --threshold 0.9 ; then
  echo 'possible leak'
fi

# one record per line, four at a time, answers folded into each record
jev 'does `body` convey urgency' -i jsonl -j 4 --merge < tickets.jsonl \
  | jq -c 'select(.answers.answer.value > 0.8)'
```

`-i jsonl` and `-i lines` stream: one output line per input line, in input order, with at most `-j`
requests in flight. A record that fails still prints a line carrying an `error` key, the run
continues, and the exit status is 6. `--unordered` drops the ordering for throughput, and
`--stop-on-error` ends the run at the first failure with that failure's own code.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | answered |
| 1 | under `-q`, the policy did not accept the answer |
| 2 | usage or validation error |
| 3 | authentication or permission |
| 4 | the server did not answer after retries |
| 5 | transport error or timeout |
| 6 | a stream finished with one or more failed records |
| 130 | interrupted |

### Configuration

| Flag | Variable | Default |
|------|----------|---------|
| `--api-key` | `TYPESAFE_API_KEY` | required |
| `--base-url` | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `-m, --model` | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |

Prefer the environment variable over `--api-key`, since argv is visible in `ps` and in shell
history.

Run `jev --help` for the full flag list and `jev -V` for the built in API limits.

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
cmd/jev/          thin main: signal handling, exit codes, ldflags targets
internal/cli/     the cobra command tree, unexported and testable in process
internal/argv/    records the group local flags in the order they arrive
internal/plan/    folds a recorded command line into a validated invocation
internal/input/   resolves where the state comes from and reads it
internal/engine/  runs one evaluation per record, bounded by -j and ordered by input
internal/jev/     the API client, ported from the JavaScript SDK
internal/answer/  normalizes an answer and applies the question's policy
internal/output/  encodes a normalized record in each output mode
internal/limits/  the API limits jev enforces locally
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
`.goreleaser.yml` means the cask is written to `dist/homebrew/Casks/jev.rb` and
never pushed. To go live, set `skip_upload: false` and add a
`HOMEBREW_TAP_TOKEN` secret with write access to `frodi-karlsson/homebrew-tap`.
The workflow's own `GITHUB_TOKEN` cannot write to another repository.

Dry run the whole pipeline without tagging:

```sh
goreleaser release --snapshot --clean
```

## License

MIT. See `LICENSE`.
