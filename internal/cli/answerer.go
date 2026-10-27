package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

const (
	envLongContext      = "ONESIE_LONG_CONTEXT"
	imageAttemptTimeout = 60 * time.Second
)

type answerer interface {
	answer(ctx context.Context, key recordKey, req onesie.Request) (reply, error)
	salt(key recordKey) (string, bool)
}

type reply struct {
	result *onesie.Result
	cached bool
}

type recordKey struct {
	position int
	line     int
	id       any
}

type answererFactory func(ctx context.Context, stats *collector) (answerer, error)

func liveAnswers(settings rootSettings, flags *runFlags) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		client, err := settings.newClient(ctx, observing(stats)...)
		if err != nil {
			return nil, err
		}

		chosen, err := runResolution(settings)
		if err != nil {
			return nil, err
		}

		return newLiveAnswerer(client, chosen.provider, flags), nil
	}
}

func newLiveAnswerer(client *onesie.Client, provider onesie.Provider, flags *runFlags) liveAnswerer {
	return liveAnswerer{client: client, provider: provider, timeoutSet: flags.timeoutSet}
}

func longContext(settings rootSettings, flags *runFlags) bool {
	if flags.longContextSet {
		return flags.longContext
	}

	value, _ := settings.lookupEnv(envLongContext)
	on, err := strconv.ParseBool(value)

	return err == nil && on
}

func checkSwitches(lookupEnv func(string) (string, bool)) error {
	for _, variable := range []struct{ name, flag string }{
		{envCache, "--" + flagCache},
		{envLongContext, "--long-context"},
	} {
		value, found := lookupEnv(variable.name)
		if !found || value == "" {
			continue
		}

		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("onesie: %s=%s is not a value %s takes. Use 1 or 0", variable.name, value, variable.flag)
		}
	}

	_, err := aliasTTL(lookupEnv)

	return err
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, req onesie.Request) (reply, error) {
	result, err := a.client.SystemOne(ctx, req, requestOptions(req, a.timeoutSet)...)

	// Every question on this path passed onesie's own bounds check, so a count the server rejects
	// says something about those bounds.
	return reply{result: result}, advise(adviseBox(
		adviseLongContext(readableResponse(err), longContextHelps(a.provider, req.Model, req.LongContext)),
		a.provider.Name, hasBox(req.Questions)), req.Model, true)
}

func requestOptions(req onesie.Request, timeoutSet bool) []onesie.RequestOption {
	var opts []onesie.RequestOption

	// A 32 MiB upload outlasts the 10 second default on an ordinary connection.
	if len(req.Assets) > 0 && !timeoutSet {
		opts = append(opts, onesie.WithRequestAttemptTimeout(imageAttemptTimeout))
	}

	return opts
}

func (liveAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type liveAnswerer struct {
	client     *onesie.Client
	provider   onesie.Provider
	timeoutSet bool
}
