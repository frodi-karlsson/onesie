package hooks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

const (
	noDefault = "onesie is on PATH and has profiles, but none is the default and nothing chose one, so every " +
		"run exits 2 until the user runs onesie profile use NAME. onesie profile list names them.\n"
	providerNotice = "onesie: ONESIE_PROVIDER is set, so a run uses no profile and reads only the provider's key " +
		"variable. To use a stored key, unset it and run onesie profile use NAME, or pass --profile NAME"
	unwritten = "onesie: could not write /home/u/.config/onesie/profiles.json, so " +
		"/home/u/.config/onesie/credentials.json is converted for this run only: creating a temporary file in " +
		"/home/u/.config/onesie: permission denied"
)

func TestPreflight(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the hook is a POSIX sh script, and Windows has no sh to run it")
	}

	tests := []struct {
		name   string
		absent bool
		show   string
		code   int
		want   string
	}{
		{
			name: "should report the profile, the provider and where the key lives, without its ending",
			show: "profile: work\nprovider: openrouter\nchosen by: the default\nkey: keychain, ends in 3f9a\n" +
				"model: typesafe/jev\nbase_url: the provider's\n",
			want: "onesie is on PATH and a key resolves, profile: work, provider: openrouter, key: keychain.\n",
		},
		{
			name: "should report a key from the environment for a run without a profile",
			show: "profile: none\nprovider: typesafe\nchosen by: nothing\nkey: env TYPESAFE_API_KEY\n" +
				"model: the provider's\nbase_url: the provider's\n",
			want: "onesie is on PATH and a key resolves, profile: none, provider: typesafe, key: env TYPESAFE_API_KEY.\n",
		},
		{
			name: "should name profile create and the key variable when no key resolves",
			show: "profile: none\nprovider: openrouter\nchosen by: ONESIE_PROVIDER\nkey: none\n" +
				"model: the provider's\nbase_url: the provider's\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider openrouter. Only --print-request and " +
				"--print-questions work until the user runs onesie profile create NAME --provider openrouter or " +
				"sets OPENROUTER_API_KEY. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name profile update when a profile has no account",
			show: "profile: cf\nprovider: cloudflare\nchosen by: ONESIE_PROFILE\nkey: file, ends in cdef\n" +
				"model: the provider's\nbase_url: the provider's\naccount: none\n",
			code: 2,
			want: "onesie is on PATH and a key resolves for profile cf, but no account does. Every request fails " +
				"until the user runs onesie profile update cf --account-id ACCOUNT_ID or sets " +
				"CLOUDFLARE_ACCOUNT_ID. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name the account variable and profile create when a run without a profile has no account",
			show: "profile: none\nprovider: cloudflare\nchosen by: ONESIE_PROVIDER\nkey: env CLOUDFLARE_API_TOKEN\n" +
				"model: the provider's\nbase_url: the provider's\naccount: none\n",
			code: 2,
			want: "onesie is on PATH and a key resolves for provider cloudflare, but no account does. Every " +
				"request fails until the user sets CLOUDFLARE_ACCOUNT_ID or stores a profile with onesie " +
				"profile create NAME --provider cloudflare --account-id ACCOUNT_ID. The onesie-setup skill " +
				"walks them through it.\n",
		},
		{
			name: "should name profile use when no profile is the default",
			show: "onesie: no profile is the default and nothing chose one. Run onesie profile use NAME, or pass " +
				"--profile NAME or --provider NAME\n",
			code: 2,
			want: noDefault,
		},
		{
			name: "should pass on a conversion and still name profile use",
			show: "onesie: converted credentials.json into profiles.json, with profile openrouter. No profile is " +
				"the default, so run onesie profile use NAME to choose one\n" +
				"onesie: no profile is the default and nothing chose one. Run onesie profile use NAME, or pass " +
				"--profile NAME or --provider NAME\n",
			code: 2,
			want: "onesie: converted credentials.json into profiles.json, with profile openrouter. No profile is " +
				"the default, so run onesie profile use NAME to choose one\n" + noDefault,
		},
		{
			name: "should pass on a conversion for this run and its warning, and still name profile use",
			show: "onesie: converted credentials.json for this run, with profile openrouter. No profile is the " +
				"default, so run onesie profile use NAME to choose one\n" + unwritten + "\n" +
				"onesie: no profile is the default and nothing chose one. Run onesie profile use NAME, or pass " +
				"--profile NAME or --provider NAME\n",
			code: 2,
			want: "onesie: converted credentials.json for this run, with profile openrouter. No profile is the " +
				"default, so run onesie profile use NAME to choose one\n" + unwritten + "\n" + noDefault,
		},
		{
			name: "should pass on the conversion lines and report the failure that follows them",
			show: "onesie: converted credentials.json into profiles.json, with profile typesafe. typesafe is the default\n" +
				providerNotice + "\n" +
				"onesie: unknown provider bogus. Use typesafe, openrouter, berget, levanto or cloudflare\n",
			code: 2,
			want: "onesie: converted credentials.json into profiles.json, with profile typesafe. typesafe is the default\n" +
				providerNotice + "\n" +
				"onesie is on PATH, but onesie profile show failed with: onesie: unknown provider bogus. Use " +
				"typesafe, openrouter, berget, levanto or cloudflare\n",
		},
		{
			name: "should report the first line of any other failure",
			show: "onesie: accepts at most 1 arg(s), received 2\n",
			code: 2,
			want: "onesie is on PATH, but onesie profile show failed with: onesie: accepts at most 1 arg(s), received 2\n",
		},
		{
			name: "should say whether a key resolves is unknown when profile show prints nothing",
			want: "onesie is on PATH, but onesie profile show gave no answer, so whether a key resolves is unknown.\n",
		},
		{
			name:   "should send the user to the setup skill when onesie is not on PATH",
			absent: true,
			want: "onesie is not on PATH, so the onesie skills cannot run any command yet. Load the onesie-setup " +
				"skill and walk the user through installing it.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bin := t.TempDir()
			if !tc.absent {
				writeFakeOnesie(t, bin, tc.show, tc.code)
			}

			if got := runPreflight(t, bin); got != tc.want {
				t.Errorf("hook output:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func writeFakeOnesie(t *testing.T, bin, show string, code int) {
	t.Helper()

	// Only profile show answers. --version and questions print nothing, so the hook adds no version
	// or question file line, and the output is the profile report alone.
	script := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = 'profile show' ]; then\n" +
		"cat <<'EOF'\n" + show + "EOF\n" +
		"exit " + strconv.Itoa(code) + "\n" +
		"fi\n"

	if err := os.WriteFile(filepath.Join(bin, "onesie"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runPreflight(t *testing.T, bin string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "/bin/sh", "preflight.sh")
	// The system directories hold sed, grep, awk and head, and never an onesie of their own.
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir()}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the hook exits 0 whatever happens, and it failed: %v\n%s", err, out)
	}

	return string(out)
}
