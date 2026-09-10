package operations

import (
	"errors"
	"fmt"

	"stk/internal/git"
	"stk/internal/stack"
)

// FoldOptions configures stk fold.
type FoldOptions struct {
	// Into names the branch that survives the fold. Empty means the target's
	// own parent, which is the smallest useful fold.
	Into string
	// Stack folds the whole stack into its lowest branch above trunk.
	Stack bool
	// Yes skips the confirmation.
	Yes bool
	// ClosePulls closes the pull requests of the folded branches.
	ClosePulls bool
}

// Fold collapses a run of stacked branches into the lowest one.
//
// The commits are not touched: in a consistent stack the top branch already
// contains every commit below it, so folding is a matter of moving the
// surviving branch's ref up to the top of the run, adopting the children, and
// dropping the branches in between. Nothing is squashed and nothing is
// rebased, which is why a fold can be this cheap and this safe.
func Fold(env *Env, g *stack.Graph, target *stack.Branch, opts FoldOptions) error {
	repo := env.Repo
	if err := requireNoOperation(env); err != nil {
		return err
	}
	survivor, folded, err := foldPlan(g, target, opts)
	if err != nil {
		return err
	}
	top := folded[len(folded)-1]

	// Every folded branch must already be contained in the top of the run, or
	// folding would drop commits on the floor.
	if err := checkContained(repo, survivor, folded, top); err != nil {
		return err
	}
	// Refs of branches held elsewhere are not stk's to move or delete.
	for _, b := range append([]*stack.Branch{survivor}, folded...) {
		if b.CheckedOutElsewhere() {
			return fmt.Errorf("%s is checked out in:\n\n    %s\n\nFold from that worktree, or close it first", b.Name, b.Worktree)
		}
	}

	adopted := adoptees(survivor, folded)
	printFoldPlan(env, repo, survivor, folded, adopted, top)
	if env.DryRun {
		env.Out.Printf("")
		env.Out.Printf("No changes have been made.")
		return nil
	}
	if !opts.Yes && env.Confirm != nil {
		ok, err := env.Confirm(fmt.Sprintf("Fold %d branch(es) into %s?", len(folded), survivor.Name), true)
		if err != nil {
			return err
		}
		if !ok {
			env.Out.Printf("Leaving the stack as it is.")
			return nil
		}
	}

	// A fold moves the surviving branch's ref, so the working tree must not be
	// sitting on something about to be deleted with changes in it.
	stash, err := Stash(env, "fold")
	if err != nil {
		return err
	}
	defer stash.Restore(env)
	if err := requireCleanTree(env); err != nil {
		return err
	}

	// Stand on the survivor: the branches under our feet are about to go.
	if repo.CurrentBranch() != survivor.Name {
		if err := repo.Switch(survivor.Name); err != nil {
			return err
		}
	}
	if res := repo.R.Capture("merge", "--ff-only", top.SHA); !res.OK() {
		echoGit(env, res)
		return fmt.Errorf("cannot fast-forward %s to %s", survivor.Name, top.Name)
	}
	survivor.SHA = top.SHA
	env.Out.OK("%s now ends at %s", survivor.Name, git.ShortSHA(top.SHA))

	for _, child := range adopted {
		if err := stack.SetParent(repo, child.Name, parentID(survivor)); err != nil {
			return err
		}
		env.Out.OK("Reparented %s onto %s", child.Name, survivor.Name)
	}

	// Deepest first, so the graph never points at a branch that is gone.
	for i := len(folded) - 1; i >= 0; i-- {
		b := folded[i]
		if err := stack.ClearBase(repo, b.ID); err != nil {
			return err
		}
		if err := stack.ClearMeta(repo, b.Name); err != nil {
			return err
		}
		// -D is justified: the commits are all in the survivor now.
		if err := repo.DeleteBranch(b.Name, true); err != nil {
			return err
		}
		env.Out.OK("Folded and deleted %s (was %s)", b.Name, git.ShortSHA(b.SHA))
	}

	if opts.ClosePulls {
		if err := closeFoldedPulls(env, g, survivor, folded); err != nil {
			return err
		}
	}

	env.Out.Printf("")
	env.Out.Printf("%d branch(es) folded into %s.", len(folded), survivor.Name)
	printFoldAftermath(env, survivor, folded, opts)
	return nil
}

// foldPlan works out which branch survives and which ones fold into it,
// bottom first.
func foldPlan(g *stack.Graph, target *stack.Branch, opts FoldOptions) (*stack.Branch, []*stack.Branch, error) {
	if target == nil {
		return nil, nil, errNoCurrentBranch
	}
	if opts.Into != "" && opts.Stack {
		return nil, nil, errors.New("use either --into or --stack, not both")
	}
	if target.IsTrunk {
		return nil, nil, fmt.Errorf("%s is the trunk branch; check out or name a branch stacked on it", target.Name)
	}
	if !target.Tracked {
		return nil, nil, fmt.Errorf("branch %q is not tracked by stk, so it is not part of a stack", target.Name)
	}
	if target.HasProblem() {
		return nil, nil, fmt.Errorf("%s has a metadata problem: %s", target.Name, problemText(target))
	}

	switch {
	case opts.Stack:
		root := g.Root(target)
		if root == nil {
			return nil, nil, fmt.Errorf("cannot determine the stack containing %s", target.Name)
		}
		leaves := stack.Leaves(root)
		if len(leaves) != 1 {
			return nil, nil, fmt.Errorf(
				"the stack rooted at %s branches, so there is no single branch to fold it into\n\nFold one run at a time with stk fold --into <branch>",
				root.Name)
		}
		return chainBetween(root, leaves[0])
	case opts.Into != "":
		into, ok := g.Resolve(opts.Into)
		if !ok {
			return nil, nil, fmt.Errorf("branch %q does not exist", opts.Into)
		}
		if into == target {
			return nil, nil, fmt.Errorf("%s is already the branch being folded into", into.Name)
		}
		if into.IsTrunk {
			return nil, nil, fmt.Errorf(
				"%s is trunk, and stk never folds a stack into it\n\nFold into the lowest branch instead:\n\n    stk fold --stack",
				into.Name)
		}
		if !stack.IsDescendant(into, target) {
			return nil, nil, fmt.Errorf("%s is not stacked above %s, so it cannot be folded into it", target.Name, into.Name)
		}
		return chainBetween(into, target)
	default:
		parent := target.Parent
		if parent == nil {
			return nil, nil, fmt.Errorf("%s has no resolvable parent to fold into", target.Name)
		}
		if parent.IsTrunk {
			return nil, nil, fmt.Errorf(
				"%s sits directly on trunk, so there is nothing below it to fold into",
				target.Name)
		}
		return parent, []*stack.Branch{target}, nil
	}
}

// chainBetween returns the survivor and the branches from just above it up to
// top, refusing anything that is not a single line of descent.
func chainBetween(survivor, top *stack.Branch) (*stack.Branch, []*stack.Branch, error) {
	var chain []*stack.Branch
	for b := top; b != nil && b != survivor; b = b.Parent {
		if b.HasProblem() {
			return nil, nil, fmt.Errorf("%s has a metadata problem: %s", b.Name, problemText(b))
		}
		chain = append([]*stack.Branch{b}, chain...)
	}
	if len(chain) == 0 {
		return nil, nil, fmt.Errorf("%s is not stacked above %s", top.Name, survivor.Name)
	}
	return survivor, chain, nil
}

// adoptees lists the branches that must be re-parented onto the survivor:
// everything hanging off a folded branch that is not itself being folded.
func adoptees(survivor *stack.Branch, folded []*stack.Branch) []*stack.Branch {
	inFold := map[string]bool{}
	for _, b := range folded {
		inFold[b.ID] = true
	}
	var out []*stack.Branch
	for _, b := range folded {
		for _, child := range b.Children {
			if !inFold[child.ID] {
				out = append(out, child)
			}
		}
	}
	return out
}

// checkContained proves that folding loses nothing.
func checkContained(repo *git.Repo, survivor *stack.Branch, folded []*stack.Branch, top *stack.Branch) error {
	if top.SHA == "" {
		return fmt.Errorf("%s has no commits", top.Name)
	}
	if survivor.SHA != "" && !repo.IsAncestor(survivor.SHA, top.SHA) {
		return fmt.Errorf(
			"%s does not contain %s, so folding would rewrite history\n\nRestack the stack first:\n\n    stk restack",
			top.Name, survivor.Name)
	}
	for _, b := range folded {
		if b.SHA == "" || repo.IsAncestor(b.SHA, top.SHA) {
			continue
		}
		return fmt.Errorf(
			"%s is not contained in %s, so folding would drop its commits\n\nRestack the stack first:\n\n    stk restack",
			b.Name, top.Name)
	}
	return nil
}

// printFoldPlan says exactly what is about to disappear, before anything does.
func printFoldPlan(env *Env, repo *git.Repo, survivor *stack.Branch, folded, adopted []*stack.Branch, top *stack.Branch) {
	p := env.Out
	prefix := ""
	if env.DryRun {
		prefix = "(dry-run) "
	}
	p.Printf("%sFolding into %s:", prefix, survivor.Name)
	p.Printf("")
	width := 0
	for _, b := range folded {
		if len(b.Name) > width {
			width = len(b.Name)
		}
	}
	for _, b := range folded {
		// Each branch's own commits, counted from its own base, not from the
		// survivor: the run's totals are on the line below.
		p.Printf("    %-*s   %d commit(s), deleted", width, b.Name, repo.CountCommits(b.Base, b.SHA))
	}
	p.Printf("")
	p.Printf("%s keeps its own base and ends up with %d commit(s).",
		survivor.Name, repo.CountCommits(survivor.Base, top.SHA))
	for _, child := range adopted {
		p.Printf("%s is reparented onto %s.", child.Name, survivor.Name)
	}
	p.Printf("")
}

// printFoldAftermath explains what the fold left for the user to do.
func printFoldAftermath(env *Env, survivor *stack.Branch, folded []*stack.Branch, opts FoldOptions) {
	p := env.Out
	p.Printf("")
	p.Printf("Recover a folded branch with:")
	p.Printf("")
	for _, b := range folded {
		p.Printf("    git branch %s %s", b.Name, git.ShortSHA(b.SHA))
	}
	p.Printf("")
	p.Printf("Publish the fold with:")
	p.Printf("")
	p.Printf("    stk submit")
	if !opts.ClosePulls {
		p.Printf("")
		p.Printf("Pull requests for the folded branches, if any, are still open. Close them")
		p.Printf("with stk fold --close-pulls next time, or by hand.")
	}
}

// closeFoldedPulls closes the pull requests of the branches that are gone,
// pointing each one at the branch that absorbed it.
//
// The remote branches are left alone: stk deletes nothing it did not create,
// and a closed pull request is recoverable while a deleted branch is not.
func closeFoldedPulls(env *Env, g *stack.Graph, survivor *stack.Branch, folded []*stack.Branch) error {
	remote := env.Cfg.Remote
	if remote == "" {
		return errors.New("no default remote is configured, so stk cannot reach the pull requests")
	}
	gh, err := openForge(env, remote)
	if err != nil {
		return err
	}
	prs := newPullRequestCache()
	into := survivor.Name
	if pr, err := prs.open(gh, survivor.Name); err == nil && pr != nil {
		into = fmt.Sprintf("#%d", pr.Number)
	}
	closed := 0
	for _, b := range folded {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return err
		}
		if pr == nil {
			continue
		}
		comment := fmt.Sprintf("Folded into %s; the commits are all there.", into)
		if err := gh.ClosePullRequest(pr.Number, comment); err != nil {
			return err
		}
		env.Out.OK("Closed %s (%s)", pr, b.Name)
		closed++
	}
	if closed == 0 {
		env.Out.Skip("no pull request was open for the folded branches")
	}
	return nil
}
