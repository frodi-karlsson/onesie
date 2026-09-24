package jq

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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
		{name: "should refuse to import a data file", source: `import "data" as $data; .`, wantErr: `cannot load module: "data"`},
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
}

func TestExpr_One(t *testing.T) {
	t.Parallel()

	ticket := decode(t, `{"id":12345678901234567890,"subject":"down","body":"the site is down",`+
		`"customer":"c1","ticket":{"body":"nested"},"messages":[{"text":"first"},{"text":"last"}]}`)

	stamped := decode(t, `{"ts":1790244000}`)

	clock := func() time.Time {
		return time.Date(2026, 9, 24, 10, 0, 0, 5e8, time.UTC).In(time.FixedZone("CEST", 2*60*60))
	}

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
		{name: "should read now from the injected clock", source: "now", value: ticket, want: 1790244000.5},
		{name: "should format now in UTC", source: "now | todate", value: ticket, want: "2026-09-24T10:00:00Z"},
		{
			name:   "should break now into the local time of the injected clock",
			source: "now | localtime",
			value:  ticket,
			want:   []any{2026, 8, 24, 12, 0, 0.5, 4, 266},
		},
		{name: "should format now in the zone of the injected clock", source: `now | strflocaltime("%H:%M %Z")`, value: ticket, want: "12:00 CEST"},
		{
			name:   "should format a broken down time in the zone of the injected clock",
			source: `[2026, 8, 24, 12, 0, 0, 4, 266] | strflocaltime("%H:%M %z")`,
			value:  ticket,
			want:   "12:00 +0200",
		},
		{name: "should read a record's timestamp in the zone of the injected clock", source: ".ts | localtime | .[3]", value: stamped, want: 12},
		{name: "should break a record's timestamp into UTC whatever the clock's zone", source: ".ts | gmtime | .[3]", value: stamped, want: 10},
		{name: "should let an expression define its own now", source: "def now: 1; now", value: ticket, want: 1},
		{name: "should report localtime on a string", source: `"x" | localtime`, value: ticket, wantErr: ErrRun},
		{name: "should report strflocaltime without a format string", source: "now | strflocaltime(1)", value: ticket, wantErr: ErrRun},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Compile(tc.source, WithClock(clock))
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
		{name: "should write a value nested as deep as encoding/json allows", value: nested(10000, "x"), limit: 1 << 20, want: strings.Repeat("[", 10000) + `"x"` + strings.Repeat("]", 10000)},
		{name: "should reject a value nested one level deeper than encoding/json allows", value: nested(10001, "x"), limit: 1 << 26, wantErr: ErrTooDeep},
		{name: "should reject a value nested far too deep for a recursive encoder", value: nested(1_000_000, "x"), limit: 1 << 26, wantErr: ErrTooDeep},
		{name: "should reject an object nested too deep", value: nestedObject(10001), limit: 1 << 26, wantErr: ErrTooDeep},
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
