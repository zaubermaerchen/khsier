//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package khsier

// This file owns a duplicate of a borrowed Unix event descriptor.

import (
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

const eventsFDSupported = true

func openEventsFD(fd int) (io.WriteCloser, error) {
	return duplicateEventsFD(fd, unix.Dup)
}

func duplicateEventsFD(fd int, dup func(int) (int, error)) (io.WriteCloser, error) {
	owned, err := dup(fd)
	if err != nil {
		return nil, err
	}
	// Access mode belongs to the shared open-file description. Inspect the
	// duplicate rather than touching the caller's descriptor or status flags.
	flags, err := unix.FcntlInt(uintptr(owned), unix.F_GETFL, 0)
	if err == nil && flags&unix.O_ACCMODE == unix.O_RDONLY {
		err = fmt.Errorf("descriptor is not writable")
	}
	var stat unix.Stat_t
	if err == nil {
		err = unix.Fstat(owned, &stat)
	}
	if err == nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		err = fmt.Errorf("descriptor is a directory")
	}
	if err != nil {
		_ = unix.Close(owned)
		return nil, err
	}
	unix.CloseOnExec(owned)
	return eventFD(owned), nil
}

type eventFD int

func (fd eventFD) Write(p []byte) (int, error) {
	// os.File may use the runtime poller for borrowed nonblocking descriptors.
	// A raw write preserves status flags and exposes each failed or short write
	// to the emitter without retrying it.
	n, err := unix.Write(int(fd), p)
	// A failed syscall reports -1; io.Writer requires a nonnegative count.
	if n < 0 {
		n = 0
	}
	return n, err
}

func (fd eventFD) Close() error { return unix.Close(int(fd)) }
