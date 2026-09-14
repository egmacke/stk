package operations

import (
	"errors"
	"fmt"

	"stk/internal/stack"
)

// notOwnerError explains that another worktree owns the in-flight operation.
func notOwnerError(op *Operation, here string) error {
	return fmt.Errorf(
		"this stk %s operation belongs to another worktree\n\n    %s\n\nRun stk continue or stk abort from there (currently in %s)",
		op.Type, op.Worktree, here)
}

// Continue resumes a restack that stopped on a conflict, picking up the
// original scope rather than only the branch that conflicted.
func Continue(env *Env) error {
	repo := env.Repo
	op, err := LoadOperation(repo)
	if err != nil {
		if errors.Is(err, ErrNoOperation) {
			return errors.New("no stk operation in progress")
		}
		return err
	}
	if !op.OwnedByThisWorktree(repo) {
		return notOwnerError(op, repo.Root)
	}

	if repo.RebaseInProgress() {
		res := repo.R.Interactive("rebase", "--continue")
		if !res.OK() {
			if repo.RebaseInProgress() {
				env.Out.Warnf("")
				env.Out.Warnf("The rebase is still unresolved.")
				env.Out.Warnf("")
				env.Out.Warnf("Resolve the remaining conflicts and run stk continue again,")
				env.Out.Warnf("or abandon the whole operation with stk abort.")
				return ErrConflict
			}
			return res.Error()
		}
	}

	g, err := stack.Load(repo, env.Cfg)
	if err != nil {
		return err
	}

	// The branch at the recorded position is the one that just finished.
	if op.Position < len(op.Branches) {
		step := op.Branches[op.Position]
		if b := g.ByID[step.BranchID]; b != nil && b.Parent != nil {
			if err := finishBranch(env, b, b.Parent.SHA); err != nil {
				return err
			}
			op.Done.Restacked++
			env.Out.OK("%s", b.Name)
		}
	}

	_, err = runPlan(env, op, g, op.Position+1)
	return err
}

// Abort undoes the whole stk operation, not merely the rebase that is
// currently in flight.
func Abort(env *Env) error {
	repo := env.Repo
	op, err := LoadOperation(repo)
	if err != nil {
		if errors.Is(err, ErrNoOperation) {
			return errors.New("no stk operation in progress")
		}
		return err
	}
	if !op.OwnedByThisWorktree(repo) {
		return notOwnerError(op, repo.Root)
	}

	if repo.RebaseInProgress() {
		if res := repo.R.Interactive("rebase", "--abort"); !res.OK() {
			return res.Error()
		}
	}

	// Detach first so no branch reset below is the one HEAD points at; the
	// final switch then performs a real checkout of the restored branch.
	if head := repo.CurrentBranch(); head != "" {
		if err := repo.SwitchDetach("HEAD"); err != nil {
			return err
		}
	}

	names := map[string]string{}
	for _, step := range op.Branches {
		names[step.BranchID] = step.Name
	}
	restored := 0
	for id, sha := range op.Snapshots {
		name := names[id]
		if name == "" || !repo.BranchExists(name) {
			continue
		}
		if err := repo.UpdateRef("refs/heads/"+name, sha); err != nil {
			return err
		}
		restored++
	}
	for id, base := range op.PrevBases {
		if base == "" {
			if err := stack.ClearBase(repo, id); err != nil {
				return err
			}
			continue
		}
		if err := stack.SetBase(repo, id, base); err != nil {
			return err
		}
	}

	if op.OriginalBranch != "" && repo.BranchExists(op.OriginalBranch) {
		if err := repo.Switch(op.OriginalBranch); err != nil {
			return err
		}
	}
	if err := op.Clear(repo); err != nil {
		return err
	}
	mirrorGHStack(env, ghSyncOptions{})
	env.Out.OK("Aborted stk %s; %d branch(es) restored.", op.Type, restored)
	// The branches are back where they were, so the changes parked when the
	// operation started belong in the working tree again.
	op.Autostash().Restore(env)
	return nil
}
