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

func liveAnswers(settings rootSettings) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		client, err := settings.newClient(ctx, observing(stats)...)
		if err != nil {
			return nil, err
		}

		return liveAnswerer{client: client}, nil
	}
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, req onesie.Request) (reply, error) {
	result, err := a.client.SystemOne(ctx, req)

	return reply{result: result}, err
}

func (liveAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type liveAnswerer struct {
	client *onesie.Client
}
