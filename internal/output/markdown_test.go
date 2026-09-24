package output_test

import (
	"bytes"
	"encoding/json"
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
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`, 212 in, 3 out tokens, $0.00042_\n",
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
				"_`jev-1.13.0`, 212 in, 3 out tokens_\n",
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
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`_\n",
		},
		{
			name: "should read a low confidence fallback as the fallback and leave its confidence blank",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "team", Answer: &answer.Answer{
					Value: "billing", Confidence: &low, Decision: "human", Decided: true,
					Fallback: "low_confidence",
				}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `team` | fallback `human` | |\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
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
				"_`jev-1.13.0`_\n",
		},
		{
			name: "should write a failed record as a caution with no table when nothing fell back",
			rec: output.Record{
				Failure: &output.Failure{Kind: "http", Status: &status, Message: "http 500: upstream failed"},
				Answers: []output.Named{{ID: "urgent"}},
			},
			opts: gate,
			want: "> [!CAUTION]\n" +
				"> **No answer:** `http 500: upstream failed`\n",
		},
		{
			name: "should keep the table under the caution when a failed record fell back",
			rec: output.Record{
				Failure: &output.Failure{Kind: "http", Status: &status, Message: "http 500: upstream failed"},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Decision: false, Decided: true, Fallback: "error"}},
					{ID: "team"},
				},
			},
			opts: gate,
			want: "> [!CAUTION]\n" +
				"> **No answer:** `http 500: upstream failed`\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | fallback no | |\n",
		},
		{
			name: "should show a probability too small or too large to round as a bound",
			rec: output.Record{Model: "jev-1.13.0", Answers: []output.Named{
				{ID: "low", Answer: &answer.Answer{Value: 0.00001}},
				{ID: "high", Answer: &answer.Answer{Value: 0.99999}},
				{ID: "none", Answer: &answer.Answer{Value: 0.0}},
				{ID: "all", Answer: &answer.Answer{Value: 1.0, Decision: true, Decided: true}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `low` | <0.0001 | |\n" +
				"| `high` | >0.9999 | |\n" +
				"| `none` | 0 | |\n" +
				"| `all` | yes, 1 | |\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name: "should fence the model so it renders as text",
			rec: output.Record{Model: "*bold* @someone", Answers: []output.Named{
				{ID: "urgent", Answer: &answer.Answer{Value: 0.5}},
			}},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `urgent` | 0.5 | |\n" +
				"\n" +
				"_`*bold* @someone`_\n",
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
				"| <code>&#64;team&#124;&#35;12</code> | ``<b>`x`</b>`` | 92% |\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
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

func TestNewMarkdownTable(t *testing.T) {
	t.Parallel()

	confidence := 0.92
	cost := 0.001
	status := 500

	answered := func(id any, urgent float64, team string) output.Record {
		return output.Record{ID: id, Model: "jev-1.13.0", Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Value: urgent}},
			{ID: "team", Answer: &answer.Answer{Value: team, Confidence: &confidence}},
		}}
	}

	rejected := answered("T-1", 0.97, "billing")
	rejected.AssertFailed = true

	unsure := answered("T-4", 0.5, "sales")
	unsure.Abstained = true

	failed := output.Record{
		ID:      "T-3",
		Failure: &output.Failure{Kind: "http", Status: &status, Message: "http 500: upstream failed"},
		Answers: []output.Named{{ID: "urgent"}, {ID: "team"}},
	}

	fellBack := output.Record{
		ID:      "T-5",
		Failure: &output.Failure{Kind: "transport", Message: "connection reset"},
		Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Decision: false, Decided: true, Fallback: "error"}},
			{ID: "team"},
		},
	}

	spent := func(rec output.Record, model string, in, out int) output.Record {
		rec.Model = model
		rec.Usage = &jev.Usage{InputTokens: in, OutputTokens: out, Cost: &cost}

		return rec
	}

	both := []string{"urgent", "team"}

	tests := []struct {
		name     string
		opts     output.MarkdownTableOptions
		records  []output.Record
		complete bool
		want     string
	}{
		{
			name:     "should write one header, a row per record, the summary and the model",
			opts:     output.MarkdownTableOptions{IDs: both, ID: true, Gate: true},
			records:  []output.Record{rejected, answered("T-2", 0.08, "shipping"), failed},
			complete: true,
			want: "| id | `urgent` | `team` | gate | error |\n" +
				"|---|---|---|---|---|\n" +
				"| `T-1` | 0.97 | `billing` | failed | |\n" +
				"| `T-2` | 0.08 | `shipping` | passed | |\n" +
				"| `T-3` | | | | `http 500: upstream failed` |\n" +
				"\n" +
				"> [!CAUTION]\n" +
				"> 3 records: 1 passed, 1 failed, 1 with no answer.\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name:     "should leave out the id and gate columns and tip when every record answered",
			opts:     output.MarkdownTableOptions{IDs: both},
			records:  []output.Record{answered(nil, 0.1, "billing"), answered(nil, 0.2, "sales")},
			complete: true,
			want: "| `urgent` | `team` | error |\n" +
				"|---|---|---|\n" +
				"| 0.1 | `billing` | |\n" +
				"| 0.2 | `sales` | |\n" +
				"\n" +
				"> [!TIP]\n" +
				"> 2 records: 2 answered.\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name:     "should tip when every record passed",
			opts:     output.MarkdownTableOptions{IDs: both, Gate: true},
			records:  []output.Record{answered(nil, 0.1, "billing")},
			complete: true,
			want: "| `urgent` | `team` | gate | error |\n" +
				"|---|---|---|---|\n" +
				"| 0.1 | `billing` | passed | |\n" +
				"\n" +
				"> [!TIP]\n" +
				"> 1 record: 1 passed.\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name:     "should warn when the worst record is an unsure",
			opts:     output.MarkdownTableOptions{IDs: both, ID: true, Gate: true},
			records:  []output.Record{answered("T-2", 0.08, "shipping"), unsure},
			complete: true,
			want: "| id | `urgent` | `team` | gate | error |\n" +
				"|---|---|---|---|---|\n" +
				"| `T-2` | 0.08 | `shipping` | passed | |\n" +
				"| `T-4` | 0.5 | `sales` | unsure | |\n" +
				"\n" +
				"> [!WARNING]\n" +
				"> 2 records: 1 passed, 1 unsure.\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name:     "should caution when a record has no answer and show its fallback",
			opts:     output.MarkdownTableOptions{IDs: both, ID: true},
			records:  []output.Record{fellBack},
			complete: true,
			want: "| id | `urgent` | `team` | error |\n" +
				"|---|---|---|---|\n" +
				"| `T-5` | fallback no | | `connection reset` |\n" +
				"\n" +
				"> [!CAUTION]\n" +
				"> 1 record: 1 with no answer.\n",
		},
		{
			name: "should list every model that answered and sum the usage",
			opts: output.MarkdownTableOptions{IDs: both},
			records: []output.Record{
				spent(answered(nil, 0.1, "billing"), "jev-1.13.0", 10, 2),
				spent(answered(nil, 0.2, "sales"), "jev-1.14.0", 20, 3),
				spent(answered(nil, 0.3, "sales"), "jev-1.13.0", 30, 4),
			},
			complete: true,
			want: "| `urgent` | `team` | error |\n" +
				"|---|---|---|\n" +
				"| 0.1 | `billing` | |\n" +
				"| 0.2 | `sales` | |\n" +
				"| 0.3 | `sales` | |\n" +
				"\n" +
				"> [!TIP]\n" +
				"> 3 records: 3 answered.\n" +
				"\n" +
				"_`jev-1.13.0`, `jev-1.14.0`, 60 in, 9 out tokens, $0.003_\n",
		},
		{
			name:    "should say the run stopped early when it did not read every record",
			opts:    output.MarkdownTableOptions{IDs: both, ID: true, Gate: true},
			records: []output.Record{answered("T-2", 0.08, "shipping"), rejected},
			want: "| id | `urgent` | `team` | gate | error |\n" +
				"|---|---|---|---|---|\n" +
				"| `T-2` | 0.08 | `shipping` | passed | |\n" +
				"| `T-1` | 0.97 | `billing` | failed | |\n" +
				"\n" +
				"> [!CAUTION]\n" +
				"> stopped after 2 records: 1 passed, 1 failed.\n" +
				"\n" +
				"_`jev-1.13.0`_\n",
		},
		{
			name: "should say the run stopped before any record",
			opts: output.MarkdownTableOptions{IDs: both},
			want: "> [!TIP]\n" +
				"> stopped after 0 records.\n",
		},
		{
			name:     "should write a summary for a stream with no records",
			opts:     output.MarkdownTableOptions{IDs: both},
			complete: true,
			want: "> [!TIP]\n" +
				"> 0 records.\n",
		},
		{
			name: "should fence ids and messages that would ping, link or break the table",
			opts: output.MarkdownTableOptions{IDs: []string{"a|b"}, ID: true},
			records: []output.Record{
				{ID: "@someone #12", Failure: &output.Failure{Kind: "input", Message: "line 2: <b>bad</b>\n`x` \\| y"}},
				{ID: json.Number("12"), Answers: []output.Named{{ID: "a|b", Answer: &answer.Answer{Value: 0.5}}}},
				{ID: "", Answers: []output.Named{{ID: "a|b", Answer: &answer.Answer{Value: 0.5}}}},
				{Failure: &output.Failure{Kind: "input", Message: "line 4: not json"}},
			},
			complete: true,
			want: "| id | <code>a&#124;b</code> | error |\n" +
				"|---|---|---|\n" +
				"| `@someone #12` | | <code>line 2&#58; &#60;b&#62;bad&#60;&#47;b&#62; &#96;x&#96; &#92;&#124; y</code> |\n" +
				"| `12` | 0.5 | |\n" +
				"| _empty_ | 0.5 | |\n" +
				"| | | `line 4: not json` |\n" +
				"\n" +
				"> [!CAUTION]\n" +
				"> 4 records: 2 answered, 2 with no answer.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			table := output.NewMarkdownTable(&buf, tc.opts)
			for _, rec := range tc.records {
				if err := table.Write(rec); err != nil {
					t.Fatalf("Write() error = %v", err)
				}
			}

			if err := table.Finish(tc.complete); err != nil {
				t.Fatalf("Finish() error = %v", err)
			}

			if buf.String() != tc.want {
				t.Errorf("wrote\n%s\nwant\n%s", buf.String(), tc.want)
			}
		})
	}
}
