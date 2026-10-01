package khsier

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRunEmitsBoundariesBeforePassthrough(t *testing.T) {
	input := []byte{0x00, 0x01, 0x80, 0xff, '\n'}
	var events bytes.Buffer
	output := &eventAwareWriter{events: &events}

	if got := Run(nil, bytes.NewReader(input), output, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("stdout = %x, want %x", output.Bytes(), input)
	}
	if !output.sawBos {
		t.Fatal("stdout write happened before bos event")
	}
	want := []string{"bos", "eos"}
	if got := eventNames(t, events.Bytes()); !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunIdleEmitsIdleAndResume(t *testing.T) {
	reader := newTimedReader(
		readStep{data: []byte("first")},
		readStep{wait: 40 * time.Millisecond, data: []byte("second")},
		readStep{err: io.EOF},
	)
	var output, events bytes.Buffer

	if got := Run([]string{"--idle", "10ms"}, reader, &output, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if got, want := output.String(), "firstsecond"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "idle", "resume", "eos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunDoesNotEmitIdleWhileStdoutIsBlocked(t *testing.T) {
	reader := newTimedReader(
		readStep{data: []byte("first")},
		readStep{waitForRead: true, data: []byte("second")},
		readStep{err: io.EOF},
	)
	writer := newBlockingWriter()
	var events synchronizedBuffer
	done := make(chan int, 1)
	go func() { done <- Run([]string{"--idle", "10ms"}, reader, writer, &events) }()

	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first stdout write")
	}
	time.Sleep(40 * time.Millisecond)
	if got, want := eventNames(t, events.Bytes()), []string{"bos"}; !slicesEqual(got, want) {
		t.Fatalf("events while stdout blocked = %#v, want %#v", got, want)
	}
	if got := reader.readCount(); got != 1 {
		t.Fatalf("Read calls while stdout blocked = %d, want 1", got)
	}

	close(writer.release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := eventNames(t, events.Bytes()); slicesEqual(got, []string{"bos", "idle"}) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "idle"}; !slicesEqual(got, want) {
		t.Fatalf("events after stdout resumed = %#v, want %#v", got, want)
	}
	reader.releaseRead()
	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("Run() = %d, want 0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run")
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "idle", "resume", "eos"}; !slicesEqual(got, want) {
		t.Fatalf("events after stdout resumed = %#v, want %#v", got, want)
	}
}

func TestRunEmitsDataAndEOSWhenReadReturnsDataEOF(t *testing.T) {
	reader := fixedResultReader{data: []byte("last"), err: io.EOF}
	var output, events bytes.Buffer

	if got := Run(nil, reader, &output, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if output.String() != "last" {
		t.Fatalf("stdout = %q, want last", output.String())
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "eos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunIdleEmitsEOSWhenReadReturnsDataEOF(t *testing.T) {
	reader := fixedResultReader{data: []byte("last"), err: io.EOF}
	var output, events bytes.Buffer

	if got := Run([]string{"--idle", "1s"}, reader, &output, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if output.String() != "last" {
		t.Fatalf("stdout = %q, want last", output.String())
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "eos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunIdleEventFailureThenBrokenPipeRemainsFailure(t *testing.T) {
	reader := fixedResultReader{data: []byte("last"), err: io.EOF}
	if got := Run([]string{"--idle", "1s"}, reader, errorWriter{err: syscall.EPIPE}, errorWriter{err: errors.New("stderr unavailable")}); got != 1 {
		t.Fatalf("Run() = %d, want 1 after event failure followed by EPIPE", got)
	}
}

func TestRunIdleZeroReadDoesNotResetTimer(t *testing.T) {
	reader := newZeroReadReader(60 * time.Millisecond)
	events := newIdleEventBuffer()
	done := make(chan int, 1)
	go func() { done <- Run([]string{"--idle", "100ms"}, reader, io.Discard, events) }()

	windowStart := time.Now()
	if !waitForReadStart(reader, 2, time.Second) {
		reader.releaseRead()
		<-done
		t.Fatal("timed out waiting for first zero read")
	}
	if !waitForReadStart(reader, 3, time.Second) {
		reader.releaseRead()
		<-done
		t.Fatal("timed out waiting for second zero read")
	}
	if !waitForReadStart(reader, 4, time.Second) {
		reader.releaseRead()
		<-done
		t.Fatal("timed out waiting for the read pending during idle")
	}
	timeout := time.Until(windowStart.Add(170 * time.Millisecond))
	if timeout < 0 {
		timeout = 0
	}
	select {
	case <-events.idle:
	case <-time.After(timeout):
		reader.releaseRead()
		status := <-done
		t.Fatalf("Run() = %d, events = %#v, want idle before timer reset after zero reads", status, eventNames(t, events.Bytes()))
	}
	reader.releaseRead()
	select {
	case got := <-done:
		if got != 0 {
			t.Fatalf("Run() = %d, want 0", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run")
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos", "idle", "resume", "eos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunIdleZeroReadBackoffBoundsBusyReader(t *testing.T) {
	reader := newBusyZeroReader(100)
	done := make(chan int, 1)
	go func() { done <- Run([]string{"--idle", "50ms"}, reader, io.Discard, io.Discard) }()

	reachedThreshold := false
	select {
	case <-reader.threshold:
		reachedThreshold = true
	case <-time.After(25 * time.Millisecond):
	}
	close(reader.stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run")
	}
	if reachedThreshold {
		t.Fatalf("busy zero reader reached %d reads before bounded backoff deadline", reader.thresholdAt)
	}
}

func TestRunEmptyInputEmitsEOSWithoutBOS(t *testing.T) {
	var output, events bytes.Buffer
	if got := Run(nil, strings.NewReader(""), &output, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"eos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunOutputFailureDoesNotEmitEOS(t *testing.T) {
	var events bytes.Buffer
	if got := Run(nil, strings.NewReader("input"), errorWriter{err: errors.New("output unavailable")}, &events); got != 1 {
		t.Fatalf("Run() = %d, want 1", got)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunBrokenPipeSucceedsWithoutEOS(t *testing.T) {
	var events bytes.Buffer
	if got := Run(nil, strings.NewReader("input"), errorWriter{err: syscall.EPIPE}, &events); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunShortStdoutWriteFailsWithoutEOS(t *testing.T) {
	var events bytes.Buffer
	if got := Run(nil, strings.NewReader("input"), partialWriter{max: 1}, &events); got != 1 {
		t.Fatalf("Run() = %d, want 1", got)
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestRunEventFailureContinuesPassthroughAndFails(t *testing.T) {
	var output bytes.Buffer
	if got := Run(nil, strings.NewReader("input"), &output, errorWriter{err: errors.New("stderr unavailable")}); got != 1 {
		t.Fatalf("Run() = %d, want 1", got)
	}
	if output.String() != "input" {
		t.Fatalf("stdout = %q, want input", output.String())
	}
}

func TestRunStopsEventEmissionAfterFailure(t *testing.T) {
	var output bytes.Buffer
	events := &failAfterWriter{}
	if got := Run(nil, strings.NewReader("input"), &output, events); got != 1 {
		t.Fatalf("Run() = %d, want 1", got)
	}
	if output.String() != "input" {
		t.Fatalf("stdout = %q, want input", output.String())
	}
	if got, want := eventNames(t, events.Bytes()), []string{"bos"}; !slicesEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func eventNames(t *testing.T, data []byte) []string {
	t.Helper()
	var names []string
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record struct {
			Event     string `json:"event"`
			Timestamp string `json:"timestamp"`
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid event JSON %q: %v", line, err)
		}
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatalf("invalid event fields %q: %v", line, err)
		}
		if record.Timestamp == "" {
			t.Errorf("event %q has empty timestamp", record.Event)
		} else if timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp); err != nil || timestamp.Location() != time.UTC {
			t.Errorf("event %q timestamp = %q, want UTC RFC3339Nano", record.Event, record.Timestamp)
		}
		if len(fields) != 2 {
			t.Errorf("event %q has unexpected fields", line)
		}
		names = append(names, record.Event)
	}
	return names
}

func slicesEqual[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

type partialWriter struct{ max int }

func (w partialWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		return w.max, nil
	}
	return len(p), nil
}

type eventAwareWriter struct {
	events *bytes.Buffer
	output bytes.Buffer
	sawBos bool
}

func (w *eventAwareWriter) Write(p []byte) (int, error) {
	w.sawBos = bytes.Contains(w.events.Bytes(), []byte(`"event":"bos"`))
	return w.output.Write(p)
}

func (w *eventAwareWriter) Bytes() []byte { return w.output.Bytes() }

type failAfterWriter struct {
	bytes.Buffer
	writes int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("stderr unavailable")
	}
	return w.Buffer.Write(p)
}

type fixedResultReader struct {
	data []byte
	err  error
}

func (r fixedResultReader) Read(p []byte) (int, error) {
	return copy(p, r.data), r.err
}

type readStep struct {
	wait        time.Duration
	waitForRead bool
	data        []byte
	err         error
}

type timedReader struct {
	mu       sync.Mutex
	steps    []readStep
	reads    int
	readGate chan struct{}
}

func newTimedReader(steps ...readStep) *timedReader {
	return &timedReader{steps: steps, readGate: make(chan struct{})}
}

func (r *timedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.reads >= len(r.steps) {
		r.mu.Unlock()
		return 0, io.EOF
	}
	step := r.steps[r.reads]
	r.reads++
	r.mu.Unlock()
	if step.waitForRead {
		<-r.readGate
	}
	if step.wait > 0 {
		time.Sleep(step.wait)
	}
	return copy(p, step.data), step.err
}

func (r *timedReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func (r *timedReader) releaseRead() { close(r.readGate) }

type zeroReadReader struct {
	delay      time.Duration
	mu         sync.Mutex
	calls      int
	readStarts []chan struct{}
	release    chan struct{}
}

func newZeroReadReader(delay time.Duration) *zeroReadReader {
	return &zeroReadReader{
		delay:      delay,
		readStarts: []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})},
		release:    make(chan struct{}),
	}
}

func (r *zeroReadReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	if call <= len(r.readStarts) {
		close(r.readStarts[call-1])
	}
	r.mu.Unlock()
	switch call {
	case 1:
		return copy(p, []byte("first")), nil
	case 2, 3:
		time.Sleep(r.delay)
		return 0, nil
	case 4:
		<-r.release
		return copy(p, []byte("second")), nil
	default:
		return 0, io.EOF
	}
}

func (r *zeroReadReader) releaseRead() { close(r.release) }

func waitForReadStart(reader *zeroReadReader, call int, timeout time.Duration) bool {
	reader.mu.Lock()
	started := reader.readStarts[call-1]
	reader.mu.Unlock()
	select {
	case <-started:
		return true
	case <-time.After(timeout):
		return false
	}
}

type idleEventBuffer struct {
	synchronizedBuffer
	idle chan struct{}
	once sync.Once
}

func newIdleEventBuffer() *idleEventBuffer {
	return &idleEventBuffer{idle: make(chan struct{})}
}

func (b *idleEventBuffer) Write(p []byte) (int, error) {
	n, err := b.synchronizedBuffer.Write(p)
	if bytes.Contains(p, []byte(`"event":"idle"`)) {
		b.once.Do(func() { close(b.idle) })
	}
	return n, err
}

type busyZeroReader struct {
	mu          sync.Mutex
	calls       int
	thresholdAt int
	threshold   chan struct{}
	stop        chan struct{}
}

func newBusyZeroReader(threshold int) *busyZeroReader {
	return &busyZeroReader{
		thresholdAt: threshold,
		threshold:   make(chan struct{}),
		stop:        make(chan struct{}),
	}
}

func (r *busyZeroReader) Read(p []byte) (int, error) {
	select {
	case <-r.stop:
		return 0, io.EOF
	default:
	}
	r.mu.Lock()
	r.calls++
	call := r.calls
	if call == r.thresholdAt {
		close(r.threshold)
	}
	r.mu.Unlock()
	if call == 1 {
		return copy(p, []byte("first")), nil
	}
	return 0, nil
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	data    bytes.Buffer
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	<-w.release
	return w.data.Write(p)
}
