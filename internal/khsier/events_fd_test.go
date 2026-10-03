package khsier

// This file verifies dedicated event routing and descriptor failure handling.

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestParseEventsFD(t *testing.T) {
	for _, args := range [][]string{{"--events-fd", "3"}, {"--events-fd=003"}} {
		opts, _, err := parseArgs(args)
		if err != nil || opts.eventsFD != 3 || !opts.eventsFDSet {
			t.Fatalf("parseArgs(%q) = %#v, %v", args, opts, err)
		}
	}
}

func TestRejectEventsFDArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--events-fd"}, {"--events-fd="}, {"--events-fd=0"}, {"--events-fd=1"}, {"--events-fd=2"},
		{"--events-fd=-3"}, {"--events-fd=+3"}, {"--events-fd= 3"}, {"--events-fd=3 "}, {"--events-fd=3.0"},
		{"--events-fd=4294967299"}, {"--events-fd=2147483648"}, {"--events-fd=0x3"}, {"--events-fd=9999999999999999999999999999999999999999"},
		{"--events-fd=3", "--events-fd", "4"}, {"--events-fd", "3", "--events-fd=3"}, {"--events-fd", "--idle=1s"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			input := newTimedReader(readStep{data: []byte("data")})
			var output, diagnostics bytes.Buffer
			if got := Run(args, input, &output, &diagnostics); got != 1 {
				t.Fatalf("status = %d", got)
			}
			if input.readCount() != 0 || output.Len() != 0 || diagnostics.Len() == 0 {
				t.Fatalf("reads=%d stdout=%q stderr=%q", input.readCount(), output.Bytes(), diagnostics.Bytes())
			}
		})
	}
}

func TestRunEventsFDSetupFailure(t *testing.T) {
	input := newTimedReader(readStep{data: []byte("data")})
	var output, diagnostics bytes.Buffer
	opener := func(int) (io.WriteCloser, error) { return nil, errors.New("dup failed") }
	if got := runWithEventsOpener([]string{"--events-fd=3"}, input, &output, &diagnostics, opener); got != 1 {
		t.Fatalf("status=%d", got)
	}
	if input.readCount() != 0 || output.Len() != 0 || !strings.Contains(diagnostics.String(), "dup failed") {
		t.Fatalf("reads=%d stdout=%q stderr=%q", input.readCount(), output.Bytes(), diagnostics.Bytes())
	}
}

func TestRunEventsFDFailures(t *testing.T) {
	for _, writeErr := range []error{nil, syscall.EPIPE, io.ErrShortWrite} {
		for _, closeErr := range []error{nil, errors.New("close failed")} {
			for _, stdoutErr := range []error{nil, syscall.EPIPE} {
				t.Run(strings.Join([]string{errorLabel(writeErr), errorLabel(closeErr), errorLabel(stdoutErr)}, "/"), func(t *testing.T) {
					events := &testEventCloser{writeErr: writeErr, closeErr: closeErr}
					var output, diagnostics bytes.Buffer
					var out io.Writer = &output
					if stdoutErr != nil {
						out = errorWriter{err: stdoutErr}
					}
					opener := func(int) (io.WriteCloser, error) { return events, nil }
					got := runWithEventsOpener([]string{"--events-fd=3"}, strings.NewReader("data"), out, &diagnostics, opener)
					want := 0
					if writeErr != nil || closeErr != nil {
						want = 1
					}
					if got != want || events.closes != 1 || diagnostics.Len() != 0 {
						t.Fatalf("status=%d want=%d closes=%d stderr=%q", got, want, events.closes, diagnostics.Bytes())
					}
					if stdoutErr == nil && output.String() != "data" {
						t.Fatalf("stdout=%q", output.Bytes())
					}
					if writeErr != nil && events.writes != 1 {
						t.Fatalf("writes=%d want=1", events.writes)
					}
					if writeErr == nil {
						wantEvents := []string{"bos", "eos"}
						if stdoutErr != nil {
							wantEvents = []string{"bos"}
						}
						if names := eventNames(t, events.Bytes()); !slices.Equal(names, wantEvents) {
							t.Fatalf("events=%v want=%v", names, wantEvents)
						}
					}
				})
			}
		}
	}
}

func errorLabel(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}

type testEventCloser struct {
	bytes.Buffer
	writeErr, closeErr error
	writes, closes     int
}

func (w *testEventCloser) Write(p []byte) (int, error) {
	w.writes++
	if w.writeErr == io.ErrShortWrite {
		return len(p) - 1, nil
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.Buffer.Write(p)
}
func (w *testEventCloser) Close() error { w.closes++; return w.closeErr }
