//go:build integration

package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
)

func TestFilterIntegration(t *testing.T) {
	requireAPIKey(t)

	tests := []struct {
		name  string
		args  []string
		stdin string
		check func(t *testing.T, out string)
	}{
		{
			name:  "should score an urgent message high",
			args:  []string{"does this convey urgency", "-o", "json"},
			stdin: "EVERYTHING IS DOWN, CUSTOMERS CANNOT CHECK OUT, CALL ME NOW",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var answer struct {
					Value float64 `json:"value"`
				}

				if err := json.Unmarshal(record["answer"], &answer); err != nil {
					t.Fatalf("decoding the answer: %v", err)
				}

				if answer.Value < 0.6 {
					t.Errorf("urgency = %.4f, want an urgent message above 0.6", answer.Value)
				}

				if _, ok := record["model"]; !ok {
					t.Error("every record must carry the versioned model id")
				}
			},
		},
		{
			name:  "should score a calm message low",
			args:  []string{"does this convey urgency", "-o", "json"},
			stdin: "just following up on that ticket whenever you get a chance, no rush at all",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var answer struct {
					Value float64 `json:"value"`
				}

				if err := json.Unmarshal(record["answer"], &answer); err != nil {
					t.Fatalf("decoding the answer: %v", err)
				}

				if answer.Value > 0.4 {
					t.Errorf("urgency = %.4f, want a calm message below 0.4", answer.Value)
				}
			},
		},
		{
			name: "should route a billing complaint to the billing team",
			args: []string{
				"which team should handle this",
				"--pick", "billing,technical,sales",
				"--desc", "billing=payments, invoicing and refunds",
				"--desc", "technical=bugs, outages and integrations",
				"--desc", "sales=pricing and new contracts",
				"-r",
			},
			stdin: "I was charged twice for last month and want one of them refunded.",
			check: func(t *testing.T, out string) {
				t.Helper()

				if got := strings.TrimSpace(out); got != "billing" {
					t.Errorf("team = %q, want billing", got)
				}
			},
		},
		{
			name: "should place an angry message high on a rubric",
			args: []string{
				"how frustrated is the customer",
				"--rate", "calm,annoyed,furious",
				"-o", "json",
			},
			stdin: "This is the fourth time I have written. Cancel my account today.",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var answer struct {
					Value string             `json:"value"`
					Norm  float64            `json:"norm"`
					Score float64            `json:"score"`
					P     map[string]float64 `json:"p"`
				}

				if err := json.Unmarshal(record["answer"], &answer); err != nil {
					t.Fatalf("decoding the answer: %v", err)
				}

				if answer.Value == "calm" {
					t.Error("value = calm, want an angry message to land higher")
				}

				if answer.Norm < 0 || answer.Norm > 1 {
					t.Errorf("norm = %f, want it inside 0 to 1", answer.Norm)
				}

				// The probabilities must come back keyed by the labels this invocation supplied,
				// not by the API's own index strings. That re-keying is the whole reason labels
				// exist and no stub can prove it.
				for _, label := range []string{"calm", "annoyed", "furious"} {
					if _, ok := answer.P[label]; !ok {
						t.Errorf("p is missing the label %q, got keys %v", label, keysOf(answer.P))
					}
				}
			},
		},
		{
			name: "should answer several questions in one request",
			args: []string{
				"--ask", "urgent=does this convey urgency",
				"--ask", "refund=is the customer asking for money back",
				"-o", "values",
			},
			stdin: "I was charged twice and I need that refunded today, this is urgent.",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				for _, id := range []string{"urgent", "refund"} {
					if _, ok := record[id]; !ok {
						t.Errorf("record is missing the answer for %q", id)
					}
				}

				if _, ok := record["model"]; ok {
					t.Error("values output must not carry the model key")
				}
			},
		},
		{
			name:  "should exit ok under -q above the threshold",
			args:  []string{"does this convey urgency", "-q", "--threshold", "0.5"},
			stdin: "EVERYTHING IS DOWN, CALL ME NOW",
			check: func(t *testing.T, out string) {
				t.Helper()

				if strings.TrimSpace(out) != "" {
					t.Errorf("-q must print nothing, got %q", out)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "integration"},
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			tc.check(t, out.String())
		})
	}
}

func TestFileIntegration(t *testing.T) {
	requireAPIKey(t)

	dir := t.TempDir()

	questionFile := filepath.Join(dir, "triage.yaml")
	questions := "urgent:\n  ask: does this convey urgency\n  threshold: 0.6\n" +
		"team:\n  ask: which team should handle this\n  pick:\n" +
		"    billing: payments, invoicing and refunds\n" +
		"    technical: bugs, outages and integrations\n" +
		"  min_confidence: 0.5\n  fallback: human\n"

	if err := os.WriteFile(questionFile, []byte(questions), 0o600); err != nil {
		t.Fatalf("writing the question file: %v", err)
	}

	bodyFile := filepath.Join(dir, "body.json")
	body := `{"state":"this is the fourth time I have written",` +
		`"questions":{"mood":{"type":"score","instructions":"how cross is the writer",` +
		`"criteria":["calm","annoyed","furious"]}}}`

	if err := os.WriteFile(bodyFile, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the body file: %v", err)
	}

	rubricFile := filepath.Join(dir, "rubric.yaml")
	rubric := "frustration:\n  ask: how frustrated is the customer\n  rate:\n" +
		"    - calm: states facts without affect\n" +
		"    - annoyed: civil but terse\n" +
		"    - furious: strong language or threats to leave\n"

	if err := os.WriteFile(rubricFile, []byte(rubric), 0o600); err != nil {
		t.Fatalf("writing the rubric file: %v", err)
	}

	tests := []struct {
		name  string
		args  []string
		stdin string
		check func(t *testing.T, out string)
	}{
		{
			name:  "should answer a question file and apply its policy",
			args:  []string{"-f", questionFile, "-o", "json"},
			stdin: "I was charged twice and need a refund today, this is urgent.",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var urgent struct {
					Value    float64 `json:"value"`
					Decision *bool   `json:"decision"`
				}

				if err := json.Unmarshal(record["urgent"], &urgent); err != nil {
					t.Fatalf("decoding urgent: %v", err)
				}

				if urgent.Decision == nil {
					t.Error("a threshold in the file must produce a decision")
				}

				var team struct {
					Value string `json:"value"`
				}

				if err := json.Unmarshal(record["team"], &team); err != nil {
					t.Fatalf("decoding team: %v", err)
				}

				if team.Value != "billing" {
					t.Errorf("team = %q, want billing", team.Value)
				}
			},
		},
		{
			name:  "should keep file question order in the record",
			args:  []string{"-f", questionFile, "-o", "json"},
			stdin: "I was charged twice.",
			check: func(t *testing.T, out string) {
				t.Helper()

				// The record is one line of JSON and the encoder writes keys in plan order, so
				// the file's order is observable in the raw text rather than only in the map.
				urgentAt := strings.Index(out, `"urgent"`)
				teamAt := strings.Index(out, `"team"`)

				if urgentAt < 0 || teamAt < 0 {
					t.Fatalf("both questions must appear, got %s", out)
				}

				if urgentAt > teamAt {
					t.Errorf("urgent is defined first in the file and must come first, got %s",
						out)
				}
			},
		},
		{
			name:  "should apply a labelled rubric from a file",
			args:  []string{"-f", rubricFile, "-o", "json"},
			stdin: "This is the fourth time I have written. Cancel my account today.",
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var frustration struct {
					Value string             `json:"value"`
					Norm  float64            `json:"norm"`
					P     map[string]float64 `json:"p"`
				}

				if err := json.Unmarshal(record["frustration"], &frustration); err != nil {
					t.Fatalf("decoding frustration: %v", err)
				}

				// A labelled question re-keys the probabilities to its own labels and drops the
				// legend, which is the opposite of the body case below.
				for _, label := range []string{"calm", "annoyed", "furious"} {
					if _, ok := frustration.P[label]; !ok {
						t.Errorf("p is missing the label %q, got %v", label, frustration.P)
					}
				}

				if frustration.Value == "calm" {
					t.Error("an angry message must not land on calm")
				}

				if frustration.Norm < 0 || frustration.Norm > 1 {
					t.Errorf("norm = %f, want it inside 0 to 1", frustration.Norm)
				}
			},
		},
		{
			name: "should normalize a body score question by index and keep its legend",
			args: []string{"-f", bodyFile, "-o", "json"},
			check: func(t *testing.T, out string) {
				t.Helper()

				record := decodeRecord(t, out)

				var mood struct {
					Value  string             `json:"value"`
					Norm   float64            `json:"norm"`
					P      map[string]float64 `json:"p"`
					Legend map[string]string  `json:"legend"`
				}

				if err := json.Unmarshal(record["mood"], &mood); err != nil {
					t.Fatalf("decoding mood: %v", err)
				}

				// This is the whole point of the body path, and no stub can prove it. An
				// unlabelled question keeps the API's index keys and its legend, where a labelled
				// one replaces both.
				for _, index := range []string{"0", "1", "2"} {
					if _, ok := mood.P[index]; !ok {
						t.Errorf("p is missing the index key %q, got %v", index, mood.P)
					}
				}

				if len(mood.Legend) == 0 {
					t.Error("an unlabelled score question must keep the API's legend")
				}

				if mood.Norm < 0 || mood.Norm > 1 {
					t.Errorf("norm = %f, want it inside 0 to 1", mood.Norm)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "integration"},
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			tc.check(t, out.String())
		})
	}
}

func TestStreamIntegration(t *testing.T) {
	requireAPIKey(t)

	const tickets = `{"id":1,"body":"EVERYTHING IS DOWN, CALL ME NOW"}
{"id":2,"body":"just following up, no rush at all"}
{"id":3,"body":"I was charged twice and need a refund today"}
{"id":4,"body":"thanks for the quick fix yesterday"}
{"id":5,"body":"THIS IS THE FOURTH TIME I HAVE WRITTEN, CANCEL MY ACCOUNT"}
{"id":6,"body":"quick question about your pricing page"}
`

	tests := []struct {
		name  string
		args  []string
		stdin string
		check func(t *testing.T, out string)
	}{
		{
			name:  "should answer every record in input order under concurrency",
			args:  []string{"does `body` convey urgency", "-i", "jsonl", "-j", "4", "--merge"},
			stdin: tickets,
			check: func(t *testing.T, out string) {
				t.Helper()

				lines := nonEmptyLines(out)
				if len(lines) != 6 {
					t.Fatalf("lines = %d, want 6:\n%s", len(lines), out)
				}

				// The merge keeps the input's own fields, so the ids prove the output is in input
				// order even though four workers were running against real, variable latency.
				// This is the assertion no stub can make, because a stub answers instantly and
				// uniformly and would come back in order by accident.
				for i, line := range lines {
					var record struct {
						ID      int `json:"id"`
						Answers struct {
							Answer struct {
								Value float64 `json:"value"`
							} `json:"answer"`
						} `json:"answers"`
					}

					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatalf("line %d is not valid json: %q", i, line)
					}

					if record.ID != i+1 {
						t.Errorf("line %d has id %d, want %d. The output is out of order",
							i, record.ID, i+1)
					}
				}
			},
		},
		{
			name:  "should score the urgent tickets above the calm ones",
			args:  []string{"does `body` convey urgency", "-i", "jsonl", "-j", "4", "-o", "json"},
			stdin: tickets,
			check: func(t *testing.T, out string) {
				t.Helper()

				lines := nonEmptyLines(out)
				if len(lines) != 6 {
					t.Fatalf("lines = %d, want 6", len(lines))
				}

				values := make([]float64, 0, 6)

				for _, line := range lines {
					var record map[string]json.RawMessage
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatalf("decoding %q: %v", line, err)
					}

					var answer struct {
						Value float64 `json:"value"`
					}

					if err := json.Unmarshal(record["answer"], &answer); err != nil {
						t.Fatalf("decoding the answer: %v", err)
					}

					values = append(values, answer.Value)
				}

				// Records 1 and 5 are shouting, 2 and 4 are not. Comparing them to each other
				// rather than to a fixed threshold keeps this alive across model releases.
				urgent := []int{0, 4}
				calm := []int{1, 3}

				for _, u := range urgent {
					for _, c := range calm {
						if values[u] <= values[c] {
							t.Errorf(
								"record %d scored %.4f and record %d scored %.4f, "+
									"want the urgent one higher",
								u+1, values[u], c+1, values[c])
						}
					}
				}
			},
		},
		{
			name:  "should emit every record exactly once under unordered",
			args:  []string{"does `body` convey urgency", "-i", "jsonl", "-j", "4", "--unordered", "--merge"},
			stdin: tickets,
			check: func(t *testing.T, out string) {
				t.Helper()

				lines := nonEmptyLines(out)
				if len(lines) != 6 {
					t.Fatalf("lines = %d, want 6:\n%s", len(lines), out)
				}

				// Order is explicitly not guaranteed here, so the assertion is that every id
				// appears exactly once. A dropped or duplicated record is the failure that
				// matters, and it is the one --unordered could plausibly introduce.
				seen := map[int]int{}

				for _, line := range lines {
					var record struct {
						ID int `json:"id"`
					}

					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatalf("line is not valid json: %q", line)
					}

					seen[record.ID]++
				}

				for id := 1; id <= 6; id++ {
					if seen[id] != 1 {
						t.Errorf("id %d appeared %d times, want once. Got %v", id, seen[id], seen)
					}
				}
			},
		},
		{
			name: "should keep going after a record jev could not read",
			args: []string{"does `body` convey urgency", "-i", "jsonl", "-j", "2"},
			stdin: `{"id":1,"body":"EVERYTHING IS DOWN"}
not json at all
{"id":3,"body":"no rush"}
`,
			check: func(t *testing.T, out string) {
				t.Helper()

				lines := nonEmptyLines(out)
				if len(lines) != 3 {
					t.Fatalf("lines = %d, want 3, one per input line:\n%s", len(lines), out)
				}

				var middle map[string]json.RawMessage
				if err := json.Unmarshal([]byte(lines[1]), &middle); err != nil {
					t.Fatalf("the failure line is not valid json: %q", lines[1])
				}

				if _, ok := middle["error"]; !ok {
					t.Errorf("the middle line should carry an error key, got %q", lines[1])
				}

				// The records either side must be real answers, so one bad line costs one record
				// rather than the batch.
				for _, i := range []int{0, 2} {
					var record map[string]json.RawMessage
					if err := json.Unmarshal([]byte(lines[i]), &record); err != nil {
						t.Fatalf("line %d is not valid json: %q", i, lines[i])
					}

					if _, ok := record["error"]; ok {
						t.Errorf("line %d should be an answer, not an error: %q", i, lines[i])
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "integration"},
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			code := cli.Execute(t.Context(), root)

			// The malformed line case finishes with one failed record, which is exit 6 rather
			// than a failure of the run.
			if code != cli.ExitOK && code != cli.ExitRecords {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			tc.check(t, out.String())
		})
	}
}

func decodeRecord(t *testing.T, out string) map[string]json.RawMessage {
	t.Helper()

	record := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &record); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}

	return record
}

func keysOf(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	return keys
}

func nonEmptyLines(out string) []string {
	lines := make([]string, 0, 8)

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

func requireAPIKey(t *testing.T) {
	t.Helper()

	if _, ok := os.LookupEnv("TYPESAFE_API_KEY"); !ok {
		t.Skip("TYPESAFE_API_KEY is not set, skipping the live suite")
	}
}
