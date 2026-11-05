package settings_test

import (
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
