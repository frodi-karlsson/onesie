package calibrate_test

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestSplitLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spec       string
		ids        []string
		wantID     string
		wantSource string
		wantErr    []string
	}{
		{
			name:       "should take the text before the first equals sign as a known id",
			spec:       "urgent=.is_urgent",
			ids:        []string{"urgent", "team"},
			wantID:     "urgent",
			wantSource: ".is_urgent",
		},
		{
			name:       "should give a bare expression to the one question asked",
			spec:       ".is_urgent",
			ids:        []string{"answer"},
			wantID:     "answer",
			wantSource: ".is_urgent",
		},
		{
			name:       "should keep a comparison as the whole expression",
			spec:       `.status=="open"`,
			ids:        []string{"answer"},
			wantID:     "answer",
			wantSource: `.status=="open"`,
		},
		{
			name:       "should keep a double equals after an id as the whole expression",
			spec:       "urgent==.x",
			ids:        []string{"urgent"},
			wantID:     "urgent",
			wantSource: "urgent==.x",
		},
		{
			name:       "should keep an equals sign inside the expression after an id",
			spec:       `urgent=.status=="open"`,
			ids:        []string{"urgent"},
			wantID:     "urgent",
			wantSource: `.status=="open"`,
		},
		{
			name:       "should name a question whose id holds an equals sign",
			spec:       "x=y=.x",
			ids:        []string{"x=y", "z"},
			wantID:     "x=y",
			wantSource: ".x",
		},
		{
			name:       "should prefer the longest id the label starts with",
			spec:       "x=y=.x",
			ids:        []string{"x", "x=y"},
			wantID:     "x=y",
			wantSource: ".x",
		},
		{
			name:       "should fall back to a shorter id when the longer one is followed by a comparison",
			spec:       "x=y==.x",
			ids:        []string{"x", "x=y"},
			wantID:     "x",
			wantSource: "y==.x",
		},
		{
			name:    "should refuse a bare expression when two questions are asked",
			spec:    ".x",
			ids:     []string{"urgent", "team"},
			wantErr: []string{"ID=", "urgent, team"},
		},
		{
			name:    "should refuse an unknown question and list the ids",
			spec:    "tema=.x",
			ids:     []string{"urgent", "team"},
			wantErr: []string{"--label names an unknown question 'tema'. Questions: urgent, team"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, source, err := calibrate.SplitLabel(tc.spec, tc.ids)

			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("SplitLabel(%q) = %q, %q, want an error", tc.spec, id, source)
				}

				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %q, want it to contain %q", err, want)
					}
				}

				return
			}

			if err != nil {
				t.Fatalf("SplitLabel(%q) error = %v", tc.spec, err)
			}

			if id != tc.wantID || source != tc.wantSource {
				t.Errorf("SplitLabel(%q) = %q, %q, want %q, %q", tc.spec, id, source, tc.wantID, tc.wantSource)
			}
		})
	}
}

func TestParseLabel(t *testing.T) {
	t.Parallel()

	options := []string{"billing", "shipping", "technical"}
	levels := []string{"1", "2", "3", "4", "5"}
	named := []string{"calm", "annoyed", "furious"}

	yes := calibrate.Label{Yes: true}
	no := calibrate.Label{Yes: false}

	tests := []struct {
		name      string
		shape     plan.Shape
		names     []string
		value     any
		want      calibrate.Label
		unlabeled bool
		wantErr   []string
	}{
		{name: "should read true as yes", shape: plan.Noul, value: true, want: yes},
		{name: "should read false as no", shape: plan.Noul, value: false, want: no},
		{name: "should read the int 1 as yes", shape: plan.Noul, value: 1, want: yes},
		{name: "should read the int 0 as no", shape: plan.Noul, value: 0, want: no},
		{name: "should read the float 1 as yes", shape: plan.Noul, value: 1.0, want: yes},
		{name: "should read the float 0 as no", shape: plan.Noul, value: 0.0, want: no},
		{name: "should read the json number 1 as yes", shape: plan.Noul, value: json.Number("1"), want: yes},
		{name: "should read the json number 0 as no", shape: plan.Noul, value: json.Number("0"), want: no},
		{name: "should read the big int 1 as yes", shape: plan.Noul, value: big.NewInt(1), want: yes},
		{name: "should read the big int 0 as no", shape: plan.Noul, value: big.NewInt(0), want: no},
		{name: "should read TRUE in any case as yes", shape: plan.Noul, value: "TRUE", want: yes},
		{name: "should read No in any case as no", shape: plan.Noul, value: "No", want: no},
		{name: "should read the string yes as yes", shape: plan.Noul, value: "yes", want: yes},
		{name: "should read the string 1 as yes", shape: plan.Noul, value: "1", want: yes},
		{name: "should read the string 0 as no", shape: plan.Noul, value: "0", want: no},
		{
			name: "should read an option name as declared", shape: plan.Pick, names: options,
			value: "shipping", want: calibrate.Label{Name: "shipping"},
		},
		{
			name: "should read a number whose text is an option name", shape: plan.Pick,
			names: []string{"1", "2"}, value: 2, want: calibrate.Label{Name: "2"},
		},
		{
			name: "should read a level label as declared", shape: plan.Rate, names: named,
			value: "annoyed", want: calibrate.Label{Name: "annoyed"},
		},
		{
			name: "should read the number 4 against the levels 1 to 5", shape: plan.Rate, names: levels,
			value: 4, want: calibrate.Label{Name: "4"},
		},
		{
			name: "should read the float 4 against the levels 1 to 5", shape: plan.Rate, names: levels,
			value: 4.0, want: calibrate.Label{Name: "4"},
		},
		{name: "should leave null unlabelled", shape: plan.Noul, value: nil, unlabeled: true},
		{name: "should leave an empty string unlabelled", shape: plan.Noul, value: "", unlabeled: true},
		{name: "should leave null unlabelled for a pick", shape: plan.Pick, names: options, value: nil, unlabeled: true},
		{name: "should leave an empty string unlabelled for a rate", shape: plan.Rate, names: levels, value: "", unlabeled: true},
		{
			name: "should refuse maybe", shape: plan.Noul, value: "maybe",
			wantErr: []string{`"maybe"`, "true, false, yes, no, 1 or 0"},
		},
		{
			name: "should refuse a fraction", shape: plan.Noul, value: 0.5,
			wantErr: []string{"0.5", "true, false, yes, no, 1 or 0"},
		},
		{
			name: "should refuse a yes with a leading space", shape: plan.Noul, value: " yes",
			wantErr: []string{`" yes"`, "true, false, yes, no, 1 or 0"},
		},
		{
			name: "should refuse an array", shape: plan.Noul, value: []any{},
			wantErr: []string{"[]", "true, false, yes, no, 1 or 0"},
		},
		{
			name: "should refuse an object", shape: plan.Pick, names: options, value: map[string]any{"a": 1},
			wantErr: []string{`{"a":1}`, "billing, shipping or technical"},
		},
		{
			name: "should refuse an option in the wrong case", shape: plan.Pick, names: options,
			value: "Billing", wantErr: []string{`"Billing"`, "billing, shipping or technical"},
		},
		{
			name: "should refuse a level that is not declared", shape: plan.Rate, names: levels,
			value: 6, wantErr: []string{"6", "1, 2, 3, 4 or 5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, labelled, err := calibrate.ParseLabel(tc.shape, tc.names, tc.value)

			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("ParseLabel(%v) = %+v, %v, want an error", tc.value, got, labelled)
				}

				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %q, want it to contain %q", err, want)
					}
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseLabel(%v) error = %v", tc.value, err)
			}

			if labelled == tc.unlabeled {
				t.Fatalf("ParseLabel(%v) labelled = %v, want %v", tc.value, labelled, !tc.unlabeled)
			}

			if labelled && got != tc.want {
				t.Errorf("ParseLabel(%v) = %+v, want %+v", tc.value, got, tc.want)
			}
		})
	}
}
