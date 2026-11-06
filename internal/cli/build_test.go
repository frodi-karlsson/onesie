package cli

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestGateOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		source  setting.Source
		spelled string
	}{
		{name: "should name the flag that supplied an invalid gate", source: setting.SourceFlag, spelled: "--assert"},
		{name: "should name the file key that supplied an invalid gate", source: setting.SourceFile, spelled: "assert"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			merged, _ := setting.Merge(settingrow.Table(), []setting.Layer{{Source: tc.source, Values: map[settingrow.Name]any{"assert": []string{"unknown"}}, Spelled: map[settingrow.Name]string{"assert": tc.spelled}}}, setting.Choice{})
			named := tc.spelled
			if tc.source == setting.SourceFile {
				named = "'" + named + "'"
			}
			_, err := gateOf("assert", merged, &plan.Plan{Questions: []plan.Question{{ID: "answer", Shape: plan.Noul}}})
			if err == nil || !strings.HasPrefix(err.Error(), named+":") {
				t.Fatalf("gateOf error = %v, want prefix %s:", err, named)
			}
		})
	}
}
