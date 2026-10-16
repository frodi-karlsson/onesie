package cli

import "testing"

func TestTakesNextAsValue(t *testing.T) {
	t.Parallel()

	flags := NewRootCmd(BuildInfo{Version: "1.2.3"}).Flags()

	tests := []struct {
		name string
		arg  string
		want bool
	}{
		{name: "should not take the next argument after --box", arg: "--box", want: false},
		{name: "should take the next argument after --ask", arg: "--ask", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := takesNextAsValue(flags, tc.arg); got != tc.want {
				t.Errorf("takesNextAsValue(%q) = %v, want %v", tc.arg, got, tc.want)
			}
		})
	}
}
