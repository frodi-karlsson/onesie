# Help menu design

Status: approved for implementation planning on 2026-11-07.

Baseline: v0.14.0, commit `9e2f06fc3be77a94a820e34f968a6466c5b170d8`.

## Goal

Make the first help screen useful for choosing a task, while keeping every flag and its constraints available inside the CLI. Reduce the number of decisions presented at once. Preserve the existing command language and execution behavior.

Root help currently lists 55 flags and roughly 1,500 words. Almost 400 words precede Usage. Calibration help has 42 flag entries and roughly 1,400 words. Common operations and rare controls receive equal prominence.

The chosen design is a short overview, focused topic pages, and a complete reference assembled from those same topics. Merely grouping all flags would leave a long first screen. Moving capabilities into new executable subcommands would expand the scope beyond help.

## Scope

Change the root and calibrate help presentation. Add topic navigation beneath the existing help command. Keep existing help for profile, cache, questions, version and completion available. Shared flag descriptions may become shorter on those commands, using their existing command overrides where meanings differ.

Do not add semantic filtering, routing, new execution flags, new settings resolution, or new model calls. Do not remove or rename existing flags. Do not change argument binding, validation, defaults, exit behavior, generated question schemas or output contracts.

All help works without credentials, profile reads, keychain access, stdin reads or network requests. Output remains plain text on stdout. Errors remain on stderr. No required color, interactive menu or automatic pager.

## Content ownership

Each fact has one production definition.

| Content | Owner | Consumers |
| --- | --- | --- |
| Flag name, shorthand, availability and command overrides | Existing setting row and registered flag | Every help view |
| Short flag description | Existing `Row.Help`, resolved through `FlagOn` | Overview, topic and full reference |
| Topic and overview visibility | Help metadata on the setting row | Help selection and grouping |
| Value placeholder | Optional flag help metadata, otherwise the existing kind | Every rendering of that flag |
| Static default | Existing bound default and registered flag | Every rendering of that flag |
| Effective or conditional default | Existing behavior owner | Shared topic explanation |
| Relationships, constraints and examples | One topic content definition | Topic page and full reference |
| Command names and summaries | Existing Cobra command tree | Overview command list |
| Exit codes | Existing CLI exit code tables | Full reference and any selected topic excerpt |

Do not create separate overview, topic and full reference descriptions. The same command flag entry has identical wording in every view. Wrapping may differ only with the available indentation.

Topic prose explains relationships and exceptional behavior. It does not repeat every short flag description in longer wording. When shortening existing help, relocate constraints and caveats into the owning topic rather than deleting them. Preserve the existing distinctions in file replacement, provider precedence, request modes, failures, resume identity and experimental features.

Use existing provider catalogs, kind definitions and limit reporting when presenting enumerations, provider names and numeric limits. Avoid new copied lists of those facts in production help. Detailed limits can remain discoverable through `version`.

Registered defaults supply ordinary flag default annotations. They do not describe every effective default. Topic fragments obtain effective and conditional defaults from the existing behavior owners, including calibration cuts from `internal/calibrate`, the comma separator from delimiter handling, automatic output selection from output handling, and the longer image timeout from the existing request limits. Preserve each of these facts when shortening descriptions. Reuse the existing value or expose it through a small accessor where necessary, rather than placing a copied literal in help or changing runtime defaults to fit rendering.

Static design examples in this specification illustrate the intended output. Production help must be assembled from its owners rather than stored as complete copies of these screens. Golden files are generated expectations for review, not production content sources.

## Root overview

Bare `onesie`, `onesie --help` and `onesie help` display the same root overview. Keep existing behavior for other arguments beside help.

The overview contains, in order:

1. One sentence describing the task.
2. Usage.
3. Three examples, asking a probability, choosing an option, and enriching records.
4. Fifteen common flags, grouped by task.
5. The available command list, including help.
6. Topic navigation and the complete reference command.
7. One connection setup example linking to profile help.

The common flags are `--ask`, `--pick`, `--rate`, `--desc`, `--file`, `--state`, `--state-file`, `--input`, `--map`, `--output`, `--raw`, `--merge`, `--threshold`, `--assert` and `--quiet`.

Use the title Asking for the asking topic, Input for input, Output for output and Decisions for decisions. These same titles appear on the overview and detailed pages. Keep the common entries in the order listed above within their topic. The overview orders these four topics as Asking, Input, Output and Decisions.

Representative opening:

```text
Ask questions about text and use the answers in scripts.

Usage:
  onesie [question] [flags]
  onesie [command]

Examples:
  Get a yes or no probability:
    echo 'The server is down' | onesie 'is this urgent'

  Choose a team:
    onesie 'which team owns this' --pick billing,support \
      --state 'Please refund the duplicate charge'

  Label records while keeping their fields:
    onesie 'is this urgent' -i jsonl --map .text --merge < tickets.jsonl
```

Each displayed example has fixture data in its verification. The text states that the first example returns a probability, rather than implying that a plain question changes the exit status.

The overview output entry includes `auto` among its accepted formats. The Output topic explains that auto uses a table on a terminal, JSON when redirected, and JSON for streams and merged records. Display the existing input default. Defaults come from the registered metadata, rather than being written into repeated descriptions.

The overview remains bounded to the common flag set. Adding a new root flag does not automatically add it to the overview.

## Topic navigation

Root topics are `asking`, `input`, `decisions`, `output`, `connections` and `execution`. The complete root reference is `onesie help all`.

Examples:

```text
onesie help asking
onesie help decisions
onesie help all
onesie help calibrate
onesie help calibrate evaluation
onesie help calibrate all
```

Existing exact command paths retain their meaning. In particular, `onesie help questions` still documents the questions command. Topic names are resolved only beneath help and do not reserve new positional question words on root. `onesie asking` therefore retains its existing question behavior.

Resolve a real command path before interpreting a final topic component. A topic is a single final component. Root topics follow `help` directly. Calibration topics follow `help calibrate`. Reject extra components and unknown topics with exit 2 and a concise hint naming the applicable help overview. Do not silently fall back to root help.

Topic lookup follows the existing command lookup policy for command spellings. Topic names themselves are exact, lowercase names. A collision between a real command path and a topic is a metadata validation failure. Add the supported topics to help argument completion without adding executable root commands.

Successful topic and complete reference requests exit 0. Unknown topics exit 2. The new help paths retain existing handling of Cobra owned help and completion flags.

## Root topic membership

Every visible root flag belongs to exactly one topic. The following membership is the baseline inventory, not a second production flag registry. Implementation records membership on the row that already owns each flag.

| Topic | Flags |
| --- | --- |
| asking | `--ask`, `--for`, `--pick`, `--rate`, `--desc`, `--sep`, `--box`, `--file`, `--replace`, `--print-questions`, `--print-schema` |
| input | `--input`, `--state`, `--state-file`, `--map`, `--id`, `--delimiter`, `--skip-blank`, `--image`, `--image-field` |
| decisions | `--threshold`, `--min-confidence`, `--fallback`, `--assert`, `--abstain-if`, `--quiet`, `--stop-on-assert` |
| output | `--output`, `--raw`, `--out`, `--merge`, `--merge-key`, `--usage`, `--stats`, `--version`, `--help` |
| connections | `--profile`, `--provider`, `--model`, `--base-url`, `--allow-http`, `--list-models` |
| execution | `--jobs`, `--timeout`, `--retries`, `--max-retry-after`, `--long-context`, `--cache`, `--no-dedup`, `--unordered`, `--stop-on-error`, `--resume`, `--prune`, `--mock`, `--print-request` |

Order full topic sections as Asking, Input, Decisions, Output, Connections and Execution. Within a topic, common flags appear first in the overview order, followed by remaining flags in settings table order. Help owned by Cobra is placed after the row owned flags. Do not reorder the settings table to obtain presentation order.

Each topic has its title, flag entries, explanatory content and examples where useful. A standalone topic ends with navigation to other topics and the complete reference. The complete reference concatenates the same topic content, omitting these repeated navigation footers, then renders the root exit code table once. It also includes Usage and the command list, so it is usable alone.

## Decisions content

The agreed short entries include:

```text
--threshold NUMBER   Turn a yes or no probability into a boolean decision.
--assert EXPR        Exit 1 when an answer fails this condition.
-q, --quiet          Suppress output. Without --assert, judge one answer.
```

The same wording appears in the overview, decisions topic and complete reference.

The topic explains these interactions in one place:

1. A threshold adds a boolean decision and does not change the exit code by itself.
2. Without an assertion, quiet mode judges one question on one record. Yes or no uses the configured threshold, defaulting to 0.5. Pick and rate need both minimum confidence and a fallback.
3. Minimum confidence applies only to pick and rate. Fallback values on yes or no questions must be boolean spellings, while pick and rate can use a review destination outside the options.
4. JSON keeps the model value when available. Low confidence and request errors have distinct fallback reasons. Fallback never converts a request failure into a successful run.
5. Assertions determine acceptance. Only when they fail is abstention checked. Quiet mode beside an assertion only suppresses output.
6. Streams print rejected records too. Stop on assertion prints the first rejected record before stopping. Earlier errors still affect the aggregate exit code. Quiet mode refuses streams.
7. Question settings bind to the preceding named question or selected file question. Link to Asking for the single detailed scope explanation.

Render any exit code excerpt from the existing tables. Do not label every exit 0 as accepted without its gate context. In ordinary root runs, exit 0 means answered. Link to calibrate for choosing thresholds from evidence. Any numerical threshold used solely to demonstrate syntax is identified as illustrative.

## Other topic content

Asking owns question shapes, descriptions, grouping and order, saved file lookup, replacement rules and export. It explains the positional question id, the literal question escape with `--`, and the difference between replacing a question and overriding one setting. Box constraints link to Input for image handling.

Input owns record formats, mapping, identifiers, delimiters, blanks and images. It explains what reaches the model and what remains in the original record. Constraints specific to file sizes and depth link to the existing limit report.

Output owns formats, automatic format selection, value versus decision, merging, merge locations, error placement, output files and summaries. It explains that merged JSON keeps answer metadata under `answers` by default, or under the location selected by `--merge-key`, and that raw output does not preserve the original record. Resume behavior lives in Execution, with a link from output files.

Connections owns authentication, provider and profile precedence, key and endpoint pairing, account requirements, local endpoints and transport restrictions. Provider names and variable names come from existing metadata. The overview setup example remains a short entry point into this topic and profile help.

Execution owns concurrency, ordering, retries, failure stops, caching, deduplication, resume, pruning, mock fixtures and request previews. Preserve the warning that resume by id retains an old answer when content changes under the same id. Explain the different coverage of request previews, question export and mock execution. Keep token and model limits discoverable without repeating them in prose.

## Calibration help

`onesie calibrate --help` and `onesie help calibrate` show the same concise calibration overview. Preserve bare calibrate behavior. Lead with comparing answers against labelled examples and the fact that calibrate does not choose a threshold.

Show one minimal labelled record, one live evaluation example and one offline replay example. Clearly distinguish the answer file selected by `--out` from the report format selected by `--output`.

Calibration topics are Asking, Input, Evaluation, Output, Connections and Execution. Their command forms use `onesie help calibrate TOPIC`. Evaluation owns `--label`, `--cuts`, `--require` and `--offline`. Root decision flags remain unavailable on calibrate.

The common calibration flags are `--ask`, `--pick`, `--rate`, `--file`, `--input`, `--map`, `--id`, `--label`, `--cuts`, `--require`, `--offline`, `--out`, `--output` and `--resume`. Group them under their owning topics. Remaining registered flags are available in topic help and `onesie help calibrate all`.

Shared flag entries resolve selected command overrides first so calibration wording, kinds and defaults remain correct. For a flag actually inherited from an ancestor, use the declaring ancestor's bound row metadata when the selected command has no local binding. In particular, profile and provider are inherited root flags on calibrate and have no calibration `FlagOn` binding. Preserve their registration and use the existing settings resolution policy rather than adding duplicate declarations to make help work. The registered command flag remains the authority for actual availability and local shadowing.

Common flag visibility can differ by command. Reuse topic explanation fragments only where the behavior is identical. Command specific evaluation and execution prose has one owner and is reused by the calibration full reference. Do not include root gate explanations on calibration pages.

The complete calibration reference includes the existing calibration exit code table. Preserve details about skipped questions, ignored file policies, label mapping, confidence intervals, requirements, fingerprint mismatches and offline answers. Offline replay validates recorded evidence and does not detect new live model behavior.

## Code structure

The existing rows remain the source of flag truth. Add narrowly scoped help metadata to them, including topic and common visibility. Resolve command specific differences through the existing command override mechanism. Do not reuse `FlagSpec.Group`, which controls question binding.

An optional value placeholder belongs to the flag metadata. This permits `NAME=QUESTION`, `FILE` and `EXPR` without changing parser types or using flag names in a separate switch. Otherwise use the existing kind description. Resolve names, shorthands, defaults and actual visibility from the registered command flags. Help cannot advertise an unavailable flag.

The renderer and topic prose live in the CLI layer, with no new dependency from settingrow into cli. Topic definitions are presentation data with names, titles, explanation fragments and examples. They do not hold copied flag definitions or executable command handlers.

Cobra owned flags such as help receive explicit help metadata where the CLI owns their presentation. They are included in coverage checks. There is no silent catchall that conceals an unclassified flag.

Keep the existing handling of the bare boolean sentinel in help. Rendering must not leave changes in flag parser state. In particular, selecting overview flags must not mutate their Hidden fields on the real command. Use view selection rather than changing command availability.

Use a deterministic plain text layout with descriptions wrapped to 88 columns. Preserve command examples as authored. Do not query terminal width or change content when output is redirected. Rows with unusually long signatures may place the description on the following indented line. Help rendering writes to the command output writer.

Keep ordinary commands on their existing help path. Extend the existing help dispatcher for topics rather than building a parallel command registry. No dependency injection changes are needed beyond existing command writers and constructor supplied dependencies.

## Verification

Use existing help snapshots for root, calibration and existing subcommands. Add topic and complete reference snapshots. Review generated expectations as presentation artifacts.

Add checks that every visible flag on the redesigned commands has exactly one valid topic, every common flag is available on that command, and each complete reference contains every available flag exactly once. Include inherited flags and Cobra owned help. Rows without a CLI flag need no topic.

Verify that a resolved flag description, signature and default are reused consistently across views. Verify that standalone topic content is the same content included in the complete reference, apart from navigation and section indentation. These checks protect composition rather than making another manually maintained expected flag inventory.

Include explicit coverage for calibration's inherited profile and provider flags and command specific output descriptions. Verify that help retains effective calibration cuts, the default delimiter, automatic output selection and the conditional image timeout using their existing value owners.

Exercise help dispatch, existing command paths, topic completion, unknown topics and extra path components. Check that topic names do not become root subcommands. Verify repeated rendering leaves command flags unchanged, including sentinel and visibility state.

Run every executable example using explicit mock fixtures or the already offline preview path. Use an isolated environment with temporary HOME, ONESIE_CONFIG_DIR and XDG_CACHE_HOME. Do not read real keys, real profile files or the OS keychain. Validate expected fields and exit codes, including false thresholds, failed assertions, abstention, low confidence fallback, failed requests and rejected stream records. Do not contact live providers.

Make no new tests for model accuracy. Existing behavior tests remain authoritative for execution. Help examples exercise their promises and the existing golden checks show text changes.

Run the repository checks applicable to implementation, including `make check`, `make skills-check` and `make settings-check`. The already approved isolated environment exception applies to offline skill checks. Check any generated documentation affected by row metadata or description changes. No release or publishing is part of this specification.

## Acceptance

The root overview contains three examples and only the fifteen selected common flags. Calibration has its own concise overview. Every available flag, static default, existing help constraint and command exit code remains reachable inside the CLI.

Users can find how to ask, read records, make decisions, format results, connect and control execution without scanning the entire flag list. Root and calibration complete references remain independently useful.

Changing a flag description updates all views that display it. Changing a topic explanation updates both its topic page and complete reference. New visible flags cannot pass coverage checks without being classified. Help rendering has no effect on execution semantics or parser state.

## Review

A Sol subagent reviewed this specification against the current code. The review found gaps in inherited flag metadata and effective default ownership, and a wording issue about the default merge location. This revision resolves all three. The reviewer confirmed that the topic inventory covers the visible root flags and that the design needs no parallel command registry or additional structural layers.

Implementation planning follows user review of the written specification.
