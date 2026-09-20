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

```sh
jev --help
jev version
```

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

## Layout

```
cmd/jev/         thin main: signal handling, exit codes, ldflags targets
internal/cli/    the cobra command tree, unexported and testable in process
```

`internal/` keeps everything unexported until there is a reason to publish an
API. New packages go under `internal/` first and graduate out only when
something outside this module needs them.

## Configuration

`jev` reads its API key from `TYPESAFE_API_KEY`. The repo carries a gitignored `.env`, populated
from 1Password.

| Variable | Default |
| :-- | :-- |
| `TYPESAFE_API_KEY` | none, required |
| `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |

Timeouts are per attempt, not per call. With the default policy a call retries twice, so it can
outlast the attempt timeout. Bound a whole call with a context deadline or `WithTotalTimeout`.

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
