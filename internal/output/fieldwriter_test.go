package output

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/delimiter"
)

func TestNewFieldWriter(t *testing.T) {
	t.Parallel()

	t.Run("should encode ordered fields without answer columns", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			mode  Mode
			comma rune
			rows  [][]string
			want  string
		}{
			{"should preserve one empty csv field", CSV, 0, [][]string{{""}}, "\"\"\n"},
			{"should quote the configured separator", CSV, ';', [][]string{{"a;b", "x"}}, "\"a;b\";x\n"},
			{"should preserve literal tsv quotes and spaces", TSV, 0, [][]string{{"\"a\"", " x"}}, "\"a\"\t x\n"},
			{"should frame a trailing data carriage return", TSV, 0, [][]string{{"x\r"}}, "x\r\r\n"},
			{"should preserve interior carriage returns and empty tsv cells", TSV, 0, [][]string{{"", "a\rb", ""}}, "\ta\rb\t\n"},
			{"should quote multiline csv fields", CSV, 0, [][]string{{"a\nb", "\"c\""}}, "\"a\nb\",\"\"\"c\"\"\"\n"},
			{"should retain row order around empty csv cells", CSV, 0, [][]string{{"a"}, {""}, {"b"}, {""}}, "a\n\"\"\nb\n\"\"\n"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var buf bytes.Buffer
				writer := newFieldWriter(&buf, tc.mode, tc.comma)
				for _, row := range tc.rows {
					if err := writer.WriteRow(row); err != nil {
						t.Fatal(err)
					}
				}
				if buf.String() != tc.want {
					t.Fatalf("got %q, want %q", buf.String(), tc.want)
				}
				if tc.mode == CSV {
					reader := csv.NewReader(strings.NewReader(buf.String()))
					if tc.comma != 0 {
						reader.Comma = tc.comma
					}
					got, err := reader.ReadAll()
					if err != nil || !reflect.DeepEqual(got, tc.rows) {
						t.Fatalf("decoded %q, error %v", got, err)
					}
				} else {
					lines := strings.SplitAfter(buf.String(), "\n")
					for i, row := range tc.rows {
						if got := delimiter.TSVFields(lines[i]); !reflect.DeepEqual(got, row) {
							t.Fatalf("decoded %q, want %q", got, row)
						}
					}
				}
			})
		}
	})
	t.Run("should reject invalid rows before writing bytes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			mode  Mode
			comma rune
			row   []string
		}{
			{"should reject no fields", CSV, 0, nil},
			{"should reject no tsv fields", TSV, 0, nil},
			{"should reject one empty tsv field", TSV, 0, []string{""}},
			{"should reject a tsv tab", TSV, 0, []string{"valid", "a\tb"}},
			{"should reject a tsv line feed", TSV, 0, []string{"valid", "a\nb"}},
			{"should reject other modes", JSON, 0, []string{"x"}},
			{"should reject invalid csv separators for an empty cell", CSV, '\n', []string{""}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var buf bytes.Buffer
				if err := newFieldWriter(&buf, tc.mode, tc.comma).WriteRow(tc.row); err == nil {
					t.Fatal("expected error")
				}
				if buf.Len() != 0 {
					t.Fatalf("wrote %q", buf.String())
				}
			})
		}
	})
	t.Run("should report write and flush failures", func(t *testing.T) {
		t.Parallel()

		failure := errors.New("destination failed")
		for _, mode := range []Mode{CSV, TSV} {
			for _, row := range [][]string{{"x"}, {""}} {
				if mode == TSV && row[0] == "" {
					continue
				}
				t.Run("should propagate destination errors "+mode.String()+strings.Join(row, ""), func(t *testing.T) {
					t.Parallel()

					err := newFieldWriter(fieldFailureWriter{err: failure}, mode, 0).WriteRow(row)
					if !errors.Is(err, failure) {
						t.Fatalf("got %v", err)
					}
				})
				t.Run("should detect short writes "+mode.String()+strings.Join(row, ""), func(t *testing.T) {
					t.Parallel()

					err := newFieldWriter(fieldFailureWriter{}, mode, 0).WriteRow(row)
					if !errors.Is(err, io.ErrShortWrite) {
						t.Fatalf("got %v", err)
					}
				})
			}
		}
	})
	t.Run("should detect short string writes for large rows", func(t *testing.T) {
		t.Parallel()

		for _, mode := range []Mode{CSV, TSV} {
			t.Run("should reject a short write in "+mode.String(), func(t *testing.T) {
				t.Parallel()

				destination := &fieldShortStringWriter{}
				err := newFieldWriter(destination, mode, 0).WriteRow([]string{strings.Repeat("x", 8192)})
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("got %v, want short write", err)
				}
			})
		}
	})
}

type fieldFailureWriter struct{ err error }

func (w fieldFailureWriter) Write(p []byte) (int, error) { return 0, w.err }

type fieldShortStringWriter struct{ called bool }

func (w *fieldShortStringWriter) Write(p []byte) (int, error) { return w.WriteString(string(p)) }
func (w *fieldShortStringWriter) WriteString(s string) (int, error) {
	if w.called {
		return 0, errors.New("write retried")
	}
	w.called = true
	return len(s) / 2, nil
}
