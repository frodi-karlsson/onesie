package settings_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestEnum(t *testing.T) {
	t.Parallel()

	output := settings.Enum([]string{"auto", "json", "markdown"}, map[string]string{"md": "markdown"})

	tests := []struct {
		name      string
		raw       string
		want      any
		wantErr   []string
		wantEmpty bool
	}{
		{name: "should read md as markdown", raw: "md", want: "markdown"},
		{name: "should read a listed value as itself", raw: "json", want: "json"},
		{
			name: "should refuse a value outside the list with every value named", raw: "jsno",
			wantErr: []string{"auto, json or markdown", "'jsno'"},
		},
		{name: "should refuse an empty value with ErrEmptyValue", raw: "", wantEmpty: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := output.Parse(tc.raw)

			switch {
			case tc.wantEmpty:
				if !errors.Is(err, settings.ErrEmptyValue) {
					t.Fatalf("Parse(%q) error = %v, want ErrEmptyValue", tc.raw, err)
				}
			case tc.wantErr != nil:
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want an error", tc.raw, got)
				}

				for _, part := range tc.wantErr {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("Parse(%q) error = %q, want it to hold %q", tc.raw, err, part)
					}
				}
			case err != nil:
				t.Fatalf("Parse(%q) error = %v", tc.raw, err)
			case got != tc.want:
				t.Errorf("Parse(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNonEmptyText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantEmpty bool
	}{
		{name: "should refuse a blank value with ErrEmptyValue", raw: " \t", wantEmpty: true},
		{name: "should refuse an empty value with ErrEmptyValue", raw: "", wantEmpty: true},
		{name: "should take text with spaces around it", raw: " work "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings.NonEmptyText().Parse(tc.raw)
			if tc.wantEmpty {
				if !errors.Is(err, settings.ErrEmptyValue) {
					t.Errorf("Parse(%q) error = %v, want ErrEmptyValue", tc.raw, err)
				}

				return
			}

			if err != nil || got != tc.raw {
				t.Errorf("Parse(%q) = %v, %v, want %q", tc.raw, got, err, tc.raw)
			}
		})
	}
}

func TestSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantEmpty bool
	}{
		{name: "should take a space", raw: " "},
		{name: "should refuse an empty value with ErrEmptyValue", raw: "", wantEmpty: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := settings.Separator().Parse(tc.raw)
			if got := errors.Is(err, settings.ErrEmptyValue); got != tc.wantEmpty {
				t.Errorf("Parse(%q) error = %v, want ErrEmptyValue %v", tc.raw, err, tc.wantEmpty)
			}
		})
	}
}

func TestEndpoint(t *testing.T) {
	t.Parallel()

	account := "0123456789abcdef0123456789ABCDEF"

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "should take an account from a source tagged HalfAccount",
			run: func(t *testing.T) {
				got, err := settings.Endpoint().ParseHalf(account, settings.HalfAccount)
				if err != nil {
					t.Fatal(err)
				}

				if want := (settings.EndpointValue{AccountID: account}); got != want {
					t.Errorf("ParseHalf = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "should take a base URL from a source tagged HalfBaseURL",
			run: func(t *testing.T) {
				got, err := settings.Endpoint().Parse("https://example.com/v1")
				if err != nil {
					t.Fatal(err)
				}

				if want := (settings.EndpointValue{BaseURL: "https://example.com/v1"}); got != want {
					t.Errorf("Parse = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "should refuse an account id that is not 32 hexadecimal characters, as creds.ValidAccountID does",
			run: func(t *testing.T) {
				for _, id := range []string{
					account, "0123456789abcdef", account + "0", "0123456789abcdef0123456789abcdeg", " " + account[1:],
				} {
					_, err := settings.Endpoint().ParseHalf(id, settings.HalfAccount)
					if got, want := err == nil, creds.ValidAccountID(id); got != want {
						t.Errorf("ParseHalf(%q) accepts %v, and creds.ValidAccountID says %v", id, got, want)
					}
				}
			},
		},
		{
			name: "should refuse a blank value with ErrEmptyValue",
			run: func(t *testing.T) {
				if _, err := settings.Endpoint().Parse(" "); !errors.Is(err, settings.ErrEmptyValue) {
					t.Errorf("Parse error = %v, want ErrEmptyValue", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     any
		want    int
		wantErr bool
	}{
		{name: "should read a json.Number from a JSON file", raw: json.Number("4"), want: 4},
		{name: "should read an int64 from a YAML file", raw: int64(3), want: 3},
		{name: "should read a uint64 from a YAML file", raw: uint64(5), want: 5},
		{name: "should read a whole float64", raw: 2.0, want: 2},
		{name: "should refuse a float64 with a fraction", raw: 2.5, wantErr: true},
		{name: "should refuse a json.Number with a fraction", raw: json.Number("2.5"), wantErr: true},
		{name: "should refuse a number out of range", raw: uint64(11), wantErr: true},
		{name: "should refuse a string", raw: "4", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings.Int(1, 10).FromFile(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("FromFile(%#v) = %v, want an error", tc.raw, got)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("FromFile(%#v) = %v, %v, want %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestProbability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     any
		want    float64
		wantErr bool
	}{
		{name: "should read a json.Number from a JSON file", raw: json.Number("0.25"), want: 0.25},
		{name: "should read a float64 from a YAML file", raw: 0.5, want: 0.5},
		{name: "should read an int64 from a YAML file", raw: int64(0), want: 0},
		{name: "should read a uint64 from a YAML file", raw: uint64(1), want: 1},
		{name: "should refuse a number above 1", raw: json.Number("1.5"), wantErr: true},
		{name: "should refuse a negative int64", raw: int64(-1), wantErr: true},
		{name: "should refuse a json.Number that is no number", raw: json.Number("x"), wantErr: true},
		{name: "should refuse a string", raw: "0.5", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings.Probability().FromFile(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("FromFile(%#v) = %v, want an error", tc.raw, got)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("FromFile(%#v) = %v, %v, want %v", tc.raw, got, err, tc.want)
			}
		})
	}

	t.Run("should point its schema at the probability definition", func(t *testing.T) {
		t.Parallel()

		if got := settings.Probability().Schema(); got.Ref != "#/definitions/probability" {
			t.Errorf("Schema() = %+v, want a $ref to #/definitions/probability", got)
		}
	})
}

func TestOptions(t *testing.T) {
	t.Parallel()

	checkListKind(t, settings.Options(), "#/definitions/pick")
}

func TestLevels(t *testing.T) {
	t.Parallel()

	checkListKind(t, settings.Levels(), "#/definitions/rate")
}

func checkListKind(t *testing.T, kind settings.Kind, ref string) {
	t.Helper()

	tests := []struct {
		name    string
		check   func() bool
		explain string
	}{
		{
			name:    "should read comma separated text as it is",
			check:   func() bool { got, err := kind.Parse("a,b"); return err == nil && got == "a,b" },
			explain: "Parse(a,b) is not a,b",
		},
		{
			name:    "should take an empty text, as --pick and --rate do",
			check:   func() bool { _, err := kind.Parse(""); return err == nil },
			explain: "Parse refuses an empty text",
		},
		{
			name: "should take a sequence or a mapping from a file",
			check: func() bool {
				_, seqErr := kind.FromFile([]any{"a", "b"})
				_, mapErr := kind.FromFile(map[string]any{"a": "x"})

				return seqErr == nil && mapErr == nil
			},
			explain: "FromFile refuses a sequence or a mapping",
		},
		{
			name:    "should refuse a number from a file",
			check:   func() bool { _, err := kind.FromFile(3); return err != nil },
			explain: "FromFile takes a number",
		},
		{
			name:    "should point its schema at its definition",
			check:   func() bool { return kind.Schema().Ref == ref },
			explain: "Schema() is no $ref to " + ref,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !tc.check() {
				t.Error(tc.explain)
			}
		})
	}
}

func TestTrueOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     any
		wantErr bool
	}{
		{name: "should take true from a file", raw: true},
		{name: "should refuse false from a file", raw: false, wantErr: true},
		{name: "should refuse a string from a file", raw: "true", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings.TrueOnly().FromFile(tc.raw)
			if (err != nil) != tc.wantErr || (err == nil && got != true) {
				t.Errorf("FromFile(%#v) = %v, %v, want an error %v", tc.raw, got, err, tc.wantErr)
			}
		})
	}

	t.Run("should parse a flag value as Bool does", func(t *testing.T) {
		t.Parallel()

		got, err := settings.TrueOnly().Parse("false")
		if err != nil || got != false {
			t.Errorf("Parse(false) = %v, %v, want false", got, err)
		}
	})

	t.Run("should describe the one value true", func(t *testing.T) {
		t.Parallel()

		if got := settings.TrueOnly().Schema(); got.Const != true {
			t.Errorf("Schema() = %+v, want const true", got)
		}
	})
}
