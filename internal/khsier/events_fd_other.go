//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package khsier

// This file rejects explicit descriptor routing on unsupported platforms.

import (
	"fmt"
	"io"
)

const eventsFDSupported = false

func openEventsFD(int) (io.WriteCloser, error) {
	return nil, fmt.Errorf("not supported on this OS")
}
