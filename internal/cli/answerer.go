package cli

import (
	"context"

	"github.com/frodi-karlsson/onesie-go"
)

func liveAnswers(settings rootSettings, flags runConfig) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		chosen, err := connected(settings, flags)
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, err
		}

		return newLiveAnswerer(client), nil
	}
}

type answererFactory func(ctx context.Context, stats *collector) (answerer, error)

type answerer interface {
	answer(ctx context.Context, key recordKey, ready prepared) (reply, error)
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

func newLiveAnswerer(client *onesie.Client) liveAnswerer {
	return liveAnswerer{client: client}
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, ready prepared) (reply, error) {
	result, _, err := a.client.SendPrepared(ctx, ready.body)

	return reply{result: result}, explain(err, requestFacts{
		provider: a.client.Provider(), model: ready.body.Model(), longContext: ready.body.LongContext(),
		images: ready.body.Images(), boxed: ready.body.Box(),
	})
}

func (liveAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type liveAnswerer struct {
	client *onesie.Client
}
