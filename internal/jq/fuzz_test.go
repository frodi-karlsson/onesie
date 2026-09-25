package jq

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

const ratExponentBound = 4000

func FuzzID(f *testing.F) {
	for _, seed := range []string{
		"7", "7.0", "7e2", "1.5E3", "7000e-3", "-0.0", "-12e1", "12345678901234567890",
		"12345678901234567890.0", "1e21", "1e1023", "0.50", "0.0012e3",
		strings.Repeat("1", 1023) + "e-1024", "9007199254740993" + strings.Repeat("0", 984) + "1e-985",
		"0e99999999999", "-0.000e-99999999999", "1e+-5", "1e--5", "1e1024", "-1e1023", "1e1000000",
		"1e10000000", "1e99999999999", strings.Repeat("9", 400) + ".5", "1.5e-400", strings.Repeat("9", 2000),
		"0.25", "1.5e-7", "1e-7", "-1.5e300", "1e308", "1.7976931348623157e309", "4.9e-324", "2.4e-324",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		checkStringID(t, text)

		if !isJSONNumber(text) {
			return
		}

		got, err := ID(json.Number(text))
		if err != nil {
			if !errors.Is(err, ErrIDOutOfRange) {
				t.Fatalf("ID(%s) error = %v, want only out of range for a json number", text, err)
			}
		} else {
			number, isNumber := got.(json.Number)
			if !isNumber || !isJSONNumber(number.String()) {
				t.Fatalf("ID(%s) = %#v, want a json number", text, got)
			}

			if again, againErr := ID(number); againErr != nil || again != got {
				t.Fatalf("ID(%s) = %s, which names %v, %v the second time", text, number, again, againErr)
			}
		}

		// big.Rat builds every digit an exponent asks for, so a larger one is only checked above.
		if exponentOf(text) <= ratExponentBound {
			checkAgainstRat(t, text, got, err)
		}
	})
}

func checkStringID(t *testing.T, text string) {
	t.Helper()

	got, err := ID(text)
	if len(text) > limits.MaxIDBytes {
		if !errors.Is(err, ErrIDTooLong) {
			t.Fatalf("ID of a %d byte string = %v, %v, want too long", len(text), got, err)
		}

		return
	}

	if err != nil || got != text {
		t.Fatalf("ID(%q) = %#v, %v, want the string itself", text, got, err)
	}
}

func checkAgainstRat(t *testing.T, text string, got any, err error) {
	t.Helper()

	exact, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("big.Rat cannot read the json number %s", text)
	}

	if exact.IsInt() {
		digits := exact.Num().String()

		switch {
		case len(digits) > limits.MaxIDBytes:
			if err == nil {
				t.Fatalf("ID(%s) = %v, want out of range for %d digits", text, got, len(digits))
			}
		case err != nil || got != json.Number(digits):
			t.Fatalf("ID(%s) = %v, %v, want every digit of %s", text, got, err, digits)
		}

		return
	}

	nearest, _ := exact.Float64()
	if nearest == 0 || math.IsInf(nearest, 0) {
		if err == nil {
			t.Fatalf("ID(%s) = %v, want out of range for a fraction a float64 cannot hold", text, got)
		}

		return
	}

	if err != nil {
		t.Fatalf("ID(%s) error = %v, want the fraction nearest %v", text, err, nearest)
	}

	// The same record whether the fraction arrived as a literal or as a computed float.
	if computed, computedErr := ID(nearest); computedErr != nil || computed != got {
		t.Fatalf("ID(%s) = %v, but ID(%v) = %v, %v", text, got, nearest, computed, computedErr)
	}

	written := got.(json.Number).String()

	parsed, parseErr := strconv.ParseFloat(written, 64)
	if parseErr != nil || parsed != nearest {
		t.Fatalf("ID(%s) = %s, which reads back as %v, want %v", text, written, parsed, nearest)
	}

	if shortest := strconv.FormatFloat(nearest, 'g', -1, 64); significantDigits(written) > significantDigits(shortest) {
		t.Fatalf("ID(%s) = %s, longer than the shortest form %s", text, written, shortest)
	}
}

func isJSONNumber(text string) bool {
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.UseNumber()

	var value any
	if decoder.Decode(&value) != nil || decoder.More() {
		return false
	}

	number, ok := value.(json.Number)

	return ok && number.String() == text
}

func exponentOf(text string) int {
	_, exponent, found := strings.Cut(strings.ToLower(text), "e")
	if !found {
		return 0
	}

	value, err := strconv.Atoi(exponent)
	if err != nil {
		return math.MaxInt
	}

	return max(value, -value)
}

func significantDigits(text string) int {
	mantissa, _, _ := strings.Cut(strings.ToLower(text), "e")
	digits := strings.Trim(strings.NewReplacer("-", "", ".", "").Replace(mantissa), "0")

	return len(digits)
}
