package qfile_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

var (
	update          = flag.Bool("update", false, "rewrite schema/questions.json from qfile.Schema")
	updateStructure = flag.Bool("update-structure", false,
		"rewrite testdata/schema_structure.json from qfile.Schema, for a structure change made on purpose")
)

func TestSchema(t *testing.T) {
	t.Parallel()

	committed := filepath.Join("..", "..", "schema", "questions.json")
	pinned := filepath.Join("testdata", "schema_structure.json")

	if *update {
		if err := os.WriteFile(committed, qfile.Schema(), 0o600); err != nil {
			t.Fatalf("writing %s: %v", committed, err)
		}
	}

	if *updateStructure {
		if err := os.WriteFile(pinned, structure(t, qfile.Schema()), 0o600); err != nil {
			t.Fatalf("writing %s: %v", pinned, err)
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
			name: "should give every not an error message an editor shows",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				walk(schema, func(node map[string]any) {
					if _, negated := node["not"]; negated && node["errorMessage"] == nil {
						t.Errorf("a not carries no errorMessage: %v", node)
					}
				})
			},
		},
		{
			name: "should give every pattern an error message an editor shows",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				walk(schema, func(node map[string]any) {
					if _, patterned := node["pattern"]; patterned && node["patternErrorMessage"] == nil {
						t.Errorf("a pattern carries no patternErrorMessage: %v", node)
					}
				})
			},
		},
		{
			name: "should put no description beside a $ref",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				walk(schema, func(node map[string]any) {
					_, ref := node["$ref"]
					if _, described := node["description"]; ref && described {
						t.Errorf("a $ref carries a description, which an editor drops: %v", node)
					}
				})
			},
		},
		{
			name: "should suggest the yes/no fallbacks",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				want := `[true,false,"yes","no"]`
				found := false

				walk(schema, func(node map[string]any) {
					if examples, err := json.Marshal(node["examples"]); err == nil && string(examples) == want {
						found = true
					}
				})

				if !found {
					t.Errorf("no node suggests %s", want)
				}
			},
		},
		{
			name: "should take every question row's file key",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				fields, _ := definition(t, schema, "fields")["properties"].(map[string]any)
				otherwise, _ := schema["else"].(map[string]any)
				top, _ := otherwise["properties"].(map[string]any)

				for _, row := range settingrow.Table() {
					_, inFields := fields[row.FileKey]
					_, atTop := top[row.FileKey]

					switch {
					case row.FileKey == "":
					case row.Scope == settingrow.ScopeQuestion && !inFields:
						t.Errorf("%s is a question row, and fields has no %s", row.Name, row.FileKey)
					case row.Scope == settingrow.ScopeRun && row.InBody && atTop:
						t.Errorf("%s is a request body key, and a question file takes it at the top level", row.FileKey)
					case row.Scope == settingrow.ScopeRun && !row.InBody && !atTop:
						t.Errorf("%s is a run row, and the top level has no %s", row.Name, row.FileKey)
					}
				}
			},
		},
		{
			name: "should point every $ref at a definition",
			check: func(t *testing.T, schema map[string]any) {
				t.Helper()

				definitions, _ := schema["definitions"].(map[string]any)

				walk(schema, func(node map[string]any) {
					ref, ok := node["$ref"].(string)
					if _, found := definitions[strings.TrimPrefix(ref, "#/definitions/")]; ok && !found {
						t.Errorf("$ref %s names no definition", ref)
					}
				})
			},
		},
		{
			name: "should keep every key, type and constraint of the pinned structure",
			check: func(t *testing.T, _ map[string]any) {
				t.Helper()

				want, err := os.ReadFile(pinned)
				if err != nil {
					t.Fatalf("reading %s: %v", pinned, err)
				}

				if got := structure(t, qfile.Schema()); !bytes.Equal(got, want) {
					t.Errorf("the schema's structure moved from %s, or run go test ./internal/qfile/ -run TestSchema "+
						"-update-structure for a change made on purpose. Got:\n%s", pinned, got)
				}
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
					t.Errorf("%s is out of date, run make settings", committed)
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

func structure(t *testing.T, schema []byte) []byte {
	t.Helper()

	var decoded any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("the schema is not valid json: %v", err)
	}

	walk(decoded, func(node map[string]any) {
		delete(node, "description")
		delete(node, "$comment")
	})

	encoded, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatalf("encoding the structure: %v", err)
	}

	return append(encoded, '\n')
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

func walk(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)

		for _, child := range typed {
			walk(child, visit)
		}
	case []any:
		for _, child := range typed {
			walk(child, visit)
		}
	}
}
