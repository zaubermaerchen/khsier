package khsier

import (
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want options
	}{
		{name: "default", want: options{}},
		{
			name: "separated idle",
			args: []string{"--idle", "250ms"},
			want: options{idle: 250 * time.Millisecond, idleSet: true},
		},
		{
			name: "equals idle",
			args: []string{"--idle=2s"},
			want: options{idle: 2 * time.Second, idleSet: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, help, err := parseArgs(tt.args)
			if err != nil {
				t.Fatalf("parseArgs() error = %v", err)
			}
			if help {
				t.Fatal("parseArgs() help = true, want false")
			}
			if got != tt.want {
				t.Fatalf("parseArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseArgsRejectsInvalidIdle(t *testing.T) {
	for _, args := range [][]string{
		{"--idle"},
		{"--idle", "0s"},
		{"--idle", "-1s"},
		{"--idle", "not-a-duration"},
		{"--idle=0s"},
		{"--idle=1s", "--idle=2s"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, _, err := parseArgs(args); err == nil {
				t.Fatalf("parseArgs(%q) error = nil, want error", args)
			}
		})
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	var output, diagnostics strings.Builder
	if got := Run([]string{"--help"}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("Run(--help) = %d, want 0", got)
	}
	if want := "Usage: khsier [--idle DURATION]\n       khsier --version\n"; output.String() != want {
		t.Fatalf("help = %q, want %q", output.String(), want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("help diagnostics = %q, want empty", diagnostics.String())
	}

	output.Reset()
	if got := Run([]string{"--version"}, strings.NewReader("input"), &output, &diagnostics); got != 0 {
		t.Fatalf("Run(--version) = %d, want 0", got)
	}
	if !strings.HasPrefix(output.String(), "khsier ") || !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("version = %q, want khsier <version> line", output.String())
	}
}
