package khsier

// This file verifies the standalone machine-readable description contract.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func describeOpener(t *testing.T) func(int) (io.WriteCloser, error) {
	t.Helper()
	return func(int) (io.WriteCloser, error) { t.Fatal("description prepared an event FD"); return nil, nil }
}

func TestRunDescribeContract(t *testing.T) {
	var output, diagnostics bytes.Buffer
	input := newTimedReader(readStep{data: []byte("must not read")})
	if status := runWithEventsOpener([]string{"--describe"}, input, &output, &diagnostics, describeOpener(t)); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, diagnostics.String())
	}
	if input.readCount() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("reads=%d stderr=%q", input.readCount(), diagnostics.String())
	}
	if !bytes.HasSuffix(output.Bytes(), []byte("\n")) {
		t.Fatal("missing trailing newline")
	}
	decoder := json.NewDecoder(&output)
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("second JSON value: %v", err)
	}
	facts := map[string]any{
		"schema_version": float64(1), "tool.name": "khsier", "tool.version": currentVersion(),
		"event_record.encoding": "jsonl", "event_record.fields.event.type": "string",
		"event_record.fields.timestamp.type": "string", "event_record.fields.timestamp.format": "RFC3339Nano", "event_record.fields.timestamp.timezone": "UTC",
		"stream.byte_preserving": true, "stream.read_ahead": false, "stream.idle_timer_during_stdout_backpressure": false, "stream.zero_byte_read_resets_idle_timer": false,
		"event_output.default_fd": float64(2), "event_output.dedicated_fd_option": "--events-fd", "event_output.synchronous": true, "event_output.retry_failed_writes": false, "event_output.runtime_diagnostics": false,
		"event_output.on_failure.disable_later_events": true, "event_output.on_failure.continue_forwarding": true, "event_output.on_failure.exit_status": float64(1),
		"eos.guarantees_event_durability": false, "eos.retracted_after_event_fd_close_failure": false,
		"exit_status.stream.success": float64(0), "exit_status.stream.stdout_epipe": float64(0), "exit_status.stream.input_failure": float64(1), "exit_status.stream.other_stdout_failure": float64(1),
		"exit_status.stream.event_write_failure": float64(1), "exit_status.stream.event_fd_close_failure": float64(1), "exit_status.stream.startup_failure": float64(1), "exit_status.stream.event_failure_overrides_stdout_epipe": true,
		"exit_status.describe.success": float64(0), "exit_status.describe.argument_failure": float64(1), "exit_status.describe.output_failure": float64(1),
	}
	for path, want := range facts {
		if got := descriptionField(t, doc, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s=%#v want=%#v", path, got, want)
		}
	}
	for _, path := range []string{"event_record.fields.event.description", "event_output.description"} {
		if value, ok := descriptionField(t, doc, path).(string); !ok || strings.TrimSpace(value) == "" {
			t.Errorf("%s must be nonempty text", path)
		}
	}
	options := descriptionByName(t, doc, "options")
	expectedForms := map[string][]any{
		"--idle": {"--idle DURATION", "--idle=DURATION"}, "--events-fd": {"--events-fd N", "--events-fd=N"}, "--help": {"--help", "-h"}, "--version": {"--version"}, "--describe": {"--describe"},
	}
	unix := map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true, "ios": true, "linux": true, "netbsd": true, "openbsd": true, "solaris": true}
	for name, forms := range expectedForms {
		option, ok := options[name]
		if !ok {
			t.Fatalf("missing option %s", name)
		}
		supported := name != "--events-fd" || unix[runtime.GOOS]
		actualForms, ok := option["forms"].([]any)
		formsMatch := ok && len(actualForms) == len(forms)
		if formsMatch {
			seen := make(map[any]bool, len(actualForms))
			for _, form := range actualForms {
				if seen[form] {
					formsMatch = false
					break
				}
				seen[form] = true
			}
			for _, form := range forms {
				if !seen[form] {
					formsMatch = false
				}
			}
		}
		if option["supported"] != supported || !formsMatch {
			t.Errorf("option %s=%v", name, option)
		}
		if name == "--help" || name == "--version" || name == "--describe" {
			if option["standalone"] != true {
				t.Errorf("%s not standalone", name)
			}
		}
	}
	for name, fields := range map[string]map[string]any{
		"--idle": {"type": "string", "format": "go-duration", "positive": true}, "--events-fd": {"type": "integer", "notation": "decimal", "minimum": float64(3)},
	} {
		for key, want := range fields {
			if got := descriptionField(t, options[name], "value."+key); got != want {
				t.Errorf("%s value.%s=%v want=%v", name, key, got, want)
			}
		}
	}
	if _, exists := descriptionField(t, options["--events-fd"], "value").(map[string]any)["maximum"]; exists {
		t.Error("FD maximum advertised")
	}
	events := descriptionByName(t, doc, "events")
	for name, prerequisites := range map[string][]any{"bos": {}, "eos": {}, "idle": {"--idle"}, "resume": {"--idle"}} {
		event, ok := events[name]
		if !ok {
			t.Fatalf("missing event %s", name)
		}
		if !reflect.DeepEqual(event["requires_options"], prerequisites) {
			t.Errorf("%s prerequisites=%v", name, event["requires_options"])
		}
		if text, ok := event["description"].(string); !ok || strings.TrimSpace(text) == "" {
			t.Errorf("%s missing description", name)
		}
	}
}

func descriptionField(t *testing.T, doc map[string]any, path string) any {
	t.Helper()
	var value any = doc
	for _, key := range strings.Split(path, ".") {
		fields, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s parent of %s is not object", path, key)
		}
		value, ok = fields[key]
		if !ok {
			t.Fatalf("missing %s", path)
		}
	}
	return value
}

func descriptionByName(t *testing.T, doc map[string]any, key string) map[string]map[string]any {
	t.Helper()
	entries, ok := doc[key].([]any)
	if !ok {
		t.Fatalf("%s is not array", key)
	}
	result := make(map[string]map[string]any)
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("%s entry not object", key)
		}
		name, ok := fields["name"].(string)
		if !ok || name == "" {
			t.Fatalf("%s invalid name", key)
		}
		if _, exists := result[name]; exists {
			t.Fatalf("duplicate %s name %s", key, name)
		}
		result[name] = fields
	}
	return result
}

func TestRunDescribeRejectsCombinations(t *testing.T) {
	for _, other := range [][]string{{"--idle", "1s"}, {"--idle=1s"}, {"--idle"}, {"--idle=bad"}, {"--idle=0"}, {"--events-fd", "3"}, {"--events-fd=3"}, {"--events-fd"}, {"--events-fd=bad"}, {"--help"}, {"-h"}, {"--version"}, {"--describe"}, {"--"}, {"file"}, {"--unknown"}, {"--describe=true"}} {
		for _, first := range []bool{true, false} {
			args := append([]string{"--describe"}, other...)
			if !first {
				args = append(append([]string{}, other...), "--describe")
			}
			t.Run(strings.Join(args, "_"), func(t *testing.T) {
				var out, diagnostics bytes.Buffer
				input := newTimedReader(readStep{data: []byte("must not read")})
				status := runWithEventsOpener(args, input, &out, &diagnostics, describeOpener(t))
				if status != 1 || input.readCount() != 0 || out.Len() != 0 || !strings.HasPrefix(diagnostics.String(), "khsier: ") || strings.Contains(diagnostics.String(), `"event"`) {
					t.Fatalf("status=%d reads=%d stdout=%q stderr=%q", status, input.readCount(), out.String(), diagnostics.String())
				}
			})
		}
	}
}

func TestRunDescribeOutputFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		writer  io.Writer
		message string
	}{
		{"EPIPE", errorWriter{syscall.EPIPE}, syscall.EPIPE.Error()}, {"error", errorWriter{errors.New("output unavailable")}, "output unavailable"},
		{"short", partialWriter{max: 1}, io.ErrShortWrite.Error()}, {"negative", invalidCountWriter{n: -1}, io.ErrShortWrite.Error()}, {"oversized", invalidCountWriter{n: 1 << 30}, io.ErrShortWrite.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			var diagnostics bytes.Buffer
			input := newTimedReader(readStep{data: []byte("must not read")})
			status := runWithEventsOpener([]string{"--describe"}, input, test.writer, &diagnostics, describeOpener(t))
			if status != 1 || input.readCount() != 0 || !strings.Contains(diagnostics.String(), test.message) || strings.Contains(diagnostics.String(), `"event"`) {
				t.Fatalf("status=%d reads=%d stderr=%q", status, input.readCount(), diagnostics.String())
			}
		})
	}
}

func TestRunDescribeVersion(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	for _, configured := range []string{"", "v0.8.0"} {
		version = configured
		var output, versionOutput, diagnostics bytes.Buffer
		if Run([]string{"--describe"}, versionPanicReader{}, &output, &diagnostics) != 0 {
			t.Fatalf("describe failed: %s", diagnostics.String())
		}
		if Run([]string{"--version"}, versionPanicReader{}, &versionOutput, &diagnostics) != 0 {
			t.Fatal("version failed")
		}
		var doc map[string]any
		if err := json.Unmarshal(output.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if got := descriptionField(t, doc, "tool.version"); versionOutput.String() != "khsier "+got.(string)+"\n" {
			t.Fatalf("version mismatch: %v %s", got, versionOutput.String())
		}
		if doc["schema_version"] != float64(1) {
			t.Fatal("release changed schema")
		}
		if configured == "" && descriptionField(t, doc, "tool.version") != "devel" {
			t.Fatal("missing development fallback")
		}
	}
}
