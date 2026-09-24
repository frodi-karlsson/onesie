package jq

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/itchyny/gojq"
	"github.com/itchyny/timefmt-go"
)

const clockModule = `
def now: _onesie_now;
def localtime: _onesie_localtime;
def strflocaltime(format): _onesie_strflocaltime(format);
`

func clockOptions(now func() time.Time) []gojq.CompilerOption {
	return []gojq.CompilerOption{
		// gojq resolves its own builtins before any function WithFunction adds, so a builtin cannot
		// be replaced by name. A definition from an init module is resolved before either, which is
		// what routes the builtins that read the clock to functions reading the injected one. A
		// loader also takes over import and include, so it refuses both, as gojq does with no loader.
		gojq.WithModuleLoader(initModule{source: clockModule}),
		gojq.WithFunction("_onesie_now", 0, 0, func(any, []any) any {
			return epochOf(now())
		}),
		gojq.WithFunction("_onesie_localtime", 0, 0, func(value any, _ []any) any {
			seconds, ok := toFloat(value)
			if !ok {
				return typeError("localtime", value)
			}

			return brokenDown(seconds, now().Location())
		}),
		gojq.WithFunction("_onesie_strflocaltime", 1, 1, func(value any, args []any) any {
			return strflocaltime(value, args[0], now().Location())
		}),
	}
}

type initModule struct {
	source string
}

func (m initModule) LoadInitModules() ([]*gojq.Query, error) {
	query, err := gojq.Parse(m.source)
	if err != nil {
		return nil, err
	}

	return []*gojq.Query{query}, nil
}

func (initModule) LoadModule(path string) (*gojq.Query, error) {
	return nil, fmt.Errorf("cannot load module: %q", path)
}

func (initModule) LoadJSON(path string) (any, error) {
	return nil, fmt.Errorf("cannot load module: %q", path)
}

func strflocaltime(value, format any, zone *time.Location) any {
	layout, ok := format.(string)
	if !ok {
		return typeError("strflocaltime", format)
	}

	if seconds, isNumber := toFloat(value); isNumber {
		return timefmt.Format(timeOf(seconds).In(zone), layout)
	}

	parts, ok := value.([]any)
	if !ok {
		return typeError("strflocaltime", value)
	}

	moment, err := fromBrokenDown(parts, zone)
	if err != nil {
		return err
	}

	return timefmt.Format(moment, layout)
}

func epochOf(moment time.Time) float64 {
	return float64(moment.Unix()) + float64(moment.Nanosecond())/1e9
}

func timeOf(seconds float64) time.Time {
	whole := math.Floor(seconds)

	return time.Unix(int64(whole), int64((seconds-whole)*1e9))
}

func brokenDown(seconds float64, zone *time.Location) []any {
	moment := timeOf(seconds).In(zone)

	return []any{
		moment.Year(),
		int(moment.Month()) - 1,
		moment.Day(),
		moment.Hour(),
		moment.Minute(),
		float64(moment.Second()) + float64(moment.Nanosecond())/1e9,
		int(moment.Weekday()),
		moment.YearDay() - 1,
	}
}

func fromBrokenDown(parts []any, zone *time.Location) (time.Time, error) {
	var fields [6]float64

	for i := range min(len(parts), len(fields)) {
		field, ok := toFloat(parts[i])
		if !ok {
			return time.Time{}, errors.New("strflocaltime: expected an array of 8 numbers")
		}

		fields[i] = field
	}

	second := math.Floor(fields[5])

	return time.Date(
		int(fields[0]), time.Month(int(fields[1])+1), int(fields[2]),
		int(fields[3]), int(fields[4]), int(second), int((fields[5]-second)*1e9), zone,
	), nil
}

func toFloat(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case float64:
		return number, true
	case *big.Int:
		converted, _ := new(big.Float).SetInt(number).Float64()

		return converted, true
	case json.Number:
		converted, err := number.Float64()

		return converted, err == nil
	default:
		return 0, false
	}
}

func typeError(name string, value any) error {
	return fmt.Errorf("%s cannot be applied to: %s", name, gojq.TypeOf(value))
}
