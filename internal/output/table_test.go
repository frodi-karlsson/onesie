package output_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		columns  string
		terminal int
		hasTerm  bool
		want     int
	}{
		{name: "should prefer a numeric COLUMNS", columns: "120", want: 120},
		{
			name:    "should prefer COLUMNS over the terminal",
			columns: "120", terminal: 200, hasTerm: true, want: 120,
		},
		{name: "should ignore a non numeric COLUMNS", columns: "wide", want: 80},
		{
			name:    "should fall back to the terminal when COLUMNS is not numeric",
			columns: "wide", terminal: 200, hasTerm: true, want: 200,
		},
		{name: "should fall back to eighty when COLUMNS is unset", columns: "", want: 80},
		{
			name:     "should use the terminal when COLUMNS is unset",
			terminal: 132, hasTerm: true, want: 132,
		},
		{name: "should ignore a zero COLUMNS", columns: "0", want: 80},
		{name: "should ignore a negative COLUMNS", columns: "-5", want: 80},
		{
			name:    "should fall back to eighty when the terminal query fails",
			hasTerm: false, want: 80,
		},
		{
			name:     "should fall back to eighty when the terminal reports no width",
			terminal: 0, hasTerm: true, want: 80,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := output.Width(
				func(string) (string, bool) {
					return tc.columns, tc.columns != ""
				},
				func() (int, bool) {
					return tc.terminal, tc.hasTerm
				},
			)

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
				Model: "onesie-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"model onesie-1.13.0", "urgent", "0.9200"},
		},
		{
			name:    "should print a false assertion beside the model in the header",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
				AssertFailed: true,
			},
			contains: []string{"model onesie-1.13.0\nassert false\n"},
		},
		{
			name:    "should print no assert line when the assertion held",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			absent: []string{"assert", "abstain"},
		},
		{
			name:    "should print an abstain line beside the model when the record abstained",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
				Abstained: true,
			},
			contains: []string{"model onesie-1.13.0\nabstain\n"},
			absent:   []string{"assert"},
		},
		{
			name:    "should print a false assertion with no model in the header",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
				AssertFailed: true,
			},
			contains: []string{"assert false"},
			absent:   []string{"model"},
		},
		{
			name:    "should add usage to the header when present",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
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
				Model: "onesie-1.13.0",
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
			name:    "should print the score and the norm of a rate answer",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "mood", Answer: &answer.Answer{
						Value: "furious", Confidence: &confidence, Score: &score, Norm: &norm,
						P: &answer.Probabilities{
							Keys:   []string{"calm", "furious"},
							Values: map[string]float64{"calm": 0.36, "furious": 0.64},
						},
					}},
				},
			},
			contains: []string{"score 1.0000", "norm 0.5000"},
		},
		{
			name:    "should omit the score and the norm of a yes/no answer",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92, Confidence: &confidence}},
				},
			},
			absent: []string{"score", "norm"},
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
			name:    "should show the legend text beside an index",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "frustration", Answer: &answer.Answer{
						Value: "1", Confidence: &confidence, Score: &score, Norm: &norm,
						P: &answer.Probabilities{
							Keys:   []string{"0", "1", "2"},
							Values: map[string]float64{"0": 0.05, "1": 0.90, "2": 0.05},
						},
						Legend: map[string]string{
							"0": "Calm", "1": "Frustrated", "2": "Very angry",
						},
					}},
				},
			},
			contains: []string{"0 Calm", "1 Frustrated", "2 Very angry"},
		},
		{
			name:    "should keep a bare index when there is no legend",
			columns: 80,
			rec: output.Record{
				Answers: []output.Named{
					{ID: "frustration", Answer: &answer.Answer{
						Value: "1",
						P: &answer.Probabilities{
							Keys:   []string{"0", "1"},
							Values: map[string]float64{"0": 0.1, "1": 0.9},
						},
					}},
				},
			},
			contains: []string{"  0 ", "  1 "},
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
		{
			name: "should print the decision beside the probability for a decided yes/no answer",
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{
					Value: 0.92, Decided: true, Decision: true,
				}}},
			},
			columns:  80,
			contains: []string{"\nurgent  true  0.9200"},
		},
		{
			name: "should print the bare probability for an undecided yes/no answer",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.92}}},
			},
			columns:  80,
			contains: []string{"urgent  0.9200"},
		},
		{
			name: "should leave a decided pick answer unchanged",
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{{ID: "team", Answer: &answer.Answer{
					Value: "billing", Decided: true, Decision: "human",
					P: &answer.Probabilities{
						Keys:   []string{"billing"},
						Values: map[string]float64{"billing": 0.41},
					},
				}}},
			},
			columns:  80,
			contains: []string{"\nteam  human\n"},
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

	t.Run("should not panic on an extreme bar width", func(t *testing.T) {
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
	})
}
