package settingsgen

import (
	"strings"
	"testing"
)

func TestTable_Row(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cells   []string
		want    string
		wantErr string
	}{
		{name: "should join the cells with pipes", cells: []string{"`a`", "b"}, want: "| `a` | b |\n"},
		{name: "should escape a pipe in a cell", cells: []string{"a|b", "c"}, want: "| a\\|b | c |\n"},
		{name: "should refuse a newline in a cell", cells: []string{"a", "b\nc"}, wantErr: "newline"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			table := newTable("A", "B")
			head := table.text.String()
			table.row(tc.cells...)

			got, err := table.String()

			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("String error = %v, want one holding %q", err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("String: %v", err)
			case strings.TrimPrefix(got, head) != tc.want:
				t.Errorf("row = %q, want %q", strings.TrimPrefix(got, head), tc.want)
			}
		})
	}
}
