package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/peter-stratton/dark-factory/internal/logging"
)

// countdownTick returns a Cmd that fires a CountdownTickMsg after one second.
func countdownTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return CountdownTickMsg{}
	})
}

// waitForLog returns a Cmd that blocks on ch and emits a LogMsg when a line
// arrives. Returns nil when ch is nil so callers without a log channel skip
// the subscription entirely. When ch closes, the inner func returns nil which
// bubbletea treats as no message, ending the subscription cleanly.
func waitForLog(ch <-chan logging.LogLine) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return LogMsg{Line: line}
	}
}

// maxLogLines bounds the in-memory log ring buffer.
const maxLogLines = 100

// autoMerge holds the feature and rollup branch names used for auto-merge.
// A nil pointer means auto-merge is not configured.
type autoMerge struct {
	feature string
	rollup  string
}

// detailKind distinguishes the two detail panel entry types.
type detailKind int

const (
	detailKindError detailKind = iota
	detailKindJudge
)

// detailEntry holds a single message shown in the detail panel below the table.
type detailEntry struct {
	issueNumber int
	kind        detailKind
	message     string
}

// Model is the root Bubble Tea model for the dark-factory TUI.
//
// It holds run metadata displayed in the header chrome, aggregate issue counts
// displayed in the summary bar, an issue table with per-row state, and
// terminal dimensions for layout calculations.
type Model struct {
	// Run metadata — populated via Update messages.
	repo       string
	milestone  string
	timestamp  string
	baseBranch string
	autoMerge  *autoMerge

	// Aggregate counts — updated as issues progress.
	merged    int
	inReview  int
	queued    int
	failed        int
	totalCost     float64
	activeWorkers int
	totalWorkers  int

	// Issue table state.
	issues         []issueRow
	issueIndex     map[int]int   // issue number → index in issues slice
	detailMessages []detailEntry // last 5 error/judge messages shown in the detail panel

	// Log panel state.
	logView viewport.Model
	logs    []string                // ring buffer, capped at maxLogLines
	logCh   <-chan logging.LogLine  // nil disables the subscription

	// Spinner for in-progress rows.
	spinner spinner.Model

	// Run lifecycle.
	done              bool      // true after the orchestrator finishes
	watching          bool      // true while in watch mode (between run and done)
	cancelling        bool      // true after user requests cancellation
	cancelFn          func()    // cancels the orchestrator context
	rateLimited       bool      // true while holding for a Claude usage limit reset
	rateLimitResetsAt time.Time // when the current usage limit hold will end

	// Terminal dimensions.
	width  int
	height int
}

// Compile-time assertion that Model implements tea.Model.
var _ tea.Model = Model{}

// Init implements tea.Model. Returns the spinner tick command so animation
// starts immediately, batched with the log subscription when a channel is
// configured.
func (m Model) Init() tea.Cmd {
	if m.logCh == nil {
		return m.spinner.Tick
	}
	return tea.Batch(m.spinner.Tick, waitForLog(m.logCh))
}

// Update implements tea.Model. Handles window-size messages, spinner ticks,
// and all progress message types.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case WatchingMsg:
		m.watching = true
		return m, nil

	case RunDoneMsg:
		m.done = true
		return m, nil

	case RateLimitedMsg:
		m.rateLimited = true
		m.rateLimitResetsAt = msg.ResetsAt
		return m, countdownTick()

	case RateLimitClearedMsg:
		m.rateLimited = false
		m.rateLimitResetsAt = time.Time{}
		return m, nil

	case CountdownTickMsg:
		if m.rateLimited {
			return m, countdownTick()
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		logHeight := 6
		if msg.Height < 20 {
			logHeight = 3
		}
		m.logView.Width = msg.Width
		m.logView.Height = logHeight

	case LogMsg:
		m.logs = append(m.logs, msg.Line.Formatted)
		if len(m.logs) > maxLogLines {
			m.logs = m.logs[len(m.logs)-maxLogLines:]
		}
		atBottom := m.logView.AtBottom()
		m.logView.SetContent(strings.Join(m.logs, "\n"))
		if atBottom {
			m.logView.GotoBottom()
		}
		return m, waitForLog(m.logCh)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case IssueStartedMsg:
		m.handleIssueStarted(msg)

	case IssueStageChangedMsg:
		m.handleIssueStageChanged(msg)

	case JudgeInterventionMsg:
		m.handleJudgeIntervention(msg)

	case IssueCompletedMsg:
		m.handleIssueCompleted(msg)

	case RunStartedMsg:
		m.handleRunStarted(msg)

	case WorkersActiveMsg:
		m.activeWorkers = msg.Active
		m.totalWorkers = msg.Total

	case WaveStartedMsg:
		// Wave metadata is informational; no per-row state changes here.

	case RunFinishedMsg:
		m.merged = msg.Implemented
		m.inReview = msg.ReadyToMerge + msg.NeedsHumanReview
		m.failed = msg.Failed
		m.queued = msg.Blocked
	}

	return m, nil
}

// View implements tea.Model. Composes the header, issue table, detail panel, and summary bar.
func (m Model) View() string {
	header := renderHeader(m)
	var rateLimitedUntil *time.Time
	if m.rateLimited && !m.rateLimitResetsAt.IsZero() {
		t := m.rateLimitResetsAt
		rateLimitedUntil = &t
	}
	table := renderTable(m.issues, m.spinner, m.width, rateLimitedUntil)
	summary := renderSummary(m)

	// Horizontal divider between content and summary bar.
	divWidth := m.width
	if divWidth <= 0 {
		divWidth = 40
	}
	divider := dividerStyle.Render(strings.Repeat("─", divWidth/2))
	detail := renderDetailPanel(m.detailMessages, divWidth)
	logs := renderLogPanel(m.logView, len(m.logs), divWidth)

	var hint string
	if m.done {
		hint = "\n\n" + headerLabelStyle.Render("press q to exit")
	} else if m.cancelling {
		hint = "\n\n" + summaryFailedStyle.Render("cancelling... waiting for current issue to finish")
	} else if m.rateLimited {
		var holdMsg string
		if !m.rateLimitResetsAt.IsZero() {
			holdMsg = fmt.Sprintf("rate limited — holding until %s · press ctrl+c to cancel",
				m.rateLimitResetsAt.Local().Format("15:04:05"))
		} else {
			holdMsg = "rate limited — holding for reset · press ctrl+c to cancel"
		}
		hint = "\n\n" + badgeRateLimitedStyle.Render("RATE LIMITED") + " " + headerLabelStyle.Render(holdMsg)
	} else if m.watching {
		hint = "\n\n" + headerLabelStyle.Render("watching for merges · press ctrl+c to cancel")
	} else {
		hint = "\n\n" + headerLabelStyle.Render("press ctrl+c to cancel")
	}

	logsSegment := ""
	if logs != "" {
		logsSegment = logs + "\n\n"
	}

	if table == "" {
		if detail == "" {
			return header + "\n\n" + logsSegment + divider + "\n\n" + summary + hint + "\n"
		}
		// detail already opens with a divider; this divider closes it.
		return header + "\n\n" + detail + "\n\n" + logsSegment + divider + "\n\n" + summary + hint + "\n"
	}
	if detail == "" {
		return header + "\n\n" + table + "\n\n" + logsSegment + divider + "\n\n" + summary + hint + "\n"
	}
	// detail already opens with a divider; this divider closes it.
	return header + "\n\n" + table + "\n\n" + detail + "\n\n" + logsSegment + divider + "\n\n" + summary + hint + "\n"
}

// renderDetailPanel renders the last N error and judge messages with an
// opening divider, above the summary bar. Returns "" when there are no
// entries so the caller can omit the block entirely (no empty gap).
// The closing divider is provided by View() so the panel uses exactly two
// dividers total when non-empty.
func renderDetailPanel(entries []detailEntry, width int) string {
	if len(entries) == 0 {
		return ""
	}
	divider := dividerStyle.Render(strings.Repeat("─", width/2))
	lines := []string{divider}
	for _, e := range entries {
		num := rowNumberStyle.Render(fmt.Sprintf("#%d", e.issueNumber))
		if e.kind == detailKindJudge {
			marker := markerJudgeStyle.Render(markerJudge)
			label := detailLabelStyle.Render("judge:")
			msg := detailJudgeStyle.Render(e.message)
			lines = append(lines, marker+" "+num+" "+label+" "+msg)
		} else {
			marker := markerFailedStyle.Render(markerFailed)
			label := detailLabelStyle.Render("error:")
			msg := detailErrStyle.Render(e.message)
			lines = append(lines, marker+" "+num+" "+label+" "+msg)
		}
	}
	return strings.Join(lines, "\n")
}

// renderLogPanel renders the log viewport with a short divider+label header.
// Returns "" when count is zero so the caller can omit the block entirely
// (matches renderDetailPanel's convention so View()'s assembly stays simple).
func renderLogPanel(view viewport.Model, count, width int) string {
	if count == 0 {
		return ""
	}
	header := dividerStyle.Render(strings.Repeat("─", width/2)) + " log"
	return header + "\n" + view.View()
}

// New returns a Model pre-populated with run metadata.
//
// cancelFn is called when the user presses ctrl+c during an active run to
// cancel the orchestrator's context. It may be nil (cancellation disabled).
// mergeFeature and mergeRollup may be empty strings when auto-merge is not
// configured. baseBranch may be empty when using the repository default.
//
// logCh feeds log lines into the scrollable log panel. A nil channel disables
// the subscription (the panel stays hidden because no LogMsg is ever appended).
func New(repo, milestone, timestamp, baseBranch, mergeFeature, mergeRollup string, cancelFn func(), logCh <-chan logging.LogLine) Model {
	spin := spinner.New()
	spin.Spinner = spinner.Dot

	m := Model{
		repo:       repo,
		milestone:  milestone,
		timestamp:  timestamp,
		baseBranch: baseBranch,
		cancelFn:   cancelFn,
		spinner:    spin,
		logView:    viewport.New(0, 0),
		logCh:      logCh,
	}
	if mergeFeature != "" || mergeRollup != "" {
		m.autoMerge = &autoMerge{
			feature: mergeFeature,
			rollup:  mergeRollup,
		}
	}
	return m
}

// handleKey processes key presses. During an active run, ctrl+c cancels the
// orchestrator. Once done, q/esc/ctrl+c exit the TUI. Scroll keys are
// forwarded to the log viewport before the lifecycle switch so they do not
// collide with the q/esc/ctrl+c handlers.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "j", "k":
		var cmd tea.Cmd
		m.logView, cmd = m.logView.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		if m.done {
			return m, tea.Quit
		}
		if !m.cancelling && m.cancelFn != nil {
			m.cancelling = true
			m.cancelFn()
		}
	case "q", "esc":
		if m.done {
			return m, tea.Quit
		}
	}
	return m, nil
}

// handleIssueStageChanged updates the stage for an in-progress issue.
func (m *Model) handleIssueStageChanged(msg IssueStageChangedMsg) {
	if idx, ok := m.issueIndex[msg.Number]; ok {
		m.issues[idx].stage = msg.Stage
	}
}

// handleIssueStarted adds a new issue row or skips if already pre-populated.
// Sets the stage to "starting" so the row shows a spinner immediately,
// keeping the visual state in sync with the queued count.
func (m *Model) handleIssueStarted(msg IssueStartedMsg) {
	if m.issueIndex == nil {
		m.issueIndex = make(map[int]int)
	}
	if _, exists := m.issueIndex[msg.Number]; !exists {
		m.issueIndex[msg.Number] = len(m.issues)
		m.issues = append(m.issues, issueRow{
			number: msg.Number,
			title:  msg.Title,
		})
	}
	if idx, ok := m.issueIndex[msg.Number]; ok {
		m.issues[idx].stage = "starting"
	}
	if m.queued > 0 {
		m.queued--
	}
}

// handleJudgeIntervention records a judge decision on the issue row and detail panel.
func (m *Model) handleJudgeIntervention(msg JudgeInterventionMsg) {
	idx, ok := m.issueIndex[msg.IssueNumber]
	if !ok {
		return
	}
	reason := strings.ToTitle(msg.Judgment) + ": " + msg.Rule
	if msg.Step != "" {
		reason += " (" + msg.Step + ")"
	}
	m.issues[idx].judgeReason = reason
	m.detailMessages = append(m.detailMessages, detailEntry{
		issueNumber: msg.IssueNumber,
		kind:        detailKindJudge,
		message:     reason,
	})
	const maxDetail = 5
	if len(m.detailMessages) > maxDetail {
		tail := m.detailMessages[len(m.detailMessages)-maxDetail:]
		m.detailMessages = append(make([]detailEntry, 0, maxDetail), tail...)
	}
}

// handleIssueCompleted updates the issue row and increments summary counts.
func (m *Model) handleIssueCompleted(msg IssueCompletedMsg) {
	idx, ok := m.issueIndex[msg.Number]
	if !ok {
		return
	}
	m.issues[idx].status = msg.Status
	m.issues[idx].prNumber = msg.PRNumber
	m.issues[idx].retries = msg.Retries
	m.issues[idx].errMsg = msg.ErrMsg
	m.issues[idx].traceID = msg.TraceID
	if msg.ErrMsg != "" {
		m.detailMessages = append(m.detailMessages, detailEntry{
			issueNumber: msg.Number,
			kind:        detailKindError,
			message:     msg.ErrMsg,
		})
		const maxDetail = 5
		if len(m.detailMessages) > maxDetail {
			tail := m.detailMessages[len(m.detailMessages)-maxDetail:]
			m.detailMessages = append(make([]detailEntry, 0, maxDetail), tail...)
		}
	}
	switch msg.Status {
	case "implemented":
		m.merged++
	case "ready-to-merge", "needs-human-review":
		m.inReview++
	case "failed":
		m.failed++
	}
	m.totalCost += msg.CostUSD
}

// handleRunStarted populates header metadata and pre-fills the issue table.
func (m *Model) handleRunStarted(msg RunStartedMsg) {
	m.repo = msg.Repo
	m.milestone = msg.Milestone
	m.timestamp = msg.Timestamp
	m.baseBranch = msg.BaseBranch
	if msg.MergeFeature != "" || msg.MergeRollup != "" {
		m.autoMerge = &autoMerge{
			feature: msg.MergeFeature,
			rollup:  msg.MergeRollup,
		}
	}
	if m.issueIndex == nil {
		m.issueIndex = make(map[int]int)
	}
	for _, iss := range msg.Issues {
		if _, exists := m.issueIndex[iss.Number]; !exists {
			m.issueIndex[iss.Number] = len(m.issues)
			m.issues = append(m.issues, issueRow{
				number: iss.Number,
				title:  iss.Title,
			})
		}
	}
	m.queued = len(m.issues)
}
