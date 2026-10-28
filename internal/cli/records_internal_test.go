package cli

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestUnwritableID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode output.Mode
		id   string
		want string
	}{
		{
			name: "should escape a single quote inside the single quoted id",
			mode: output.CSV,
			id:   "it's\r",
			want: `--id: 'it\'s\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should leave a double quote unescaped inside the single quoted id",
			mode: output.CSV,
			id:   "say \"hi\"\r",
			want: `--id: 'say "hi"\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should escape a tab and a newline as they are written in Go",
			mode: output.TSV,
			id:   "a\tb\nc",
			want: `--id: 'a\tb\nc' holds a tab, carriage return or newline, which -o tsv cannot write`,
		},
		{
			name: "should keep a backslash before a quote apart from the escaped quote",
			mode: output.CSV,
			id:   "a\\'\r",
			want: `--id: 'a\\\'\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should accept an id the output mode can write",
			mode: output.CSV,
			id:   "it's",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := unwritableID(tc.mode, tc.id)
			if got := errorText(err); got != tc.want {
				t.Errorf("unwritableID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestNaming_Next(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "should exit 130 when the --id context ends before the engine's"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parent := &orderedCancels{Context: context.Background(), done: make(chan struct{})}
			defer parent.cancelAll()

			// The naming context is the first one the run derives, so cancelling only it as the
			// first record is read opens the window a real interrupt can, where the engine's
			// context is not yet cancelled.
			stdin := &handingOver{data: "{\"id\":\"first\"}\n{\"id\":\"second\"}\n", handed: parent.cancelFirst}

			root := NewRootCmd(BuildInfo{Version: "1.2.3"},
				withKeychain(noKeychain()),
				WithStdin(stdin),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
			)

			var out, errOut strings.Builder

			root.Command.SetOut(&out)
			root.Command.SetErr(&errOut)
			root.Command.SetArgs([]string{
				"is this urgent", "-i", "jsonl", "--id", ".id", "--mock", writeMock(t, `{"answer":0.5}`),
			})

			if code := root.Execute(parent); code != ExitInterrupt {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitInterrupt, out.String(), errOut.String())
			}

			if out.String() != "" {
				t.Errorf("stdout = %q, want nothing", out.String())
			}
		})
	}
}

func (r *handingOver) Read(p []byte) (int, error) {
	n, err := strings.NewReader(r.data[r.offset:]).Read(p)
	r.offset += n

	if n > 0 && !r.called {
		r.called = true
		r.handed()
	}

	return n, err
}

type handingOver struct {
	data   string
	offset int
	called bool
	handed func()
}

func (c *orderedCancels) Done() <-chan struct{} {
	return c.done
}

func (c *orderedCancels) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.err
}

func (c *orderedCancels) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.after = append(c.after, f)

	return func() bool { return true }
}

func (c *orderedCancels) cancelFirst() {
	c.mu.Lock()
	c.err = context.Canceled
	first := c.after[0]
	c.after = c.after[1:]
	c.mu.Unlock()

	first()
}

func (c *orderedCancels) cancelAll() {
	c.mu.Lock()
	c.err = context.Canceled
	rest := c.after
	c.after = nil
	c.mu.Unlock()

	close(c.done)

	for _, f := range rest {
		f()
	}
}

type orderedCancels struct {
	context.Context

	mu    sync.Mutex
	err   error
	done  chan struct{}
	after []func()
}

func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
