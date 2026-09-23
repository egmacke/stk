package operations

import (
	"errors"
	"fmt"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// TriageAction is what to do with one untracked branch.
type TriageAction int

const (
	// TriageSkip leaves the branch as it is.
	TriageSkip TriageAction = iota
	// TriageTrack brings the branch into the stack.
	TriageTrack
	// TriageDelete removes the local branch.
	TriageDelete
	// TriageQuit stops before the next branch, leaving the rest alone.
	TriageQuit
)

// TriageQuestion describes one branch to the command layer, which asks what
// to do with it.
type TriageQuestion struct {
	Branch *stack.Branch
	// Parent is the branch it would be tracked onto.
	Parent string
	// Remote is the remote branch it publishes to, when HasRemote is set.
	Remote    string
	HasRemote bool
	// CanDelete is whether deleting is on offer: only for a branch nobody
	// else can see, and that no other worktree has checked out.
	CanDelete bool
}

// TriageAsk is how the command layer collects the answer for one branch.
type TriageAsk func(q TriageQuestion) (TriageAction, error)

// TriageOptions configures stk triage.
type TriageOptions struct {
	// Parent is where tracked branches go; trunk when empty.
	Parent string
	// Ask collects each answer. stk triage cannot run without it.
	Ask TriageAsk
}

// Triage walks every local branch stk does not track and asks what to do with
// each one in turn.
//
// A branch the remote also has is someone's published work, so it may only be
// tracked or left alone; a branch that exists nowhere but here may also be
// deleted. Each answer is carried out before the next branch is shown, so
// quitting part way keeps everything decided so far.
func Triage(env *Env, g *stack.Graph, opts TriageOptions) error {
	if err := requireNoOperation(env); err != nil {
		return err
	}
	parent := opts.Parent
	if parent == "" {
		parent = env.Cfg.Trunk
	}
	if err := CanBeParent(g, parent); err != nil {
		return err
	}

	var names []string
	for _, b := range g.Untracked {
		names = append(names, b.Name)
	}
	if len(names) == 0 {
		env.Out.Printf("Every local branch is already tracked.")
		return nil
	}
	if opts.Ask == nil {
		return errors.New("stk triage asks about each branch, and stk cannot ask here\n\n" +
			"Run it from a terminal, or answer on stdin with --interactive")
	}
	env.Out.Printf("%s", output.Heading(fmt.Sprintf("%d untracked branch(es):", len(names))))

	var tracked, deleted, skipped, failed int
	for i, name := range names {
		b, ok := g.Resolve(name)
		if !ok || b.Tracked {
			continue
		}
		remote, hasRemote := remoteCounterpart(env, b)
		lost := unreachableCommits(env.Repo, b, survivingTips(g, []*stack.Branch{b}))
		q := TriageQuestion{
			Branch:    b,
			Parent:    parent,
			HasRemote: hasRemote,
			CanDelete: !hasRemote && !b.CheckedOutElsewhere(),
		}
		if hasRemote {
			q.Remote = remote.String()
		}
		env.Out.Printf("")
		printTriageBranch(env, g, q, i+1, len(names), lost)

		action, err := opts.Ask(q)
		if err != nil {
			return err
		}
		if action == TriageDelete && !q.CanDelete {
			// The command layer never offers it; refuse rather than trust that.
			return fmt.Errorf("%s cannot be deleted here", name)
		}
		if action == TriageQuit {
			skipped += len(names) - i
			break
		}

		switch action {
		case TriageSkip:
			env.Out.Skip("Left %s untracked", output.BranchName(name))
			skipped++
			continue
		case TriageTrack:
			err = Track(env, g, name, parent)
		case TriageDelete:
			var done bool
			done, err = triageDelete(env, g, b, lost)
			if err == nil && !done {
				skipped++
				continue
			}
		}
		if err != nil {
			// One branch that will not go where it was sent is no reason to
			// stop asking about the others.
			env.Out.Fail("%s: %v", name, err)
			failed++
			continue
		}
		if action == TriageTrack {
			tracked++
		} else {
			deleted++
		}
		if !env.DryRun {
			// Later answers are judged against what is left: a deleted branch
			// no longer holds anyone's commits.
			if g, err = stack.Load(env.Repo, env.Cfg); err != nil {
				return err
			}
		}
	}

	env.Out.Printf("")
	if env.DryRun {
		env.Out.Printf("Would track %d, delete %d and leave %d untracked.", tracked, deleted, skipped)
	} else {
		env.Out.Printf("Tracked %d, deleted %d, left %d untracked.", tracked, deleted, skipped)
	}
	if failed > 0 {
		return fmt.Errorf("%d branch(es) could not be handled", failed)
	}
	return nil
}

// printTriageBranch says what stk knows about a branch before asking about it.
func printTriageBranch(env *Env, g *stack.Graph, q TriageQuestion, n, total int, lost int) {
	p := env.Out
	b := q.Branch
	p.Printf("%s %s %s", output.Dim(fmt.Sprintf("[%d/%d]", n, total)), output.BranchName(b.Name), output.Dim(git.ShortSHA(b.SHA)))
	switch {
	case q.HasRemote:
		// The remote holds its commits, so what only this clone has is beside
		// the point.
		p.Printf("    on the remote as %s", q.Remote)
	case lost > 0:
		p.Printf("    not on the remote, with %d commit(s) kept nowhere else", lost)
	default:
		p.Printf("    not on the remote, with nothing that %s or another branch lacks", g.Trunk.Name)
	}
	if b.CheckedOutElsewhere() {
		p.Printf("    checked out in %s", b.Worktree)
	}
}

// triageDelete removes one untracked branch that exists only here. A branch
// that would take commits with it is asked about a second time, defaulting to
// no, since the first answer was given among several.
func triageDelete(env *Env, g *stack.Graph, b *stack.Branch, lost int) (bool, error) {
	if lost > 0 {
		if env.Confirm == nil {
			return false, errors.New("stk cannot confirm deleting commits kept nowhere else")
		}
		ok, err := env.Confirm(fmt.Sprintf("%s has %d commit(s) kept nowhere else. Delete it anyway?", b.Name, lost), false)
		if err != nil {
			return false, err
		}
		if !ok {
			env.Out.Skip("Kept %s", output.BranchName(b.Name))
			return false, nil
		}
	}
	if env.DryRun {
		env.Out.Dry("would delete %s", output.BranchName(b.Name))
		return true, nil
	}
	doomed := []*stack.Branch{b}
	if err := stepOffDoomedBranch(env, g, doomed); err != nil {
		return false, err
	}
	if err := dropBranch(env, b); err != nil {
		return false, err
	}
	env.Out.OK("Deleted %s (was %s)", output.BranchName(b.Name), git.ShortSHA(b.SHA))
	if lost > 0 {
		env.Out.Printf("    Recover it with: %s",
			output.Command(fmt.Sprintf("git branch %s %s", b.Name, git.ShortSHA(b.SHA))))
	}
	return true, nil
}
