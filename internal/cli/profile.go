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
	providersOwn   = "the provider's"
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
		Args: cobra.NoArgs,
		// Set, so cobra validates the arguments before deciding the command is not runnable. A parent
		// with no RunE answers onesie profile nonsense with its own help and exit 0.
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
	_, spelled := mockSource(settings, flags)
	if spelled == "" {
		return nil
	}

	advice := "Unset it"
	if spelled == flagMock {
		advice = "Drop it"
	}

	return fmt.Errorf("onesie: profile reads and writes real configuration, so it does not run under %s. %s",
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
			"letters, digits, - or _, and does not start with -. The first profile becomes the default.",
		Example: "  pass show openrouter | head -n 1 | onesie profile create work --provider openrouter\n" +
			"  onesie profile create cf --provider cloudflare --account-id 0123456789abcdef0123456789abcdef",
		Args: profileNames("create", 1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileCreate(cmd, settings, flags, args[0], *fields)
		},
	}

	cmd.Flags().StringVar(&fields.provider, flagProvider, "",
		"the profile's provider, typesafe, openrouter, berget, levanto or cloudflare. Required")
	addMemberFlags(cmd, fields)
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
		return fmt.Errorf("onesie: %w", invalid)
	}

	provider := strings.TrimSpace(fields.provider)
	if provider == "" {
		return errors.New(
			"onesie: profile create needs --provider: typesafe, openrouter, berget, levanto or cloudflare")
	}

	path, opened, err := profilesToWrite(settings)
	if err != nil {
		return err
	}

	if _, taken := opened.Profiles.Profiles[name]; taken {
		return fmt.Errorf("onesie: profile '%s' exists. Change it with onesie profile update %s, "+
			"or delete it and create it again", name, name)
	}

	profile := creds.Profile{
		Provider: provider, Model: strings.TrimSpace(fields.model), BaseURL: strings.TrimSpace(fields.baseURL),
		AccountID: strings.TrimSpace(fields.accountID),
	}

	account := creds.KeychainAccount(name, path)

	// Ahead of the read, so a typo costs the user nothing and leaves no file behind.
	if refused := checkProfile(opened.Profiles, name, profile); refused != nil {
		return refused
	}

	if refused := refuseTakenItem(opened.Profiles, name, account, fields.toFile); refused != nil {
		return refused
	}

	key, err := readKey(cmd, settings, "profile create")
	if err != nil {
		return err
	}

	stored, notice, err := storeProfileKey(settings, profile, key, account, path, fields.toFile)
	if err != nil {
		return err
	}

	profiles := withProfile(opened.Profiles, name, stored)

	first := ""
	if len(opened.Profiles.Profiles) == 0 {
		profiles.Default = name
		first = "onesie: " + name + " is the default profile, since it is the first"
	}

	if saveErr := saveProfiles(cmd, settings, path, profiles); saveErr != nil {
		if stored.Store == creds.StoreKeychain {
			return failedSave(cmd, saveErr, undoneItem(settings.keychain.Delete(account), account))
		}

		return saveErr
	}

	return notify(cmd, notice, first)
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
	addMemberFlags(cmd, fields)
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
		return fmt.Errorf(
			"onesie: profile update %s changes nothing. Pass --model, --base-url, --account-id or --key", name)
	}

	// Ahead of the read, so a broken rule costs the user nothing.
	if refused := checkProfile(opened.Profiles, name, profile); refused != nil {
		return refused
	}

	if !fields.key {
		return saveProfiles(cmd, settings, path, withProfile(opened.Profiles, name, profile))
	}

	if refused := refuseTakenItem(
		opened.Profiles, name, creds.KeychainAccount(name, path), fields.toFile); refused != nil {
		return refused
	}

	return replaceKey(cmd, settings, path, opened.Profiles, name, profile, fields.toFile)
}

func refuseUpdate(fields profileFlags, changed func(string) bool) error {
	switch {
	case fields.toFile && !fields.key:
		return errors.New("onesie: profile update: --file applies to a new key, so it needs --key")
	case changed(flagProvider) && !fields.key:
		return errors.New("onesie: profile update: --provider needs --key, since a key belongs to one provider")
	case changed(flagProvider) && strings.TrimSpace(fields.provider) == "":
		return errors.New("onesie: profile update: --provider is empty. " +
			"Name typesafe, openrouter, berget, levanto or cloudflare")
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

func replaceKey(
	cmd *cobra.Command, settings rootSettings, path string, profiles creds.Profiles, name string,
	profile creds.Profile, toFile bool,
) error {
	key, err := readKey(cmd, settings, "profile update")
	if err != nil {
		return err
	}

	oldItem := keychainItem(profiles.Profiles[name])
	account := creds.KeychainAccount(name, path)

	kept, err := keepKey(settings.keychain, oldItem, account, toFile)
	if err != nil {
		return err
	}

	stored, notice, err := storeProfileKey(settings, profile, key, account, path, toFile)
	if err != nil {
		return err
	}

	if saveErr := saveProfiles(cmd, settings, path, withProfile(profiles, name, stored)); saveErr != nil {
		if stored.Store == creds.StoreKeychain {
			return failedSave(cmd, saveErr, kept.undo(settings.keychain, account))
		}

		return saveErr
	}

	notices := []string{notice}

	// After the save, since the file points at the old item until then.
	if oldItem != "" && oldItem != stored.Account && !sharedItem(profiles, name, oldItem) {
		if deleteErr := settings.keychain.Delete(oldItem); deleteErr != nil {
			notices = append(notices, leftBehind(deleteErr, oldItem))
		}
	}

	return notify(cmd, notices...)
}

func keepKey(keychain Keychain, oldItem, account string, toFile bool) (keptKey, error) {
	if toFile || oldItem != account {
		return keptKey{}, nil
	}

	key, err := keychain.Get(account)

	// A keychain that did not answer a read would not answer a write either, and a write to it
	// could land later, after onesie has given up on it.
	if errors.Is(err, creds.ErrKeychainTimeout) {
		return keptKey{}, fmt.Errorf("%w. Answer the keychain prompt, or run the command again with --file", err)
	}

	return keptKey{
		same: true, key: key, found: err == nil, missing: errors.Is(err, creds.ErrKeychainMissing),
	}, nil
}

type keptKey struct {
	same    bool
	key     string
	found   bool
	missing bool
}

func (k keptKey) undo(keychain Keychain, account string) string {
	// A new item, or one that held no key, would keep a key no saved file chose.
	if !k.same || k.missing {
		return undoneItem(keychain.Delete(account), account)
	}

	if !k.found {
		return "warning: the keychain item " + account + " now holds the new key, and its old key " +
			"could not be read to put back"
	}

	if err := keychain.Set(account, k.key); err != nil {
		return "warning: " + strings.TrimPrefix(err.Error(), "onesie: ") + ". The keychain item " + account +
			" now holds the new key, and its old key could not be put back"
	}

	return ""
}

func failedSave(cmd *cobra.Command, saveErr error, notice string) error {
	//nolint:errcheck // The save error carries the exit code, and a stderr that cannot take the notice must not replace it.
	_ = notify(cmd, notice)

	return saveErr
}

func undoneItem(err error, account string) string {
	if err == nil {
		return ""
	}

	return leftBehind(err, account)
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

	profile, found := opened.Profiles.Profiles[name]
	if !found {
		return noProfileNamed(name, opened.Profiles)
	}

	rest := withoutProfile(opened.Profiles, name)

	var notices []string

	// With the last profile goes any credentials.json beside it, so a later run does not convert
	// the deleted profiles back.
	if len(rest.Profiles) == 0 {
		warnings, clearErr := settings.credStore.ClearProfilesIn(filepath.Dir(path))
		if clearErr != nil {
			return clearErr
		}

		notices = append(notices, warningLines(warnings)...)
	} else if saveErr := saveProfiles(cmd, settings, path, rest); saveErr != nil {
		return saveErr
	}

	if opened.Profiles.Default == name && len(rest.Profiles) > 0 {
		notices = append(notices, "onesie: "+name+" was the default, so no profile is the default now. "+
			"Run onesie profile use NAME to choose one")
	}

	// After the save, since the file points at the item until then. A hand written file can name one
	// item from two profiles, and the other one still needs it.
	if account := keychainItem(profile); account != "" && !sharedItem(opened.Profiles, name, account) {
		if deleteErr := settings.keychain.Delete(account); deleteErr != nil {
			notices = append(notices, leftBehind(deleteErr, account))
		}
	}

	return notify(cmd, notices...)
}

func withoutProfile(profiles creds.Profiles, name string) creds.Profiles {
	rest := creds.Profiles{Default: profiles.Default, Profiles: maps.Clone(profiles.Profiles)}
	delete(rest.Profiles, name)

	if rest.Default == name {
		rest.Default = ""
	}

	return rest
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

	profiles := withProfile(opened.Profiles, name, profile)
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

	opened, err := openedProfiles(settings)
	if err != nil {
		return err
	}

	if len(opened.Profiles.Profiles) == 0 {
		return notify(cmd, "onesie: no profiles. Run onesie profile create NAME --provider PROVIDER to store one")
	}

	active, err := chooseAndResolve(flags, settings.lookupEnv, opened.Profiles)

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
			"by, key, model and base_url lines, and an account line on cloudflare. It never reads the " +
			"keychain or prints a key.\n\n" +
			"It exits 2 when key or account is none, as a real run does.",
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

	account := shown.accountSource(settings.lookupEnv)

	lines := []string{
		"profile: " + cmp.Or(shown.profile, noProfile),
		"provider: " + shown.provider.Name,
		"chosen by: " + shown.by,
		"key: " + shown.keyLine(),
		"model: " + cmp.Or(output.Printable(shown.model), providersOwn),
		"base_url: " + cmp.Or(output.Printable(shown.baseURL), providersOwn),
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

func newProfileTestCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "test [NAME]",
		Short: "Call the models endpoint with a profile's key",
		Long: "Call the provider's models endpoint with the key of the named profile, or of what a run would " +
			"use when given no name, which costs no tokens.\n\n" +
			"Berget lists its models to any key, so on berget profile test also asks one small question with " +
			"the profile's model, which spends a few tokens.",
		Args: profileNames("test", 0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return profileTest(cmd, settings, flags, args)
		},
	}
}

func profileTest(cmd *cobra.Command, settings rootSettings, flags *runFlags, args []string) error {
	tested, err := shownResolution(settings, flags, args, "test")
	if err != nil {
		return err
	}

	if tested.keyFrom == sourceNone {
		return noKeyToTest(settings, tested)
	}

	client, err := settings.clientFor(cmd.Context(), tested)
	if err != nil {
		return err
	}

	models, err := client.VerifyKey(cmd.Context())
	if err != nil {
		return advise(err, "", false)
	}

	_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\nprovider: %s\nkey: %s\nmodels: %d\n",
		cmp.Or(tested.profile, noProfile), tested.provider.Name, tested.keyLine(), len(models))

	return printErr
}

func noKeyToTest(settings rootSettings, tested resolution) error {
	missing := &onesie.ValidationError{Message: "no API key. Set " + tested.provider.EnvAPIKey}

	opened, err := openedProfiles(settings)
	if err != nil || firstProfileOf(tested.provider.Name, opened.Profiles) == "" {
		return fmt.Errorf("%w, or store one with onesie profile create NAME --provider %s",
			missing, tested.provider.Name)
	}

	return missingKeyHint(missing, tested, opened.Profiles)
}

func shownResolution(settings rootSettings, flags *runFlags, args []string, command string) (resolution, error) {
	if len(args) == 1 {
		if refused := refuseChosen(flags, command, true); refused != nil {
			return resolution{}, refused
		}
	}

	opened, err := openedProfiles(settings)
	if err != nil {
		return resolution{}, err
	}

	if len(args) == 0 {
		return chooseAndResolve(flags, settings.lookupEnv, opened.Profiles)
	}

	name := args[0]

	profile, found := opened.Profiles.Profiles[name]
	if !found {
		return resolution{}, noProfileNamed(name, opened.Profiles)
	}

	by := notActive
	if active, activeErr := chooseAndResolve(flags, settings.lookupEnv, opened.Profiles); activeErr == nil &&
		active.profile == name {
		by = active.by
	}

	return profileRun(name, profile, by)
}

func addMemberFlags(cmd *cobra.Command, fields *profileFlags) {
	cmd.Flags().StringVar(&fields.model, flagModel, "", "the model a run uses when it names none")
	cmd.Flags().StringVar(&fields.baseURL, flagBaseURL, "", "the api root the profile's runs use")
	cmd.Flags().StringVar(&fields.accountID, flagAccountID, "",
		"cloudflare only, the account id, 32 hexadecimal characters")
}

func profileNames(command string, least, most int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		switch {
		case len(args) >= least && len(args) <= most:
			return nil
		case most == 0:
			return fmt.Errorf("onesie: profile %s takes no argument", command)
		case least == most:
			return fmt.Errorf("onesie: profile %s takes one profile name, as in onesie profile %s work",
				command, command)
		}

		return fmt.Errorf("onesie: profile %s takes at most one profile name", command)
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
		return fmt.Errorf("onesie: --profile does not apply to profile %s", command)
	}

	if provider && flags.providerSet {
		return fmt.Errorf("onesie: --provider does not apply to profile %s", command)
	}

	return nil
}

func profilesToWrite(settings rootSettings) (string, creds.Opened, error) {
	path, err := settings.profilesPath()
	if err != nil {
		return "", creds.Opened{}, err
	}

	opened, err := openedProfiles(settings)
	if err != nil {
		return "", creds.Opened{}, err
	}

	return path, opened, nil
}

func noProfileNamed(name string, profiles creds.Profiles) error {
	return fmt.Errorf("onesie: there is no profile named '%s'. %s", name, knownProfiles(profiles))
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

	if broken := withProfile(profiles, name, profile).Validate(); broken != nil {
		return fmt.Errorf("onesie: %w", broken)
	}

	return nil
}

func withProfile(profiles creds.Profiles, name string, profile creds.Profile) creds.Profiles {
	changed := creds.Profiles{Default: profiles.Default, Profiles: maps.Clone(profiles.Profiles)}
	if changed.Profiles == nil {
		changed.Profiles = map[string]creds.Profile{}
	}

	changed.Profiles[name] = profile

	return changed
}

func storeProfileKey(
	settings rootSettings, profile creds.Profile, key, account, path string, toFile bool,
) (stored creds.Profile, notice string, err error) {
	profile.APIKey, profile.Store, profile.Account, profile.KeyEnd = "", "", "", ""

	if toFile {
		profile.APIKey = key

		return profile, "", nil
	}

	err = settings.keychain.Set(account, key)
	if err == nil {
		profile.Store, profile.Account, profile.KeyEnd = creds.StoreKeychain, account, creds.KeyEnd(key)

		return profile, "stored the key in the OS keychain as " + account, nil
	}

	// A keychain that timed out may still be waiting on a prompt that stores the key later, so a
	// fallback here could leave the key in both places.
	if errors.Is(err, creds.ErrKeychainTimeout) {
		return creds.Profile{}, "", fmt.Errorf("%w. Answer the keychain prompt, or run the command again with --file", err)
	}

	profile.APIKey = key

	return profile, "warning: " + strings.TrimPrefix(err.Error(), "onesie: ") + ". The key is in " + path + " instead", nil
}

func saveProfiles(cmd *cobra.Command, settings rootSettings, path string, profiles creds.Profiles) error {
	warnings, err := settings.credStore.SaveProfilesIn(filepath.Dir(path), profiles)
	if err != nil {
		return err
	}

	return notify(cmd, warningLines(warnings)...)
}

func warningLines(warnings []error) []string {
	lines := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		lines = append(lines, warning.Error())
	}

	return lines
}

func keychainItem(profile creds.Profile) string {
	if profile.Store != creds.StoreKeychain {
		return ""
	}

	return profile.Account
}

func refuseTakenItem(profiles creds.Profiles, name, account string, toFile bool) error {
	owner := itemOwner(profiles, name, account)
	if toFile || owner == "" {
		return nil
	}

	return fmt.Errorf("onesie: profile '%s' already uses the keychain item %s. Delete or update it first",
		owner, account)
}

func sharedItem(profiles creds.Profiles, except, account string) bool {
	return itemOwner(profiles, except, account) != ""
}

func itemOwner(profiles creds.Profiles, except, account string) string {
	for _, name := range slices.Sorted(maps.Keys(profiles.Profiles)) {
		if name != except && keychainItem(profiles.Profiles[name]) == account {
			return name
		}
	}

	return ""
}

func leftBehind(err error, account string) string {
	return "warning: " + strings.TrimPrefix(err.Error(), "onesie: ") + ". The keychain item " + account +
		" is left behind"
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
