package khsier

// This file runs khsier's synchronous stdin-to-stdout observer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	readBufferSize  = 32 * 1024
	zeroReadBackoff = time.Millisecond
)

type eventEmitter struct {
	out    io.Writer
	failed bool
}

func (emitter *eventEmitter) emit(event string) {
	if emitter == nil || emitter.failed {
		return
	}
	line, err := json.Marshal(struct {
		Event     string `json:"event"`
		Timestamp string `json:"timestamp"`
	}{
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		emitter.failed = true
		return
	}
	line = append(line, '\n')
	if err := writeChunk(emitter.out, line); err != nil {
		emitter.failed = true
	}
}

func (emitter *eventEmitter) status() int {
	if emitter != nil && emitter.failed {
		return 1
	}
	return 0
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

// Run executes khsier with the supplied arguments and streams. It returns the
// process exit status without calling os.Exit.
func Run(args []string, in io.Reader, out, events io.Writer) int {
	opts, help, err := parseArgs(args)
	if err != nil {
		reportDiagnostic(events, err)
		return 1
	}
	if help {
		printUsage(out)
		return 0
	}
	if opts.showVersion {
		printVersion(out)
		return 0
	}

	stopBrokenPipe := configureBrokenPipe()
	defer stopBrokenPipe()

	emitter := &eventEmitter{out: events}
	if opts.idleSet {
		return runIdle(opts.idle, in, out, emitter)
	}
	return runPlain(in, out, emitter)
}

func runPlain(in io.Reader, out io.Writer, emitter *eventEmitter) int {
	buffer := make([]byte, readBufferSize)
	seenData := false
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			if !seenData {
				emitter.emit("bos")
				seenData = true
			}
			if writeErr := writeChunk(out, buffer[:n]); writeErr != nil {
				if isBrokenPipe(writeErr) {
					return emitter.status()
				}
				return 1
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			emitter.emit("eos")
			return emitter.status()
		}
		return 1
	}
}

func runIdle(idle time.Duration, in io.Reader, out io.Writer, emitter *eventEmitter) int {
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
						emitter.emit("idle")
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
					emitter.emit("idle")
					isIdle = true
				}
			default:
				if seenData && !isIdle {
					emitter.emit("idle")
					isIdle = true
				}
				continue
			}
		}

		if result.n > 0 {
			if !seenData {
				emitter.emit("bos")
				seenData = true
			} else if isIdle {
				emitter.emit("resume")
			}
			isIdle = false
			if writeErr := writeChunk(out, buffer[:result.n]); writeErr != nil {
				if isBrokenPipe(writeErr) {
					return emitter.status()
				}
				return 1
			}
		}
		if result.err != nil {
			if errors.Is(result.err, io.EOF) {
				emitter.emit("eos")
				return emitter.status()
			}
			return 1
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
						emitter.emit("idle")
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

func reportDiagnostic(out io.Writer, err error) {
	_, _ = fmt.Fprintf(out, "khsier: %v\n", err)
}
