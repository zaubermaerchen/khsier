// Package khsier observes stream lifecycle boundaries while copying bytes.
package khsier

import (
	"errors"
	"io"
	"sync"
	"time"
)

const (
	readBufferSize  = 32 * 1024
	zeroReadBackoff = time.Millisecond
)

// EventKind identifies a stream lifecycle transition.
type EventKind string

const (
	// EventBOS marks the first non-empty read, before its bytes are written.
	EventBOS EventKind = "bos"
	// EventIdle marks a pending read that has reached the idle duration after data.
	EventIdle EventKind = "idle"
	// EventResume marks the first non-empty read after idle, before its bytes are written.
	EventResume EventKind = "resume"
	// EventEOS marks input EOF after any accompanying bytes were written successfully.
	EventEOS EventKind = "eos"
)

// Event records a lifecycle transition and its observation time.
// Use named fields when constructing Event values; future versions may add fields.
type Event struct {
	Kind      EventKind
	Timestamp time.Time
}

// Options configures lifecycle observation. Use named fields when constructing values.
type Options struct {
	// Idle enables idle/resume observation when positive. Zero disables it;
	// a negative value is rejected before copying or notifying events.
	Idle time.Duration
}

// Observe copies in to out byte-for-byte and synchronously calls emit for each
// lifecycle transition. A nil emit ignores events. Observe borrows both streams
// and never closes them. EOF is successful; other read/write errors are returned
// without an EOS event, including broken pipes and short writes.
//
// Observe has no cancellation mechanism. The caller must interrupt a blocked
// input Read, for example by closing its own reader. With idle enabled, one
// persistent worker performs reads, without reading ahead while output is blocked.
// Callback and output-write time do not count toward the next idle window.
func Observe(in io.Reader, out io.Writer, opts Options, emit func(Event)) error {
	if opts.Idle < 0 {
		return errors.New("khsier: Idle must not be negative")
	}
	if opts.Idle > 0 {
		return runIdle(opts.Idle, in, out, emit)
	}
	return runPlain(in, out, emit)
}

func notify(emit func(Event), kind EventKind) {
	if emit != nil {
		emit(Event{Kind: kind, Timestamp: time.Now().UTC()})
	}
}

type readResult struct {
	n   int
	err error
}

// readWorker owns one persistent Read goroutine. Requests are explicit so an
// idle run never has more than one read outstanding or reads ahead while the
// stdout path is blocked.
type readWorker struct {
	in       io.Reader
	requests chan []byte
	results  chan readResult
	stopOnce sync.Once
}

func newReadWorker(in io.Reader) *readWorker {
	worker := &readWorker{
		in:       in,
		requests: make(chan []byte),
		results:  make(chan readResult, 1),
	}
	go worker.run()
	return worker
}

func (worker *readWorker) run() {
	for buffer := range worker.requests {
		n, err := worker.in.Read(buffer)
		worker.results <- readResult{n: n, err: err}
	}
}

func (worker *readWorker) request(buffer []byte) {
	worker.requests <- buffer
}

func (worker *readWorker) stop() {
	worker.stopOnce.Do(func() {
		close(worker.requests)
	})
}

func runPlain(in io.Reader, out io.Writer, emit func(Event)) error {
	buffer := make([]byte, readBufferSize)
	seenData := false
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			if !seenData {
				notify(emit, EventBOS)
				seenData = true
			}
			if writeErr := writeChunk(out, buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			notify(emit, EventEOS)
			return nil
		}
		return err
	}
}

func runIdle(idle time.Duration, in io.Reader, out io.Writer, emit func(Event)) error {
	buffer := make([]byte, readBufferSize)
	worker := newReadWorker(in)
	defer worker.stop()
	worker.request(buffer)
	readDone := worker.results
	var timer *time.Timer
	var timerC <-chan time.Time
	seenData := false
	isIdle := false

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerC = nil
	}
	startTimer := func() {
		if timer == nil {
			timer = time.NewTimer(idle)
		} else {
			timer.Reset(idle)
		}
		timerC = timer.C
	}

	for {
		var result readResult
		select {
		case result = <-readDone:
			if result.n > 0 || result.err != nil {
				stopTimer()
			} else if timerC != nil {
				// A Reader may legally return (0, nil). Keep the existing
				// observation window instead of treating that as fresh activity.
				select {
				case <-timerC:
					timerC = nil
					if seenData && !isIdle {
						notify(emit, EventIdle)
						isIdle = true
					}
				default:
				}
			}
		case <-timerC:
			timerC = nil
			// Prefer an already completed read over an idle transition when both
			// become ready together; the bytes were observed before the timer.
			select {
			case result = <-readDone:
				if result.n == 0 && result.err == nil && seenData && !isIdle {
					notify(emit, EventIdle)
					isIdle = true
				}
			default:
				if seenData && !isIdle {
					notify(emit, EventIdle)
					isIdle = true
				}
				continue
			}
		}

		if result.n > 0 {
			if !seenData {
				notify(emit, EventBOS)
				seenData = true
			} else if isIdle {
				notify(emit, EventResume)
			}
			isIdle = false
			if writeErr := writeChunk(out, buffer[:result.n]); writeErr != nil {
				return writeErr
			}
		}
		if result.err != nil {
			if errors.Is(result.err, io.EOF) {
				notify(emit, EventEOS)
				return nil
			}
			return result.err
		}

		if result.n == 0 && result.err == nil {
			// Avoid monopolizing a CPU when a non-blocking Reader repeatedly
			// reports no data. The timer remains the idle boundary; this bounded
			// delay does not reset it.
			time.Sleep(zeroReadBackoff)
			if timerC != nil {
				select {
				case <-timerC:
					timerC = nil
					if seenData && !isIdle {
						notify(emit, EventIdle)
						isIdle = true
					}
				default:
				}
			}
		}
		worker.request(buffer)
		if seenData && !isIdle && timerC == nil {
			startTimer()
		}
	}
}

func writeChunk(out io.Writer, data []byte) error {
	n, err := out.Write(data)
	if n < 0 || n > len(data) {
		return io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
