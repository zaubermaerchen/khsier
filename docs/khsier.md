# khsier

`khsier` is a small stream-boundary observer. It copies stdin to stdout
byte-for-byte and writes lifecycle records as JSONL to stderr by default.
Use `--events-fd` on supported Unix systems to dedicate a separate descriptor
to events. Passthrough data never shares stdout with events.

## Usage

```text
khsier [--idle DURATION] [--events-fd N]
khsier --help
khsier --version
```

`--idle` is optional and accepts a positive Go duration such as `250ms` or
`2s`. Both `--idle DURATION` and `--idle=DURATION` are supported. Without the
option, khsier emits only `bos` and `eos`. With it, khsier can additionally
emit `idle` and `resume`.

`--events-fd N` and `--events-fd=N` select a writable decimal file descriptor
of at least 3; duplicate specification is rejected. Regular files and pipes
are supported. The descriptor is borrowed: khsier duplicates it internally,
closes only that duplicate, and leaves the original descriptor and its
mode/flags unchanged. Writes remain synchronous; nonblocking mode is not required.

Dedicated event output is supported on AIX, Android, macOS (Darwin),
DragonFly BSD, FreeBSD, illumos, iOS, Linux, NetBSD, OpenBSD, and Solaris.
Windows and other unsupported systems retain default output behavior but
reject an explicit `--events-fd` before reading stdin.

```sh
producer | khsier --events-fd 3 3>events.jsonl | consumer
```

The selected descriptor receives only lifecycle JSONL. Human-readable startup
diagnostics always go to stderr. Without `--events-fd`, stderr receives both
lifecycle events and possible startup diagnostics, so it is not unconditionally
JSONL. Invalid arguments or an unusable descriptor fail before reading stdin,
with status 1 and a stderr diagnostic; events never fall back to stderr.

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
final status is 1, even if stdout subsequently fails with EPIPE. Event writes
are not retried and the destination does not change. No runtime diagnostic is
appended to either the event stream or stderr. A failed event write can leave
an incomplete final JSONL record.

Failure to close the internal event descriptor duplicate also returns status 1,
without a diagnostic. EOS means input EOF was observed and its accompanying
data was successfully forwarded; it does not guarantee event-output durability.
A subsequent close failure does not retract an already delivered EOS. khsier
does not call fsync.

## Exit status

| Condition | Status |
| --- | ---: |
| stdin EOF and all writes succeed | 0 |
| downstream broken pipe, with no event-output failure | 0 |
| input failure or stdout failure other than broken pipe | 1 |
| event write or internal event descriptor close failure | 1 |
| invalid arguments, unusable event FD, setup failure, or unsupported explicit option | 1 |

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
output broken pipes and `io.ErrShortWrite`. Returned errors preserve the
underlying read/write error in the Go error chain, so callers can detect it
with `errors.Is`. Exact error identity is not guaranteed. CLI-specific handling
of broken pipes and failed JSONL writes remains in the CLI.

There is no context or cancellation API. The caller is responsible for
interrupting blocked input reads (for example, by closing a reader it owns),
and for managing blocked output writes or callbacks. With idle enabled, a
persistent read worker performs one explicitly requested read at a time;
`Observe` waits for that read and never closes the input to interrupt it.
