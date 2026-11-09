package cli

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestJudge(t *testing.T) {
	t.Parallel()

	const (
		holds = "answer.value > 0.5"
		fails = "answer.value > 0.95"
	)

	tests := []struct {
		name    string
		gate    string
		abstain string
		want    output.Verdict
	}{
		{
			name: "should pass when the assertion holds and the abstain expression holds too",
			gate: holds, abstain: holds, want: output.Pass,
		},
		{
			name: "should pass when the assertion holds and the abstain expression is false",
			gate: holds, abstain: fails, want: output.Pass,
		},
		{
			name: "should be unsure when the assertion is false and the abstain expression holds",
			gate: fails, abstain: holds, want: output.Unsure,
		},
		{
			name: "should fail when both are false",
			gate: fails, abstain: fails, want: output.Fail,
		},
		{
			name: "should fail when there is no abstain expression and the assertion is false",
			gate: fails, want: output.Fail,
		},
		{
			name: "should be answered when there is neither an assertion nor an abstain expression",
			want: output.Answered,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := output.Record{Answers: []output.Named{
				{ID: "answer", Answer: &answer.Answer{Value: 0.9}},
			}}

			got := judge(gated(parsed(t, tc.gate), parsed(t, tc.abstain)), record)

			if got.Gate != tc.want {
				t.Errorf("Gate = %v, want %v", got.Gate, tc.want)
			}
		})
	}
}

func parsed(t *testing.T, source string) *assert.Expr {
	t.Helper()

	if source == "" {
		return nil
	}

	expr, err := assert.Parse(source)
	if err != nil {
		t.Fatalf("Parse(%q): %v", source, err)
	}

	return expr
}

func TestNoState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved input.Resolved
		want     string
	}{
		{
			name:     "should ask for a state when nothing was read",
			resolved: input.Resolved{Source: input.SourceNone},
			want:     "no state given. Pipe one to stdin, or pass --state",
		},
		{
			name:     "should say stdin was empty when it was read and held nothing",
			resolved: input.Resolved{Source: input.SourceNone, EmptyStdin: true},
			want:     "no state given, since stdin was empty. Pipe one to stdin, or pass --state",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := noState(tc.resolved); got == nil || got.Error() != tc.want {
				t.Errorf("noState = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestStreamResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		failed int
		judged output.Tally
		want   int
	}{
		{
			name: "should exit six when an error sits beside the rest", failed: 1,
			judged: output.Tally{output.Fail: 1, output.Unsure: 1}, want: ExitRecords,
		},
		{
			name:   "should exit one when a fail sits beside an unsure",
			judged: output.Tally{output.Fail: 1, output.Unsure: 1}, want: ExitFail,
		},
		{name: "should exit seven when an unsure is all the stream holds", judged: output.Tally{output.Unsure: 2}, want: ExitAbstain},
		{
			name:   "should exit zero when every record passed or answered",
			judged: output.Tally{output.Pass: 1, output.Answered: 1}, want: ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := streamResult(engine.Result{Failed: tc.failed}, tc.judged)

			if got := Classify(err); got != tc.want {
				t.Errorf("exit = %d, want %d, from %v", got, tc.want, err)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	apiError := func(status int) error {
		return &onesie.APIError{Status: status}
	}

	tests := []struct {
		name  string
		cause error
	}{
		{name: "should agree with a fresh run on a line onesie could not read", cause: &input.LineError{
			Line: 1, Err: errors.New("not valid JSON"),
		}},
		{name: "should agree with a fresh run on an unusable response", cause: &onesie.ResponseError{
			Status: http.StatusOK,
		}},
		{name: "should agree with a fresh run on a bad request", cause: apiError(http.StatusBadRequest)},
		{name: "should agree with a fresh run on an unauthorized status", cause: apiError(http.StatusUnauthorized)},
		{name: "should agree with a fresh run on a payment required status", cause: &advisedError{
			cause: apiError(http.StatusPaymentRequired), message: "402. Add credits",
		}},
		{name: "should agree with a fresh run on a forbidden status", cause: apiError(http.StatusForbidden)},
		{
			name: "should agree with a fresh run on a cloudflare 400 that refuses the token",
			cause: onesie.NewAPIError(onesie.Cloudflare(), "", onesie.Needs{}, http.StatusBadRequest,
				[]byte(`{"success":false,"errors":[{"code":9106,"message":"Authentication failed (status: 400)"}]}`)),
		},
		{name: "should agree with a fresh run on a not found status", cause: apiError(http.StatusNotFound)},
		{name: "should agree with a fresh run on a request timeout status", cause: apiError(http.StatusRequestTimeout)},
		{name: "should agree with a fresh run on a rate limit", cause: apiError(http.StatusTooManyRequests)},
		{name: "should agree with a fresh run on a server error", cause: apiError(http.StatusBadGateway)},
		{name: "should agree with a fresh run on a retry after above the cap", cause: &onesie.RetryAfterError{
			APIError: onesie.APIError{Status: http.StatusServiceUnavailable},
		}},
		{name: "should agree with a fresh run on a connection error", cause: &onesie.ConnectionError{
			Err: errors.New("connection refused"),
		}},
		{name: "should agree with a fresh run on a request timeout", cause: &onesie.TimeoutError{}},
		{name: "should agree with a fresh run on a deadline", cause: context.DeadlineExceeded},
		{name: "should agree with a fresh run on a request onesie refused to send", cause: &onesie.ValidationError{
			Message: "question too long",
		}},
		{name: "should agree with a fresh run on an unrecognised plain error", cause: errors.New("boom")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := &storedFailure{failure: *describe(tc.cause)}
			stopped := streamResult(engine.Result{Aborted: true, Cause: tc.cause}, output.Tally{})

			if got, want := stored.freshRunCode(), Classify(stopped); got != want {
				t.Errorf("stored %+v exits %d, want %d as a fresh run under --stop-on=error does", stored.failure, got, want)
			}

			if strings.HasPrefix(stored.failure.Message, "onesie: ") {
				t.Errorf("message = %q, want no onesie prefix, as a line error has none", stored.failure.Message)
			}
		})
	}

	t.Run("should keep the kind, status and message each failure is written with", func(t *testing.T) {
		t.Parallel()

		statusOf := func(status int) *int {
			return &status
		}

		pins := []struct {
			cause   error
			kind    output.Kind
			status  *int
			message string
		}{
			{cause: &input.LineError{Line: 2, Err: errors.New("not json")}, kind: "input", message: "line 2: not json"},
			{
				cause: &onesie.ResponseError{Status: http.StatusOK, Err: errors.New("wrong shape")},
				kind:  "response", status: statusOf(http.StatusOK), message: "200 response could not be used: wrong shape",
			},
			{
				cause: &onesie.APIError{Status: http.StatusBadRequest},
				kind:  "http", status: statusOf(http.StatusBadRequest),
			},
			{
				cause: &advisedError{cause: &onesie.APIError{Status: http.StatusPaymentRequired}, message: "402. Add credits"},
				kind:  "auth", status: statusOf(http.StatusPaymentRequired), message: "402. Add credits",
			},
			{
				cause: &onesie.RetryAfterError{APIError: onesie.APIError{Status: http.StatusServiceUnavailable}},
				kind:  "http", status: statusOf(http.StatusServiceUnavailable),
				message: "status 503, the server asked to retry after 0s, above the 0s cap",
			},
			{
				cause: &onesie.ConnectionError{Err: errors.New("refused")},
				kind:  "transport", message: "connection error: refused",
			},
			{
				cause: &onesie.TimeoutError{Timeout: time.Second},
				kind:  "transport", message: "request timed out after 1s",
			},
			{cause: context.DeadlineExceeded, kind: "transport", message: "context deadline exceeded"},
			{cause: context.Canceled, kind: "input", message: "context canceled"},
			{
				cause: &onesie.ValidationError{Message: "question too long"},
				kind:  "input", message: "question too long",
			},
			{cause: errors.New("boom"), kind: "input", message: "boom"},
		}

		for _, pin := range pins {
			got := describe(pin.cause)

			if got.Kind != pin.kind || !sameStatus(got.Status, pin.status) || got.Message != pin.message {
				t.Errorf("describe(%v) = %s %v %q, want %s %v %q",
					pin.cause, got.Kind, got.Status, got.Message, pin.kind, pin.status, pin.message)
			}
		}
	})
}

func sameStatus(got, want *int) bool {
	if got == nil || want == nil {
		return got == want
	}

	return *got == *want
}

func TestAnswered(t *testing.T) {
	t.Parallel()

	t.Run("should keep no reference to the response in a record under --usage", func(t *testing.T) {
		t.Parallel()

		built := &plan.Plan{Questions: []plan.Question{{ID: "answer", Shape: plan.Noul}}}
		collected := make(chan struct{})

		asker := resultAnswerer{result: func() *onesie.Result {
			result := &onesie.Result{
				Model:   "m",
				Usage:   onesie.Usage{InputTokens: 5, OutputTokens: 2},
				Answers: map[string]onesie.Answer{"answer": &onesie.NoulAnswer{Noul: 0.9}},
			}
			runtime.AddCleanup(result, func(done chan struct{}) { close(done) }, collected)

			return result
		}}

		record, _, _, err := answered(t.Context(), asker, recordKey{position: 1, line: 1}, built, "m",
			plan.WireAll(built.Questions), "x", nil, true)
		if err != nil {
			t.Fatalf("answered: %v", err)
		}

		if record.Usage == nil || *record.Usage != (onesie.Usage{InputTokens: 5, OutputTokens: 2}) {
			t.Fatalf("usage = %v, want 5 in and 2 out", record.Usage)
		}

		if !released(collected) {
			t.Error("the record held on to the response it was read from")
		}

		runtime.KeepAlive(record)
	})
}

func released(collected <-chan struct{}) bool {
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		runtime.GC()

		select {
		case <-collected:
			return true
		case <-time.After(10 * time.Millisecond):
		}
	}

	return false
}

func (a resultAnswerer) answer(context.Context, recordKey, onesie.Request) (reply, error) {
	return reply{result: a.result()}, nil
}

func (resultAnswerer) salt(recordKey) (string, bool) {
	return "", true
}

type resultAnswerer struct {
	result func() *onesie.Result
}

func TestRunInputs(t *testing.T) {
	t.Parallel()

	account := strings.Repeat("ab", 16)
	other := strings.Repeat("cd", 16)
	wholeAccount := "https://api.cloudflare.com/client/v4/accounts/" + other + "/ai"

	tests := []struct {
		name     string
		chosen   resolution
		flags    runConfig
		from     setting.Source
		endpoint settingrow.EndpointValue
		want     answersfile.FingerprintInputs
	}{
		{
			name:   "should leave the base URL empty for the provider's own",
			chosen: resolution{provider: onesie.TypeSafe()},
			want:   answersfile.FingerprintInputs{Provider: "typesafe"},
		},
		{
			name:   "should leave the base URL empty when --base-url names the provider's own",
			chosen: resolution{provider: onesie.TypeSafe()}, from: setting.SourceFlag,
			endpoint: settingrow.EndpointValue{BaseURL: onesie.TypeSafe().DefaultBaseURL() + "/"},
			want:     answersfile.FingerprintInputs{Provider: "typesafe"},
		},
		{
			name:   "should fingerprint --base-url",
			chosen: resolution{provider: onesie.TypeSafe()}, from: setting.SourceFlag,
			endpoint: settingrow.EndpointValue{BaseURL: "https://onesie.example.com/"},
			want:     answersfile.FingerprintInputs{Provider: "typesafe", BaseURL: "https://onesie.example.com"},
		},
		{
			name:   "should fingerprint ONESIE_BASE_URL",
			chosen: resolution{provider: onesie.TypeSafe()}, from: setting.SourceEnv,
			endpoint: settingrow.EndpointValue{BaseURL: "https://onesie.example.com"},
			want:     answersfile.FingerprintInputs{Provider: "typesafe", BaseURL: "https://onesie.example.com"},
		},
		{
			name:   "should fingerprint a profile's base URL and name",
			chosen: resolution{provider: onesie.TypeSafe(), profile: "work"}, from: setting.SourceChosenProfile,
			endpoint: settingrow.EndpointValue{BaseURL: "https://onesie.example.com"},
			want: answersfile.FingerprintInputs{
				Provider: "typesafe", Profile: "work", BaseURL: "https://onesie.example.com",
			},
		},
		{
			name:   "should fingerprint the cloudflare account from the environment",
			chosen: resolution{provider: onesie.Cloudflare()}, from: setting.SourceEnv,
			endpoint: settingrow.EndpointValue{AccountID: account},
			want:     answersfile.FingerprintInputs{Provider: "cloudflare", AccountID: account},
		},
		{
			name:   "should fingerprint a profile's cloudflare account",
			chosen: resolution{provider: onesie.Cloudflare(), profile: "edge"}, from: setting.SourceChosenProfile,
			endpoint: settingrow.EndpointValue{AccountID: other},
			want:     answersfile.FingerprintInputs{Provider: "cloudflare", Profile: "edge", AccountID: other},
		},
		{
			name:   "should fingerprint a whole cloudflare account URL as the base URL",
			chosen: resolution{provider: onesie.Cloudflare()}, from: setting.SourceFlag,
			endpoint: settingrow.EndpointValue{BaseURL: wholeAccount},
			want:     answersfile.FingerprintInputs{Provider: "cloudflare", BaseURL: wholeAccount},
		},
		{
			name:   "should leave out the base URL a mock run ignores",
			chosen: resolution{provider: onesie.TypeSafe()}, from: setting.SourceFlag,
			endpoint: settingrow.EndpointValue{BaseURL: "https://onesie.example.com"},
			flags:    runConfig{mockPath: "m.json", mockSpelled: "--mock"},
			want:     answersfile.FingerprintInputs{Provider: "typesafe", Mock: true},
		},
		{
			name:   "should leave --usage to the caller, since calibrate does not fingerprint it",
			chosen: resolution{provider: onesie.TypeSafe()},
			flags:  runWith(map[string]any{"usage": true}),
			want:   answersfile.FingerprintInputs{Provider: "typesafe"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var layers []setting.Layer
			if tc.from != setting.SourceDefault {
				layers = append(layers, setting.Layer{
					Source: tc.from, Values: map[settingrow.Name]any{"endpoint": tc.endpoint},
				})
			}

			flags := tc.flags
			flags.merged, _ = setting.Merge(settingrow.Table(), layers, setting.Choice{})
			flags.merged.Set(setting.Profile.Name(), tc.chosen.profile, setting.SourceDefaultProfile)

			settings := rootSettings{
				holder:    &holder{resolved: func() (resolution, error) { return tc.chosen, nil }},
				lookupEnv: lookupFrom(nil),
			}

			got, err := runInputs(settings, flags, answersfile.FingerprintInputs{})
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("inputs = %+v, want %+v", got, tc.want)
			}
		})
	}
}
