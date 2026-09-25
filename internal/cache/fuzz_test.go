package cache

import (
	"bytes"
	"testing"
)

func FuzzDecodeEntry(f *testing.F) {
	f.Add([]byte(`{"version":1,"model":"jev-1.13.0","stored":"2026-03-01T12:00:00Z","value":{"answers":{}}}`))
	f.Add([]byte(`{"version":1,"model":"jev-latest","stored":"2026-03-01T12:00:00.5+01:00","value": [1, 2] }`))
	f.Add([]byte(`{"version":2,"model":"m","stored":"2026-03-01T12:00:00Z","value":1}`))
	f.Add([]byte(`{"version":1,"model":"m","stored":"2026-03-01T12:00:00Z"}`))
	f.Add([]byte(`{"version":1,"model":"é\"","stored":"0001-01-01T00:00:00Z","value":"x"}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, err := decodeEntry(data)
		if err != nil {
			return
		}

		encoded, err := encodeEntry(decoded)
		if err != nil {
			t.Fatalf("an accepted entry does not encode: %v", err)
		}

		again, err := decodeEntry(encoded)
		if err != nil {
			t.Fatalf("an encoded entry does not decode: %v\n%s", err, encoded)
		}

		if again.Model != decoded.Model || !again.Stored.Equal(decoded.Stored) || !bytes.Equal(again.Value, decoded.Value) {
			t.Fatalf("round trip changed the entry\nfirst:  %+v\nsecond: %+v", decoded, again)
		}
	})
}
