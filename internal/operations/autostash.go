package operations

import (
	"fmt"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// Autostash is a set of uncommitted changes stk has parked so it can move or
// rewrite branches, and will put back afterwards.
//
// The stash commit is held by a ref under refs/stk/autostash rather than only
// by the stash reflog, so changes stay reachable — and reported by stk doctor
// — if stk is killed between parking them and restoring them.
type Autostash struct {
	// SHA is the stash commit holding the parked changes.
	SHA string
	// Ref is the stk-owned ref keeping that commit alive.
	Ref string
	// Label describes the operation the changes were parked for.
	Label string
}

// Stash parks the working tree's tracked changes and says so.
//
// It returns nil, and no error, whenever there is nothing to do: the tree is
// clean, the caller was not given permission to stash, or this is a dry run.
func Stash(env *Env, label string) (*Autostash, error) {
	a, err := stashQuietly(env, label)
	if a != nil {
		env.Out.OK("Stashed uncommitted changes")
	}
	return a, err
}

// stashQuietly is Stash without the progress line, for callers that may undo
// the whole operation and should then report nothing at all.
func stashQuietly(env *Env, label string) (*Autostash, error) {
	if !env.Autostash || env.DryRun {
		return nil, nil
	}
	clean, err := env.Repo.IsClean()
	if err != nil {
		return nil, err
	}
	if clean {
		return nil, nil
	}

	repo := env.Repo
	message := stashMessage(label)
	sha, err := repo.StashCreate(message)
	if err != nil {
		return nil, err
	}
	if sha == "" {
		// Only untracked files, which stk leaves alone.
		return nil, nil
	}
	id, err := stack.NewID()
	if err != nil {
		return nil, err
	}
	a := &Autostash{SHA: sha, Ref: stack.AutostashRef(id), Label: label}
	// Anchor the commit before clearing the tree, never after.
	if err := repo.UpdateRef(a.Ref, sha); err != nil {
		return nil, err
	}
	if err := repo.ResetHard(); err != nil {
		a.Keep(env, "the working tree could not be cleared")
		return nil, err
	}
	return a, nil
}

func stashMessage(label string) string { return "stk autostash: " + label }

// Apply puts the parked changes back into the working tree without forgetting
// the stash, so a failed apply loses nothing.
func (a *Autostash) Apply(env *Env) git.Result {
	res := a.tryApply(env)
	if !res.OK() {
		// Never hide git's own account of a conflict it is about to leave in
		// the working tree.
		echoGit(env, res)
	}
	return res
}

// tryApply is Apply without the report, for callers that undo the apply again
// and would otherwise describe a conflict that no longer exists.
func (a *Autostash) tryApply(env *Env) git.Result { return env.Repo.StashApply(a.SHA) }

// Drop forgets the parked changes. Callers must have established that they are
// back in the working tree.
func (a *Autostash) Drop(env *Env) error { return env.Repo.DeleteRef(a.Ref) }

// Keep hands the parked changes to the git stash list and says how to reach
// them. It is the fallback for every path that cannot put them back cleanly:
// an unfinished operation is recoverable, lost work is not.
func (a *Autostash) Keep(env *Env, why string) {
	p := env.Out
	if err := env.Repo.StashStore(a.SHA, stashMessage(a.Label)); err != nil {
		p.Warnf("")
		p.Warnf("Your stashed changes %s, and could not be added to the stash list.", why)
		p.Warnf("They are still held by %s; recover them with:", a.Ref)
		p.Warnf("")
		p.Warnf("    git stash apply %s", a.SHA)
		p.Warnf("")
		return
	}
	_ = a.Drop(env)
	p.Warnf("")
	p.Warnf("Your stashed changes %s.", why)
	p.Warnf("They are safe in the stash list; recover them with:")
	p.Warnf("")
	p.Warnf("    git stash pop %s", git.StashListRef(0))
	p.Warnf("")
}

// Restore puts the parked changes back, mirroring git's own autostash: a
// conflicting apply is left in the working tree for the user to resolve and
// the stash is kept as well, so nothing depends on that resolution.
func (a *Autostash) Restore(env *Env) {
	if a == nil || a.SHA == "" {
		return
	}
	if res := a.Apply(env); !res.OK() {
		a.Keep(env, "did not reapply cleanly")
		return
	}
	if err := a.Drop(env); err != nil {
		env.Out.Warnf("%s could not remove %s: %v", git.ShortSHA(a.SHA), a.Ref, err)
		return
	}
	env.Out.OK("Restored stashed changes")
}

// Switch checks out a branch, parking uncommitted changes when git refuses to
// carry them across, and reports the switch.
//
// Unlike a restack, a checkout can be undone, so it is atomic: if the parked
// changes will not merge onto the target branch, stk returns the user to the
// branch they started on with the changes restored.
func Switch(env *Env, name string) error {
	repo := env.Repo
	res := repo.TrySwitch(name)
	if res.OK() {
		env.Out.OK("Switched to %s", output.BranchName(name))
		return nil
	}
	// Git carries uncommitted changes across a switch on its own, so reaching
	// here with a dirty tree means it refused to: the target branch has
	// different content in a file the user has edited.
	origin := repo.CurrentBranch()
	// Quietly: a checkout that has to be rolled back below should look to the
	// user like the no-op it is.
	stash, err := stashQuietly(env, "checkout "+name)
	if err != nil {
		return err
	}
	if stash == nil {
		return res.Error()
	}
	if retry := repo.TrySwitch(name); !retry.OK() {
		// Refused for some other reason; leave the user exactly where they
		// were, with their changes.
		putBack(env, stash)
		return retry.Error()
	}
	if applied := stash.tryApply(env); applied.OK() {
		if err := stash.Drop(env); err != nil {
			return err
		}
		env.Out.OK("Switched to %s", output.BranchName(name))
		env.Out.OK("Carried your uncommitted changes across")
		return nil
	}
	// Roll the whole checkout back: the conflict markers are worthless, the
	// branch the user was standing on is not.
	if err := repo.ResetHard(); err != nil {
		stash.Keep(env, "could not be restored after the checkout was undone")
		return err
	}
	if origin != "" {
		if err := repo.Switch(origin); err != nil {
			stash.Keep(env, fmt.Sprintf("could not be restored because returning to %s failed", origin))
			return err
		}
	}
	if !putBack(env, stash) {
		return fmt.Errorf("your uncommitted changes conflict with %s, and stk could not put them back", name)
	}
	return fmt.Errorf("your uncommitted changes conflict with %s\n\n"+
		"Nothing was switched. Commit or stash them, or resolve the overlap, then\n"+
		"check %s out", name, name)
}

// putBack restores a stash onto the branch it was taken from, where it applies
// cleanly by construction, and reports whether it did.
//
// Nothing is printed on success: the caller is about to explain that nothing
// happened at all. A failure is loud, because then something did.
func putBack(env *Env, a *Autostash) bool {
	if a == nil {
		return true
	}
	if res := a.Apply(env); !res.OK() {
		a.Keep(env, "could not be put back")
		return false
	}
	_ = a.Drop(env)
	return true
}
