package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestOpenOut(t *testing.T) {
	t.Parallel()

	const input = "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n{\"id\":4}\n"

	const bodies = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n" +
		"{\"id\":3,\"body\":\"c\"}\n{\"id\":4,\"body\":\"d\"}\n"

	valuesFor := func(question, provider, model, mapSource, idSource string) string {
		return fingerprintWith(t, plan.Source{Positional: question}, answersfile.FingerprintInputs{
			Provider: provider, Model: model, MapSource: mapSource, IDSource: idSource, Output: "values",
			Input: "jsonl",
		})
	}

	matching := valuesFor("is this urgent", "typesafe", onesie.DefaultModel, "", "")
	picking := fingerprintWith(t, plan.Source{
		Positional: "which team",
		Events: []argv.Event{
			{Name: "pick", Value: "billing,technical"}, {Name: "fallback", Value: "human"},
		},
	}, answersfile.FingerprintInputs{Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "jsonl"})
	merged := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
		Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "jsonl",
		MergeKey: "answers",
	})
	fromLines := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
		Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "lines",
	})
	gated := func(assert, abstainIf string) string {
		return fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
			Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "jsonl",
			Assert: assert, AbstainIf: abstainIf,
		})
	}
	changed := "the questions, flags or gate changed since"
	malformed := "does not hold a fingerprint onesie wrote"

	tests := []struct {
		name        string
		existing    string
		noFile      bool
		sidecar     string
		args        []string
		stdin       string
		wantCode    int
		wantFile    string
		wantSidecar string
		wantErr     string
		wantCalls   int32
		unreached   bool
		env         map[string]string
		bare        bool
		// questions, when set, is written to a question file passed with -f.
		questions string

		emptySidecar bool
		sidecarDir   bool
		readOnlyDir  bool
		readOnlyFile bool
		// wantMode, when set, is the mode a file this run created must have.
		wantMode         os.FileMode
		wantEmptySidecar bool
	}{
		{
			name:        "should write the answers to the file and nothing to stdout",
			noFile:      true,
			wantMode:    0o600,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls:   4,
		},
		{
			name:        "should skip the records the file already answers",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should drop a line the earlier run was cut off in",
			existing:    "old one\nold two\nold thr",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should start fresh when the file does not exist yet",
			noFile:      true,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls:   4,
		},
		{
			name:        "should leave a finished file as it is",
			existing:    "a\nb\nc\nd\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "a\nb\nc\nd\n",
		},
		{
			name:        "should empty the file when there is nothing to answer",
			existing:    "stale\n",
			sidecar:     "stale fingerprint",
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl"},
			wantFile:    "",
		},
		{
			name:        "should leave the file untouched when the command is rejected",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "--resume"},
			stdin:       "one record",
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
		},
		{
			name:        "should leave the file untouched when the run fails before any answer",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "-q"},
			stdin:       "the site is down",
			wantCode:    ExitTransport,
			wantFile:    "keep me\n",
			unreached:   true,
		},
		{
			name:        "should leave the file untouched on a rejected fresh run",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "-i", "jsonl", "--unordered", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
		},
		{
			name:     "should write the fingerprint beside the file on a fresh run",
			existing: "stale\n",
			args: []string{
				"is this urgent", "-i", "jsonl", "-m", "m1", "--map", ".body", "--id", ".id",
			},
			stdin:       bodies,
			wantFile:    idLines(1, 4),
			wantSidecar: valuesFor("is this urgent", "typesafe", "m1", ".body", ".id"),
			wantCalls:   4,
		},
		{
			name:     "should resume when the fingerprint matches every setting",
			existing: idLines(1, 2),
			sidecar:  valuesFor("is this urgent", "typesafe", "m1", ".body", ".id"),
			args: []string{
				"is this urgent", "-i", "jsonl", "-m", "m1", "--map", ".body", "--id", ".id", "--resume",
			},
			stdin:       bodies,
			wantFile:    idLines(1, 4),
			wantSidecar: valuesFor("is this urgent", "typesafe", "m1", ".body", ".id"),
			wantCalls:   2,
		},
		{
			name:        "should refuse to resume when the questions changed",
			existing:    "keep me\n",
			sidecar:     valuesFor("is this critical", "typesafe", onesie.DefaultModel, "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: valuesFor("is this critical", "typesafe", onesie.DefaultModel, "", ""),
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the model changed",
			existing:    "keep me\n",
			sidecar:     valuesFor("is this urgent", "typesafe", "a", "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "-m", "b", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: valuesFor("is this urgent", "typesafe", "a", "", ""),
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --map changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--map", ".body", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --id changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--id", ".id", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the output mode changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-o", "json", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the input mode changed",
			existing:    "keep me\n",
			sidecar:     fromLines,
			args:        []string{"is this urgent", "-o", "values", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: fromLines,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume by line count when --skip-blank was added",
			existing:    "{\"answer\":0.5}\n{\"error\":\"blank line\"}\n",
			sidecar:     fromLines,
			args:        []string{"is this urgent", "-i", "lines", "--skip-blank", "--resume"},
			stdin:       "a\n\nb\n\nc\n",
			wantCode:    ExitUsage,
			wantFile:    "{\"answer\":0.5}\n{\"error\":\"blank line\"}\n",
			wantSidecar: fromLines,
			wantErr:     changed,
		},
		{
			name:     "should refuse to resume by line count when --skip-blank was dropped",
			existing: "{\"answer\":0.5}\n",
			sidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "lines",
				SkipBlank: true,
			}),
			args:     []string{"is this urgent", "-i", "lines", "--resume"},
			stdin:    "a\n\nb\n\nc\n",
			wantCode: ExitUsage,
			wantFile: "{\"answer\":0.5}\n",
			wantSidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "lines",
				SkipBlank: true,
			}),
			wantErr: changed,
		},
		{
			name:     "should resume csv by row count when --skip-blank changed",
			existing: "{\"answer\":0.5}\n",
			sidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "csv",
			}),
			args:     []string{"is this urgent", "-i", "csv", "--skip-blank", "--resume"},
			stdin:    "body\na\n\nb\n",
			wantFile: "{\"answer\":0.5}\n{\"answer\":0.5}\n",
			wantSidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.DefaultModel, Output: "values", Input: "csv",
			}),
			wantCalls: 1,
		},
		{
			name:     "should resume by id when --skip-blank changed",
			existing: idLines(1, 2),
			sidecar:  valuesFor("is this urgent", "typesafe", onesie.DefaultModel, "", ".id"),
			args: []string{
				"is this urgent", "-i", "jsonl", "--id", ".id", "--skip-blank", "--resume",
			},
			stdin:       "{\"id\":1}\n\n{\"id\":2}\n\n{\"id\":3}\n{\"id\":4}\n",
			wantFile:    idLines(1, 4),
			wantSidecar: valuesFor("is this urgent", "typesafe", onesie.DefaultModel, "", ".id"),
			wantCalls:   2,
		},
		{
			name:        "should refuse to resume when --merge changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--merge", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --merge-key changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--merge-key", "verdict", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --threshold changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--threshold", "0.7", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:     "should refuse to resume when --min-confidence changed",
			existing: "keep me\n",
			sidecar:  picking,
			args: []string{
				"which team", "--pick", "billing,technical", "--fallback", "human", "--min-confidence", "0.7",
				"-i", "jsonl", "--resume",
			},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: picking,
			wantErr:     changed,
		},
		{
			name:        "should resume when -o auto resolves to the json mode a stream wrote",
			existing:    "old one\nold two\n",
			sidecar:     fingerprintFor(t, "is this urgent", "typesafe", onesie.DefaultModel, "", ""),
			wantSidecar: fingerprintFor(t, "is this urgent", "typesafe", onesie.DefaultModel, "", ""),
			args:        []string{"is this urgent", "-o", "auto", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should resume when --merge-key answers names the key --merge wrote",
			existing:    "old one\nold two\n",
			sidecar:     merged,
			wantSidecar: merged,
			args:        []string{"is this urgent", "--merge-key", "answers", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile: "old one\nold two\n" +
				"{\"id\":3,\"answers\":{\"answer\":0.5}}\n{\"id\":4,\"answers\":{\"answer\":0.5}}\n",
			wantCalls: 2,
		},
		{
			name:        "should refuse to resume when --fallback changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--fallback", "no", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --assert changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--assert", "answer.value > 0.9", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --abstain-if changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should resume when a question file's assert is the one the file was written under",
			existing:    "old one\nold two\n",
			sidecar:     gated("answer.value > 0.4", ""),
			wantSidecar: gated("answer.value > 0.4", ""),
			questions:   "assert: answer.value > 0.4\n",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should refuse to resume when a question file's assert changed",
			existing:    "keep me\n",
			sidecar:     gated("answer.value > 0.4", ""),
			wantSidecar: gated("answer.value > 0.4", ""),
			questions:   "assert: answer.value > 0.9\n",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when a question file's abstain_if changed",
			existing:    "keep me\n",
			sidecar:     gated("answer.value > 0.9", "answer.value > 0.4"),
			wantSidecar: gated("answer.value > 0.9", "answer.value > 0.4"),
			questions:   "assert: answer.value > 0.9\nabstain_if: answer.value > 0.3\n",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantErr:     changed,
		},
		{
			name:     "should refuse to resume a file with no fingerprint beside it",
			existing: "keep me\n",
			args:     []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:    input,
			wantCode: ExitUsage,
			wantFile: "keep me\n",
			wantErr:  "has no fingerprint beside it",
		},
		{
			name:        "should resume an empty file with no fingerprint beside it",
			existing:    "",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantSidecar: matching,
			wantCalls:   4,
		},
		{
			name:        "should refuse to resume when the provider changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--provider", "openrouter", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the provider changed to berget",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--provider", "berget", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the provider changed to levanto",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--provider", "levanto", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --image-field was added",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--image-field", "photo", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the default model changed",
			existing:    "keep me\n",
			sidecar:     matching,
			env:         map[string]string{onesie.EnvDefaultModel: "jev-next"},
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should resume when -m names the default model a plain run used",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "-m", onesie.DefaultModel, "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should resume without rewriting a fingerprint that matches",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			readOnlyDir: true,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:         "should refuse a file it cannot write before any request",
			existing:     "keep me\n",
			readOnlyFile: true,
			args:         []string{"is this urgent", "-i", "jsonl"},
			stdin:        input,
			wantCode:     ExitUsage,
			wantFile:     "keep me\n",
			wantErr:      "answers.jsonl",
		},
		{
			name:         "should refuse to resume into a file it cannot write before any request",
			existing:     "old one\n",
			sidecar:      matching,
			wantSidecar:  matching,
			readOnlyFile: true,
			args:         []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:        input,
			wantCode:     ExitUsage,
			wantFile:     "old one\n",
			wantErr:      "answers.jsonl",
		},
		{
			name:        "should refuse a fresh run beside a fingerprint it cannot replace before any request",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			readOnlyDir: true,
			args:        []string{"is this urgent", "-i", "jsonl"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantErr:     "answers.jsonl.onesie",
		},
		{
			name:       "should refuse to resume beside a fingerprint it cannot read",
			existing:   "keep me\n",
			sidecarDir: true,
			args:       []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:      input,
			wantCode:   ExitUsage,
			wantFile:   "keep me\n",
			wantErr:    "answers.jsonl.onesie: ",
		},
		{
			name:        "should refuse to resume beside a malformed fingerprint",
			existing:    "keep me\n",
			sidecar:     "garbage",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "garbage",
			wantErr:     "answers.jsonl.onesie " + malformed,
		},
		{
			name:        "should refuse to resume beside a truncated fingerprint",
			existing:    "keep me\n",
			sidecar:     matching[:10],
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching[:10],
			wantErr:     malformed,
		},
		{
			name:             "should refuse to resume beside a fingerprint an interrupted resume left empty",
			existing:         "keep me\n",
			emptySidecar:     true,
			args:             []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:            input,
			wantCode:         ExitUsage,
			wantFile:         "keep me\n",
			wantEmptySidecar: true,
			wantErr:          malformed,
		},
		{
			name:        "should say an older onesie wrote a fingerprint of an earlier version",
			existing:    "keep me\n",
			sidecar:     "v0:" + strings.Repeat("0", 64),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "v0:" + strings.Repeat("0", 64),
			wantErr:     "an older onesie wrote",
		},
		{
			name:        "should say an older onesie wrote a fingerprint that leaves out the flags and the gate",
			existing:    "keep me\n",
			sidecar:     "v1:1ac939d902a081db7010b65849b69fc0a88cfa4373d81bd466316aae7dd6caef",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "v1:1ac939d902a081db7010b65849b69fc0a88cfa4373d81bd466316aae7dd6caef",
			wantErr:     "an older onesie wrote",
		},
		{
			name:        "should say a newer onesie wrote a fingerprint of a later version",
			existing:    "keep me\n",
			sidecar:     "v3:" + strings.Repeat("0", 64),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "v3:" + strings.Repeat("0", 64),
			wantErr:     "a newer onesie wrote",
		},
		{
			name:     "should write no fingerprint beside a question file",
			existing: "stale\n",
			sidecar:  matching,
			bare:     true,
			args:     []string{"--ask", "urgent=is this urgent", "--print-questions"},
			wantFile: "# yaml-language-server: $schema=" +
				"https://raw.githubusercontent.com/frodi-karlsson/onesie/main/schema/questions.json\n" +
				"urgent:\n  ask: is this urgent\n",
		},
		{
			name:      "should write no fingerprint beside a list of models",
			existing:  "stale\n",
			sidecar:   matching,
			bare:      true,
			args:      []string{"--list-models"},
			wantFile:  "jev-latest  d  r\n",
			wantCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.readOnlyDir && tc.wantCode != ExitOK && runtime.GOOS == "windows" {
				t.Skip("a read only directory on windows still lets a file be created in it")
			}

			var calls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")

				body := `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`
				if r.URL.Path == "/v1/models" {
					body = `{"models":[{"name":"jev-latest","description":"d","release_date":"r"}]}`
				}

				if _, err := io.WriteString(w, body); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer srv.Close()

			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if !tc.noFile {
				if err := os.WriteFile(path, []byte(tc.existing), 0o600); err != nil {
					t.Fatalf("writing the existing file: %v", err)
				}
			}

			writeSidecar(t, path+".onesie", tc.sidecar, tc.emptySidecar, tc.sidecarDir)

			if tc.readOnlyFile {
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatalf("making the file read only: %v", err)
				}
			}

			if tc.readOnlyDir {
				lockDir(t, filepath.Dir(path))
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				withKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(tc.env)),
				WithClientFactory(stubFactory(baseFor(srv.URL, tc.unreached))),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			global := []string{"--out", path, "-o", "values"}
			if tc.bare {
				global = global[:2]
			}

			args := tc.args
			if tc.questions != "" {
				questions := filepath.Join(t.TempDir(), "questions.yaml")
				if err := os.WriteFile(questions, []byte(tc.questions), 0o600); err != nil {
					t.Fatalf("writing the question file: %v", err)
				}

				args = append(slices.Clone(args), "-f", questions)
			}

			root.SetArgs(append(global, args...))

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if out.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", out.String())
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			if string(data) != tc.wantFile {
				t.Errorf("file = %q, want %q", data, tc.wantFile)
			}

			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("requests = %d, want %d", got, tc.wantCalls)
			}

			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}

			if tc.wantMode != 0 && runtime.GOOS != "windows" {
				assertMode(t, path, tc.wantMode)
				assertMode(t, path+".onesie", tc.wantMode)
			}

			sidecar, err := os.ReadFile(path + ".onesie")

			switch {
			case tc.sidecarDir:
			case tc.wantEmptySidecar && (err != nil || len(sidecar) != 0):
				t.Errorf("fingerprint file = %q, %v, want it left empty", sidecar, err)
			case tc.wantEmptySidecar:
			case tc.wantSidecar == "" && !os.IsNotExist(err):
				t.Errorf("fingerprint file = %q, %v, want none", sidecar, err)
			case tc.wantSidecar != "" && string(sidecar) != tc.wantSidecar+"\n":
				t.Errorf("fingerprint file = %q, %v, want %q", sidecar, err, tc.wantSidecar+"\n")
			}
		})
	}

	specials := []struct {
		name      string
		path      func(t *testing.T) string
		args      []string
		wantCode  int
		wantErr   string
		wantCalls int32
	}{
		{
			name:      "should write to a device with no fingerprint beside it",
			path:      func(*testing.T) string { return "/dev/null" },
			args:      []string{"is this urgent", "-i", "jsonl"},
			wantCalls: 4,
		},
		{
			name:     "should refuse to resume from a device before any request",
			path:     func(*testing.T) string { return "/dev/null" },
			args:     []string{"is this urgent", "-i", "jsonl", "--resume"},
			wantCode: ExitUsage,
			wantErr:  "onesie: --resume needs --out to name a regular file, and /dev/null is not one",
		},
		{
			name:     "should refuse a directory before any request",
			path:     func(t *testing.T) string { return t.TempDir() },
			args:     []string{"is this urgent", "-i", "jsonl"},
			wantCode: ExitUsage,
			wantErr:  "is a directory",
		},
	}

	for _, tc := range specials {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" && strings.HasPrefix(tc.path(t), "/dev/") {
				t.Skip("windows has no /dev")
			}

			var calls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w, `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader(input)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				withKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(nil)),
				WithClientFactory(stubFactory(srv.URL)),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(append([]string{"--out", tc.path(t), "-o", "values"}, tc.args...))

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("requests = %d, want %d", got, tc.wantCalls)
			}

			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source plan.Source
		inputs answersfile.FingerprintInputs
		want   string
	}{
		{
			name: "should change a csv fingerprint once a fallback adds its column",
			source: plan.Source{
				Positional: "which team",
				Events: []argv.Event{
					{Name: "pick", Value: "billing,technical"},
					{Name: "min-confidence", Value: "0.7"},
					{Name: "fallback", Value: "human"},
				},
			},
			inputs: answersfile.FingerprintInputs{
				Provider:  "typesafe",
				Model:     "jev-latest",
				MapSource: ".body",
				IDSource:  ".id",
				Output:    "csv",
				Input:     "jsonl",
				MergeKey:  "answers",
				Assert:    "answer.p.billing > 0.5",
				AbstainIf: "answer.p.billing > 0.2",
			},
			want: "v2:6503a1da7f53909577dcdf301457dd5133c7ab408d885c408b7d02af578ef5d8",
		},
		{
			name: "should keep a json fingerprint for a question that has a fallback",
			source: plan.Source{
				Positional: "which team",
				Events: []argv.Event{
					{Name: "pick", Value: "billing,technical"},
					{Name: "min-confidence", Value: "0.7"},
					{Name: "fallback", Value: "human"},
				},
			},
			inputs: answersfile.FingerprintInputs{
				Provider: "typesafe", Model: "jev-latest", Output: "json", Input: "jsonl", MergeKey: "answers",
			},
			want: "v2:92623101f8f774431ea1d792b28932b5a8e345d66688be20209169aa31dc94e4",
		},
		{
			name:   "should keep a csv fingerprint with no fallback",
			source: plan.Source{Positional: "is this urgent"},
			inputs: answersfile.FingerprintInputs{Provider: "typesafe", Model: "jev-latest", Output: "csv", Input: "csv"},
			want:   "v2:b960f50ff4f87b31d39fe0889f79ab00daf3e178256a20470f092b0f356c2ae6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := fingerprintWith(t, tc.source, tc.inputs)
			if got != tc.want {
				t.Errorf("fingerprint = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("should mark a mock run, apart from the same real run", func(t *testing.T) {
		t.Parallel()

		source := plan.Source{Positional: "is this urgent"}
		live := answersfile.FingerprintInputs{Provider: "typesafe", Model: "jev-latest", Output: "csv", Input: "csv"}
		mocked := live
		mocked.Mock = true

		if fingerprintWith(t, source, live) == fingerprintWith(t, source, mocked) {
			t.Error("a mock run and a real run share a fingerprint")
		}
	})
}

func writeSidecar(t *testing.T, path, content string, empty, dir bool) {
	t.Helper()

	switch {
	case dir:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("making the fingerprint a directory: %v", err)
		}
	case empty:
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("writing the empty fingerprint: %v", err)
		}
	case content != "":
		if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
			t.Fatalf("writing the existing fingerprint: %v", err)
		}
	}
}

func lockDir(t *testing.T, dir string) {
	t.Helper()

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("locking %s: %v", dir, err)
	}

	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("unlocking %s: %v", dir, err)
		}
	})
}

func fingerprintFor(t *testing.T, question, provider, model, mapSource, idSource string) string {
	t.Helper()

	return fingerprintWith(t, plan.Source{Positional: question}, answersfile.FingerprintInputs{
		Provider: provider, Model: model, MapSource: mapSource, IDSource: idSource, Output: "json",
		Input: "jsonl",
	})
}

func fingerprintWith(t *testing.T, source plan.Source, inputs answersfile.FingerprintInputs) string {
	t.Helper()

	built, err := plan.Assemble(source)
	if err != nil {
		t.Fatalf("assembling %q: %v", source.Positional, err)
	}

	fingerprint, err := answersfile.Fingerprint(built.Questions, wireAll(built.Questions), inputs)
	if err != nil {
		t.Fatalf("fingerprinting %q: %v", source.Positional, err)
	}

	return fingerprint
}

func idLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func baseFor(url string, unreached bool) string {
	if unreached {
		return "http://127.0.0.1:1"
	}

	return url
}

func TestBindOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		legacy  bool
		wantErr string
	}{
		{
			name:    "should say an older onesie wrote the columns when only the fallback column is new",
			legacy:  true,
			wantErr: "was written by an older onesie, without the fallback columns",
		},
		{
			name:    "should keep the changed flags message for any other difference",
			wantErr: "the questions, flags or gate changed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			srv := answeringServer(t, &calls)

			path := filepath.Join(t.TempDir(), "answers.csv")
			if err := os.WriteFile(path, []byte("urgent,error\n0.5,\n"), 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			source := plan.Source{Events: []argv.Event{
				{Name: "ask", Value: "urgent=is this urgent"}, {Name: "fallback", Value: "no"},
			}}
			if !tc.legacy {
				source = plan.Source{Events: []argv.Event{{Name: "ask", Value: "urgent=is it urgent"}}}
			}

			fingerprint := fingerprintWith(t, source, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.DefaultModel, Output: "csv", Input: "lines",
				WithoutFallbackColumns: true,
			}) + "\n"
			if err := os.WriteFile(path+".onesie", []byte(fingerprint), 0o600); err != nil {
				t.Fatalf("writing the existing fingerprint: %v", err)
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader("first\nsecond\n")),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				withKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(nil)),
				WithClientFactory(stubFactory(srv.URL)),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs([]string{
				"--ask", "urgent=is this urgent", "--fallback", "no", "-i", "lines", "-o", "csv",
				"--out", path, "--resume",
			})

			if code := Execute(t.Context(), root); code != ExitUsage {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitUsage, errOut.String())
			}

			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}

			if calls.Load() != 0 {
				t.Errorf("requests = %d, want none", calls.Load())
			}
		})
	}
}
