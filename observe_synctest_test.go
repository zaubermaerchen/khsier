//go:build go1.25

//go:debug asynctimerchan=0

// synctest requires synchronous timer channels; keep the Go 1.22 module
// minimum while selecting that behavior only for this test binary.

package khsier_test

// This file verifies idle windows and synchronous boundaries using virtual time.

import (
	"bytes"
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	khsier "github.com/zaubermaerchen/khsier"
)

const synctestIdle = time.Second

func TestObserveSynctestIdleResumeOrdering(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newSynctestObserver(t)
		run.expectRead(t)
		time.Sleep(2 * synctestIdle)
		synctest.Wait()
		run.expectTrace(t)

		run.deliver("first", nil)
		run.expectRead(t)
		run.expectTrace(t, "bos", "write:first")
		time.Sleep(synctestIdle)
		synctest.Wait()
		run.expectTrace(t, "bos", "write:first", "idle")
		time.Sleep(2 * synctestIdle)
		synctest.Wait()
		run.expectTrace(t, "bos", "write:first", "idle")

		run.deliver("second", nil)
		run.expectRead(t)
		run.expectTrace(t, "bos", "write:first", "idle", "resume", "write:second")
		run.deliver("last", io.EOF)
		run.expectDone(t)
		run.expectTrace(t, "bos", "write:first", "idle", "resume", "write:second", "write:last", "eos")
		if got := run.output.String(); got != "firstsecondlast" {
			t.Fatalf("output = %q", got)
		}
	})
}

func TestObserveSynctestBlockedWriteStartsFreshWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newSynctestObserver(t)
		gate := make(chan struct{})
		entered := make(chan struct{}, 1)
		run.beforeWrite = func() {
			entered <- struct{}{}
			select {
			case <-gate:
			case <-run.stop:
			}
		}
		run.expectRead(t)
		run.deliver("first", nil)
		synctest.Wait()
		select {
		case <-entered:
		default:
			t.Fatal("output write has not started")
		}
		run.expectNoRead(t)
		time.Sleep(3 * synctestIdle)
		synctest.Wait()
		run.expectTrace(t, "bos")
		run.expectNoRead(t)

		close(gate)
		run.expectRead(t)
		run.expectTrace(t, "bos", "write:first")
		run.expectFreshIdle(t, "bos", "write:first")
		run.deliver("", io.EOF)
		run.expectDone(t)
		run.expectTrace(t, "bos", "write:first", "idle", "eos")
	})
}

func TestObserveSynctestBlockedCallbackStartsFreshWindow(t *testing.T) {
	for _, kind := range []khsier.EventKind{khsier.EventBOS, khsier.EventResume} {
		t.Run(string(kind), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := newSynctestObserver(t)
				gate := make(chan struct{})
				entered := make(chan struct{}, 1)
				run.beforeEvent = func(event khsier.Event) {
					if event.Kind == kind {
						entered <- struct{}{}
						select {
						case <-gate:
						case <-run.stop:
						}
					}
				}
				run.expectRead(t)
				var trace []string
				if kind == khsier.EventResume {
					run.deliver("first", nil)
					run.expectRead(t)
					time.Sleep(synctestIdle)
					synctest.Wait()
					trace = []string{"bos", "write:first", "idle"}
					run.expectTrace(t, trace...)
				}
				run.deliver("blocked", nil)
				synctest.Wait()
				select {
				case <-entered:
				default:
					t.Fatal("callback has not started")
				}
				trace = append(trace, string(kind))
				run.expectTrace(t, trace...)
				run.expectNoRead(t)
				time.Sleep(3 * synctestIdle)
				synctest.Wait()
				run.expectTrace(t, trace...)
				run.expectNoRead(t)

				close(gate)
				run.expectRead(t)
				trace = append(trace, "write:blocked")
				run.expectTrace(t, trace...)
				run.expectFreshIdle(t, trace...)
				run.deliver("", io.EOF)
				run.expectDone(t)
				trace = append(trace, "idle", "eos")
				run.expectTrace(t, trace...)
			})
		})
	}
}

func TestObserveSynctestZeroReadsKeepIdleDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newSynctestObserver(t)
		run.expectRead(t)
		run.deliver("first", nil)
		run.expectRead(t)
		deadline := time.Now().Add(synctestIdle)
		for range 3 {
			time.Sleep(synctestIdle / 4)
			run.deliver("", nil)
			synctest.Wait()
			// The observer backs off after a legal (0, nil) read. Advance
			// past that delay before requiring another pending read.
			time.Sleep(time.Millisecond)
			run.expectRead(t)
			run.expectTrace(t, "bos", "write:first")
		}
		time.Sleep(deadline.Sub(time.Now()))
		synctest.Wait()
		run.expectTrace(t, "bos", "write:first", "idle")
		if got := run.lastEventTime(); !got.Equal(deadline) {
			t.Fatalf("idle timestamp = %v, want original deadline %v", got, deadline)
		}
		run.deliver("", io.EOF)
		run.expectDone(t)
		run.expectTrace(t, "bos", "write:first", "idle", "eos")
	})
}

type synctestRead struct {
	data string
	err  error
}

type synctestObserver struct {
	mu          sync.Mutex
	reads       chan struct{}
	results     chan synctestRead
	stop        chan struct{}
	done        chan error
	output      bytes.Buffer
	trace       []string
	events      []khsier.Event
	beforeWrite func()
	beforeEvent func(khsier.Event)
}

func newSynctestObserver(t *testing.T) *synctestObserver {
	run := &synctestObserver{
		reads: make(chan struct{}, 8), results: make(chan synctestRead, 1),
		stop: make(chan struct{}), done: make(chan error, 1),
	}
	// Release every potentially blocked Read, Write, or callback even when
	// an assertion stops the test, so the bubble can finish its goroutines.
	t.Cleanup(func() {
		close(run.stop)
		synctest.Wait()
	})
	go func() {
		run.done <- khsier.Observe(run, synctestOutput{run}, khsier.Options{Idle: synctestIdle}, func(event khsier.Event) {
			run.mu.Lock()
			run.trace = append(run.trace, string(event.Kind))
			run.events = append(run.events, event)
			run.mu.Unlock()
			if run.beforeEvent != nil {
				run.beforeEvent(event)
			}
		})
	}()
	return run
}

func (run *synctestObserver) Read(p []byte) (int, error) {
	run.reads <- struct{}{}
	select {
	case result := <-run.results:
		return copy(p, result.data), result.err
	case <-run.stop:
		return 0, io.EOF
	}
}

type synctestOutput struct{ run *synctestObserver }

func (output synctestOutput) Write(p []byte) (int, error) {
	run := output.run
	if run.beforeWrite != nil {
		run.beforeWrite()
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	run.trace = append(run.trace, "write:"+string(p))
	return run.output.Write(p)
}

func (run *synctestObserver) deliver(data string, err error) {
	run.results <- synctestRead{data, err}
}

func (run *synctestObserver) expectRead(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case <-run.reads:
	default:
		t.Fatal("next input read has not started")
	}
}

func (run *synctestObserver) expectNoRead(t *testing.T) {
	t.Helper()
	select {
	case <-run.reads:
		t.Fatal("read ahead while output or callback is blocked")
	default:
	}
}

func (run *synctestObserver) expectTrace(t *testing.T, want ...string) {
	t.Helper()
	run.mu.Lock()
	defer run.mu.Unlock()
	if !slices.Equal(run.trace, want) {
		t.Fatalf("callback/write order = %v, want %v", run.trace, want)
	}
}

func (run *synctestObserver) expectFreshIdle(t *testing.T, trace ...string) {
	t.Helper()
	deadline := time.Now().Add(synctestIdle)
	time.Sleep(synctestIdle / 2)
	synctest.Wait()
	run.expectTrace(t, trace...)
	time.Sleep(synctestIdle / 2)
	synctest.Wait()
	run.expectTrace(t, append(slices.Clone(trace), "idle")...)
	if got := run.lastEventTime(); !got.Equal(deadline) {
		t.Fatalf("idle timestamp = %v, want fresh deadline %v", got, deadline)
	}
}

func (run *synctestObserver) expectDone(t *testing.T) {
	t.Helper()
	synctest.Wait()
	select {
	case err := <-run.done:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("Observe has not returned")
	}
}

func (run *synctestObserver) lastEventTime() time.Time {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.events[len(run.events)-1].Timestamp
}
