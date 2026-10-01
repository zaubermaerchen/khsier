//go:build windows

package khsier

// This file classifies Windows downstream broken-pipe errors.

import (
	"errors"
	"syscall"
)

const windowsErrorNoData = syscall.Errno(0xe8) // ERROR_NO_DATA (232): the pipe is being closed.

func configureBrokenPipe() func() { return func() {} }

func isBrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windowsErrorNoData)
}
