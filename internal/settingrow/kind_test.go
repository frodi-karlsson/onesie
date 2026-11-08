package settingrow_test

import (
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestEnumList(t *testing.T) {
	t.Parallel()

	kind := settingrow.EnumList([]string{"error", "fail", "unsure"})

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
		empty   bool
	}{
		{name: "should take one value", raw: "unsure", want: "unsure"},
		{name: "should hold several values in table order", raw: "unsure,error", want: "error,unsure"},
		{name: "should trim spaces around each value", raw: " error , fail ", want: "error,fail"},
		{name: "should fold a repeated value", raw: "error,error", want: "error"},
		{name: "should refuse an unknown value", raw: "error,warn", wantErr: "takes error, fail or unsure, comma separated, got 'warn'"},
		{name: "should refuse an empty item", raw: "error,", wantErr: "takes error, fail or unsure, comma separated, got ''"},
		{name: "should refuse an empty value", raw: " ", empty: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := kind.Parse(tc.raw)

			switch {
			case tc.empty:
				if !errors.Is(err, settingrow.ErrEmptyValue) {
					t.Fatalf("Parse(%q) error = %v, want ErrEmptyValue", tc.raw, err)
				}
			case tc.wantErr != "":
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Parse(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
				}
			case err != nil || got != tc.want:
				t.Fatalf("Parse(%q) = %v, %v, want %q", tc.raw, got, err, tc.want)
			}
		})
	}
}
