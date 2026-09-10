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
// stk never stashes on the user's behalf.
func requireCleanTree(env *Env) error {
	clean, err := env.Repo.IsClean()
	if err != nil {
		return err
	}
	if clean {
		return nil
	}
	return errors.New("working tree has uncommitted changes\n\nCommit or stash them first; stk does not stash automatically")
}

// resolveHead reads a branch's current tip straight from git.
func resolveHead(repo *git.Repo, b *stack.Branch) (string, error) {
	return repo.RevParse("refs/heads/" + b.Name)
}
