package qfile

import (
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestCheckKeys(t *testing.T) {
	t.Run("should accept every question row file key", func(t *testing.T) {
		for _, row := range settingrow.Table() {
			if row.Scope != settingrow.ScopeQuestion || row.FileKey == "" || row.InBody {
				continue
			}
			if err := checkKeys("q", yaml.MapSlice{{Key: row.FileKey}}); err != nil {
				t.Error(err)
			}
		}
	})
	t.Run("should reject run row keys inside a question", func(t *testing.T) {
		for _, row := range settingrow.Table() {
			if row.Scope != settingrow.ScopeRun || row.FileKey == "" {
				continue
			}
			if err := checkKeys("q", yaml.MapSlice{{Key: row.FileKey}}); err == nil {
				t.Errorf("accepted %s", row.FileKey)
			}
		}
	})
}
