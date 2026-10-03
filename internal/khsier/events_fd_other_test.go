//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package khsier

// This file verifies startup rejection on unsupported descriptor platforms.

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunEventsFDUnsupported(t *testing.T) {
	input := newTimedReader(readStep{data: []byte("data")})
	var output, diagnostics bytes.Buffer
	if got := Run([]string{"--events-fd=3"}, input, &output, &diagnostics); got != 1 {
		t.Fatalf("status=%d", got)
	}
	if input.readCount() != 0 || output.Len() != 0 || !strings.Contains(diagnostics.String(), "not supported on this OS") {
		t.Fatalf("reads=%d stdout=%q stderr=%q", input.readCount(), output.Bytes(), diagnostics.Bytes())
	}
}
