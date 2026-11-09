package output_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestVerdict_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict output.Verdict
		want    string
	}{
		{name: "should name a record answered with no gate", verdict: output.Answered, want: "answered"},
		{name: "should name a pass", verdict: output.Pass, want: "pass"},
		{name: "should name a fail", verdict: output.Fail, want: "fail"},
		{name: "should name an unsure", verdict: output.Unsure, want: "unsure"},
		{name: "should name an error", verdict: output.Error, want: "error"},
		{name: "should show the number of a verdict it does not know", verdict: output.Verdict(9), want: "Verdict(9)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.verdict.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecord_Verdict(t *testing.T) {
	t.Parallel()

	failure := &output.Failure{Kind: output.KindHTTP, Message: "boom"}

	tests := []struct {
		name   string
		record output.Record
		want   output.Verdict
	}{
		{name: "should be answered when nothing judged the record", record: output.Record{}, want: output.Answered},
		{name: "should be the gate's pass", record: output.Record{Gate: output.Pass}, want: output.Pass},
		{name: "should be the gate's fail", record: output.Record{Gate: output.Fail}, want: output.Fail},
		{name: "should be the gate's unsure", record: output.Record{Gate: output.Unsure}, want: output.Unsure},
		{name: "should be an error when the record carries a failure", record: output.Record{Failure: failure}, want: output.Error},
		{
			name:   "should be an error whatever the gate holds beside a failure",
			record: output.Record{Gate: output.Fail, Failure: failure},
			want:   output.Error,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.record.Verdict(); got != tc.want {
				t.Errorf("Verdict() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict output.Verdict
		count   int
		want    string
	}{
		{name: "should write one error in the singular", verdict: output.Error, count: 1, want: "1 error"},
		{name: "should write two errors in the plural", verdict: output.Error, count: 2, want: "2 errors"},
		{name: "should write a fail count with the verdict word as it is", verdict: output.Fail, count: 2, want: "2 fail"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := output.Count(tc.verdict, tc.count); got != tc.want {
				t.Errorf("Count(%v, %d) = %q, want %q", tc.verdict, tc.count, got, tc.want)
			}
		})
	}
}
