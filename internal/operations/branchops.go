package operations

import (
	"errors"
	"fmt"

	"stk/internal/stack"
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

	env.Out.OK("Created %s from %s", opts.Name, parent.Name)
	env.Out.OK("Parent: %s", parent.Name)
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
	env.Out.OK("Tracking %s with parent %s", child.Name, parent.Name)
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
			env.Out.OK("Reparented %s onto %s", child.Name, newParent.Name)
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
		env.Out.OK("Untracked %s (git branch left in place)", t.Name)
	}
	return nil
}

// Rename renames a git branch. Stack relationships survive because they are
// keyed by stable ids, not names.
func Rename(env *Env, g *stack.Graph, oldName, newName string) error {
	repo := env.Repo
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
	if env.DryRun {
		env.Out.Printf("Would rename %s -> %s", oldName, newName)
		return nil
	}
	if err := repo.RenameBranch(oldName, newName); err != nil {
		return err
	}
	env.Out.Printf("Renamed:")
	env.Out.Printf("    %s -> %s", oldName, newName)
	if b.HasUpstream() {
		// A local rename says nothing about the remote; stk never touches it.
		env.Out.Printf("")
		env.Out.Printf("Remote branch remains:")
		env.Out.Printf("    %s", b.Upstream)
		env.Out.Printf("")
		env.Out.Printf("To publish the renamed branch:")
		env.Out.Printf("")
		env.Out.Printf("    git push -u %s %s", remoteOf(b.Upstream, env.Cfg.Remote), newName)
		env.Out.Printf("")
		env.Out.Printf("To remove the previous remote branch:")
		env.Out.Printf("")
		env.Out.Printf("    git push %s --delete %s", remoteOf(b.Upstream, env.Cfg.Remote), oldName)
	}
	return nil
}

func remoteOf(upstream, fallback string) string {
	for i := 0; i < len(upstream); i++ {
		if upstream[i] == '/' {
			return upstream[:i]
		}
	}
	if fallback == "" {
		return "origin"
	}
	return fallback
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
	env.Out.OK("Parent of %s is now %s", b.Name, newParent.Name)

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
