package settings

import "errors"

func cacheBesideMockRule() Rule {
	return Rule{
		ID:    "cache-beside-mock",
		Reads: []Name{Cache.Name(), Mock.Name()},
		Check: func(v View) error {
			// An ambient ONESIE_MOCK answers and leaves the cache untouched, so only a typed --mock makes
			// a typed --cache pointless.
			if typed(v, Cache.Name()) && Get(v.Merged, Cache) && typed(v, Mock.Name()) {
				return errors.New("--cache stores answers from the API, which --mock replaces. Drop one")
			}

			return nil
		},
	}
}
