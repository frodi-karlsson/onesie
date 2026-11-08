package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestHelpTopics(t *testing.T) {
	t.Parallel()

	variables := map[string]bool{}
	for _, row := range settingrow.Table() {
		for _, variable := range row.Env {
			variables[variable.Name] = true
		}
	}

	flagName := regexp.MustCompile(`--[a-z][a-z-]*`)
	variableName := regexp.MustCompile(`[A-Z][A-Z0-9]*_[A-Z0-9_]+`)

	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		for _, topic := range helpTopics(command) {
			t.Run("should name only real flags and variables in "+command.String()+" "+topic.name, func(t *testing.T) {
				t.Parallel()

				cmd := helpTestCommand(t, command)
				var page bytes.Buffer
				if err := writeHelp(&page, cmd, command, topic.name); err != nil {
					t.Fatal(err)
				}

				for _, flag := range flagName.FindAllString(page.String(), -1) {
					if cmd.Flags().Lookup(strings.TrimPrefix(flag, "--")) == nil {
						t.Errorf("%s names %s, which %s does not register", topic.name, flag, command)
					}
				}

				for _, variable := range variableName.FindAllString(page.String(), -1) {
					if !variables[variable] {
						t.Errorf("%s names %s, which no settings row reads", topic.name, variable)
					}
				}
			})
		}
	}
}
