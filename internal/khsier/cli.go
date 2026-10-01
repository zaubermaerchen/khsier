package khsier

// This file parses khsier's intentionally small command-line interface.

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type options struct {
	idle        time.Duration
	idleSet     bool
	showVersion bool
}

func parseArgs(args []string) (options, bool, error) {
	var opts options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--version":
			if len(args) != 1 {
				return options{}, false, fmt.Errorf("--version cannot be combined with other arguments")
			}
			return options{showVersion: true}, false, nil
		case arg == "-h" || arg == "--help":
			if len(args) != 1 {
				return options{}, false, fmt.Errorf("%s cannot be combined with other arguments", arg)
			}
			return options{}, true, nil
		case arg == "--idle":
			if opts.idleSet {
				return options{}, false, fmt.Errorf("--idle specified more than once")
			}
			value, next, err := parseSeparateDuration(args, i)
			if err != nil {
				return options{}, false, err
			}
			opts.idle, opts.idleSet = value, true
			i = next
		case strings.HasPrefix(arg, "--idle="):
			if opts.idleSet {
				return options{}, false, fmt.Errorf("--idle specified more than once")
			}
			value, err := parseDuration(arg[len("--idle="):])
			if err != nil {
				return options{}, false, err
			}
			opts.idle, opts.idleSet = value, true
		case strings.HasPrefix(arg, "-"):
			return options{}, false, fmt.Errorf("unknown option %s", arg)
		default:
			return options{}, false, fmt.Errorf("unexpected positional argument %s", arg)
		}
	}

	if opts.idleSet && opts.idle <= 0 {
		return options{}, false, fmt.Errorf("--idle must be greater than zero")
	}
	return opts, false, nil
}

func parseSeparateDuration(args []string, optionIndex int) (time.Duration, int, error) {
	valueIndex := optionIndex + 1
	if valueIndex >= len(args) || strings.HasPrefix(args[valueIndex], "--") {
		return 0, optionIndex, fmt.Errorf("missing value for --idle")
	}
	value, err := parseDuration(args[valueIndex])
	if err != nil {
		return 0, optionIndex, err
	}
	return value, valueIndex, nil
}

func parseDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("empty duration for --idle")
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for --idle: %w", err)
	}
	return duration, nil
}

func printUsage(out io.Writer) {
	_, _ = io.WriteString(out, "Usage: khsier [--idle DURATION]\n       khsier --version\n")
}
