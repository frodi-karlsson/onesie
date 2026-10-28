package cli

import (
	"bufio"
	"strings"
	"testing"
)

func TestFirstLine(t *testing.T) {
	t.Parallel()

	const overCap = bufio.MaxScanTokenSize

	tests := []struct {
		name    string
		stdin   string
		wantKey string
		wantErr string
	}{
		{
			name:    "should return the longest single line the scanner can hand back",
			stdin:   strings.Repeat("A", overCap-1),
			wantKey: strings.Repeat("A", overCap-1),
		},
		{
			name:    "should report the read failure for one line over the cap",
			stdin:   strings.Repeat("A", overCap),
			wantErr: "reading the key from stdin: bufio.Scanner: token too long",
		},
		{
			name:    "should report the read failure when blank lines follow the long one",
			stdin:   strings.Repeat("A", overCap) + "\n\n   \n",
			wantErr: "reading the key from stdin: bufio.Scanner: token too long",
		},
		{
			name:  "should name the command when stdin carries a second line",
			stdin: "SECRET-STDIN\nlogin: someone\n",
			wantErr: "profile create: stdin carries more than one line, and it must hold the key alone. " +
				"Pipe a password manager entry through head -n 1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := firstLine(strings.NewReader(tc.stdin), "profile create")

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				if key != "" {
					t.Errorf("key length = %d, want a rejected read to hand back nothing", len(key))
				}

				return
			}

			if err != nil {
				t.Fatalf("firstLine returned an error: %v", err)
			}

			// Lengths rather than the keys, since a failure message carrying 64KiB helps nobody.
			if key != tc.wantKey {
				t.Errorf("key length = %d, want %d", len(key), len(tc.wantKey))
			}
		})
	}
}
