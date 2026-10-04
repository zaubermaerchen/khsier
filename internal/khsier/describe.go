package khsier

// This file renders the binary's static public contract and capabilities.

import (
	"encoding/json"
	"io"
)

type optionDescription struct {
	Name       string         `json:"name"`
	Supported  bool           `json:"supported"`
	Forms      []string       `json:"forms"`
	Value      map[string]any `json:"value,omitempty"`
	Standalone bool           `json:"standalone,omitempty"`
}

type eventDescription struct {
	Name            string   `json:"name"`
	RequiresOptions []string `json:"requires_options"`
	Description     string   `json:"description"`
}

func printDescription(out io.Writer) error {
	description := map[string]any{
		"schema_version": 1,
		"tool":           map[string]any{"name": "khsier", "version": currentVersion()},
		"options": []optionDescription{
			{Name: "--idle", Supported: true, Forms: []string{"--idle DURATION", "--idle=DURATION"}, Value: map[string]any{"type": "string", "format": "go-duration", "positive": true}},
			{Name: "--events-fd", Supported: eventsFDSupported, Forms: []string{"--events-fd N", "--events-fd=N"}, Value: map[string]any{"type": "integer", "notation": "decimal", "minimum": 3}},
			{Name: "--help", Supported: true, Forms: []string{"--help", "-h"}, Standalone: true},
			{Name: "--version", Supported: true, Forms: []string{"--version"}, Standalone: true},
			{Name: "--describe", Supported: true, Forms: []string{"--describe"}, Standalone: true},
		},
		"events": []eventDescription{
			{Name: "bos", RequiresOptions: []string{}, Description: "First nonempty input read; emitted before forwarding its data."},
			{Name: "idle", RequiresOptions: []string{"--idle"}, Description: "After first nonempty input, input-read wait reaches the configured duration; once per idle transition."},
			{Name: "resume", RequiresOptions: []string{"--idle"}, Description: "Nonempty input read after idle; emitted before forwarding its data."},
			{Name: "eos", RequiresOptions: []string{}, Description: "Input EOF observed and accompanying data successfully forwarded."},
		},
		"event_record": map[string]any{
			"encoding": "jsonl",
			"fields": map[string]any{
				"event":     map[string]any{"type": "string", "description": "Name from events."},
				"timestamp": map[string]any{"type": "string", "format": "RFC3339Nano", "timezone": "UTC"},
			},
		},
		"stream": map[string]any{"byte_preserving": true, "read_ahead": false, "idle_timer_during_stdout_backpressure": false, "zero_byte_read_resets_idle_timer": false},
		"event_output": map[string]any{
			"default_fd": 2, "dedicated_fd_option": "--events-fd", "synchronous": true, "retry_failed_writes": false, "runtime_diagnostics": false,
			"on_failure":  map[string]any{"disable_later_events": true, "continue_forwarding": true, "exit_status": 1},
			"description": "Startup diagnostics use stderr. Dedicated output contains only lifecycle JSONL; a failed write may leave an incomplete record.",
		},
		"eos": map[string]any{"guarantees_event_durability": false, "retracted_after_event_fd_close_failure": false},
		"exit_status": map[string]any{
			"stream":   map[string]any{"success": 0, "stdout_epipe": 0, "input_failure": 1, "other_stdout_failure": 1, "event_write_failure": 1, "event_fd_close_failure": 1, "startup_failure": 1, "event_failure_overrides_stdout_epipe": true},
			"describe": map[string]any{"success": 0, "argument_failure": 1, "output_failure": 1},
		},
	}
	data, err := json.Marshal(description)
	if err != nil {
		return err
	}
	return writeChunk(out, append(data, '\n'))
}
