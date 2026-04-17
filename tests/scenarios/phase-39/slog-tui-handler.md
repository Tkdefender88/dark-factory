# Scenario: slog handler for TUI channel

Relates to: Issue #1

## Setup
- Test runs in `internal/logging` package with Go's `testing` package
- `github.com/charmbracelet/log` and `github.com/muesli/termenv` available as dependencies
- `slog.Logger` and `slog.Handler` interfaces from the standard library
- A buffered `chan logging.LogLine` of configurable capacity, created per-test

## Cases

### Warn record is formatted and delivered
- GIVEN a `chan LogLine` of capacity 4 and a handler returned by `NewTUIHandler(ch, slog.LevelWarn)`
- WHEN the handler processes a `slog.Record` at `LevelWarn` with message `"missing ANTHROPIC_API_KEY"` and an attribute `source=auth`
- THEN one `LogLine` arrives on the channel whose `Formatted` field contains both the substring `WARN` and the record message

### Error record is formatted and delivered
- GIVEN a `chan LogLine` of capacity 4 and a handler returned by `NewTUIHandler(ch, slog.LevelWarn)`
- WHEN the handler processes a `slog.Record` at `LevelError` with message `"token refresh failed"`
- THEN one `LogLine` arrives on the channel whose `Formatted` field contains both the substring `ERRO` (or `ERROR`) and the record message

### Info record is filtered at Enabled()
- GIVEN a handler returned by `NewTUIHandler(ch, slog.LevelWarn)`
- WHEN `Enabled(ctx, slog.LevelInfo)` is called
- THEN the call returns `false` and no bytes are written to the channel writer

### Info record with lazy attrs does not evaluate the closure
- GIVEN a handler returned by `NewTUIHandler(ch, slog.LevelWarn)` and an `slog.Logger` wrapping it
- WHEN `logger.Info("hello", slog.Any("lazy", slog.AnyValue(sentinel)))` is called where evaluating `sentinel` would set a flag
- THEN the channel remains empty AND the flag remains unset (attribute evaluation is skipped)

### Channel full drops silently without blocking
- GIVEN a `chan LogLine` of capacity 1 that already holds one line
- WHEN the handler processes a second `slog.Record` at `LevelWarn`
- THEN the `Handle` call returns `nil` within a few microseconds (no blocking) AND the channel still contains exactly one line (the original)

### multiHandler fans out to every inner handler
- GIVEN a `multiHandler` composed of two in-memory test handlers that each record the records they receive
- WHEN `Handle(ctx, r)` is called once with a `LevelWarn` record
- THEN both inner handlers report exactly one received record with matching message and level

### multiHandler Enabled short-circuits on the first enabled handler
- GIVEN a `multiHandler` composed of one disabled handler (always returns false from `Enabled`) and one enabled handler (returns true)
- WHEN `Enabled(ctx, slog.LevelWarn)` is called on the `multiHandler`
- THEN the call returns `true`

### multiHandler with all disabled handlers returns false
- GIVEN a `multiHandler` whose every inner handler returns `false` from `Enabled`
- WHEN `Enabled(ctx, slog.LevelWarn)` is called
- THEN the call returns `false`

### WithHandler composes base and extra handlers
- GIVEN a base `slog.Handler` (in-memory recorder A) and an extra `slog.Handler` (in-memory recorder B)
- WHEN `logging.WithHandler(base, extra)` is called and the resulting handler processes a `LevelWarn` record
- THEN both recorder A and recorder B report the record, AND the base's pre-change single-handler behavior is unaffected when the returned handler is not used

### Existing NewLogger two-handler path still works
- GIVEN a temp directory and a call to `logging.NewLogger(dir)`
- WHEN a `LevelWarn` record is logged
- THEN the record appears both in `debug.log` as valid JSON AND on the captured stdout writer as human-readable text (backwards compatibility is preserved after the multiHandler refactor)
