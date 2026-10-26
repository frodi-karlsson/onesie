package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func newAuthCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store, inspect and remove the API key onesie falls back to",
		Args:  cobra.NoArgs,
		// Set, so cobra validates the arguments before deciding the command is not runnable. A
		// parent with no RunE answers onesie auth nonsense with its own help and exit 0, which is the
		// silence this guard exists to prevent. Measured against cobra 1.10.2.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return refuseMock(settings, flags, "auth reads and writes a real key")
		},
	}

	cmd.AddCommand(newAuthSetCmd(settings, flags))
	cmd.AddCommand(newAuthStatusCmd(settings, flags))
	cmd.AddCommand(newAuthTestCmd(settings, flags))
	cmd.AddCommand(newAuthClearCmd(settings, flags))

	return cmd
}

func newAuthSetCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	baseURL := ""
	toFile := false

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Read a key from a prompt or stdin and store it",
		Long: "Read a key from a hidden prompt on a terminal, or from stdin otherwise, and store it.\n\n" +
			"Stdin must hold the key alone. pass show and its siblings print metadata after the " +
			"secret, so pipe them through head -n 1.",
		Example: "  pass show openrouter | head -n 1 | onesie --provider openrouter auth set",
		Args:    authNoArgs("set", "It reads the key from a prompt or stdin"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authSet(cmd, settings, flags, setOptions{
				baseURL: baseURL, hasBaseURL: cmd.Flags().Changed(flagBaseURL), toFile: toFile,
			})
		},
	}

	cmd.Flags().StringVar(&baseURL, flagBaseURL, "", "api root stored beside the key")
	cmd.Flags().BoolVar(&toFile, "file", false, "store the key in the credential file, not the OS keychain")

	return cmd
}

func authSet(cmd *cobra.Command, settings rootSettings, flags *runFlags, opts setOptions) error {
	baseURL, hasBaseURL := opts.baseURL, opts.hasBaseURL

	chosen, err := runResolution(settings, flags)
	if err != nil {
		return err
	}

	provider := chosen.provider

	// Ahead of the read, so a typo costs the user nothing and leaves no file behind. onesie.New owns
	// the same check, and a second one here would be a second message to keep in step.
	if hasBaseURL {
		if invalid := onesie.ValidateBaseURL(baseURL); invalid != nil {
			return invalid
		}
	}

	key, err := readKey(cmd, settings, "auth set")
	if err != nil {
		return err
	}

	path, err := settings.credPath()
	if err != nil {
		return err
	}

	file, found, loadErr := settings.credStore.Load(path)

	// Rebuilding a file others can reach would drop every other provider's entry, and chmod is the
	// whole fix.
	var exposed *creds.ReadableError
	if errors.As(loadErr, &exposed) {
		return loadErr
	}

	// stderr rather than stdout. It is a notice and not output, and pass show x | onesie auth set > log
	// must not put a filesystem path in a log the user expected to stay empty.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "writing the credential file "+path); printErr != nil {
		return printErr
	}

	if loadErr != nil || !found {
		file = creds.File{Providers: map[string]creds.Entry{}}
	}

	previous := file.Providers[provider.Name]
	account := creds.KeychainAccount(provider.Name, path)
	replaced := replacedKey(settings, previous, provider, account)

	entry, notice, err := storeKey(settings, provider, key, path, account, opts.toFile)
	if err != nil {
		return err
	}

	if hasBaseURL {
		entry.BaseURL = baseURL
	}

	file.Providers[provider.Name] = entry

	warning, err := settings.credStore.Save(path, file)
	if err != nil {
		if entry.Store == creds.StoreKeychain {
			return errors.Join(err, replaced.undo(settings.keychain, account))
		}

		return err
	}

	if notice != "" {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), notice); printErr != nil {
			return printErr
		}
	}

	// After the save, since the file points at the old item until then.
	if stale := previousAccount(previous, provider); stale != "" && stale != entry.Account {
		if deleteErr := settings.keychain.Delete(stale); deleteErr != nil {
			if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+deleteErr.Error()); printErr != nil {
				return printErr
			}
		}
	}

	if loadErr != nil {
		_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: replaced "+path+
			", which could not be read. Any other provider's key in it was dropped")
		if printErr != nil {
			return printErr
		}
	}

	if warning != nil {
		// No onesie prefix. The word warning classifies the line already, and the streaming warnings
		// in run.go and print.go are printed the same way.
		_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning.Error())

		return printErr
	}

	return nil
}

type setOptions struct {
	baseURL    string
	hasBaseURL bool
	toFile     bool
}

func storeKey(
	settings rootSettings,
	provider onesie.Provider,
	key, path, account string,
	toFile bool,
) (entry creds.Entry, notice string, err error) {
	if toFile {
		return creds.Entry{APIKey: key}, "", nil
	}

	err = settings.keychain.Set(account, key)
	if err == nil {
		return creds.Entry{Store: creds.StoreKeychain, Account: account},
			"stored the " + provider.Name + " key in the OS keychain", nil
	}

	// A keychain that timed out may still be waiting on a prompt that stores the key later, so a
	// fallback here could leave the key in both places.
	if errors.Is(err, creds.ErrKeychainTimeout) {
		return creds.Entry{}, "", fmt.Errorf("%w. Answer the keychain prompt, or run onesie auth set --file", err)
	}

	return creds.Entry{APIKey: key},
		"warning: " + strings.TrimPrefix(err.Error(), "onesie: ") + ". The key is in " + path + " instead", nil
}

func replacedKey(settings rootSettings, previous creds.Entry, provider onesie.Provider, account string) replaced {
	if previousAccount(previous, provider) != account {
		return replaced{}
	}

	key, err := settings.keychain.Get(account)

	return replaced{sameAccount: true, key: key, found: err == nil}
}

type replaced struct {
	sameAccount bool
	key         string
	found       bool
}

func (r replaced) undo(keychain Keychain, account string) error {
	if !r.sameAccount {
		// The file still points wherever it pointed before, so the new item would be an orphan.
		return keychain.Delete(account)
	}

	// The file still points at this item, so it keeps a key either way.
	if !r.found {
		return nil
	}

	return keychain.Set(account, r.key)
}

func newAuthStatusCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report which source holds the API key",
		Long: "Report the provider and which source holds its API key, never the key itself.\n\n" +
			"When no key resolves it prints source: none and exits 2, as a real run does. On cloudflare it also " +
			"prints where the account comes from, and exits 2 when none does.",
		Args: authNoArgs("status", "It reports which source holds the key"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authStatus(cmd, settings, flags)
		},
	}
}

func authStatus(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	// No client is built. One would fail on a bad base URL, which says nothing about the question
	// being asked.
	// Located rather than resolved, so status never prompts for a keychain secret it only names.
	source, err := locateKey(settings, flags)
	if err != nil {
		return err
	}

	if _, printErr := fmt.Fprintf(cmd.OutOrStdout(), "provider: %s\n%s\n",
		source.provider.Name, source); printErr != nil {
		return printErr
	}

	account := accountSource(settings, source)
	if account != "" {
		if _, printErr := fmt.Fprintf(cmd.OutOrStdout(), "account: %s\n", account); printErr != nil {
			return printErr
		}
	}

	if source.name == sourceNone || account == sourceNone {
		return &silentError{code: ExitUsage}
	}

	return nil
}

func accountSource(settings rootSettings, source keySource) string {
	name := source.provider.EnvAccountID
	if name == "" {
		return ""
	}

	if value, ok := settings.lookupEnv(name); ok && strings.TrimSpace(value) != "" {
		return name
	}

	if source.baseURL != "" {
		return "the base URL in the credential file"
	}

	return sourceNone
}

func newAuthTestCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Call the models endpoint with the resolved key",
		Long: "Call the models endpoint with the resolved key, which costs no tokens.\n\n" +
			"Berget lists its models to any key, so on berget auth test also asks one small question " +
			"with the default model, which spends a few tokens.",
		Args: authNoArgs("test", "It calls the models endpoint with the resolved key"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authTest(cmd, settings, flags)
		},
	}
}

func authTest(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	source, err := resolveKey(settings, flags)
	if err != nil {
		return err
	}

	if source.name == sourceNone {
		return fmt.Errorf("onesie: no API key. Set %s or run onesie auth set", source.provider.EnvAPIKey)
	}

	// defaultClientFactory resolves the file itself, so this repeats a read on the production
	// path. It is passed anyway because an injected factory does no resolution of its own and
	// would otherwise be handed a client with no key.
	client, err := settings.newClient(cmd.Context(), storedOptions(settings, flags, source)...)
	if err != nil {
		return err
	}

	models, err := client.VerifyKey(cmd.Context())
	if err != nil {
		return advise(err, "", false)
	}

	out := cmd.OutOrStdout()
	if _, printErr := fmt.Fprintf(out, "provider: %s\n%s\n", source.provider.Name, source); printErr != nil {
		return printErr
	}

	_, printErr := fmt.Fprintf(out, "models: %d\n", len(models))

	return printErr
}

func storedOptions(settings rootSettings, flags *runFlags, source keySource) []onesie.Option {
	opts := []onesie.Option{onesie.WithAPIKey(source.key)}

	if source.baseURL == "" {
		return opts
	}

	// --base-url, the provider's base URL variable and its account variable stay ahead of the file's
	// own base URL, so the stored one is only passed when none of them has anything to say.
	if strings.TrimSpace(flags.baseURL) != "" {
		return opts
	}

	if name := source.provider.EnvBaseURL; name != "" {
		if value, ok := settings.lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return opts
		}
	}

	if name := source.provider.EnvAccountID; name != "" {
		if value, ok := settings.lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return opts
		}
	}

	return append(opts, onesie.WithBaseURL(source.baseURL))
}

func newAuthClearCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the provider's key from the credential file and the OS keychain",
		Args:  authNoArgs("clear", "It removes the provider's key"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authClear(cmd, settings, flags)
		},
	}
}

func authClear(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	chosen, err := runResolution(settings, flags)
	if err != nil {
		return err
	}

	provider := chosen.provider

	path, err := settings.credPath()
	if err != nil {
		return err
	}

	file, found, err := settings.credStore.Load(path)

	var exposed *creds.ReadableError
	if errors.As(err, &exposed) {
		return err
	}

	if err != nil {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: removed "+path+
			", which could not be read. Any keychain item it pointed at is left behind"); printErr != nil {
			return printErr
		}

		return settings.credStore.Clear(path)
	}

	entry, ok := file.Providers[provider.Name]
	if !found || !ok {
		return nil
	}

	// A keychain that cannot be reached must not trap the entry, so it goes either way.
	if account := previousAccount(entry, provider); account != "" {
		if deleteErr := settings.keychain.Delete(account); deleteErr != nil {
			_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+strings.TrimPrefix(deleteErr.Error(), "onesie: ")+
				". The keychain item "+account+" is left behind")
			if printErr != nil {
				return printErr
			}
		}
	}

	delete(file.Providers, provider.Name)

	if len(file.Providers) == 0 {
		return settings.credStore.Clear(path)
	}

	warning, err := settings.credStore.Save(path, file)
	if err != nil {
		return err
	}

	if warning != nil {
		return warning
	}

	return nil
}

func resolveKey(settings rootSettings, flags *runFlags) (keySource, error) {
	source, err := locateKey(settings, flags)
	if err != nil || source.name != sourceKeychain {
		return source, err
	}

	key, err := settings.keychain.Get(source.account)
	if err != nil {
		return keySource{}, err
	}

	source.key = key

	return source, nil
}

func locateKey(settings rootSettings, flags *runFlags) (keySource, error) {
	chosen, err := runResolution(settings, flags)
	if err != nil {
		return keySource{}, err
	}

	provider := chosen.provider

	// The provider's variable outranks the credential file, which is read only when it is unset.
	if value, ok := settings.lookupEnv(provider.EnvAPIKey); ok && strings.TrimSpace(value) != "" {
		return keySource{name: sourceEnv, provider: provider, key: strings.TrimSpace(value)}, nil
	}

	path, err := settings.credPath()
	if err != nil {
		return keySource{}, err
	}

	file, found, err := settings.credStore.Load(path)
	if err != nil {
		return keySource{}, err
	}

	entry, ok := file.Providers[provider.Name]
	if !found || !ok {
		return keySource{name: sourceNone, provider: provider}, nil
	}

	if account := previousAccount(entry, provider); account != "" {
		return keySource{
			name: sourceKeychain, provider: provider, path: path, account: account, baseURL: entry.BaseURL,
		}, nil
	}

	return keySource{
		name: sourceFile, provider: provider, path: path, key: entry.APIKey, baseURL: entry.BaseURL,
	}, nil
}

func previousAccount(entry creds.Entry, provider onesie.Provider) string {
	if entry.Store != creds.StoreKeychain {
		return ""
	}

	if entry.Account == "" {
		return provider.Name
	}

	return entry.Account
}

type keySource struct {
	name     string
	provider onesie.Provider
	path     string
	account  string
	key      string // Never printed. Callers format String, so a stray %v shows the source.
	baseURL  string
}

func (s keySource) String() string {
	switch s.name {
	case sourceFile:
		return "source: " + sourceFile + " " + s.path
	case sourceEnv:
		return "source: " + sourceEnv + " " + s.provider.EnvAPIKey
	}

	return "source: " + s.name
}

func authNoArgs(name, clause string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		return errors.New("onesie: auth " + name + " takes no question or state. " + clause)
	}
}
