# Settings lookup

Use `onesie help all` for command flags, `onesie help exit-codes` for exit codes, and `onesie help calibrate all` for calibration.

## What a run uses

Read each row from left to right. The first source that supplies the setting wins.

<!-- settings:run_uses begin -->
| Setting | Flag | Chosen profile | Question file or body | Environment | Default profile |
|---------|------|----------------|-----------------------|-------------|-----------------|
| `assert` | `--assert` |  | the question file's `assert` |  |  |
| `abstain_if` | `--abstain-if` |  | the question file's `abstain_if` |  |  |
| `model` | `--model` | `model` | the request body's `model` | `TYPESAFE_DEFAULT_MODEL` on typesafe | `model` |
| `endpoint` | `--base-url` | `base_url` or `account_id` |  | `ONESIE_BASE_URL`, then `TYPESAFE_BASE_URL` on typesafe, then `CLOUDFLARE_ACCOUNT_ID` on cloudflare | `base_url` or `account_id` |
| `long_context` | `--long-context` |  | the request body's `long_context` | `ONESIE_LONG_CONTEXT` |  |
| `mock` | `--mock` |  |  | `ONESIE_MOCK` |  |
| `cache` | `--cache` |  |  | `ONESIE_CACHE` |  |
| `api_key` |  | `api_key` |  | `TYPESAFE_API_KEY` on typesafe, or `OPENROUTER_API_KEY` on openrouter, or `BERGET_API_KEY` on berget, or `LEVANTO_API_KEY` on levanto, or `CLOUDFLARE_API_TOKEN` on cloudflare, or `AI_GATEWAY_API_KEY` on vercel | `api_key` |
<!-- settings:run_uses end -->

### Choosing a profile

A chosen profile is one selected with `--profile` or `ONESIE_PROFILE`. Run `onesie profile show`
to see which profile, model and endpoint a command will use.

## profiles.json

<!-- settings:profile_members begin -->
| Member | Meaning |
|--------|---------|
| `default` | the profile a run uses when nothing else chooses one. Optional |
| `profiles` | name to profile. May be empty |
| `provider` | `typesafe`, `openrouter`, `berget`, `levanto`, `cloudflare` or `vercel`. Required |
| `api_key` | the key, held in the file |
| `store`, `account` | `keychain` and the keychain item's name, for a key held in the keychain |
| `key_end` | the last four characters of a keychain key of 16 characters or more, so `profile show` tells two keys apart without reading the keychain |
| `base_url` | the API root the profile's runs use |
| `account_id` | cloudflare only, the account, 32 hexadecimal characters |
| `model` | the model a run uses when it names none |
<!-- settings:profile_members end -->

## Environment

<!-- settings:environment begin -->
| Variable | What it does |
|----------|--------------|
| `TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`, `BERGET_API_KEY`, `LEVANTO_API_KEY`, `CLOUDFLARE_API_TOKEN`, `AI_GATEWAY_API_KEY` | the key for each provider. It beats the default profile's key, and a chosen profile's beats it, as in What a run uses |
| `CLOUDFLARE_ACCOUNT_ID` | the Cloudflare account when no flag, chosen profile or base URL variable names one. It beats the default profile's, as in What a run uses |
| `TYPESAFE_DEFAULT_MODEL` | the model on typesafe when neither `-m`, a chosen profile nor a request body names one. It beats the default profile's `model` |
| `ONESIE_BASE_URL` | the API root on every provider when neither `--base-url` nor a chosen profile's `base_url` or `account_id` names one. It beats the default profile's |
| `TYPESAFE_BASE_URL` | the API root on typesafe when neither `--base-url`, a chosen profile nor `ONESIE_BASE_URL` names one, so `ONESIE_BASE_URL` wins. It beats the default profile's |
| `ONESIE_PROFILE` | the profile a run uses when `--profile` is not given. Beside `--provider`, it exits 2 |
| `ONESIE_PROVIDER` | runs without a profile on this provider when neither `--profile` nor `--provider` is given. Set beside `ONESIE_PROFILE`, it exits 2 |
| `ONESIE_CONFIG_DIR` | where `profiles.json` and the saved question files live |
| `ONESIE_MOCK` | a file to answer from, as `--mock`. Skipped beside `--list-models`, `-i request`, `--print-request` and `--print-questions` |
| `ONESIE_CACHE` | `1` turns the response cache on, as `--cache` |
| `ONESIE_CACHE_DIR` | where the response cache lives |
| `ONESIE_CACHE_TTL` | how long a cached answer for a model alias lives, `24h` unless set |
| `ONESIE_LONG_CONTEXT` | `1` or `true` raises the context limits, as `--long-context`. It takes the values the flag takes, and anything else exits 2 where it applies |
<!-- settings:environment end -->

## Question file keys

<!-- settings:question_keys begin -->
| Key | Where | What it does |
|-----|-------|--------------|
| `ask` | a question | the instructions sent to the API, as text or a mapping or sequence of it |
| `yes_means` | a question | what a yes means |
| `no_means` | a question | what a no means |
| `true` | a question | another spelling of yes_means |
| `false` | a question | another spelling of no_means |
| `pick` | a question | the options, as a sequence of names or a mapping of name to description |
| `rate` | a question | the levels from lowest to highest, as names, one key mappings of name to description, or a mapping of name to description |
| `box` | a question | makes this a box question, answered with the edges of what it finds in an image |
| `threshold` | a question | the yes probability a yes or no answer must reach, between 0 and 1 |
| `min_confidence` | a question | the confidence a pick or rate answer must reach, between 0 and 1, or the fallback stands in |
| `fallback` | a question | the answer that stands in for one that falls short |
| `assert` | the file | a gate over every answer, as --assert takes it |
| `abstain_if` | the file | an abstain expression, as --abstain-if takes it |
| `model` | a request body | the model a request body asks, unless -m names one |
| `long_context` | a request body | raises the context limits of the request body. Only true has an effect |
<!-- settings:question_keys end -->

## Servers and CI

A profile keeps its key in the OS keychain unless `--file` stores it in `profiles.json`. A runner
often has no keychain, so give the job a config dir of its own and store each key with `--file`:

```sh
export ONESIE_CONFIG_DIR="$RUNNER_TEMP/onesie"
printf '%s\n' "$OPENROUTER_API_KEY" | onesie profile create prod --provider openrouter --file
printf '%s\n' "$CLOUDFLARE_API_TOKEN" | onesie profile create cf --provider cloudflare --account-id "$CLOUDFLARE_ACCOUNT_ID" --file
onesie --profile cf -f shell-safety -q --state "$cmd"
```

One connection needs no file. Set the provider's key variable and pass `--provider`, or set
`ONESIE_PROVIDER`. A server mounts a `profiles.json` that holds `api_key`, at a mode such as `0400`,
and points `ONESIE_CONFIG_DIR` at its directory.

## Provider notes

The Cloudflare token needs the `Account` > `Workers AI` > `Read` permission.

<a id="streams"></a>
<a id="caching"></a>
<a id="more"></a>

## Exit codes

`onesie help exit-codes` lists every exit code. `onesie help calibrate exit-codes` lists the codes calibration uses.

### Related help

| To look up | Run |
|---|---|
| Exit codes and what each means | `onesie help exit-codes` |
| Gates, thresholds and abstention | `onesie help decisions` |
| Reading records and writing results | `onesie help input` and `onesie help output` |
| Caching and resuming a run | `onesie help execution` |
| Saved questions and built in sets | `onesie help asking` and `onesie questions` |
