//go:build !windows

package khsier

// This file verifies the real Unix SIGPIPE/EPIPE contract in a subprocess.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSubprocessBrokenPipeExitsSuccessfullyWithoutEOS(t *testing.T) {
	binary := buildKhsierBinary(t)
	cmd := exec.Command(binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	inputDone := make(chan error, 1)
	go func() {
		_, writeErr := stdin.Write(bytes.Repeat([]byte("khsier-payload-"), 1<<20))
		inputDone <- writeErr
	}()
	prefix := make([]byte, 4096)
	prefixDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(stdout, prefix)
		prefixDone <- readErr
	}()
	select {
	case err := <-prefixDone:
		if err != nil {
			t.Fatalf("ReadFull(stdout) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("khsier did not produce stdout before timeout")
	}
	if err := stdout.Close(); err != nil {
		t.Fatalf("Close(stdout) error = %v", err)
	}

	status := processExitCode(t, cmd)
	if status != 0 {
		t.Fatalf("khsier exit code = %d, want 0; stderr = %q", status, stderr.String())
	}
	select {
	case <-inputDone:
	case <-time.After(time.Second):
		t.Fatal("stdin writer did not finish after downstream close")
	}
	if strings.Contains(stderr.String(), `"event":"eos"`) {
		t.Fatalf("stderr = %q, want no eos after downstream close", stderr.String())
	}
}

func buildKhsierBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "khsier")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/khsier")
	command.Dir = filepath.Join(filepath.Dir(sourceFile(t)), "..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build error = %v; output = %s", err, output)
	}
	return binary
}

func sourceFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return file
}

func processExitCode(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		t.Fatalf("Wait() error = %v", err)
		return -1
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("process did not exit before timeout")
		return -1
	}
}
