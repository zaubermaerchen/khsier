# khsier

`khsier` is a small stream-boundary observer. It copies stdin to stdout
byte-for-byte and writes lifecycle records as JSONL to stderr by default.
Use `--events-fd` on supported Unix systems to dedicate a separate descriptor
to events. Passthrough data never shares stdout with events.

**khsier** takes its name from *kiseru* (煙管), a traditional Japanese smoking
pipe—a nod to the Unix pipes it observes.

## Install and build

```sh
go install github.com/zaubermaerchen/khsier/cmd/khsier@latest
```

From a checkout:

```sh
go install ./cmd/khsier
go build -o khsier ./cmd/khsier
```

## Download releases

Tagged binaries are available from [GitHub Releases](https://github.com/zaubermaerchen/khsier/releases).
Each release provides seven archives: `linux_amd64`, `linux_arm64`, `linux_armv6`,
`darwin_amd64`, `darwin_arm64`, `windows_amd64`, and `windows_arm64`.
Each archive contains khsier, LICENSE, README, and the reference documentation.

Verify the downloaded archive against the accompanying `SHA256SUMS` before extracting it.
Replace `vX.Y.Z` with your release tag:

```sh
version=vX.Y.Z
archive="khsier_${version}_linux_amd64.tar.gz"
grep -F -- "  $archive" SHA256SUMS > "$archive.sha256" || exit 1
sha256sum -c "$archive.sha256"
```

On macOS, use `shasum -a 256 -c "$archive.sha256"` with the macOS archive name.
On Windows PowerShell:

```powershell
$version = "vX.Y.Z"
$archive = "khsier_${version}_windows_amd64.zip"
$expected = (Get-Content SHA256SUMS | Where-Object { $_ -like "*  $archive" }).Split()[0]
$actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "checksum mismatch: $archive" }
```

## Example

```sh
producer | khsier --idle 250ms 2>events.jsonl | consumer
```

The consumer receives the original bytes, while `events.jsonl` records stream boundaries
and idle/resume transitions. See the [reference](docs/khsier.md) for precise semantics.

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

| Condition | Status |
| --- | ---: |
| stdin EOF and all writes succeed | 0 |
| downstream broken pipe, with no event-output failure | 0 |
| input failure or stdout failure other than broken pipe | 1 |
| event write or internal event descriptor close failure | 1 |
| invalid arguments, unusable event FD, setup failure, or unsupported explicit option | 1 |

## Go library

The module root exports `Observe` for observing an `io.Reader` while copying to
an `io.Writer`:

```go
err := khsier.Observe(in, out, khsier.Options{Idle: 250 * time.Millisecond}, func(event khsier.Event) {
    switch event.Kind {
    case khsier.EventBOS, khsier.EventIdle, khsier.EventResume, khsier.EventEOS:
        // React to the observed stream boundary.
    }
})
```

Import `github.com/zaubermaerchen/khsier` and `time`. `Event` contains `Kind`
(`EventKind`) and `Timestamp` (`time.Time`). The callback runs synchronously;
`nil` ignores events. `Options{}` disables idle monitoring while retaining
BOS/EOS. A negative `Idle` fails before any copying or event notification.
`Observe` never closes either stream and returns read/write errors, including
broken pipes and short writes. The CLI retains its own JSONL and exit-status
handling. See the [library reference](docs/khsier.md#go-library) for details.

## Related pipeline tools

| Tool | Role |
| --- | --- |
| [`khsier`](https://github.com/zaubermaerchen/khsier) | Observe flow and stream boundaries |
| [`pipewisp`](https://github.com/zaubermaerchen/pipewisp) | React to lifecycle transitions with hooks |
| [`dam`](https://github.com/zaubermaerchen/dam) | Hold flow until release conditions are satisfied |
| [`outage`](https://github.com/zaubermaerchen/outage) | Cut flow when a condition is triggered |
| [`sluice`](https://github.com/zaubermaerchen/sluice) | Switch flow between open and closed states |

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

The implementation and existing tests were extracted from
[pipewisp](https://github.com/zaubermaerchen/pipewisp/tree/c7fe99dc856c8b2d7703d87f321501b175d6c1e8).
