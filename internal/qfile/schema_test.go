package qfile_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

var update = flag.Bool("update", false, "rewrite schema/questions.json from qfile.Schema")

func TestSchema(t *testing.T) {
	t.Parallel()

	committed := filepath.Join("..", "..", "schema", "questions.json")

	if *update {
		if err := os.WriteFile(committed, qfile.Schema(), 0o600); err != nil {
			t.Fatalf("writing %s: %v", committed, err)
		}
	}

	tests := []struct {
		name  string
		check func(t *testing.T, schema map[string]any)
	}{
		{
			name: "should declare draft 07",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				if got := schema["$schema"]; got != "http://json-schema.org/draft-07/schema#" {
					t.Errorf("$schema = %v, want draft 07", got)
				}
			},
		},
		{
			name: "should take the pick option counts from the limits",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				checkBounds(t, definition(t, schema, "pick"), limits.MinChoiceOptions, limits.MaxChoiceOptions)
			},
		},
		{
			name: "should take the rate level counts from the limits",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				checkBounds(t, definition(t, schema, "rate"), limits.MinScoreLevels, limits.MaxScoreLevels)
			},
		},
		{
			name: "should match the committed copy byte for byte",
			check: func(t *testing.T, _ map[string]any) {
				t.Helper()

				want, err := os.ReadFile(committed)
				if err != nil {
					t.Fatalf("reading %s: %v", committed, err)
				}

				if !bytes.Equal(qfile.Schema(), want) {
					t.Errorf("%s is out of date, run go test ./internal/qfile/ -run TestSchema -update", committed)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var schema map[string]any
			if err := json.Unmarshal(qfile.Schema(), &schema); err != nil {
				t.Fatalf("the schema is not valid json: %v", err)
			}

			tc.check(t, schema)
		})
	}
}

func definition(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()

	definitions, _ := schema["definitions"].(map[string]any)

	found, ok := definitions[name].(map[string]any)
	if !ok {
		t.Fatalf("the schema has no definition %q", name)
	}

	return found
}

func checkBounds(t *testing.T, shape map[string]any, least, most int) {
	t.Helper()

	forms, _ := shape["oneOf"].([]any)
	if len(forms) != 2 {
		t.Fatalf("want a sequence form and a mapping form, got %v", shape)
	}

	for _, form := range forms {
		fields, _ := form.(map[string]any)

		lower, upper := "minItems", "maxItems"
		if fields["type"] == "object" {
			lower, upper = "minProperties", "maxProperties"
		}

		if fields[lower] != float64(least) || fields[upper] != float64(most) {
			t.Errorf("%s %v and %s %v, want %d and %d in %v",
				lower, fields[lower], upper, fields[upper], least, most, fields)
		}
	}
}
