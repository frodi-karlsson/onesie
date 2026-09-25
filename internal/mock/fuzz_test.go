package mock

import (
	"bytes"
	"testing"
)

func FuzzLoad(f *testing.F) {
	f.Add([]byte(`{"u":0.9,"t":"billing","r":"curt"}`), false)
	f.Add([]byte(`{"id":"T-1","u":{"value":0.2},"t":{"value":"platform","confidence":0.4},"r":{"value":"rude","score":1.6,"norm":0.8}}`), true)
	f.Add([]byte("{\"u\":1,\"t\":\"billing\",\"r\":\"calm\"}\n{\"error\":503}\n{\"error\":\"timeout\"}\n"), false)
	f.Add([]byte(`{"error":{"kind":"input","status":null,"message":"x"}}`), false)
	f.Add([]byte(`{"u":1e-400,"t":"billing","r":"calm"}`), false)

	f.Fuzz(func(t *testing.T, data []byte, byID bool) {
		questions := threeQuestions()

		answers, err := Load(bytes.NewReader(data), questions, Options{ByID: byID})
		if err != nil {
			return
		}

		for position := range 4 {
			entry, found := answers.Lookup(position, "T-1")
			if !found {
				continue
			}

			result, resultErr := entry.Result()
			if resultErr != nil {
				continue
			}

			answer, err := result.Noul("u")
			if err != nil {
				t.Fatalf("a loaded entry has no yes/no answer: %v", err)
			}

			if !(answer.Noul >= 0 && answer.Noul <= 1) {
				t.Fatalf("a loaded yes/no answer is %v", answer.Noul)
			}
		}
	})
}
