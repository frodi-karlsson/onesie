//go:build integration

package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
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

func requireAPIKey(t *testing.T) {
	t.Helper()

	if _, ok := os.LookupEnv("TYPESAFE_API_KEY"); !ok {
		t.Skip("TYPESAFE_API_KEY is not set, skipping the live suite")
	}
}
