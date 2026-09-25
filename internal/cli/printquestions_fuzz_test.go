package cli

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
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
		checkPrintRoundTrip(t, data)
	})
}

func FuzzPrintQuestionsJSON(f *testing.F) {
	for _, doc := range unitTestDocs(f, filepath.Join("..", "qfile")) {
		if json.Valid([]byte(doc)) {
			f.Add([]byte(doc))
		}
	}

	f.Add([]byte(`{"questions":{"u":{"type":"noul","instructions":{"n":8e13}}},"state":"x"}`))
	f.Add([]byte(`{"urgent":{"ask":{"big":123456789012345678901234567890,"f":1.50},"threshold":1e-7}}`))
	f.Add([]byte(`{"team":{"ask":"who","pick":{"a":-0.0,"b":[1e2,"1e2"]},"min_confidence":5E-1}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}

		loaded, err := qfile.Load(data)
		if err != nil {
			return
		}

		checkAgainstJSON(t, data, loaded)
		checkPrintRoundTrip(t, data)
	})
}

func checkAgainstJSON(t *testing.T, data []byte, loaded *qfile.File) {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var top map[string]any
	if err := decoder.Decode(&top); err != nil {
		t.Fatalf("Load accepted %s, which encoding/json reads as no object: %v", data, err)
	}

	questions := top
	if loaded.IsBody {
		questions, _ = top["questions"].(map[string]any)

		if state, hasState := top["state"]; hasState != loaded.HasState || hasState && !sameJSON(encoded(t, state), loaded.StateWire) {
			t.Fatalf("Load read the state of %s as %s", data, loaded.StateWire)
		}
	}

	for _, question := range loaded.Questions {
		written, found := questions[question.ID]
		if !found {
			t.Fatalf("Load read a question %q that %s does not hold", question.ID, data)
		}

		fields, _ := written.(map[string]any)

		ask := written
		if fields != nil {
			ask = fields["ask"]
			if loaded.IsBody {
				ask = fields["instructions"]
			}
		}

		if !sameJSON(encoded(t, ask), encoded(t, question.Instructions)) {
			t.Fatalf("Load read the instructions of %q in %s as %s", question.ID, data, encoded(t, question.Instructions))
		}

		for key, got := range map[string]*float64{"threshold": question.Policy.Threshold, "min_confidence": question.Policy.MinConfidence} {
			number, isNumber := fields[key].(json.Number)
			if want, err := number.Float64(); isNumber && err == nil && (got == nil || *got != want) {
				t.Fatalf("Load read %s %s in %s as %v", key, number, data, got)
			}
		}
	}
}

func checkPrintRoundTrip(t *testing.T, data []byte) {
	t.Helper()

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

	// As JSON with numbers compared by value, since a description is sent as JSON and a file may
	// write 8e13 back as 8.0e+13 or 0 as another integer type.
	if wantJSON, gotJSON := encoded(t, want), encoded(t, got); !sameJSON(wantJSON, gotJSON) {
		t.Fatalf("--print-questions changed the questions\nwant %s\ngot  %s\nfrom\n%s\nwrote\n%s",
			wantJSON, gotJSON, data, printed)
	}
}

func sameJSON(a, b []byte) bool {
	left, leftOK := decodedNumbers(a)
	right, rightOK := decodedNumbers(b)

	return leftOK && rightOK && sameValue(left, right)
}

func decodedNumbers(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var value any

	return value, decoder.Decode(&value) == nil
}

func hugeExponent(numbers ...json.Number) bool {
	for _, number := range numbers {
		_, exponent, _ := strings.Cut(strings.ToLower(number.String()), "e")
		if value, err := strconv.Atoi(exponent); exponent != "" && (err != nil || value > 1000 || value < -1000) {
			return true
		}
	}

	return false
}

func sameValue(a, b any) bool {
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}

		for key, value := range left {
			if other, found := right[key]; !found || !sameValue(value, other) {
				return false
			}
		}

		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}

		for i := range left {
			if !sameValue(left[i], right[i]) {
				return false
			}
		}

		return true
	case json.Number:
		right, ok := b.(json.Number)
		if !ok || left == right {
			return ok
		}

		// big.Rat builds every digit an exponent asks for, so a huge one is compared as written.
		if len(left) > 400 || len(right) > 400 || strings.ContainsAny(string(left)+string(right), "eE") && hugeExponent(left, right) {
			return false
		}

		leftRat, leftOK := new(big.Rat).SetString(left.String())
		rightRat, rightOK := new(big.Rat).SetString(right.String())

		return leftOK && rightOK && leftRat.Cmp(rightRat) == 0
	default:
		return a == b
	}
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

	kept := []any{json.RawMessage(sent)}
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
