package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/interrupt"
)

func readKey(cmd *cobra.Command, settings rootSettings, command string) (string, error) {
	key, err := readSecretLine(cmd, settings, command)
	if err != nil {
		return "", err
	}

	if key == "" {
		return "", fmt.Errorf("%s: the key is empty", command)
	}

	return key, nil
}

func readSecretLine(cmd *cobra.Command, settings rootSettings, command string) (string, error) {
	if !settings.stdinTTY {
		ctx := cmd.Context()

		key, err := interrupt.Wait(ctx, func() (string, error) { return firstLine(settings.stdin, command) })
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		return key, err
	}

	// The prompt goes to stderr so a key read inside a pipeline does not corrupt what stdout carries.
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

func firstLine(r io.Reader, command string) (string, error) {
	scanner := bufio.NewScanner(r)

	key := ""

	if scanner.Scan() {
		key = strings.TrimSpace(scanner.Text())
	} else if err := scanner.Err(); err != nil {
		// Without this a key over the scanner's 64KiB cap leaves the first Scan empty and the loop
		// below hands back the remainder, which reads as a second line and is rejected as one.
		return "", fmt.Errorf("reading the key from stdin: %w", err)
	}

	// pass show and its siblings print the secret first and metadata after it, so a second non
	// blank line means the caller piped more than the key. Storing it would make the metadata part
	// of the key and truncating it in silence would hide the mistake.
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			return "", errors.New(command + ": stdin carries more than one line, and it must " +
				"hold the key alone. Pipe a password manager entry through head -n 1")
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading the key from stdin: %w", err)
	}

	return key, nil
}
