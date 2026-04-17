# Scenario: Log channel wiring in run command

Relates to: Issue #3

## Setup
- Test runs in `internal/cmd` package using Go's `testing` package
- `github.com/peter-stratton/dark-factory/internal/logging` and `internal/tui` packages available
- A temp directory for log files, cleaned up after each test
- `orchestrator.Run` is replaced with a stub via an existing test seam (or the test exercises the wiring directly through an extracted helper) — no real orchestrator work is executed
- The goroutine that runs `orchestrator.Run` and optionally `runEnterWatch` is synchronized via the existing `errCh` pattern plus a drain of `logCh`

## Cases

### Wrapped logger fans out warn records to both channel and file
- GIVEN a `chan logging.LogLine` of capacity 4, a temp log directory, and a `logFactory` wrapped per the `useTUI` branch (base = `NewLoggerFileOnly`, plus `NewTUIHandler` composed via `WithHandler`)
- WHEN a logger produced by the wrapped factory logs a `LevelWarn` record with message `"missing ANTHROPIC_API_KEY"`
- THEN a `LogLine` arrives on the channel whose `Formatted` field contains the message AND the file `<dir>/debug.log` contains a JSON record with `"msg":"missing ANTHROPIC_API_KEY"` and `"level":"WARN"`

### Wrapped logger still writes info records to file only
- GIVEN the same wrapped factory setup from the previous case
- WHEN a logger produced by the wrapped factory logs a `LevelInfo` record
- THEN no `LogLine` arrives on the channel (filtered at `LevelWarn`) AND `<dir>/debug.log` contains a JSON record at `"level":"INFO"`

### Non-TUI branch is unchanged
- GIVEN `useTUI = false` and the unwrapped default `logFactory` (`logging.NewLogger`)
- WHEN the `run` command's wiring executes the non-TUI path
- THEN `logCh` remains a nil `chan logging.LogLine` AND the logger returned by the factory writes to both `debug.log` and the configured stdout writer exactly as before the change

### Bootstrap logger captures preflight warnings
- GIVEN the TUI branch is active, the wrapped `logFactory` is in place, and `logger` at `run.go:88` is built from the wrapped factory
- WHEN the bootstrap logger is used to emit a `LevelWarn` record representing a preflight failure (simulating `CheckWorkingTree` or `EnsureBaseBranch`)
- THEN a `LogLine` arrives on `logCh` (confirming warnings emitted before `program.Run()` starts are buffered in the channel)

### Per-run logger created inside orchestrator.Run inherits the TUI handler
- GIVEN the wrapped `logFactory` closure, and a second call to that closure representing the run-directory logger created at `orchestrator.go:125`
- WHEN the resulting per-run logger logs a `LevelError` record
- THEN a `LogLine` arrives on `logCh` (confirming the closure composes the TUI handler onto every logger the factory produces, not just the bootstrap one)

### Channel is closed after orchestrator exits without watch
- GIVEN the TUI goroutine path with `watchFlag = false` and a stub `orchestrator.Run` that returns `nil` immediately
- WHEN the goroutine completes
- THEN a receive on `logCh` returns `(_, ok=false)` (channel closed) AND the close occurs after `program.Send(tui.RunDoneMsg{})` has been called

### Channel is closed after orchestrator error
- GIVEN the TUI goroutine path with a stub `orchestrator.Run` that returns a non-nil error
- WHEN the goroutine completes
- THEN `logCh` is closed (the error exit path also closes the channel, preventing the TUI subscription from leaking)

### Channel is closed after watch mode exits
- GIVEN the TUI goroutine path with `watchFlag = true`, a stub `orchestrator.Run` that returns `nil`, and a stub `runEnterWatch` that returns `nil` after a short delay
- WHEN both the stubbed orchestrator and watch functions return
- THEN `logCh` is closed only after `runEnterWatch` returns (not between `orchestrator.Run` and `runEnterWatch`), so warnings emitted during watch are still delivered

### Channel capacity allows buffering during startup burst
- GIVEN `logCh = make(chan logging.LogLine, 256)` and no active subscription yet (simulating the window before `program.Run()` starts)
- WHEN 10 successive `LevelWarn` records are emitted through the wrapped logger
- THEN all 10 `LogLine`s are present on the channel and none are dropped (capacity 256 comfortably absorbs the startup burst)

### Channel full during a very high warning burst drops silently
- GIVEN `logCh` that is already full to its 256-capacity limit
- WHEN one more `LevelWarn` record is emitted through the wrapped logger
- THEN the logger call returns without error AND no goroutine is blocked AND the channel length is still 256 (the new line is dropped at the handler's `chanWriter`)
