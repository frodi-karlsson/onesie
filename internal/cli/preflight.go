package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const (
	setupSkill   = "The onesie-setup skill walks the user through it."
	keylessModes = "Only --print-request, --print-questions, --print-schema and --mock work until the user "
	accountFails = "Every request fails until the user "
)

func newProfilePreflightCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use: "preflight", Hidden: true, Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProfilePreflight(cmd, settings, flags)
		},
	}
}

func runProfilePreflight(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	settings.holder = newHolderWithOpen(settings, flags, settings.holder.stderr, func() (creds.Opened, error) { return readProfiles(settings) })
	opened, openErr := settings.holder.opened()
	if openErr != nil {
		var invalid *creds.InvalidProfilesError
		var readable *creds.ReadableError
		if !errors.As(openErr, &invalid) && !errors.As(openErr, &readable) {
			return errors.New("automatic readiness is unavailable")
		}
	}
	inspected, err := inspectConnection(settings, flags, nil)
	facts := readinessFacts{}
	if err != nil {
		facts.problem = safeReadinessError(err)
	} else {
		shown := inspected.shown
		facts.provider, facts.profile = shown.provider.Name(), shown.profile
		facts.keySource, facts.keyVariable = shown.keyFrom, shown.provider.EnvAPIKey()
		facts.accountVariable = shown.provider.EnvAccountID()
		facts.providerEnv = shown.by == settingEnv(setting.Provider.Name())
		facts.missingAccount = inspected.account == sourceNone
		facts.custom = inspected.custom
		facts.remoteHTTP = inspected.remoteHTTP
		if endpoint, parseErr := url.Parse(inspected.baseURL); parseErr == nil && inspected.invalid == nil {
			facts.endpoint = endpoint.Scheme + "://" + endpoint.Host
		}
		facts.endpointEnv = shown.endpointFrom == settingEnv(setting.APIEndpoint.Name())
		if inspected.switchErr != nil {
			facts.problem = safeReadinessError(inspected.switchErr)
		}
	}
	if openErr == nil {
		facts.converted, facts.inMemory = len(opened.Converted) > 0, opened.InMemory

		for _, warning := range opened.Warnings {
			var mode *creds.ModeWarning
			if errors.As(warning, &mode) {
				facts.permissions = true
				continue
			}
			var retained *creds.RetainedCredentialsWarning
			if errors.As(warning, &retained) {
				facts.retained = true
				facts.linkRemoved = retained.LinkRemoved
				continue
			}
			facts.warning = true
		}
	}
	_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), renderReadiness(facts))
	return writeErr
}

func inspectConnection(settings rootSettings, flags *runFlags, args []string) (connectionInspection, error) {
	chosen, err := shownResolution(settings, flags, args, "show")
	if err != nil {
		return connectionInspection{}, err
	}
	shown, err := testedConnection(settings, chosen)
	if err != nil {
		return connectionInspection{}, err
	}
	baseURL, source, invalid := shown.provider.ResolveBaseURL(shown.baseURL, shown.accountID, noEnvironment)
	var refused *onesie.BaseURLError
	if errors.As(invalid, &refused) {
		return connectionInspection{}, baseURLRefusal(refused, baseURLFrom(source, shown), sendPlainHTTP)
	}
	if invalid != nil && !errors.Is(invalid, onesie.ErrNoAccountID) {
		return connectionInspection{}, invalid
	}
	return connectionInspection{
		shown: shown, baseURL: baseURL, source: source, invalid: invalid,
		account:    shown.accountSource(source, invalid),
		custom:     invalid == nil && shown.provider.CustomBaseURL(baseURL),
		remoteHTTP: invalid == nil && onesie.InsecureBaseURL(baseURL),
		switchErr:  checkEnvSettings(settings.lookupEnv, "cache", "long_context", "cache_ttl"),
	}, nil
}

type connectionInspection struct {
	account            string
	custom, remoteHTTP bool
	switchErr          error
	shown              resolution
	baseURL            string
	source             onesie.BaseURLSource
	invalid            error
}

func renderReadiness(f readinessFacts) string {
	lines := migrationLines(f.migrationFacts)
	verdict, ready := readinessVerdict(f)
	lines = append(lines, verdict)
	if f.endpointEnv && f.endpoint != "" && f.keySource != sourceNone {
		lines = append(lines, "Its base URL comes from "+settingEnv(setting.APIEndpoint.Name())+", so the key goes to "+safeReadinessName(f.endpoint)+".")
	}
	if ready && f.providerEnv {
		provider := settingEnv(setting.Provider.Name())
		lines = append(lines, provider+" is set, so a stored profile is not used. Unset "+provider+" to use a stored profile.")
	}
	if f.endpoint != "" && f.custom && (!f.endpointEnv || f.keySource == sourceNone) {
		lines = append(lines, "Its base URL uses "+safeReadinessName(f.endpoint)+".")
	}
	if f.remoteHTTP {
		lines = append(lines, "Its base URL is plain http to another host, so every run exits 2 unless it passes --allow-http.")
	}
	return strings.Join(lines, "\n")
}

func migrationLines(m migrationFacts) []string {
	var lines []string
	if m.converted {
		where := "into profiles.json"
		if m.inMemory {
			where = "for this run only"
		}
		lines = append(lines, "onesie: converted credentials.json "+where+".")
	}
	if m.permissions {
		lines = append(lines, "onesie: warning: could not set mode 600 on profiles.json. The key is not protected by the filesystem. Run onesie profile show for manual inspection.")
	}
	if m.warning {
		lines = append(lines, "onesie: warning: configuration storage needs attention. Run onesie profile show to inspect file permissions and migration warnings.")
	}
	if m.retained {
		message := "Legacy credentials.json still holds the key. Remove it after verifying profiles.json."
		if m.linkRemoved {
			message = "Removed the credentials.json link, but its target still holds the key. Remove the target after verifying profiles.json."
		}
		lines = append(lines, "onesie: warning: "+message)
	}
	return lines
}

func readinessVerdict(f readinessFacts) (string, bool) {
	missingKey := f.keySource == sourceNone && !f.custom
	if f.problem != "" {
		return "onesie is on PATH, but configuration is not ready. " + f.problem + " " + setupSkill, false
	}
	if !missingKey && !f.missingAccount {
		key := "a key resolves"
		if f.keySource == sourceKeychain {
			key = "a keychain entry is configured, retrieval is not verified"
		}
		if f.keySource == sourceNone {
			key = "needs no key, since its base URL is custom"
		}
		return "onesie is on PATH and " + key + ", profile: " + safeReadinessName(f.profile) + ", provider: " + f.provider + ".", true
	}
	var gap string
	switch {
	case missingKey && f.missingAccount:
		gap = "onesie is on PATH, but no API key resolves for provider " + f.provider + ", and no account does either. "
	case missingKey:
		gap = "onesie is on PATH, but no API key resolves for provider " + f.provider + ". "
	case f.profile != "":
		gap = "onesie is on PATH and a key resolves for profile " + safeReadinessName(f.profile) + ", but no account does. "
	default:
		gap = "onesie is on PATH and a key resolves for provider " + f.provider + ", but no account does. "
	}
	until := accountFails
	if missingKey {
		until = keylessModes
	}
	return gap + until + setupAction(f, missingKey) + ". " + setupSkill, false
}

func setupAction(f readinessFacts, missingKey bool) string {
	if f.profile != "" {
		update := "runs onesie profile update " + safeReadinessName(f.profile)
		switch {
		case missingKey && f.missingAccount:
			return update + " --key --account-id ACCOUNT_ID"
		case missingKey:
			return update + " --key"
		default:
			return update + " --account-id ACCOUNT_ID or sets " + f.accountVariable
		}
	}
	create := "stores a profile with onesie profile create NAME --provider " + f.provider
	var action string
	switch {
	case missingKey && f.missingAccount:
		action = "sets " + f.keyVariable + " and " + f.accountVariable + ", or " + create + " --account-id ACCOUNT_ID"
	case missingKey:
		action = "sets " + f.keyVariable + " or " + create
	default:
		action = "sets " + f.accountVariable + " or " + create + " --account-id ACCOUNT_ID"
	}
	if f.providerEnv {
		action += ". " + settingEnv(setting.Provider.Name()) + " is set, so no stored profile is used"
	}
	return action
}

type readinessFacts struct {
	provider, profile, keySource, keyVariable, accountVariable, endpoint, problem string
	providerEnv                                                                   bool
	missingAccount                                                                bool
	custom                                                                        bool
	remoteHTTP                                                                    bool
	endpointEnv                                                                   bool
	migrationFacts
}

type migrationFacts struct {
	converted   bool
	inMemory    bool
	warning     bool
	permissions bool
	retained    bool
	linkRemoved bool
}

func safeReadinessError(err error) string {
	if errors.Is(err, errNoDefaultProfile) {
		return "No profile is the default. Run onesie profile use NAME. onesie profile list names them."
	}
	var readable *creds.ReadableError
	if errors.As(err, &readable) {
		return "Configuration is readable or writable by its group or others. Set mode 600 on the file named by onesie profile show."
	}
	var invalid *setting.EnvironmentError
	if errors.As(err, &invalid) {
		return "Unset or correct " + invalid.Name + ". Run onesie profile show for manual inspection."
	}
	return "Run onesie profile show for manual inspection, then correct the selected profile, provider or endpoint."
}

func safeReadinessName(name string) string {
	if name == "" {
		return sourceNone
	}
	quoted := strconv.Quote(name)
	return quoted[1 : len(quoted)-1]
}
