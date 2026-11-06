package cli

import (
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const (
	envCache       = "ONESIE_CACHE"
	envCacheTTL    = "ONESIE_CACHE_TTL"
	envLongContext = "ONESIE_LONG_CONTEXT"
)

func checkEnvSettings(lookupEnv func(string) (string, bool), names ...settingrow.Name) error {
	rows := make([]settingrow.Row, 0, len(names))
	for _, name := range names {
		row, _ := settingrow.Lookup(name)
		rows = append(rows, row)
	}
	_, err := setting.Read(rows, setting.Sources{LookupEnv: lookupEnv})
	return err
}
