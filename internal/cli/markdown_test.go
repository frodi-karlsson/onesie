package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		failing  bool
		wantCode int
		want     string
	}{
		{
			name: "should write one record as a markdown table",
			args: []string{"is this urgent", "-o", "markdown"},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `answer` | 0.5 | |\n" +
				"\n" +
				"_m_\n",
		},
		{
			name:     "should open with the assertion that failed",
			args:     []string{"is this urgent", "-o", "md", "--assert", "answer.value > 0.9"},
			wantCode: ExitRejected,
			want: "> [!CAUTION]\n" +
				"> **Failed:** `answer.value > 0.9` did not hold.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `answer` | 0.5 | |\n" +
				"\n" +
				"_m_\n",
		},
		{
			name: "should quote the abstain expression when the record abstained",
			args: []string{
				"is this urgent", "-o", "markdown", "--assert", "answer.value > 0.9",
				"--abstain-if", "answer.value > 0.4",
			},
			wantCode: ExitAbstain,
			want: "> [!WARNING]\n" +
				"> **Unsure:** `answer.value > 0.9` did not hold and `answer.value > 0.4` did, " +
				"so a person should decide.\n" +
				"\n" +
				"| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `answer` | 0.5 | |\n" +
				"\n" +
				"_m_\n",
		},
		{
			name: "should add the token counts under usage",
			args: []string{"is this urgent", "-o", "markdown", "--usage"},
			want: "| question | answer | confidence |\n" +
				"|---|---|---|\n" +
				"| `answer` | 0.5 | |\n" +
				"\n" +
				"_m, 0 in, 0 out tokens_\n",
		},
		{
			name:     "should write a failed request as a caution with no table",
			args:     []string{"is this urgent", "-o", "markdown", "--assert", "answer.value > 0.9"},
			failing:  true,
			wantCode: ExitUsage,
			want:     "> [!CAUTION]\n> **No answer:** `",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := answeringServer(t, nil)
			if tc.failing {
				srv = refusingServer(t)
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader("the site is down")),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(nil)),
				WithClientFactory(stubFactory(srv.URL)),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(tc.args)

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if tc.failing {
				if !strings.HasPrefix(out.String(), tc.want) || strings.Contains(out.String(), "| question") {
					t.Errorf("stdout =\n%s\nwant a caution starting %q and no table", out.String(), tc.want)
				}

				return
			}

			if out.String() != tc.want {
				t.Errorf("stdout =\n%s\nwant\n%s", out.String(), tc.want)
			}
		})
	}
}

func refusingServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)

		if _, err := io.WriteString(w, `{"error":{"message":"bad <b>request</b>"}}`); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv
}

func TestMarkdown(t *testing.T) {
	t.Parallel()

	const tickets = `{"id":"T-1","body":"site down"}` + "\n" + "not json\n" + `{"id":"@T-2","body":"slow"}` + "\n"

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		want     string
	}{
		{
			name:     "should write one table with a gate column, the summary and the model",
			args:     []string{"is this urgent", "-i", "jsonl", "--id", ".id", "-o", "markdown", "--assert", "answer.value > 0.4"},
			stdin:    tickets,
			wantCode: ExitRecords,
			want: "| id | `answer` | gate | error |\n" +
				"|---|---|---|---|\n" +
				"| `T-1` | 0.5 | passed | |\n" +
				"| | | | `line 2: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')` |\n" +
				"| `@T-2` | 0.5 | passed | |\n" +
				"\n" +
				"> [!CAUTION]\n" +
				"> 3 records: 2 passed, 1 with no answer.\n" +
				"\n" +
				"_m_\n",
		},
		{
			name:  "should leave out the id and gate columns and sum the usage",
			args:  []string{"--ask", "urgent=is this urgent", "-i", "lines", "-o", "md", "--usage"},
			stdin: "site down\nslow\n",
			want: "| `urgent` | error |\n" +
				"|---|---|\n" +
				"| 0.5 | |\n" +
				"| 0.5 | |\n" +
				"\n" +
				"> [!TIP]\n" +
				"> 2 records: 2 answered.\n" +
				"\n" +
				"_m, 0 in, 0 out tokens_\n",
		},
		{
			name:     "should warn when the worst record is an unsure",
			args:     []string{"is this urgent", "-i", "lines", "-o", "markdown", "--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4"},
			stdin:    "site down\n",
			wantCode: ExitAbstain,
			want: "| `answer` | gate | error |\n" +
				"|---|---|---|\n" +
				"| 0.5 | unsure | |\n" +
				"\n" +
				"> [!WARNING]\n" +
				"> 1 record: 1 unsure.\n" +
				"\n" +
				"_m_\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := answeringServer(t, nil)

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(nil)),
				WithClientFactory(stubFactory(srv.URL)),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(tc.args)

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if out.String() != tc.want {
				t.Errorf("stdout =\n%s\nwant\n%s", out.String(), tc.want)
			}
		})
	}

	t.Run("should refuse the flags whose output a markdown table cannot carry", func(t *testing.T) {
		t.Parallel()

		out := filepath.Join(t.TempDir(), "answers.md")

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should refuse -q",
				args:    []string{"is this urgent", "-o", "markdown", "-q"},
				wantErr: "onesie: -q suppresses output, which leaves -o markdown nothing to write",
			},
			{
				name:    "should refuse --merge",
				args:    []string{"is this urgent", "-i", "jsonl", "-o", "md", "--merge"},
				wantErr: "onesie: --merge needs -o json, values, csv or tsv",
			},
			{
				name:    "should refuse -r",
				args:    []string{"is this urgent", "-o", "markdown", "-r"},
				wantErr: "onesie: -r and -o are mutually exclusive",
			},
			{
				name: "should refuse --resume",
				args: []string{"is this urgent", "-i", "jsonl", "-o", "markdown", "--out", out, "--resume"},
				wantErr: "onesie: --resume reads each record's outcome back from --out, " +
					"which a markdown table does not keep. Use -o json, values, csv or tsv",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var stdout, errOut bytes.Buffer

				root := NewRootCmd(
					BuildInfo{Version: "1.2.3"},
					WithStdin(strings.NewReader("{}\n")),
					WithStdinTTY(false),
					WithStdoutTTY(false),
					WithKeychain(noKeychain()),
					WithLookupEnv(lookupFrom(nil)),
					WithClientFactory(stubFactory(answeringServer(t, nil).URL)),
				)

				root.SetOut(&stdout)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				if code := Execute(t.Context(), root); code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitUsage, errOut.String())
				}

				if !strings.Contains(errOut.String(), tc.wantErr) {
					t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
				}

				if stdout.String() != "" {
					t.Errorf("stdout = %q, want nothing", stdout.String())
				}
			})
		}
	})

	t.Run("should write the table into --out when not resuming", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "answers.md")

		var stdout, errOut bytes.Buffer

		root := NewRootCmd(
			BuildInfo{Version: "1.2.3"},
			WithStdin(strings.NewReader("site down\n")),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithKeychain(noKeychain()),
			WithLookupEnv(lookupFrom(nil)),
			WithClientFactory(stubFactory(answeringServer(t, nil).URL)),
		)

		root.SetOut(&stdout)
		root.SetErr(&errOut)
		root.SetArgs([]string{"is this urgent", "-i", "lines", "-o", "markdown", "--out", path})

		if code := Execute(t.Context(), root); code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut.String())
		}

		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading --out: %v", err)
		}

		want := "| `answer` | error |\n|---|---|\n| 0.5 | |\n\n> [!TIP]\n> 1 record: 1 answered.\n\n_m_\n"
		if string(written) != want {
			t.Errorf("--out =\n%s\nwant\n%s", written, want)
		}
	})
}
