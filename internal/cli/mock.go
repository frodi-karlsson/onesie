package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/mock"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	envMock  = "ONESIE_MOCK"
	flagMock = "--mock"
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
	settings rootSettings, path, spelled string, built *plan.Plan, byID bool,
) (answererFactory, error) {
	data, err := settings.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("onesie: %s: %w", spelled, err)
	}

	answers, err := mock.Load(bytes.NewReader(data), built.Questions, byID)
	if err != nil {
		// The package names the flag, and a run that took the file from the variable names that.
		return nil, errors.New(strings.Replace(err.Error(), flagMock, spelled, 1))
	}

	return func(_ context.Context, stats *collector) (answerer, error) {
		return mockAnswerer{answers: answers, spelled: spelled, stats: stats}, nil
	}, nil
}

func (a mockAnswerer) answer(ctx context.Context, key recordKey, _ jev.Request) (*jev.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entry, found := a.answers.Lookup(key.position, key.id)
	if !found {
		return nil, &uncoveredError{spelled: a.spelled, line: key.line, id: key.id}
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
	spelled string
	stats   *collector
}

func (e *uncoveredError) Error() string {
	if e.id != nil {
		return fmt.Sprintf("onesie: %s has no answer for id '%s'. Add a line for it", e.spelled, jq.IDText(e.id))
	}

	return fmt.Sprintf("onesie: %s has no answer for line %d. Add an entry for it", e.spelled, e.line)
}

type uncoveredError struct {
	spelled string
	line    int
	id      any
}

func uncovered(err error) bool {
	var missing *uncoveredError

	return errors.As(err, &missing)
}
