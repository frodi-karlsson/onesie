# AGENTS.md

Conventions for this repo. Follow them without being asked.

## Code ordering

**Do** define a unit of code below its user. The entry point goes at the top, helpers below it.
Types follow the same rule. This is the `caller-above-callee` convention.

**Do** use either order when two units reference each other.

**Don't** put a helper above the function that calls it.

```go
// Do
func NewRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{Use: "onesie"}
	root.AddCommand(newVersionCmd(info))
	return root
}

// BuildInfo carries the build metadata stamped into the binary at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}
```

**Do** treat package level `const` and `var` blocks as exempt. They sit directly below the imports,
where Go readers expect them.

Every other package level declaration is held to the rule. Go resolves package level names in any
order, so nothing else forces a helper above its caller.

## Dependency injection

**Do** pass every dependency in through the constructor. A unit takes what it needs as arguments
and reaches for nothing global.

**Do** treat anything nondeterministic as a dependency. The clock, the random source, the
environment, the filesystem, the network. If a test cannot control it, it has to be injectable.

**Do** define the interface where it is consumed, and keep it to the methods that consumer calls.
Accept an interface, return a concrete type.

**Do** supply working defaults in the constructor, so ordinary callers pass nothing and only a test
overrides.

**Don't** use package level mutable state, an `init` that wires things up, or a singleton. Nothing
can swap it in a test, and parallel tests fight over it.

```go
// Do
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

func New(opts ...Option) (*Client, error) {
	c := &Client{clock: systemClock{}, random: rand.Float64}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}

// Don't
func retryDelay(attempt int) time.Duration {
	// A test can only observe this by running it many times and hoping.
	jitter := 1 - rand.Float64()*0.25
	return time.Duration(float64(backoff(attempt)) * jitter)
}
```

## Comments

**Do** ask first whether a rename or a restructure makes the comment unnecessary. A comment that
explains what the code does is a naming problem.

**Don't** write an inline comment unless the code cannot be understood without it. The cases that
earn one are the reasons living outside the code, a library quirk or a deliberate tradeoff.

**Do** write doc comments on exported declarations. Keep them to one or two lines, not counting an
example.

**Don't** doc comment an unexported declaration. Its name and its body are the documentation.

```go
// Do
if worthReporting(err) {
	fmt.Fprintln(os.Stderr, "onesie:", err)
}

func worthReporting(err error) bool {
	return !errors.Is(err, context.Canceled)
}

// Don't
// A cancelled context means the user interrupted the run, which is not an
// error worth printing.
if !errors.Is(err, context.Canceled) {
	fmt.Fprintln(os.Stderr, "onesie:", err)
}
```

## Tests

**Do** start every test name with `should `. In Go that is the subtest name, since the function
name has to start with `Test`.

**Do** name the test function exactly after the code unit it covers. `NewRootCmd` is covered by
`TestNewRootCmd`.

**Don't** write a flat test function with no subtests.

```go
// Do
func TestNewRootCmd(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "should print help when given no arguments"},
		{name: "should fail on an unknown command"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {})
	}
}

// Don't
func TestRootCmd(t *testing.T) {
	t.Run("bare invocation prints help", func(t *testing.T) {})
}
```

## Writing

This applies to docs, comments, commit messages and anything else written in English.

**Do** use only these punctuation marks: `,` `.` `!` `:`

**Do** use markdown for formatting.

**Don't** use dashes, semicolons, parentheses or question marks in a sentence. Split the sentence in
two, or use a comma.

```
Do:    Build metadata is stamped at link time, so nothing reads it at runtime.
Don't: Build metadata is stamped at link time (via ldflags) — nothing reads it at runtime.
```

**Do** keep identifiers and tool names as they are spelled. `golangci-lint`, `homebrew-tap` and
`go-version-file` are names, not hyphenated prose.

## Commits

**Do** write semantic commit messages. Start with a type, then a colon, then what changed. Types:
`feat`, `fix`, `chore`, `test`, `docs`, `refactor`, `perf`, `build`, `ci`.

**Do** keep a commit message to one line.

**Don't** add a body, a bullet list, or a trailing attribution block.

```
Do:    feat: add version subcommand
Do:    fix: print command output to stdout instead of stderr
Do:    ci: pin golangci-lint installer by checksum
Don't: Added a version command

       - reports commit and build date
       - covered by a table test
```
