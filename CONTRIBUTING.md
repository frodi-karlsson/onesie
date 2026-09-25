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
cmd/onesie/           thin main: signal handling, exit codes, ldflags targets
cmd/skillgen/         generates the per client skill files from skills/
cmd/skillcheck/       dry runs every example in every skill against the built binary
cmd/skilleval/        dry runs every onesie command an agent wrote in a plugin eval run
internal/cli/         the cobra command tree, unexported and testable in process
internal/argv/        records the group local flags in the order they arrive
internal/plan/        folds a recorded command line into a validated invocation
internal/input/       resolves where the state comes from and reads it
internal/interrupt/   stops waiting on a read that has no deadline when the run is interrupted
internal/qfile/       loads and writes a question file or a raw request body
internal/engine/      runs one evaluation per record, bounded by -j and ordered by input
internal/jev/         the API client, ported from the JavaScript SDK
internal/jq/          compiles and runs the jq expressions --map and --id take
internal/answer/      normalizes an answer and applies the question's policy
internal/assert/      parses and evaluates --assert and --abstain-if
internal/calibrate/   reads the labels calibrate compares with, and scores the answers
internal/output/      encodes a normalized record in each output mode
internal/creds/       resolves, reads and writes the credential file and the keychain item
internal/limits/      the API limits onesie enforces locally
internal/skillgen/    decodes and validates skill.json and renders the skill files
internal/skillcheck/  dry runs each skill rule's bad and good example
internal/skilleval/   extracts and grades the commands of a plugin eval run
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
linux, darwin and windows on amd64 and arm64, puts the bash, zsh and fish
completions in every archive, and publishes the archives and `checksums.txt` to
a GitHub release. The workflow then attests build provenance for the archives
and `checksums.txt`, and generates the Homebrew formula.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

Dry run the whole pipeline without tagging:

```sh
goreleaser release --snapshot --clean
```

### The Homebrew formula

onesie ships a formula, not a cask. Homebrew quarantines a cask on download, and
Gatekeeper refuses to run an unsigned binary that carries the quarantine
attribute. A formula is not quarantined.

`cmd/formulagen` reads `dist/checksums.txt` and the version, and writes a
formula that installs the prebuilt archive for darwin and linux on arm64 and
amd64, along with its completions. To see the formula for a snapshot, pass the
version from the snapshot's archive names:

```sh
go run ./cmd/formulagen -version 0.0.1-next -out dist/homebrew/Formula/onesie.rb
```

The push to `frodi-karlsson/homebrew-tap` is currently off.
`HOMEBREW_TAP_PUBLISH: 'false'` on the release job in
`.github/workflows/release.yml` keeps the tap checkout and push steps from
running, so every release generates the formula and prints it to the log, and
nothing is pushed. The `HOMEBREW_TAP_TOKEN` repository secret is set, with
write access to `frodi-karlsson/homebrew-tap`, because the workflow's own
`GITHUB_TOKEN` cannot write to another repository. To switch the push on, set
`HOMEBREW_TAP_PUBLISH: 'true'` and merge that. A tag with a prerelease suffix,
such as `v1.0.0-rc.1`, never reaches the tap.

### Notarization

The darwin binaries ship unsigned for now, as jq and ripgrep do. Homebrew, the
install script and `go install` never set the quarantine attribute, so
Gatekeeper lets the binary run. The `notarize.macos` block in `.goreleaser.yml`
stays off until the `MACOS_SIGN_P12` secret exists. To switch it on:

1. Create a Developer ID Application certificate in the Apple Developer
   account. Import the `.cer` into Keychain Access, then export the certificate
   with its private key as a `.p12` protected by a password.
2. Create an App Store Connect API key under Users and Access, then
   Integrations. Note its issuer ID and key ID, and download its `.p8` file.
   Apple lets you download it only once.
3. Add five repository secrets:
   - `MACOS_SIGN_P12`: the output of `base64 -i Certificates.p12`
   - `MACOS_SIGN_PASSWORD`: the password of the `.p12`
   - `MACOS_NOTARY_ISSUER_ID`: the issuer ID
   - `MACOS_NOTARY_KEY_ID`: the key ID
   - `MACOS_NOTARY_KEY`: the output of `base64 -i AuthKey_KEYID.p8`
4. Tag a release. goreleaser signs and notarizes the darwin binaries before it
   archives them. A bare binary cannot carry a stapled ticket, so Gatekeeper
   checks the notarization online the first time the binary runs.
5. Verify on a Mac. Extract a darwin archive and run `codesign -dv onesie`. It
   should print your `TeamIdentifier`, and not `Signature=adhoc`. Then run
   `xcrun notarytool history --issuer ISSUER_ID --key-id KEY_ID --key AuthKey_KEYID.p8`
   and check that the release's submissions show `Accepted`.
