package output_test

import (
	"bytes"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestWriteMarkdown(t *testing.T) {
	t.Parallel()

	confidence := 0.92
	low := 0.41
	cost := 0.00042
	status := 500

	answers := []output.Named{
		{ID: "urgent", Answer: &answer.Answer{Value: 0.97}},
		{ID: "team", Answer: &answer.Answer{Value: "billing", Confidence: &confidence}},
	}

	gate := output.MarkdownOptions{Assert: "urgent.value < 0.9", Abstain: "urgent.value < 0.99"}

	tests := []struct {
		name string
		rec  output.Record
		opts output.MarkdownOptions
		want string
	}{
		{
			name: "should write a table and the model with no alert when there is no gate",
			rec:  output.Record{Model: "jev-1.13.0", Answers: answers},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"| `team` | `billing` | 92% |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should add the token counts and the cost under usage",
			rec: output.Record{
				Model:   "jev-1.13.0",
				Usage:   &jev.Usage{InputTokens: 212, OutputTokens: 3, Cost: &cost},
				Answers: answers[:1],
			},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"\n" +
				"_jev-1.13.0, 212 in, 3 out tokens, $0.00042_\n",
		},
		{
			name: "should leave the cost out when the provider reports none",
			rec: output.Record{
				Model:   "jev-1.13.0",
				Usage:   &jev.Usage{InputTokens: 212, OutputTokens: 3},
				Answers: answers[:1],
			},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"\n" +
				"_jev-1.13.0, 212 in, 3 out tokens_\n",
		},
		{
			name: "should open with a caution when the assertion failed",
			rec:  output.Record{Model: "jev-1.13.0", Answers: answers, AssertFailed: true},
			opts: gate,
			want: "> [!CAUTION]\n" +
				"> **Failed:** `urgent.value < 0.9` did not hold.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"| `team` | `billing` | 92% |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should open with a tip when the assertion held",
			rec:  output.Record{Model: "jev-1.13.0", Answers: answers[:1]},
			opts: gate,
			want: "> [!TIP]\n" +
				"> **Passed:** `urgent.value < 0.9` held.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should open with a warning that a person should decide when the record abstained",
			rec:  output.Record{Model: "jev-1.13.0", Answers: answers[:1], Abstained: true},
			opts: gate,
			want: "> [!WARNING]\n" +
				"> **Unsure:** `urgent.value < 0.9` did not hold and `urgent.value < 0.99` did, " +
				"so a person should decide.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.97 | |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should put the decision ahead of the probability when a policy decided it",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "urgent", Answer: &answer.Answer{Value: 0.97, Decision: true, Decided: true}},
				{ID: "spam", Answer: &answer.Answer{Value: 0.12, Decision: false, Decided: true}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | yes, 0.97 | |\n" +
				"| `spam` | no, 0.12 | |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should round a probability to four places",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "urgent", Answer: &answer.Answer{Value: 0.123456}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.1235 | |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should read a low confidence fallback as the fallback",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "team", Answer: &answer.Answer{
					Value: "billing", Confidence: &low, Decision: "human", Decided: true,
					Fallback: "low_confidence",
				}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `team` | fallback `human` | 41% |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should show a rate answer as its level with the legend beside an index",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "mood", Answer: &answer.Answer{
					Value: "1", Confidence: &confidence, Legend: map[string]string{"1": "Frustrated"},
				}},
				{ID: "severity", Answer: &answer.Answer{Value: "high", Confidence: &confidence}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `mood` | `1 Frustrated` | 92% |\n" +
				"| `severity` | `high` | 92% |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
		{
			name: "should write a failed record as a caution with no table",
			rec: output.Record{
				Failure: &output.Failure{Kind: "http", Status: &status, Message: "http 500: upstream failed"},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Decision: false, Decided: true, Fallback: "error"}},
				},
			},
			opts: gate,
			want: "> [!CAUTION]\n" +
				"> **No answer:** `http 500: upstream failed`\n",
		},
		{
			name: "should keep the usage of a failed record that still cost tokens",
			rec: output.Record{
				Failure: &output.Failure{Kind: "response", Status: &status, Message: "unusable"},
				Usage:   &jev.Usage{InputTokens: 10, OutputTokens: 2},
			},
			want: "> [!CAUTION]\n" +
				"> **No answer:** `unusable`\n" +
				"\n" +
				"_10 in, 2 out tokens_\n",
		},
		{
			name: "should fence data that would ping, link or render",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "@team|#12", Answer: &answer.Answer{Value: "<b>`x`</b>", Confidence: &confidence}},
			}, AssertFailed: true},
			opts: output.MarkdownOptions{Assert: "a\nb"},
			want: "> [!CAUTION]\n" +
				"> **Failed:** `a b` did not hold.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `@team\\|#12` | ``<b>`x`</b>`` | 92% |\n" +
				"\n" +
				"_jev-1.13.0_\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := output.WriteMarkdown(&buf, tc.rec, tc.opts); err != nil {
				t.Fatalf("WriteMarkdown() error = %v", err)
			}

			if buf.String() != tc.want {
				t.Errorf("wrote\n%s\nwant\n%s", buf.String(), tc.want)
			}
		})
	}
}
