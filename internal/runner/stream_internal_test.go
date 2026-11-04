package runner

import (
	"errors"
	"net/http"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestAborting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "should abort on a refused key", status: http.StatusUnauthorized, want: true},
		{name: "should abort on a forbidden key", status: http.StatusForbidden, want: true},
		{name: "should abort when the account is out of credits", status: http.StatusPaymentRequired, want: true},
		{name: "should carry on after a bad request", status: http.StatusBadRequest},
		{name: "should carry on after a server error", status: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := Aborting(&onesie.APIError{Status: tc.status}); got != tc.want {
				t.Errorf("aborting(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestAuthFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "should match a refused key", err: &onesie.APIError{Status: http.StatusUnauthorized}, want: true},
		{name: "should match a forbidden key", err: &onesie.APIError{Status: http.StatusForbidden}, want: true},
		{
			name: "should match an account out of credits",
			err:  &onesie.APIError{Status: http.StatusPaymentRequired}, want: true,
		},
		{
			name: "should match a refusal wrapped in advice",
			err:  &advised{cause: &onesie.APIError{Status: http.StatusUnauthorized}}, want: true,
		},
		{name: "should not match a bad request", err: &onesie.APIError{Status: http.StatusBadRequest}},
		{name: "should not match a missing key", err: onesie.ErrNoAPIKey},
		{name: "should not match a record the mock does not answer", err: &HaltError{Err: errors.New("x")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := AuthFailure(tc.err); got != tc.want {
				t.Errorf("authFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
