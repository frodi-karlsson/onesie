package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/output"
)

const (
	flagAccountID  = "account-id"
	flagKey        = "key"
	flagToFile     = "file"
	keyPlaceholder = "placeholder"
	noProfile      = "none"
	notActive      = "not active"
	providersOwn   = "the provider's default"
	fromProfile    = "profile"
)

func newProfileCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Create, inspect and remove the named connections a run uses",
		Long: "A profile is a named connection: a provider, its key in the OS keychain or in profiles.json, " +
			"and optionally a model and a base URL or a Cloudflare account id. A run uses the profile " +
			"--profile or ONESIE_PROFILE names, or the default one, which profile use sets. --provider or " +
			"ONESIE_PROVIDER runs without a profile, with the key from the provider's variable.\n\n" +
			"profiles.json lives in the config dir: $ONESIE_CONFIG_DIR, then $XDG_CONFIG_HOME/onesie, then " +
			"%APPDATA%\\onesie on Windows, then ~/.config/onesie. A credentials.json there is converted into " +
			"it the first time onesie reads it.",
		Args: noArguments,
		// Cobra validates the arguments only of a runnable command. A parent with no RunE answers
		// onesie profile nonsense with its own help and exit 0.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return refuseMock(settings, flags)
		},
	}

	cmd.AddCommand(
		newProfileCreateCmd(settings, flags),
		newProfileUpdateCmd(settings, flags),
		newProfileDeleteCmd(settings, flags),
		newProfileUseCmd(settings, flags),
		newProfileListCmd(settings, flags),
		newProfileShowCmd(settings, flags),
		newProfileTestCmd(settings, flags),
	)

	return cmd
}

func refuseMock(settings rootSettings, flags *runFlags) error {
	_, spelled := mockSource(settings.lookupEnv, flags.mock)
	if spelled == "" {
		return nil
	}

	advice := "Unset it"
	if spelled == flagMock {
		advice = "Drop it"
	}

	return fmt.Errorf("profile reads and writes real configuration, so it does not run under %s. %s",
		spelled, advice)
}

func newProfileCreateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	fields := &profileFlags{}

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Store a new profile, reading its key from a prompt or stdin",
		Long: "Store a new profile, and read its key from a hidden prompt on a terminal, or from stdin " +
			"otherwise. The key is never a flag, since a flag lands in shell history.\n\n" +
			"Stdin must hold the key alone. pass show and its siblings print metadata after the secret, so " +
			"pipe them through head -n 1.\n\n" +
			"The key goes to the OS keychain, or to profiles.json with --file. A name is 1 to 64 lowercase " +
			"letters, digits, - or _, does not start with -, and is not none. The first profile becomes the default.",
		Example: "  pass show openrouter | head -n 1 | onesie profile create work --provider openrouter\n" +
			"  onesie profile create cf --provider cloudflare --account-id 0123456789abcdef0123456789abcdef",
		Args: profileNames("create", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileCreate(cmd, settings, flags, args[0], *fields)
		},
	}

	cmd.Flags().StringVar(&fields.provider, flagProvider, "",
		"the profile's provider, typesafe, openrouter, berget, levanto or cloudflare. Required")
	addMemberFlags(cmd, fields, flags)
	refuseEmpty(cmd.Flags(), flagModel)
	refuseEmpty(cmd.Flags(), flagBaseURL)
	refuseEmpty(cmd.Flags(), flagAccountID)
	cmd.Flags().BoolVar(&fields.toFile, flagToFile, false, "store the key in profiles.json, not the OS keychain")

	return cmd
}

func profileCreate(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, name string, fields profileFlags,
) error {
	if refused := refuseChosen(flags, "create", false); refused != nil {
		return refused
	}

	if invalid := creds.ValidName(name); invalid != nil {
		return invalid
	}

	provider := strings.TrimSpace(fields.provider)
	if provider == "" {
		return errors.New(
			"profile create needs --provider: typesafe, openrouter, berget, levanto or cloudflare")
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	if _, taken := opened.Profiles.Profiles[name]; taken {
		return fmt.Errorf("profile %s exists. Change it with onesie profile update %s, "+
			"or delete it and create it again", quotedName(name), quotedName(name))
	}

	profile := creds.Profile{
		Provider: provider, Model: strings.TrimSpace(fields.model), BaseURL: strings.TrimSpace(fields.baseURL),
		AccountID: strings.TrimSpace(fields.accountID),
	}

	// Ahead of the read, so a typo costs the user nothing and leaves no file behind.
	if refused := checkGiven(profile, cmd.Flags().Changed, true); refused != nil {
		return refused
	}

	if refused := refusePlainBaseURL(flags, profile.BaseURL); refused != nil {
		return refused
	}

	if refused := checkProfile(opened.Profiles, name, profile); refused != nil {
		return refused
	}

	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Create(creds.Change{
		Path: path, Profiles: opened.Profiles, Name: name, Profile: profile, ToFile: fields.toFile,
		ReadKey: func() (string, error) { return readKey(cmd, settings, "profile create") },
	})

	return reported(cmd, notices, err)
}

func newProfileUpdateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	fields := &profileFlags{}

	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change a profile's members, or its key with --key",
		Long: "Change only the members named. An empty value clears one, as in --model \"\".\n\n" +
			"--key reads a new key as create does, into the OS keychain, or into profiles.json with --file. " +
			"--provider needs --key, since a key belongs to one provider, and drops account_id off " +
			"cloudflare. There is no rename. Delete the profile and create it again.",
		Example: "  onesie profile update work --model typesafe/jev-1.13\n" +
			"  pass show work | head -n 1 | onesie profile update work --key",
		Args: profileNames("update", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileUpdate(cmd, settings, flags, args[0], *fields)
		},
	}

	cmd.Flags().StringVar(&fields.provider, flagProvider, "", "a new provider for the profile, which needs --key")
	refuseEmpty(cmd.Flags(), flagProvider)
	addMemberFlags(cmd, fields, flags)
	cmd.Flags().BoolVar(&fields.key, flagKey, false, "read a new key from a prompt or stdin")
	cmd.Flags().BoolVar(&fields.toFile, flagToFile, false,
		"with --key, store the new key in profiles.json, not the OS keychain")

	return cmd
}

func profileUpdate(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, name string, fields profileFlags,
) error {
	if refused := refuseChosen(flags, "update", false); refused != nil {
		return refused
	}

	changed := cmd.Flags().Changed

	if refused := refuseUpdate(fields, changed); refused != nil {
		return refused
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	old, found := opened.Profiles.Profiles[name]
	if !found {
		return noProfileNamed(name, opened.Profiles)
	}

	profile := updated(old, fields, changed)
	if profile == old && !fields.key {
		if already := alreadySet(old, changed); already != "" {
			_, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "onesie: profile %s is unchanged, since %s\n",
				quotedName(name), already)

			return printErr
		}

		return fmt.Errorf(
			"profile update %s changes nothing. Pass --model, --base-url, --account-id or --key",
			quotedName(name))
	}

	// Ahead of the read, so a broken rule costs the user nothing.
	if refused := checkGiven(profile, changed, changed(flagProvider)); refused != nil {
		return refused
	}

	if changed(flagBaseURL) {
		if refused := refusePlainBaseURL(flags, profile.BaseURL); refused != nil {
			return refused
		}
	}

	if refused := checkProfile(opened.Profiles, name, profile); refused != nil {
		return refused
	}

	change := creds.Change{Path: path, Profiles: opened.Profiles, Name: name, Profile: profile, ToFile: fields.toFile}
	if fields.key {
		change.ReadKey = func() (string, error) { return readKey(cmd, settings, "profile update") }
	}

	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Update(change)

	return reported(cmd, notices, err)
}

func refuseUpdate(fields profileFlags, changed func(string) bool) error {
	switch {
	case fields.toFile && !fields.key:
		return errors.New("profile update: --file applies to a new key, so it needs --key")
	case changed(flagProvider) && !fields.key:
		return errors.New("profile update: --provider needs --key, since a key belongs to one provider")
	}

	return nil
}

func updated(old creds.Profile, fields profileFlags, changed func(string) bool) creds.Profile {
	profile := old

	if changed(flagProvider) {
		profile.Provider = strings.TrimSpace(fields.provider)

		if provider, err := onesie.ProviderNamed(profile.Provider); err == nil && provider.EnvAccountID == "" {
			profile.AccountID = ""
		}
	}

	if changed(flagModel) {
		profile.Model = strings.TrimSpace(fields.model)
	}

	if changed(flagBaseURL) {
		profile.BaseURL = strings.TrimSpace(fields.baseURL)
	}

	if changed(flagAccountID) {
		profile.AccountID = strings.TrimSpace(fields.accountID)
	}

	return profile
}

func newProfileDeleteCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Remove a profile and its keychain item",
		Long: "Remove a profile from profiles.json, then its keychain item, unless another profile names the " +
			"same item. Deleting the default leaves no default, and deleting the last profile removes the file.",
		Args: profileNames("delete", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileDelete(cmd, settings, flags, args[0])
		},
	}
}

func profileDelete(cmd *cobra.Command, settings rootSettings, flags *runFlags, name string) error {
	if refused := refuseChosen(flags, "delete", true); refused != nil {
		return refused
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	if _, found := opened.Profiles.Profiles[name]; !found {
		return noProfileNamed(name, opened.Profiles)
	}

	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Delete(path, opened.Profiles, name)

	return reported(cmd, notices, err)
}

func newProfileUseCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Make a profile the default a run uses",
		Long: "Make a profile the default, which a run uses when neither --profile, --provider, " +
			"ONESIE_PROFILE nor ONESIE_PROVIDER chooses.",
		Args: profileNames("use", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileUse(cmd, settings, flags, args[0])
		},
	}
}

func profileUse(cmd *cobra.Command, settings rootSettings, flags *runFlags, name string) error {
	if refused := refuseChosen(flags, "use", true); refused != nil {
		return refused
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	profile, found := opened.Profiles.Profiles[name]
	if !found {
		return noProfileNamed(name, opened.Profiles)
	}

	profiles := opened.Profiles.With(name, profile)
	profiles.Default = name

	return saveProfiles(cmd, settings, path, profiles)
}

func newProfileListCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the profiles, marking the one a run would use",
		Long: "List every profile with its provider, where its key lives and its model, and mark with * the " +
			"one a run would use now. ONESIE_PROFILE and ONESIE_PROVIDER count. When a run would use no " +
			"profile, a stderr line says why.",
		Args: profileNames("list", 0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return profileList(cmd, settings, flags)
		},
	}
}

func profileList(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	if refused := refuseChosen(flags, "list", true); refused != nil {
		return refused
	}

	opened, err := settings.holder.opened()
	if err != nil {
		return err
	}

	if len(opened.Profiles.Profiles) == 0 {
		return notify(cmd, "onesie: no profiles. Run onesie profile create NAME --provider PROVIDER to store one")
	}

	active, err := settings.holder.resolved()

	if writeErr := writeProfiles(cmd.OutOrStdout(), opened.Profiles, active.profile); writeErr != nil {
		return writeErr
	}

	return notify(cmd, inactiveReason(active, err))
}

func writeProfiles(w io.Writer, profiles creds.Profiles, active string) error {
	rows := [][]string{{"NAME", "PROVIDER", "KEY", "MODEL"}}

	for _, name := range slices.Sorted(maps.Keys(profiles.Profiles)) {
		profile := profiles.Profiles[name]

		key := sourceFile
		if profile.Store == creds.StoreKeychain {
			key = sourceKeychain
		}

		rows = append(rows, []string{name, profile.Provider, key, output.Printable(profile.Model)})
	}

	widths := make([]int, len(rows[0]))

	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	for i, row := range rows {
		var line strings.Builder

		if i > 0 && row[0] == active {
			line.WriteString("* ")
		} else {
			line.WriteString("  ")
		}

		for j, cell := range row {
			line.WriteString(cell)

			if j < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[j]-utf8.RuneCountInString(cell)+2))
			}
		}

		if _, err := fmt.Fprintln(w, strings.TrimRight(line.String(), " ")); err != nil {
			return err
		}
	}

	return nil
}

func inactiveReason(active resolution, err error) string {
	switch {
	case err != nil:
		return "active: none, since " + strings.TrimPrefix(err.Error(), "onesie: ")
	case active.profile == "":
		return "active: " + active.by + "=" + active.provider.Name + ", no profile"
	}

	return ""
}

func newProfileShowCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Report a profile, or what a run would use, never the key",
		Long: "With a name, report that profile. Without one, report what a run would use now, profile or " +
			"not, as --profile, --provider and their variables choose. It prints profile, provider, chosen " +
			"by, key, model and base_url lines, and an account line on cloudflare. The model and base_url " +
			"lines give the value a run would use and where it comes from: profile, an env variable such as " +
			"TYPESAFE_DEFAULT_MODEL, account_id, or the provider's default. An allow_http line says a run needs " +
			"--allow-http, when base_url is plain http to another host. It never reads the keychain or " +
			"prints a key.\n\n" +
			"It exits 2 when key or account is none, or the account is not 32 hexadecimal characters, " +
			"as a real run does.",
		Args: profileNames("show", 0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileShow(cmd, settings, flags, args)
		},
	}
}

func profileShow(cmd *cobra.Command, settings rootSettings, flags *runFlags, args []string) error {
	shown, err := shownResolution(settings, flags, args, "show")
	if err != nil {
		return err
	}

	// A hand edited profiles.json skips create's checks, and credentials in its base URL would print whole.
	if refused := refuseUnclearBaseURL(shown.baseURL); refused != nil {
		return refused
	}

	baseURL, found, invalid := effectiveBaseURL(shown, settings.lookupEnv)

	account := shown.accountSource(settings.lookupEnv)

	// A run refuses a malformed account, so show refuses it too. A missing account prints as none
	// and exits 2 below.
	if invalid != nil && account != sourceNone && account != "" {
		return invalid
	}

	// Again for what the variables chose, since TYPESAFE_BASE_URL skips create's checks too.
	if refused := refuseUnclearBaseURL(baseURL); found && refused != nil {
		return refused
	}

	baseURLLine := sourceNone
	if found {
		baseURLLine = output.Printable(baseURL) + ", " + baseURLOrigin(shown, settings.lookupEnv)
	}

	lines := []string{
		"profile: " + cmp.Or(shown.profile, noProfile),
		"provider: " + shown.provider.Name,
		"chosen by: " + shown.by,
		"key: " + shown.keyLine(),
		"model: " + output.Printable(shown.modelFor("", settings.lookupEnv)) + ", " +
			modelOrigin(shown, settings.lookupEnv),
		"base_url: " + baseURLLine,
	}

	if found && plainRemote(baseURL) {
		lines = append(lines, "allow_http: needed, since base_url is plain http to another host")
	}

	if account != "" {
		lines = append(lines, "account: "+account)
	}

	if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n")); printErr != nil {
		return printErr
	}

	if shown.keyFrom == sourceNone || account == sourceNone {
		return &silentError{code: ExitUsage}
	}

	return nil
}

func effectiveBaseURL(shown resolution, lookupEnv func(string) (string, bool)) (string, bool, error) {
	// Built the way a run builds its client, with a stand in for the key show never reads, so the
	// order the URL is chosen in is the run's own.
	opts := []onesie.Option{
		onesie.WithProvider(shown.provider), onesie.WithEnv(shown.env(lookupEnv)), onesie.WithAPIKey(keyPlaceholder),
	}

	if shown.baseURL != "" {
		opts = append(opts, onesie.WithBaseURL(shown.baseURL))
	}

	client, err := onesie.New(opts...)
	if err != nil {
		return "", false, err
	}

	return client.BaseURL(), true, nil
}

func baseURLOrigin(shown resolution, lookupEnv func(string) (string, bool)) string {
	switch {
	case shown.baseURL != "":
		return fromProfile
	case shown.provider.EnvBaseURL != "" && setVariable(lookupEnv, shown.provider.EnvBaseURL) != "":
		return sourceEnv + " " + shown.provider.EnvBaseURL
	case shown.accountID != "":
		return "account_id"
	case shown.provider.EnvAccountID != "" && setVariable(lookupEnv, shown.provider.EnvAccountID) != "":
		return sourceEnv + " " + shown.provider.EnvAccountID
	}

	return providersOwn
}

func modelOrigin(shown resolution, lookupEnv func(string) (string, bool)) string {
	switch {
	case strings.TrimSpace(shown.model) != "":
		return fromProfile
	case shown.provider.EnvDefaultModel != "" && setVariable(lookupEnv, shown.provider.EnvDefaultModel) != "":
		return sourceEnv + " " + shown.provider.EnvDefaultModel
	}

	return providersOwn
}

func newProfileTestCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [NAME]",
		Short: "Call the models endpoint with a profile's key",
		Long: "Call the provider's models endpoint with the key of the named profile, or of what a run would " +
			"use when given no name, which costs no tokens.\n\n" +
			"On openrouter profile test also reads /v1/key under the base URL, which costs nothing and refuses " +
			"a bad key, so a proxy set as the base URL must serve it too. Berget lists its models to any key, " +
			"so on berget profile test also asks one small question with the profile's model, which spends a " +
			"few tokens.",
		Args: profileNames("test", 0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileTest(cmd, settings, flags, args)
		},
	}

	cmd.Flags().BoolVar(&flags.allowHTTP, flagAllowHTTP, false, allowHTTPHelp)

	return cmd
}

func profileTest(cmd *cobra.Command, settings rootSettings, flags *runFlags, args []string) error {
	tested, err := shownResolution(settings, flags, args, "test")
	if err != nil {
		return err
	}

	if tested.keyFrom == sourceNone {
		return noKeyToTest(settings, tested)
	}

	client, err := settings.newClient(cmd.Context(), tested)
	if err != nil {
		return err
	}

	models, err := client.VerifyKey(cmd.Context())
	if err != nil {
		return keyHint(advise(err, "", false), tested)
	}

	_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\nprovider: %s\nkey: %s\nmodels: %d\n",
		cmp.Or(tested.profile, noProfile), tested.provider.Name, tested.keyLine(), len(models))

	return printErr
}

func noKeyToTest(settings rootSettings, tested resolution) error {
	missing := &onesie.ValidationError{Message: "no API key. Set " + tested.provider.EnvAPIKey}

	// A file that cannot be read names no profile, and the missing key is the failure to report.
	opened, err := settings.holder.opened()
	if err != nil {
		opened = creds.Opened{}
	}

	return missingKeyHint(missing, tested, opened.Profiles)
}

func shownResolution(settings rootSettings, flags *runFlags, args []string, command string) (resolution, error) {
	if len(args) == 1 {
		if refused := refuseChosen(flags, command, true); refused != nil {
			return resolution{}, refused
		}
	}

	opened, err := settings.holder.opened()
	if err != nil {
		return resolution{}, err
	}

	if len(args) == 0 {
		return settings.holder.resolved()
	}

	name := args[0]

	profile, found := opened.Profiles.Profiles[name]
	if !found {
		return resolution{}, noProfileNamed(name, opened.Profiles)
	}

	by := notActive
	if active, activeErr := settings.holder.resolved(); activeErr == nil &&
		active.profile == name {
		by = active.by
	}

	return profileRun(name, profile, by)
}

func addMemberFlags(cmd *cobra.Command, fields *profileFlags, flags *runFlags) {
	cmd.Flags().StringVar(&fields.model, flagModel, "", "the model a run uses when it names none")
	cmd.Flags().StringVar(&fields.baseURL, flagBaseURL, "", "the api root the profile's runs use")
	cmd.Flags().BoolVar(&flags.allowHTTP, flagAllowHTTP, false,
		"store a plain http --base-url to a host other than localhost, 127.0.0.0/8, also in IPv4-mapped "+
			"form, or ::1. Every run on the profile then needs --allow-http too")
	cmd.Flags().StringVar(&fields.accountID, flagAccountID, "",
		"cloudflare only, the account id, 32 hexadecimal characters")
}

func alreadySet(profile creds.Profile, changed func(string) bool) string {
	var unset, held []string

	for _, member := range []struct{ flag, name, value string }{
		{flag: flagModel, name: "model", value: profile.Model},
		{flag: flagBaseURL, name: "base_url", value: profile.BaseURL},
		{flag: flagAccountID, name: "account_id", value: profile.AccountID},
	} {
		switch {
		case !changed(member.flag):
		case member.value == "":
			unset = append(unset, "its "+member.name)
		default:
			held = append(held, fmt.Sprintf("its %s is already %s", member.name, output.Printable(member.value)))
		}
	}

	if len(unset) > 0 {
		verb := "is"
		if len(unset) > 1 {
			verb = "are"
		}

		held = append(held, fmt.Sprintf("%s %s already unset", strings.Join(unset, " and "), verb))
	}

	return strings.Join(held, ", and ")
}

func profileNames(command string, least, most int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch {
		case len(args) >= least && len(args) <= most:
			return nil
		case most == 0:
			return fmt.Errorf("profile %s takes no argument", command)
		case least == most:
			return fmt.Errorf("profile %s takes one profile name, as in onesie profile %s work",
				command, command)
		}

		return fmt.Errorf("profile %s takes at most one profile name", command)
	}
}

type profileFlags struct {
	provider  string
	model     string
	baseURL   string
	accountID string
	key       bool
	toFile    bool
}

func refuseChosen(flags *runFlags, command string, provider bool) error {
	if flags.profileSet {
		return fmt.Errorf("--profile does not apply to profile %s", command)
	}

	if provider && flags.providerSet {
		return fmt.Errorf("--provider does not apply to profile %s", command)
	}

	return nil
}

func profilesToWrite(settings rootSettings) (string, creds.Opened, error) {
	path, err := settings.profilesPath()
	if err != nil {
		return "", creds.Opened{}, err
	}

	opened, err := settings.holder.opened()
	if err != nil {
		return "", creds.Opened{}, err
	}

	return path, opened, nil
}

func noProfileNamed(name string, profiles creds.Profiles) error {
	return fmt.Errorf("there is no profile named %s. %s", quotedName(name), knownProfiles(profiles))
}

func checkGiven(profile creds.Profile, changed func(string) bool, providerGiven bool) error {
	provider, err := onesie.ProviderNamed(profile.Provider)
	if providerGiven && (err != nil || provider.Name != profile.Provider) {
		return fmt.Errorf("--%s names '%s', which is not typesafe, openrouter, berget, levanto or cloudflare",
			flagProvider, profile.Provider)
	}

	accountGiven := changed(flagAccountID) && profile.AccountID != ""
	baseURLGiven := changed(flagBaseURL) && profile.BaseURL != ""

	switch {
	case accountGiven && provider.EnvAccountID == "":
		return fmt.Errorf("--%s applies only to cloudflare, and this profile is on %s",
			flagAccountID, profile.Provider)
	case accountGiven && !creds.ValidAccountID(profile.AccountID):
		return fmt.Errorf("--%s must be 32 hexadecimal characters", flagAccountID)
	case accountGiven && baseURLGiven:
		return fmt.Errorf("--%s and --%s both name the account. Pass one", flagBaseURL, flagAccountID)
	case accountGiven && profile.BaseURL != "":
		return fmt.Errorf("--%s cannot join the profile's base_url, since each names the account. "+
			`Clear it with --%s "" in the same update`, flagAccountID, flagBaseURL)
	case baseURLGiven && profile.AccountID != "":
		return fmt.Errorf("--%s cannot join the profile's account_id, since each names the account. "+
			`Clear it with --%s "" in the same update`, flagBaseURL, flagAccountID)
	}

	return nil
}

func refusePlainBaseURL(flags *runFlags, baseURL string) error {
	if flags.allowHTTP || !plainRemote(baseURL) {
		return nil
	}

	// Ahead of the message, which quotes the URL.
	if refused := refuseUnclearBaseURL(baseURL); refused != nil {
		return refused
	}

	return errors.New("--" + flagBaseURL + " " + output.Printable(baseURL) + plainHTTPReason +
		". Use https, or pass --" + flagAllowHTTP + " to store it, and every run on the profile then needs --" +
		flagAllowHTTP + " too")
}

func checkProfile(profiles creds.Profiles, name string, profile creds.Profile) error {
	if profile.BaseURL != "" {
		if invalid := validBaseURL(profile.BaseURL); invalid != nil {
			return invalid
		}
	}

	// The key is read only once the rest passes, so a stand in takes its place for the rules.
	if profile.APIKey == "" && profile.Store == "" {
		profile.APIKey = keyPlaceholder
	}

	if broken := profiles.With(name, profile).Validate(); broken != nil {
		return broken
	}

	return nil
}

func saveProfiles(cmd *cobra.Command, settings rootSettings, path string, profiles creds.Profiles) error {
	warnings, err := settings.credStore.SaveProfilesIn(filepath.Dir(path), profiles)
	if err != nil {
		return err
	}

	return notify(cmd, warningLines(warnings)...)
}

func reported(cmd *cobra.Command, notices []creds.Notice, err error) error {
	lines := make([]string, 0, len(notices))
	for _, notice := range notices {
		lines = append(lines, noticeLine(notice))
	}

	if err == nil {
		return notify(cmd, lines...)
	}

	if errors.Is(err, creds.ErrKeychainTimeout) {
		err = fmt.Errorf("%w. Answer the keychain prompt, or run the command again with --file", err)
	}

	//nolint:errcheck // The error carries the exit code, and a stderr that cannot take the notices must not replace it.
	_ = notify(cmd, lines...)

	return err
}

func noticeLine(notice creds.Notice) string {
	switch notice.Kind {
	case creds.SaveWarning:
		return warningLine(notice.Err)
	case creds.KeyInKeychain:
		return "stored the key in the OS keychain as " + notice.Account
	case creds.KeyInFile:
		return "warning: " + notice.Err.Error() + ". The key is in " + notice.Path + " instead"
	case creds.FirstDefault:
		return "onesie: " + quotedName(notice.Name) + " is the default profile, since it is the first"
	case creds.DefaultDeleted:
		return "onesie: " + quotedName(notice.Name) + " was the default, so no profile is the default now. " +
			"Run onesie profile use NAME to choose one"
	case creds.ItemLeftBehind:
		return "warning: " + notice.Err.Error() + ". The keychain item " + notice.Account + " is left behind"
	case creds.OldKeyUnread:
		return "warning: the keychain item " + notice.Account + " now holds the new key, and its old key " +
			"could not be read to put back"
	case creds.OldKeyLost:
		return "warning: " + notice.Err.Error() + ". The keychain item " + notice.Account +
			" now holds the new key, and its old key could not be put back"
	}

	return ""
}

func warningLines(warnings []error) []string {
	lines := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		lines = append(lines, warningLine(warning))
	}

	return lines
}

func warningLine(warning error) string {
	var unprotected *creds.ModeWarning
	if errors.As(warning, &unprotected) {
		return warning.Error()
	}

	return "onesie: " + warning.Error()
}

func notify(cmd *cobra.Command, lines ...string) error {
	for _, line := range lines {
		if line == "" {
			continue
		}

		// stderr rather than stdout. These are notices, and a redirected stdout stays empty.
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), line); err != nil {
			return err
		}
	}

	return nil
}
