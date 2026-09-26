package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestOpenOut(t *testing.T) {
	t.Parallel()

	const input = "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n{\"id\":4}\n"

	const bodies = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n" +
		"{\"id\":3,\"body\":\"c\"}\n{\"id\":4,\"body\":\"d\"}\n"

	valuesFor := func(question, provider, model, mapSource, idSource string) string {
		return fingerprintWith(t, plan.Source{Positional: question}, fingerprintInputs{
			provider: provider, model: model, mapSource: mapSource, idSource: idSource, output: "values",
			input: "jsonl",
		})
	}

	matching := valuesFor("is this urgent", "typesafe", jev.DefaultModel, "", "")
	picking := fingerprintWith(t, plan.Source{
		Positional: "which team",
		Events: []argv.Event{
			{Name: "pick", Value: "billing,technical"}, {Name: "fallback", Value: "human"},
		},
	}, fingerprintInputs{provider: "typesafe", model: jev.DefaultModel, output: "values", input: "jsonl"})
	merged := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
		provider: "typesafe", model: jev.DefaultModel, output: "values", input: "jsonl",
		mergeKey: "answers",
	})
	fromLines := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
		provider: "typesafe", model: jev.DefaultModel, output: "values", input: "lines",
	})
	gated := func(assert, abstainIf string) string {
		return fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
			provider: "typesafe", model: jev.DefaultModel, output: "values", input: "jsonl",
			assert: assert, abstainIf: abstainIf,
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
			sidecar:     valuesFor("is this critical", "typesafe", jev.DefaultModel, "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: valuesFor("is this critical", "typesafe", jev.DefaultModel, "", ""),
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
			sidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
				provider: "typesafe", model: jev.DefaultModel, output: "values", input: "lines",
				skipBlank: true,
			}),
			args:     []string{"is this urgent", "-i", "lines", "--resume"},
			stdin:    "a\n\nb\n\nc\n",
			wantCode: ExitUsage,
			wantFile: "{\"answer\":0.5}\n",
			wantSidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
				provider: "typesafe", model: jev.DefaultModel, output: "values", input: "lines",
				skipBlank: true,
			}),
			wantErr: changed,
		},
		{
			name:     "should resume csv by row count when --skip-blank changed",
			existing: "{\"answer\":0.5}\n",
			sidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
				provider: "typesafe", model: jev.DefaultModel, output: "values", input: "csv",
			}),
			args:     []string{"is this urgent", "-i", "csv", "--skip-blank", "--resume"},
			stdin:    "body\na\n\nb\n",
			wantFile: "{\"answer\":0.5}\n{\"answer\":0.5}\n",
			wantSidecar: fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
				provider: "typesafe", model: jev.DefaultModel, output: "values", input: "csv",
			}),
			wantCalls: 1,
		},
		{
			name:     "should resume by id when --skip-blank changed",
			existing: idLines(1, 2),
			sidecar:  valuesFor("is this urgent", "typesafe", jev.DefaultModel, "", ".id"),
			args: []string{
				"is this urgent", "-i", "jsonl", "--id", ".id", "--skip-blank", "--resume",
			},
			stdin:       "{\"id\":1}\n\n{\"id\":2}\n\n{\"id\":3}\n{\"id\":4}\n",
			wantFile:    idLines(1, 4),
			wantSidecar: valuesFor("is this urgent", "typesafe", jev.DefaultModel, "", ".id"),
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
			sidecar:     fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", ""),
			wantSidecar: fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", ""),
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
			name:        "should refuse to resume when the default model changed",
			existing:    "keep me\n",
			sidecar:     matching,
			env:         map[string]string{jev.EnvDefaultModel: "jev-next"},
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
			args:        []string{"is this urgent", "-i", "jsonl", "-m", jev.DefaultModel, "--resume"},
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
				WithKeychain(noKeychain()),
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
				WithKeychain(noKeychain()),
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

func TestResolveTarget(t *testing.T) {
	t.Parallel()

	failed := errors.New("input/output error")

	tests := []struct {
		name    string
		file    bool
		links   map[string]string
		path    string
		resolve func(path string) (string, error)
		want    string
		wantErr error
	}{
		{
			name: "should keep a path with no file at it",
			path: "answers.jsonl",
			want: "answers.jsonl",
		},
		{
			name:  "should follow a link to a file that exists",
			file:  true,
			links: map[string]string{"link.jsonl": "answers.jsonl"},
			path:  "link.jsonl",
			want:  "answers.jsonl",
		},
		{
			name:  "should follow a chain of links to a file not written yet",
			links: map[string]string{"link.jsonl": "middle.jsonl", "middle.jsonl": "answers.jsonl"},
			path:  "link.jsonl",
			want:  "answers.jsonl",
		},
		{
			name:  "should refuse a loop of links",
			links: map[string]string{"link.jsonl": "other.jsonl", "other.jsonl": "link.jsonl"},
			path:  "link.jsonl",
			resolve: func(string) (string, error) {
				return "", fs.ErrNotExist
			},
			wantErr: errLinkLoop,
		},
		{
			name:    "should return a failure to resolve the path",
			path:    "answers.jsonl",
			resolve: func(string) (string, error) { return "", failed },
			wantErr: failed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.links) > 0 && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("resolving the directory: %v", err)
			}

			if tc.file {
				if writeErr := os.WriteFile(filepath.Join(dir, "answers.jsonl"), nil, 0o600); writeErr != nil {
					t.Fatalf("writing the answers file: %v", writeErr)
				}
			}

			for link, target := range tc.links {
				if linkErr := os.Symlink(target, filepath.Join(dir, link)); linkErr != nil {
					t.Fatalf("linking: %v", linkErr)
				}
			}

			resolve := tc.resolve
			if resolve == nil {
				resolve = filepath.EvalSymlinks
			}

			got, err := resolveTarget(filepath.Join(dir, tc.path), resolve, os.Readlink)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			if err != nil {
				return
			}

			if want := filepath.Join(dir, tc.want); filepath.Clean(got) != want {
				t.Errorf("target = %q, want %q", got, want)
			}
		})
	}
}

func TestOutFile_Lock(t *testing.T) {
	t.Parallel()

	refused := errors.New("operation not supported")

	tests := []struct {
		name       string
		resume     bool
		lockErr    error
		wantErr    string
		wantLocked bool
	}{
		{
			name:       "should hold the lock on a fresh run",
			wantLocked: true,
		},
		{
			name:    "should refuse a fresh run while a resume holds the file",
			lockErr: errLocked,
			wantErr: "is in use by another onesie run",
		},
		{
			name:    "should write a fresh run unguarded when no lock can be taken beside the file",
			lockErr: refused,
		},
		{
			name:    "should refuse a resume when no lock can be taken beside the file",
			resume:  true,
			lockErr: refused,
			wantErr: "operation not supported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var took string

			out := &outFile{path: "link.jsonl", target: "answers.jsonl"}

			err := out.lock(func(answers string) (func() error, error) {
				took = answers
				if tc.lockErr != nil {
					return nil, tc.lockErr
				}

				return func() error { return nil }, nil
			}, tc.resume)
			if !strings.Contains(fmt.Sprint(err), tc.wantErr) || (tc.wantErr == "") != (err == nil) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			if took != "answers.jsonl" {
				t.Errorf("locked %q, want the resolved target", took)
			}

			if (out.unlock != nil) != tc.wantLocked {
				t.Errorf("holding the lock = %v, want %v", out.unlock != nil, tc.wantLocked)
			}
		})
	}
}

func TestOutFile_Write(t *testing.T) {
	t.Parallel()

	failedRename := errors.New("disk full")

	tests := []struct {
		name        string
		resume      bool
		bind        bool
		rename      func(string, string) error
		wantErr     string
		wantFile    string
		wantSidecar string
		wantRenames []string
	}{
		{
			name:        "should write the fingerprint through a temporary file and a rename",
			bind:        true,
			wantFile:    "answer\n",
			wantSidecar: "v1:" + strings.Repeat("a", 64) + "\n",
			wantRenames: []string{"answers.jsonl.onesie.tmp answers.jsonl.onesie"},
		},
		{
			name:        "should refuse a resume that was never bound",
			resume:      true,
			wantErr:     "was never checked against its fingerprint",
			wantFile:    "keep me\n",
			wantSidecar: "keep this\n",
		},
		{
			name:        "should let go of the file when the fingerprint cannot be written",
			bind:        true,
			rename:      func(string, string) error { return failedRename },
			wantErr:     "disk full",
			wantFile:    "",
			wantSidecar: "keep this\n",
			wantRenames: []string{
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")

			if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			if err := os.WriteFile(path+".onesie", []byte("keep this\n"), 0o600); err != nil {
				t.Fatalf("writing the existing fingerprint: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			var renames []string

			out := &outFile{
				path:   path,
				target: path,
				open:   os.OpenFile,
				remove: os.Remove,
				rename: func(from, to string) error {
					renames = append(renames, filepath.Base(from)+" "+filepath.Base(to))

					return rename(from, to)
				},
				resume: tc.resume,
				keep:   int64(len("keep me\n")),
			}

			if tc.bind {
				if err := out.bind("v1:" + strings.Repeat("a", 64)); err != nil {
					t.Fatalf("bind: %v", err)
				}
			}

			_, writeErr := io.WriteString(out, "answer\n")
			if writeErr == nil {
				writeErr = out.finish(nil)
			}

			if !strings.Contains(fmt.Sprint(writeErr), tc.wantErr) || (tc.wantErr == "") != (writeErr == nil) {
				t.Fatalf("error = %v, want %q", writeErr, tc.wantErr)
			}

			if writeErr != nil {
				if out.file != nil {
					t.Errorf("file = %v, want it let go after the failure", out.file)
				}

				_, againErr := io.WriteString(out, "again\n")
				if againErr == nil || errors.Is(againErr, os.ErrClosed) {
					t.Errorf("second write error = %v, want the first failure again", againErr)
				}

				if err := out.finish(writeErr); err != nil {
					t.Errorf("finish after the failure: %v", err)
				}
			}

			assertFileHolds(t, path, tc.wantFile)
			assertFileHolds(t, path+".onesie", tc.wantSidecar)

			if _, err := os.Stat(path + ".onesie.tmp"); !os.IsNotExist(err) {
				t.Errorf("temporary fingerprint left behind: %v", err)
			}

			if strings.Join(renames, ",") != strings.Join(tc.wantRenames, ",") {
				t.Errorf("renames = %q, want %q", renames, tc.wantRenames)
			}
		})
	}
}

func TestOutFile_Finish(t *testing.T) {
	t.Parallel()

	const appended = "{\"id\":2,\"answer\":0.5}\n{\"id\":1,\"answer\":0.5}\n"

	const compacted = "{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"answer\":0.5}\n"

	failed := errors.New("disk full")

	tests := []struct {
		name        string
		rename      func(string, string) error
		open        func(string, int, os.FileMode) (*os.File, error)
		link        bool
		goos        string
		wantErr     string
		wantFile    string
		wantDirSync bool
	}{
		{
			name:        "should rewrite the file in input order through a rename",
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name: "should leave the uncompacted file when the rename fails",
			rename: func(from, to string) error {
				if strings.HasSuffix(from, compactSuffix) {
					return failed
				}

				return os.Rename(from, to)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name: "should leave the uncompacted file when the temporary file cannot be opened",
			open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if strings.HasSuffix(name, compactSuffix) {
					return nil, failed
				}

				return os.OpenFile(name, flag, perm)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name:        "should write through a symlink at the path and leave the link in place",
			link:        true,
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name:     "should not flush the directory on windows, which cannot",
			goos:     "windows",
			wantFile: compacted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.link && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")
			target := path

			if tc.link {
				target = filepath.Join(dir, "target.jsonl")
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("linking: %v", err)
				}
			}

			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			open := tc.open
			if open == nil {
				open = os.OpenFile
			}

			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}

			resolvedDir, resolveErr := filepath.EvalSymlinks(dir)
			if resolveErr != nil {
				t.Fatalf("resolving the directory: %v", resolveErr)
			}

			var dirSynced atomic.Bool

			out := &outFile{
				path: path,
				open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
					if name == resolvedDir {
						dirSynced.Store(true)
					}

					return open(name, flag, perm)
				},
				target: filepath.Join(resolvedDir, filepath.Base(target)),
				rename: rename,
				remove: os.Remove,
				goos:   goos,
				resume: true,
			}
			if err := out.bind("v1:" + strings.Repeat("a", 64)); err != nil {
				t.Fatalf("bind: %v", err)
			}

			if _, err := io.WriteString(out, appended); err != nil {
				t.Fatalf("write: %v", err)
			}

			half := int64(len(appended) / 2)
			out.compactInto([]span{{start: half, end: 2 * half}, {start: 0, end: half}}, output.Values)

			err := out.finish(nil)
			if !strings.Contains(fmt.Sprint(err), tc.wantErr) || (tc.wantErr == "") != (err == nil) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			assertFileHolds(t, target, tc.wantFile)

			if runtime.GOOS != "windows" {
				assertMode(t, target, 0o600)
			}

			if info, statErr := os.Lstat(path); statErr != nil || (info.Mode()&os.ModeSymlink != 0) != tc.link {
				t.Errorf("path = %v, %v, want a symlink %v", info, statErr, tc.link)
			}

			for _, beside := range []string{path, target} {
				if _, statErr := os.Stat(beside + compactSuffix); !os.IsNotExist(statErr) {
					t.Errorf("temporary compaction file left behind: %v", statErr)
				}
			}

			if dirSynced.Load() != tc.wantDirSync {
				t.Errorf("directory flushed = %v, want %v", dirSynced.Load(), tc.wantDirSync)
			}
		})
	}
}

func TestFingerprintOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source plan.Source
		inputs fingerprintInputs
		want   string
	}{
		{
			name: "should keep the fingerprint format a sidecar on disk was written in",
			source: plan.Source{
				Positional: "which team",
				Events: []argv.Event{
					{Name: "pick", Value: "billing,technical"},
					{Name: "min-confidence", Value: "0.7"},
					{Name: "fallback", Value: "human"},
				},
			},
			inputs: fingerprintInputs{
				provider:  "typesafe",
				model:     "jev-latest",
				mapSource: ".body",
				idSource:  ".id",
				output:    "csv",
				input:     "jsonl",
				mergeKey:  "answers",
				assert:    "answer.p.billing > 0.5",
				abstainIf: "answer.p.billing > 0.2",
			},
			want: "v2:6fc264bc94b1e04cdc7da8841f7ce348e0447168a23721534d12856f754c015a",
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
}

func assertFileHolds(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Base(path), err)
	}

	if string(data) != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), data, want)
	}
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

	return fingerprintWith(t, plan.Source{Positional: question}, fingerprintInputs{
		provider: provider, model: model, mapSource: mapSource, idSource: idSource, output: "json",
		input: "jsonl",
	})
}

func fingerprintWith(t *testing.T, source plan.Source, inputs fingerprintInputs) string {
	t.Helper()

	built, err := plan.Assemble(source)
	if err != nil {
		t.Fatalf("assembling %q: %v", source.Positional, err)
	}

	fingerprint, err := fingerprintOf(built.Questions, inputs)
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

func TestSpecialFile(t *testing.T) {
	t.Parallel()

	regular := func(string) (fs.FileInfo, error) { return os.Stat(os.Args[0]) }
	missing := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	directory := func(string) (fs.FileInfo, error) { return os.Stat(os.TempDir()) }

	tests := []struct {
		name    string
		target  string
		goos    string
		stat    func(string) (fs.FileInfo, error)
		want    bool
		wantErr bool
	}{
		{name: "should treat a regular file as regular", target: "answers.jsonl", goos: "linux", stat: regular},
		{name: "should treat a file not written yet as regular", target: "answers.jsonl", goos: "linux", stat: missing},
		{
			name:   "should treat a descriptor under /dev as special when it stats as a regular file",
			target: "/dev/fd/1", goos: "darwin", stat: regular, want: true,
		},
		{name: "should refuse a directory", target: "out", goos: "linux", stat: directory, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := specialFile(tc.target, tc.goos, tc.stat)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want an error %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("special = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOutFile_CheckFingerprint(t *testing.T) {
	t.Parallel()

	current := "v2:" + strings.Repeat("ab", 32)

	tests := []struct {
		name     string
		sidecar  *string
		checking string
		wantErr  string
	}{
		{name: "should refuse a file with no sidecar", checking: current, wantErr: "has no fingerprint beside it"},
		{name: "should refuse a sidecar onesie did not write", sidecar: new("hello"), checking: current, wantErr: "does not hold a fingerprint onesie wrote"},
		{name: "should refuse an older version", sidecar: new("v1:abc"), checking: current, wantErr: "an older onesie wrote"},
		{name: "should refuse a newer version", sidecar: new("v3:abc"), checking: current, wantErr: "a newer onesie wrote"},
		{name: "should refuse a changed fingerprint", sidecar: new(current), checking: "v2:" + strings.Repeat("cd", 32), wantErr: "changed since"},
		{name: "should accept the same fingerprint", sidecar: new(current), checking: current},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if err := os.WriteFile(path, []byte("{\"id\":\"a\"}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if tc.sidecar != nil {
				if err := os.WriteFile(path+fingerprintSuffix, []byte(*tc.sidecar+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			out := &outFile{path: path, target: path, open: os.OpenFile}

			matched, err := out.checkFingerprint(tc.checking)
			if tc.wantErr == "" {
				if err != nil || !matched {
					t.Fatalf("checkFingerprint = %v, %v, want a match", matched, err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !errors.Is(err, ErrStaleAnswers) {
				t.Fatalf("checkFingerprint error = %v, want one containing %q that is ErrStaleAnswers", err, tc.wantErr)
			}

			if strings.Contains(err.Error(), ErrStaleAnswers.Error()) {
				t.Errorf("error = %q, want today's wording without the sentinel's text", err)
			}
		})
	}
}
