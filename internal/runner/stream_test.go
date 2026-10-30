package runner_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func TestStream(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{{ID: "answer", Shape: plan.Noul}}}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "should ask a record with no images when Images is nil",
			in:   "a ticket\n",
			want: `{"line":1,"model":"m","answer":{"value":0.9}}` + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder

			stub := &requestAnswerer{}
			outcome, err := runner.Stream(t.Context(), runner.StreamConfig{
				Input:      strings.NewReader(tc.in),
				InputMode:  input.Lines,
				Out:        &out,
				ErrOut:     io.Discard,
				OutputMode: output.JSON,
				Ask: runner.Ask{
					Plan: built, Model: "m", Questions: plan.WireAll(built.Questions), Message: errorMessage,
				},
				Answerers: func(context.Context, *runner.Collector) (runner.Answerer, error) { return stub, nil },
				Stats:     &runner.Collector{},
				Jobs:      1,
			})
			if err != nil || outcome.Failed != 0 {
				t.Fatalf("Stream = %+v, %v, want every record answered", outcome, err)
			}

			if out.String() != tc.want {
				t.Errorf("output = %q, want %q", out.String(), tc.want)
			}

			if len(stub.requests) != 1 || stub.requests[0].Assets != nil {
				t.Errorf("requests = %+v, want one with no assets", stub.requests)
			}
		})
	}
}

func errorMessage(err error) string {
	return err.Error()
}

func (a *requestAnswerer) Answer(_ context.Context, _ runner.RecordKey, req onesie.Request) (runner.Reply, error) {
	a.requests = append(a.requests, req)

	return runner.Reply{Result: &onesie.Result{
		Model: "m", Answers: map[string]onesie.Answer{"answer": &onesie.NoulAnswer{Noul: 0.9}},
	}}, nil
}

func (*requestAnswerer) Salt(runner.RecordKey) (string, bool) {
	return "", true
}

type requestAnswerer struct {
	requests []onesie.Request
}
