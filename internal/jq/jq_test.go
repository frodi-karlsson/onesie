package jq

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

func TestCompile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{name: "should compile a path", source: ".ticket.body"},
		{name: "should name the column of an unexpected token", source: ".body | }", wantErr: "at column 9"},
		{name: "should name the column past the end of an unfinished expression", source: "{subject, body", wantErr: "unexpected EOF at column 15"},
		{name: "should count the column in characters", source: `"é" | }`, wantErr: "at column 7"},
		{name: "should reject an undefined function", source: ".body | nope", wantErr: "function not defined: nope/0"},
		{name: "should refuse to import a module", source: `import "lib" as lib; .`, wantErr: `cannot load module: "lib"`},
		{name: "should refuse to include a module", source: `include "lib"; .`, wantErr: `cannot load module: "lib"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Compile(tc.source)

			if tc.wantErr == "" {
				if err != nil || expr == nil {
					t.Fatalf("Compile(%q) = %v, %v, want an expression", tc.source, expr, err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Compile(%q) error = %v, want one containing %q", tc.source, err, tc.wantErr)
			}
		})
	}

	t.Run("should refuse to import a data file without reading it", func(t *testing.T) {
		t.Parallel()

		expr, err := Compile(`import "/etc/passwd" as $passwd; $passwd`)
		if expr != nil {
			t.Fatalf("Compile() = %v, want no expression", expr)
		}

		const want = `cannot load module: "/etc/passwd"`
		if err == nil || err.Error() != want {
			t.Fatalf("Compile() error = %v, want exactly %q", err, want)
		}
	})
}

func TestExpr_One(t *testing.T) {
	t.Parallel()

	ticket := decode(t, `{"id":12345678901234567890,"subject":"down","body":"the site is down",`+
		`"customer":"c1","ticket":{"body":"nested"},"messages":[{"text":"first"},{"text":"last"}]}`)

	tests := []struct {
		name    string
		source  string
		value   any
		want    any
		wantErr error
	}{
		{name: "should pick a field", source: ".body", value: ticket, want: "the site is down"},
		{name: "should pick a nested field", source: ".ticket.body", value: ticket, want: "nested"},
		{
			name:   "should build an object from fields",
			source: "{subject, body}",
			value:  ticket,
			want:   map[string]any{"subject": "down", "body": "the site is down"},
		},
		{
			name:   "should join fields into one string",
			source: `.subject + "\n\n" + .body`,
			value:  ticket,
			want:   "down\n\nthe site is down",
		},
		{name: "should index from the end of an array", source: ".messages[-1].text", value: ticket, want: "last"},
		{name: "should keep the digits of a large number", source: ".id", value: ticket, want: json.Number("12345678901234567890")},
		{name: "should yield null for a missing field", source: ".nope", value: ticket, want: nil},
		{name: "should report an expression that yields nothing", source: "empty", value: ticket, wantErr: ErrNoValue},
		{name: "should report an expression that yields two values", source: ".subject, .body", value: ticket, wantErr: ErrManyValues},
		{name: "should report an expression that fails as it runs", source: ".body.text", value: ticket, wantErr: ErrRun},
		{name: "should report a failure after the first value", source: `.body, error("boom")`, value: ticket, wantErr: ErrRun},
		{name: "should refuse to read a module's metadata", source: `"lib" | modulemeta`, value: ticket, wantErr: ErrRun},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Compile(tc.source)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.source, err)
			}

			got, err := expr.One(t.Context(), tc.value)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("One() error = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("One(): %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("One() = %#v, want %#v", got, tc.want)
			}
		})
	}

	t.Run("should read now from the real clock", func(t *testing.T) {
		t.Parallel()

		expr, err := Compile("now")
		if err != nil {
			t.Fatalf("Compile(): %v", err)
		}

		before := float64(time.Now().UnixMicro()) / 1e6

		got, err := expr.One(t.Context(), nil)
		if err != nil {
			t.Fatalf("One(): %v", err)
		}

		after := float64(time.Now().UnixMicro()) / 1e6

		seconds, ok := got.(float64)
		if !ok || seconds < before-1e-3 || seconds > after+1e-3 {
			t.Errorf("One() = %v, want a time between %f and %f", got, before, after)
		}
	})

	t.Run("should stop an expression that never ends once the context is cancelled", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			after time.Duration
		}{
			{name: "should stop when the context is already cancelled"},
			{name: "should stop when the context is cancelled as it runs", after: 20 * time.Millisecond},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				expr, err := Compile("until(false; .)")
				if err != nil {
					t.Fatalf("Compile(): %v", err)
				}

				ctx, cancel := context.WithCancel(t.Context())
				if tc.after == 0 {
					cancel()
				} else {
					time.AfterFunc(tc.after, cancel)
				}

				returned := make(chan error, 1)

				go func() {
					_, runErr := expr.One(ctx, "x")
					returned <- runErr
				}()

				select {
				case runErr := <-returned:
					if !errors.Is(runErr, context.Canceled) {
						t.Errorf("One() error = %v, want %v", runErr, context.Canceled)
					}

					if errors.Is(runErr, ErrRun) {
						t.Errorf("One() error = %v, want an interrupt rather than a failed run", runErr)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("One() ignored the cancelled context")
				}
			})
		}
	})
}

func TestID(t *testing.T) {
	t.Parallel()

	manyOnes := strings.Repeat("1", 1023) + "e-1024"
	pastHalfway := "9007199254740993" + strings.Repeat("0", 984) + "1e-985"

	tests := []struct {
		name    string
		value   any
		want    any
		wantIs  error
		wantErr string
	}{
		{name: "should keep a string", value: "T-7", want: "T-7"},
		{name: "should keep an empty string", value: "", want: ""},
		{name: "should keep a string as long as the limit", value: strings.Repeat("a", limits.MaxIDBytes), want: strings.Repeat("a", limits.MaxIDBytes)},
		{name: "should reject a string longer than the limit", value: strings.Repeat("a", limits.MaxIDBytes+1), wantIs: ErrIDTooLong, wantErr: "id is longer than 1024 bytes"},
		{name: "should write an integer as its digits", value: json.Number("7"), want: json.Number("7")},
		{name: "should write 7.0 as 7", value: json.Number("7.0"), want: json.Number("7")},
		{name: "should write an exponent that lands on an integer as its digits", value: json.Number("7e2"), want: json.Number("700")},
		{name: "should read an upper case exponent", value: json.Number("1.5E3"), want: json.Number("1500")},
		{name: "should write a fraction that a negative exponent lands on an integer as its digits", value: json.Number("7000e-3"), want: json.Number("7")},
		{name: "should write negative zero as zero", value: json.Number("-0.0"), want: json.Number("0")},
		{name: "should keep the sign of a negative integer", value: json.Number("-12e1"), want: json.Number("-120")},
		{name: "should keep the digits of a large integer", value: json.Number("12345678901234567890"), want: json.Number("12345678901234567890")},
		{name: "should keep the digits of a large integer written with a fraction", value: json.Number("12345678901234567890.0"), want: json.Number("12345678901234567890")},
		{name: "should write an integer past 1e21 as its digits", value: json.Number("1e21"), want: json.Number("1000000000000000000000")},
		{name: "should write an integer as long as the limit", value: json.Number("1e1023"), want: json.Number("1" + strings.Repeat("0", 1023))},
		{name: "should drop the trailing zeros of a fraction", value: json.Number("0.50"), want: json.Number("0.5")},
		{name: "should read a fraction whose leading zeros an exponent moves", value: json.Number("0.0012e3"), want: json.Number("1.2")},
		{name: "should read a fraction with more significant digits than ParseFloat keeps", value: json.Number(manyOnes), want: ratID(t, manyOnes)},
		{name: "should read a fraction whose rounding hangs on a digit past the 800th", value: json.Number(pastHalfway), want: ratID(t, pastHalfway)},
		{name: "should write a zero with an exponent past an int32 as zero", value: json.Number("0e99999999999"), want: json.Number("0")},
		{name: "should write a negative zero with a negative exponent past an int32 as zero", value: json.Number("-0.000e-99999999999"), want: json.Number("0")},
		{name: "should reject an exponent with two signs as not an id", value: json.Number("1e+-5"), wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got 1e+-5"},
		{name: "should reject an exponent with two minus signs as not an id", value: json.Number("1e--5"), wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got 1e--5"},
		{name: "should reject an integer that writes out longer than the limit", value: json.Number("1e1024"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got 1e1024, which writes out to more than 1024 bytes"},
		{name: "should count the sign of a negative integer against the limit", value: json.Number("-1e1023"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got -1e1023, which writes out to more than 1024 bytes"},
		{name: "should reject a million digit integer without writing it out", value: json.Number("1e1000000"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got 1e1000000, which writes out to more than 1024 bytes"},
		{name: "should reject an exponent past what big.Rat takes as out of range", value: json.Number("1e10000000"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got 1e10000000, which writes out to more than 1024 bytes"},
		{name: "should reject an exponent past an int32 as out of range", value: json.Number("1e99999999999"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got 1e99999999999"},
		{name: "should reject a fraction too large for a float64", value: json.Number(strings.Repeat("9", 400) + ".5"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range"},
		{name: "should reject a fraction that underflows a float64", value: json.Number("1.5e-400"), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, got 1.5e-400"},
		{name: "should leave out a number too long to show", value: json.Number(strings.Repeat("9", 2000)), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, it writes out to more than 1024 bytes"},
		{name: "should write a computed integer as its digits", value: 7, want: json.Number("7")},
		{name: "should write a computed float that lands on an integer as its digits", value: 7.0, want: json.Number("7")},
		{name: "should write a computed float past 1e21 as the digits its literal takes", value: 1e21, want: json.Number("1000000000000000000000")},
		{name: "should write a computed huge integer as its digits", value: 1.5e300, want: json.Number("15" + strings.Repeat("0", 299))},
		{name: "should write a computed big integer as its digits", value: new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil), want: json.Number("1" + strings.Repeat("0", 30))},
		{name: "should reject a computed big integer longer than the limit", value: new(big.Int).Exp(big.NewInt(10), big.NewInt(1024), nil), wantIs: ErrIDOutOfRange, wantErr: "id is out of range, it writes out to more than 1024 bytes"},
		{name: "should write a computed fraction in its shortest form", value: 0.25, want: json.Number("0.25")},
		{name: "should write a tiny fraction with an exponent", value: 1.5e-7, want: json.Number("1.5e-7")},
		{name: "should reject null", value: nil, wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got null"},
		{name: "should reject a boolean", value: true, wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got boolean"},
		{name: "should reject an array", value: []any{"a"}, wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got array"},
		{name: "should reject an object", value: map[string]any{"a": "b"}, wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got object"},
		{name: "should reject infinity", value: math.Inf(1), wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got +Inf"},
		{name: "should reject a number that is not a number", value: math.NaN(), wantIs: ErrNotID, wantErr: "id must be a string or a finite number, got NaN"},
		{name: "should write a computed negative zero as zero", value: math.Copysign(0, -1), want: json.Number("0")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ID(tc.value)

			if tc.wantErr != "" {
				if !errors.Is(err, tc.wantIs) || err.Error() != tc.wantErr {
					t.Fatalf("ID() error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ID(): %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ID() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func ratID(t *testing.T, text string) json.Number {
	t.Helper()

	exact, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("big.Rat cannot read %q", text)
	}

	nearest, _ := exact.Float64()

	return json.Number(strconv.FormatFloat(nearest, 'f', -1, 64))
}

func TestMarshal(t *testing.T) {
	t.Parallel()

	const limit = 64

	tests := []struct {
		name    string
		value   any
		limit   int
		want    string
		wantErr error
	}{
		{name: "should write a string", value: "a <b>", want: `"a <b>"`},
		{name: "should keep the digits of a large number", value: json.Number("12345678901234567890"), want: "12345678901234567890"},
		{name: "should sort object keys", value: map[string]any{"zebra": 1, "alpha": 2}, want: `{"alpha":2,"zebra":1}`},
		{name: "should write a value nested as deep as the request envelope leaves room for", value: nested(9999, "x"), limit: 1 << 20, want: strings.Repeat("[", 9999) + `"x"` + strings.Repeat("]", 9999)},
		{name: "should reject a value that nests as deep as encoding/json allows with no room for the envelope", value: nested(10000, "x"), limit: 1 << 26, wantErr: ErrTooDeep},
		{name: "should reject a value nested far too deep for a recursive encoder", value: nested(1_000_000, "x"), limit: 1 << 26, wantErr: ErrTooDeep},
		{name: "should reject an object nested too deep", value: nestedObject(10000), limit: 1 << 26, wantErr: ErrTooDeep},
		{name: "should write a value that fills the limit exactly", value: strings.Repeat("x", limit-2), want: `"` + strings.Repeat("x", limit-2) + `"`},
		{name: "should reject a string past the limit", value: strings.Repeat("x", limit-1), wantErr: ErrTooLarge},
		{name: "should reject an array whose separators alone pass the limit", value: make([]any, limit), wantErr: ErrTooLarge},
		{name: "should reject an object whose keys alone pass the limit", value: wideObject(limit), wantErr: ErrTooLarge},
		{name: "should reject a value its escapes carry past the limit", value: strings.Repeat("\x01", limit/4), wantErr: ErrTooLarge},
		{name: "should reject a large value nested inside small ones", value: []any{map[string]any{"a": []any{strings.Repeat("x", limit)}}}, wantErr: ErrTooLarge},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.limit == 0 {
				tc.limit = limit
			}

			got, err := Marshal(tc.value, tc.limit)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Marshal() error = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Marshal(): %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("Marshal() = %.80s, want %.80s", got, tc.want)
			}
		})
	}
}

func nested(depth int, leaf any) any {
	value := leaf
	for range depth {
		value = []any{value}
	}

	return value
}

func nestedObject(depth int) any {
	var value any = "x"
	for range depth {
		value = map[string]any{"a": value}
	}

	return value
}

func wideObject(size int) map[string]any {
	object := make(map[string]any, size)
	for i := range size {
		object[strings.Repeat("k", i+1)] = nil
	}

	return object
}

func decode(t *testing.T, text string) any {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decoding %s: %v", text, err)
	}

	return value
}

func TestIDText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   any
		want string
	}{
		{name: "should write a string id as it is", id: "7", want: "7"},
		{name: "should write a number id as its digits", id: json.Number("7.5"), want: "7.5"},
		{name: "should write nothing for a record with no id", id: nil, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IDText(tc.id); got != tc.want {
				t.Errorf("IDText(%v) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
