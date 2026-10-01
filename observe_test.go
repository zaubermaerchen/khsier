package khsier_test

// This file verifies the public observer's stream and callback contract.

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	khsier "github.com/zaubermaerchen/khsier"
)

func TestObserveBoundariesAndOwnership(t *testing.T) {
	for _, idle := range []time.Duration{0, time.Second} {
		t.Run(idle.String(), func(t *testing.T) {
			input := &ownedReader{Reader: bytes.NewReader([]byte{0, 0xff, '\n'})}
			var events []khsier.Event
			output := &ownedWriter{beforeWrite: func() {
				if len(events) != 1 || events[0].Kind != khsier.EventBOS {
					t.Fatal("write did not follow synchronous BOS callback")
				}
			}}
			before := time.Now()
			err := khsier.Observe(input, output, khsier.Options{Idle: idle}, func(event khsier.Event) { events = append(events, event) })
			after := time.Now()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(output.Bytes(), []byte{0, 0xff, '\n'}) {
				t.Fatalf("output = %x", output.Bytes())
			}
			if len(events) != 2 || events[1].Kind != khsier.EventEOS {
				t.Fatalf("events = %+v", events)
			}
			for _, event := range events {
				if event.Timestamp.Before(before) || event.Timestamp.After(after) {
					t.Fatalf("timestamp outside observation: %v", event.Timestamp)
				}
			}
			if input.closed || output.closed {
				t.Fatal("Observe closed borrowed streams")
			}
		})
	}
}

func TestObserveCopiesDataReturnedWithEOF(t *testing.T) {
	for _, idle := range []time.Duration{0, time.Second} {
		var output bytes.Buffer
		var events []khsier.EventKind
		err := khsier.Observe(resultReader{data: "last", err: io.EOF}, &output, khsier.Options{Idle: idle}, func(event khsier.Event) {
			if event.Kind == khsier.EventEOS && output.String() != "last" {
				t.Fatal("EOS before copying EOF data")
			}
			events = append(events, event.Kind)
		})
		if err != nil {
			t.Fatal(err)
		}
		if output.String() != "last" || !reflect.DeepEqual(events, []khsier.EventKind{khsier.EventBOS, khsier.EventEOS}) {
			t.Fatalf("output = %q, events = %v", output.String(), events)
		}
	}
}

func TestObserveEmptyAndNilCallback(t *testing.T) {
	for _, idle := range []time.Duration{0, time.Second} {
		var events []khsier.EventKind
		if err := khsier.Observe(strings.NewReader(""), io.Discard, khsier.Options{Idle: idle}, func(event khsier.Event) { events = append(events, event.Kind) }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(events, []khsier.EventKind{khsier.EventEOS}) {
			t.Fatalf("events = %v", events)
		}
		var output bytes.Buffer
		if err := khsier.Observe(strings.NewReader("data"), &output, khsier.Options{Idle: idle}, nil); err != nil {
			t.Fatal(err)
		}
		if output.String() != "data" {
			t.Fatalf("output = %q", output.String())
		}
	}
}

func TestObserveRejectsNegativeIdleBeforeSideEffects(t *testing.T) {
	input := &countingReader{}
	output := &ownedWriter{beforeWrite: func() { t.Fatal("unexpected write") }}
	if err := khsier.Observe(input, output, khsier.Options{Idle: -time.Nanosecond}, func(khsier.Event) { t.Fatal("unexpected callback") }); err == nil {
		t.Fatal("negative Idle succeeded")
	}
	if input.reads != 0 {
		t.Fatalf("reads = %d", input.reads)
	}
}

func TestObserveReturnsStreamErrorsWithoutEOS(t *testing.T) {
	readErr := errors.New("read failure")
	writeErr := errors.New("write failure")
	for _, idle := range []time.Duration{0, time.Second} {
		for _, tc := range []struct {
			name   string
			reader io.Reader
			writer io.Writer
			want   error
			data   string
		}{
			{"read", resultReader{data: "last", err: readErr}, &bytes.Buffer{}, readErr, "last"},
			{"write", strings.NewReader("data"), failingWriter{err: writeErr}, writeErr, ""},
			{"broken pipe", strings.NewReader("data"), failingWriter{err: syscall.EPIPE}, syscall.EPIPE, ""},
			{"short write", strings.NewReader("data"), failingWriter{}, io.ErrShortWrite, ""},
		} {
			t.Run(idle.String()+"/"+tc.name, func(t *testing.T) {
				var events []khsier.EventKind
				err := khsier.Observe(tc.reader, tc.writer, khsier.Options{Idle: idle}, func(event khsier.Event) { events = append(events, event.Kind) })
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
				if !reflect.DeepEqual(events, []khsier.EventKind{khsier.EventBOS}) {
					t.Fatalf("events = %v", events)
				}
				if output, ok := tc.writer.(*bytes.Buffer); ok && output.String() != tc.data {
					t.Fatalf("output = %q", output.String())
				}
			})
		}
	}
}

func TestObserveIdleAndResume(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	events := make(chan khsier.Event, 4)
	done := make(chan error, 1)
	var output bytes.Buffer
	go func() {
		done <- khsier.Observe(input, &output, khsier.Options{Idle: time.Millisecond}, func(event khsier.Event) {
			if event.Kind == khsier.EventResume {
				_ = writer.Close()
			}
			events <- event
		})
	}()
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []khsier.EventKind{khsier.EventBOS, khsier.EventIdle} {
		awaitEvent(t, events, want)
	}
	if _, err := writer.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	awaitEvent(t, events, khsier.EventResume)
	writer.Close()
	awaitEvent(t, events, khsier.EventEOS)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Observe did not return")
	}
	if output.String() != "firstsecond" {
		t.Fatalf("output = %q", output.String())
	}
}

func awaitEvent(t *testing.T, events <-chan khsier.Event, want khsier.EventKind) {
	t.Helper()
	select {
	case event := <-events:
		if event.Kind != want {
			t.Fatalf("event = %v, want %v", event.Kind, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %v", want)
	}
}

type ownedReader struct {
	*bytes.Reader
	closed bool
}

func (r *ownedReader) Close() error { r.closed = true; return nil }

type ownedWriter struct {
	bytes.Buffer
	closed      bool
	beforeWrite func()
}

func (w *ownedWriter) Write(p []byte) (int, error) {
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	return w.Buffer.Write(p)
}
func (w *ownedWriter) Close() error { w.closed = true; return nil }

type countingReader struct{ reads int }

func (r *countingReader) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

type resultReader struct {
	data string
	err  error
}

func (r resultReader) Read(p []byte) (int, error) { return copy(p, r.data), r.err }

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
