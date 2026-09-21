package qfile_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		want    string
		wantErr bool
	}{
		{
			name: "should convert a nested mapping into a json object",
			doc:  "what: States facts\nexamples: [\"ok\"]\n",
			want: `{"examples":["ok"],"what":"States facts"}`,
		},
		{
			name: "should convert a mapping nested inside a sequence",
			doc:  "levels:\n  - calm:\n      what: no affect\n",
			want: `{"levels":[{"calm":{"what":"no affect"}}]}`,
		},
		{
			name: "should keep a bare string as a string",
			doc:  "just a string\n",
			want: `"just a string"`,
		},
		{
			name: "should keep a number as a number",
			doc:  "0.7\n",
			want: `0.7`,
		},
		{
			name: "should read json as well as yaml",
			doc:  `{"a":1,"b":["x"]}`,
			want: `{"a":1,"b":["x"]}`,
		},
		{
			name:    "should reject malformed input",
			doc:     "a:\n  - b\n c: broken\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Decode([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}
}
