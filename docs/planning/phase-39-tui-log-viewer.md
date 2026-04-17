# Phase 39: TUI Log Viewer

> **Goal:** When the TUI is running, warn- and error-level log records appear
> live in a scrollable panel so users can see why a run is stalled (missing API
> keys, auth failures, etc.) without exiting to tail `debug.log`.

## Milestone

`Phase 39: TUI Log Viewer`

---

## Issue 1: slog handler for TUI channel

### Description

Add a new `slog.Handler` in `internal/logging` that formats records with
`github.com/charmbracelet/log` (for colored level prefixes and aligned
key=value attrs) and non-blocking-sends each formatted line onto a caller-
supplied buffered channel. Filter records below `slog.LevelWarn` so the TUI
panel shows only warnings and errors, not routine info chatter. Generalize
the existing `multiHandler` from two fixed fields to a variadic handler
slice so the TUI handler can be composed alongside the JSON file handler
without introducing a parallel fanout type.

This issue touches 2 existing files and adds 1 new file, all in the
`foundation` layer (`internal/logging/`). The main complexity is forcing
ANSI color through charmbracelet/log when the underlying writer is not a
TTY (the bytes land in a channel, not stdout).

### Key constraints

- Create `internal/logging/tui_handler.go` with the following exported types:
  ```go
  // LogLine is a single formatted log entry destined for the TUI panel.
  type LogLine struct {
      Formatted string
  }

  // NewTUIHandler returns a slog.Handler that formats warn+ records with
  // charmbracelet/log and non-blocking-sends each line onto ch. Records
  // below minLevel are dropped at Enabled() so attribute closures are
  // not evaluated for filtered records.
  func NewTUIHandler(ch chan<- LogLine, minLevel slog.Level) slog.Handler
  ```
- The handler is implemented as two composed types defined in the same file:
  ```go
  type filteredHandler struct {
      min   slog.Level
      inner slog.Handler
  }
  // Enabled returns false for levels below h.min so slog skips attribute
  // evaluation. Handle/WithAttrs/WithGroup delegate to inner.
  ```
  And a `chanWriter` that wraps `chan<- LogLine`:
  ```go
  type chanWriter struct { ch chan<- LogLine }
  func (w chanWriter) Write(p []byte) (int, error) {
      line := LogLine{Formatted: strings.TrimRight(string(p), "\n")}
      select {
      case w.ch <- line:
      default: // full — drop
      }
      return len(p), nil
  }
  ```
- Inside `NewTUIHandler`, construct the charmbracelet logger with color forced
  on regardless of TTY detection:
  ```go
  l := log.NewWithOptions(chanWriter{ch: ch}, log.Options{
      ReportTimestamp: true,
      TimeFormat:      "15:04:05",
      Level:           log.WarnLevel,
  })
  l.SetColorProfile(termenv.TrueColor)
  return filteredHandler{min: minLevel, inner: l}
  ```
  `*log.Logger` satisfies `slog.Handler` directly.
- Generalize `multiHandler` in `internal/logging/logger.go:135` from two fixed
  fields to a variadic slice:
  ```go
  type multiHandler struct { handlers []slog.Handler }

  func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
      for _, inner := range h.handlers {
          if inner.Enabled(ctx, level) { return true }
      }
      return false
  }
  func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
      for _, inner := range h.handlers {
          if err := inner.Handle(ctx, r); err != nil { return err }
      }
      return nil
  }
  // WithAttrs and WithGroup return a new multiHandler with each inner
  // handler transformed.
  ```
  Update the existing call at `logger.go:90` to construct via
  `&multiHandler{handlers: []slog.Handler{jsonHandler, textHandler}}`.
- Add a helper in the same file:
  ```go
  // WithHandler returns a slog.Handler that fans out records to base and extra.
  func WithHandler(base, extra slog.Handler) slog.Handler {
      return &multiHandler{handlers: []slog.Handler{base, extra}}
  }
  ```
  This is the public seam `internal/cmd/run.go` will use to compose the
  TUI handler with each factory-produced logger.
- Add `github.com/charmbracelet/log` as a dependency (run `go get` + `go mod tidy`).
  The `github.com/muesli/termenv` transitive already exists via `lipgloss`;
  import it explicitly for `termenv.TrueColor`.
- The `LogLine` type lives in `internal/logging` (not `internal/tui`) because
  `foundation` must_not_depend_on `presentation` per `docs/architecture.json`.
  The TUI message type that wraps it is defined in Issue 2.
- Do NOT change any existing `NewLogger`/`NewLoggerFileOnly`/`NewFileLogger`
  signatures. Existing callers in `internal/cmd/run.go`, `watch.go`,
  `implement.go` must keep compiling unchanged.

### Acceptance criteria

- [ ] `internal/logging/tui_handler.go` exists and exports `LogLine` and
      `NewTUIHandler(ch chan<- LogLine, minLevel slog.Level) slog.Handler`
- [ ] `NewTUIHandler` returns nil-safe output: a channel-full write does not
      block or return an error
- [ ] Records below `minLevel` are filtered at `Enabled()` — attribute
      closures are not invoked for filtered records
- [ ] `multiHandler` in `logger.go` takes a `[]slog.Handler` slice; the
      existing `NewLogger` call site still produces a handler that writes
      to both JSON file and stdout
- [ ] `logging.WithHandler(base, extra)` composes two handlers into a
      `multiHandler` without changing the base's behavior
- [ ] `go build ./...` passes
- [ ] `go test ./internal/logging/...` passes (existing tests unchanged)

### Test cases

- **Warn record delivered**: Call `Handle` with a `slog.Record` at
  `LevelWarn` — drain the channel and verify a `LogLine` arrives with
  `Formatted` non-empty and containing the record message
- **Info record filtered**: Call `Enabled` for `LevelInfo` with
  `minLevel=LevelWarn` — returns false; `Handle` at `LevelInfo` produces
  nothing on the channel
- **Channel full drops**: Fill a channel of cap 1 with a single record,
  then send a second — second send returns without blocking and without
  error; channel still contains only the first line
- **multiHandler fans out**: Build a `multiHandler` wrapping two in-memory
  handlers, call `Handle` once — both receive the record
- **multiHandler Enabled short-circuits**: Build a `multiHandler` where one
  inner handler is disabled — `Enabled` still returns true when the other
  is enabled
- **Formatted line contains level**: Drain a `LogLine` from a Warn record
  — `Formatted` contains the substring `WARN` (charmbracelet's level label)

---

## Issue 2: Viewport log panel in TUI model

**Blocked by**: #1

### Description

Add a scrollable log panel to the Bubble Tea TUI that renders lines arriving
on a channel of `logging.LogLine`. The panel uses `bubbles/viewport` for
scrollback, keeps a 100-line ring buffer, sticks to the bottom when the
user has not scrolled up, and sits between the existing detail panel and
the summary divider in `View()`. The TUI receives lines via a `tea.Cmd`
subscription that reads the channel and emits `LogMsg`; this is the
idiomatic bubbletea pattern for bridging an external event source into
the update loop.

This issue modifies 2 files in `internal/tui/` (`model.go` 404 lines,
`messages.go` 105 lines), both in the `presentation` layer. The import
`internal/logging` is legal because `presentation` may_depend_on
`foundation`. The main complexity is the stick-to-bottom autoscroll
heuristic and laying out the panel so it doesn't crush the table when the
terminal is short.

### Key constraints

- Add to `internal/tui/messages.go` (after existing message types):
  ```go
  import "github.com/peter-stratton/dark-factory/internal/logging"

  // LogMsg is emitted by the log-subscription tea.Cmd when a new log line
  // arrives on the channel.
  type LogMsg struct { Line logging.LogLine }
  ```
- Update the `Model` struct in `internal/tui/model.go:46` to add (grouped
  after the detail-panel fields):
  ```go
  logView viewport.Model
  logs    []string       // ring buffer, cap 100
  logCh   <-chan logging.LogLine
  ```
- Update `New(...)` in `internal/tui/model.go:252` to accept `logCh
  <-chan logging.LogLine` as a trailing parameter. When `logCh` is nil
  (test callers), the subscription is disabled. Initialize `logView`
  with zero width/height — real dimensions arrive via `WindowSizeMsg`:
  ```go
  vp := viewport.New(0, 0)
  m.logView = vp
  m.logCh = logCh
  ```
- Add a subscription helper at the top of `model.go` next to
  `countdownTick`:
  ```go
  // waitForLog blocks on ch and emits a LogMsg when a line arrives.
  // Returns nil when ch is nil or closed, ending the subscription.
  func waitForLog(ch <-chan logging.LogLine) tea.Cmd {
      if ch == nil { return nil }
      return func() tea.Msg {
          line, ok := <-ch
          if !ok { return nil }
          return LogMsg{Line: line}
      }
  }
  ```
- Update `Init()` in `model.go:89` to return
  `tea.Batch(m.spinner.Tick, waitForLog(m.logCh))` when `logCh` is
  non-nil; otherwise keep returning just `m.spinner.Tick`.
- Add a `LogMsg` handler to the `Update` switch (near the other progress
  message handlers):
  ```go
  case LogMsg:
      m.logs = append(m.logs, msg.Line.Formatted)
      const maxLogs = 100
      if len(m.logs) > maxLogs {
          m.logs = m.logs[len(m.logs)-maxLogs:]
      }
      atBottom := m.logView.AtBottom()
      m.logView.SetContent(strings.Join(m.logs, "\n"))
      if atBottom { m.logView.GotoBottom() }
      return m, waitForLog(m.logCh)
  ```
  The `atBottom` check preserves scrollback when the user has paged up
  to read an older warning.
- Update the `tea.WindowSizeMsg` handler at `model.go:124` to size the
  log viewport:
  ```go
  case tea.WindowSizeMsg:
      m.width = msg.Width
      m.height = msg.Height
      logHeight := 6
      if msg.Height < 20 { logHeight = 3 }
      m.logView.Width = msg.Width
      m.logView.Height = logHeight
  ```
  Fixed 6-line panel in normal terminals, 3 lines in small terminals.
- In `handleKey` at `model.go:275`, forward viewport scroll keys before
  the existing key switch runs (so `pgup`/`pgdown`/`home`/`end`/`j`/`k`
  do not collide with `q`/`esc`/`ctrl+c`):
  ```go
  switch msg.String() {
  case "pgup", "pgdown", "home", "end", "j", "k":
      var cmd tea.Cmd
      m.logView, cmd = m.logView.Update(msg)
      return m, cmd
  }
  ```
  Apply this branch *before* the existing `switch msg.String()`.
- Add a `renderLogPanel` helper below `renderDetailPanel`
  (`model.go:223`) that returns empty string when `len(m.logs) == 0` and
  otherwise returns the panel with a short header:
  ```go
  func renderLogPanel(view viewport.Model, count, width int) string {
      if count == 0 { return "" }
      header := dividerStyle.Render(strings.Repeat("─", width/2)) + " log"
      return header + "\n" + view.View()
  }
  ```
- In `View()` at `model.go:166`, insert the log panel between `detail` and
  the closing `divider`:
  ```go
  logs := renderLogPanel(m.logView, len(m.logs), divWidth)
  ```
  Extend the final assembly (the four branches of the `if table == ""` /
  `if detail == ""` tree) to append `logs` when non-empty, with `\n\n`
  separation.
- Test helper `tui.NewForTest(...)` or similar is NOT needed — callers
  pass `nil` for `logCh` to disable the subscription.
- Do not add `q`/`esc`/`ctrl+c` to the viewport-forward branch — those
  must keep their existing lifecycle-control semantics.

### Acceptance criteria

- [ ] `internal/tui/messages.go` exports `LogMsg` wrapping `logging.LogLine`
- [ ] `Model` has `logView viewport.Model`, `logs []string`, and
      `logCh <-chan logging.LogLine` fields
- [ ] `New(...)` accepts a trailing `logCh <-chan logging.LogLine` and
      treats nil as "subscription disabled"
- [ ] `Init()` returns both the spinner tick and `waitForLog(m.logCh)`
      when `logCh` is non-nil
- [ ] `LogMsg` appends to `m.logs`, truncates to cap 100, updates
      viewport content, auto-scrolls only when already at bottom, and
      re-issues the subscription
- [ ] `tea.WindowSizeMsg` sizes the log viewport width to the terminal
      width and height to 6 (or 3 on terminals shorter than 20 lines)
- [ ] `pgup`/`pgdown`/`home`/`end`/`j`/`k` scroll the log viewport
      without triggering existing key handlers
- [ ] `View()` renders the log panel between the detail panel and the
      closing divider; renders nothing when `len(m.logs) == 0`
- [ ] `go build ./...` passes
- [ ] `go test ./internal/tui/...` passes

### Test cases

- **LogMsg appends to ring**: Send `LogMsg{Line: LogLine{Formatted: "x"}}`
  to a fresh model — `m.logs` contains `"x"`
- **Ring buffer truncates**: Send 150 `LogMsg`s — `len(m.logs) == 100` and
  `m.logs[0]` matches the 51st line sent
- **Panel hidden when empty**: Call `View()` on a model with zero logs —
  returned string does not contain the log header
- **Panel rendered when non-empty**: Send a `LogMsg`, call `View()` — output
  contains the log header `"log"` and the formatted line
- **Stick to bottom**: With the viewport at bottom, send a new log —
  `m.logView.AtBottom()` is true after Update
- **Preserve scroll**: Scroll the viewport up by 2 lines, send a new log —
  `AtBottom()` returns false; viewport position is unchanged
- **Nil channel disables subscription**: Call `Init()` on a model built
  with `logCh=nil` — returned command is just the spinner tick, no
  `waitForLog`

---

## Issue 3: Wire log channel through run command

**Blocked by**: #2

### Description

Wire the TUI log channel end-to-end in `internal/cmd/run.go`. In the
`useTUI` branch, create a buffered `chan logging.LogLine` (cap 256),
build a `logging.NewTUIHandler` targeting it, and wrap the existing
`logFactory` in a closure so every logger the factory produces fans out
to both the original handler (JSON to debug.log) AND the TUI handler.
This is required because `orchestrator.Run` calls `logFactory` at
`internal/orchestrator/orchestrator.go:125` to replace the bootstrap
logger with a run-directory-scoped one — if we wrap only the top-level
logger, auth warnings from `internal/sandbox/auth.go:52` never reach the
TUI. Pass the channel into `tui.New(...)`, and close it after the
orchestrator goroutine returns so the subscription loop terminates
cleanly.

This issue modifies 1 file (`internal/cmd/run.go`, 309 lines) in the
`cmd` layer. The non-TUI code path is untouched, and `watch.go` /
`implement.go` are explicitly out of scope — their TUI-mode paths can be
wired in a follow-up phase once the pattern is validated in `run`.

### Key constraints

- Between `internal/cmd/run.go:78` (after the existing
  `if useTUI { logFactory = logging.NewLoggerFileOnly }`) and
  `internal/cmd/run.go:88` (the first `logFactory(logDir)` call), add:
  ```go
  var logCh chan logging.LogLine
  if useTUI {
      logCh = make(chan logging.LogLine, 256)
      tuiHandler := logging.NewTUIHandler(logCh, slog.LevelWarn)
      origFactory := logFactory
      logFactory = func(dir string) (*slog.Logger, error) {
          l, err := origFactory(dir)
          if err != nil { return nil, err }
          return slog.New(logging.WithHandler(l.Handler(), tuiHandler)), nil
      }
  }
  ```
- Pass `logCh` to `tui.New(...)` at `internal/cmd/run.go:119` as a
  trailing argument (the signature change is already specified in Issue 2).
- Close `logCh` in the orchestrator goroutine at
  `internal/cmd/run.go:125-138` after `orchestrator.Run` returns and
  after the (optional) `runEnterWatch` call, so the subscription loop
  in the TUI returns `nil` from `waitForLog` and no further `LogMsg`s
  are scheduled. Place the close after the existing
  `program.Send(tui.RunDoneMsg{})` calls:
  ```go
  if logCh != nil { close(logCh) }
  ```
  Both the early-return path (orchestrator error or `!watchFlag`) and
  the watch-exit path must close the channel.
- The non-TUI branch at `internal/cmd/run.go:144` is unchanged — no
  channel, no wrap, no pass-through.
- Do NOT modify `internal/cmd/watch.go` or `internal/cmd/implement.go`.
  Their `logFactory` declarations remain untouched; this phase scopes
  the wiring to `run` only.
- The channel capacity of 256 and the warn-level filter are set once in
  `run.go` and are not configurable via CLI flags in this phase.
- The `logger` variable at `internal/cmd/run.go:88` is created from the
  wrapped factory, so preflight warnings from
  `CheckWorkingTree`/`EnsureBaseBranch` (`run.go:99-104`) also land in
  the channel buffer and drain into the TUI once `program.Run()` starts.

### Acceptance criteria

- [ ] In the TUI branch, `logCh = make(chan logging.LogLine, 256)` exists
      and the original `logFactory` is wrapped in a closure that composes
      each produced logger's handler with `NewTUIHandler`
- [ ] `tui.New(...)` at `run.go:119` receives `logCh` as its trailing argument
- [ ] The orchestrator goroutine closes `logCh` after run (and after watch,
      when `watchFlag` is set) on every exit path
- [ ] The non-TUI branch at `run.go:144` is unchanged (no channel, no wrap)
- [ ] Preflight logs from `CheckWorkingTree`/`EnsureBaseBranch` are captured
      by the TUI handler (wrapped factory is used for the bootstrap logger)
- [ ] `go build ./...` passes
- [ ] `go test ./internal/cmd/...` passes

### Test cases

- **Wrapped logger fans out**: Build a logger via the wrapped factory, log
  a warn record — the underlying test channel receives a `LogLine` AND
  the JSON debug.log contains the record
- **Non-TUI path unaffected**: Build a logger via the original factory
  when `useTUI=false` — no channel reference in the closure; logger
  behaves identically to the pre-change implementation
- **Channel closed on orchestrator exit**: Run the TUI goroutine path with
  a stub orchestrator that returns immediately — `logCh` is closed; a
  subsequent receive returns `ok=false`
- **Channel closed after watch exit**: Run the TUI goroutine with
  `watchFlag=true` and a stub `runEnterWatch` that returns — `logCh` is
  closed after watch returns, not before

---

## Integration chain audit

```
logging.LogLine defined in Issue 1 (internal/logging/tui_handler.go)
  -> imported by internal/tui/messages.go in Issue 2 (LogMsg wraps LogLine)
  -> imported by internal/cmd/run.go in Issue 3 (channel type)

logging.NewTUIHandler defined in Issue 1
  -> called by run.go closure in Issue 3 to build the tuiHandler
  -> wrapped with base handler via logging.WithHandler (defined in Issue 1)

logging.WithHandler defined in Issue 1
  -> called inside the wrapped logFactory closure in Issue 3

multiHandler generalized to variadic in Issue 1
  -> existing NewLogger call site in Issue 1 keeps working (two-handler case)
  -> WithHandler uses it for the Issue 3 two-handler composition

logFactory wrapped in run.go in Issue 3
  -> produces bootstrap logger at run.go:88 (preflight warnings)
  -> passed to orchestrator.Run at run.go:126 (existing call)
    -> called again inside orchestrator.go:125 to build run-directory logger
      (each per-run logger automatically fans out to logCh via closure)
  -> auth warnings from sandbox/auth.go:52 reach the run-directory logger
    -> run-directory logger has tuiHandler in its multiHandler
      -> tuiHandler formats + sends to logCh

logCh created in run.go in Issue 3
  -> passed to NewTUIHandler in Issue 3 (send side)
  -> passed to tui.New(...) in Issue 3 (receive side)
    -> stored on Model.logCh in Issue 2
      -> Init() returns waitForLog(logCh) tea.Cmd in Issue 2
        -> Cmd reads channel, emits LogMsg
          -> Update handler appends to ring, renders via viewport
  -> closed by goroutine after orchestrator exit in Issue 3
    -> waitForLog returns nil, subscription loop ends

viewport.Model (charmbracelet/bubbles/viewport) used in Issue 2
  -> bubbles v1.0.0 already in go.mod, no new dep needed
  -> sized in WindowSizeMsg handler in Issue 2
  -> scrolled via key forwarding in handleKey in Issue 2
  -> rendered via renderLogPanel helper in Issue 2

charmbracelet/log added as dep in Issue 1
  -> imported only by internal/logging/tui_handler.go
  -> used to format records with colored level labels
  -> writes to chanWriter, which sends LogLine onto logCh
```

All hops covered. No gaps.
