package qfile_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestLoadBody(t *testing.T) {
	t.Parallel()

	const full = `{
	  "state": "a ticket body",
	  "model": "onesie-1.13.0",
	  "questions": {
	    "urgent": {"type": "noul", "instructions": "is this urgent",
	               "criteria": {"true": "shouting", "false": "calm"}},
	    "team": {"type": "choice", "instructions": "which team",
	             "criteria": {"billing": "money", "technical": null}},
	    "mood": {"type": "score", "instructions": "how cross",
	             "criteria": ["calm", "annoyed", "furious"]}
	  }
	}`

	square := plainPNG(t)
	asset := func(format, data string) string {
		return `{"state":{"photo":"img"},"questions":{"q":{"type":"noul","instructions":"x"}},` +
			`"assets":{"img":{"type":"image","format":"` + format + `","data_base64":"` + data + `"}}}`
	}

	tests := []struct {
		name    string
		doc     string
		wantErr string
		check   func(t *testing.T, f *qfile.File)
	}{
		{
			name: "should read a body's long_context",
			doc:  `{"questions":{"q":{"type":"noul","instructions":"x"}},"long_context":true}`,
			check: func(t *testing.T, file *qfile.File) {
				t.Helper()

				if !file.LongContext {
					t.Error("long context = false, want true")
				}
			},
		},
		{
			name:    "should refuse a long_context that is not true or false",
			doc:     `{"questions":{"q":{"type":"noul","instructions":"x"}},"long_context":"yes"}`,
			wantErr: "'long_context' in a request body must be true or false",
		},
		{
			name: "should keep a body's assets and the sum of each file",
			doc:  asset("png", base64.StdEncoding.EncodeToString(square)),
			check: func(t *testing.T, file *qfile.File) {
				t.Helper()

				if got := file.Assets["img"]; got.Format() != "png" || file.AssetSums["img"] != sha256.Sum256(square) {
					t.Errorf("assets = %+v and sums %x, want img as the png and its sum", file.Assets, file.AssetSums)
				}
			},
		},
		{
			name: "should take an asset's format from its bytes, not from the body",
			doc:  asset("jpg", base64.StdEncoding.EncodeToString(square)),
			check: func(t *testing.T, file *qfile.File) {
				t.Helper()

				if got := file.Assets["img"].Format(); got != "png" {
					t.Errorf("format = %q, want png", got)
				}
			},
		},
		{
			name: "should take an asset that declares no format",
			doc: `{"state":{"photo":"img"},"questions":{"q":{"type":"noul","instructions":"x"}},` +
				`"assets":{"img":{"data_base64":"` + base64.StdEncoding.EncodeToString(square) + `"}}}`,
			check: func(t *testing.T, file *qfile.File) {
				t.Helper()

				if got := file.Assets["img"].Format(); got != "png" {
					t.Errorf("format = %q, want png", got)
				}
			},
		},
		{
			name:    "should refuse an asset that is not an image",
			doc:     asset("png", "AQ=="),
			wantErr: "asset 'img' in a request body: an image must be PNG or JPEG",
		},
		{
			name:    "should refuse an asset that is not base64",
			doc:     `{"questions":{"q":{"type":"noul","instructions":"x"}},"assets":{"img":{"format":"png","data_base64":"%%"}}}`,
			wantErr: "asset 'img' in a request body",
		},
		{
			name: "should report itself as a body",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.IsBody {
					t.Error("a file with a top level questions key is a body")
				}

				if f.Model != "onesie-1.13.0" {
					t.Errorf("model = %q", f.Model)
				}

				if !f.HasState || f.State != "a ticket body" {
					t.Errorf("state = %v, hasState = %v", f.State, f.HasState)
				}
			},
		},
		{
			name: "should keep body questions in file order",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				want := []string{"urgent", "team", "mood"}
				for i, q := range f.Questions {
					if q.ID != want[i] {
						t.Errorf("question %d = %s, want %s", i, q.ID, want[i])
					}
				}
			},
		},
		{
			name: "should map the three wire types onto shapes",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				shapes := []plan.Shape{plan.Noul, plan.Pick, plan.Rate}
				for i, want := range shapes {
					if f.Questions[i].Shape != want {
						t.Errorf("question %d shape = %s, want %s",
							i, f.Questions[i].Shape, want)
					}
				}
			},
		},
		{
			name: "should leave a body score question unlabelled",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				mood := f.Questions[2]
				if mood.Labelled {
					t.Error("a body score question has no labels and must not be Labelled")
				}

				if len(mood.Levels) != 3 {
					t.Fatalf("levels = %d, want 3", len(mood.Levels))
				}

				for _, level := range mood.Levels {
					if level.Label != "" {
						t.Errorf("a body level must carry no label, got %q", level.Label)
					}
				}

				if mood.Levels[0].Desc != "calm" {
					t.Errorf("level 0 desc = %v, want calm", mood.Levels[0].Desc)
				}
			},
		},
		{
			name: "should read choice criteria as ordered options",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				team := f.Questions[1]
				if team.Options[0].Name != "billing" || team.Options[1].Name != "technical" {
					t.Errorf("options = %+v, want billing then technical", team.Options)
				}

				if team.Options[1].Desc != nil {
					t.Errorf("a null criteria must stay nil, got %v", team.Options[1].Desc)
				}
			},
		},
		{
			name: "should read noul criteria into the yes and no rubric",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				urgent := f.Questions[0]
				if urgent.Criteria == nil || urgent.Criteria.Yes != "shouting" {
					t.Errorf("criteria = %+v", urgent.Criteria)
				}
			},
		},
		{
			name: "should carry no state when the body omits one",
			doc:  `{"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.HasState {
					t.Error("a body with no state key must not report one")
				}
			},
		},
		{
			name: "should carry an explicit null state as present",
			doc:  `{"state":null,"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.HasState {
					t.Error("an explicit null state is present, not absent")
				}
			},
		},
		{
			name: "should load a body written as yaml",
			doc: "state: a ticket\nquestions:\n  a:\n    type: noul\n" +
				"    instructions: is this urgent\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.IsBody || f.Questions[0].ID != "a" {
					t.Errorf("a body is a body whether written as json or yaml, got %+v", f)
				}
			},
		},
		{
			name: "should leave an empty noul criteria unset",
			doc:  `{"questions":{"a":{"type":"noul","instructions":"q","criteria":{}}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.Questions[0].Criteria != nil {
					t.Errorf("criteria = %+v, want nil for a body that named neither key",
						f.Questions[0].Criteria)
				}
			},
		},
		{
			name: "should keep a noul criteria that names only one key",
			doc:  `{"questions":{"a":{"type":"noul","instructions":"q","criteria":{"true":"y"}}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.Questions[0].Criteria == nil || f.Questions[0].Criteria.Yes != "y" {
					t.Errorf("criteria = %+v, want the true key kept", f.Questions[0].Criteria)
				}
			},
		},
		{
			name:    "should refuse a body question with no instructions",
			doc:     `{"questions":{"a":{"type":"noul"}}}`,
			wantErr: "question 'a' in a request body has no 'instructions'",
		},
		{
			name:    "should refuse a question key the wire format does not name",
			doc:     `{"questions":{"a":{"type":"noul","instructions":"q","rubric":{"true":"y"}}}}`,
			wantErr: "question 'a' has an unknown key 'rubric'. A question takes type, instructions and criteria",
		},
		{
			name: "should refuse a top level key it would not send",
			doc:  `{"stat":{"n":3},"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			wantErr: "a request body has an unknown key 'stat'. A request body takes state, model, questions, " +
				"long_context and assets",
		},
		{
			name: "should read unquoted true and false criteria keys in a yaml body",
			doc:  "questions:\n  a:\n    type: noul\n    instructions: q\n    criteria: {true: y, false: n}\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if got := f.Questions[0].Criteria; got == nil || got.Yes != "y" || got.No != "n" {
					t.Errorf("criteria = %+v, want yes y and no n", got)
				}
			},
		},
		{
			name:    "should refuse a noul criteria that is not a mapping",
			doc:     `{"questions":{"a":{"type":"noul","criteria":"shouting"}}}`,
			wantErr: "noul question 'a' needs criteria as an object, or none",
		},
		{
			name:    "should refuse a noul criteria sequence",
			doc:     `{"questions":{"a":{"type":"noul","criteria":["y","n"]}}}`,
			wantErr: "noul question 'a' needs criteria as an object, or none",
		},
		{
			name:    "should refuse a choice criteria sequence",
			doc:     `{"questions":{"a":{"type":"choice","instructions":"q","criteria":["y","n"]}}}`,
			wantErr: "choice question 'a' needs criteria as an object",
		},
		{
			name:    "should refuse a score criteria mapping",
			doc:     `{"questions":{"a":{"type":"score","instructions":"q","criteria":{"y":"n"}}}}`,
			wantErr: "score question 'a' needs criteria as an array",
		},
		{
			name:    "should name the question that holds a value JSON cannot carry",
			doc:     "questions: {a: {type: noul, instructions: .nan}}",
			wantErr: "question 'a' in a request body cannot be sent: ",
		},
		{
			name:    "should refuse a question key YAML spells twice",
			doc:     "questions:\n  1: {type: noul, instructions: a}\n  \"1\": {type: box, instructions: b}\n",
			wantErr: `mapping key "1" already defined`,
		},
		{
			name: "should refuse two question keys that name the same question",
			doc: "questions:\n  0x1: {type: noul, instructions: a}\n" +
				"  \"1\": {type: box, instructions: b, criteria: {x: y}}\n",
			wantErr: "question '1' is given twice in a request body",
		},
		{
			name:    "should refuse a question that is not a mapping",
			doc:     `{"questions":{"a":"is it urgent"}}`,
			wantErr: "question 'a' is not an object",
		},
		{
			name: "should treat a null noul criteria as absent",
			doc:  `{"questions":{"a":{"type":"noul","instructions":"q","criteria":null}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.Questions[0].Criteria != nil {
					t.Errorf("criteria = %+v, want nil", f.Questions[0].Criteria)
				}
			},
		},
		{
			name:    "should refuse an unknown question type",
			doc:     `{"questions":{"a":{"type":"vibes","instructions":"q"}}}`,
			wantErr: "vibes",
		},
		{
			name:    "should refuse a question with no type",
			doc:     `{"questions":{"a":{"instructions":"q"}}}`,
			wantErr: "type",
		},
		{
			name:    "should refuse a questions key that is not a mapping",
			doc:     `{"questions":[1,2]}`,
			wantErr: "questions",
		},
		{
			name:    "should refuse a top level assert in a request body",
			doc:     `{"assert":"a.value > 0.5","questions":{"a":{"type":"noul"}}}`,
			wantErr: "a request body carries no 'assert'. Pass --assert on the command line",
		},
		{
			name:    "should refuse a top level abstain_if in a request body",
			doc:     `{"abstain_if":"a.value > 0.5","questions":{"a":{"type":"noul"}}}`,
			wantErr: "a request body carries no 'abstain_if'. Pass --abstain-if on the command line",
		},
		{
			name:    "should name the kind of a structured model",
			doc:     `{"model":{"name":"onesie"},"questions":{"a":{"type":"noul"}}}`,
			wantErr: "'model' in a request body must be a string, got a mapping",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got)
		})
	}

	t.Run("should carry a structured body instruction through", func(t *testing.T) {
		t.Parallel()

		t.Run("should carry the numbers of a structured body instruction through as written", func(t *testing.T) {
			t.Parallel()

			const doc = `{"questions":{"u":{"type":"noul","instructions":` +
				`{"n":8e13,"big":123456789012345678901234567890,"f":1.50}}},"state":{"n":8e13}}`

			f, err := qfile.Load([]byte(doc))
			if err != nil {
				t.Fatalf("loading: %v", err)
			}

			encoded, err := json.Marshal(f.Questions[0].Instructions)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if want := `{"n":8e13,"big":123456789012345678901234567890,"f":1.50}`; string(encoded) != want {
				t.Errorf("instructions got  %s\nwant %s", encoded, want)
			}

			if want := `{"n":8e13}`; string(f.StateWire) != want {
				t.Errorf("state got  %s\nwant %s", f.StateWire, want)
			}
		})

		t.Run("should carry a structured body instruction through as an object", func(t *testing.T) {
			t.Parallel()

			const doc = `{"questions":{"a":{"type":"noul","instructions":` +
				`{"what":"is this urgent","examples":["call me now"]}}}}`

			f, err := qfile.Load([]byte(doc))
			if err != nil {
				t.Fatalf("loading: %v", err)
			}

			encoded, err := json.Marshal(f.Questions[0].Instructions)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			want := `{"what":"is this urgent","examples":["call me now"]}`
			if string(encoded) != want {
				t.Errorf("got  %s\nwant %s", encoded, want)
			}
		})
	})
}

func plainPNG(t *testing.T) []byte {
	t.Helper()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}

	return encoded.Bytes()
}
