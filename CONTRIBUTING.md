# Contributing

## Development

Go 1.27.1 or newer. `go.mod` is the single source of truth for the Go version.
CI reads it through `go-version-file`, so there is one number to bump.

```sh
make            # list every target
make check      # fuzz target check, lint and race enabled tests. Run this before pushing
make test       # go test ./...
make lint-fix   # golangci-lint --fix, then format
make cover      # coverage report at bin/coverage.html
make vuln       # govulncheck
make fuzz       # fuzz every parser and escaper in turn, FUZZTIME=1m each, about 12 minutes in all by default
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
cmd/onesie/       thin main: signal handling, exit codes, ldflags targets
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
