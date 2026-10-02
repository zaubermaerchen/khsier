# khsier

`khsier` is a small stream-boundary observer. It copies stdin to stdout
byte-for-byte and writes lifecycle records as JSONL to stderr. Stderr is the
event stream; passthrough data never shares stdout with events.

## Usage

```text
khsier [--idle DURATION]
khsier --help
khsier --version
```

`--idle` is optional and accepts a positive Go duration such as `250ms` or
`2s`. Both `--idle DURATION` and `--idle=DURATION` are supported. Without the
option, khsier emits only `bos` and `eos`. With it, khsier can additionally
emit `idle` and `resume`.

Every event is one JSON object with exactly these fields:

```json
{"event":"bos","timestamp":"2026-09-21T00:00:00.123456789Z"}
```

Timestamps are UTC RFC3339Nano values. Events are written synchronously. A
`bos` or `resume` record is written immediately after the corresponding data is
observed by stdin and before those same bytes are written to stdout.

## Lifecycle

`bos` is emitted for the first non-empty stdin read. In idle mode, `idle` is
emitted once when the observer has already seen data and is waiting for its
next stdin read for the configured duration. `resume` is emitted when data is
subsequently observed after that idle transition. `eos` is emitted only after
stdin returns `io.EOF` and any data returned with that EOF has been copied
successfully.

The idle definition is intentionally about readiness to wait for the next
read, not elapsed wall-clock time since the last data observation. In
particular, stdout backpressure stops the idle timer: khsier does not start the
next read until the preceding stdout write completes, and it performs no
read-ahead or additional buffering. A zero-byte, nil-error read is not fresh
activity: it keeps the same idle observation window while khsier proceeds with
the single next read. An initially empty stream therefore emits `eos` without
a preceding `bos`.

Signals and stdout write failures do not produce `eos`, because neither means
that stdin EOF was observed. SIGINT, SIGTERM, and similar signals retain their
normal process-termination behavior. Unix SIGPIPE is handled only so a
downstream broken pipe can be observed as EPIPE and treated as a successful
early termination; other stdout failures return status 1. If an event write
fails, subsequent event writes are disabled, passthrough continues, and the
final status is 1. No diagnostic is appended to the event stream.

## Exit status

| Condition | Status |
| --- | ---: |
| stdin EOF and all writes succeed | 0 |
| downstream broken pipe | 0 |
| stdout failure other than broken pipe | 1 |
| event write failure | 1 |
| invalid command-line arguments | 1 |

## Go library

Import `github.com/zaubermaerchen/khsier`. The root package exposes:

```go
func Observe(in io.Reader, out io.Writer, opts Options, emit func(Event)) error

type Options struct {
    Idle time.Duration
}

type EventKind string

const (
    EventBOS    EventKind = "bos"
    EventIdle   EventKind = "idle"
    EventResume EventKind = "resume"
    EventEOS    EventKind = "eos"
)

type Event struct {
    Kind      EventKind
    Timestamp time.Time
}
```

Use named fields when constructing `Options` or `Event` values. Future versions
may add fields. Events currently contain only their kind and observation time;
`Timestamp` is a UTC `time.Time`, captured immediately before callback delivery.
JSONL formatting belongs to the CLI.

`Observe` owns the copy operation and borrows both streams without closing
them. Its lifecycle ordering, no-read-ahead behavior, and idle semantics match
the CLI described above. The callback runs synchronously on the observer's
calling goroutine and does not return an error. A slow callback delays copying
and further event processing. Passing `nil` ignores events while copying normally.

`Options.Idle == 0` disables idle/resume monitoring; BOS/EOS remain enabled.
A positive value enables idle monitoring. A negative value returns an error
before reading, writing, or notifying any event. The CLI continues to accept
only positive `--idle` durations.

Input EOF returns `nil` after any accompanying data is successfully copied and
EOS is delivered. Other input/output errors are returned without EOS, including
output broken pipes and `io.ErrShortWrite`. CLI-specific handling of broken
pipes and failed JSONL writes remains in the CLI.

There is no context or cancellation API. The caller is responsible for
interrupting blocked input reads (for example, by closing a reader it owns),
and for managing blocked output writes or callbacks. With idle enabled, a
persistent read worker performs one explicitly requested read at a time;
`Observe` waits for that read and never closes the input to interrupt it.
