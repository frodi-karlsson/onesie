package assert

import "testing"

func TestFields(t *testing.T) {
	t.Parallel()

	t.Run("should give every entry a complete set of behaviours", func(t *testing.T) {
		t.Parallel()

		for _, f := range fields {
			if f.available == nil || f.typeOf == nil || f.read == nil {
				t.Errorf("%q is missing a behaviour, which would panic at use", f.name)
			}
		}
	})
}
