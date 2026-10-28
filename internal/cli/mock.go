package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/mock"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	envMock  = "ONESIE_MOCK"
	flagMock = "--mock"
)

func mockSource(lookupEnv func(string) (string, bool), flag string) (path, spelled string) {
	if flag != "" {
		return flag, flagMock
	}

	if value, found := lookupEnv(envMock); found && value != "" {
		return value, envMock
	}

	return "", ""
}

func mockAnswers(
	settings rootSettings, flags runConfig, provider onesie.Provider, built *plan.Plan, known []plan.Question,
) (answererFactory, error) {
	data, err := settings.readFile(flags.mockPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flags.mockSpelled, err)
	}

	asked := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		asked = append(asked, question.ID)
	}

	answers, err := mock.Load(bytes.NewReader(data), known, mock.Options{
		ByID: flags.idSource != "", Spelled: flags.mockSpelled, Timeout: time.Duration(flags.timeout) * time.Second,
		Asked: asked,
	})
	if err != nil {
		return nil, err
	}

	return func(_ context.Context, stats *collector) (answerer, error) {
		stats.answerFromMock()

		return mockAnswerer{answers: answers, stats: stats, provider: provider}, nil
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

func (a mockAnswerer) answer(ctx context.Context, key recordKey, req onesie.Request) (reply, error) {
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}

	if err := a.provider.Check(req); err != nil {
		return reply{}, err
	}

	entry, found := a.answers.Lookup(key.position, key.id)
	if !found {
		return reply{}, &uncoveredError{message: a.answers.Missing(key.position, key.id, key.line)}
	}

	result, err := entry.Result()
	a.stats.observe(mockAttempt(err))

	if err != nil {
		return reply{}, err
	}

	result.Model = req.Model

	return reply{result: result}, nil
}

func (a mockAnswerer) salt(key recordKey) (string, bool) {
	// A record the file does not answer is never shared, so its error names its own input line and
	// no identical record takes an answer the file gave another.
	entry, found := a.answers.Lookup(key.position, key.id)
	if !found {
		return "", false
	}

	return entry.Key(), true
}

func mockAttempt(err error) onesie.Attempt {
	var api *onesie.APIError
	if errors.As(err, &api) {
		return onesie.Attempt{Status: api.Status, Err: err}
	}

	var connection *onesie.ConnectionError
	if errors.As(err, &connection) {
		return onesie.Attempt{Err: err}
	}

	return onesie.Attempt{Status: http.StatusOK}
}

type mockAnswerer struct {
	answers  *mock.Answers
	stats    *collector
	provider onesie.Provider
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
