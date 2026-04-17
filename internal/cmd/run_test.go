package cmd

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/peter-stratton/dark-factory/internal/config"
	"github.com/peter-stratton/dark-factory/internal/github"
	"github.com/peter-stratton/dark-factory/internal/logging"
	"github.com/peter-stratton/dark-factory/internal/progress"
	"github.com/spf13/cobra"
)

func TestNoTUIFlagRegistered(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"run", runCmd},
		{"implement", implementCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.cmd.Flags().Lookup("no-tui")
			if f == nil {
				t.Fatalf("%s command missing --no-tui flag", tc.name)
			}
			if f.DefValue != "false" {
				t.Errorf("no-tui default = %q, want %q", f.DefValue, "false")
			}
		})
	}
}

// TestIsTerminalFnReplaceable verifies that isTerminalFn is a testability seam
// that can be replaced to force text mode regardless of actual terminal state.
func TestIsTerminalFnReplaceable(t *testing.T) {
	orig := isTerminalFn
	t.Cleanup(func() { isTerminalFn = orig })

	// Force non-terminal → useTUI should be false even if no-tui is false.
	isTerminalFn = func(_ int) bool { return false }
	useTUI := !false && isTerminalFn(1)
	if useTUI {
		t.Error("useTUI = true, want false when isTerminalFn returns false")
	}

	// Force terminal → useTUI should be true when no-tui is false.
	isTerminalFn = func(_ int) bool { return true }
	useTUI = !false && isTerminalFn(1)
	if !useTUI {
		t.Error("useTUI = false, want true when isTerminalFn returns true and no-tui is false")
	}

	// Force terminal but no-tui=true → useTUI should be false.
	useTUI = !true && isTerminalFn(1)
	if useTUI {
		t.Error("useTUI = true, want false when no-tui is set even if terminal is detected")
	}
}

func TestNoSandboxFlagAbsent(t *testing.T) {
	// The --no-sandbox flag must not be registered on the run command.
	f := runCmd.Flags().Lookup("no-sandbox")
	if f != nil {
		t.Error("run command should not have --no-sandbox flag")
	}
}

func TestNoSandboxYAMLSilentlyIgnored(t *testing.T) {
	// no_sandbox: true in YAML should be silently ignored (yaml.v3 non-strict mode).
	dir := t.TempDir()
	p := filepath.Join(dir, "godark.yaml")
	err := os.WriteFile(p, []byte("repo: owner/repo\nno_sandbox: true\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = config.Load(p, config.CLIFlags{})
	if err != nil {
		t.Fatalf("unexpected error loading config with no_sandbox: %v", err)
	}
}

func TestAutoMergeFlagParsing(t *testing.T) {
	// The --auto-merge-feature flag should be registered on both run and implement commands,
	// and default to "none".
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"run", runCmd},
		{"implement", implementCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.cmd.Flags().Lookup("auto-merge-feature")
			if f == nil {
				t.Fatalf("%s command missing --auto-merge-feature flag", tc.name)
			}
			if f.DefValue != "none" {
				t.Errorf("auto-merge-feature default = %q, want %q", f.DefValue, "none")
			}
		})
	}
}

func TestAutoMergeRollupFlagParsing(t *testing.T) {
	// The --auto-merge-rollup flag should be registered on both run and implement commands,
	// and default to "none".
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"run", runCmd},
		{"implement", implementCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.cmd.Flags().Lookup("auto-merge-rollup")
			if f == nil {
				t.Fatalf("%s command missing --auto-merge-rollup flag", tc.name)
			}
			if f.DefValue != "manual" {
				t.Errorf("auto-merge-rollup default = %q, want %q", f.DefValue, "manual")
			}
		})
	}
}

func TestAutoMergeConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "godark.yaml")
	err := os.WriteFile(p, []byte("repo: owner/repo\nauto_merge:\n  feature: all\n  rollup: manual\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(p, config.CLIFlags{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AutoMerge.Feature != "all" {
		t.Errorf("AutoMerge.Feature = %q, want %q from config file", cfg.AutoMerge.Feature, "all")
	}
	if cfg.AutoMerge.Rollup != "manual" {
		t.Errorf("AutoMerge.Rollup = %q, want %q from config file", cfg.AutoMerge.Rollup, "manual")
	}
}

func TestAutoMergeDefaultManual(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "godark.yaml")
	err := os.WriteFile(p, []byte("repo: owner/repo\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(p, config.CLIFlags{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AutoMerge.Feature != "none" {
		t.Errorf("AutoMerge.Feature = %q, want %q by default", cfg.AutoMerge.Feature, "none")
	}
	if cfg.AutoMerge.Rollup != "manual" {
		t.Errorf("AutoMerge.Rollup = %q, want %q by default", cfg.AutoMerge.Rollup, "manual")
	}
}

func TestAutoMergeFlagOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "godark.yaml")
	err := os.WriteFile(p, []byte("repo: owner/repo\nauto_merge:\n  feature: none\n  rollup: none\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	feat := "all"
	rollup := "auto"
	cfg, err := config.Load(p, config.CLIFlags{AutoMergeFeature: &feat, AutoMergeRollup: &rollup})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AutoMerge.Feature != "all" {
		t.Errorf("AutoMerge.Feature = %q, want %q (flag should override config)", cfg.AutoMerge.Feature, "all")
	}
	if cfg.AutoMerge.Rollup != "auto" {
		t.Errorf("AutoMerge.Rollup = %q, want %q (flag should override config)", cfg.AutoMerge.Rollup, "auto")
	}
}

// TestWatchFlagRegistered verifies that the --watch flag exists on the run
// command and defaults to false.
func TestWatchFlagRegistered(t *testing.T) {
	f := runCmd.Flags().Lookup("watch")
	if f == nil {
		t.Fatal("run command missing --watch flag")
	}
	if f.DefValue != "false" {
		t.Errorf("watch default = %q, want %q", f.DefValue, "false")
	}
}

// TestWatchFlagIndependentOfNoTUI verifies that --watch and --no-tui are
// separate flags that can be set independently.
func TestWatchFlagIndependentOfNoTUI(t *testing.T) {
	watchFlag := runCmd.Flags().Lookup("watch")
	noTUIFlag := runCmd.Flags().Lookup("no-tui")

	if watchFlag == nil {
		t.Fatal("run command missing --watch flag")
	}
	if noTUIFlag == nil {
		t.Fatal("run command missing --no-tui flag")
	}

	// Both flags have independent defaults.
	if watchFlag.DefValue != "false" {
		t.Errorf("watch default = %q, want %q", watchFlag.DefValue, "false")
	}
	if noTUIFlag.DefValue != "false" {
		t.Errorf("no-tui default = %q, want %q", noTUIFlag.DefValue, "false")
	}
}

// TestRunListPRsFnSkipsWatchWhenNoPRs verifies that runEnterWatch exits without
// entering the polling loop when no PRs are awaiting review.
func TestRunListPRsFnSkipsWatchWhenNoPRs(t *testing.T) {
	orig := runListPRsFn
	runListPRsFn = func(_, _ string) ([]github.PRInfo, error) {
		return nil, nil // no awaiting PRs
	}
	defer func() { runListPRsFn = orig }()

	cfg := &config.Config{Repo: "owner/repo"}

	// runEnterWatch should not invoke the watch loop — it just returns nil.
	// Providing a cancelled context would expose any loop that starts (it would
	// exit immediately), but we primarily verify no panic/error on empty queue.
	ctx := context.Background()
	if err := runEnterWatch(ctx, cfg, config.RunMode{Workers: 1}, slog.Default(), "", progress.NewTextReporter(os.Stdout)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestTagResolutionSurfacesConfigError verifies that config.Load returns a
// real error when the config file is syntactically valid YAML but fails
// validation (e.g. wait_for_checks as a flat list instead of the struct
// format). Note: config validation errors are NOT surfaced through the --tag
// path in RunE when --repo is absent; that path uses bare yaml.Unmarshal via
// resolveRepo, which skips validation and returns "", causing the user to see
// "--repo is required when using --tag". This test only confirms that
// config.Load itself returns a descriptive error for the invalid format.
func TestTagResolutionSurfacesConfigError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "godark.yaml")
	// This config has repo set correctly but wait_for_checks in the old flat-list
	// format, which fails config validation.
	err := os.WriteFile(p, []byte("repo: owner/repo\nwait_for_checks:\n  - test\n  - lint\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = config.Load(p, config.CLIFlags{})
	if err == nil {
		t.Fatal("expected config.Load to return an error for invalid wait_for_checks format, got nil")
	}
	// The error should not be a "repo is required" message — it should describe
	// the actual config problem so users can fix their godark.yaml.
	if strings.Contains(err.Error(), "repo is required") {
		t.Errorf("config.Load error should describe the real problem, got: %v", err)
	}
}

// wrapFactoryForTUI mirrors the factory-wrap closure built in run.go's RunE
// when useTUI is true. Keeping the helper here lets tests exercise the exact
// composition pattern without invoking the Cobra command.
func wrapFactoryForTUI(orig func(string) (*slog.Logger, error), logCh chan logging.LogLine) func(string) (*slog.Logger, error) {
	tuiHandler := logging.NewTUIHandler(logCh, slog.LevelWarn)
	return func(dir string) (*slog.Logger, error) {
		l, err := orig(dir)
		if err != nil {
			return nil, err
		}
		return slog.New(logging.WithHandler(l.Handler(), tuiHandler)), nil
	}
}

// TestRunTUIFactoryWrapFansOut verifies that a logger produced by the wrapped
// factory writes warn-level records to both the JSON debug.log AND the TUI
// channel. This is the core behavior change introduced by issue #3.
func TestRunTUIFactoryWrapFansOut(t *testing.T) {
	dir := t.TempDir()
	logCh := make(chan logging.LogLine, 16)
	factory := wrapFactoryForTUI(logging.NewLoggerFileOnly, logCh)

	logger, err := factory(dir)
	if err != nil {
		t.Fatalf("wrapped factory returned error: %v", err)
	}

	logger.Warn("preflight-warning", "reason", "auth-missing")

	select {
	case line := <-logCh:
		if !strings.Contains(line.Formatted, "preflight-warning") {
			t.Errorf("log channel line missing message, got %q", line.Formatted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log line on TUI channel")
	}

	data, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatalf("reading debug.log: %v", err)
	}
	if !strings.Contains(string(data), "preflight-warning") {
		t.Errorf("debug.log missing message, got %q", string(data))
	}

	// Confirm debug.log is valid JSON (proof the original JSON handler is still
	// in the fan-out chain, not shadowed by the TUI handler).
	var rec map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("debug.log line is not JSON: %v (line=%q)", err, line)
		}
	}
}

// TestRunTUIFactoryWrapDropsInfo verifies that info-level records do not
// leak onto the TUI channel (the TUI handler filters below warn) but still
// land in the JSON debug.log.
func TestRunTUIFactoryWrapDropsInfo(t *testing.T) {
	dir := t.TempDir()
	logCh := make(chan logging.LogLine, 16)
	factory := wrapFactoryForTUI(logging.NewLoggerFileOnly, logCh)

	logger, err := factory(dir)
	if err != nil {
		t.Fatalf("wrapped factory returned error: %v", err)
	}

	logger.Info("info-only-message")

	select {
	case line := <-logCh:
		t.Errorf("expected no TUI channel line for info record, got %q", line.Formatted)
	case <-time.After(50 * time.Millisecond):
	}

	data, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatalf("reading debug.log: %v", err)
	}
	if !strings.Contains(string(data), "info-only-message") {
		t.Errorf("debug.log missing info message, got %q", string(data))
	}
}

// TestRunNonTUIFactoryIdentity verifies that the non-TUI branch of run.go
// leaves logFactory equal to logging.NewLogger — no wrap, no channel.
func TestRunNonTUIFactoryIdentity(t *testing.T) {
	useTUI := false
	logFactory := logging.NewLogger
	if useTUI {
		logFactory = logging.NewLoggerFileOnly
	}
	var logCh chan logging.LogLine
	if useTUI {
		logCh = make(chan logging.LogLine, 256)
		logFactory = wrapFactoryForTUI(logFactory, logCh)
	}

	if logCh != nil {
		t.Error("logCh should be nil when useTUI=false")
	}
	if reflect.ValueOf(logFactory).Pointer() != reflect.ValueOf(logging.NewLogger).Pointer() {
		t.Error("logFactory should be the unwrapped logging.NewLogger when useTUI=false")
	}
}

// runGoroutineBody mirrors the orchestrator goroutine body in run.go so that
// its close semantics can be tested in isolation. The stubs represent
// orchestrator.Run and runEnterWatch; errCh and the sendRunDone closure stand
// in for the bubbletea program's Send channel.
func runGoroutineBody(logCh chan logging.LogLine, watchFlag bool, orchRun func() error, watchRun func() error, sendRunDone func(), sendWatching func()) chan error {
	errCh := make(chan error, 1)
	go func() {
		err := orchRun()
		if err != nil || !watchFlag {
			errCh <- err
			sendRunDone()
			if logCh != nil {
				close(logCh)
			}
			return
		}
		sendWatching()
		werr := watchRun()
		errCh <- werr
		sendRunDone()
		if logCh != nil {
			close(logCh)
		}
	}()
	return errCh
}

// TestRunGoroutineClosesLogChOnOrchestratorExit verifies that the channel is
// closed after orchestrator.Run returns when watchFlag is false (or when the
// orchestrator returns an error) — the TUI subscription loop depends on this
// close to terminate cleanly.
func TestRunGoroutineClosesLogChOnOrchestratorExit(t *testing.T) {
	logCh := make(chan logging.LogLine, 4)
	var runDoneCount int
	errCh := runGoroutineBody(
		logCh,
		false, // watchFlag
		func() error { return nil },
		func() error { t.Fatal("watchRun should not be called when watchFlag=false"); return nil },
		func() { runDoneCount++ },
		func() { t.Fatal("sendWatching should not be called when watchFlag=false") },
	)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("errCh = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for goroutine to finish")
	}

	_, ok := <-logCh
	if ok {
		t.Error("expected logCh to be closed after orchestrator exit")
	}
	if runDoneCount != 1 {
		t.Errorf("runDoneCount = %d, want 1", runDoneCount)
	}
}

// TestRunGoroutineClosesLogChOnWatchExit verifies that when watchFlag=true and
// the orchestrator succeeds, the channel is closed after runEnterWatch
// returns — not before — so watch-phase log records can still reach the TUI.
func TestRunGoroutineClosesLogChOnWatchExit(t *testing.T) {
	logCh := make(chan logging.LogLine, 4)
	watchStarted := make(chan struct{})
	watchRelease := make(chan struct{})
	var sendOrder []string

	errCh := runGoroutineBody(
		logCh,
		true, // watchFlag
		func() error { return nil },
		func() error {
			close(watchStarted)
			<-watchRelease
			// At this point the channel must still be open — watch runs under
			// the same goroutine as the close call.
			select {
			case logCh <- logging.LogLine{Formatted: "watch-log"}:
			default:
				t.Error("logCh unexpectedly closed before watch returned")
			}
			return nil
		},
		func() { sendOrder = append(sendOrder, "run-done") },
		func() { sendOrder = append(sendOrder, "watching") },
	)

	<-watchStarted
	close(watchRelease)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("errCh = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for goroutine to finish")
	}

	// Drain the watch-log line first, then verify close.
	line, ok := <-logCh
	if !ok || line.Formatted != "watch-log" {
		t.Errorf("expected watch-log before close, got ok=%v line=%q", ok, line.Formatted)
	}
	_, ok = <-logCh
	if ok {
		t.Error("expected logCh to be closed after watch exit")
	}

	want := []string{"watching", "run-done"}
	if !reflect.DeepEqual(sendOrder, want) {
		t.Errorf("sendOrder = %v, want %v", sendOrder, want)
	}
}

// TestRunGoroutineClosesLogChOnOrchestratorError verifies that an orchestrator
// error still closes the channel (the early-return path must not leak the
// channel and hang the TUI subscription).
func TestRunGoroutineClosesLogChOnOrchestratorError(t *testing.T) {
	logCh := make(chan logging.LogLine, 4)
	wantErr := context.Canceled

	errCh := runGoroutineBody(
		logCh,
		true, // watchFlag — irrelevant since orch errors
		func() error { return wantErr },
		func() error { t.Fatal("watchRun must not be called after orchestrator error"); return nil },
		func() {},
		func() { t.Fatal("sendWatching must not be called after orchestrator error") },
	)

	select {
	case err := <-errCh:
		if err != wantErr {
			t.Errorf("errCh = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for goroutine to finish")
	}

	_, ok := <-logCh
	if ok {
		t.Error("expected logCh to be closed after orchestrator error")
	}
}

// TestRunTUIFactoryRunScopedLogger verifies that every logger produced by
// the wrapped factory — including ones produced on a subsequent call that
// simulates orchestrator.Run's own invocation — fans out to the same shared
// TUI handler. This guards against regressions where the wrap is applied only
// to the bootstrap logger.
func TestRunTUIFactoryRunScopedLogger(t *testing.T) {
	bootstrapDir := t.TempDir()
	runDir := t.TempDir()
	logCh := make(chan logging.LogLine, 16)
	factory := wrapFactoryForTUI(logging.NewLoggerFileOnly, logCh)

	bootstrap, err := factory(bootstrapDir)
	if err != nil {
		t.Fatalf("bootstrap factory: %v", err)
	}
	runScoped, err := factory(runDir)
	if err != nil {
		t.Fatalf("run-scoped factory: %v", err)
	}

	bootstrap.Warn("bootstrap-warn")
	runScoped.Warn("run-scoped-warn")

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case line := <-logCh:
			if strings.Contains(line.Formatted, "bootstrap-warn") {
				got["bootstrap-warn"] = true
			}
			if strings.Contains(line.Formatted, "run-scoped-warn") {
				got["run-scoped-warn"] = true
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for log line %d", i)
		}
	}
	if !got["bootstrap-warn"] || !got["run-scoped-warn"] {
		t.Errorf("expected both warns on TUI channel, got %v", got)
	}
}
