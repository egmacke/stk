package operations

import (
	"errors"
	"fmt"

	"stk/internal/config"
	"stk/internal/git"
	"stk/internal/output"
	"stk/internal/stack"
)

// Env carries everything an operation needs: the repository, the resolved
// configuration and the output sink.
type Env struct {
	Repo        *git.Repo
	Cfg         config.Config
	Out         *output.Printer
	Interactive bool
	DryRun      bool
	// Autostash allows commands that need a clean working tree to park
	// uncommitted changes and restore them afterwards, instead of refusing to
	// run. It is never on unless the user asked, by flag or by config.
	Autostash bool
	// Confirm asks the user a yes/no question. It is nil when the command is
	// running non-interactively.
	Confirm func(question string, defaultYes bool) (bool, error)
}

// ErrConflict signals that a rebase stopped on a conflict and the operation
// journal has been left in place for stk continue.
var ErrConflict = errors.New("restack stopped on a conflict")

// ErrOperationInProgress is returned when another stk operation is unfinished.
type ErrOperationInProgress struct{ Op *Operation }

func (e *ErrOperationInProgress) Error() string {
	return fmt.Sprintf("an stk %s operation is already in progress (started in %s)\n\nFinish it with:\n\n    stk continue\n\nOr abandon it with:\n\n    stk abort",
		e.Op.Type, e.Op.Worktree)
}

// RequireNoOperation refuses to start work while an operation journal exists.
//
// Commands call it before anything else, because a paused operation leaves the
// worktree mid-rebase and every other diagnosis would be misleading.
func RequireNoOperation(repo *git.Repo) error {
	op, err := LoadOperation(repo)
	if errors.Is(err, ErrNoOperation) {
		return nil
	}
	if err != nil {
		return err
	}
	return &ErrOperationInProgress{Op: op}
}

func requireNoOperation(env *Env) error { return RequireNoOperation(env.Repo) }

// requireCleanTree refuses history rewriting while the worktree has changes.
//
// Every command calls Stash before it reaches here, so a dirty tree at this
// point means autostashing was turned off and the changes are the user's to
// deal with.
func requireCleanTree(env *Env) error {
	clean, err := env.Repo.IsClean()
	if err != nil {
		return err
	}
	if clean {
		return nil
	}
	if env.Autostash && env.DryRun {
		noteDryRunStash(env)
		return nil
	}
	return errors.New("working tree has uncommitted changes\n\n" +
		"Autostashing is off (--no-autostash, or stk.autostash = false), so stk\n" +
		"will not park them for you. Commit or stash them first, or re-run with\n" +
		"--autostash")
}

// noteDryRunStash says what a real run would have parked. A dry run parks
// nothing, and must never refuse to describe a plan.
func noteDryRunStash(env *Env) {
	if !env.Autostash {
		return
	}
	if clean, err := env.Repo.IsClean(); err != nil || clean {
		return
	}
	env.Out.Printf("(dry-run) would stash the uncommitted changes and restore them afterwards")
}

// resolveHead reads a branch's current tip straight from git.
func resolveHead(repo *git.Repo, b *stack.Branch) (string, error) {
	return repo.RevParse("refs/heads/" + b.Name)
}
