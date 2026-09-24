#!/usr/bin/env sh
# SessionStart hook: tells the agent whether onesie is on PATH and whether a key resolves.
# Never blocks session start, so every failure exits 0.

if ! command -v onesie >/dev/null 2>&1; then
  printf '%s\n' 'onesie is not on PATH, so the onesie skills cannot run any command yet. Load the onesie-setup skill and walk the user through installing it.'
  exit 0
fi

# onesie auth status exits 3 when no key resolves, which is an answer rather than a failure.
status=$(onesie auth status </dev/null 2>&1)
provider=$(printf '%s\n' "$status" | sed -n 's/^provider: //p')
source=$(printf '%s\n' "$status" | sed -n 's/^source: //p')

case "$source" in
  none)
    case "$provider" in
      openrouter) variable=OPENROUTER_API_KEY ;;
      *) variable=TYPESAFE_API_KEY ;;
    esac
    printf 'onesie is on PATH, but no API key resolves for provider %s. Only --print-request and --print-questions work until the user runs onesie auth set or sets %s. The onesie-setup skill walks them through it.\n' "$provider" "$variable"
    ;;
  ?*)
    printf 'onesie is on PATH and a key resolves, provider: %s, source: %s.\n' "$provider" "$source"
    ;;
  *)
    if [ -z "$status" ]; then
      printf '%s\n' 'onesie is on PATH, but onesie auth status gave no answer, so whether a key resolves is unknown.'
    else
      printf 'onesie is on PATH, but onesie auth status failed with: %s\n' "$(printf '%s\n' "$status" | head -n 1)"
    fi
    ;;
esac

exit 0
