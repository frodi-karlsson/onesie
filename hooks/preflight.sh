#!/usr/bin/env sh
# SessionStart hook: tells the agent whether onesie is on PATH and whether a key resolves.
# Never blocks session start, so every failure exits 0.

if ! command -v onesie >/dev/null 2>&1; then
  printf '%s\n' 'onesie is not on PATH, so the onesie skills cannot run any command yet. Ask the user to install it with: go install github.com/frodi-karlsson/onesie/cmd/onesie@latest, and to put the Go bin directory on PATH.'
  exit 0
fi

# onesie auth status exits 3 when no key resolves, which is an answer rather than a failure.
status=$(onesie auth status </dev/null 2>&1)

case "$status" in
  'source: none'*)
    printf '%s\n' 'onesie is on PATH, but no API key resolves. Only --print-request and --print-questions work until the user runs onesie auth set or sets TYPESAFE_API_KEY.'
    ;;
  'source: '?*)
    printf 'onesie is on PATH and a key resolves, %s.\n' "$status"
    ;;
  '')
    printf '%s\n' 'onesie is on PATH, but onesie auth status gave no answer, so whether a key resolves is unknown.'
    ;;
  *)
    printf 'onesie is on PATH, but onesie auth status failed with: %s\n' "$(printf '%s\n' "$status" | head -n 1)"
    ;;
esac

exit 0
