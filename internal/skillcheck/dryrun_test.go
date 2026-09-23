package skillcheck

import (
	"reflect"
	"testing"
)

func TestDryRunArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tokens []string
		want   []string
		wantOK bool
	}{
		{
			name:   "should strip -o, -r, -q, --usage, --merge and --stats from a command",
			tokens: []string{"jev", "--ask", "a=x", "-o", "json", "-r", "-q", "--usage", "--merge", "--stats"},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name: "should strip --merge-key too, since it implies --merge",
			tokens: []string{
				"jev", "--ask", "a=x", "--merge-key", "answers",
			},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name: "should strip -i, -j, -m, --state and --state-file when the dry run is --print-questions",
			tokens: []string{
				"jev", "--ask", "a=x", "--assert", "a > 0", "-i", "json", "-j", "4", "-m", "jev-latest",
				"--state", "s", "--state-file", "f",
			},
			want:   []string{"--print-questions", "--ask", "a=x", "--assert", "a > 0"},
			wantOK: true,
		},
		{
			name:   "should leave -i, -j, -m, --state and --state-file alone for --print-request",
			tokens: []string{"jev", "--ask", "a=x", "-i", "json", "-j", "4", "-m", "jev-latest", "--state", "s"},
			want: []string{
				"--print-request",
				"--ask", "a=x", "-i", "json", "-j", "4", "-m", "jev-latest", "--state", "s",
			},
			wantOK: true,
		},
		{
			name:   "should strip a flag and its value when the value is separate",
			tokens: []string{"jev", "--ask", "a=x", "-o", "json"},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name:   "should strip a flag and its value when the value is joined with an equals sign",
			tokens: []string{"jev", "--ask", "a=x", "-o=json"},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name:   "should strip a long flag and its value when the value is joined with an equals sign",
			tokens: []string{"jev", "--ask", "a=x", "--output=json"},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name:   "should pick --print-questions for a command carrying --assert",
			tokens: []string{"jev", "--ask", "a=x", "--assert", "a > 0"},
			want:   []string{"--print-questions", "--ask", "a=x", "--assert", "a > 0"},
			wantOK: true,
		},
		{
			name:   "should pick --print-request otherwise",
			tokens: []string{"jev", "--ask", "a=x"},
			want:   []string{"--print-request", "--ask", "a=x"},
			wantOK: true,
		},
		{
			name: "should leave the question, --ask and the shape flags alone",
			tokens: []string{
				"jev", "--ask", "a=x", "--pick", "yes,no", "--rate", "1,2,3", "--desc", "yes=ok",
				"--sep", ";", "--threshold", "0.5", "--min-confidence", "0.5", "--fallback", "no",
				"is this urgent",
			},
			want: []string{
				"--print-request",
				"--ask", "a=x", "--pick", "yes,no", "--rate", "1,2,3", "--desc", "yes=ok",
				"--sep", ";", "--threshold", "0.5", "--min-confidence", "0.5", "--fallback", "no",
				"is this urgent",
			},
			wantOK: true,
		},
		{
			name:   "should insert the print flag at the front even when the rest carries a literal --",
			tokens: []string{"jev", "-o", "json", "--", "-is this urgent"},
			want:   []string{"--print-request", "--", "-is this urgent"},
			wantOK: true,
		},
		{
			name:   "should report a command it cannot parse as a jev invocation as skipped",
			tokens: []string{"jq", "'.value > 0.5'"},
			wantOK: false,
		},
		{
			name:   "should report an empty token list as skipped",
			tokens: nil,
			wantOK: false,
		},
		{
			// Before the mode flag moved to the front, this was the one case provenDryRun caught:
			// a bare --assert immediately followed by the checker's own appended flag was
			// indistinguishable from --assert taking that flag as its value. With the mode flag
			// now leading, nothing is ever appended after --assert, so this no longer trips the
			// invariant. It parses, and --assert is simply left with no value of its own, which
			// pflag itself will refuse to run rather than silently swallowing anything.
			name:   "should no longer need to skip a bare --assert, now that nothing follows it",
			tokens: []string{"jev", "--ask", "a=x", "--assert"},
			want:   []string{"--print-questions", "--ask", "a=x", "--assert"},
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := dryRunArgs(tc.tokens)
			if ok != tc.wantOK {
				t.Fatalf("dryRunArgs(%v) ok = %v, want %v", tc.tokens, ok, tc.wantOK)
			}

			if !tc.wantOK {
				return
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dryRunArgs(%v) = %#v, want %#v", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestDryRunArgsProtectsAnUnstrippedFlagsValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tokens    []string
		wantValue string
	}{
		{
			name:      "should keep --state's value intact when it spells the mode flag",
			tokens:    []string{"jev", "--ask", "a=x", "--state", "--print-request"},
			wantValue: "--print-request",
		},
		{
			name:      "should keep --state's value intact when it spells an always stripped flag",
			tokens:    []string{"jev", "--ask", "a=x", "--state", "--merge"},
			wantValue: "--merge",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := dryRunArgs(tc.tokens)
			if !ok {
				t.Fatalf("dryRunArgs(%v) ok = false, want true", tc.tokens)
			}

			stateAt := -1

			for i, arg := range got {
				if arg == "--state" {
					stateAt = i
				}
			}

			if stateAt == -1 || stateAt+1 >= len(got) || got[stateAt+1] != tc.wantValue {
				t.Fatalf("dryRunArgs(%v) = %#v, want --state immediately followed by %q",
					tc.tokens, got, tc.wantValue)
			}

			if modeCount := countModeFlags(got); modeCount != 1 {
				t.Errorf("dryRunArgs(%v) = %#v, want exactly one dry run flag, counted %d",
					tc.tokens, got, modeCount)
			}
		})
	}
}

// TestDryRunArgsPlacesTheModeFlagWhereNothingCanConsumeIt is the case that motivated moving the
// mode flag to the front rather than the end: --fallback is not in the catalog, so its value gets
// stripped like any unprotected --merge occurrence would, and --fallback ends up as the true last
// argument with nothing after it. Appending the mode flag used to land right there, where
// --fallback would consume it as its own value: `jev --ask a=x --fallback --print-request`
// against the real binary reports `--fallback on a yes/no question takes true, false, yes or no,
// got '--print-request'`, a live call. With the mode flag leading instead, there is nothing left
// for --fallback to consume: `jev --print-request --ask a=x --fallback` reports `flag needs an
// argument: --fallback` and exits 2, verified against the real binary, never a request.
func TestDryRunArgsPlacesTheModeFlagWhereNothingCanConsumeIt(t *testing.T) {
	t.Parallel()

	t.Run("should parse and run --fallback --merge rather than skip it", func(t *testing.T) {
		t.Parallel()

		tokens := []string{"jev", "--ask", "a=x", "--fallback", "--merge"}

		got, ok := dryRunArgs(tokens)
		if !ok {
			t.Fatalf("dryRunArgs(%v) ok = false, want true", tokens)
		}

		want := []string{"--print-request", "--ask", "a=x", "--fallback"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dryRunArgs(%v) = %#v, want %#v", tokens, got, want)
		}

		if got[0] != "--print-request" {
			t.Errorf("dryRunArgs(%v)[0] = %q, want the mode flag at the very front", tokens, got[0])
		}

		if modeCount := countModeFlags(got); modeCount != 1 {
			t.Errorf("dryRunArgs(%v) = %#v, want exactly one dry run flag, counted %d",
				tokens, got, modeCount)
		}
	})
}

func countModeFlags(args []string) int {
	count := 0

	for _, occ := range flagOccurrences(args, catalog) {
		if occ.spec.long == "print-request" || occ.spec.long == "print-questions" {
			count++
		}
	}

	return count
}
