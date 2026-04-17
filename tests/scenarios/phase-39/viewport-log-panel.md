# Scenario: Viewport log panel in TUI model

Relates to: Issue #2

## Setup
- Test runs in `internal/tui` package using Go's `testing` package
- `charmbracelet/bubbletea` and `charmbracelet/bubbles/viewport` imported
- `logging.LogLine` type available from `internal/logging`
- A `chan logging.LogLine` constructed per-test and passed into `tui.New(...)` as its trailing argument
- Test helper constructs a `Model` with fixed width/height by first dispatching a `tea.WindowSizeMsg{Width: 120, Height: 40}` through `Update`

## Cases

### LogMsg appends to the ring buffer
- GIVEN a freshly constructed `Model` with an empty `logs` slice
- WHEN a `LogMsg{Line: logging.LogLine{Formatted: "WARN missing key"}}` is dispatched through `Update`
- THEN `m.logs` has length 1 and `m.logs[0]` equals `"WARN missing key"`

### Ring buffer truncates at 100 entries
- GIVEN a freshly constructed `Model`
- WHEN 150 successive `LogMsg`s are dispatched, each with `Formatted` set to its 1-indexed sequence number (e.g., `"1"`, `"2"`, ..., `"150"`)
- THEN `len(m.logs) == 100` AND `m.logs[0] == "51"` AND `m.logs[99] == "150"`

### Panel is hidden when logs are empty
- GIVEN a `Model` with `len(m.logs) == 0` after a `WindowSizeMsg`
- WHEN `View()` is called
- THEN the returned string does not contain the substring `" log"` (the log panel header label) AND the panel's viewport content is not rendered

### Panel is rendered when at least one log is present
- GIVEN a `Model` that has received one `LogMsg{Line: LogLine{Formatted: "WARN auth missing"}}`
- WHEN `View()` is called
- THEN the returned string contains both the log header label `" log"` AND the substring `"WARN auth missing"`

### Auto-scroll keeps panel at bottom when user has not scrolled up
- GIVEN a `Model` whose viewport is at the bottom (`m.logView.AtBottom()` returns true) after prior log lines
- WHEN a new `LogMsg` is dispatched
- THEN after `Update` returns, `m.logView.AtBottom()` returns true

### Scroll position is preserved when user has scrolled up
- GIVEN a `Model` whose viewport has been scrolled up 2 lines from the bottom via a `pgup` key event (so `AtBottom()` returns false)
- WHEN a new `LogMsg` is dispatched
- THEN after `Update` returns, `m.logView.AtBottom()` still returns false AND the viewport's `YOffset` is unchanged from before the `LogMsg`

### Subscription re-issues itself after every LogMsg
- GIVEN a `Model` constructed with a non-nil `logCh`
- WHEN a `LogMsg` is dispatched through `Update`
- THEN the returned `tea.Cmd` is non-nil (representing the next `waitForLog` subscription call) so the loop keeps draining

### Nil channel disables the subscription in Init
- GIVEN a `Model` constructed with `logCh = nil`
- WHEN `Init()` is called
- THEN the returned command batch does NOT contain a `waitForLog` subscription — it contains only the spinner tick command

### WindowSizeMsg sizes the viewport to 6 lines on normal terminals
- GIVEN a freshly constructed `Model`
- WHEN a `tea.WindowSizeMsg{Width: 120, Height: 40}` is dispatched
- THEN `m.logView.Width == 120` AND `m.logView.Height == 6`

### WindowSizeMsg shrinks viewport on small terminals
- GIVEN a freshly constructed `Model`
- WHEN a `tea.WindowSizeMsg{Width: 80, Height: 18}` is dispatched (height below the 20-line threshold)
- THEN `m.logView.Width == 80` AND `m.logView.Height == 3`

### Scroll keys forward to the log viewport
- GIVEN a `Model` with 50 log lines already present and a viewport sized to 6 lines
- WHEN a `tea.KeyMsg` for `"pgup"` is dispatched
- THEN the viewport's `YOffset` decreases by the viewport page size AND the existing ctrl+c/q/esc key handlers are not invoked (run is not cancelled, TUI does not quit)

### Lifecycle keys are not consumed by the viewport
- GIVEN a `Model` in `done` state with 50 log lines present
- WHEN a `tea.KeyMsg` for `"q"` is dispatched
- THEN the returned command is `tea.Quit` (existing behavior preserved, key was not forwarded to the viewport)

### LogMsg with empty Formatted still increments ring
- GIVEN a `Model` with an empty `logs` slice
- WHEN a `LogMsg{Line: LogLine{Formatted: ""}}` is dispatched
- THEN `len(m.logs) == 1` AND `m.logs[0] == ""` (no filtering at the model layer; the handler is responsible for filtering)
