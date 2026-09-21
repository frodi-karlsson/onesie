package output_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	status := 429

	simple := output.Record{
		Model: "jev-1.13.0",
		Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
		},
	}

	decided := output.Record{
		Model: "jev-1.13.0",
		Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{
				Value: 0.92, Decision: true, Decided: true,
			}},
		},
	}

	failed := output.Record{
		Failure: &output.Failure{
			Kind: "http", Status: &status, Message: "rate limited after 2 retries",
		},
		Answers: []output.Named{
			{ID: "team", Answer: &answer.Answer{
				Decision: "refuse", Decided: true, Fallback: "error",
			}},
		},
	}

	ordered := output.Record{
		Model: "jev-1.13.0",
		Answers: []output.Named{
			{ID: "zebra", Answer: &answer.Answer{Value: 0.1}},
			{ID: "apple", Answer: &answer.Answer{Value: 0.2}},
		},
	}

	tests := []struct {
		name string
		mode output.Mode
		rec  output.Record
		want string
	}{
		{
			name: "should include the model in json output",
			mode: output.JSON,
			rec:  simple,
			want: `{"model":"jev-1.13.0","urgent":{"value":0.92}}`,
		},
		{
			name: "should keep questions in definition order rather than sorting them",
			mode: output.JSON,
			rec:  ordered,
			want: `{"model":"jev-1.13.0","zebra":{"value":0.1},"apple":{"value":0.2}}`,
		},
		{
			name: "should add usage to json output when present",
			mode: output.JSON,
			rec: output.Record{
				Model:   "jev-1.13.0",
				Usage:   &jev.Usage{InputTokens: 2841, OutputTokens: 71},
				Answers: simple.Answers,
			},
			want: `{"model":"jev-1.13.0","usage":{"input_tokens":2841,"output_tokens":71},` +
				`"urgent":{"value":0.92}}`,
		},
		{
			name: "should omit the model from values output",
			mode: output.Values,
			rec:  simple,
			want: `{"urgent":0.92}`,
		},
		{
			name: "should prefer the decision in values output",
			mode: output.Values,
			rec:  decided,
			want: `{"urgent":true}`,
		},
		{
			name: "should print the bare scalar in raw output",
			mode: output.Raw,
			rec:  simple,
			want: "0.92",
		},
		{
			name: "should print the decision in raw output",
			mode: output.Raw,
			rec:  decided,
			want: "true",
		},
		{
			name: "should emit the error key whole in json output",
			mode: output.JSON,
			rec:  failed,
			want: `{"error":{"kind":"http","status":429,` +
				`"message":"rate limited after 2 retries"},` +
				`"team":{"fallback":"error","decision":"refuse"}}`,
		},
		{
			name: "should emit the error key in values output too",
			mode: output.Values,
			rec:  failed,
			want: `{"error":{"kind":"http","status":429,` +
				`"message":"rate limited after 2 retries"},"team":"refuse"}`,
		},
		{
			name: "should emit a null status for a transport failure",
			mode: output.Values,
			rec: output.Record{
				Failure: &output.Failure{Kind: "transport", Message: "connection refused"},
			},
			want: `{"error":{"kind":"transport","status":null,` +
				`"message":"connection refused"}}`,
		},
		{
			name: "should print the fallback word in raw output on failure",
			mode: output.Raw,
			rec:  failed,
			want: "refuse",
		},
		{
			name: "should print an empty line in raw output with no fallback",
			mode: output.Raw,
			rec: output.Record{
				Failure: &output.Failure{Kind: "transport", Message: "connection refused"},
				Answers: []output.Named{{ID: "urgent"}},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder

			if err := output.Write(&buf, tc.mode, tc.rec); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := strings.TrimSuffix(buf.String(), "\n")
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flag      string
		tty       bool
		streaming bool
		want      output.Mode
		wantErr   bool
	}{
		{name: "should default to table on a terminal", flag: "", tty: true, want: output.Table},
		{name: "should default to json off a terminal", flag: "", want: output.JSON},
		{
			name: "should default to json when streaming even on a terminal",
			flag: "", tty: true, streaming: true, want: output.JSON,
		},
		{name: "should accept an explicit mode", flag: "values", want: output.Values},
		{name: "should reject an unknown mode", flag: "yaml", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := output.ParseMode(tc.flag, tc.tty, tc.streaming)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tc.want {
				t.Errorf("mode = %v, want %v", got, tc.want)
			}
		})
	}
}
