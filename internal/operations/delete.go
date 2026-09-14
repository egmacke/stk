package operations

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// DeleteOptions configures stk delete.
type DeleteOptions struct {
	// Yes answers every question this command would ask, including the one
	// about the remote branch.
	Yes bool
	// Remote deletes the remote counterpart without asking.
	Remote bool
	// NoRemote leaves the remote alone without asking.
	NoRemote bool
}

// Delete removes local branches and, when asked, their remote counterparts.
//
// Branches stacked above a deleted one are re-parented onto the nearest
// ancestor that survives, so the rest of the graph keeps its shape. Nothing is
// rebased: the survivors are left needing a restack rather than rewritten
// behind the user's back.
func Delete(env *Env, g *stack.Graph, names []string, opts DeleteOptions) error {
	repo := env.Repo
	if opts.Remote && opts.NoRemote {
		return errors.New("use either --remote or --no-remote, not both")
	}
	if err := requireNoOperation(env); err != nil {
		return err
	}
	doomed, err := deletePlan(g, names)
	if err != nil {
		return err
	}

	// What survives decides what is lost: a branch whose commits are still
	// reachable from trunk or from a branch that stays has nothing to lose.
	keepers := survivingTips(g, doomed)
	lost := map[string]int{}
	for _, b := range doomed {
		lost[b.Name] = unreachableCommits(repo, b, keepers)
	}
	adopted := reparented(g, doomed)

	printDeletePlan(env, g, doomed, adopted, lost)
	if env.DryRun {
		env.Out.Printf("")
		env.Out.Printf("No changes have been made.")
		return nil
	}

	// A branch that would lose commits is never the default answer, however
	// explicitly it was named.
	safe := true
	for _, n := range lost {
		if n > 0 {
			safe = false
		}
	}
	if !opts.Yes {
		if env.Confirm == nil {
			return errors.New("stk cannot ask whether to delete these branches\n\nRe-run with:\n\n    stk delete --yes")
		}
		env.Out.Printf("")
		ok, err := env.Confirm(fmt.Sprintf("Delete %d branch(es)?", len(doomed)), safe)
		if err != nil {
			return err
		}
		if !ok {
			env.Out.Printf("Leaving them in place.")
			return nil
		}
	}

	// Asked before anything is deleted, so the whole run is decided up front
	// rather than one question at a time between destructive steps.
	remotes, err := resolveRemoteDeletes(env, doomed, opts)
	if err != nil {
		return err
	}

	env.Out.Printf("")
	if err := stepOffDoomedBranch(env, g, doomed); err != nil {
		return err
	}
	if err := reparentSurvivors(env, adopted); err != nil {
		return err
	}

	for _, b := range doomed {
		if err := dropBranch(env, b); err != nil {
			return err
		}
		env.Out.OK("Deleted %s (was %s)", output.BranchName(b.Name), git.ShortSHA(b.SHA))
	}

	var failed []string
	for _, b := range doomed {
		target, ok := remotes[b.Name]
		if !ok {
			continue
		}
		if err := deleteRemoteBranch(env, target); err != nil {
			env.Out.Fail("%v", err)
			failed = append(failed, target.String())
			continue
		}
		env.Out.OK("Deleted %s", target)
	}

	printDeleteAftermath(env, doomed, lost)
	if len(failed) > 0 {
		return fmt.Errorf("could not delete %s on the remote", strings.Join(failed, ", "))
	}
	return nil
}

// deletePlan resolves the named branches and refuses the ones stk must not
// touch. The result is ordered deepest first, so the graph never points at a
// branch that is already gone.
func deletePlan(g *stack.Graph, names []string) ([]*stack.Branch, error) {
	if len(names) == 0 {
		return nil, errNoCurrentBranch
	}
	seen := map[string]bool{}
	var out []*stack.Branch
	for _, name := range names {
		b, ok := g.Resolve(name)
		if !ok {
			return nil, fmt.Errorf("branch %q does not exist", name)
		}
		if b.IsTrunk {
			return nil, fmt.Errorf("%s is the trunk branch; stk never deletes it", b.Name)
		}
		if b.CheckedOutElsewhere() {
			return nil, fmt.Errorf(
				"%s is checked out in:\n\n    %s\n\nDelete it from that worktree, or close it first",
				b.Name, b.Worktree)
		}
		if seen[b.Name] {
			continue
		}
		seen[b.Name] = true
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return depth(out[i]) > depth(out[j]) })
	return out, nil
}

// depth is how far a branch sits above trunk, used to delete the top of a run
// before the branches holding it up.
func depth(b *stack.Branch) int {
	n := 0
	seen := map[*stack.Branch]bool{b: true}
	for cur := b.Parent; cur != nil && !cur.IsTrunk && !seen[cur]; cur = cur.Parent {
		seen[cur] = true
		n++
	}
	return n
}

// doomedNames indexes a delete set by branch name, which every branch has;
// untracked branches carry no stk id.
func doomedNames(doomed []*stack.Branch) map[string]bool {
	out := make(map[string]bool, len(doomed))
	for _, b := range doomed {
		out[b.Name] = true
	}
	return out
}

// survivingTips lists the commits that will still be reachable from a named
// ref once the delete is done: trunk, and every branch that stays.
func survivingTips(g *stack.Graph, doomed []*stack.Branch) []string {
	gone := doomedNames(doomed)
	var out []string
	if g.Trunk.SHA != "" {
		out = append(out, g.Trunk.SHA)
	}
	for _, b := range g.ByName {
		if b.IsTrunk || gone[b.Name] || b.SHA == "" {
			continue
		}
		out = append(out, b.SHA)
	}
	return out
}

// unreachableCommits counts the commits a branch would take with it: none when
// something that survives still holds them.
func unreachableCommits(repo *git.Repo, b *stack.Branch, keepers []string) int {
	if b.SHA == "" || len(keepers) == 0 {
		return 0
	}
	return repo.CountUniqueCommits(b.SHA, keepers)
}

// reparented lists the tracked branches that must move, paired with the
// ancestor they move onto: everything hanging off a doomed branch that is not
// itself doomed.
type adoption struct {
	Child     *stack.Branch
	NewParent *stack.Branch
}

func reparented(g *stack.Graph, doomed []*stack.Branch) []adoption {
	gone := doomedNames(doomed)
	var out []adoption
	for _, b := range g.Tracked {
		if gone[b.Name] || b.HasProblem() || b.Parent == nil || !gone[b.Parent.Name] {
			continue
		}
		ancestor := b.Parent
		for ancestor != nil && !ancestor.IsTrunk && gone[ancestor.Name] {
			ancestor = ancestor.Parent
		}
		if ancestor == nil {
			ancestor = g.Trunk
		}
		out = append(out, adoption{Child: b, NewParent: ancestor})
	}
	return out
}

// reparentSurvivors records the new parents worked out by reparented.
func reparentSurvivors(env *Env, adopted []adoption) error {
	for _, a := range adopted {
		if err := stack.SetParent(env.Repo, a.Child.Name, parentID(a.NewParent)); err != nil {
			return err
		}
		env.Out.OK("Reparented %s onto %s", output.BranchName(a.Child.Name), output.BranchName(a.NewParent.Name))
	}
	return nil
}

// dropBranch removes one branch and the stk state that describes it.
//
// -D rather than -d: whether the commits are safe is the caller's decision,
// already made and already reported, and git's own merge check would refuse
// branches the caller has proved are fine.
func dropBranch(env *Env, b *stack.Branch) error {
	if b.Tracked && b.ID != "" {
		if err := stack.ClearBase(env.Repo, b.ID); err != nil {
			return err
		}
	}
	if err := env.Repo.DeleteBranch(b.Name, true); err != nil {
		return err
	}
	// git removes the branch config section on delete, but clear it
	// explicitly in case an older git left it behind.
	return stack.ClearMeta(env.Repo, b.Name)
}

// stepOffDoomedBranch moves this worktree onto a branch that will still exist,
// because git will not delete the branch HEAD points at.
func stepOffDoomedBranch(env *Env, g *stack.Graph, doomed []*stack.Branch) error {
	gone := doomedNames(doomed)
	current := env.Repo.CurrentBranch()
	if current == "" || !gone[current] {
		return nil
	}
	landing := g.Trunk
	if b, ok := g.Resolve(current); ok {
		for cur := b.Parent; cur != nil; cur = cur.Parent {
			if !gone[cur.Name] {
				landing = cur
				break
			}
		}
	}
	if landing == nil || landing.SHA == "" {
		return fmt.Errorf("%s is checked out here and stk has nowhere to move to; check out another branch first", current)
	}
	return Switch(env, landing.Name)
}

// remoteBranch names one branch on one remote.
type remoteBranch struct {
	Remote string
	Name   string
}

func (r remoteBranch) String() string { return r.Remote + "/" + r.Name }

// remoteCounterpart returns the remote branch a local branch publishes to, if
// stk knows of one that still exists.
func remoteCounterpart(env *Env, b *stack.Branch) (remoteBranch, bool) {
	if b.UpstreamGone {
		// The last fetch proved it is already gone.
		return remoteBranch{}, false
	}
	if b.Upstream != "" {
		remote, name, ok := strings.Cut(b.Upstream, "/")
		if ok && remote != "" && name != "" {
			return remoteBranch{Remote: remote, Name: name}, true
		}
	}
	remote := env.Cfg.Remote
	if remote == "" || !env.Repo.RemoteExists(remote) {
		return remoteBranch{}, false
	}
	if _, ok := env.Repo.RemoteBranchSHA(remote, b.Name); !ok {
		return remoteBranch{}, false
	}
	return remoteBranch{Remote: remote, Name: b.Name}, true
}

// resolveRemoteDeletes decides which remote branches go with the local ones.
//
// Deleting a remote branch is the one thing here that other people can see, so
// it is asked separately from the local delete and never assumed.
func resolveRemoteDeletes(env *Env, doomed []*stack.Branch, opts DeleteOptions) (map[string]remoteBranch, error) {
	out := map[string]remoteBranch{}
	if opts.NoRemote {
		return out, nil
	}
	candidates := map[string]remoteBranch{}
	var listed []string
	for _, b := range doomed {
		target, ok := remoteCounterpart(env, b)
		if !ok {
			continue
		}
		candidates[b.Name] = target
		listed = append(listed, target.String())
	}
	if len(candidates) == 0 {
		return out, nil
	}
	if opts.Remote || opts.Yes {
		return candidates, nil
	}
	// Confirm is never nil here: without it the local delete above would
	// already have refused for want of an answer.
	env.Out.Printf("")
	env.Out.Printf("%s", output.Heading("These branches also exist on the remote:"))
	env.Out.Printf("")
	for _, name := range listed {
		env.Out.Printf("    %s", output.BranchName(name))
	}
	env.Out.Printf("")
	env.Out.Printf("GitHub closes any open pull request whose head branch is deleted.")
	env.Out.Printf("")
	ok, err := env.Confirm(fmt.Sprintf("Delete %d remote branch(es) as well?", len(candidates)), false)
	if err != nil {
		return nil, err
	}
	if !ok {
		env.Out.Printf("Leaving the remote branches in place.")
		return out, nil
	}
	return candidates, nil
}

// deleteRemoteBranch removes one branch from a remote, treating one that is
// already gone as the success it is.
func deleteRemoteBranch(env *Env, target remoteBranch) error {
	if env.DryRun {
		env.Out.Dry("would delete %s", target)
		return nil
	}
	res := env.Repo.DeleteRemoteBranch(target.Remote, target.Name)
	if res.OK() {
		return nil
	}
	if git.RemoteRefMissing(res) {
		env.Out.Skip("%s is already gone", target)
		return nil
	}
	echoGit(env, res)
	return fmt.Errorf("deleting %s failed", target)
}

// printDeletePlan says exactly what is about to disappear, before anything
// does.
func printDeletePlan(env *Env, g *stack.Graph, doomed []*stack.Branch, adopted []adoption, lost map[string]int) {
	p := env.Out
	prefix := ""
	if env.DryRun {
		prefix = output.Dim("(dry-run)") + " "
	}
	p.Printf("%s%s", prefix, output.Heading("Deleting:"))
	p.Printf("")
	width := 0
	for _, b := range doomed {
		if len(b.Name) > width {
			width = len(b.Name)
		}
	}
	for _, b := range doomed {
		note := fmt.Sprintf("nothing that %s or another branch lacks", g.Trunk.Name)
		if n := lost[b.Name]; n > 0 {
			note = fmt.Sprintf("%d commit(s) kept nowhere else", n)
		}
		p.Printf("    %-*s   %s", width, b.Name, note)
	}
	if len(adopted) > 0 {
		p.Printf("")
	}
	for _, a := range adopted {
		p.Printf("%s is reparented onto %s, and will need a restack.", a.Child.Name, a.NewParent.Name)
	}
}

// printDeleteAftermath leaves behind the one command that undoes this.
func printDeleteAftermath(env *Env, doomed []*stack.Branch, lost map[string]int) {
	var recoverable []*stack.Branch
	for _, b := range doomed {
		if lost[b.Name] > 0 && b.SHA != "" {
			recoverable = append(recoverable, b)
		}
	}
	if len(recoverable) == 0 {
		return
	}
	p := env.Out
	p.Printf("")
	p.Printf("Recover a deleted branch with:")
	p.Printf("")
	for _, b := range recoverable {
		p.Printf("    git branch %s %s", b.Name, git.ShortSHA(b.SHA))
	}
}
