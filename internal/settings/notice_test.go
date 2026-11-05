package settings_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestOrigin_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		origin settings.Origin
		want   string
	}{
		{
			name:   "should spell a flag as given",
			origin: settings.Origin{Source: settings.SourceFlag, Spelled: "--model"},
			want:   "--model",
		},
		{
			name:   "should spell a variable by its name",
			origin: settings.Origin{Source: settings.SourceEnv, Spelled: "ONESIE_BASE_URL"},
			want:   "ONESIE_BASE_URL",
		},
		{
			name:   "should name a question file's key",
			origin: settings.Origin{Source: settings.SourceFile, Spelled: "threshold"},
			want:   "the question file's threshold",
		},
		{
			name:   "should name a request body's key",
			origin: settings.Origin{Source: settings.SourceFile, Spelled: "model", Body: true},
			want:   "the request body's model",
		},
		{
			name:   "should name a chosen profile's member",
			origin: settings.Origin{Source: settings.SourceChosenProfile, Spelled: "model"},
			want:   "the profile's model",
		},
		{
			name:   "should name the default profile's rank",
			origin: settings.Origin{Source: settings.SourceDefaultProfile, Spelled: "account_id"},
			want:   "the default profile's account_id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.origin.String(); got != tc.want {
				t.Errorf("String = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNotice_String(t *testing.T) {
	t.Parallel()

	flag := func(spelled string) settings.Origin {
		return settings.Origin{Source: settings.SourceFlag, Spelled: spelled}
	}

	tests := []struct {
		name   string
		notice settings.Notice
		want   string
	}{
		{
			name: "should say the winner overrides the loser",
			notice: settings.Notice{
				Setting: "model", Winner: flag("--model"),
				Loser: settings.Origin{Source: settings.SourceChosenProfile, Spelled: "model"},
			},
			want: "--model overrides the profile's model",
		},
		{
			name: "should spell a flag by its long name",
			notice: settings.Notice{
				Setting: "model", Winner: flag("--model"),
				Loser: settings.Origin{Source: settings.SourceFile, Spelled: "model", Body: true},
			},
			want: "--model overrides the request body's model",
		},
		{
			name: "should name a variable",
			notice: settings.Notice{
				Setting: "endpoint", Winner: settings.Origin{Source: settings.SourceEnv, Spelled: "ONESIE_BASE_URL"},
				Loser: settings.Origin{Source: settings.SourceDefaultProfile, Spelled: "account_id"},
			},
			want: "ONESIE_BASE_URL overrides the default profile's account_id",
		},
		{
			name: "should name the default profile's rank",
			notice: settings.Notice{
				Setting: "model", Winner: settings.Origin{Source: settings.SourceEnv, Spelled: "TYPESAFE_DEFAULT_MODEL"},
				Loser: settings.Origin{Source: settings.SourceDefaultProfile, Spelled: "model"},
			},
			want: "TYPESAFE_DEFAULT_MODEL overrides the default profile's model",
		},
		{
			name: "should end a question's override with its id",
			notice: settings.Notice{
				Setting: "threshold", Winner: flag("--threshold"), Question: "urgent",
				Loser: settings.Origin{Source: settings.SourceFile, Spelled: "threshold"},
			},
			want: "--threshold overrides the question file's threshold for 'urgent'",
		},
		{
			name: "should say sets where the file question had no value",
			notice: settings.Notice{
				Setting: "threshold", Winner: flag("--threshold"), Question: "urgent", Verb: settings.VerbSets,
				Loser: settings.Origin{Source: settings.SourceFile, Spelled: "threshold"},
			},
			want: "--threshold sets the question file's threshold for 'urgent'",
		},
		{
			name: "should never print a value",
			notice: settings.Notice{
				Setting: "api_key", Winner: settings.Origin{Source: settings.SourceEnv, Spelled: "TYPESAFE_API_KEY"},
				Loser:  settings.Origin{Source: settings.SourceDefaultProfile, Spelled: "api_key"},
				Values: [2]string{"secret", "other"},
			},
			want: "TYPESAFE_API_KEY overrides the default profile's api_key",
		},
		{
			name: "should print a profile's name beside the flag that chose it",
			notice: settings.Notice{
				Setting: "profile", Winner: flag("--profile"), Values: [2]string{"work"},
				Loser: settings.Origin{Source: settings.SourceEnv, Spelled: "ONESIE_PROVIDER"},
			},
			want: "--profile work overrides ONESIE_PROVIDER",
		},
		{
			name: "should say a provider choice runs without the default profile",
			notice: settings.Notice{
				Setting: "provider", Winner: flag("--provider"), Verb: settings.VerbRunsWithout,
				Loser: settings.Origin{Source: settings.SourceDefaultProfile}, Values: [2]string{"openrouter", "work"},
			},
			want: "--provider openrouter runs without the default profile 'work'",
		},
		{
			name: "should join a variable that chose a provider to its name",
			notice: settings.Notice{
				Setting: "provider", Winner: settings.Origin{Source: settings.SourceEnv, Spelled: "ONESIE_PROVIDER"},
				Verb: settings.VerbRunsWithout, Loser: settings.Origin{Source: settings.SourceDefaultProfile},
				Values: [2]string{"openrouter", "work"},
			},
			want: "ONESIE_PROVIDER=openrouter runs without the default profile 'work'",
		},
		{
			name: "should print both values of a setting given twice, spelled as typed",
			notice: settings.Notice{
				Setting: "model", Winner: flag("-m"), Loser: flag("-m"), Verb: settings.VerbGivenTwice,
				Values: [2]string{"jev-preview", "jev-1.13.0"},
			},
			want: "-m is given twice, jev-preview overrides jev-1.13.0",
		},
		{
			name: "should name the question of a setting given twice",
			notice: settings.Notice{
				Setting: "fallback", Winner: flag("--fallback"), Loser: flag("--fallback"), Question: "tone",
				Verb: settings.VerbGivenTwice, Values: [2]string{"x", "human"},
			},
			want: "--fallback is given twice for 'tone', x overrides human",
		},
		{
			name: "should say the winner keeps the loser",
			notice: settings.Notice{
				Setting: "model", Winner: flag("--provider"), Verb: settings.VerbKeeps,
				Loser: settings.Origin{Source: settings.SourceChosenProfile, Spelled: "model"},
			},
			want: "--provider keeps the profile's model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.notice.String(); got != tc.want {
				t.Errorf("String = %q, want %q", got, tc.want)
			}
		})
	}
}
