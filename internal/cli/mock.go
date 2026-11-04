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
	"github.com/frodi-karlsson/onesie/internal/runner"
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
) (runner.AnswererFactory, error) {
	data, err := settings.readFile(flags.mockPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flags.mockSpelled, err)
	}

	asked := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		asked = append(asked, question.ID)
	}

	answers, err := mock.Load(bytes.NewReader(data), known, mock.Options{
		ByID: flags.idSource != "", Spelled: flags.mockSpelled, Asked: asked, Provider: provider,
	})
	if err != nil {
		return nil, err
	}

	return func(_ context.Context, stats *runner.Collector) (runner.Answerer, error) {
		stats.AnswerFromMock()

		client, err := mockClient(settings, flags, provider, stats)
		if err != nil {
			return nil, err
		}

		return mockAnswerer{answers: answers, provider: provider, live: explainedAnswerer(client)}, nil
	}, nil
}

func mockClient(
	settings rootSettings, flags runConfig, provider onesie.Provider, stats *runner.Collector,
) (*onesie.Client, error) {
	opts := []onesie.Option{
		onesie.WithProvider(provider),
		onesie.WithEnv(settings.lookupEnv),
		onesie.WithAPIKey(mockKey),
		onesie.WithBaseURL(mockBaseURL),
		onesie.WithHTTPClient(&http.Client{Transport: settings.mockTransport(provider)}),
		onesie.WithClock(instantClock{now: settings.now}),
		onesie.WithRetry(retryPolicyOf(flags.runFlags)),
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

func (a mockAnswerer) Answer(ctx context.Context, key runner.RecordKey, req onesie.Request) (runner.Reply, error) {
	if err := ctx.Err(); err != nil {
		return runner.Reply{}, err
	}

	if err := a.provider.Check(req); err != nil {
		return runner.Reply{}, err
	}

	entry, found := a.answers.Lookup(key.Position, key.ID)
	if !found {
		return runner.Reply{}, &runner.HaltError{Err: errors.New(a.answers.Missing(key.Position, key.ID, key.Line))}
	}

	return a.live.Answer(mock.WithEntry(ctx, entry, req.Model), key, req)
}

func (a mockAnswerer) Salt(key runner.RecordKey) (string, bool) {
	// A record the file does not answer is never shared, so its error names its own input line and
	// no identical record takes an answer the file gave another.
	entry, found := a.answers.Lookup(key.Position, key.ID)
	if !found {
		return "", false
	}

	return entry.Key(), true
}

type mockAnswerer struct {
	answers  *mock.Answers
	provider onesie.Provider
	live     runner.LiveAnswerer
}
