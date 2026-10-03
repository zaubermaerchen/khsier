//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package khsier

// This file verifies real Unix event descriptors, ownership, and setup errors.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRunEventsFDRealOutputs(t *testing.T) {
	for _, pipe := range []bool{false, true} {
		t.Run(fmt.Sprint("pipe=", pipe), func(t *testing.T) {
			var file, reader *os.File
			var err error
			if pipe {
				reader, file, err = os.Pipe()
				if err == nil {
					defer reader.Close()
				}
			} else {
				file, err = os.CreateTemp(t.TempDir(), "events")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			// Fd intentionally gives a blocking descriptor for os.Pipe. The separate
			// status-flag test below exercises nonblocking mode without calling Fd again.
			fd := int(file.Fd())
			flags := configurableStatusFlags(fdFlags(t, fd, unix.F_GETFL))
			descriptorFlags := fdFlags(t, fd, unix.F_GETFD)
			info, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			data := []byte{0, 128, 255, '\n'}
			var output, diagnostics bytes.Buffer
			if got := Run([]string{fmt.Sprintf("--events-fd=%d", fd)}, bytes.NewReader(data), &output, &diagnostics); got != 0 {
				t.Fatalf("status=%d stderr=%q", got, diagnostics.Bytes())
			}
			if !bytes.Equal(output.Bytes(), data) || diagnostics.Len() != 0 {
				t.Fatalf("stdout=%x stderr=%q", output.Bytes(), diagnostics.Bytes())
			}
			if got := configurableStatusFlags(fdFlags(t, fd, unix.F_GETFL)); got != flags {
				t.Fatalf("status flags=%x want=%x", got, flags)
			}
			if got := fdFlags(t, fd, unix.F_GETFD); got != descriptorFlags {
				t.Fatalf("descriptor flags=%x want=%x", got, descriptorFlags)
			}
			after, err := file.Stat()
			if err != nil || after.Mode() != info.Mode() {
				t.Fatalf("mode/open changed: %v", err)
			}
			var events []byte
			if pipe {
				if _, err := file.Write([]byte("sentinel\n")); err != nil {
					t.Fatalf("original write: %v", err)
				}
				file.Close()
				events, err = io.ReadAll(reader)
				events = bytes.TrimSuffix(events, []byte("sentinel\n"))
			} else {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				events, err = io.ReadAll(file)
			}
			if err != nil {
				t.Fatal(err)
			}
			if names := eventNames(t, events); !slices.Equal(names, []string{"bos", "eos"}) {
				t.Fatalf("events=%v", names)
			}
		})
	}
}

func TestEventsFDDuplicateOwnershipAndFlags(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	fd := int(writer.Fd())
	for _, nonblock := range []bool{false, true} {
		if err := unix.SetNonblock(fd, nonblock); err != nil {
			t.Fatal(err)
		}
		flags := fdFlags(t, fd, unix.F_GETFL)
		originalFDFlags := fdFlags(t, fd, unix.F_GETFD)
		events, err := openEventsFD(fd)
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		t.Cleanup(func() {
			if !closed {
				_ = events.Close()
			}
		})
		// Setup itself must preserve the full F_GETFL word, including any
		// kernel history bits, and every original descriptor flag.
		if fdFlags(t, fd, unix.F_GETFL) != flags || fdFlags(t, fd, unix.F_GETFD) != originalFDFlags {
			t.Fatal("setup changed original flags")
		}
		owned := int(events.(eventFD))
		if owned == fd {
			t.Fatal("borrowed descriptor was not duplicated")
		}
		if fdFlags(t, owned, unix.F_GETFD)&unix.FD_CLOEXEC == 0 {
			t.Fatal("duplicate lacks close-on-exec")
		}
		if _, err := events.Write([]byte("event")); err != nil {
			t.Fatal(err)
		}
		err = events.Close()
		closed = true
		if err != nil {
			t.Fatal(err)
		}
		if _, err := unix.FcntlInt(uintptr(owned), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Fatalf("duplicate remains open: %v", err)
		}
		if configurableStatusFlags(fdFlags(t, fd, unix.F_GETFL)) != configurableStatusFlags(flags) || fdFlags(t, fd, unix.F_GETFD) != originalFDFlags {
			t.Fatal("original flags changed")
		}
	}
}

func TestRunEventsFDUnusable(t *testing.T) {
	path := t.TempDir() + "/events"
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	readOnly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	for _, fd := range []int{int(readOnly.Fd()), int(directory.Fd()), 2147483647} {
		input := newTimedReader(readStep{data: []byte("data")})
		var output, diagnostics bytes.Buffer
		if got := Run([]string{fmt.Sprintf("--events-fd=%d", fd)}, input, &output, &diagnostics); got != 1 {
			t.Fatalf("status=%d fd=%d", got, fd)
		}
		if input.readCount() != 0 || output.Len() != 0 || !strings.HasPrefix(diagnostics.String(), "khsier: --events-fd:") {
			t.Fatalf("reads=%d stdout=%q stderr=%q", input.readCount(), output.Bytes(), diagnostics.Bytes())
		}
	}
}

func TestEventsFDDuplicationFailureAndSetupCleanup(t *testing.T) {
	file, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fd := int(file.Fd())
	sentinel := errors.New("dup failed")
	if _, err := duplicateEventsFD(fd, func(int) (int, error) { return -1, sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("dup error=%v", err)
	}
	owned := -1
	_, err = duplicateEventsFD(fd, func(fd int) (int, error) { var err error; owned, err = unix.Dup(fd); return owned, err })
	if err == nil {
		t.Fatal("read-only directory accepted")
	}
	if _, err := unix.FcntlInt(uintptr(owned), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("setup leaked duplicate: %v", err)
	}
	_ = fdFlags(t, fd, unix.F_GETFD)
}

func TestRunEventsFDPipeEPIPE(t *testing.T) {
	for _, brokenStdout := range []bool{false, true} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		reader.Close()
		var output, diagnostics bytes.Buffer
		var out io.Writer = &output
		if brokenStdout {
			out = errorWriter{err: unix.EPIPE}
		}
		got := Run([]string{fmt.Sprintf("--events-fd=%d", writer.Fd())}, strings.NewReader("data"), out, &diagnostics)
		writer.Close()
		if got != 1 || diagnostics.Len() != 0 {
			t.Fatalf("status=%d stderr=%q", got, diagnostics.Bytes())
		}
		if !brokenStdout && output.String() != "data" {
			t.Fatalf("stdout=%q", output.Bytes())
		}
	}
}

func TestEventFDWriteEPIPE(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := openEventsFD(int(writer.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	stopBrokenPipe := configureBrokenPipe()
	defer stopBrokenPipe()
	if n, err := events.Write([]byte("event")); n != 0 || !errors.Is(err, unix.EPIPE) {
		t.Fatalf("Write=(%d,%v), want (0,EPIPE)", n, err)
	}
}

func TestRunEventsFDFullNonblockingPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	fd := int(writer.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	// Fill the last byte as well: a large write can return EAGAIN while a
	// smaller event record would still fit in the remaining pipe capacity.
	filler := bytes.Repeat([]byte("x"), 4096)
	filled := 0
	for {
		n, err := unix.Write(fd, filler)
		if errors.Is(err, unix.EAGAIN) {
			if len(filler) > 1 {
				filler = filler[:1]
				continue
			}
			break
		}
		if err != nil || n <= 0 {
			t.Fatalf("fill Write=(%d,%v)", n, err)
		}
		filled += n
	}
	flags := fdFlags(t, fd, unix.F_GETFL)
	descriptorFlags := fdFlags(t, fd, unix.F_GETFD)
	var output, diagnostics bytes.Buffer
	// BOS fails before stdout is written. Drain in stdout.Write so that EOS
	// would succeed if the emitter incorrectly attempted a later event.
	drained := false
	out := eventFDWriterFunc(func(p []byte) (int, error) {
		if !drained {
			data := make([]byte, filled)
			if _, err := io.ReadFull(reader, data); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, bytes.Repeat([]byte("x"), filled)) {
				t.Fatal("event output changed pipe filler")
			}
			drained = true
		}
		return output.Write(p)
	})
	data := []byte{0, 128, 255, '\n'}
	if got := Run([]string{fmt.Sprintf("--events-fd=%d", fd)}, bytes.NewReader(data), out, &diagnostics); got != 1 {
		t.Fatalf("status=%d, want 1", got)
	}
	if !drained || !bytes.Equal(output.Bytes(), data) || diagnostics.Len() != 0 {
		t.Fatalf("drained=%v stdout=%x stderr=%q", drained, output.Bytes(), diagnostics.Bytes())
	}
	if fdFlags(t, fd, unix.F_GETFL) != flags || fdFlags(t, fd, unix.F_GETFD) != descriptorFlags {
		t.Fatal("event failure changed original flags")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("events after failure=%q error=%v", remaining, err)
	}
}

type eventFDWriterFunc func([]byte) (int, error)

func (f eventFDWriterFunc) Write(p []byte) (int, error) { return f(p) }

func fdFlags(t *testing.T, fd, command int) int {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(fd), command, 0)
	if err != nil {
		t.Fatalf("fcntl(%d,%d): %v", fd, command, err)
	}
	return flags
}

// Darwin exposes the kernel-only FWASWRITTEN history bit through F_GETFL.
// Any successful write sets it on the shared open-file description. Compare
// every configuration bit, while testing the intrinsic history change below.
const darwinWasWritten = 0x00010000

func configurableStatusFlags(flags int) int {
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return flags &^ darwinWasWritten
	}
	return flags
}

func TestDarwinWriteMaySetKernelHistoryFlag(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		t.Skip("Darwin kernel write-history behavior")
	}
	for _, pipe := range []bool{false, true} {
		t.Run(fmt.Sprint("pipe=", pipe), func(t *testing.T) {
			var file, reader *os.File
			var err error
			if pipe {
				reader, file, err = os.Pipe()
				if err == nil {
					defer reader.Close()
				}
			} else {
				file, err = os.CreateTemp(t.TempDir(), "write-history")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			fd := int(file.Fd())
			before := fdFlags(t, fd, unix.F_GETFL)
			fdBefore := fdFlags(t, fd, unix.F_GETFD)
			// This control uses neither duplication nor khsier event routing.
			if n, err := unix.Write(fd, []byte("x")); n != 1 || err != nil {
				t.Fatalf("direct write=(%d,%v)", n, err)
			}
			after := fdFlags(t, fd, unix.F_GETFL)
			// XNU currently exposes FWASWRITTEN here. A future kernel may
			// hide it; neither behavior may change any configuration bit.
			if after != before && after != before|darwinWasWritten {
				t.Fatalf("status flags after direct write=%x before=%x", after, before)
			}
			if fdFlags(t, fd, unix.F_GETFD) != fdBefore {
				t.Fatal("direct write changed descriptor flags")
			}
		})
	}
}
