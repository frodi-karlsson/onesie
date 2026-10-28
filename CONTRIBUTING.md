# Contributing

## Issues

Search the open and closed issues first.

A bug report needs:

- the output of `onesie --version`, and your system, such as macOS on arm64
- the command you ran, and the input when it matters. `--print-request` prints the exact request
  without sending it or needing a key, so paste that in place of private data.
- what you expected, what happened, and the exit code
- everything onesie wrote to stderr

Never paste an API key. onesie never prints one, but a shell history or an environment dump can
hold one.

A feature request says what you are trying to do and how you do it today. A use case gets further
than a flag name.

Report a security problem privately, with Report a vulnerability on the Security tab, and not in an
issue.

## Pull requests

Open an issue before a large change, so the shape is agreed before the code is written. A small fix
can go straight to a pull request.

A pull request needs:

- a one line title in the form `type: what it does`, such as `fix: ...`, `feat: ...` or `docs: ...`.
  Pull requests are squash merged, and the title becomes the commit message.
- tests for what it changes. A fix comes with a test that fails without it.
- the docs a user reads, updated in the same pull request: `README.md`, `REFERENCE.md`, and the
  skill sources in `skills/` followed by `make skills`. Docs describe how onesie works now, not
  what changed.
- a reason for any new dependency
- the conventions in `AGENTS.md`
- `make check` and `make skills-check` passing on your machine

Your commits need no signature, since the squash merge is signed by GitHub.

The checks GitHub marks as Required on the pull request must pass before a merge. The rest are
advice. The History lessons check needs a key, so it skips on a pull request from a fork.

## Development

`go.mod` names the Go version, `make` lists every target, and `golangci-lint` is expected on `PATH`
at the version `.github/workflows/ci.yml` pins. [MAINTAINING.md](MAINTAINING.md) covers the layout,
the live suite, the calibrated gates and releases.

## The skills check

`make skills-check` dry runs every example in `skills/`, and checks each flag and subcommand the
skills name outside their examples against the `--help` output. It reads each skill's description,
compatibility, intro, sections and references, and each rule's short and why.

Three kinds of mention count, so the flags of other tools are never read as onesie's:

- A onesie command in a code span or on a line of a fenced block. It may follow a pipe, a chain or
  env assignments, as in `ONESIE_MOCK=x onesie ...`, or sit inside `$(...)`. Its flags are checked
  against the help of the command they follow: the root before any subcommand, and after
  `onesie calibrate` or `onesie profile create`, that subcommand's own. A bare word where a
  subcommand can stand must be one. The command ends at a pipe, a chain, a redirection, a closing
  parenthesis or a lone `--`, so `onesie ... | jq -c --unbuffered` checks nothing of jq.
- A code span whose first word is a flag, such as `--ask NAME=@FILE`. Only that flag counts.
- A flag in plain prose, outside any code.

A code span with no onesie command in it, such as `jq --unbuffered`, `git push --force` or
`curl -fsSL`, is ignored. The last two kinds name no command, so they pass when the root help lists
the flag, or the help of a subcommand the same skill runs in its prose or its examples.
`completion` and a command that only asks for `--help` never count, and short flags are not checked.
