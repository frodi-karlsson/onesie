package cli

import (
	"context"
	"strings"
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

		return newLiveAnswerer(client, settings, flags), nil
	}
}

func newLiveAnswerer(client *onesie.Client, settings rootSettings, flags *runFlags) liveAnswerer {
	return liveAnswerer{client: client, timeoutSet: flags.timeoutSet, longContext: longContext(settings, flags)}
}

func longContext(settings rootSettings, flags *runFlags) bool {
	if flags.longContext {
		return true
	}

	value, _ := settings.lookupEnv(envLongContext)

	return value == "1" || strings.EqualFold(value, "true")
}

func (a liveAnswerer) answer(ctx context.Context, _ recordKey, req onesie.Request) (reply, error) {
	result, err := a.client.SystemOne(ctx, req, requestOptions(req, a.timeoutSet, a.longContext)...)

	return reply{result: result}, adviseLongContext(err, a.longContext)
}

func requestOptions(req onesie.Request, timeoutSet, longContext bool) []onesie.RequestOption {
	var opts []onesie.RequestOption

	// A 32 MiB upload outlasts the 10 second default on an ordinary connection.
	if len(req.Assets) > 0 && !timeoutSet {
		opts = append(opts, onesie.WithRequestAttemptTimeout(imageAttemptTimeout))
	}

	if longContext {
		opts = append(opts, onesie.WithRequestHeader(onesie.HeaderLongContext, "true"))
	}

	return opts
}

func (liveAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type liveAnswerer struct {
	client      *onesie.Client
	timeoutSet  bool
	longContext bool
}
