package output_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestWriteMerged(t *testing.T) {
	t.Parallel()

	simple := output.Record{
		Model: "onesie-1.13.0",
		Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
		},
	}

	tests := []struct {
		name    string
		mode    output.Mode
		raw     string
		state   any
		key     string
		rec     output.Record
		want    string
		wantErr string
	}{
		{
			name:  "should fold answers into a json object",
			mode:  output.JSON,
			raw:   `{"id":7,"body":"hello"}`,
			state: map[string]any{"id": float64(7), "body": "hello"},
			key:   "answers",
			rec:   simple,
			want: `{"id":7,"body":"hello","answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name:  "should add no id, since the input carries its own",
			mode:  output.Values,
			raw:   `{"id":7,"body":"hello"}`,
			state: map[string]any{"id": float64(7), "body": "hello"},
			key:   "answers",
			rec: output.Record{
				ID:      json.Number("7"),
				Answers: simple.Answers,
			},
			want: `{"id":7,"body":"hello","answers":{"urgent":0.92}}`,
		},
		{
			name:  "should keep the input's key order",
			mode:  output.JSON,
			raw:   `{"zebra":1,"apple":2}`,
			state: map[string]any{"zebra": float64(1), "apple": float64(2)},
			key:   "answers",
			rec:   simple,
			want: `{"zebra":1,"apple":2,"answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name:  "should compact a pretty printed object onto one line",
			mode:  output.JSON,
			raw:   "{\n  \"zebra\": 1,\n  \"apple\": 2\n}",
			state: map[string]any{"zebra": float64(1), "apple": float64(2)},
			key:   "answers",
			rec:   simple,
			want: `{"zebra":1,"apple":2,"answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name:  "should fold into an empty object",
			mode:  output.JSON,
			raw:   `{}`,
			state: map[string]any{},
			key:   "answers",
			rec:   simple,
			want:  `{"answers":{"model":"onesie-1.13.0","urgent":{"value":0.92}}}`,
		},
		{
			name:  "should wrap an array under state",
			mode:  output.JSON,
			raw:   `["a","b"]`,
			state: []any{"a", "b"},
			key:   "answers",
			rec:   simple,
			want: `{"state":["a","b"],"answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name:  "should wrap a text line under state",
			mode:  output.JSON,
			raw:   "a ticket body",
			state: "a ticket body",
			key:   "answers",
			rec:   simple,
			want: `{"state":"a ticket body","answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name: "should keep a numeric looking text line a string",
			mode: output.JSON,
			raw:  "42",
			// Under -i lines the record's state is the string "42", and the merged record must say
			// so. Probing the raw line as JSON instead would emit the number 42 and change the
			// type the caller sent.
			state: "42",
			key:   "answers",
			rec:   simple,
			want:  `{"state":"42","answers":{"model":"onesie-1.13.0","urgent":{"value":0.92}}}`,
		},
		{
			name: "should wrap a line that looks like an object but does not parse",
			mode: output.JSON,
			raw:  "{oops",
			// An input error record has no state at all. Falling through to the wrapper keeps the
			// line parseable rather than failing the whole run on a single malformed input.
			state: nil,
			key:   "answers",
			rec: output.Record{Failure: &output.Failure{
				Kind: "input", Message: "line 1: line is not one complete JSON value",
			}},
			want: `{"state":"{oops","answers":{"error":{"kind":"input","status":null,` +
				`"message":"line 1: line is not one complete JSON value"}}}`,
		},
		{
			name:  "should use values shape under the merge key",
			mode:  output.Values,
			raw:   `{"id":7}`,
			state: map[string]any{"id": float64(7)},
			key:   "answers",
			rec:   simple,
			want:  `{"id":7,"answers":{"urgent":0.92}}`,
		},
		{
			name:  "should relocate with a merge key",
			mode:  output.JSON,
			raw:   `{"answers":"mine"}`,
			state: map[string]any{"answers": "mine"},
			key:   "onesie",
			rec:   simple,
			want:  `{"answers":"mine","onesie":{"model":"onesie-1.13.0","urgent":{"value":0.92}}}`,
		},
		{
			name: "should wrap an object looking text line under state",
			mode: output.JSON,
			raw:  `{"a":1}`,
			// Under -i lines this was sent as a string, so folding it as an object would claim a
			// shape the API never received.
			state: `{"a":1}`,
			key:   "answers",
			rec:   simple,
			want: `{"state":"{\"a\":1}","answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name:  "should fold answers into a raw json object",
			mode:  output.JSON,
			raw:   `{"zebra":1,"alpha":2}`,
			state: json.RawMessage(`{"zebra":1,"alpha":2}`),
			key:   "answers",
			rec:   simple,
			want: `{"zebra":1,"alpha":2,"answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name: "should keep a large integer's digits when wrapping an array",
			mode: output.JSON,
			raw:  `[12345678901234567890]`,
			// A parsed state would have gone through float64 and come back 12345678901234567000,
			// handing the caller a number they never sent.
			state: json.RawMessage(`[12345678901234567890]`),
			key:   "answers",
			rec:   simple,
			want: `{"state":[12345678901234567890],"answers":{"model":"onesie-1.13.0",` +
				`"urgent":{"value":0.92}}}`,
		},
		{
			name: "should wrap a raw json object when there is no line to fold into",
			mode: output.JSON,
			raw:  "",
			// The merge key was already taken, so the record failed and its object goes under
			// state rather than being folded into a line that is not there.
			state: json.RawMessage(`{"answers":"mine"}`),
			key:   "answers",
			rec: output.Record{Failure: &output.Failure{
				Kind: "input", Message: "line 1: --merge would overwrite",
			}},
			want: `{"state":{"answers":"mine"},"answers":{"error":{"kind":"input",` +
				`"status":null,"message":"line 1: --merge would overwrite"}}}`,
		},
		{
			name:    "should refuse to overwrite an existing key",
			mode:    output.JSON,
			raw:     `{"id":7,"answers":"mine"}`,
			state:   map[string]any{"id": float64(7), "answers": "mine"},
			key:     "answers",
			rec:     simple,
			wantErr: "already has the merge key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder

			err := output.WriteMerged(&buf, tc.mode, tc.rec, tc.raw, tc.state, tc.key)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %q", buf.String())
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := strings.TrimSuffix(buf.String(), "\n")
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}

			if strings.Contains(got, "\n") {
				t.Errorf("a merged record must be one line, got:\n%s", got)
			}
		})
	}
}
