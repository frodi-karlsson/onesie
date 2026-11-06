package settings

import "fmt"

func cacheBesideMockRule() Rule {
	return Rule{
		ID:    "cache-beside-mock",
		Reads: []Name{Cache.Name(), Mock.Name()},
		Check: func(v View) error {
			if !typed(v, Cache.Name()) || !Get(v.Merged, Cache) {
				return nil
			}

			switch mockSpelled(v) {
			case "--mock":
				return fmt.Errorf("--cache stores answers from the API, which --mock replaces. Drop one")
			case "ONESIE_MOCK":
				return fmt.Errorf("--cache stores answers from the API, which ONESIE_MOCK replaces. " +
					"Drop --cache or unset ONESIE_MOCK")
			default:
				return nil
			}
		},
	}
}
