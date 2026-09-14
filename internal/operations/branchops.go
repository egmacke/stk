package operations

import (
	"errors"
	"fmt"

	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// resolveParent validates a candidate stack parent.
//
// A parent must be trunk or a branch stk already tracks; stk never adopts an
// arbitrary branch into the graph on the user's behalf.
func resolveParent(g *stack.Graph, name string) (*stack.Branch, error) {
	b, ok := g.Resolve(name)
	if !ok {
		return nil, fmt.Errorf("branch %q does not exist", name)
	}
	if b.IsTrunk {
		return b, nil
	}
	if !b.Tracked {
		return nil, fmt.Errorf(
			"branch %q is not tracked by stk\n\nTrack it first:\n\n    stk track %s --parent <branch>",
			name, name)
	}
	if b.HasProblem() {
		return nil, fmt.Errorf("branch %q has a metadata problem: %s", name, problemText(b))
	}
	return b, nil
}

// parentID converts a parent node to the value stored in metadata; trunk is
// recorded as an absent parent.
func parentID(parent *stack.Branch) string {
	if parent == nil || parent.IsTrunk {
		return ""
	}
	return parent.ID
}

// currentStackParent returns the branch a new child should hang off when no
// --from was given.
func currentStackParent(g *stack.Graph) (*stack.Branch, error) {
	if g.CurrentName == "" {
		return nil, errors.New("HEAD is detached; use --from to name the parent branch")
	}
	cur, ok := g.Resolve(g.CurrentName)
	if !ok {
		return nil, fmt.Errorf("branch %q is not known to stk", g.CurrentName)
	}
	if cur.IsTrunk {
		return cur, nil
	}
	if !cur.Tracked {
		return nil, fmt.Errorf(
			"the current branch %q is not tracked by stk\n\nTrack it first:\n\n    stk track %s --parent <branch>",
			cur.Name, cur.Name)
	}
	return cur, nil
}

// CreateOptions configures stk create.
type CreateOptions struct {
	Name     string
	From     string
	Checkout bool
}

// Create adds a branch to the stack graph.
//
// The branch is created from the parent's ref rather than by checking the
// parent out, so it works even when the parent is checked out in another
// worktree.
func Create(env *Env, g *stack.Graph, opts CreateOptions) error {
	repo := env.Repo
	if !repo.ValidBranchName(opts.Name) {
		return fmt.Errorf("%q is not a valid branch name", opts.Name)
	}
	if repo.BranchExists(opts.Name) {
		return fmt.Errorf("branch %q already exists", opts.Name)
	}
	if opts.Name == env.Cfg.Trunk {
		return fmt.Errorf("%q is the trunk branch", opts.Name)
	}

	var parent *stack.Branch
	var err error
	if opts.From != "" {
		parent, err = resolveParent(g, opts.From)
	} else {
		parent, err = currentStackParent(g)
	}
	if err != nil {
		return err
	}
	if parent.SHA == "" {
		return fmt.Errorf("branch %q has no commits to branch from", parent.Name)
	}

	id, err := stack.NewID()
	if err != nil {
		return err
	}
	if env.DryRun {
		env.Out.Printf("Would create %s from %s", opts.Name, parent.Name)
		return nil
	}
	if err := repo.CreateBranch(opts.Name, "refs/heads/"+parent.Name); err != nil {
		return err
	}
	if err := stack.WriteMeta(repo, opts.Name, stack.Meta{ID: id, ParentID: parentID(parent)}); err != nil {
		return err
	}
	if err := stack.SetBase(repo, id, parent.SHA); err != nil {
		return err
	}

	env.Out.OK("Created %s from %s", output.BranchName(opts.Name), output.BranchName(parent.Name))
	env.Out.OK("Parent: %s", output.BranchName(parent.Name))
	mirrorGHStack(env, ghSyncOptions{})
	if opts.Checkout {
		if err := Switch(env, opts.Name); err != nil {
			return err
		}
	}
	return nil
}

// Track brings an existing branch into the stack graph under an explicit
// parent. The initial base is the merge base of the two branches.
func Track(env *Env, g *stack.Graph, name, parentName string) error {
	repo := env.Repo
	child, ok := g.Resolve(name)
	if !ok {
		return fmt.Errorf("branch %q does not exist", name)
	}
	if child.IsTrunk {
		return fmt.Errorf("%q is the trunk branch and is always the root of the graph", name)
	}
	parent, err := resolveParent(g, parentName)
	if err != nil {
		return err
	}
	if parent == child {
		return errors.New("a branch cannot be its own parent")
	}
	if child.Tracked && stack.IsDescendant(child, parent) {
		return fmt.Errorf("%s is below %s in the stack; that would create a cycle", parentName, name)
	}

	base, err := repo.MergeBase("refs/heads/"+parent.Name, "refs/heads/"+child.Name)
	if err != nil {
		return fmt.Errorf("%s and %s have no common ancestor, so stk cannot tell which commits belong to %s",
			parent.Name, child.Name, child.Name)
	}

	id := child.ID
	if !child.Tracked || id == "" {
		if id, err = stack.NewID(); err != nil {
			return err
		}
	}
	if env.DryRun {
		env.Out.Printf("Would track %s with parent %s (base %s)", child.Name, parent.Name, base[:7])
		return nil
	}
	if err := stack.WriteMeta(repo, child.Name, stack.Meta{ID: id, ParentID: parentID(parent)}); err != nil {
		return err
	}
	if err := stack.SetBase(repo, id, base); err != nil {
		return err
	}
	env.Out.OK("Tracking %s with parent %s", output.BranchName(child.Name), output.BranchName(parent.Name))
	mirrorGHStack(env, ghSyncOptions{})
	return nil
}

// UntrackOptions configures stk untrack.
type UntrackOptions struct {
	Recursive bool
	Reparent  string
}

// Untrack removes stack metadata. It never deletes a git branch.
func Untrack(env *Env, g *stack.Graph, name string, opts UntrackOptions) error {
	repo := env.Repo
	b, ok := g.Resolve(name)
	if !ok {
		return fmt.Errorf("branch %q does not exist", name)
	}
	if b.IsTrunk {
		return errors.New("trunk is not tracked metadata and cannot be untracked")
	}
	if !b.Tracked {
		return fmt.Errorf("branch %q is not tracked by stk", name)
	}

	targets := []*stack.Branch{b}
	switch {
	case len(b.Children) == 0:
		// Nothing to decide.
	case opts.Recursive && opts.Reparent != "":
		return errors.New("use either --recursive or --reparent, not both")
	case opts.Recursive:
		targets = stack.Subtree(b)
	case opts.Reparent != "":
		newParent, err := resolveParent(g, opts.Reparent)
		if err != nil {
			return err
		}
		if stack.IsDescendant(b, newParent) {
			return fmt.Errorf("%s is below %s, so it cannot become the new parent", opts.Reparent, name)
		}
		if env.DryRun {
			env.Out.Printf("Would reparent %d branch(es) onto %s and untrack %s", len(b.Children), newParent.Name, b.Name)
			return nil
		}
		for _, child := range b.Children {
			if err := stack.SetParent(repo, child.Name, parentID(newParent)); err != nil {
				return err
			}
			env.Out.OK("Reparented %s onto %s", output.BranchName(child.Name), output.BranchName(newParent.Name))
		}
	default:
		var childNames []string
		for _, c := range b.Children {
			childNames = append(childNames, c.Name)
		}
		return fmt.Errorf(
			"%s has tracked children: %v\n\nChoose how to handle them:\n\n    stk untrack %s --recursive\n    stk untrack %s --reparent <branch>",
			name, childNames, name, name)
	}

	if env.DryRun {
		env.Out.Printf("Would untrack %d branch(es)", len(targets))
		return nil
	}
	// Untrack deepest first so the graph never contains a dangling parent.
	for i := len(targets) - 1; i >= 0; i-- {
		t := targets[i]
		if err := stack.ClearMeta(repo, t.Name); err != nil {
			return err
		}
		if err := stack.ClearBase(repo, t.ID); err != nil {
			return err
		}
		env.Out.OK("Untracked %s (git branch left in place)", output.BranchName(t.Name))
	}
	var names []string
	for _, t := range targets {
		names = append(names, t.Name)
	}
	mirrorGHStack(env, ghSyncOptions{Untracked: names})
	return nil
}

// RenameOptions configures stk rename.
type RenameOptions struct {
	// Yes answers every question this command would ask, including the one
	// about the remote branch.
	Yes bool
	// Remote renames the remote counterpart without asking.
	Remote bool
	// NoRemote leaves the remote alone without asking.
	NoRemote bool
}

// Rename renames a git branch and, when asked, the remote branch it publishes
// to. Stack relationships survive because they are keyed by stable ids, not
// names.
func Rename(env *Env, g *stack.Graph, oldName, newName string, opts RenameOptions) error {
	repo := env.Repo
	if opts.Remote && opts.NoRemote {
		return errors.New("use either --remote or --no-remote, not both")
	}
	b, ok := g.Resolve(oldName)
	if !ok {
		return fmt.Errorf("branch %q does not exist", oldName)
	}
	if !repo.ValidBranchName(newName) {
		return fmt.Errorf("%q is not a valid branch name", newName)
	}
	if repo.BranchExists(newName) {
		return fmt.Errorf("branch %q already exists", newName)
	}
	if repo.RebaseInProgress() {
		return errors.New("a rebase is in progress in this worktree; finish or abort it first")
	}
	if op, err := LoadOperation(repo); err == nil {
		return &ErrOperationInProgress{Op: op}
	} else if !errors.Is(err, ErrNoOperation) {
		return err
	}

	// Decided before the local rename, so a refusal costs nothing and the
	// question is asked while the old name is still the branch's own.
	target, moveRemote, err := resolveRemoteRename(env, b, newName, opts)
	if err != nil {
		return err
	}

	if env.DryRun {
		env.Out.Printf("Would rename %s -> %s", oldName, newName)
		if moveRemote {
			env.Out.Printf("Would rename %s -> %s/%s", target, target.Remote, newName)
		}
		return nil
	}
	if err := repo.RenameBranch(oldName, newName); err != nil {
		return err
	}
	env.Out.Printf("%s", output.Heading("Renamed:"))
	env.Out.Printf("    %s -> %s", output.Dim(oldName), output.BranchName(newName))
	// gh stack keys its tracking by name, so the rename is what it most
	// needs to hear about.
	mirrorGHStack(env, ghSyncOptions{Renames: map[string]string{oldName: newName}})

	if moveRemote {
		res := repo.RenameRemoteBranch(target.Remote, target.Name, newName)
		if !res.OK() {
			echoGit(env, res)
			return fmt.Errorf(
				"the local branch is now %s, but renaming %s failed\n\n"+
					"Finish it by hand:\n\n    git push -u %s %s\n    git push %s --delete %s",
				newName, target, target.Remote, newName, target.Remote, target.Name)
		}
		env.Out.Printf("    %s -> %s/%s", target, target.Remote, newName)
		return nil
	}
	if target.Name != "" {
		// A local rename says nothing about the remote; stk never touches it
		// unless it was asked to.
		env.Out.Printf("")
		env.Out.Printf("%s", output.Heading("Remote branch remains:"))
		env.Out.Printf("    %s", target)
		env.Out.Printf("")
		env.Out.Printf("%s", output.Heading("To publish the renamed branch:"))
		env.Out.Printf("")
		env.Out.Printf("    %s", output.Command(fmt.Sprintf("git push -u %s %s", target.Remote, newName)))
		env.Out.Printf("")
		env.Out.Printf("%s", output.Heading("To remove the previous remote branch:"))
		env.Out.Printf("")
		env.Out.Printf("    %s", output.Command(fmt.Sprintf("git push %s --delete %s", target.Remote, target.Name)))
	}
	return nil
}

// resolveRemoteRename decides whether the remote branch moves with the local
// one. The returned branch is the remote counterpart stk found, whatever the
// answer, so the caller can describe what it left behind.
func resolveRemoteRename(env *Env, b *stack.Branch, newName string, opts RenameOptions) (remoteBranch, bool, error) {
	target, ok := remoteCounterpart(env, b)
	if !ok {
		return remoteBranch{}, false, nil
	}
	if opts.NoRemote {
		return target, false, nil
	}
	// Renaming onto a remote branch someone else is using would replace their
	// work with this one under a lease stk never took.
	if _, taken := env.Repo.RemoteBranchSHA(target.Remote, newName); taken {
		if opts.Remote {
			return target, false, fmt.Errorf("%s/%s already exists, so stk will not rename %s onto it", target.Remote, newName, target)
		}
		env.Out.Warnf("%s/%s already exists, so the remote branch is left alone.", target.Remote, newName)
		return target, false, nil
	}
	if opts.Remote || opts.Yes {
		return target, true, nil
	}
	if env.Confirm == nil {
		return target, false, nil
	}
	env.Out.Printf("")
	env.Out.Printf("%s publishes to %s.", output.BranchName(b.Name), target)
	env.Out.Printf("")
	env.Out.Printf("GitHub closes any open pull request whose head branch is deleted, so a")
	env.Out.Printf("pull request open for %s will not survive the rename.", target.Name)
	env.Out.Printf("")
	yes, err := env.Confirm(fmt.Sprintf("Rename %s to %s/%s as well?", target, target.Remote, newName), false)
	if err != nil {
		return target, false, err
	}
	return target, yes, nil
}

// Move re-parents a branch and restacks it and its descendants onto the new
// parent.
func Move(env *Env, g *stack.Graph, name, ontoName string) error {
	repo := env.Repo
	b, ok := g.Resolve(name)
	if !ok {
		return fmt.Errorf("branch %q does not exist", name)
	}
	if b.IsTrunk || !b.Tracked {
		return fmt.Errorf("branch %q is not tracked by stk", name)
	}
	newParent, err := resolveParent(g, ontoName)
	if err != nil {
		return err
	}
	if newParent == b {
		return errors.New("a branch cannot be its own parent")
	}
	if stack.IsDescendant(b, newParent) {
		return fmt.Errorf("%s is below %s in the stack; moving it there would create a cycle", ontoName, name)
	}
	if b.Parent == newParent {
		env.Out.Printf("%s is already on %s.", name, ontoName)
		return nil
	}
	if env.DryRun {
		env.Out.Printf("Would move %s onto %s and restack its descendants", name, ontoName)
		return nil
	}
	// Check both before touching metadata: a half-moved branch would leave the
	// graph describing a rebase that never happened.
	if err := requireNoOperation(env); err != nil {
		return err
	}
	// Move rewrites metadata before it rebases, so it parks the working tree
	// itself and hands the stash to the restack that follows.
	stash, err := Stash(env, "move")
	if err != nil {
		return err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			stash.Restore(env)
		}
	}()
	if err := requireCleanTree(env); err != nil {
		return err
	}
	if err := stack.SetParent(repo, b.Name, parentID(newParent)); err != nil {
		return err
	}
	env.Out.OK("Parent of %s is now %s", output.BranchName(b.Name), output.BranchName(newParent.Name))
	// Recorded now rather than only after the restack, which may stop on a
	// conflict: the new parent is a fact whether or not the rebase is done.
	mirrorGHStack(env, ghSyncOptions{})

	// Reload so the graph reflects the new parent before restacking.
	fresh, err := stack.Load(repo, env.Cfg)
	if err != nil {
		return err
	}
	target := fresh.ByID[b.ID]
	if target == nil {
		return fmt.Errorf("branch %q disappeared during move", name)
	}
	handedOver = true
	_, err = Restack(env, fresh, target, RestackOptions{
		Scope:       ScopeUp,
		Heading:     fmt.Sprintf("Restacking from %s...", target.Name),
		DoneMessage: "Upstack is up to date.",
		Stash:       stash,
	})
	return err
}
