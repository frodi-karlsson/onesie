package argv_test

import (
	"testing"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
)

func TestRecorder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []argv.Event
	}{
		{
			name: "should record nothing when no flags are given",
			args: []string{},
			want: nil,
		},
		{
			name: "should preserve interleaving across different flags",
			args: []string{
				"--ask", "a=first", "--pick", "x,y", "--desc", "x=ex",
				"--ask", "b=second", "--rate", "p,q",
			},
			want: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "desc", Value: "x=ex"},
				{Name: "ask", Value: "b=second"},
				{Name: "rate", Value: "p,q"},
			},
		},
		{
			name: "should record the equals form identically",
			args: []string{"--ask=a=first", "--pick=x,y"},
			want: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
			},
		},
		{
			name: "should record repeats in order",
			args: []string{"--pick", "a,b", "--pick", "c,d"},
			want: []argv.Event{
				{Name: "pick", Value: "a,b"},
				{Name: "pick", Value: "c,d"},
			},
		},
		{
			name: "should accept a lone dash as a value",
			args: []string{"--sep", "-"},
			want: []argv.Event{
				{Name: "sep", Value: "-"},
			},
		},
		{
			name: "should keep a dash inside a description",
			args: []string{"--desc", "x=- a bullet"},
			want: []argv.Event{
				{Name: "desc", Value: "x=- a bullet"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := argv.New()

			set := pflag.NewFlagSet("onesie", pflag.ContinueOnError)
			for _, name := range []string{"ask", "pick", "rate", "desc", "sep"} {
				set.Var(recorder.Flag(name), name, "recorded")
			}

			if err := set.Parse(tc.args); err != nil {
				t.Fatalf("parsing %v: %v", tc.args, err)
			}

			got := recorder.Events()
			if len(got) != len(tc.want) {
				t.Fatalf("events = %v, want %v", got, tc.want)
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
