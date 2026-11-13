package answersfile

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestFingerprint(t *testing.T) {
	t.Parallel()

	questions := []plan.Question{{ID: "urgent", Policy: plan.Policy{Fallback: &plan.Fallback{Text: "no"}}}}
	wired := json.RawMessage(`[]`)
	inputs := FingerprintInputs{Provider: "typesafe", Model: "m", Output: "csv", Input: "lines"}

	t.Run("should sum each part of the run after the version", func(t *testing.T) {
		t.Parallel()

		got, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		names := []string{}
		for _, part := range strings.Split(strings.TrimPrefix(got.String(), "v5:"), ",") {
			name, sum, _ := strings.Cut(part, "=")
			if len(sum) != 2*partSumLength {
				t.Errorf("part %s has the sum %q, want %d hex digits", name, sum, 2*partSumLength)
			}

			names = append(names, name)
		}

		want := "questions policies provider model map id output input merge_key assert abstain_if fallback_columns"
		if strings.Join(names, " ") != want {
			t.Errorf("parts = %v, want %s", names, want)
		}
	})

	t.Run("should leave a fingerprint with no delimiter as it was", func(t *testing.T) {
		t.Parallel()

		got, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(got.String(), "delimiter=") {
			t.Errorf("fingerprint %q carries a delimiter part, want none when no delimiter was given", got)
		}
	})

	t.Run("should fingerprint a given comma as no delimiter at all", func(t *testing.T) {
		t.Parallel()

		comma := inputs
		comma.Delimiter = input.Delimiter{Comma: ','}

		given, err := Fingerprint(questions, wired, comma)
		if err != nil {
			t.Fatal(err)
		}

		none, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		if given.String() != none.String() {
			t.Errorf("fingerprint = %q under a given comma, want %q as with no delimiter", given, none)
		}
	})

	t.Run("should change the fingerprint when the delimiter changes", func(t *testing.T) {
		t.Parallel()

		semicolon, auto := inputs, inputs
		semicolon.Delimiter, auto.Delimiter = input.Delimiter{Comma: ';'}, input.Delimiter{Detect: true}

		first, err := Fingerprint(questions, wired, semicolon)
		if err != nil {
			t.Fatal(err)
		}

		second, err := Fingerprint(questions, wired, auto)
		if err != nil {
			t.Fatal(err)
		}

		if first.String() == second.String() {
			t.Errorf("fingerprint %q is the same under ; and auto, want them apart", first)
		}
	})

	t.Run("should fit a fingerprint with every part set in the bytes the sidecar read takes", func(t *testing.T) {
		t.Parallel()

		every := FingerprintInputs{
			Provider: "typesafe", Model: "m", MapSource: ".body", IDSource: ".id", Output: "csv", Input: "csv",
			MergeKey: "onesie", Assert: "urgent", AbstainIf: "urgent > 0.5", SkipBlank: true,
			Images: []string{"square.png"}, Delimiter: input.Delimiter{Comma: ';'}, LongContext: true, Mock: true,
			BaseURL: "https://onesie.example.com/v1", AccountID: strings.Repeat("ab", 16), Usage: true,
		}

		got, err := Fingerprint(questions, wired, every)
		if err != nil {
			t.Fatal(err)
		}

		written := got.String() + "\n"
		if parts := strings.Count(written, "="); parts != reflect.TypeFor[runPrint]().NumField() {
			t.Errorf("fingerprint %q has %d parts, want every part of the run set", written, parts)
		}

		if len(written) >= maxSidecarBytes {
			t.Errorf("fingerprint %q is %d bytes, want fewer than the %d the sidecar read takes",
				written, len(written), maxSidecarBytes)
		}
	})

	sumChanges := []struct {
		name   string
		change func(*FingerprintInputs)
		part   string
	}{
		{name: "should change the sum when the base URL changes", change: func(in *FingerprintInputs) {
			in.BaseURL = "https://onesie.example.com"
		}, part: "base_url"},
		{name: "should change the sum when the cloudflare account changes", change: func(in *FingerprintInputs) {
			in.AccountID = strings.Repeat("ab", 16)
		}, part: "account_id"},
		{name: "should change the sum when usage is turned on", change: func(in *FingerprintInputs) {
			in.Usage = true
		}, part: "usage"},
	}

	for _, tc := range sumChanges {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			changed := inputs
			tc.change(&changed)

			before, err := Fingerprint(questions, wired, inputs)
			if err != nil {
				t.Fatal(err)
			}

			after, err := Fingerprint(questions, wired, changed)
			if err != nil {
				t.Fatal(err)
			}

			if got := after.changedFrom(sumsOf(t, before)); !reflect.DeepEqual(got, []string{partLabel(tc.part)}) {
				t.Errorf("changed = %q, want only %q", got, partLabel(tc.part))
			}

			if partLabel(tc.part) == "" {
				t.Errorf("part %s has no label for the stale message", tc.part)
			}
		})
	}

	t.Run("should leave the sum alone for the provider's own base URL", func(t *testing.T) {
		t.Parallel()

		got, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		for _, part := range []string{"base_url=", "account_id=", "usage="} {
			if strings.Contains(got.String(), part) {
				t.Errorf("fingerprint %q carries %s, want none for a run on the defaults", got, part)
			}
		}
	})
}

func TestStaleReason(t *testing.T) {
	t.Parallel()

	current := Print{
		parts: []partSum{{name: "questions", sum: strings.Repeat("ab", 8)}, {name: "model", sum: strings.Repeat("cd", 8)}},
	}

	tests := []struct {
		name        string
		stored      string
		found       bool
		want        StaleReason
		wantChanged []string
		wantStale   bool
	}{
		{name: "should accept the run's own fingerprint", stored: current.String(), found: true},
		{name: "should refuse a file with no fingerprint", want: NoFingerprint, wantStale: true},
		{
			name: "should refuse a fingerprint onesie did not write", stored: "not a fingerprint", found: true,
			want: ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse a version 5 fingerprint with no parts", stored: "v5:" + strings.Repeat("0", 64),
			found: true, want: ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse a version 5 fingerprint that names a part twice", found: true,
			stored: "v5:model=" + strings.Repeat("cd", 8) + ",model=" + strings.Repeat("cd", 8),
			want:   ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse a version 5 fingerprint that names a part onesie does not write", found: true,
			stored: "v5:questions=" + strings.Repeat("ab", 8) + ",colour=" + strings.Repeat("cd", 8),
			want:   ForeignFingerprint, wantStale: true,
		},
		{
			name: "should accept the run's own fingerprint written in uppercase hex", found: true,
			stored: "v5:questions=" + strings.Repeat("AB", 8) + ",model=" + strings.Repeat("CD", 8),
		},
		{
			name: "should refuse an older version", stored: "v4:model=" + strings.Repeat("ab", 8), found: true,
			want: OlderFingerprint, wantStale: true,
		},
		{name: "should refuse a newer version", stored: "v6:x", found: true, want: NewerFingerprint, wantStale: true},
		{
			name: "should name the part that changed", found: true,
			stored: "v5:questions=" + strings.Repeat("ab", 8) + ",model=" + strings.Repeat("00", 8),
			want:   ChangedRun, wantChanged: []string{"the model"}, wantStale: true,
		},
		{
			name: "should name every part that changed, a part the file lacks and one the run lacks", found: true,
			stored: "v5:questions=" + strings.Repeat("00", 8) + ",mock=" + strings.Repeat("00", 8),
			want:   ChangedRun, wantChanged: []string{"the questions", "the model", "--mock"}, wantStale: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, changed, stale := staleReason(tc.stored, tc.found, current)
			if got != tc.want || stale != tc.wantStale || strings.Join(changed, ", ") != strings.Join(tc.wantChanged, ", ") {
				t.Errorf("staleReason = %v, %v, %v, want %v, %v, %v", got, changed, stale, tc.want, tc.wantChanged, tc.wantStale)
			}
		})
	}
}

func TestPrint_Matches(t *testing.T) {
	t.Parallel()

	current := Print{
		parts: []partSum{{name: "questions", sum: strings.Repeat("ab", 8)}, {name: "model", sum: strings.Repeat("cd", 8)}},
	}

	tests := []struct {
		name   string
		stored string
		want   bool
	}{
		{name: "should match the run's own fingerprint", stored: current.String(), want: true},
		{name: "should not match a run whose model changed", stored: "v5:questions=" + strings.Repeat("ab", 8) + ",model=" + strings.Repeat("00", 8)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := current.Matches(tc.stored); got != tc.want {
				t.Errorf("Matches(%q) = %v, want %v", tc.stored, got, tc.want)
			}
		})
	}
}

func TestStaleAnswersError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		changed   []string
		wantError string
		wantCause string
	}{
		{
			name:      "should name one part",
			changed:   []string{"-o"},
			wantError: "-o changed since A was written",
			wantCause: "-o changed since it was written",
		},
		{
			name:      "should join the last of several parts with and",
			changed:   []string{"the model", "--map", "--assert"},
			wantError: "the model, --map and --assert changed since A was written",
			wantCause: "the model, --map and --assert changed since it was written",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := &StaleAnswersError{Path: "A", Sidecar: "A.onesie", Reason: ChangedRun, Changed: tc.changed}
			if err.Error() != tc.wantError || err.Cause() != tc.wantCause {
				t.Errorf("Error = %q, Cause = %q, want %q and %q", err.Error(), err.Cause(), tc.wantError, tc.wantCause)
			}
		})
	}
}

func sumsOf(t *testing.T, fingerprint Print) map[string]string {
	t.Helper()

	_, sums, ok := parsePrint(fingerprint.String())
	if !ok {
		t.Fatalf("parsing the fingerprint %q", fingerprint)
	}

	return sums
}

func TestDifferences(t *testing.T) {
	t.Parallel()

	print := func(t *testing.T, inputs FingerprintInputs, question string) string {
		t.Helper()

		got, err := Fingerprint([]plan.Question{{ID: "urgent"}}, json.RawMessage(question), inputs)
		if err != nil {
			t.Fatal(err)
		}

		return got.String()
	}

	march := FingerprintInputs{Provider: "typesafe", Model: "jev-latest", Output: "json", Input: "jsonl"}
	november := march
	november.Model = "jev-1.15.0"

	tests := []struct {
		name      string
		baseline  func(t *testing.T) string
		other     func(t *testing.T) string
		want      []string
		wantKnown bool
	}{
		{
			name:      "should name nothing for two runs of the same settings",
			baseline:  func(t *testing.T) string { return print(t, march, `[]`) },
			other:     func(t *testing.T) string { return print(t, march, `[]`) },
			wantKnown: true,
		},
		{
			name:     "should name each part that differs, in the order a fingerprint writes them",
			baseline: func(t *testing.T) string { return print(t, march, `[]`) },
			other:    func(t *testing.T) string { return print(t, november, `[{"q":1}]`) },
			want:     []string{"the questions", "the model"}, wantKnown: true,
		},
		{
			name:     "should name a part only one side carries",
			baseline: func(t *testing.T) string { return print(t, march, `[]`) },
			other: func(t *testing.T) string {
				longer := march
				longer.LongContext = true

				return print(t, longer, `[]`)
			},
			want: []string{"--long-context"}, wantKnown: true,
		},
		{
			name:     "should not compare a fingerprint of an older version",
			baseline: func(*testing.T) string { return "v4:model=" + strings.Repeat("ab", partSumLength) },
			other:    func(t *testing.T) string { return print(t, march, `[]`) },
		},
		{
			name:     "should not compare a fingerprint onesie did not write",
			baseline: func(*testing.T) string { return "made up" },
			other:    func(t *testing.T) string { return print(t, march, `[]`) },
		},
		{
			name:     "should not compare a fingerprint of a newer version",
			baseline: func(t *testing.T) string { return print(t, march, `[]`) },
			other:    func(*testing.T) string { return "v9:model=" + strings.Repeat("ab", partSumLength) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, known := Differences(tc.baseline(t), tc.other(t))
			if known != tc.wantKnown || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Differences = %q, %v, want %q, %v", got, known, tc.want, tc.wantKnown)
			}
		})
	}

	t.Run("should list every part a fingerprint can write, in its order", func(t *testing.T) {
		t.Parallel()

		var names []string

		fields := reflect.TypeFor[runPrint]()
		for field := range fields.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			names = append(names, name)

			if partLabel(name) == "" {
				t.Errorf("part %s has no label", name)
			}
		}

		if !reflect.DeepEqual(names, partOrder) {
			t.Errorf("partOrder = %v, want the runPrint fields %v", partOrder, names)
		}
	})
}
