package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/smileoniks-ctrl/govm/internal/deps"
)

// DepsService encapsulates the CLI dependency workflow.
//
// The full update lifecycle (check -> confirm apply -> apply ->
// checks -> rollback) is driven as a synchronous loop over the pure
// deps.UpdateCycle state machine. Operational intents emitted by the
// cycle are executed through Deps; confirmation intents are rendered
// to Stdout and resolved through Confirm. The standalone list /
// backups / restore commands call Deps directly.
type DepsService struct {
	ModuleDir string
	Stdout    io.Writer
	Stdin     io.Reader

	// Confirm asks the user a yes/no question. The default answer
	// (used on empty input or EOF) is defaultYes. The default
	// implementation writes the question to Stdout and reads a
	// line from Stdin.
	Confirm func(question string, defaultYes bool) (bool, error)

	// Deps performs the dependency operations of the module at
	// ModuleDir. It is the single seam through which every command
	// does side-effecting work; main.go binds the same deps.Executor
	// the TUI uses, tests substitute a fake.
	Deps deps.API
}

// NewDepsService builds a DepsService over the already-bound
// executor: the composition root (main.go) owns settings loading and
// executor construction, so the service never touches the filesystem
// or the go toolchain here. The module is resolved lazily by the
// executor, not by this constructor.
func NewDepsService(moduleDir string, executor deps.API, stdout io.Writer, stdin io.Reader) *DepsService {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stdin == nil {
		stdin = os.Stdin
	}
	return &DepsService{
		ModuleDir: moduleDir,
		Stdout:    stdout,
		Stdin:     stdin,
		Confirm:   defaultConfirm(stdin, stdout),
		Deps:      executor,
	}
}

// defaultConfirm returns a Confirm implementation that prompts on
// stdout and reads a single line from stdin.
func defaultConfirm(stdin io.Reader, stdout io.Writer) func(string, bool) (bool, error) {
	scanner := bufio.NewScanner(stdin)
	return func(question string, defaultYes bool) (bool, error) {
		prompt := fmt.Sprintf("%s [%s]: ", question, yesNoLabel(defaultYes))
		for {
			if _, err := io.WriteString(stdout, prompt); err != nil {
				return false, err
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
					return false, err
				}
				return defaultYes, nil
			}
			answer := strings.TrimSpace(scanner.Text())
			if answer == "" {
				return defaultYes, nil
			}
			switch strings.ToLower(answer) {
			case "y", "yes":
				return true, nil
			case "n", "no":
				return false, nil
			default:
				if _, err := io.WriteString(stdout, "Please answer y or n.\n"); err != nil {
					return false, err
				}
			}
		}
	}
}

func yesNoLabel(defaultYes bool) string {
	if defaultYes {
		return "Y/n"
	}
	return "y/N"
}

// RunList prints the current module dependencies to Stdout.
func (s *DepsService) RunList() error {
	deps, err := s.Deps.List()
	if err != nil {
		return fmt.Errorf("failed to read dependencies: %w", err)
	}
	direct, indirect := 0, 0
	for _, d := range deps {
		if d.Indirect {
			indirect++
		} else {
			direct++
		}
	}
	fmt.Fprintf(s.Stdout, "🔍 Reading module dependencies in %s...\n\n", s.ModuleDir)
	if len(deps) == 0 {
		fmt.Fprintln(s.Stdout, "  (no dependencies)")
	} else {
		for _, d := range deps {
			kind := "direct"
			if d.Indirect {
				kind = "indirect"
			}
			fmt.Fprintf(s.Stdout, "  %s\t%s\t%s\n", d.Path, d.Version, kind)
		}
	}
	fmt.Fprintf(s.Stdout, "\n✅ %d direct, %d indirect dependencies.\n", direct, indirect)
	return nil
}

// RunCheck prints the dependencies and marks available updates at the
// given level. The check is an IntentCheckUpdates through Deps, so
// RunCheck and RunUpdate share the same code path.
func (s *DepsService) RunCheck(level deps.UpdateLevel) error {
	event, err := s.Deps.Execute(deps.IntentCheckUpdates{})
	if err != nil {
		return fmt.Errorf("failed to check dependencies: %w", err)
	}
	done, ok := event.(deps.CheckUpdatesDoneEvent)
	if !ok {
		return fmt.Errorf("failed to check dependencies: unexpected event %T", event)
	}
	if done.Err != nil {
		return fmt.Errorf("failed to check dependencies: %w", done.Err)
	}
	mods := done.Dependencies
	updates := countDirectUpdates(mods, level)
	fmt.Fprintf(s.Stdout, "🔍 Checking available updates in %s...\n\n", s.ModuleDir)
	if len(mods) == 0 {
		fmt.Fprintln(s.Stdout, "  (no dependencies)")
	} else {
		for _, d := range mods {
			status := "current"
			version := d.Version
			target := deps.TargetVersion(d, level)
			switch {
			case d.Error != "":
				status = "error: " + d.Error
			case d.Deprecated != "" && target != "":
				status = "update available (deprecated)"
				version = fmt.Sprintf("%s → %s", d.Version, target)
			case target != "":
				status = "update available"
				version = fmt.Sprintf("%s → %s", d.Version, target)
			case d.Deprecated != "":
				status = "deprecated"
			}
			fmt.Fprintf(s.Stdout, "  %s\t%s\t%s\n", d.Path, version, status)
		}
	}
	suffix := levelSuffix(level)
	if updates == 0 {
		fmt.Fprintf(s.Stdout, "\n✅ 0 direct updates available%s.\n", suffix)
	} else {
		fmt.Fprintf(s.Stdout, "\n📦 %d direct update(s) available%s.\n", updates, suffix)
	}
	return nil
}

// levelSuffix renders the level qualifier appended to summary lines
// ("" for latest, " (patch)" otherwise).
func levelSuffix(level deps.UpdateLevel) string {
	if level == deps.LevelLatest {
		return ""
	}
	return fmt.Sprintf(" (%s)", level)
}

// RunBackups prints saved dependency backups for the current module.
func (s *DepsService) RunBackups() error {
	backups, err := s.Deps.Backups()
	if err != nil {
		return fmt.Errorf("failed to list dependency backups: %w", err)
	}
	fmt.Fprintf(s.Stdout, "📦 Dependency backups in %s:\n\n", s.ModuleDir)
	if len(backups) == 0 {
		fmt.Fprintln(s.Stdout, "  (no backups)")
		return nil
	}
	for _, b := range backups {
		fmt.Fprintf(
			s.Stdout,
			"  %s\t%s\t%s\t%d update(s)\n",
			b.Name,
			b.ModulePath,
			b.Kind,
			b.Updated,
		)
	}
	return nil
}

// RunRestore restores one saved dependency backup by filename.
func (s *DepsService) RunRestore(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("backup filename is required")
	}
	ok, err := s.Confirm(fmt.Sprintf("Restore dependency backup %s?", name), true)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(s.Stdout, "🛑 Restore canceled.")
		return nil
	}
	restored, err := s.Deps.Restore(name)
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	fmt.Fprintf(
		s.Stdout,
		"✅ Restored dependencies from %s (%s).\n",
		restored.BackupName,
		restored.BackupCreated.Format("2006-01-02 15:04:05"),
	)
	return nil
}

func countDirectUpdates(mods []deps.ModuleDependency, level deps.UpdateLevel) int {
	entries, _ := deps.BuildUpdatePlan(mods, deps.UpdateSelection{Level: level})
	return len(entries)
}

// UpdateOptions configures RunUpdate. Modules narrows the plan to the
// named dependencies (exact path or unique suffix; empty = every
// direct dependency). Level bounds the target versions. DryRun prints
// the plan and stops before any prompt or change. Yes answers every
// prompt (apply, checks, rollback) with yes; DryRun wins over Yes.
type UpdateOptions struct {
	Modules []string
	Level   deps.UpdateLevel
	DryRun  bool
	Yes     bool
}

func (o UpdateOptions) selection() deps.UpdateSelection {
	return deps.UpdateSelection{Modules: o.Modules, Level: o.Level}
}

// RunUpdate drives the full Dependency Update Cycle synchronously:
// check, confirm apply, apply, optional checks, optional rollback.
// The pure deps.UpdateCycle owns all transition logic and outcome
// classification; this method renders user-facing output and threads
// confirmations and operational execution through its seams.
func (s *DepsService) RunUpdate(opts UpdateOptions) error {
	fmt.Fprintf(s.Stdout, "🔍 Checking available updates in %s...\n", s.ModuleDir)
	confirm := s.Confirm
	if opts.Yes && !opts.DryRun {
		confirm = func(string, bool) (bool, error) { return true, nil }
	}
	cycle := deps.NewUpdateCycle()
	c, intent, err := cycle.Handle(deps.StartEvent{ModuleDir: s.ModuleDir, Selection: opts.selection()})
	if err != nil {
		return err
	}
	for !c.IsTerminal() {
		if confirmApply, ok := intent.(deps.IntentConfirmApply); ok && opts.DryRun {
			s.renderDryRun(confirmApply)
			return nil
		}
		next, nextIntent, advErr := s.advance(c, intent, confirm)
		if advErr != nil {
			return advErr
		}
		c, intent = next, nextIntent
	}
	return s.renderOutcome(c, opts)
}

// renderDryRun prints the plan in the `deps check` row format and a
// summary line, without prompting or applying.
func (s *DepsService) renderDryRun(i deps.IntentConfirmApply) {
	fmt.Fprintln(s.Stdout)
	for _, e := range i.Entries {
		fmt.Fprintf(s.Stdout, "  %s\t%s → %s\tupdate available\n", e.Path, e.OldVersion, e.NewVersion)
	}
	fmt.Fprintf(s.Stdout, "\n📦 %d %s would be updated%s (dry run).\n",
		len(i.Entries), deps.Pluralize(len(i.Entries), "dependency", "dependencies"), levelSuffix(i.Level))
}

// advance processes a single intent produced by the cycle.
// Confirmation intents are rendered and resolved through Confirm;
// operational intents are executed through Deps. The
// returned cycle/intent pair is the result of feeding the resolved
// event back into deps.UpdateCycle.Handle.
func (s *DepsService) advance(c deps.UpdateCycle, intent deps.Intent, confirm func(string, bool) (bool, error)) (deps.UpdateCycle, deps.Intent, error) {
	switch i := intent.(type) {
	case deps.NoIntent:
		return c, intent, fmt.Errorf("deps: cycle stalled in phase %s with no intent", c.Phase())
	case deps.IntentConfirmApply:
		s.renderApplyConfirm(i)
		yes, err := confirm("\nApply these updates?", i.DefaultYes)
		if err != nil {
			return c, intent, err
		}
		return c.Handle(deps.ConfirmApplyEvent{Yes: yes})
	case deps.IntentConfirmChecks:
		updated := i.UpdatedCount
		fmt.Fprintf(s.Stdout, "✅ Updated %d direct %s.\n",
			updated, deps.Pluralize(updated, "dependency", "dependencies"))
		yes, err := confirm("\n🧪 Run checks (go test ./... and go vet ./...)?", i.DefaultYes)
		if err != nil {
			return c, intent, err
		}
		return c.Handle(deps.ConfirmChecksEvent{Yes: yes})
	case deps.IntentConfirmRollback:
		s.renderCheckFailure(i)
		yes, err := confirm("\nRoll back to pre-update state?", i.DefaultYes)
		if err != nil {
			return c, intent, err
		}
		return c.Handle(deps.ConfirmRollbackEvent{Yes: yes})
	case deps.IntentCheckUpdates, deps.IntentApplyUpdates, deps.IntentCompensate,
		deps.IntentRunChecks, deps.IntentRollback:
		event, err := s.Deps.Execute(intent)
		if err != nil {
			return c, intent, fmt.Errorf("failed to execute %T: %w", intent, err)
		}
		return c.Handle(event)
	default:
		return c, intent, fmt.Errorf("deps: unhandled intent %T", intent)
	}
}

func (s *DepsService) renderApplyConfirm(i deps.IntentConfirmApply) {
	kind := "direct "
	if i.Explicit {
		kind = ""
	}
	fmt.Fprintf(s.Stdout, "\n⚠️  %d %s%s will be updated%s:\n",
		len(i.Entries), kind, deps.Pluralize(len(i.Entries), "dependency", "dependencies"), levelSuffix(i.Level))
	for _, e := range i.Entries {
		fmt.Fprintf(s.Stdout, "  - %s  %s → %s\n", e.Path, e.OldVersion, e.NewVersion)
	}
}

// renderCheckFailure prints the reason checks did not pass. Failed
// checks (a check command returned non-zero) and inconclusive checks
// (the checks could not be run at all) produce distinct output but
// both lead to the rollback confirmation.
func (s *DepsService) renderCheckFailure(i deps.IntentConfirmRollback) {
	if i.CheckErr != nil {
		fmt.Fprintf(s.Stdout, "\n❌ Checks could not run: %v\n", i.CheckErr)
		return
	}
	if i.CheckResult != nil {
		fmt.Fprintf(s.Stdout, "\n❌ Checks failed: %s\n", i.CheckResult.Command)
		if i.CheckResult.Output != "" {
			fmt.Fprintln(s.Stdout, i.CheckResult.Output)
		}
	}
}

// renderOutcome translates a terminal cycle state into the final
// user-facing message and return error. Failure outcomes surface the
// operational error preserved by the cycle; recovery outcomes also
// surface the persistent backup metadata so the user can recover
// manually.
func (s *DepsService) renderOutcome(c deps.UpdateCycle, opts UpdateOptions) error {
	switch c.Outcome() {
	case deps.OutcomeNoUpdates:
		if len(opts.Modules) > 0 {
			fmt.Fprintf(s.Stdout, "ℹ️  Already up to date%s: %s\n", levelSuffix(opts.Level), strings.Join(opts.Modules, ", "))
			return nil
		}
		fmt.Fprintf(s.Stdout, "ℹ️  No direct dependency updates available%s.\n", levelSuffix(opts.Level))
		return nil
	case deps.OutcomeApplyCanceled:
		fmt.Fprintln(s.Stdout, "🛑 Update canceled.")
		return nil
	case deps.OutcomeUpdatedUnchecked:
		fmt.Fprintln(s.Stdout, "ℹ️  Update complete. Checks skipped.")
		return nil
	case deps.OutcomeUpdatedVerified:
		fmt.Fprintln(s.Stdout, "✅ Checks passed.")
		return nil
	case deps.OutcomeUpdatesKeptWithFailedChecks:
		fmt.Fprintln(s.Stdout, "⚠️  Update kept. Failed checks were not rolled back.")
		return nil
	case deps.OutcomeRolledBack:
		fmt.Fprintln(s.Stdout, "✅ Rolled back to pre-update state.")
		return nil
	case deps.OutcomeUpdateFailedRestored:
		// The apply mutated files then failed, but automatic
		// compensation restored them. This is still an error
		// outcome: tell the user the files were restored and
		// surface the original apply failure.
		fmt.Fprintln(s.Stdout, "✅ Update failed; dependencies were automatically restored to the pre-update state.")
		return s.failureError(c, "update failed")
	case deps.OutcomeRecoveryRequired:
		return s.recoveryError(c)
	case deps.OutcomeFailed:
		return s.failureError(c, "update failed")
	default:
		return fmt.Errorf("update ended in unexpected outcome %s", c.Outcome())
	}
}

// failureError wraps the cycle's preserved failure with prefix, or
// returns a bare prefix error when no failure is recorded.
func (s *DepsService) failureError(c deps.UpdateCycle, prefix string) error {
	if c.Failure() != nil {
		return fmt.Errorf("%s: %w", prefix, c.Failure())
	}
	return fmt.Errorf("%s", prefix)
}

// recoveryError builds the error for OutcomeRecoveryRequired. It
// always includes the persistent backup name and path (when the cycle
// retained them) so the user can recover the pre-update state by hand.
func (s *DepsService) recoveryError(c deps.UpdateCycle) error {
	detail := ""
	if b := c.Backup(); b != nil {
		detail = fmt.Sprintf(" (persistent backup: %s at %s)", b.Name, b.Path)
	}
	prefix := fmt.Sprintf("update failed and could not be fully rolled back%s", detail)
	return s.failureError(c, prefix)
}
