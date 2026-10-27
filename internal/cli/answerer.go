package cli

import (
	"context"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

const imageAttemptTimeout = 60 * time.Second

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

func liveAnswers(settings rootSettings, flags runConfig) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		chosen, err := runResolution(settings)
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, observing(stats)...)
		if err != nil {
			return nil, err
		}

		return newLiveAnswerer(client, chosen.provider, flags), nil
	}
}

func newLiveAnswerer(client *onesie.Client, provider onesie.Provider, flags runConfig) liveAnswerer {
	return liveAnswerer{client: client, provider: provider, timeoutSet: flags.timeoutSet}
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, req onesie.Request) (reply, error) {
	result, err := a.client.SystemOne(ctx, req, requestOptions(req, a.timeoutSet)...)

	// Every question on this path passed onesie's own bounds check, so a count the server rejects
	// says something about those bounds.
	return reply{result: result}, explain(err, requestFacts{
		provider: a.provider, model: req.Model, longContext: req.LongContext, boxed: hasBox(req.Questions),
		validated: true,
	})
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
