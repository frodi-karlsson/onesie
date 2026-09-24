package output_test

import (
	"bytes"
	"encoding/json"
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

	abstained := answered
	abstained.Abstained = true

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
			name:    "should write abstain in the csv assert column when the record abstained",
			mode:    output.CSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, Assert: true, Header: true},
			records: []output.Record{answered, gated, abstained},
			want: "urgent,team,assert,error\n" +
				"0.92,billing,true,\n" +
				"0.92,billing,false,\n" +
				"0.92,billing,abstain,\n",
		},
		{
			name:    "should write abstain in the tsv assert column when the record abstained",
			mode:    output.TSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, Assert: true, Header: true},
			records: []output.Record{abstained},
			want:    "urgent\tteam\tassert\terror\n0.92\tbilling\tabstain\t\n",
		},
		{
			name: "should write tsv without quoting and flatten tabs and newlines",
			mode: output.TSV,
			opts: output.DelimitedOptions{IDs: []string{"urgent", "team"}, Header: true},
			records: []output.Record{{
				Failure: &output.Failure{Kind: "http", Message: "line one\n\"two\"\tthree"},
				Answers: []output.Named{{ID: "urgent"}, {ID: "team"}},
			}},
			want: "urgent\tteam\terror\n\t\tline one \"two\" three\n",
		},
		{
			name:    "should put the id column first when the run names its records",
			mode:    output.CSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, ID: true, Assert: true, Header: true},
			records: []output.Record{withID(answered, "T-1"), withID(failed, json.Number("12"))},
			want: "id,urgent,team,assert,error\n" +
				"T-1,0.92,billing,true,\n" +
				"12,,,,\"onesie: 400 bad, request\"\n",
		},
		{
			name:    "should leave the id cell empty for a record with no id",
			mode:    output.TSV,
			opts:    output.DelimitedOptions{IDs: []string{"urgent", "team"}, ID: true, Header: true},
			records: []output.Record{failed},
			want:    "id\turgent\tteam\terror\n\t\t\tonesie: 400 bad, request\n",
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

	t.Run("should refuse an input column named like the id column", func(t *testing.T) {
		t.Parallel()

		writer := output.NewDelimited(&bytes.Buffer{}, output.CSV,
			output.DelimitedOptions{IDs: []string{"urgent"}, ID: true, Header: true})

		err := writer.Write(answered, []string{"id"}, map[string]any{"id": "x"})
		if !errors.Is(err, output.ErrColumnTaken) {
			t.Errorf("error = %v, want ErrColumnTaken", err)
		}
	})

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

func withID(rec output.Record, id any) output.Record {
	rec.ID = id

	return rec
}
