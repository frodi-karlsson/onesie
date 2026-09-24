package calibrate_test

import (
	"encoding/json"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func FuzzSplitLabel(f *testing.F) {
	for _, seed := range []struct{ spec, ids string }{
		{"urgent=.is_urgent", "urgent\x00team"},
		{".is_urgent", "answer"},
		{`.status=="open"`, "answer"},
		{"urgent==.x", "urgent"},
		{`urgent=.status=="open"`, "urgent"},
		{"x=y=.x", "x=y\x00z"},
		{"x=y=.x", "x\x00x=y"},
		{"x=y==.x", "x\x00x=y"},
		{".x", "urgent\x00team"},
		{"tema=.x", "urgent\x00team"},
		{"=.x", "\x00a"},
		{"a=", "a"},
	} {
		f.Add(seed.spec, seed.ids)
	}

	f.Fuzz(func(t *testing.T, spec, joined string) {
		ids := distinct(strings.Split(joined, "\x00"))

		id, source, err := calibrate.SplitLabel(spec, ids)
		if err != nil {
			if id != "" || source != "" {
				t.Fatalf("SplitLabel(%q, %q) = %q, %q beside the error %v", spec, ids, id, source, err)
			}

			return
		}

		if !slices.Contains(ids, id) {
			t.Fatalf("SplitLabel(%q, %q) gave the id %q, which is not a question", spec, ids, id)
		}

		longest, found := "", false
		for _, candidate := range ids {
			rest, named := strings.CutPrefix(spec, candidate+"=")
			if named && !strings.HasPrefix(rest, "=") && (!found || len(candidate) > len(longest)) {
				longest, found = candidate, true
			}
		}

		switch {
		case found:
			if id != longest || spec != id+"="+source {
				t.Fatalf("SplitLabel(%q, %q) = %q, %q, want the longest named id %q and the text after it",
					spec, ids, id, source, longest)
			}
		case len(ids) != 1 || source != spec:
			t.Fatalf("SplitLabel(%q, %q) = %q, %q, want the whole text for the one question",
				spec, ids, id, source)
		}
	})
}

func FuzzParseLabel(f *testing.F) {
	for _, seed := range []struct {
		shape uint8
		names string
		kind  uint8
		value string
	}{
		{0, "", 0, "TRUE"},
		{0, "", 0, "No"},
		{0, "", 0, "maybe"},
		{0, "", 0, " yes"},
		{0, "", 0, ""},
		{0, "", 1, "1"},
		{0, "", 1, "0.5"},
		{0, "", 2, "1"},
		{0, "", 2, "1.0e0"},
		{0, "", 3, "0"},
		{0, "", 4, "true"},
		{0, "", 5, "null"},
		{1, "billing\x00shipping\x00technical", 0, "shipping"},
		{1, "billing\x00shipping\x00technical", 0, "Billing"},
		{1, "1\x002", 3, "2"},
		{1, "", 0, "x"},
		{1, "", 5, "null"},
		{2, "1\x002\x003\x004\x005", 1, "4"},
		{2, "1\x002\x003\x004\x005", 3, "6"},
		{2, "calm\x00annoyed\x00furious", 0, "annoyed"},
		{2, "4\x001e0\x001", 2, "1e0"},
		{2, "-0\x000", 1, "-0"},
	} {
		f.Add(seed.shape, seed.names, seed.kind, seed.value)
	}

	f.Fuzz(func(t *testing.T, shapeIndex uint8, joined string, kind uint8, text string) {
		shape := []plan.Shape{plan.Noul, plan.Pick, plan.Rate}[int(shapeIndex)%3]
		names := distinct(strings.Split(joined, "\x00"))

		value, ok := labelValue(kind, text)
		if !ok {
			return
		}

		label, labelled, err := calibrate.ParseLabel(shape, names, value)
		if err != nil {
			if labelled {
				t.Fatalf("ParseLabel(%v, %q, %#v) labelled a record beside the error %v", shape, names, value, err)
			}

			return
		}

		if !labelled {
			if value != nil && value != "" {
				t.Fatalf("ParseLabel(%v, %q, %#v) left a value unlabelled", shape, names, value)
			}

			return
		}

		if shape == plan.Noul {
			if label.Name != "" {
				t.Fatalf("ParseLabel(%v, %#v) = %+v, a yes/no label carries no name", shape, value, label)
			}

			return
		}

		if !slices.Contains(names, label.Name) {
			t.Fatalf("ParseLabel(%v, %q, %#v) = %+v, whose name is not declared", shape, names, value, label)
		}
	})
}

func FuzzParseCuts(f *testing.F) {
	for _, seed := range []string{
		"0.9,0.95,0.97,0.99", "0.9,0.5,0.9,0", "0,1", "0.5,1.2", "-0.1", "NaN", "x", "", "0.5,,0.6",
		" 0.5 , 0.25", "1e-400", "0x1p-2", "Inf", "+0.5", ",", "5e-1,0.5",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		cuts, err := calibrate.ParseCuts(text)
		if err != nil {
			if cuts != nil {
				t.Fatalf("ParseCuts(%q) = %v beside the error %v", text, cuts, err)
			}

			return
		}

		if len(cuts) == 0 {
			t.Fatalf("ParseCuts(%q) accepted a list with no cuts", text)
		}

		for i, cut := range cuts {
			// A negative zero must come back as zero, since the report would print it as -0.
			if !(cut >= 0 && cut <= 1) || math.Signbit(cut) {
				t.Fatalf("ParseCuts(%q) = %v, whose cut %v is outside [0,1]", text, cuts, cut)
			}

			if i > 0 && cuts[i-1] >= cut {
				t.Fatalf("ParseCuts(%q) = %v, which is not sorted and distinct", text, cuts)
			}
		}

		for item := range strings.SplitSeq(text, ",") {
			parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(item), 64)
			if parseErr != nil || !slices.Contains(cuts, parsed) {
				t.Fatalf("ParseCuts(%q) = %v, which lost the item %q", text, cuts, item)
			}
		}
	})
}

func distinct(items []string) []string {
	var kept []string

	for _, item := range items {
		if !slices.Contains(kept, item) {
			kept = append(kept, item)
		}
	}

	return kept
}

func labelValue(kind uint8, text string) (any, bool) {
	switch kind % 6 {
	case 0:
		return text, true
	case 1:
		number, err := strconv.ParseFloat(text, 64)
		return number, err == nil
	case 2:
		return json.Number(text), json.Valid([]byte(text)) && strings.Trim(text, "-0123456789.eE+") == ""
	case 3:
		number, ok := new(big.Int).SetString(text, 10)
		return number, ok
	case 4:
		return text == "true", true
	default:
		return nil, true
	}
}
