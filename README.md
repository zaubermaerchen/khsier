# khsier

`khsier` is a small stream-boundary observer. It copies stdin to stdout
byte-for-byte and writes lifecycle records as JSONL to stderr. Stderr is the
event stream; passthrough data never shares stdout with events.

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
