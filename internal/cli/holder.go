package cli

import (
	"io"
	"strings"
	"sync"

	"github.com/frodi-karlsson/onesie-go"
)

func runResolution(settings rootSettings, flags *runFlags) (resolution, error) {
	if settings.holder == nil {
		return resolveRun(settings, flags)
	}

	return settings.holder.resolved()
}

func newHolder(settings rootSettings, flags *runFlags, stderr func() io.Writer) *holder {
	return &holder{
		stderr: stderr,
		resolved: sync.OnceValues(func() (resolution, error) {
			return resolveRun(settings, flags)
		}),
	}
}

type holder struct {
	stderr   func() io.Writer
	resolved func() (resolution, error)
}

func resolveRun(settings rootSettings, flags *runFlags) (resolution, error) {
	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return resolution{}, err
	}

	return resolution{provider: provider}, nil
}

func resolveProvider(settings rootSettings, flags *runFlags) (onesie.Provider, error) {
	if name := strings.TrimSpace(flags.provider); name != "" {
		return onesie.ProviderNamed(name)
	}

	value, _ := settings.lookupEnv(envProvider)

	return onesie.ProviderNamed(value)
}
