package runner

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestJudge(t *testing.T) {
	t.Parallel()

	const (
		holds = "answer.value > 0.5"
		fails = "answer.value > 0.95"
	)

	tests := []struct {
		name          string
		gate          string
		abstain       string
		wantFailed    bool
		wantAbstained bool
	}{
		{
			name: "should be yes when the assertion holds and the abstain expression holds too",
			gate: holds, abstain: holds,
		},
		{
			name: "should be yes when the assertion holds and the abstain expression is false",
			gate: holds, abstain: fails,
		},
		{
			name: "should be unsure when the assertion is false and the abstain expression holds",
			gate: fails, abstain: holds, wantAbstained: true,
		},
		{
			name: "should be no when both are false",
			gate: fails, abstain: fails, wantFailed: true,
		},
		{
			name: "should be no when there is no abstain expression and the assertion is false",
			gate: fails, wantFailed: true,
		},
		{
			name: "should be yes when there is neither an assertion nor an abstain expression",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := output.Record{Answers: []output.Named{
				{ID: "answer", Answer: &answer.Answer{Value: 0.9}},
			}}

			stats := &Collector{}

			got := Judge(parsed(t, tc.gate), parsed(t, tc.abstain), record, stats)

			if got.AssertFailed != tc.wantFailed {
				t.Errorf("AssertFailed = %v, want %v", got.AssertFailed, tc.wantFailed)
			}

			if got.Abstained != tc.wantAbstained {
				t.Errorf("Abstained = %v, want %v", got.Abstained, tc.wantAbstained)
			}

			snapshot := stats.Snapshot(0)

			if want := count(tc.wantFailed); snapshot.FalseAsserts != want {
				t.Errorf("FalseAsserts = %d, want %d", snapshot.FalseAsserts, want)
			}

			if want := count(tc.wantAbstained); snapshot.Abstains != want {
				t.Errorf("Abstains = %d, want %d", snapshot.Abstains, want)
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

func count(counted bool) int {
	if counted {
		return 1
	}

	return 0
}

func TestAsk_answered(t *testing.T) {
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

		ask := Ask{Plan: built, Model: "m", Questions: plan.WireAll(built.Questions), Usage: true, Message: text}

		record, _, _, err := ask.answered(t.Context(), asker, RecordKey{Position: 1, Line: 1}, "x", nil)
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

func (a resultAnswerer) Answer(context.Context, RecordKey, onesie.Request) (Reply, error) {
	return Reply{Result: a.result()}, nil
}

func (resultAnswerer) Salt(RecordKey) (string, bool) {
	return "", true
}

type resultAnswerer struct {
	result func() *onesie.Result
}

func TestDescribe(t *testing.T) {
	t.Parallel()

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
				cause: &advised{cause: &onesie.APIError{Status: http.StatusPaymentRequired}, message: "402. Add credits"},
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
			got := Describe(pin.cause, text)

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

func (e *advised) Error() string {
	return e.message
}

func (e *advised) Unwrap() error {
	return e.cause
}

type advised struct {
	cause   error
	message string
}
