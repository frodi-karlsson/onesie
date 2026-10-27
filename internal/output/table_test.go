package output_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
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
		{name: "should cap a huge COLUMNS", columns: "100000000", want: 1000},
		{name: "should cap a huge terminal", terminal: 5000, hasTerm: true, want: 1000},
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
	sharp := 0.977
	cost := 0.0000123
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
			name:     "should write a box's edges and confidence",
			columns:  80,
			rec:      output.Record{Model: "m", Answers: []output.Named{{ID: "mug", Box: true, Answer: boxAnswer()}}},
			contains: []string{"mug  left 0.44  top 0.25  right 0.84  bottom 0.82", "confidence 0.92"},
		},
		{
			name:    "should put the model in the header",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"model onesie-1.13.0", "urgent", "0.92"},
			absent:   []string{"0.9200"},
		},
		{
			name:    "should strip terminal controls from the text it prints",
			columns: 80,
			rec: output.Record{
				Model:   "m\x1b[2J1",
				Failure: &output.Failure{Kind: "api", Message: "bad\u009b\x07 news\r"},
				Answers: []output.Named{
					{ID: "team", Answer: &answer.Answer{Value: "bil\x1bling"}},
				},
			},
			contains: []string{"model m[2J1", "bad news", "team  billing"},
			absent:   []string{"\x1b", "\x07", "\r", "\u009b"},
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
				Usage: &onesie.Usage{InputTokens: 2841, OutputTokens: 71},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"2841 in", "71 out"},
			absent:   []string{"$"},
		},
		{
			name:    "should add the cost to the usage in the header",
			columns: 80,
			rec: output.Record{
				Model: "onesie-1.13.0",
				Usage: &onesie.Usage{InputTokens: 2841, OutputTokens: 71, Cost: &cost},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.92}},
				},
			},
			contains: []string{"model onesie-1.13.0  2841 in / 71 out  $0.0000123\n"},
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
			contains: []string{"score 1  norm 0.5\n"},
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
			contains: []string{"\nurgent  true  0.92\n"},
		},
		{
			name: "should print the bare probability for an undecided yes/no answer",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.92}}},
			},
			columns:  80,
			contains: []string{"urgent  0.92\n"},
		},
		{
			name: "should print a yes/no probability as -o json writes it",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.1}}},
			},
			columns:  80,
			contains: []string{"urgent  0.1\n"},
		},
		{
			name: "should print a zero probability as -o json writes it",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.0}}},
			},
			columns:  80,
			contains: []string{"urgent  0\n"},
		},
		{
			name: "should print a certain probability as -o json writes it",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 1.0}}},
			},
			columns:  80,
			contains: []string{"urgent  1\n"},
		},
		{
			name: "should keep every decimal a yes/no probability carries",
			rec: output.Record{
				Model:   "onesie-1.13.0",
				Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.795}}},
			},
			columns:  80,
			contains: []string{"urgent  0.795\n"},
		},
		{
			name: "should print a confidence and p unrounded, as -o json writes them",
			rec: output.Record{
				Answers: []output.Named{{ID: "team", Answer: &answer.Answer{
					Value: "billing", Confidence: &sharp,
					P: &answer.Probabilities{
						Keys:   []string{"billing", "sales"},
						Values: map[string]float64{"billing": 0.58984375, "sales": 0.41015625},
					},
				}}},
			},
			columns:  80,
			contains: []string{"  confidence 0.977\n", " 0.58984375\n", " 0.41015625\n"},
		},
		{
			name: "should fit a long probability inside the width",
			rec: output.Record{
				Answers: []output.Named{{ID: "team", Answer: &answer.Answer{
					Value: "billing",
					P: &answer.Probabilities{
						Keys:   []string{"billing", "sales"},
						Values: map[string]float64{"billing": 0.6666666666666666, "sales": 0.33333333333333337},
					},
				}}},
			},
			columns:  80,
			contains: []string{" 0.33333333333333337\n"},
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

	t.Run("should keep every line inside the width when a probability is long", func(t *testing.T) {
		t.Parallel()

		var buf strings.Builder

		rec := output.Record{Answers: []output.Named{{ID: "team", Answer: &answer.Answer{
			Value: "billing",
			P: &answer.Probabilities{
				Keys:   []string{"billing", "sales"},
				Values: map[string]float64{"billing": 0.6666666666666666, "sales": 0.33333333333333337},
			},
		}}}}

		if err := output.WriteTable(&buf, rec, 80); err != nil {
			t.Fatal(err)
		}

		for line := range strings.SplitSeq(buf.String(), "\n") {
			if width := utf8.RuneCountInString(line); width > 80 {
				t.Errorf("line %q is %d wide, want at most 80", line, width)
			}
		}
	})

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
