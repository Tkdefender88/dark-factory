## Phase 39: TUI Log Viewer

**Goal**: When the TUI is running, warn- and error-level log records appear
live in a scrollable panel so users can see why a run is stalled (missing API
keys, auth failures, etc.) without exiting to tail `debug.log`.

**Milestone**: `Phase 39: TUI Log Viewer` | **Label**: `phase-39`

- slog handler for TUI channel — add `logging.NewTUIHandler` backed by charmbracelet/log, non-blocking-sends formatted lines into a buffered channel, level-filters to warn+error; generalize `multiHandler` to variadic handlers; add `charmbracelet/log` dependency
- Viewport log panel in TUI model — add `LogLine`/`LogMsg` types, viewport field, ring buffer (cap 100) and `waitForLog` subscription Cmd in `internal/tui/model.go`; render panel between detail panel and summary divider; stick-to-bottom autoscroll unless user scrolled up; pgup/pgdn/home/end scroll bindings
- Wire log channel through run command — in `internal/cmd/run.go`, wrap `logFactory` in the TUI branch so every logger it produces (including the run-directory replacement at `orchestrator.go:125`) fans out to the channel; pass `logCh` into `tui.New`; close channel on orchestrator exit

**Issues**: #1-#3 (on `Tkdefender88/dark-factory` fork, pending upstream PR)

**Planning doc**: `docs/planning/phase-39-tui-log-viewer.md`
