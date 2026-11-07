#!/usr/bin/env sh
# SessionStart hook: tells the agent whether onesie is on PATH, whether a key resolves, whether the
# binary matches the release the skills describe, and which question files -f already finds.
# Never blocks session start, so every failure exits 0.

if ! command -v onesie >/dev/null 2>&1; then
  printf '%s\n' 'onesie is not on PATH, so the onesie skills cannot run any command yet. Load the onesie-setup skill and walk the user through installing it.'
  exit 0
fi

if report=$(onesie profile preflight </dev/null 2>/dev/null) && [ -n "$report" ]; then
  printf '%s\n' "$report"
else
  printf '%s\n' 'onesie is on PATH, but automatic readiness is unavailable. Run onesie profile show for manual inspection.'
fi

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
