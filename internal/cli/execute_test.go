package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "should exit 130 on an interrupt while one record is still on stdin", args: []string{"is this urgent"}},
		{name: "should exit 130 on an interrupt while a json record is still on stdin", args: []string{"is this urgent", "-i", "json"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			held, _ := io.Pipe()

			code := interruptedRun(t, tc.args,
				WithStdin(held),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(map[string]string{"TYPESAFE_API_KEY": "k"})),
				WithKeychain(noKeychain()),
				WithClientFactory(func(context.Context, ...jev.Option) (*jev.Client, error) {
					return nil, errors.New("onesie: no client should be built before stdin ends")
				}))

			if code != ExitInterrupt {
				t.Errorf("exit code = %d, want %d", code, ExitInterrupt)
			}
		})
	}
}

func interruptedRun(t *testing.T, args []string, opts ...RootOption) int {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var out, errOut bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, opts...)
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	done := make(chan int, 1)

	go func() {
		done <- Execute(ctx, root)
	}()

	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatalf("the run was still waiting on stdin after the interrupt\nstderr:\n%s", errOut.String())

		return 0
	}
}
