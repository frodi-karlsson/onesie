package output_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
)

func TestWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		columns string
		want    int
	}{
		{name: "should prefer a numeric COLUMNS", columns: "120", want: 120},
		{name: "should ignore a non numeric COLUMNS", columns: "wide", want: 80},
		{name: "should fall back to eighty when COLUMNS is unset", columns: "", want: 80},
		{name: "should ignore a zero COLUMNS", columns: "0", want: 80},
		{name: "should ignore a negative COLUMNS", columns: "-5", want: 80},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := output.Width(func(string) (string, bool) {
				return tc.columns, tc.columns != ""
			})

			if got != tc.want {
				t.Errorf("width = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWriteTable(t *testing.T) {
	t.Parallel()

	confidence := 0.81
	score := 1.0
	norm := 0.5

	tests := []struct {
		name     string
		rec      output.Record
		columns  int
		contains []string
		absent   []string
	}{
		{
			name:    "should put the model in the header",
			columns: 80,
			rec: output.Record{
				Model: "jev-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"model jev-1.13.0", "urgent", "0.9200"},
		},
		{
			name:    "should add usage to the header when present",
			columns: 80,
			rec: output.Record{
				Model: "jev-1.13.0",
				Usage: &jev.Usage{InputTokens: 2841, OutputTokens: 71},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"2841 in", "71 out"},
		},
		{
			name:    "should draw a bar per option",
			columns: 80,
			rec: output.Record{
				Model: "jev-1.13.0",
				Answers: []output.Named{
					{ID: "team", Answer: &answer.Answer{
						Value:      "technical",
						Confidence: &confidence,
						P: &answer.Probabilities{
							Keys: []string{"billing", "technical"},
							Values: map[string]float64{
								"billing": 0.10, "technical": 0.88,
							},
						},
					}},
				},
			},
			contains: []string{"team", "technical", "billing", "█", "confidence 0.81"},
		},
		{
			name:    "should keep probability rows in key order",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "severity", Answer: &answer.Answer{
						Value: "high", Score: &score, Norm: &norm,
						P: &answer.Probabilities{
							Keys:   []string{"zebra", "apple"},
							Values: map[string]float64{"zebra": 0.4, "apple": 0.6},
						},
					}},
				},
			},
			contains: []string{"zebra"},
		},
		{
			name:    "should report the failure instead of answers",
			columns: 80,
			rec: output.Record{
				Failure: &output.Failure{Kind: "transport", Message: "connection refused"},
			},
			contains: []string{"error", "transport", "connection refused"},
		},
		{
			name:    "should omit a confidence line when there is none",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			absent: []string{"confidence"},
		},
		{
			name:    "should narrow the bars on a narrow terminal",
			columns: 40,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "team", Answer: &answer.Answer{
						P: &answer.Probabilities{
							Keys:   []string{"a"},
							Values: map[string]float64{"a": 1.0},
						},
					}},
				},
			},
			contains: []string{"█"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder

			if err := output.WriteTable(&buf, tc.rec, tc.columns); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, want := range tc.contains {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("output missing %q\ngot:\n%s", want, buf.String())
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(buf.String(), unwanted) {
					t.Errorf("output should not contain %q\ngot:\n%s", unwanted, buf.String())
				}
			}
		})
	}
}

func TestWriteTableBarWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		columns int
	}{
		{name: "should not panic on a zero width", columns: 0},
		{name: "should not panic on a negative width", columns: -20},
		{name: "should not panic on an absurd width", columns: 100000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder

			rec := output.Record{
				Answers: []output.Named{
					{ID: "team", Answer: &answer.Answer{
						P: &answer.Probabilities{
							Keys:   []string{"a"},
							Values: map[string]float64{"a": 0.5},
						},
					}},
				},
			}

			if err := output.WriteTable(&buf, rec, tc.columns); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
