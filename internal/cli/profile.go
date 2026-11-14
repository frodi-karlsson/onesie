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

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/providers"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const (
	flagAccountID  = "account-id"
	flagKey        = "key"
	flagToFile     = "file"
	keyPlaceholder = "placeholder"
	noProfile      = "none"
	notActive      = "not active"
	providersOwn   = "the provider's default"
)

func newProfileCmd(settings rootSettings, flags *runFlags, choice []*pflag.Flag) *cobra.Command {
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
			return refuseMock(settings.lookupEnv)
		},
	}

	cmd.AddCommand(
		newProfileCreateCmd(settings, flags),
		newProfileUpdateCmd(settings, flags),
		newProfileDeleteCmd(settings),
		newProfileUseCmd(settings),
		newProfileListCmd(settings),
		shareChoice(newProfileShowCmd(settings, flags), choice),
		shareChoice(newProfilePreflightCmd(settings, flags), choice),
		shareChoice(newProfileTestCmd(settings, flags), choice),
	)

	return cmd
}

func refuseMock(lookupEnv func(string) (string, bool)) error {
	if value, found := lookupEnv(settingEnv(setting.Mock.Name())); !found || value == "" {
		return nil
	}

	return fmt.Errorf("profile reads and writes real configuration, so it does not run under %s. Unset it",
		settingEnv(setting.Mock.Name()))
}

func newProfileCreateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	fields := &profileFlags{}

	var given *setting.Given

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Store a new profile, reading its key from a prompt or stdin",
		Long: "Store a new profile, and read its key from a hidden prompt on a terminal, or from stdin " +
			"otherwise. The key is never a flag, since a flag lands in shell history.\n\n" +
			"Stdin must hold the key alone. pass show and its siblings print metadata after the secret, so " +
			"pipe them through head -n 1.\n\n" +
			"The key goes to the OS keychain, or to profiles.json with --file. An empty key stores a profile " +
			"with no key when --base-url is not the provider's own. A name is 1 to 64 lowercase " +
			"letters, digits, - or _, does not start with -, and is not none. The first profile becomes the default.",
		Example: "  pass show openrouter | head -n 1 | onesie profile create work --provider openrouter\n" +
			"  onesie profile create cf --provider cloudflare --account-id 0123456789abcdef0123456789abcdef",
		Args: profileNames("create", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileCreate(cmd, settings, flags, given, args[0], *fields)
		},
	}

	given = setting.Register(cmd.Flags(), settingrow.Table(), nil, settingrow.CommandProfileCreate,
		profileTargets(fields, flags))

	return cmd
}

func profileCreate(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, given *setting.Given, name string, fields profileFlags,
) error {
	if invalid := creds.ValidName(name); invalid != nil {
		return invalid
	}

	provider := strings.TrimSpace(fields.provider)
	if provider == "" {
		return errors.New("profile create needs --provider: " + providers.Names())
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	if _, taken := opened.Profiles.Profiles[name]; taken {
		return fmt.Errorf("profile %s exists. Change it with onesie profile update %s, "+
			"or delete it and create it again", creds.QuotedName(name), creds.QuotedName(name))
	}

	profile := applied(creds.Profile{}, profileTargets(&fields, flags), given, settingrow.CommandProfileCreate)

	// Ahead of the read, so a typo costs the user nothing and leaves no file behind.
	if refused := checkGiven(profile, given, true); refused != nil {
		return refused
	}

	if profile.AccountID != "" && profile.BaseURL != "" {
		return fmt.Errorf("--%s and --%s both name the account. Pass one", flagBaseURL, flagAccountID)
	}

	if refused := checkProfile(opened.Profiles, name, profile, "--"+flagBaseURL, true); refused != nil {
		return refused
	}

	if refused := refusePlainHTTP(flags, profile.BaseURL); refused != nil {
		return refused
	}

	change := creds.Change{
		Path: path, Profiles: opened.Profiles, Name: name, Profile: profile, ToFile: fields.toFile,
		ReadKey: func() (string, error) { return readKey(cmd, settings, "profile create", customProfile(profile)) },
	}
	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Create(change)

	return reported(cmd, settings.laterFailures, change, notices, err)
}

func newProfileUpdateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	fields := &profileFlags{}

	var given *setting.Given

	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change a profile's members, or its key with --key",
		Long: "Change only the members named. An empty value clears one, as in --model \"\". The result is " +
			"checked as a whole profile, so one update reaches any valid profile.\n\n" +
			"--key reads a new key as create does, into the OS keychain, or into profiles.json with --file. " +
			"--provider keeps base_url, account_id and model, and names on stderr the ones it keeps. On a " +
			"profile that holds a key it needs --key, since a key belongs to one provider. An account_id kept " +
			"on a provider that takes none is refused, so clear it with --account-id \"\" in the same update.\n\n" +
			"--name renames the profile and moves a keychain item named after it, and the default follows it. An empty key " +
			"typed at the prompt drops the stored key and its keychain item, when the base URL is not the " +
			"provider's own. An empty key on stdin never drops a key.",
		Example: "  onesie profile update work --model typesafe/jev-1.13\n" +
			"  pass show work | head -n 1 | onesie profile update work --key\n" +
			"  onesie profile update work --name personal",
		Args:              profileNames("update", 1, 1),
		ValidArgsFunction: completeProfileName(settings),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileUpdate(cmd, settings, flags, given, args[0], *fields)
		},
	}

	given = setting.Register(cmd.Flags(), settingrow.Table(), nil, settingrow.CommandProfileUpdate,
		profileTargets(fields, flags))

	return cmd
}

func profileUpdate(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, given *setting.Given, name string, fields profileFlags,
) error {
	if fields.toFile && !fields.key {
		return errors.New("profile update: --file applies to a new key, so it needs --key")
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	old, found := opened.Profiles.Profiles[name]
	if !found {
		return noProfileNamed(name, opened.Profiles)
	}

	target, refused := renamedTo(opened.Profiles, name, fields.newName, given.Count("name") > 0)
	if refused != nil {
		return refused
	}

	profile := applied(old, profileTargets(&fields, flags), given, settingrow.CommandProfileUpdate)
	if profile == old && target == name && !fields.key {
		if already := alreadySet(name, old, given); already != "" {
			return output.Notice(cmd.ErrOrStderr(),
				fmt.Sprintf("profile %s is unchanged, since %s", creds.QuotedName(name), already))
		}

		return fmt.Errorf(
			"profile update %s changes nothing. Pass --provider, --model, --base-url, --account-id, --name or --key",
			creds.QuotedName(name))
	}

	// Ahead of the read, so a broken rule costs the user nothing.
	if refused := checkGiven(profile, given, given.Count(setting.Provider.Name()) > 0); refused != nil {
		return refused
	}

	if refused := checkUpdated(opened.Profiles.Renamed(name, target, old), target, old, profile, given, fields.key); refused != nil {
		return refused
	}

	if given.Count(setting.APIEndpoint.Name()) > 0 {
		if refused := refusePlainHTTP(flags, profile.BaseURL); refused != nil {
			return refused
		}
	}

	change := creds.Change{
		Path: path, Profiles: opened.Profiles, Name: name, NewName: target, Profile: profile, ToFile: fields.toFile,
	}
	if fields.key {
		change.ReadKey = func() (string, error) { return updatedKey(cmd, settings, name, old, profile) }
		change.DropKey = settings.stdinTTY
	}

	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Update(change)
	if refused := reported(cmd, settings.laterFailures, change, notices, unmovedKey(err, target != name)); refused != nil {
		return refused
	}

	return notify(cmd.ErrOrStderr(), keptOnProvider(target, old, profile, given))
}

func unmovedKey(err error, renames bool) error {
	var keychainErr *creds.KeychainError
	if !renames || !errors.As(err, &keychainErr) || errors.Is(err, creds.ErrKeychainTimeout) {
		return err
	}

	return fmt.Errorf("%w. Rename it with --%s, and add --%s to keep the key in profiles.json", err, flagKey, flagToFile)
}

func renamedTo(profiles creds.Profiles, name, newName string, given bool) (string, error) {
	newName = strings.TrimSpace(newName)
	if !given || newName == name {
		return name, nil
	}

	if invalid := creds.ValidName(newName); invalid != nil {
		return "", invalid
	}

	if _, taken := profiles.Profiles[newName]; taken {
		return "", fmt.Errorf("profile %s exists. Choose another name, or delete it first", creds.QuotedName(newName))
	}

	return newName, nil
}

func updatedKey(cmd *cobra.Command, settings rootSettings, name string, old, profile creds.Profile) (string, error) {
	key, err := readSecretLine(cmd, settings, "profile update")
	if err != nil || key != "" {
		return key, err
	}

	switch {
	case !customProfile(profile) && !settings.stdinTTY:
		return "", errors.New("profile update: the key is empty")
	case !customProfile(profile):
		return "", fmt.Errorf("profile %s holds no key, so its base URL must stay custom. Enter a key, or pass a "+
			"--%s of your own in the same update", creds.QuotedName(name), flagBaseURL)
	case !settings.stdinTTY && creds.HoldsKey(old):
		return "", fmt.Errorf("profile %s holds a key, and an empty key from stdin would delete it. To drop the key, "+
			"run onesie profile update %s --%s at a terminal and enter an empty key", creds.QuotedName(name), name, flagKey)
	}

	return "", nil
}

func applied(
	old creds.Profile, targets map[settingrow.Name]any, given *setting.Given, command settingrow.Command,
) creds.Profile {
	profile := old

	for _, row := range settingrow.Table() {
		bound, ok := row.FlagOn(command)
		if !ok || bound.Member.Key == "" || given.Count(row.Name) == 0 {
			continue
		}

		if value, isText := targets[row.Name].(*string); isText {
			*profileMember(&profile, bound.Member.Key) = strings.TrimSpace(*value)
		}
	}

	return profile
}

func checkUpdated(
	profiles creds.Profiles, name string, old, profile creds.Profile, given *setting.Given, readsKey bool,
) error {
	if profile.Provider != old.Provider && creds.HoldsKey(old) && !readsKey {
		return errors.New("profile update: --provider needs --key, since a key belongs to one provider")
	}

	// Ahead of checkProfile, whose rules would refuse the profile without saying why.
	if !readsKey && !creds.HoldsKey(old) && !customProfile(profile) {
		return fmt.Errorf("profile %s holds no key, so its base URL must stay custom. Pass --%s to store one",
			creds.QuotedName(name), flagKey)
	}

	baseURLSource := profileBaseURL
	if given.Count(setting.APIEndpoint.Name()) > 0 {
		baseURLSource = "--" + flagBaseURL
	}

	refused := checkProfile(profiles, name, profile, baseURLSource, readsKey)
	if refused == nil || !accountClash(profile) {
		return refused
	}

	return fmt.Errorf("%w. %s", refused, clearingHint(profile, given))
}

func accountClash(profile creds.Profile) bool {
	provider, err := onesie.ProviderNamed(profile.Provider)

	return profile.AccountID != "" && (profile.BaseURL != "" || err != nil || provider.EnvAccountID() == "")
}

func clearingHint(profile creds.Profile, given *setting.Given) string {
	baseURLRow, baseURLFlag := memberRow("base_url")
	accountRow, accountFlag := memberRow("account_id")
	baseURLTyped, accountTyped := given.Count(baseURLRow) > 0, given.Count(accountRow) > 0

	switch {
	case profile.BaseURL != "" && baseURLTyped && accountTyped:
		return "Pass only one of " + baseURLFlag + " and " + accountFlag
	case profile.BaseURL != "" && accountTyped:
		return fmt.Sprintf(`Clear it with %s "" in the same update`, baseURLFlag)
	}

	return fmt.Sprintf(`Clear it with %s "" in the same update`, accountFlag)
}

func keptOnProvider(name string, old, profile creds.Profile, given *setting.Given) notice {
	if profile.Provider == old.Provider {
		return notice{}
	}

	var kept []string

	for _, key := range []string{"base_url", "account_id", "model"} {
		if row, _ := memberRow(key); *profileMember(&profile, key) != "" && given.Count(row) == 0 {
			kept = append(kept, key)
		}
	}

	if len(kept) == 0 {
		return notice{}
	}

	return notice{
		text: fmt.Sprintf("profile %s keeps its %s on %s", creds.QuotedName(name), joinAnd(kept), profile.Provider),
	}
}

func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func memberRow(key string) (settingrow.Name, string) {
	for _, row := range settingrow.Table() {
		if bound, ok := row.FlagOn(settingrow.CommandProfileUpdate); ok && bound.Member.Key == key {
			return row.Name, "--" + bound.Long
		}
	}

	panic("cli: no profile update flag writes " + key)
}

func profileMember(profile *creds.Profile, key string) *string {
	switch key {
	case "provider":
		return &profile.Provider
	case "model":
		return &profile.Model
	case "base_url":
		return &profile.BaseURL
	case "account_id":
		return &profile.AccountID
	}

	panic("cli: no profile member " + key)
}

func newProfileDeleteCmd(settings rootSettings) *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Remove a profile and its keychain item",
		Long: "Remove a profile from profiles.json, then its keychain item, unless another profile names the " +
			"same item. Deleting the default leaves no default, and deleting the last profile removes the file.",
		Args:              profileNames("delete", 1, 1),
		ValidArgsFunction: completeProfileName(settings),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileDelete(cmd, settings, args[0])
		},
	}
}

func profileDelete(cmd *cobra.Command, settings rootSettings, name string) error {
	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	if _, found := opened.Profiles.Profiles[name]; !found {
		return noProfileNamed(name, opened.Profiles)
	}

	change := creds.Change{Path: path, Profiles: opened.Profiles, Name: name}
	notices, err := creds.NewProfileManager(settings.credStore, settings.keychain).Delete(change)

	return reported(cmd, settings.laterFailures, change, notices, err)
}

func newProfileUseCmd(settings rootSettings) *cobra.Command {
	return &cobra.Command{
		Use:   "use NAME",
		Short: "Make a profile the default a run uses",
		Long: "Make a profile the default, which a run uses when neither --profile, --provider, " +
			"ONESIE_PROFILE nor ONESIE_PROVIDER chooses.",
		Args:              profileNames("use", 1, 1),
		ValidArgsFunction: completeProfileName(settings),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileUse(cmd, settings, args[0])
		},
	}
}

func profileUse(cmd *cobra.Command, settings rootSettings, name string) error {
	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	profile, found := opened.Profiles.Profiles[name]
	if !found {
		return providerNameHint(noProfileNamed(name, opened.Profiles), name, "use")
	}

	profiles := opened.Profiles.With(name, profile)
	profiles.Default = name

	return saveProfiles(cmd, settings, path, profiles)
}

func newProfileListCmd(settings rootSettings) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the profiles, marking the one a run would use",
		Long: "List every profile with its provider, where its key lives and its model, and mark with * the " +
			"one a run would use now. ONESIE_PROFILE and ONESIE_PROVIDER count. When a run would use no " +
			"profile, a stderr line says why.",
		Args: profileNames("list", 0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return profileList(cmd, settings)
		},
	}
}

func profileList(cmd *cobra.Command, settings rootSettings) error {
	opened, err := settings.holder.opened()
	if err != nil {
		return err
	}

	if len(opened.Profiles.Profiles) == 0 {
		return output.Notice(cmd.ErrOrStderr(),
			"no profiles. Run onesie profile create NAME --provider PROVIDER to store one")
	}

	active, err := settings.holder.resolved()

	if writeErr := writeProfiles(cmd.OutOrStdout(), opened.Profiles, active.profile); writeErr != nil {
		return writeErr
	}

	reason := inactiveReason(active, err)
	if reason == "" {
		return nil
	}

	return output.Notice(cmd.ErrOrStderr(), reason)
}

func writeProfiles(w io.Writer, profiles creds.Profiles, active string) error {
	rows := [][]string{{"NAME", "PROVIDER", "KEY", "MODEL"}}

	for _, name := range slices.Sorted(maps.Keys(profiles.Profiles)) {
		profile := profiles.Profiles[name]

		key := sourceFile
		switch {
		case profile.Store == creds.StoreKeychain:
			key = sourceKeychain
		case !creds.HoldsKey(profile):
			key = sourceNone
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
		return "active: none, since " + text(err)
	case active.profile == "":
		return "active: " + active.by + "=" + active.provider.Name() + ", no profile"
	}

	return ""
}

func newProfileShowCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show [NAME]",
		Short: "Report a profile, or what a run would use, never the key",
		Long: "With a name, report what a run on that profile would use, as --profile NAME runs it unless a " +
			"run uses it now. Without one, report what a run would use now, profile or not, as --profile, " +
			"--provider and their variables choose. It prints profile, provider, chosen by, key, model and " +
			"base_url lines, and an account line on " + providers.WithAccounts() + ". The profile line names " +
			"its rank, the default profile, which the environment beats, or a chosen profile, which beats the " +
			"environment. The model and base_url lines give the value a run would use and where it comes " +
			"from. The model comes from the profile's model, an env " +
			"variable such as TYPESAFE_DEFAULT_MODEL, or the provider's default. The base_url comes from the " +
			"profile's base_url, the profile's account_id, " + settingEnv(setting.APIEndpoint.Name()) + ", a variable such as TYPESAFE_BASE_URL, " +
			providers.AccountVariables() + ", or the provider's default. An allow_http line says a run needs " +
			"--allow-http, when base_url is plain http to another host. It never reads the keychain or " +
			"prints a key.\n\n" +
			"It exits 2 when key is none on the provider's own base URL, or account is none, printing the " +
			"hint a run gives for each, when the account is not 32 hexadecimal characters, or when " +
			"ONESIE_CACHE, ONESIE_LONG_CONTEXT or ONESIE_CACHE_TTL holds " +
			"a value a run refuses, as a real run does.",
		Args:              profileNames("show", 0, 1),
		ValidArgsFunction: completeProfileName(settings),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileShow(cmd, settings, flags, args)
		},
	}
}

func profileShow(cmd *cobra.Command, settings rootSettings, flags *runFlags, args []string) error {
	inspection, err := inspectConnection(settings, flags, args)
	if err != nil {
		return err
	}
	shown, baseURL, source, invalid := inspection.shown, inspection.baseURL, inspection.source, inspection.invalid

	account := inspection.account

	baseURLLine := sourceNone
	if invalid == nil {
		baseURLLine = output.Printable(baseURL) + ", " + baseURLFrom(source, shown)
	}

	custom := inspection.custom

	lines := []string{
		"profile: " + shown.rankedProfile(),
		"provider: " + shown.provider.Name(),
		"chosen by: " + shown.by,
		"key: " + shown.keyLine(custom),
		"model: " + output.Printable(shown.modelFor(shown.model)) + ", " + shown.modelFrom,
		"base_url: " + baseURLLine,
	}

	if inspection.remoteHTTP {
		lines = append(lines, "allow_http: needed, since base_url is plain http to another host")
	}

	if account != "" {
		lines = append(lines, "account: "+output.Printable(account))
	}

	if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n")); printErr != nil {
		return printErr
	}

	return showRefusals(cmd, settings, inspection)
}

func showRefusals(
	cmd *cobra.Command, settings rootSettings, inspection connectionInspection,
) error {
	shown, accountErr, account := inspection.shown, inspection.invalid, inspection.account
	custom, switchErr := inspection.custom, inspection.switchErr
	var refusals []error

	if shown.profile == "" && shown.keyFrom == sourceNone && !custom {
		refusals = append(refusals, missingKey(settings, shown))
	}

	if account == sourceNone {
		refusals = append(refusals, accountNote(accountErr, shown))
	}

	// Checked as a run checks them, after the report, so a reader of the key line still finds it.
	if switchErr != nil {
		refusals = append(refusals, switchErr)
	}

	if len(refusals) == 0 {
		return nil
	}

	last := len(refusals) - 1
	for _, refusal := range refusals[:last] {
		if !worthReporting(refusal) {
			continue
		}

		if err := output.Notice(cmd.ErrOrStderr(), output.Printable(text(refusal))); err != nil {
			return err
		}
	}

	return refusals[last]
}

func missingKey(settings rootSettings, shown resolution) error {
	noKey := fmt.Errorf("%w. Set %s", onesie.ErrNoAPIKey, shown.provider.EnvAPIKey())

	opened, err := settings.holder.opened()
	if err != nil {
		return noKey
	}

	return missingKeyHint(noKey, shown, opened.Profiles)
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
		Args:              profileNames("test", 0, 1),
		ValidArgsFunction: completeProfileName(settings),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileTest(cmd, settings, flags, args)
		},
	}

	allowHTTP, _ := settingrow.Lookup(setting.AllowHTTP.Name())
	cmd.Flags().BoolVar(&flags.allowHTTP, flagAllowHTTP, false, allowHTTP.Help)

	return cmd
}

func profileTest(cmd *cobra.Command, settings rootSettings, flags *runFlags, args []string) error {
	shown, err := shownResolution(settings, flags, args, "test")
	if err != nil {
		return err
	}

	tested, err := testedConnection(settings, shown)
	if err != nil {
		return err
	}

	client, err := settings.newClient(cmd.Context(), tested)
	if err != nil {
		return err
	}

	models, err := client.VerifyKey(cmd.Context())
	if err != nil {
		return keyHint(advise(err, ""), tested)
	}

	_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\nprovider: %s\nkey: %s\nmodels: %d\n",
		cmp.Or(tested.profile, noProfile), tested.provider.Name(), tested.keyLine(!client.SendsKey()), len(models))

	return printErr
}

func testedConnection(settings rootSettings, shown resolution) (resolution, error) {
	run := loadedRun{}
	chosen := setting.Choice{}

	switch {
	case shown.profile != "":
		opened, err := settings.holder.opened()
		if err != nil {
			return resolution{}, err
		}

		run.name, run.profile = shown.profile, opened.Profiles.Profiles[shown.profile]
		if shown.chosenProfile() {
			chosen.Profile, chosen.ProfileBy = shown.profile, setting.SourceEnv
		} else {
			chosen.Profile, chosen.ProfileBy = shown.profile, setting.SourceDefaultProfile
		}
	case shown.by != byNothing:
		chosen.Provider, chosen.ProviderBy = shown.provider.Name(), setting.SourceEnv
	}

	layers, err := setting.Read(settingrow.Table(), setting.Sources{
		LookupEnv: profileTestModelEndpointKeyEnv(settings.lookupEnv),
		Profile:   run.values(),
		Choice:    &chosen,
		Command:   settingrow.CommandRoot,
	})
	if err != nil {
		return resolution{}, err
	}

	merged, _ := setting.Merge(settingrow.Table(), layers, chosen)

	tested := runResolution(merged, shown.provider)
	tested.by = shown.by
	return tested, nil
}

func profileTestModelEndpointKeyEnv(lookupEnv func(string) (string, bool)) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, key := range []settingrow.Name{setting.Model.Name(), setting.APIEndpoint.Name(), setting.APIKey.Name()} {
			row, _ := settingrow.Lookup(key)
			if slices.ContainsFunc(row.Env, func(variable settingrow.EnvSpec) bool { return variable.Name == name }) {
				return lookupEnv(name)
			}
		}

		return "", false
	}
}

func shownResolution(settings rootSettings, flags *runFlags, args []string, command string) (resolution, error) {
	if len(args) == 1 {
		if refused := refuseChosen(flags, command); refused != nil {
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
		return resolution{}, providerNameHint(noProfileNamed(name, opened.Profiles), name, command)
	}

	by := notActive
	if active, activeErr := settings.holder.resolved(); activeErr == nil &&
		active.profile == name {
		by = active.by
	}

	return profileRun(name, profile, by)
}

func profileTargets(fields *profileFlags, flags *runFlags) map[settingrow.Name]any {
	return map[settingrow.Name]any{
		setting.Provider.Name(): &fields.provider, setting.Model.Name(): &fields.model,
		setting.APIEndpoint.Name(): &fields.baseURL, setting.AllowHTTP.Name(): &flags.allowHTTP,
		"account_id": &fields.accountID, "key": &fields.key, "to_file": &fields.toFile, "name": &fields.newName,
	}
}

func alreadySet(name string, profile creds.Profile, given *setting.Given) string {
	var unset, held []string

	for _, member := range []struct {
		row         settingrow.Name
		name, value string
	}{
		{row: "name", name: "name", value: name},
		{row: setting.Provider.Name(), name: "provider", value: profile.Provider},
		{row: setting.Model.Name(), name: "model", value: profile.Model},
		{row: setting.APIEndpoint.Name(), name: "base_url", value: profile.BaseURL},
		{row: "account_id", name: "account_id", value: profile.AccountID},
	} {
		switch {
		case given.Count(member.row) == 0:
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
	newName   string
	key       bool
	toFile    bool
}

func refuseChosen(flags *runFlags, command string) error {
	if flags.profileChosen() {
		return fmt.Errorf("--profile does not apply to profile %s", command)
	}

	if flags.providerChosen() {
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
	return fmt.Errorf("there is no profile named %s. %s", creds.QuotedName(name), knownProfiles(profiles))
}

func checkGiven(profile creds.Profile, given *setting.Given, providerGiven bool) error {
	provider, err := onesie.ProviderNamed(profile.Provider)
	if providerGiven && (err != nil || provider.Name() != profile.Provider) {
		return fmt.Errorf("--%s names %s, which is not %s",
			flagProvider, creds.QuotedName(profile.Provider), providers.Names())
	}

	accountGiven := given.Count("account_id") > 0 && profile.AccountID != ""

	switch {
	case accountGiven && provider.EnvAccountID() == "":
		return fmt.Errorf("--%s applies only to %s, and this profile is on %s",
			flagAccountID, providers.WithAccounts(), profile.Provider)
	case accountGiven && !creds.ValidAccountID(profile.AccountID):
		return fmt.Errorf("--%s must be 32 hexadecimal characters", flagAccountID)
	}

	return nil
}

func refusePlainHTTP(flags *runFlags, baseURL string) error {
	// An empty base URL means none is stored.
	if flags.allowHTTP || baseURL == "" {
		return nil
	}

	// checkProfile already refused any other broken base URL, so only plain http is left to refuse.
	var refused *onesie.BaseURLError
	if !errors.As(onesie.RefuseInsecureBaseURL(baseURL), &refused) {
		return nil
	}

	return baseURLRefusal(refused, "--"+flagBaseURL, storePlainHTTP)
}

func checkProfile(
	profiles creds.Profiles, name string, profile creds.Profile, baseURLSource string, readsKey bool,
) error {
	if profile.BaseURL != "" {
		if refused := checkBaseURL(profile.BaseURL, baseURLSource); refused != nil {
			return refused
		}
	}

	// The key is read only once the rest passes, so a stand in takes its place for the rules.
	if readsKey && !creds.HoldsKey(profile) {
		profile.APIKey = keyPlaceholder
	}

	if broken := profiles.With(name, profile).Validate(); broken != nil {
		return broken
	}

	return nil
}

func customProfile(profile creds.Profile) bool {
	provider, err := onesie.ProviderNamed(profile.Provider)

	return err == nil && profile.BaseURL != "" && provider.CustomBaseURL(profile.BaseURL)
}

func checkBaseURL(baseURL, source string) error {
	var refused *onesie.BaseURLError
	if errors.As(onesie.ValidateBaseURL(baseURL), &refused) {
		return baseURLRefusal(refused, source, sendPlainHTTP)
	}

	return nil
}

func saveProfiles(cmd *cobra.Command, settings rootSettings, path string, profiles creds.Profiles) error {
	warnings, err := settings.credStore.SaveProfilesIn(filepath.Dir(path), profiles)
	if err != nil {
		return err
	}

	return notify(cmd.ErrOrStderr(), warningLines(warnings)...)
}

func reported(
	cmd *cobra.Command, laterFailures *failureLog, change creds.Change, notices []creds.Notice, err error,
) error {
	lines := make([]notice, 0, len(notices))
	for _, each := range notices {
		lines = append(lines, noticeLine(each, change))
	}

	if err == nil {
		return notify(cmd.ErrOrStderr(), lines...)
	}

	if errors.Is(err, creds.ErrKeychainTimeout) {
		err = fmt.Errorf("%w. Answer the keychain prompt, or run the command again with --file", err)
	}

	return laterFailures.keepFirst(err, notify(cmd.ErrOrStderr(), lines...))
}

func noticeLine(each creds.Notice, change creds.Change) notice {
	saved := cmp.Or(change.NewName, change.Name)

	switch each.Kind {
	case creds.SaveWarning:
		return warningLine(each.Err)
	case creds.KeyInKeychain:
		return notice{text: "stored the key in the OS keychain as " + each.Account}
	case creds.KeyInFile:
		return notice{text: each.Err.Error() + ". The key is in " + change.Path + " instead", warning: true}
	case creds.FirstDefault:
		return notice{text: creds.QuotedName(change.Name) + " is the default profile, since it is the first"}
	case creds.DefaultDeleted:
		return notice{text: creds.QuotedName(change.Name) + " was the default, so no profile is the default now. " +
			"Run onesie profile use NAME to choose one"}
	case creds.ItemLeftBehind:
		return notice{
			text: each.Err.Error() + ". The keychain item " + each.Account + " is left behind", warning: true,
		}
	case creds.OldKeyUnread:
		return notice{text: "the keychain item " + each.Account + " now holds the new key, and its old key " +
			"could not be read to put back", warning: true}
	case creds.KeyNotStored:
		return notice{text: "profile " + creds.QuotedName(saved) + " holds no key, since its base URL is custom"}
	case creds.KeyMoved:
		return notice{text: "moved the key in the OS keychain to " + each.Account}
	case creds.KeyDropped:
		return notice{text: "profile " + creds.QuotedName(saved) + " holds no key now"}
	case creds.OldKeyLost:
		return notice{text: each.Err.Error() + ". The keychain item " + each.Account +
			" now holds the new key, and its old key could not be put back", warning: true}
	}

	return notice{}
}

type notice struct {
	text    string
	warning bool
}

func warningLines(warnings []error) []notice {
	lines := make([]notice, 0, len(warnings))
	for _, warning := range warnings {
		lines = append(lines, warningLine(warning))
	}

	return lines
}

func warningLine(warning error) notice {
	var unprotected *creds.ModeWarning

	return notice{text: warning.Error(), warning: errors.As(warning, &unprotected)}
}

func notify(w io.Writer, notices ...notice) error {
	for _, each := range notices {
		if each.text == "" {
			continue
		}

		write := output.Notice
		if each.warning {
			write = output.Warning
		}

		// stderr rather than stdout. These are notices, and a redirected stdout stays empty.
		if err := write(w, each.text); err != nil {
			return err
		}
	}

	return nil
}
