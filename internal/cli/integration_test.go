//go:build integration

package cli_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/cli"
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
				// not by the API's own index strings. Keying by label is the whole reason labels
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
				cli.WithKeychain(offKeychain()),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(liveLookup(os.LookupEnv)),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			if code := root.Execute(t.Context()); code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			tc.check(t, out.String())
		})
	}

	t.Run("should answer through --provider openrouter", func(t *testing.T) {
		if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
			t.Skip("OPENROUTER_API_KEY is not set, skipping the OpenRouter case")
		}

		out, errOut, code := runLive(t,
			[]string{"--provider", "openrouter", "does this convey urgency", "-o", "json"},
			"EVERYTHING IS DOWN, CUSTOMERS CANNOT CHECK OUT, CALL ME NOW")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		record := decodeRecord(t, out)

		var model string
		if err := json.Unmarshal(record["model"], &model); err != nil {
			t.Fatalf("decoding the model: %v", err)
		}

		if !strings.HasPrefix(model, "typesafe/") {
			t.Errorf("model = %q, want an OpenRouter id starting with typesafe/", model)
		}
	})

	t.Run("should answer through --provider levanto", func(t *testing.T) {
		if strings.TrimSpace(os.Getenv("LEVANTO_API_KEY")) == "" {
			t.Skip("LEVANTO_API_KEY is not set, skipping the Levanto case")
		}

		out, errOut, code := runLive(t,
			[]string{"--provider", "levanto", "does this convey urgency", "-o", "json"},
			"EVERYTHING IS DOWN, CUSTOMERS CANNOT CHECK OUT, CALL ME NOW")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		record := decodeRecord(t, out)

		var model string
		if err := json.Unmarshal(record["model"], &model); err != nil {
			t.Fatalf("decoding the model: %v", err)
		}

		if !strings.HasPrefix(model, "levanto-sage-") {
			t.Errorf("model = %q, want a Levanto id starting with levanto-sage-", model)
		}
	})

	t.Run("should answer a pick and a rate with one of the names and p over exactly those names on openrouter, levanto and cloudflare", func(t *testing.T) {
		for _, provider := range []struct {
			name string
			args []string
			keys []string
		}{
			{name: "openrouter", args: []string{"--provider", "openrouter"}, keys: []string{"OPENROUTER_API_KEY"}},
			{name: "levanto", args: []string{"--provider", "levanto"}, keys: []string{"LEVANTO_API_KEY"}},
			{
				name: "cloudflare jev", args: []string{"--provider", "cloudflare"},
				keys: []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
			},
			{
				name: "cloudflare clef", args: []string{"--provider", "cloudflare", "-m", "clef"},
				keys: []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
			},
			{
				name: "cloudflare clef-flash", args: []string{"--provider", "cloudflare", "-m", "clef-flash"},
				keys: []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
			},
		} {
			for _, question := range []struct {
				shape string
				args  []string
				names []string
			}{
				{shape: "pick", args: []string{"who should handle this", "--pick", "billing,shipping,technical"}, names: []string{"billing", "shipping", "technical"}},
				{shape: "rate", args: []string{"how frustrated is the customer", "--rate", "calm,annoyed,furious"}, names: []string{"calm", "annoyed", "furious"}},
			} {
				t.Run("should answer a "+question.shape+" through "+provider.name+" with one of the names and p over exactly those names", func(t *testing.T) {
					for _, key := range provider.keys {
						if strings.TrimSpace(os.Getenv(key)) == "" {
							t.Skip(key + " is not set, skipping the " + provider.name + " case")
						}
					}

					args := append(append(slices.Clone(provider.args), question.args...), "-o", "json")

					out, errOut, code := runLive(t, args, "I was charged twice for my order and nobody answers my emails.")
					if code != cli.ExitOK {
						t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
					}

					var answer struct {
						Value string             `json:"value"`
						P     map[string]float64 `json:"p"`
					}

					if err := json.Unmarshal(decodeRecord(t, out)["answer"], &answer); err != nil {
						t.Fatalf("decoding the answer: %v\n%s", err, out)
					}

					if !slices.Contains(question.names, answer.Value) {
						t.Errorf("value = %q, want one of %v", answer.Value, question.names)
					}

					if got := keysOf(answer.P); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(question.names))) {
						t.Errorf("p keys = %v, want %v", got, question.names)
					}
				})
			}
		}
	})

	t.Run("should see the red square through --image", func(t *testing.T) {
		out, errOut, code := runLive(t, []string{
			"--image", "square=" + liveImage(t, true), "--image", "empty=" + liveImage(t, false),
			"--ask", "in_square=is there a red square in `square`",
			"--ask", "in_empty=is there a red square in `empty`", "-o", "json",
		}, "")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		record := decodeRecord(t, out)

		for id, check := range map[string]func(float64) bool{
			"in_square": func(value float64) bool { return value > 0.8 },
			"in_empty":  func(value float64) bool { return value < 0.2 },
		} {
			var answer struct {
				Value float64 `json:"value"`
			}

			if err := json.Unmarshal(record[id], &answer); err != nil {
				t.Fatalf("decoding %s: %v", id, err)
			}

			if !check(answer.Value) {
				t.Errorf("%s = %.4f, want a clear answer", id, answer.Value)
			}
		}
	})

	t.Run("should box the red square on jev-preview", func(t *testing.T) {
		out, errOut, code := runLive(t, []string{
			"--image", "photo=" + liveImage(t, true), "--ask", "sq=the red square in `photo`", "--box",
			"-m", "jev-preview", "-r",
		}, "")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		fields := strings.Fields(out)
		if len(fields) != 4 {
			t.Fatalf("raw output = %q, want four edges", out)
		}

		// The square's edges are 0.25, 0.25, 0.5 and 0.5, in the order left, top, right, bottom.
		const tolerance = 0.05

		for i, want := range []float64{0.25, 0.25, 0.5, 0.5} {
			got, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				t.Fatalf("edge %d = %q: %v", i, fields[i], err)
			}

			if math.Abs(got-want) > tolerance {
				t.Errorf("edge %d = %.3f, want %.2f within %.2f. Edges: %s", i, got, want, tolerance, out)
			}
		}
	})

	t.Run("should read images per line", func(t *testing.T) {
		out, errOut, code := runLive(t, []string{
			"-i", "lines", "--image-field", "photo", "is there a red square in `photo`", "-o", "values",
		}, liveImage(t, true)+"\n"+liveImage(t, false)+"\n")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if lines := nonEmptyLines(out); len(lines) != 2 {
			t.Errorf("lines = %d, want two\n%s", len(lines), out)
		}
	})

	t.Run("should see the red square through --provider openrouter", func(t *testing.T) {
		if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
			t.Skip("OPENROUTER_API_KEY is not set, skipping the OpenRouter case")
		}

		out, errOut, code := runLive(t, []string{
			"--provider", "openrouter", "-m", "typesafe/jev-1.14",
			"--image", "square=" + liveImage(t, true), "--image", "empty=" + liveImage(t, false),
			"--ask", "in_square=is there a red square in `square`",
			"--ask", "in_empty=is there a red square in `empty`", "-o", "values",
		}, "")
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		var values struct {
			InSquare float64 `json:"in_square"`
			InEmpty  float64 `json:"in_empty"`
		}

		if err := json.Unmarshal([]byte(out), &values); err != nil {
			t.Fatalf("decoding %q: %v", out, err)
		}

		if values.InSquare < 0.8 || values.InEmpty > 0.2 {
			t.Errorf("in_square = %.2f, in_empty = %.2f, want a clear yes and a clear no", values.InSquare, values.InEmpty)
		}
	})
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

				// A labelled question keys the probabilities by its labels and carries no legend.
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
				cli.WithKeychain(offKeychain()),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(liveLookup(os.LookupEnv)),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			if code := root.Execute(t.Context()); code != cli.ExitOK {
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

				// The merge keeps the input's fields, so the ids prove input order across four
				// workers with real latency. A stub answers instantly and would come back in order
				// by accident.
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
			name: "should keep going after a record onesie could not read",
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
				cli.WithKeychain(offKeychain()),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(liveLookup(os.LookupEnv)),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			code := root.Execute(t.Context())

			// The malformed line case finishes with one failed record, which is exit 6 rather
			// than a failure of the run.
			if code != cli.ExitOK && code != cli.ExitRecords {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			tc.check(t, out.String())
		})
	}
}

func keysOf(values map[string]float64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	return keys
}

func TestAssertIntegration(t *testing.T) {
	requireAPIKey(t)

	const (
		urgentState = "EVERYTHING IS DOWN, CUSTOMERS CANNOT CHECK OUT, CALL ME NOW"
		calmState   = "Thanks, that fixed it. No rush on the rest."
	)

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		wantKeys []string
	}{
		{
			name: "should open the gate when the answer satisfies the assertion",
			args: []string{
				"--ask", "urgent=does this convey urgency",
				"--assert", "urgent.value > 0.5", "-o", "json",
			},
			stdin:    urgentState,
			wantCode: cli.ExitOK,
			wantKeys: []string{"urgent"},
		},
		{
			name: "should close the gate and still print the record",
			args: []string{
				"--ask", "urgent=does this convey urgency",
				"--assert", "urgent.value > 0.5", "-o", "json",
			},
			stdin:    calmState,
			wantCode: cli.ExitRejected,
			wantKeys: []string{"assert", "urgent"},
		},
		{
			// One assertion that any probability satisfies and one that this state does not, so
			// the false result comes from the combination rather than from a single judgment.
			name: "should combine repeated assertions with and",
			args: []string{
				"--ask", "urgent=does this convey urgency",
				"--assert", "urgent.value >= 0",
				"--assert", "urgent.value > 0.5", "-o", "json",
			},
			stdin:    calmState,
			wantCode: cli.ExitRejected,
			wantKeys: []string{"assert", "urgent"},
		},
		{
			name: "should assert membership of a pick answer",
			args: []string{
				"--ask", "team=which team should handle this",
				"--pick", "billing,technical",
				"--assert", `team.value in ["billing", "technical"]`, "-o", "json",
			},
			stdin:    "I was charged twice and need a refund",
			wantCode: cli.ExitOK,
			wantKeys: []string{"team"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, _, code := runLive(t, tc.args, tc.stdin)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d, output:\n%s", code, tc.wantCode, out)
			}

			record := decodeRecord(t, out)
			for _, key := range tc.wantKeys {
				if _, ok := record[key]; !ok {
					t.Errorf("record has no %q key: %s", key, out)
				}
			}

			if tc.wantCode != cli.ExitRejected {
				return
			}

			if string(record["assert"]) != "false" {
				t.Errorf("assert = %s, want false", record["assert"])
			}
		})
	}
}

func TestStatsIntegration(t *testing.T) {
	requireAPIKey(t)

	t.Run("should summarise a live request on stderr", func(t *testing.T) {
		out, errOut, code := runLive(t,
			[]string{"does this convey urgency", "--stats", "-o", "json"},
			"EVERYTHING IS DOWN, CALL ME NOW")

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if len(nonEmptyLines(errOut)) != 1 {
			t.Fatalf("--stats must write one line, got:\n%s", errOut)
		}

		// Token counts and a model name are the parts no stub can supply, since they come back
		// from the request itself.
		for _, want := range []string{"1 request", "1 question", " in / ", "model jev", "/attempt"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("stats line is missing %q: %s", want, errOut)
			}
		}

		if strings.Contains(errOut, "failed") {
			t.Errorf("a successful run must report no failures: %s", errOut)
		}

		// A positional question is unnamed, so its answer lands under the reserved answer key.
		if _, ok := decodeRecord(t, out)["answer"]; !ok {
			t.Errorf("--stats must leave stdout alone: %s", out)
		}
	})
}

func TestCalibrateIntegration(t *testing.T) {
	const labelled = `{"id":"T-1","urgent":true,"body":"the site is down and customers cannot pay, fix it now"}
{"id":"T-2","urgent":false,"body":"please update my newsletter preferences when you have time"}
{"id":"T-3","urgent":true,"body":"production database is corrupting orders right now, all hands"}
{"id":"T-4","urgent":false,"body":"no rush, just sharing a blog post I liked"}
{"id":"T-5","urgent":true,"body":"a security breach is leaking customer passwords this minute"}
{"id":"T-6","urgent":false,"body":"whenever you get a chance, could you rename the wiki page"}
`

	args := []string{
		"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
		"--label", "urgent=.urgent", "--id", ".id", "-j", "3", "-o", "json",
	}

	for _, tc := range []struct {
		name, env, also string
		provider        []string
	}{
		{name: "should separate obvious yes from obvious no records", env: "TYPESAFE_API_KEY"},
		{
			name: "should separate obvious yes from obvious no records through --provider openrouter",
			env:  "OPENROUTER_API_KEY", provider: []string{"--provider", "openrouter"},
		},
		{
			name: "should separate obvious yes from obvious no records through --provider levanto",
			env:  "LEVANTO_API_KEY", provider: []string{"--provider", "levanto"},
		},
		{
			name: "should separate obvious yes from obvious no records through --provider cloudflare",
			env:  "CLOUDFLARE_API_TOKEN", also: "CLOUDFLARE_ACCOUNT_ID", provider: []string{"--provider", "cloudflare"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{tc.env, tc.also} {
				if name != "" && strings.TrimSpace(os.Getenv(name)) == "" {
					t.Skip(name + " is not set, skipping this case")
				}
			}

			out, errOut, code := runLive(t, append(slices.Clone(tc.provider), args...), labelled)
			if code != cli.ExitOK {
				t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
			}

			if !strings.Contains(errOut, "asking 6 of 6 records, 1 question each") {
				t.Errorf("stderr = %q, want the cost line", errOut)
			}

			var report struct {
				Questions []struct {
					AUC *float64 `json:"auc"`
				} `json:"questions"`
			}

			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("the report is not json: %v\n%s", err, out)
			}

			if len(report.Questions) != 1 || report.Questions[0].AUC == nil || *report.Questions[0].AUC < 0.8 {
				t.Errorf("report = %s, want an AUC of at least 0.8", out)
			}
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

func TestListModelsIntegration(t *testing.T) {
	requireAPIKey(t)

	t.Run("should list the models the account may ask", func(t *testing.T) {
		out, errOut, code := runLive(t, []string{"--list-models"}, "")

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		lines := nonEmptyLines(out)
		if len(lines) == 0 {
			t.Fatal("--list-models printed nothing")
		}

		for i, line := range lines {
			if strings.TrimSpace(strings.Fields(line)[0]) == "" {
				t.Errorf("line %d has no model id: %q", i, line)
			}
		}
	})
}

func TestRequestRoundTripIntegration(t *testing.T) {
	requireAPIKey(t)

	t.Run("should answer a body its own --print-request produced", func(t *testing.T) {
		// No -o on the first run. --print-request writes a request body rather than a record, so
		// the CLI rejects an output format there, and the format belongs to the run that answers.
		args := []string{"--ask", "urgent=does this convey urgency"}

		body, errOut, code := runLive(t,
			append(slices.Clone(args), "--print-request"),
			"EVERYTHING IS DOWN, CALL ME NOW")

		if code != cli.ExitOK {
			t.Fatalf("--print-request exit code = %d, stderr:\n%s", code, errOut)
		}

		out, errOut, code := runLive(t, []string{"-i", "request"}, body)

		if code != cli.ExitOK {
			t.Fatalf("-i request exit code = %d, stderr:\n%s", code, errOut)
		}

		// -i request forwards the API's own response rather than a onesie record, so the question id
		// is read back out of the wire shape.
		var answers struct {
			Answers map[string]json.RawMessage `json:"answers"`
		}

		if err := json.Unmarshal([]byte(out), &answers); err != nil {
			t.Fatalf("decoding the forwarded response %q: %v", out, err)
		}

		if _, ok := answers.Answers["urgent"]; !ok {
			t.Errorf("the round tripped body lost its question: %s", out)
		}
	})
}

func TestProfileTestIntegration(t *testing.T) {
	requireAPIKey(t)

	t.Run("should authenticate with a key read back from the profiles file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "profiles.json")

		// The key is piped in and never held in a variable this test formats. Every assertion
		// below is on the key line or the model count, so a failure cannot print it.
		_, errOut, code := runStored(t, path, []string{"profile", "create", "live", "--provider", "typesafe", "--file"},
			os.Getenv("TYPESAFE_API_KEY"))
		if code != cli.ExitOK {
			t.Fatalf("profile create exit code = %d, stderr:\n%s", code, errOut)
		}

		out, errOut, code := runStored(t, path, []string{"profile", "test", "live"}, "")
		if code != cli.ExitOK {
			t.Fatalf("profile test exit code = %d, stderr:\n%s", code, errOut)
		}

		// The environment is hidden from this run, so reaching the API at all proves the key came
		// off disk. This is the one key path with no offline equivalent.
		if !strings.Contains(out, "key: file") {
			t.Errorf("profile test output = %q, want it to name the file", out)
		}

		count, err := strconv.Atoi(strings.TrimPrefix(nonEmptyLines(out)[3], "models: "))
		if err != nil {
			t.Fatalf("parsing the model count from %q: %v", out, err)
		}

		if count < 1 {
			t.Errorf("models = %d, want at least one", count)
		}

		if _, err := os.Stat(path); err != nil {
			t.Fatalf("profiles.json is gone before delete: %v", err)
		}

		if _, errOut, code = runStored(t, path, []string{"profile", "delete", "live"}, ""); code != cli.ExitOK {
			t.Fatalf("profile delete exit code = %d, stderr:\n%s", code, errOut)
		}

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("profile delete left the file behind, stat error = %v", err)
		}
	})
}

func liveImage(t *testing.T, square bool) string {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := range 480 {
		for x := range 640 {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})

			if square && image.Pt(x, y).In(image.Rect(160, 120, 320, 240)) {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the png: %v", err)
	}

	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing the png: %v", err)
	}

	return path
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

func runLive(t *testing.T, args []string, stdin string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "integration"},
		cli.WithKeychain(offKeychain()),
		cli.WithStdin(strings.NewReader(stdin)),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		// A developer's exported ONESIE_MOCK would otherwise turn the live suite into a mock run.
		cli.WithLookupEnv(liveLookup(os.LookupEnv)),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	// Execute is called on its own line. In a return statement the two String calls are evaluated
	// before it and would report empty buffers.
	code := root.Execute(t.Context())

	return out.String(), errOut.String(), code
}

func runStored(t *testing.T, path string, args []string, stdin string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "integration"},
		cli.WithStdin(strings.NewReader(stdin)),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithProfilesPath(func() (string, error) { return path, nil }),
		// The live key is in the environment of whoever runs this suite, and hiding it proves the
		// stored key is the one sent.
		cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		cli.WithKeychain(offKeychain()),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	// Execute is called on its own line. In a return statement the two String calls are evaluated
	// before it and would report empty buffers.
	code := root.Execute(t.Context())

	return out.String(), errOut.String(), code
}
