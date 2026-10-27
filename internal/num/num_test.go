package num_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/num"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
	}{
		{name: "should write two places as two places", value: 0.7},
		{name: "should write three places as three places", value: 0.977},
		{name: "should keep every place a raw float carries", value: 0.41015625},
		{name: "should write zero and one bare", value: 1},
		{name: "should write a tiny number as encoding/json does", value: 0.0000001},
		{name: "should write a small number in plain form as encoding/json does", value: 0.00005},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}

			if got := num.Format(tc.value); got != string(want) {
				t.Errorf("Format(%v) = %q, want %q", tc.value, got, want)
			}
		})
	}
}

func TestRounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		want  float64
	}{
		{name: "should round to four places", value: 0.123456, want: 0.1235},
		{name: "should drop float noise", value: 0.1 + 0.2, want: 0.3},
		{name: "should keep a number with fewer places", value: 0.5, want: 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := num.Rounded(tc.value); got != tc.want {
				t.Errorf("Rounded(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
