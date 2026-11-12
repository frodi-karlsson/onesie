package calibrate

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestWriteComparisonTable(t *testing.T) {
	t.Parallel()

	cost := func(value float64) *float64 { return &value }
	question := QuestionComparison{
		ID: "urgent", Labelled: 2, LeftOut: make([]LeftOut, 2),
		Scores: CompareYesNo([][]YesNoCase{
			{{Yes: true, Value: 0.9}, {Value: 0.1}}, {{Yes: true, Value: 0.8}, {Value: 0.2}},
		}, nil, nil),
	}

	tests := []struct {
		name    string
		runs    []Run
		want    string
		without bool
	}{
		{
			name: "should show mean tokens and cost per 1000 records, and dashes for a file without usage",
			runs: []Run{
				{File: "march.jsonl", Usage: &Usage{
					Usage: onesie.Usage{InputTokens: 825, OutputTokens: 12, Cost: cost(0.0000882)}, Records: 2,
				}},
				{File: "november.jsonl"},
			},
			want: "\nusage, mean per record over the 2 records any question compares\n\n" +
				"  run             records  input tokens  output tokens  cost per 1000\n" +
				"  march.jsonl           2           413              6        $0.0441\n" +
				"  november.jsonl        -             -              -              -\n",
		},
		{
			name: "should show a dash for the cost of a file whose usage carries none",
			runs: []Run{
				{File: "march.jsonl", Usage: &Usage{Usage: onesie.Usage{InputTokens: 1000, OutputTokens: 10}, Records: 2}},
				{File: "november.jsonl", Usage: &Usage{
					Usage: onesie.Usage{InputTokens: 1000, OutputTokens: 10, Cost: cost(24)}, Records: 2,
				}},
			},
			want: "\nusage, mean per record over the 2 records any question compares\n\n" +
				"  run             records  input tokens  output tokens  cost per 1000\n" +
				"  march.jsonl           2           500              5              -\n" +
				"  november.jsonl        2           500              5         $12000\n",
		},
		{
			name:    "should leave the usage block out when no file carries usage",
			runs:    []Run{{File: "march.jsonl"}, {File: "november.jsonl"}},
			without: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder
			if err := WriteComparisonTable(&b, Comparison{Runs: tc.runs, Compared: 2, Questions: []QuestionComparison{question}}); err != nil {
				t.Fatal(err)
			}

			out := b.String()

			if tc.without && strings.Contains(out, "usage") {
				t.Errorf("table holds a usage block\n%s", out)
			}

			if !tc.without && !strings.HasSuffix(out, tc.want) {
				t.Errorf("table ends\n%s\nwant it to end\n%s", out, tc.want)
			}
		})
	}
}
