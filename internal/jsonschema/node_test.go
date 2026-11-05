package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jsonschema"
)

func TestProperties_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		properties jsonschema.Properties
		want       string
	}{
		{
			name: "should keep the insertion order",
			properties: jsonschema.Properties{
				{Name: "zebra", Schema: jsonschema.Node{Type: "string"}},
				{Name: "apple", Schema: true},
				{Name: "mango", Schema: jsonschema.Node{Ref: "#/definitions/mango"}},
			},
			want: `{"zebra":{"type":"string"},"apple":true,"mango":{"$ref":"#/definitions/mango"}}`,
		},
		{
			name:       "should write an empty object for no properties",
			properties: jsonschema.Properties{},
			want:       `{}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.properties)
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
