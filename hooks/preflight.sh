#!/usr/bin/env sh
# SessionStart hook: tells the agent whether jev is on PATH and whether a key resolves.
# Never blocks session start, so every failure exits 0.

if ! command -v jev >/dev/null 2>&1; then
  printf '%s\n' 'jev is not on PATH, so the jev skills cannot run any command yet. Ask the user to install it with: go install github.com/frodi-karlsson/jev-cli/cmd/jev@latest'
  exit 0
fi

# jev auth status exits 3 when no key resolves, which is an answer rather than a failure.
status=$(jev auth status 2>/dev/null)

case "$status" in
  'source: none'*)
    printf '%s\n' 'jev is on PATH, but no API key resolves. Only --print-request and --print-questions work until the user runs jev auth set or sets TYPESAFE_API_KEY.'
    ;;
  'source: '?*)
    printf 'jev is on PATH and a key resolves, %s.\n' "$status"
    ;;
  *)
    printf '%s\n' 'jev is on PATH, but jev auth status gave no answer, so whether a key resolves is unknown.'
    ;;
esac

exit 0
