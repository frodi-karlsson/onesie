## Installing onesie

Three ways work. Prefer Homebrew when the user has it.

- The Homebrew formula, `brew install frodi-karlsson/tap/onesie`.
- The install script,
  `curl -fsSL https://raw.githubusercontent.com/frodi-karlsson/onesie/main/install.sh | sh`. It
  checks the sha256 of the download, installs to `~/.local/bin` or to `$ONESIE_INSTALL_DIR`, and
  warns when that directory is not on PATH.
- A Go toolchain, at the version `go.mod` names or newer, with
  `go install github.com/frodi-karlsson/onesie/cmd/onesie@latest`. It writes the binary to `$GOBIN`,
  or to `$(go env GOPATH)/bin` when that is unset, which is often not on PATH. If
  `command -v onesie` still finds nothing, have the user add that directory to PATH in their shell
  profile and open a new shell.

Prefer these three to a download from the releases page, which leaves the user to unpack the archive
and put the binary on PATH by hand.

Confirm the install with `onesie -V`, which prints the version and the built in limits.

## Storing the key

| provider | command | env var alternative |
| --- | --- | --- |
| typesafe | `onesie profile create work --provider typesafe` | `TYPESAFE_API_KEY` |
| openrouter | `onesie profile create work --provider openrouter` | `OPENROUTER_API_KEY` |
| berget | `onesie profile create work --provider berget` | `BERGET_API_KEY` |
| levanto | `onesie profile create work --provider levanto` | `LEVANTO_API_KEY` |
| cloudflare | `onesie profile create work --provider cloudflare --account-id ACCOUNT_ID` | `CLOUDFLARE_API_TOKEN`, with `CLOUDFLARE_ACCOUNT_ID` |

A name in place of `work` is 1 to 64 lowercase letters, digits, `-` or `_`, does not start with `-`,
and is not `none`. A profile holds the provider, its key, and optionally `--model`, the model a run
uses when it names none, and `--base-url`, or `--account-id` on cloudflare. The first profile
becomes the default, and `onesie profile use NAME` changes it. [Choosing a profile][choosing] gives
the order a run picks a profile in. `--provider` or `ONESIE_PROVIDER` runs without a profile and
reads only the provider's env var, which suits CI.

Profiles live in `profiles.json`, in the config dir [profiles.json][file] names. The file is written
at mode 600, and a directory it creates at mode 700. When there is an OS keychain the key goes
there, under the service `onesie`, and the profile only names the item. `--file` keeps the key in
the file itself. An older `credentials.json` there is converted into profiles the first time onesie
reads it. `onesie profile list` lists the profiles, `onesie profile update NAME --key` stores a new
key, and `onesie profile delete NAME` removes a profile and its keychain item.

[choosing]: https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#choosing-a-profile
[file]: https://github.com/frodi-karlsson/onesie/blob/main/REFERENCE.md#profilesjson

`profile create` writes only to stderr, never the key:

| stderr line | meaning |
| --- | --- |
| `stored the key in the OS keychain as NAME@...` | the key is in the keychain, and the profile names the item |
| `NAME is the default profile, since it is the first` | the first profile became the default |
| `warning: ... The key is in PATH instead` | the keychain refused, so the key went into the file |
| `the keychain did not answer in time` | a keychain prompt waited over 10 seconds, and the file was not written. Have the user answer the prompt and run the command again, or pass `--file` |
| `stdin carries more than one line` | the piped input held more than the key. Pipe it through `head -n 1` |
| `is readable or writable by its group or others` | the existing `profiles.json` is readable or writable by its group or others. Have the user run `chmod 600` on it |
| `converted credentials.json into profiles.json` | an older key file became profiles, named after their providers |

## Installing the plugin

In Claude Code:

```
/plugin marketplace add frodi-karlsson/onesie
/plugin install onesie@onesie
```

The plugin adds the onesie skills and a session start line that reports whether onesie is on PATH,
whether a key resolves, whether `onesie --version` differs from the release the skills describe, and
which question files `-f` finds. It never blocks a command.
