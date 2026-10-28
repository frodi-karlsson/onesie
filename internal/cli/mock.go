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
	envMock     = "ONESIE_MOCK"
	flagMock    = "--mock"
	mockKey     = "mock-key"
	mockBaseURL = "https://mock.invalid"
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
		ByID: flags.idSource != "", Spelled: flags.mockSpelled, Asked: asked,
	})
	if err != nil {
		return nil, err
	}

	return func(_ context.Context, stats *collector) (answerer, error) {
		stats.answerFromMock()

		client, err := mockClient(settings, flags, provider, stats)
		if err != nil {
			return nil, err
		}

		return mockAnswerer{answers: answers, provider: provider, live: newLiveAnswerer(client)}, nil
	}, nil
}

func mockClient(
	settings rootSettings, flags runConfig, provider onesie.Provider, stats *collector,
) (*onesie.Client, error) {
	opts := []onesie.Option{
		onesie.WithProvider(provider),
		onesie.WithEnv(settings.lookupEnv),
		onesie.WithAPIKey(mockKey),
		onesie.WithBaseURL(mockBaseURL),
		onesie.WithHTTPClient(&http.Client{Transport: settings.mockTransport(provider)}),
		onesie.WithClock(instantClock{now: settings.now}),
		onesie.WithRetry(retryPolicy(flags.runFlags)),
	}

	return onesie.New(append(opts, runOptions(flags, stats)...)...)
}

func (c instantClock) Now() time.Time {
	return c.now()
}

func (instantClock) Sleep(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

type instantClock struct {
	now func() time.Time
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

	return a.live.answer(mock.WithEntry(ctx, entry, req.Model), key, req)
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

type mockAnswerer struct {
	answers  *mock.Answers
	provider onesie.Provider
	live     liveAnswerer
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
