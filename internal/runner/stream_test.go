package runner_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func TestStream(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{{ID: "answer", Shape: plan.Noul}}}

	namer, err := jq.Compile(".id")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		in         string
		inputMode  input.Mode
		outputMode output.Mode
		merge      bool
		delimiter  input.Delimiter
		namer      *jq.Expr
		bare       bool
		want       string
		wantFailed int
	}{
		{
			name:      "should ask a record with no images when Images is nil",
			in:        "a ticket\n",
			inputMode: input.Lines,
			want:      `{"line":1,"model":"m","answer":{"value":0.9}}` + "\n",
		},
		{
			name: "should merge into the input's semicolons", in: "id;body\n1;a,b\n",
			inputMode: input.CSV, outputMode: output.CSV, merge: true, delimiter: input.Delimiter{Detect: true},
			want: "id;body;answer;error\n1;a,b;0.9;\n",
		},
		{
			name: "should keep commas for -i tsv -o csv --merge", in: "id\tbody\n1\ta;b\n",
			inputMode: input.TSV, outputMode: output.CSV, merge: true, delimiter: input.Delimiter{Comma: ';'},
			want: "id,body,answer,error\n1,a;b,0.9,\n",
		},
		{
			name: "should keep commas for a plain -o csv", in: "id;body\n1;a\n",
			inputMode: input.CSV, outputMode: output.CSV, delimiter: input.Delimiter{Comma: ';'},
			want: "answer,error\n0.9,\n",
		},
		{
			name: "should build the table on the first write, so an empty input writes nothing", in: "",
			inputMode: input.CSV, outputMode: output.CSV, merge: true, delimiter: input.Delimiter{Detect: true},
		},
		{
			name: "should word a failure by its error and count it with no Message or Stats", in: "a ticket\n\n",
			inputMode: input.Lines, bare: true, wantFailed: 1,
			want: `{"line":1,"model":"m","answer":{"value":0.9}}` + "\n" +
				`{"line":2,"error":{"kind":"input","status":null,"message":"line 2: blank line, an empty state is ` +
				`a request the model cannot answer. Pass --skip-blank to drop blank lines"}}` + "\n",
		},
		{
			name: "should drop the id hint with no ErrOut", in: "id\tbody\n1\ta\n", inputMode: input.CSV, namer: namer,
			bare: true, wantFailed: 1,
			want: `{"error":{"kind":"input","status":null,"message":"line 2: --id: id must be a string ` +
				`or a finite number, got null"}}` + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder

			stub := &requestAnswerer{}
			config := runner.StreamConfig{
				Input:      strings.NewReader(tc.in),
				InputMode:  tc.inputMode,
				Delimiter:  tc.delimiter,
				Out:        &out,
				ErrOut:     io.Discard,
				OutputMode: tc.outputMode,
				Merge:      tc.merge,
				MergeKey:   "answers",
				Namer:      tc.namer,
				Ask: runner.Ask{
					Plan: built, Model: "m", Questions: plan.WireAll(built.Questions), Message: errorMessage,
				},
				Answerers: func(context.Context, *runner.Collector) (runner.Answerer, error) { return stub, nil },
				Stats:     &runner.Collector{},
				Jobs:      1,
			}

			if tc.bare {
				config.ErrOut, config.Ask.Message, config.Stats = nil, nil, nil
			}

			outcome, err := runner.Stream(t.Context(), config)
			if err != nil || outcome.Failed != tc.wantFailed {
				t.Fatalf("Stream = %+v, %v, want %d failed", outcome, err, tc.wantFailed)
			}

			if out.String() != tc.want {
				t.Errorf("output = %q, want %q", out.String(), tc.want)
			}

			if wantAsked := strings.Count(tc.want, "0.9"); len(stub.requests) != wantAsked || slices.ContainsFunc(
				stub.requests, func(req onesie.Request) bool { return req.Assets != nil }) {
				t.Errorf("requests = %+v, want %d with no assets", stub.requests, wantAsked)
			}
		})
	}
}

func TestAnswersDelimiter(t *testing.T) {
	t.Parallel()

	semicolon := input.Delimiter{Comma: ';'}

	tests := []struct {
		name   string
		merge  bool
		out    output.Mode
		in     input.Mode
		given  input.Delimiter
		wanted input.Delimiter
	}{
		{
			name: "should keep the input's delimiter for a merge from csv into csv", merge: true,
			out: output.CSV, in: input.CSV, given: semicolon, wanted: semicolon,
		},
		{
			name: "should keep detection for a merge from csv into csv", merge: true,
			out: output.CSV, in: input.CSV, given: input.Delimiter{Detect: true}, wanted: input.Delimiter{Detect: true},
		},
		{name: "should use commas for a plain -o csv", out: output.CSV, in: input.CSV, given: semicolon},
		{name: "should use commas for a merge from tsv", merge: true, out: output.CSV, in: input.TSV, given: semicolon},
		{name: "should use commas for a merge into json", merge: true, out: output.JSON, in: input.CSV, given: semicolon},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := runner.AnswersDelimiter(tc.merge, tc.out, tc.in, tc.given); got != tc.wanted {
				t.Errorf("AnswersDelimiter = %+v, want %+v", got, tc.wanted)
			}
		})
	}
}

func errorMessage(err error) string {
	return err.Error()
}

func (a *requestAnswerer) Answer(_ context.Context, _ runner.RecordKey, req onesie.Request) (runner.Reply, error) {
	a.requests = append(a.requests, req)

	return runner.Reply{Result: &onesie.Result{
		Model: "m", Answers: map[string]onesie.Answer{"answer": &onesie.NoulAnswer{Noul: 0.9}},
	}}, nil
}

func (*requestAnswerer) Salt(runner.RecordKey) (string, bool) {
	return "", true
}

type requestAnswerer struct {
	requests []onesie.Request
}
