package khsier

// This file verifies version source precedence and the development fallback.

import (
	"bytes"
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	if got := resolveVersion("snapshot", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true); got != "snapshot" {
		t.Fatalf("resolveVersion() = %q, want snapshot", got)
	}
	if got := resolveVersion("", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true); got != "v1.2.3" {
		t.Fatalf("resolveVersion() = %q, want v1.2.3", got)
	}
	if got := resolveVersion("", &debug.BuildInfo{
		Main:     debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
	}, true); got != "devel" {
		t.Fatalf("resolveVersion() = %q, want devel for dirty build", got)
	}
}

func TestRunCLIVersionUsesLinkerVersion(t *testing.T) {
	previous := version
	version = "v1.2.3"
	t.Cleanup(func() { version = previous })

	var output, diagnostics bytes.Buffer
	if got := Run([]string{"--version"}, versionPanicReader{}, &output, &diagnostics); got != 0 {
		t.Fatalf("Run() = %d, want 0", got)
	}
	if got, want := output.String(), "khsier v1.2.3\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("version diagnostics = %q, want empty", diagnostics.String())
	}
}

type versionPanicReader struct{}

func (versionPanicReader) Read([]byte) (int, error) { panic("version command read stdin") }
