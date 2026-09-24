package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/jev"
)

const (
	// sourceFlag cannot be reached from auth status. Section 16.3 keeps --api-key root local, so
	// cobra rejects it on a subcommand in either position. It is here for the ordinary run path,
	// where the flag is reachable and outranks both later sources.
	sourceFlag     = "flag"
	sourceEnv      = "env"
	sourceFile     = "file"
	sourceKeychain = "keychain"
	sourceNone     = "none"

	envProvider = "ONESIE_PROVIDER"
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
		Args:  authNoArgs("set", "It reads the key from a prompt or stdin"),
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

type setOptions struct {
	baseURL    string
	hasBaseURL bool
	toFile     bool
}

func authSet(cmd *cobra.Command, settings rootSettings, flags *runFlags, opts setOptions) error {
	baseURL, hasBaseURL := opts.baseURL, opts.hasBaseURL

	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return err
	}

	// Ahead of the read, so a typo costs the user nothing and leaves no file behind. jev.New owns
	// the same check, and a second one here would be a second message to keep in step.
	if hasBaseURL {
		if invalid := jev.ValidateBaseURL(baseURL); invalid != nil {
			return invalid
		}
	}

	key, err := readKey(cmd, settings)
	if err != nil {
		return err
	}

	if key == "" {
		return errors.New("onesie: auth set: the key is empty")
	}

	path, err := settings.credPath()
	if err != nil {
		return err
	}

	// stderr rather than stdout. It is a notice and not output, and pass show x | onesie auth set > log
	// must not put a filesystem path in a log the user expected to stay empty.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "onesie: writing "+path); printErr != nil {
		return printErr
	}

	file, found, loadErr := settings.credStore.Load(path)
	if loadErr != nil || !found {
		file = creds.File{Providers: map[string]creds.Entry{}}
	}

	previous := file.Providers[provider.Name]

	entry, err := storeKey(cmd, settings, provider, key, path, opts.toFile)
	if err != nil {
		return err
	}

	if hasBaseURL {
		entry.BaseURL = baseURL
	}

	// A key moving from the keychain to the file would otherwise leave a stale copy behind.
	if previous.Store == creds.StoreKeychain && entry.Store != creds.StoreKeychain {
		if deleteErr := settings.keychain.Delete(provider.Name); deleteErr != nil {
			if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+deleteErr.Error()); printErr != nil {
				return printErr
			}
		}
	}

	file.Providers[provider.Name] = entry

	warning, err := settings.credStore.Save(path, file)
	if err != nil {
		return err
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

func storeKey(
	cmd *cobra.Command,
	settings rootSettings,
	provider jev.Provider,
	key, path string,
	toFile bool,
) (creds.Entry, error) {
	if toFile {
		return creds.Entry{APIKey: key}, nil
	}

	err := settings.keychain.Set(provider.Name, key)
	if err == nil {
		_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "onesie: stored the "+provider.Name+" key in the OS keychain")

		return creds.Entry{Store: creds.StoreKeychain}, printErr
	}

	_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+strings.TrimPrefix(err.Error(), "onesie: ")+
		". The key is in "+path+" instead")

	return creds.Entry{APIKey: key}, printErr
}

func readKey(cmd *cobra.Command, settings rootSettings) (string, error) {
	if !settings.stdinTTY {
		return firstLine(settings.stdin)
	}

	// The prompt goes to stderr so onesie auth set inside a pipeline does not corrupt what stdout
	// carries.
	if _, printErr := fmt.Fprint(cmd.ErrOrStderr(), "API key: "); printErr != nil {
		return "", printErr
	}

	secret, err := settings.readSecret()

	// Written whether or not the read succeeded, since the terminal echoed nothing and the cursor
	// is still on the prompt line.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr()); printErr != nil {
		return "", printErr
	}

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(secret), nil
}

func firstLine(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)

	key := ""

	if scanner.Scan() {
		key = strings.TrimSpace(scanner.Text())
	} else if err := scanner.Err(); err != nil {
		// Without this a key over the scanner's 64KiB cap leaves the first Scan empty and the loop
		// below hands back the remainder, which reads as a second line and is rejected as one.
		return "", fmt.Errorf("onesie: reading the key from stdin: %w", err)
	}

	// pass show and its siblings print the secret first and metadata after it, so a second non
	// blank line means the caller piped more than the key. Storing it would make the metadata part
	// of the key and truncating it in silence would hide the mistake.
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			return "", errors.New(
				"onesie: auth set: stdin carries more than one line. The key is the first line")
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("onesie: reading the key from stdin: %w", err)
	}

	return key, nil
}

func newAuthStatusCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report which source holds the API key",
		Args:  authNoArgs("status", "It reports which source holds the key"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authStatus(cmd, settings, flags)
		},
	}
}

func authStatus(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	// No client is built. One would fail on a bad base URL, which says nothing about the question
	// being asked.
	source, err := resolveKey(settings, flags)
	if err != nil {
		return err
	}

	if _, printErr := fmt.Fprintf(cmd.OutOrStdout(), "provider: %s\n%s\n",
		source.provider.Name, source); printErr != nil {
		return printErr
	}

	if source.name == sourceNone {
		return &silentError{code: ExitAuth}
	}

	return nil
}

func newAuthTestCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Call the models endpoint with the resolved key",
		Args:  authNoArgs("test", "It calls the models endpoint with the resolved key"),
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

	// defaultClientFactory resolves the file itself, so this repeats a read on the production
	// path. It is passed anyway because an injected factory does no resolution of its own and
	// would otherwise be handed a client with no key.
	client, err := settings.newClient(cmd.Context(), storedOptions(settings, flags, source)...)
	if err != nil {
		return err
	}

	// The same request --list-models makes, which costs no tokens.
	models, err := client.ListModels(cmd.Context())
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

func storedOptions(settings rootSettings, flags *runFlags, source keySource) []jev.Option {
	opts := []jev.Option{jev.WithAPIKey(source.key)}

	if source.baseURL == "" {
		return opts
	}

	// Section 16.2 keeps --base-url and TYPESAFE_BASE_URL ahead of the file's own base URL, so the
	// stored one is only passed when neither of them has anything to say.
	if strings.TrimSpace(flags.baseURL) != "" {
		return opts
	}

	if name := source.provider.EnvBaseURL; name != "" {
		if value, ok := settings.lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return opts
		}
	}

	return append(opts, jev.WithBaseURL(source.baseURL))
}

func newAuthClearCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the provider's key from the credential file",
		Args:  authNoArgs("clear", "It removes the provider's key from the credential file"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authClear(settings, flags)
		},
	}
}

func authClear(settings rootSettings, flags *runFlags) error {
	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return err
	}

	path, err := settings.credPath()
	if err != nil {
		return err
	}

	file, found, err := settings.credStore.Load(path)
	if err != nil {
		return settings.credStore.Clear(path)
	}

	entry, ok := file.Providers[provider.Name]
	if !found || !ok {
		return nil
	}

	if entry.Store == creds.StoreKeychain {
		if deleteErr := settings.keychain.Delete(provider.Name); deleteErr != nil {
			return deleteErr
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
	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return keySource{}, err
	}

	if key := strings.TrimSpace(flags.apiKey); key != "" {
		return keySource{name: sourceFlag, provider: provider, key: key}, nil
	}

	if value, ok := settings.lookupEnv(provider.EnvAPIKey); ok && strings.TrimSpace(value) != "" {
		return keySource{name: sourceEnv, provider: provider, key: strings.TrimSpace(value)}, nil
	}

	path, err := settings.credPath()
	if err != nil {
		return keySource{}, err
	}

	// Load returns a populated File beside a non nil error when only the close failed, so the error
	// is checked before found.
	file, found, err := settings.credStore.Load(path)
	if err != nil {
		return keySource{}, err
	}

	entry, ok := file.Providers[provider.Name]
	if !found || !ok {
		return keySource{name: sourceNone, provider: provider}, nil
	}

	if entry.Store == creds.StoreKeychain {
		key, keychainErr := settings.keychain.Get(provider.Name)
		if keychainErr != nil {
			return keySource{}, keychainErr
		}

		return keySource{
			name: sourceKeychain, provider: provider, path: path, key: key, baseURL: entry.BaseURL,
		}, nil
	}

	return keySource{
		name: sourceFile, provider: provider, path: path, key: entry.APIKey, baseURL: entry.BaseURL,
	}, nil
}

func resolveProvider(settings rootSettings, flags *runFlags) (jev.Provider, error) {
	if name := strings.TrimSpace(flags.provider); name != "" {
		return jev.ProviderNamed(name)
	}

	value, _ := settings.lookupEnv(envProvider)

	return jev.ProviderNamed(value)
}

type keySource struct {
	name     string
	provider jev.Provider
	path     string
	// key is never printed. String is what every caller formats, so a stray %v of a keySource
	// reports the source rather than the value it carries.
	key     string
	baseURL string
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
