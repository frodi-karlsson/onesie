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
	modeWarning = "onesie: warning: could not set mode 600 on /home/u/.config/onesie/profiles.json. The key is not " +
		"protected by the filesystem"
	removedLink = "onesie: removed the link /home/u/.config/onesie/credentials.json, but " +
		"/home/u/secrets/credentials.json still holds the key"
	goodReport = "profile: work\nprovider: openrouter\nchosen by: the default\nkey: file, ends in cdef\n" +
		"model: jev-latest, the provider's default\nbase_url: https://openrouter.ai/api, the provider's default\n"
	goodVerdict = "onesie is on PATH and a key resolves, profile: work, provider: openrouter, key: file.\n"
	badTTL      = "onesie: ONESIE_CACHE_TTL=abc is not a duration of zero or more, such as 90m or 72h"
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
				"model: typesafe/jev, profile\nbase_url: https://openrouter.ai/api, the provider's default\n",
			want: "onesie is on PATH and a key resolves, profile: work, provider: openrouter, key: keychain.\n",
		},
		{
			name: "should say every run needs --allow-http when show reports it",
			show: "profile: work\nprovider: typesafe\nchosen by: the default\nkey: file\n" +
				"model: jev-latest, the provider's default\nbase_url: http://proxy.example, profile\n" +
				"allow_http: needed, since base_url is plain http to another host\n",
			want: "onesie is on PATH and a key resolves, profile: work, provider: typesafe, key: file.\n" +
				"Its base URL is plain http to another host, so every run exits 2 unless it passes --allow-http.\n",
		},
		{
			name: "should report a key from the environment for a run without a profile",
			show: "profile: none\nprovider: typesafe\nchosen by: nothing\nkey: env TYPESAFE_API_KEY\n" +
				"model: jev-latest, the provider's default\nbase_url: https://api.typesafe.ai, the provider's default\n",
			want: "onesie is on PATH and a key resolves, profile: none, provider: typesafe, key: env TYPESAFE_API_KEY.\n",
		},
		{
			name: "should name the key variable first under ONESIE_PROVIDER, which no stored profile serves",
			show: "profile: none\nprovider: openrouter\nchosen by: ONESIE_PROVIDER\nkey: none\n" +
				"model: jev-latest, the provider's default\nbase_url: https://openrouter.ai/api, the provider's default\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider openrouter. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user sets OPENROUTER_API_KEY. " +
				"ONESIE_PROVIDER is set, so a stored profile is not used. To use one instead, the user unsets " +
				"ONESIE_PROVIDER and runs onesie profile create NAME --provider openrouter. " +
				"The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name the key variable of berget when no key resolves",
			show: "profile: none\nprovider: berget\nchosen by: nothing\nkey: none\n" +
				"model: systemone, the provider's default\nbase_url: https://api.berget.ai, the provider's default\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider berget. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user runs onesie profile create NAME --provider berget or " +
				"sets BERGET_API_KEY. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name the key variable of levanto when no key resolves",
			show: "profile: none\nprovider: levanto\nchosen by: nothing\nkey: none\n" +
				"model: sage-latest, the provider's default\nbase_url: https://sage.levanto.ai, the provider's default\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider levanto. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user runs onesie profile create NAME --provider levanto or " +
				"sets LEVANTO_API_KEY. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name the key variable of cloudflare when no key resolves",
			show: "profile: none\nprovider: cloudflare\nchosen by: nothing\nkey: none\n" +
				"model: typesafe/jev, the provider's default\nbase_url: none\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider cloudflare. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user runs onesie profile create NAME --provider cloudflare or " +
				"sets CLOUDFLARE_API_TOKEN. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name TYPESAFE_API_KEY through the default arm when no key resolves on typesafe",
			show: "profile: none\nprovider: typesafe\nchosen by: nothing\nkey: none\n" +
				"model: jev-latest, the provider's default\nbase_url: https://api.typesafe.ai, the provider's default\n",
			code: 2,
			want: "onesie is on PATH, but no API key resolves for provider typesafe. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user runs onesie profile create NAME --provider typesafe or " +
				"sets TYPESAFE_API_KEY. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name profile update when a profile has no account",
			show: "profile: cf\nprovider: cloudflare\nchosen by: ONESIE_PROFILE\nkey: file, ends in cdef\n" +
				"model: typesafe/jev, the provider's default\nbase_url: none\naccount: none\n",
			code: 2,
			want: "onesie is on PATH and a key resolves for profile cf, but no account does. Every request fails " +
				"until the user runs onesie profile update cf --account-id ACCOUNT_ID or sets " +
				"CLOUDFLARE_ACCOUNT_ID. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should name the account variable and profile create when a run without a profile has no account",
			show: "profile: none\nprovider: cloudflare\nchosen by: ONESIE_PROVIDER\nkey: env CLOUDFLARE_API_TOKEN\n" +
				"model: typesafe/jev, the provider's default\nbase_url: none\naccount: none\n",
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
			name: "should pass on a mode warning and still report the key",
			show: modeWarning + "\n" + goodReport,
			want: modeWarning + "\n" + goodVerdict,
		},
		{
			name: "should pass on a variable every run refuses and still report the key",
			show: goodReport + badTTL + "\n",
			code: 2,
			want: badTTL + "\n" + goodVerdict,
		},
		{
			name: "should pass on a variable every run refuses and still report a missing key after its hint",
			show: "profile: none\nprovider: typesafe\nchosen by: nothing\nkey: none\n" +
				"model: jev-latest, the provider's default\nbase_url: https://api.typesafe.ai, the provider's default\n" +
				"onesie: no API key. Set TYPESAFE_API_KEY, or store one with onesie profile create NAME --provider typesafe\n" +
				badTTL + "\n",
			code: 2,
			want: badTTL + "\nonesie is on PATH, but no API key resolves for provider typesafe. Only --print-request, " +
				"--print-questions, --print-schema and --mock work until the user runs onesie profile create NAME " +
				"--provider typesafe or sets TYPESAFE_API_KEY. The onesie-setup skill walks them through it.\n",
		},
		{
			name: "should pass on a removed link and still report the key",
			show: removedLink + "\n" + goodReport,
			want: removedLink + "\n" + goodVerdict,
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
