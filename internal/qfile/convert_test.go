package qfile

import "testing"

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

			raw, err := DecodeOrdered([]byte(tc.doc))
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}

			got, err := MarshalOrdered(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
