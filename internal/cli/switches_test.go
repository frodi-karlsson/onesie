package cli

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/cache"
)

func TestReadSwitches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		want    switches
		wantErr string
	}{
		{name: "should read no variable as every switch off"},
		{name: "should read empty variables as unset", env: map[string]string{envCache: "", envLongContext: "", envCacheTTL: ""}},
		{
			name: "should read what the flags take",
			env:  map[string]string{envCache: "true", envLongContext: "1", envCacheTTL: "90m"},
			want: switches{cache: true, longContext: true, aliasTTL: 90 * time.Minute},
		},
		{name: "should read ONESIE_CACHE_TTL=0 as no alias lifetime", env: map[string]string{envCacheTTL: "0"}, want: switches{aliasTTL: cache.NoAliasTTL}},
		{name: "should be on under --cache", args: []string{"--cache"}, want: switches{cache: true}},
		{name: "should let --cache=false win over ONESIE_CACHE=1", env: map[string]string{envCache: "1"}, args: []string{"--cache=false"}},
		{name: "should let --cache win over ONESIE_CACHE=0", env: map[string]string{envCache: "0"}, args: []string{"--cache"}, want: switches{cache: true}},
		{
			name: "should let --long-context=false win over ONESIE_LONG_CONTEXT=1",
			env:  map[string]string{envLongContext: "1"}, args: []string{"--long-context=false"},
		},
		{
			name:    "should refuse an ONESIE_LONG_CONTEXT the flag would not take",
			env:     map[string]string{envLongContext: "garbage"},
			wantErr: "ONESIE_LONG_CONTEXT=garbage is not a value --long-context takes. Use 1 or 0",
		},
		{
			name:    "should refuse an ONESIE_CACHE the flag would not take, even beside the flag",
			env:     map[string]string{envCache: "yes"},
			args:    []string{"--cache"},
			wantErr: "ONESIE_CACHE=yes is not a value --cache takes. Use 1 or 0",
		},
		{
			name:    "should refuse a negative ONESIE_CACHE_TTL",
			env:     map[string]string{envCacheTTL: "-1h"},
			wantErr: "ONESIE_CACHE_TTL=-1h is not a duration of zero or more, such as 90m or 72h",
		},
		{
			name:    "should refuse an ONESIE_CACHE_TTL that is no duration",
			env:     map[string]string{envCacheTTL: "soon"},
			wantErr: "ONESIE_CACHE_TTL=soon is not a duration of zero or more, such as 90m or 72h",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags := &runFlags{}
			cmd := &cobra.Command{Use: "onesie"}
			cmd.Flags().BoolVar(&flags.cache, flagCache, false, "")
			cmd.Flags().BoolVar(&flags.longContext, flagLongContext, false, "")

			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			got, err := readSwitches(cmd, lookupFrom(tc.env), *flags)

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr || Classify(err) != ExitUsage {
					t.Fatalf("readSwitches = %v, want the usage error %q", err, tc.wantErr)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("readSwitches = %+v, %v, want %+v", got, err, tc.want)
			}
		})
	}
}
