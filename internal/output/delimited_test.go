package output_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestNewDelimited(t *testing.T) {
	t.Parallel()

	answered := output.Record{Answers: []output.Named{
		{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
		{ID: "team", Answer: &answer.Answer{Value: "billing"}},
	}}

	failed := output.Record{
		Failure: &output.Failure{Kind: "http", Message: "onesie: 400 bad, request"},
		Answers: []output.Named{{ID: "urgent"}, {ID: "team"}},
	}

	gated := answered
	gated.AssertFailed = true

	tests := []struct {
		name    string
		mode    output.Mode
		opts    output.DelimitedOptions
		records []output.Record
		header  []string
		fields  []map[string]any
		want    string
	}{
		{
			name:    "should write one header then a row per record",
			mode:    output.CSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, Header: true},
			records: []output.Record{answered, failed},
			want: "urgent,team,error\n" +
				"0.92,billing,\n" +
				",,\"onesie: 400 bad, request\"\n",
		},
		{
			name:    "should put the input columns first under a merge",
			mode:    output.CSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, Header: true},
			records: []output.Record{answered},
			header:  []string{"id", "body"},
			fields:  []map[string]any{{"id": "7", "body": "site down"}},
			want:    "id,body,urgent,team,error\n7,site down,0.92,billing,\n",
		},
		{
			name:    "should add an assert column when there is an assertion",
			mode:    output.TSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, Assert: true, Header: true},
			records: []output.Record{answered, gated, failed},
			want: "urgent\tteam\tassert\terror\n" +
				"0.92\tbilling\ttrue\t\n" +
				"0.92\tbilling\tfalse\t\n" +
				"\t\t\tonesie: 400 bad, request\n",
		},
		{
			name:    "should leave the header out when resuming a file that has one",
			mode:    output.CSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}},
			records: []output.Record{answered},
			want:    "0.92,billing,\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			writer := output.NewDelimited(&buf, tc.mode, tc.opts)

			for i, rec := range tc.records {
				var fields map[string]any
				if i < len(tc.fields) {
					fields = tc.fields[i]
				}

				if err := writer.Write(rec, tc.header, fields); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}

			if buf.String() != tc.want {
				t.Errorf("wrote\n%q\nwant\n%q", buf.String(), tc.want)
			}
		})
	}

	t.Run("should refuse an input column named like a question", func(t *testing.T) {
		t.Parallel()

		writer := output.NewDelimited(&bytes.Buffer{}, output.CSV,
			output.DelimitedOptions{IDs: []string{"urgent"}, Header: true})

		err := writer.Write(answered, []string{"urgent"}, map[string]any{"urgent": "x"})
		if !errors.Is(err, output.ErrColumnTaken) {
			t.Errorf("error = %v, want ErrColumnTaken", err)
		}
	})
}
