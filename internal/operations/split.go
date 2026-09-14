package operations

import (
	"errors"
	"fmt"

	"stk/internal/git"
	"stk/internal/stack"
)

// SplitChoice is one split point: a commit that ends a segment, and the name
// of the branch that will hold it.
type SplitChoice struct {
	SHA  string
	Name string
}

// SplitPoints is how the command layer collects the choice. The commits run
// oldest first, which is the order they will be split in.
type SplitPoints func(branch string, commits []git.Commit) ([]SplitChoice, error)

// SplitOptions configures stk split.
type SplitOptions struct {
	// At names the commits to split after, as revisions.
	At []string
	// Names are the new branches, bottom first, one per split point.
	Names []string
}

// Split divides a branch's commits into several stacked branches.
//
// The branch keeps its name and ends up on top: its pull request then shows
// only the commits above the last split point instead of losing its identity,
// and anything already stacked on it stays where it is. Nothing is rebased —
// the commits are already in a line, so splitting is a matter of pointing new
// branches at commits that are already there and rewriting the metadata.
func Split(env *Env, g *stack.Graph, target *stack.Branch, opts SplitOptions) error {
	repo := env.Repo
	if err := requireNoOperation(env); err != nil {
		return err
	}
	if target == nil {
		return errNoCurrentBranch
	}
	if target.IsTrunk {
		return fmt.Errorf("%s is the trunk branch; check out or name a branch stacked on it", target.Name)
	}
	if !target.Tracked {
		return fmt.Errorf(
			"branch %q is not tracked by stk\n\nTrack it first:\n\n    stk track %s --parent <branch>",
			target.Name, target.Name)
	}
	if target.HasProblem() {
		return fmt.Errorf("%s has a metadata problem: %s", target.Name, problemText(target))
	}

	commits := repo.Commits(target.Base, target.SHA)
	if len(commits) < 2 {
		return fmt.Errorf("%s has %d commit(s) of its own; there is nothing to split",
			target.Name, len(commits))
	}

	choices, err := splitChoices(env, target, commits, opts)
	if err != nil {
		return err
	}
	if len(choices) == 0 {
		env.Out.Printf("No split points chosen; leaving %s as it is.", target.Name)
		return nil
	}
	if err := validateSplit(repo, target, commits, choices); err != nil {
		return err
	}

	printSplitPlan(env, repo, target, commits, choices)
	if env.DryRun {
		env.Out.Printf("")
		env.Out.Printf("No changes have been made.")
		return nil
	}

	// Bottom up, so every new branch has its parent already in place.
	parent := target.Parent
	base := target.Base
	for _, c := range choices {
		id, err := stack.NewID()
		if err != nil {
			return err
		}
		if err := repo.CreateBranch(c.Name, c.SHA); err != nil {
			return err
		}
		if err := stack.WriteMeta(repo, c.Name, stack.Meta{ID: id, ParentID: parentID(parent)}); err != nil {
			return err
		}
		if err := stack.SetBase(repo, id, base); err != nil {
			return err
		}
		env.Out.OK("Created %s at %s on %s", c.Name, git.ShortSHA(c.SHA), parentName(parent, g))
		parent = &stack.Branch{ID: id, Name: c.Name}
		base = c.SHA
	}

	// The branch that was split keeps its tip and its children, and now sits
	// on the last of the new branches.
	if err := stack.SetParent(repo, target.Name, parent.ID); err != nil {
		return err
	}
	if err := stack.SetBase(repo, target.ID, base); err != nil {
		return err
	}
	env.Out.OK("%s now sits on %s", target.Name, parent.Name)

	env.Out.Printf("")
	env.Out.Printf("%s split into %d branch(es).", target.Name, len(choices)+1)
	env.Out.Printf("")
	env.Out.Printf("Publish them with:")
	env.Out.Printf("")
	env.Out.Printf("    stk submit --stack --pull")
	return nil
}

// splitChoices resolves the split points, asking when none were given.
func splitChoices(env *Env, target *stack.Branch, commits []git.Commit, opts SplitOptions) ([]SplitChoice, error) {
	if len(opts.At) > 0 {
		if len(opts.Names) != len(opts.At) {
			return nil, fmt.Errorf("%d split point(s) need %d name(s), got %d\n\nName each one with --name, bottom first",
				len(opts.At), len(opts.At), len(opts.Names))
		}
		out := make([]SplitChoice, 0, len(opts.At))
		for i, rev := range opts.At {
			sha, err := env.Repo.RevParse(rev)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve %q", rev)
			}
			out = append(out, SplitChoice{SHA: sha, Name: opts.Names[i]})
		}
		return out, nil
	}
	if len(opts.Names) > 0 {
		return nil, errors.New("--name needs a --at to go with it")
	}
	if env.AskSplitPoints == nil {
		return nil, fmt.Errorf(
			"no split points given, and stk cannot ask\n\nName them explicitly:\n\n    stk split --at <commit> --name <branch>")
	}
	return env.AskSplitPoints(target.Name, commits)
}

// validateSplit checks the choice against the branch's own history.
func validateSplit(repo *git.Repo, target *stack.Branch, commits []git.Commit, choices []SplitChoice) error {
	position := map[string]int{}
	for i, c := range commits {
		position[c.SHA] = i
	}
	last := -1
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, c := range choices {
		at, ok := position[c.SHA]
		if !ok {
			return fmt.Errorf("%s is not one of the commits %s adds to its parent", git.ShortSHA(c.SHA), target.Name)
		}
		if at == len(commits)-1 {
			return fmt.Errorf("%s is the tip of %s; splitting there would leave nothing above it",
				git.ShortSHA(c.SHA), target.Name)
		}
		if seen[c.SHA] {
			return fmt.Errorf("%s is named twice", git.ShortSHA(c.SHA))
		}
		if at <= last {
			return errors.New("split points must run in history order, oldest first")
		}
		seen[c.SHA] = true
		last = at

		if !repo.ValidBranchName(c.Name) {
			return fmt.Errorf("%q is not a valid branch name", c.Name)
		}
		if repo.BranchExists(c.Name) {
			return fmt.Errorf("branch %q already exists", c.Name)
		}
		if names[c.Name] {
			return fmt.Errorf("branch %q is named twice", c.Name)
		}
		names[c.Name] = true
	}
	return nil
}

// printSplitPlan lays out the segments before any branch is created.
func printSplitPlan(env *Env, repo *git.Repo, target *stack.Branch, commits []git.Commit, choices []SplitChoice) {
	p := env.Out
	prefix := ""
	if env.DryRun {
		prefix = "(dry-run) "
	}
	p.Printf("%sSplitting %s into %d branch(es), bottom first:", prefix, target.Name, len(choices)+1)
	p.Printf("")

	cut := map[string]string{}
	for _, c := range choices {
		cut[c.SHA] = c.Name
	}
	name := ""
	for i, c := range commits {
		if name == "" {
			name = target.Name
			if i < len(commits) {
				// Segments are named by the branch that will end them.
				for j := i; j < len(commits); j++ {
					if n, ok := cut[commits[j].SHA]; ok {
						name = n
						break
					}
				}
			}
			p.Printf("  %s", name)
		}
		p.Printf("      %s  %s", git.ShortSHA(c.SHA), c.Subject)
		if _, ends := cut[c.SHA]; ends {
			name = ""
		}
	}
	p.Printf("")
}

// parentName says where a new branch hangs, with trunk named properly.
func parentName(parent *stack.Branch, g *stack.Graph) string {
	if parent == nil || parent.IsTrunk {
		return g.Trunk.Name
	}
	return parent.Name
}
