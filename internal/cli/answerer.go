package cli

import (
	"context"

	"github.com/frodi-karlsson/onesie-go"
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

func liveAnswers(settings rootSettings, flags runConfig) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		chosen, err := settings.holder.resolved()
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, err
		}

		return newLiveAnswerer(client, chosen.provider), nil
	}
}

func newLiveAnswerer(client *onesie.Client, provider onesie.Provider) liveAnswerer {
	return liveAnswerer{client: client, provider: provider}
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, req onesie.Request) (reply, error) {
	result, err := a.client.SystemOne(ctx, req)

	return reply{result: result}, explain(err, requestFacts{
		provider: a.provider, model: req.Model, longContext: req.LongContext, boxed: hasBox(req.Questions),
	})
}

func (liveAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type liveAnswerer struct {
	client   *onesie.Client
	provider onesie.Provider
}
