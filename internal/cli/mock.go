package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/mock"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	envMock  = plan.MockVariable
	flagMock = plan.MockFlag
)

func mockSource(settings rootSettings, flags *runFlags) (path, spelled string) {
	if flags.mock != "" {
		return flags.mock, flagMock
	}

	if value, found := settings.lookupEnv(envMock); found && value != "" {
		return value, envMock
	}

	return "", ""
}

func mockAnswers(
	settings rootSettings, flags *runFlags, path, spelled string, built *plan.Plan,
) (answererFactory, error) {
	data, err := settings.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("onesie: %s: %w", spelled, err)
	}

	answers, err := mock.Load(bytes.NewReader(data), built.Questions, mock.Options{
		ByID: flags.idSource != "", Spelled: spelled, Timeout: time.Duration(flags.timeout) * time.Second,
	})
	if err != nil {
		return nil, err
	}

	return func(_ context.Context, stats *collector) (answerer, error) {
		return mockAnswerer{answers: answers, stats: stats}, nil
	}, nil
}

func wrapped(settings rootSettings, answers answererFactory) answererFactory {
	if settings.wrapAnswerer == nil {
		return answers
	}

	return func(ctx context.Context, stats *collector) (answerer, error) {
		inner, err := answers(ctx, stats)
		if err != nil {
			return nil, err
		}

		return settings.wrapAnswerer(inner), nil
	}
}

func (a mockAnswerer) answer(ctx context.Context, key recordKey, _ jev.Request) (*jev.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entry, found := a.answers.Lookup(key.position, key.id)
	if !found {
		return nil, &uncoveredError{message: a.answers.Missing(key.position, key.id, key.line)}
	}

	result, err := entry.Result()
	a.stats.observe(mockAttempt(err))

	return result, err
}

func mockAttempt(err error) jev.Attempt {
	var api *jev.APIError
	if errors.As(err, &api) {
		return jev.Attempt{Status: api.Status, Err: err}
	}

	var connection *jev.ConnectionError
	if errors.As(err, &connection) {
		return jev.Attempt{Err: err}
	}

	return jev.Attempt{Status: http.StatusOK}
}

type mockAnswerer struct {
	answers *mock.Answers
	stats   *collector
}

func (e *uncoveredError) Error() string {
	return e.message
}

type uncoveredError struct {
	message string
}

func uncovered(err error) bool {
	var missing *uncoveredError

	return errors.As(err, &missing)
}
