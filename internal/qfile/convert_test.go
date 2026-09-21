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

func TestMarshalOrdered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "should keep an object's keys in the order they were written",
			doc:  `{"zebra":1,"alpha":2,"middle":3}`,
			want: `{"zebra":1,"alpha":2,"middle":3}`,
		},
		{
			name: "should keep the order of a nested object",
			doc:  `{"outer":{"zebra":1,"alpha":2}}`,
			want: `{"outer":{"zebra":1,"alpha":2}}`,
		},
		{
			name: "should keep the order of every object in an array",
			doc:  `{"items":[{"zebra":1,"alpha":2},{"beta":3,"acme":4}]}`,
			want: `{"items":[{"zebra":1,"alpha":2},{"beta":3,"acme":4}]}`,
		},
		{
			name: "should keep a nineteen digit integer's digits",
			doc:  `{"ticket_id":12345678901234567890}`,
			want: `{"ticket_id":12345678901234567890}`,
		},
		{
			name: "should escape a key and a value that need it",
			doc:  `{"a \"quoted\" key":"a \"quoted\" value"}`,
			want: `{"a \"quoted\" key":"a \"quoted\" value"}`,
		},
		{
			name: "should encode a bare scalar as itself",
			doc:  `"just a string"`,
			want: `"just a string"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw, err := qfile.DecodeOrdered([]byte(tc.doc))
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}

			got, err := qfile.MarshalOrdered(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
