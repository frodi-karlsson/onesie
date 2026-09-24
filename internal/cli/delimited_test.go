package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestDelimited(t *testing.T) {
	t.Parallel()

	const tickets = "id,body\n1,the site is down\n2,\"a, quoted\nbody\"\n"

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		want     string
		wantErr  string
	}{
		{
			name:  "should read csv and write csv with the input columns first",
			args:  []string{"does `body` convey urgency", "-i", "csv", "-o", "csv", "--merge"},
			stdin: tickets,
			want:  "id,body,answer,error\n1,the site is down,0.5,\n2,\"a, quoted\nbody\",0.5,\n",
		},
		{
			name:  "should write tsv without the input columns when not merging",
			args:  []string{"--ask", "urgent=is this urgent", "-i", "csv", "-o", "tsv"},
			stdin: tickets,
			want:  "urgent\terror\n0.5\t\n0.5\t\n",
		},
		{
			name:     "should add an assert column and exit 1 on a false assertion",
			args:     []string{"is this urgent", "-i", "tsv", "-o", "csv", "--assert", "answer.value > 0.9"},
			stdin:    "body\nsite down\n",
			wantCode: ExitRejected,
			want:     "answer,assert,error\n0.5,false,\n",
		},
		{
			name:  "should write one record as a header and a row",
			args:  []string{"is this urgent", "-o", "csv"},
			stdin: "the site is down",
			want:  "answer,error\n0.5,\n",
		},
		{
			name:  "should keep the columns of a row that fails",
			args:  []string{"is this urgent", "-i", "csv", "-o", "csv", "--merge"},
			stdin: "id,body\n1\n2,ok\n",
			want: "id,body,answer,error\n" +
				",,,\"line 2: row has 1 fields, the header has 2\"\n" +
				"2,ok,0.5,\n",
			wantCode: ExitRecords,
		},
		{
			name:  "should keep an input column named answers under -o csv",
			args:  []string{"is this urgent", "-i", "csv", "-o", "csv", "--merge"},
			stdin: "answers\nsite down\n",
			want:  "answers,answer,error\nsite down,0.5,\n",
		},
		{
			name:     "should refuse an input column named like a question",
			args:     []string{"--ask", "body=is this urgent", "-i", "csv", "-o", "csv", "--merge"},
			stdin:    tickets,
			wantCode: ExitUsage,
			wantErr:  "an input column has the name of an output column: 'body'",
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
				t.Errorf("stdout =\n%q\nwant\n%q", out.String(), tc.want)
			}

			if tc.wantErr != "" && !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
		})
	}

	t.Run("should resume a csv file by rows, not lines, and keep one header", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		srv := answeringServer(t, &calls)

		path := filepath.Join(t.TempDir(), "answers.csv")
		existing := "id,body,answer,error\n1,\"two\nlines\",0.5,\n2,cut"

		if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
			t.Fatalf("writing the existing file: %v", err)
		}

		fingerprint := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", "") + "\n"
		if err := os.WriteFile(path+".onesie", []byte(fingerprint), 0o600); err != nil {
			t.Fatalf("writing the existing fingerprint: %v", err)
		}

		var out, errOut bytes.Buffer

		root := NewRootCmd(
			BuildInfo{Version: "1.2.3"},
			WithStdin(strings.NewReader("id,body\n1,\"two\nlines\"\n2,second\n3,third\n")),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithKeychain(noKeychain()),
			WithLookupEnv(lookupFrom(nil)),
			WithClientFactory(stubFactory(srv.URL)),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{
			"is this urgent", "-i", "csv", "-o", "csv", "--merge", "--out", path, "--resume",
		})

		if code := Execute(t.Context(), root); code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut.String())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the file: %v", err)
		}

		want := "id,body,answer,error\n1,\"two\nlines\",0.5,\n2,second,0.5,\n3,third,0.5,\n"
		if string(data) != want {
			t.Errorf("file =\n%q\nwant\n%q", data, want)
		}

		if calls.Load() != 2 {
			t.Errorf("requests = %d, want 2", calls.Load())
		}
	})
}

func answeringServer(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls != nil {
			calls.Add(1)
		}

		w.Header().Set("Content-Type", "application/json")

		if _, err := io.WriteString(w,
			`{"model":"m","answers":{"answer":{"type":"noul","noul":0.5},`+
				`"urgent":{"type":"noul","noul":0.5},"body":{"type":"noul","noul":0.5}},"usage":{}}`); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv
}
