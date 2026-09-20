package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func newAskCmd(newClient clientFactory) *cobra.Command {
	var (
		state  string
		model  string
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask Jev one yes or no question about some state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := resolveState(cmd.InOrStdin(), state)
			if err != nil {
				return err
			}

			client, err := newClient(cmd.Context())
			if err != nil {
				return err
			}

			result, err := client.SystemOne(cmd.Context(), jev.Request{
				State:     text,
				Model:     model,
				Questions: map[string]jev.Question{"answer": jev.Noul{Instructions: args[0]}},
			})
			if err != nil {
				return err
			}

			return writeAnswer(cmd.OutOrStdout(), result, asJSON)
		},
	}

	cmd.Flags().StringVarP(&state, "state", "s", "", "state to evaluate, or - to read stdin")
	cmd.Flags().StringVarP(&model, "model", "m", "", "model override")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the answer as json")

	return cmd
}

func resolveState(stdin io.Reader, state string) (string, error) {
	if state != "-" {
		return state, nil
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("reading state from stdin: %w", err)
	}

	return string(data), nil
}

func writeAnswer(out io.Writer, result *jev.Result, asJSON bool) error {
	answer, err := result.Noul("answer")
	if err != nil {
		return err
	}

	if !asJSON {
		_, writeErr := fmt.Fprintf(out, "%.4f\n", answer.Noul)

		return writeErr
	}

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")

	return encoder.Encode(map[string]any{
		"noul":       answer.Noul,
		"model":      result.Model,
		"request_id": result.RequestID,
		"usage":      result.Usage,
	})
}

// clientFactory builds the API client. It is injected so tests supply one pointed at a stub
// server rather than steering the real constructor through flags.
type clientFactory func(ctx context.Context) (*jev.Client, error)

func defaultClientFactory(info BuildInfo) clientFactory {
	return func(context.Context) (*jev.Client, error) {
		return jev.New(jev.WithUserAgent("jev-cli/" + info.Version))
	}
}
