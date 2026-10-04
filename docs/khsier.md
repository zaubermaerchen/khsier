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
khsier --describe
```

`--idle` is optional and accepts a positive Go duration such as `250ms` or
`2s`. Both `--idle DURATION` and `--idle=DURATION` are supported. Without the
option, khsier emits only `bos` and `eos`. With it, khsier can additionally
emit `idle` and `resume`.

`--events-fd N` and `--events-fd=N` select a writable decimal file descriptor
of at least 3; duplicate specification is rejected. Writable descriptors are
accepted, including regular files and pipes. The descriptor is borrowed:
khsier duplicates it internally, closes only that duplicate, and leaves the
original descriptor and its mode/flags unchanged. Writes remain synchronous;
blocking descriptors are recommended. A nonblocking descriptor is accepted, but if it is not ready for a
write, `EAGAIN` (or `EWOULDBLOCK`) is an event-output failure: khsier disables
later events, continues forwarding data, and returns status 1 without a runtime
diagnostic. Interrupted writes (`EINTR`) also fail without retry. khsier does
not wait for a nonblocking descriptor to become writable or change its flags.

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

The following table applies to stream mode. See the description section below
for `--describe` exit statuses.

| Condition | Status |
| --- | ---: |
| stdin EOF and all writes succeed | 0 |
| downstream broken pipe, with no event-output failure | 0 |
| input failure or stdout failure other than broken pipe | 1 |
| event write or internal event descriptor close failure | 1 |
| invalid arguments, unusable event FD, setup failure, or unsupported explicit option | 1 |

## Machine-readable description

`khsier --describe` writes exactly one JSON object followed by a newline to
stdout. It describes the static contract and capabilities of that binary,
not the supplied invocation or runtime resources. This is a standalone
terminal mode: it never reads stdin, forwards stream data, emits lifecycle
events, or prepares/opens an event FD, whether it succeeds or fails.

Every combination with other arguments is rejected, including `--idle`,
`--events-fd`, `--help`, `-h`, `--version`, positional arguments, and duplicate
`--describe`. Rejection produces no description, a stderr diagnostic, and
status 1. A complete description and trailing newline return status 0.
Any stdout failure, including EPIPE or a short write, produces a stderr
diagnostic and status 1; stdout may contain an incomplete JSON object. Stream
mode's successful stdout-EPIPE policy does not apply to this terminal mode.

### Description fields

| Field | Meaning |
| --- | --- |
| `schema_version` | Integer description-contract version, initially 1. |
| `tool` | `name: "khsier"` and `version`, using the same value as `--version`, including `devel`. |
| `options` | Named CLI options with `supported`, accepted `forms`, and applicable `value` or `standalone` metadata. |
| `events` | Named lifecycle events with `requires_options` and a short supplemental `description`. |
| `event_record` | Lifecycle JSONL encoding and record field types/formats. |
| `stream` | Structured byte-preservation, read-ahead, and idle/backpressure guarantees. |
| `event_output` | Default/dedicated destinations, synchronous delivery, retry/diagnostic policy, and failure behavior. |
| `eos` | Event-output durability and close-failure limitations. |
| `exit_status` | Separate `stream` and `describe` exit-status mappings. |

`options` includes `--idle`, `--events-fd`, `--help` (including form `-h`),
`--version`, and `--describe`. The last three have `standalone: true`.
`--idle.value` has `type: "string"`, `format: "go-duration"`, and
`positive: true`. `--events-fd.value` has `type: "integer"`,
`notation: "decimal"`, and `minimum: 3`, with no `maximum`. The value type
expresses the argument's meaning; all argv tokens themselves are text.
Omitting a maximum does not promise that every integer of at least 3 names a
usable FD or alter existing startup validation.

`--events-fd.supported` is true on the supported Unix builds listed above and
false on Windows and other unsupported builds. The option appears in every
description. Consumers use this field directly rather than infer support
from an OS name. Capability reporting does not probe FD availability or write
readiness. Other listed options have `supported: true` on every build.

Each event's `requires_options` is an array of required option names:

| Event | `requires_options` |
| --- | --- |
| `bos` | `[]` |
| `idle` | `["--idle"]` |
| `resume` | `["--idle"]` |
| `eos` | `[]` |

Prerequisites make an event available; occurrence still depends on its
lifecycle condition. Short descriptions express BOS/resume ordering before
forwarding, idle's input-read wait, and EOS after EOF and successful forwarding
of accompanying data. Empty input may produce EOS without BOS.

`event_record.encoding` is `"jsonl"`; `fields.event` has `type: "string"`
and a short description identifying an event name. `fields.timestamp` has
`type: "string"`, `format: "RFC3339Nano"`, and `timezone: "UTC"`.
This describes lifecycle records, not the description object itself.

`stream.byte_preserving` is true. `read_ahead`,
`idle_timer_during_stdout_backpressure`, and
`zero_byte_read_resets_idle_timer` are false.

`event_output` has `default_fd: 2`, `dedicated_fd_option: "--events-fd"`,
`synchronous: true`, `retry_failed_writes: false`, and
`runtime_diagnostics: false`. Its `on_failure` has
`disable_later_events: true`, `continue_forwarding: true`, and
`exit_status: 1`. A short description supplements the startup-diagnostic and
dedicated-JSONL guarantees. EAGAIN/EWOULDBLOCK and EINTR retain the event-write
failure policy described above.

`eos.guarantees_event_durability` and
`eos.retracted_after_event_fd_close_failure` are false.

`exit_status.stream` maps `success` and `stdout_epipe` to 0; `input_failure`,
`other_stdout_failure`, `event_write_failure`, `event_fd_close_failure`, and
`startup_failure` to 1. `event_failure_overrides_stdout_epipe: true` qualifies
the stdout-EPIPE value. Startup failure includes argument/FD/setup errors and
explicit unsupported options. `exit_status.describe` maps `success` to 0,
and `argument_failure` and `output_failure` to 1.

### Compatibility

The description starts at `schema_version: 1`. It is independent of
`tool.version`: ordinary releases keep the schema version if the description
contract is unchanged. It is neither a protocol version for the entire CLI
nor an added field in lifecycle records.

Removing an existing field, changing its type, or incompatibly changing its
externally observable meaning requires a schema-version increase. This also
covers incompatible changes to an existing event's occurrence conditions or
other observable semantics. Adding fields, options, or event descriptions is
allowed under the same schema version.

Consumers ignore unknown fields and identify options/events by their unique
names, not array positions; they must not assume a permanently closed set of
names. JSON key order, array order, whitespace, and exact human-readable
explanatory wording are not contractual. Rewording prose without changing
observable meaning does not change the schema version.

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
