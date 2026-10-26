#!/usr/bin/env sh
# SessionStart hook: tells the agent whether onesie is on PATH, whether a key resolves, whether the
# binary matches the release the skills describe, and which question files -f already finds.
# Never blocks session start, so every failure exits 0.

if ! command -v onesie >/dev/null 2>&1; then
  printf '%s\n' 'onesie is not on PATH, so the onesie skills cannot run any command yet. Load the onesie-setup skill and walk the user through installing it.'
  exit 0
fi

# onesie profile show exits 2 when no key or no account resolves, which is an answer rather than a
# failure, so the key line decides and the exit code is ignored. The first read of the config dir can
# convert credentials.json, and the lines it prints about that and the warnings that follow are passed
# on, kept apart from the report.
status=$(onesie profile show </dev/null 2>&1)
converted='^onesie: converted '
unprofiled='^onesie: ONESIE_PROVIDER is set, so a run uses no profile'
unwritten='^onesie: could not write '
unlinked='^onesie: removed the link '
warned='^warning: '
notices=$(printf '%s\n' "$status" | grep -e "$converted" -e "$unprofiled" -e "$unwritten" -e "$unlinked" -e "$warned")
report=$(printf '%s\n' "$status" | grep -v -e "$converted" -e "$unprofiled" -e "$unwritten" -e "$unlinked" -e "$warned")
profile=$(printf '%s\n' "$report" | sed -n 's/^profile: //p')
provider=$(printf '%s\n' "$report" | sed -n 's/^provider: //p')
key=$(printf '%s\n' "$report" | sed -n 's/^key: //p')
account=$(printf '%s\n' "$report" | sed -n 's/^account: //p')

if [ -n "$notices" ]; then
  printf '%s\n' "$notices"
fi

case "$key" in
  none)
    case "$provider" in
      openrouter) variable=OPENROUTER_API_KEY ;;
      berget) variable=BERGET_API_KEY ;;
      levanto) variable=LEVANTO_API_KEY ;;
      cloudflare) variable=CLOUDFLARE_API_TOKEN ;;
      *) variable=TYPESAFE_API_KEY ;;
    esac
    printf 'onesie is on PATH, but no API key resolves for provider %s. Only --print-request, --print-questions, --print-schema and --mock work until the user runs onesie profile create NAME --provider %s or sets %s. The onesie-setup skill walks them through it.\n' "$provider" "$provider" "$variable"
    ;;
  ?*)
    if [ "$account" != none ]; then
      printf 'onesie is on PATH and a key resolves, profile: %s, provider: %s, key: %s.\n' "$profile" "$provider" "${key%%, ends in *}"
    elif [ "$profile" = none ]; then
      printf 'onesie is on PATH and a key resolves for provider %s, but no account does. Every request fails until the user sets CLOUDFLARE_ACCOUNT_ID or stores a profile with onesie profile create NAME --provider cloudflare --account-id ACCOUNT_ID. The onesie-setup skill walks them through it.\n' "$provider"
    else
      printf 'onesie is on PATH and a key resolves for profile %s, but no account does. Every request fails until the user runs onesie profile update %s --account-id ACCOUNT_ID or sets CLOUDFLARE_ACCOUNT_ID. The onesie-setup skill walks them through it.\n' "$profile" "$profile"
    fi
    ;;
  *)
    first=$(printf '%s\n' "$report" | head -n 1)
    case "$first" in
      'onesie: no profile is the default'*)
        printf '%s\n' 'onesie is on PATH and has profiles, but none is the default and nothing chose one, so every run exits 2 until the user runs onesie profile use NAME. onesie profile list names them.'
        ;;
      '')
        printf '%s\n' 'onesie is on PATH, but onesie profile show gave no answer, so whether a key resolves is unknown.'
        ;;
      *)
        printf 'onesie is on PATH, but onesie profile show failed with: %s\n' "$first"
        ;;
    esac
    ;;
esac

# The manifest sits in the plugin root, one directory above this script. A build from source
# reports dev or a git describe version such as v0.2.0-9-ga5a2ace, which is expected to differ, so
# only a release version is compared.
manifest="$(dirname -- "$0")/../.claude-plugin/plugin.json"
plugin=$(sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest" 2>/dev/null | head -n 1)
binary=$(onesie --version </dev/null 2>/dev/null | head -n 1 | sed -n 's/^onesie v\{0,1\}//p')
case "$binary" in
  dev | *-[0-9]*-g[0-9a-f]* | *-dirty) ;;
  [0-9]*.[0-9]*.[0-9]*)
    if [ -n "$plugin" ] && [ "$plugin" != "$binary" ]; then
      printf 'onesie --version reports %s, while the skills describe the flags of release %s. Check a flag with onesie --help before relying on it.\n' "$binary" "$plugin"
    fi
    ;;
esac

# There is no portable timeout in POSIX sh, and none is needed: onesie questions only lists the
# local question directories and the built in sets, with no network call.
saved=$(onesie questions </dev/null 2>/dev/null | awk '
  {
    gsub(/[[:cntrl:]]/, "")
    if (NF < 2) next
    name = $1
    sub(/^[^ ]+ +/, "")
    count++
    if (count > 20) next
    where = ($0 == "built in") ? "built in" : "in " $0
    list = list (list == "" ? "" : ", ") name " " where
  }
  END {
    if (count > 20) list = list ", and " (count - 20) " more"
    print list
  }
')
if [ -n "$saved" ]; then
  printf 'Question files -f NAME loads: %s. Reuse one before writing a new one.\n' "$saved"
fi

exit 0
