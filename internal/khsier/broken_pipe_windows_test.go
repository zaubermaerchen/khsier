//go:build windows

package khsier

// This file verifies Windows downstream broken-pipe classifications.

import (
	"errors"
	"syscall"
	"testing"
)

func TestIsBrokenPipeWindows(t *testing.T) {
	for _, err := range []error{
		syscall.ERROR_BROKEN_PIPE,
		syscall.Errno(0xe8),
		syscall.EPIPE,
		errors.Join(errors.New("wrapped"), syscall.Errno(0xe8)),
	} {
		if !isBrokenPipe(err) {
			t.Errorf("isBrokenPipe(%v) = false, want true", err)
		}
	}
	if isBrokenPipe(errors.New("ordinary output failure")) {
		t.Fatal("isBrokenPipe(ordinary error) = true, want false")
	}
}
