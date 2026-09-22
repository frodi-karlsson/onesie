package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

const (
	// sourceFlag cannot be reached from auth status. Section 16.3 keeps --api-key root local, so
	// cobra rejects it on a subcommand in either position. It is here for the ordinary run path,
	// where the flag is reachable and outranks both later sources.
	sourceFlag = "flag"
	sourceEnv  = "env"
	sourceFile = "file"
	sourceNone = "none"
)

func newAuthCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store, inspect and remove the API key jev falls back to",
		Args:  cobra.NoArgs,
		// Set, so cobra validates the arguments before deciding the command is not runnable. A
		// parent with no RunE answers jev auth nonsense with its own help and exit 0, which is the
		// silence this guard exists to prevent. Measured against cobra 1.10.2.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAuthSetCmd(settings))
	cmd.AddCommand(newAuthStatusCmd(settings, flags))
	cmd.AddCommand(newAuthTestCmd(settings, flags))
	cmd.AddCommand(newAuthClearCmd(settings))

	return cmd
}

func newAuthSetCmd(settings rootSettings) *cobra.Command {
	baseURL := ""

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Read a key from a prompt or stdin and store it",
		Args:  authNoArgs("set", "It reads the key from a prompt or stdin"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authSet(cmd, settings, baseURL, cmd.Flags().Changed("base-url"))
		},
	}

	cmd.Flags().StringVar(&baseURL, "base-url", "", "api root stored beside the key")

	return cmd
}

func authSet(cmd *cobra.Command, settings rootSettings, baseURL string, hasBaseURL bool) error {
	// Ahead of the read, so a typo costs the user nothing and leaves no file behind. jev.New owns
	// the same check, and a second one here would be a second message to keep in step.
	if hasBaseURL {
		if err := jev.ValidateBaseURL(baseURL); err != nil {
			return err
		}
	}

	key, err := readKey(cmd, settings)
	if err != nil {
		return err
	}

	if key == "" {
		return errors.New("jev: auth set: the key is empty")
	}

	path, err := settings.credPath()
	if err != nil {
		return err
	}

	// stderr rather than stdout. It is a notice and not output, and pass show x | jev auth set > log
	// must not put a filesystem path in a log the user expected to stay empty.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "jev: writing "+path); printErr != nil {
		return printErr
	}

	file := creds.File{APIKey: key}
	if hasBaseURL {
		file.BaseURL = baseURL
	}

	warning, err := settings.credStore.Save(path, file)
	if err != nil {
		return err
	}

	if warning != nil {
		_, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "jev: "+warning.Error())

		return printErr
	}

	return nil
}

func readKey(cmd *cobra.Command, settings rootSettings) (string, error) {
	if !settings.stdinTTY {
		return firstLine(settings.stdin)
	}

	// The prompt goes to stderr so jev auth set inside a pipeline does not corrupt what stdout
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
	}

	// pass show and its siblings print the secret first and metadata after it, so a second non
	// blank line means the caller piped more than the key. Storing it would make the metadata part
	// of the key and truncating it in silence would hide the mistake.
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			return "", errors.New(
				"jev: auth set: stdin carries more than one line. The key is the first line")
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("jev: reading the key from stdin: %w", err)
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

	if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), source); printErr != nil {
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

	client, err := settings.newClient(cmd.Context(), storedOptions(settings, flags, source)...)
	if err != nil {
		return err
	}

	// The same request --list-models makes, which costs no tokens.
	models, err := client.ListModels(cmd.Context())
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if _, printErr := fmt.Fprintln(out, source); printErr != nil {
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

	if value, ok := settings.lookupEnv(jev.EnvBaseURL); ok && strings.TrimSpace(value) != "" {
		return opts
	}

	return append(opts, jev.WithBaseURL(source.baseURL))
}

func newAuthClearCmd(settings rootSettings) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete the credential file",
		Args:  authNoArgs("clear", "It deletes the credential file"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authClear(settings)
		},
	}
}

func authClear(settings rootSettings) error {
	path, err := settings.credPath()
	if err != nil {
		return err
	}

	return settings.credStore.Clear(path)
}

func resolveKey(settings rootSettings, flags *runFlags) (keySource, error) {
	if key := strings.TrimSpace(flags.apiKey); key != "" {
		return keySource{name: sourceFlag, key: key}, nil
	}

	if value, ok := settings.lookupEnv(jev.EnvAPIKey); ok && strings.TrimSpace(value) != "" {
		return keySource{name: sourceEnv, key: strings.TrimSpace(value)}, nil
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

	if !found {
		return keySource{name: sourceNone}, nil
	}

	return keySource{name: sourceFile, path: path, key: file.APIKey, baseURL: file.BaseURL}, nil
}

type keySource struct {
	name string
	path string
	// key is never printed. String is what every caller formats, so a stray %v of a keySource
	// reports the source rather than the value it carries.
	key     string
	baseURL string
}

func (s keySource) String() string {
	if s.name == sourceFile {
		return "source: " + sourceFile + " " + s.path
	}

	return "source: " + s.name
}

func authNoArgs(name, clause string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}

		return errors.New("jev: auth " + name + " takes no question or state. " + clause)
	}
}
