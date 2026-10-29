package cli

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func pseudoTerminal(t *testing.T) string {
	t.Helper()

	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening a pseudo terminal: %v", err)
	}

	t.Cleanup(func() { _ = controller.Close() })

	fd := int(controller.Fd())

	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlocking the pseudo terminal: %v", err)
	}

	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("numbering the pseudo terminal: %v", err)
	}

	return fmt.Sprintf("/dev/pts/%d", number)
}
