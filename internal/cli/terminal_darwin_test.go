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

	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatalf("granting the pseudo terminal: %v", err)
	}

	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatalf("unlocking the pseudo terminal: %v", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatalf("reading the pseudo terminal: %v", err)
	}

	// x/sys exports no TIOCPTYGNAME call on darwin, and a controller shares its minor number with the
	// terminal it drives.
	return fmt.Sprintf("/dev/ttys%03d", unix.Minor(uint64(stat.Rdev)))
}
