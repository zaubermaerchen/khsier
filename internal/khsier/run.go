package khsier

// This file adapts the public observer to CLI JSONL and exit-status semantics.

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	observer "github.com/zaubermaerchen/khsier"
)

type eventEmitter struct {
	out    io.Writer
	failed bool
}

func (emitter *eventEmitter) emit(event observer.Event) {
	if emitter == nil || emitter.failed {
		return
	}
	line, err := json.Marshal(struct {
		Event     string `json:"event"`
		Timestamp string `json:"timestamp"`
	}{
		Event:     string(event.Kind),
		Timestamp: event.Timestamp.UTC().Format(time.RFC3339Nano),
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

// Run executes khsier with the supplied arguments and streams. It returns the
// process exit status without calling os.Exit.
func Run(args []string, in io.Reader, out, events io.Writer) int {
	return runWithEventsOpener(args, in, out, events, openEventsFD)
}

func runWithEventsOpener(args []string, in io.Reader, out, events io.Writer, openEvents func(int) (io.WriteCloser, error)) int {
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

	var dedicated io.WriteCloser
	if opts.eventsFDSet {
		dedicated, err = openEvents(opts.eventsFD)
		if err != nil {
			reportDiagnostic(events, fmt.Errorf("--events-fd: %w", err))
			return 1
		}
		events = dedicated
	}

	stopBrokenPipe := configureBrokenPipe()
	defer stopBrokenPipe()

	emitter := &eventEmitter{out: events}
	stdout := &outputWriter{Writer: out}
	err = observer.Observe(in, stdout, observer.Options{Idle: opts.idle}, emitter.emit)
	// Closing the owned duplicate is part of event delivery, even after stdout
	// EPIPE. EOS records input completion, not event-output durability.
	if dedicated != nil && dedicated.Close() != nil {
		emitter.failed = true
	}
	if err != nil && !isBrokenPipe(stdout.err) {
		return 1
	}
	return emitter.status()
}

// Only a stdout EPIPE is a successful CLI termination; an input EPIPE remains
// a read failure even though Observe returns both stream errors to its caller.
type outputWriter struct {
	io.Writer
	err error
}

func (out *outputWriter) Write(p []byte) (int, error) {
	n, err := out.Writer.Write(p)
	out.err = err
	if n < 0 || n > len(p) {
		// An invalid count takes precedence over EPIPE in Observe too, so it
		// must not become a successful broken-pipe termination here.
		out.err = io.ErrShortWrite
	}
	return n, err
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
