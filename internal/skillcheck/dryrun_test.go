package skillcheck

import (
	"reflect"
	"testing"
)

func TestDryRunArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tokens []string
		want   []string
		wantOK bool
	}{
		{
			name:   "should strip -o, -r, -q, --usage, --merge and --stats from a command",
			tokens: []string{"jev", "--ask", "a=x", "-o", "json", "-r", "-q", "--usage", "--merge", "--stats"},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name: "should strip --merge-key too, since it implies --merge",
			tokens: []string{
				"jev", "--ask", "a=x", "--merge-key", "answers",
			},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name: "should strip -i, -j, -m, --state and --state-file when the dry run is --print-questions",
			tokens: []string{
				"jev", "--ask", "a=x", "--assert", "a > 0", "-i", "json", "-j", "4", "-m", "jev-latest",
				"--state", "s", "--state-file", "f",
			},
			want:   []string{"--ask", "a=x", "--assert", "a > 0", "--print-questions"},
			wantOK: true,
		},
		{
			name:   "should leave -i, -j, -m, --state and --state-file alone for --print-request",
			tokens: []string{"jev", "--ask", "a=x", "-i", "json", "-j", "4", "-m", "jev-latest", "--state", "s"},
			want: []string{
				"--ask", "a=x", "-i", "json", "-j", "4", "-m", "jev-latest", "--state", "s",
				"--print-request",
			},
			wantOK: true,
		},
		{
			name:   "should strip a flag and its value when the value is separate",
			tokens: []string{"jev", "--ask", "a=x", "-o", "json"},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name:   "should strip a flag and its value when the value is joined with an equals sign",
			tokens: []string{"jev", "--ask", "a=x", "-o=json"},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name:   "should strip a long flag and its value when the value is joined with an equals sign",
			tokens: []string{"jev", "--ask", "a=x", "--output=json"},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name:   "should pick --print-questions for a command carrying --assert",
			tokens: []string{"jev", "--ask", "a=x", "--assert", "a > 0"},
			want:   []string{"--ask", "a=x", "--assert", "a > 0", "--print-questions"},
			wantOK: true,
		},
		{
			name:   "should pick --print-request otherwise",
			tokens: []string{"jev", "--ask", "a=x"},
			want:   []string{"--ask", "a=x", "--print-request"},
			wantOK: true,
		},
		{
			name: "should leave the question, --ask and the shape flags alone",
			tokens: []string{
				"jev", "--ask", "a=x", "--pick", "yes,no", "--rate", "1,2,3", "--desc", "yes=ok",
				"--sep", ";", "--threshold", "0.5", "--min-confidence", "0.5", "--fallback", "no",
				"is this urgent",
			},
			want: []string{
				"--ask", "a=x", "--pick", "yes,no", "--rate", "1,2,3", "--desc", "yes=ok",
				"--sep", ";", "--threshold", "0.5", "--min-confidence", "0.5", "--fallback", "no",
				"is this urgent", "--print-request",
			},
			wantOK: true,
		},
		{
			name:   "should insert the print flag before a literal -- separator",
			tokens: []string{"jev", "-o", "json", "--", "-is this urgent"},
			want:   []string{"--print-request", "--", "-is this urgent"},
			wantOK: true,
		},
		{
			name:   "should report a command it cannot parse as a jev invocation as skipped",
			tokens: []string{"jq", "'.value > 0.5'"},
			wantOK: false,
		},
		{
			name:   "should report an empty token list as skipped",
			tokens: nil,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := dryRunArgs(tc.tokens)
			if ok != tc.wantOK {
				t.Fatalf("dryRunArgs(%v) ok = %v, want %v", tc.tokens, ok, tc.wantOK)
			}

			if !tc.wantOK {
				return
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dryRunArgs(%v) = %#v, want %#v", tc.tokens, got, tc.want)
			}
		})
	}
}
