## Installing onesie

With Homebrew:

```sh
brew install frodi-karlsson/tap/onesie
```

With a Go toolchain:

```sh
go install github.com/frodi-karlsson/onesie/cmd/onesie@latest
```

`go install` writes the binary to `$(go env GOPATH)/bin`, which is often not on PATH. If `command -v onesie` still finds nothing, have the user add that directory to PATH in their shell profile and open a new shell.

Confirm the install with `onesie -V`, which prints the version and the built in limits.

## Storing the key

| provider | command | env var alternative |
| --- | --- | --- |
| typesafe | `onesie auth set` | `TYPESAFE_API_KEY` |
| openrouter | `onesie --provider openrouter auth set` | `OPENROUTER_API_KEY` |

The key goes to the OS keychain when there is one, and otherwise to a credential file at mode 600, one entry per provider. `auth set --file` forces the file. An env var outranks both, which suits CI.

## Installing the plugin

In Claude Code:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

The plugin adds the onesie skills and a session start line that reports whether onesie is on PATH and whether a key resolves. It never blocks a command.
