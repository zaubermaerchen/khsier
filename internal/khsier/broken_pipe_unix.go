//go:build !windows

package khsier

// This file classifies Unix downstream broken-pipe errors.

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
)

func configureBrokenPipe() func() {
	// Ignore SIGPIPE only long enough for stdout.Write to return EPIPE; other
	// signals retain their default process-termination behavior.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGPIPE)
	return func() {
		signal.Stop(signals)
	}
}

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
