package cli

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func FuzzPrintQuestions(f *testing.F) {
	for _, doc := range unitTestDocs(f, filepath.Join("..", "qfile")) {
		f.Add([]byte(doc))
	}

	f.Add([]byte(`{"questions":{"frustration":{"type":"score",` +
		`"instructions":"how cross is the writer","criteria":["calm","annoyed","furious"]}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		loaded, err := qfile.Load(data)
		if err != nil {
			return
		}

		printed, ok := printedQuestions(t, data)
		if !ok {
			return
		}

		reprinted, ok := printedQuestions(t, printed)
		if !ok {
			t.Fatalf("--print-questions wrote a file it refuses to load\nfrom\n%s\nwrote\n%s", data, printed)
		}

		if !bytes.Equal(printed, reprinted) {
			t.Fatalf("--print-questions changed its own file\nfirst\n%s\nsecond\n%s", printed, reprinted)
		}

		reloaded, err := qfile.Load(printed)
		if err != nil {
			t.Fatalf("Load refused the printed file: %v\n%s", err, printed)
		}

		if reloaded.Assert != loaded.Assert || reloaded.AbstainIf != loaded.AbstainIf {
			t.Fatalf("--print-questions changed the gate from %q, %q to %q, %q\nfrom\n%s\nwrote\n%s",
				loaded.Assert, loaded.AbstainIf, reloaded.Assert, reloaded.AbstainIf, data, printed)
		}

		var want, got any = asAuthored(loaded.Questions), asAuthored(reloaded.Questions)

		// A body's levels carry no labels, and the printed file labels each by its index, which is
		// what the API answers with. So a body keeps what it sends and its policy, not its labels.
		if loaded.IsBody {
			want, got = asSent(t, loaded.Questions), asSent(t, reloaded.Questions)
		}

		// As JSON, since a description is sent as JSON and YAML may read 0 back as another integer type.
		if wantJSON, gotJSON := encoded(t, want), encoded(t, got); !bytes.Equal(wantJSON, gotJSON) {
			t.Fatalf("--print-questions changed the questions\nwant %s\ngot  %s\nfrom\n%s\nwrote\n%s",
				wantJSON, gotJSON, data, printed)
		}
	})
}

func encoded(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %#v: %v", value, err)
	}

	return encoded
}

func asSent(t *testing.T, questions []plan.Question) []any {
	t.Helper()

	sent, err := jev.MarshalQuestionsBody(jev.Request{Questions: wireAll(questions)})
	if err != nil {
		t.Fatalf("encoding the questions: %v", err)
	}

	kept := []any{string(sent)}
	for _, question := range questions {
		kept = append(kept, question.Policy)
	}

	return kept
}

func printedQuestions(t *testing.T, data []byte) ([]byte, bool) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithReadFile(func(path string) ([]byte, error) {
			if path == "questions.yaml" {
				return data, nil
			}

			return nil, fs.ErrNotExist
		}),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": filepath.Join("no", "config")})),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"-f", "questions.yaml", "--print-questions"})

	code := Execute(t.Context(), root)

	return out.Bytes(), code == ExitOK
}

func asAuthored(questions []plan.Question) []plan.Question {
	kept := make([]plan.Question, 0, len(questions))

	for _, question := range questions {
		// How a question was authored is not part of it, and a file cannot carry it.
		question.Origin = plan.OriginPositional
		question.DescOrder = nil
		question.UnknownDesc = nil

		kept = append(kept, question)
	}

	return kept
}

func unitTestDocs(f *testing.F, dir string) []string {
	f.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		f.Fatalf("listing the unit tests: %v", err)
	}

	var docs []string

	for _, file := range files {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if parseErr != nil {
			f.Fatalf("parsing %s: %v", file, parseErr)
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			field, isField := node.(*ast.KeyValueExpr)
			if !isField {
				return true
			}

			if key, isIdent := field.Key.(*ast.Ident); isIdent && key.Name == "doc" {
				if doc, isText := stringOf(field.Value); isText {
					docs = append(docs, doc)
				}
			}

			return true
		})
	}

	return docs
}

func stringOf(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		text, err := strconv.Unquote(typed.Value)

		return text, err == nil && typed.Kind == token.STRING
	case *ast.BinaryExpr:
		left, leftOK := stringOf(typed.X)
		right, rightOK := stringOf(typed.Y)

		return left + right, leftOK && rightOK && typed.Op == token.ADD
	default:
		return "", false
	}
}
