## Installing onesie

Until the first release is tagged, install with a Go toolchain, at the version `go.mod` names or
newer:

```sh
go install github.com/frodi-karlsson/onesie/cmd/onesie@latest
```

`go install` writes the binary to `$GOBIN`, or to `$(go env GOPATH)/bin` when that is unset, which is often not on PATH. If `command -v onesie` still finds nothing, have the user add that directory to PATH in their shell profile and open a new shell.

The Homebrew formula, `brew install frodi-karlsson/tap/onesie`, works only once a release exists. Do not tell the user when that will be.

Confirm the install with `onesie -V`, which prints the version and the built in limits.

## Storing the key

| provider | command | env var alternative |
| --- | --- | --- |
| typesafe | `onesie auth set` | `TYPESAFE_API_KEY` |
| openrouter | `onesie --provider openrouter auth set` | `OPENROUTER_API_KEY` |

An env var outranks a stored key, which suits CI. `auth set` stores one entry per provider in `credentials.json`, in `$ONESIE_CONFIG_DIR`, then `$XDG_CONFIG_HOME/onesie`, then, on Windows, `%APPDATA%\onesie`, and otherwise `~/.config/onesie`. The file is written at mode 600, and a directory it creates at mode 700. When there is an OS keychain the key goes there, under the service `onesie`, and the file entry only points at it. `auth set --file` keeps the key in the file itself. `--base-url` on `auth set` stores an API root beside the key. `onesie auth clear` removes the provider's entry and its keychain item.

`auth set` writes only to stderr, never the key:

| stderr line | meaning |
| --- | --- |
| `onesie: writing PATH` | the credential file it is about to write |
| `onesie: stored the typesafe key in the OS keychain` | the key is in the keychain, the file points at it |
| `warning: ... The key is in PATH instead` | the keychain refused, so the key went into the file |
| `the keychain did not answer in time` | a keychain prompt waited over 10 seconds, and the file was not written. Have the user answer the prompt and run `auth set` again, or pass `--file` |
| `stdin carries more than one line` | the piped input held more than the key. Pipe it through `head -n 1` |
| `is accessible by others` | the existing credential file is readable or writable by others. Have the user run `chmod 600` on it |

## Installing the plugin

In Claude Code:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

The plugin adds the onesie skills and a session start line that reports whether onesie is on PATH and whether a key resolves. It never blocks a command.
