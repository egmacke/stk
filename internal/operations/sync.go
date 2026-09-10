package operations

import (
	"fmt"
	"sort"
	"strings"

	"stk/internal/stack"
)

// CleanupMode decides what stk sync does with branches that are provably
// contained in trunk.
type CleanupMode int

const (
	// CleanupAsk prompts before deleting anything.
	CleanupAsk CleanupMode = iota
	// CleanupAlways deletes safe branches without prompting.
	CleanupAlways
	// CleanupNever leaves every branch alone.
	CleanupNever
)

// SyncOptions configures stk sync.
type SyncOptions struct {
	StackOnly bool
	Restack   bool
	Cleanup   CleanupMode
}

// Sync fetches the remote, fast-forwards trunk where that is provably safe,
// offers to remove branches already contained in trunk, and restacks what is
// left. It never pushes.
func Sync(env *Env, opts SyncOptions) error {
	repo := env.Repo
	cfg := env.Cfg

	if err := requireNoOperation(env); err != nil {
		return err
	}
	if opts.Restack {
		if err := requireCleanTree(env); err != nil {
			return err
		}
	}

	if cfg.Remote != "" && repo.RemoteExists(cfg.Remote) {
		if env.DryRun {
			env.Out.Printf("(dry-run) would fetch %s", cfg.Remote)
		} else {
			env.Out.Printf("Fetching %s...", cfg.Remote)
			if err := repo.Fetch(cfg.Remote); err != nil {
				return err
			}
		}
	} else if cfg.Remote != "" {
		env.Out.Warnf("Remote %q is not configured; skipping fetch.", cfg.Remote)
	}

	trunkSHA, err := updateTrunk(env)
	if err != nil {
		return err
	}

	g, err := stack.Load(repo, cfg)
	if err != nil {
		return err
	}
	if trunkSHA == "" {
		trunkSHA = g.Trunk.SHA
	}

	if opts.Cleanup != CleanupNever {
		if err := cleanup(env, g, trunkSHA, opts); err != nil {
			return err
		}
		if g, err = stack.Load(repo, cfg); err != nil {
			return err
		}
	}

	if !opts.Restack {
		return nil
	}

	scope := ScopeAll
	var target *stack.Branch
	if opts.StackOnly {
		scope = ScopeStack
		target = g.Current
		if target == nil {
			return errNoCurrentBranch
		}
	}
	env.Out.Printf("")
	if env.DryRun {
		env.Out.Printf("The plan below is computed against the current trunk, because")
		env.Out.Printf("a dry run does not fetch or move it.")
		env.Out.Printf("")
	}
	_, err = Restack(env, g, target, RestackOptions{
		Scope:       scope,
		Heading:     "Restacking...",
		DoneMessage: "All stacks are up to date.",
	})
	return err
}

// updateTrunk fast-forwards the local trunk ref to its remote counterpart and
// returns the commit trunk ends up at.
//
// It works whether trunk is checked out here, in another worktree, or nowhere,
// and it refuses to touch a trunk that has diverged from the remote.
func updateTrunk(env *Env) (string, error) {
	repo := env.Repo
	trunk := env.Cfg.Trunk
	if env.Cfg.Remote == "" {
		return "", nil
	}
	remoteRef := "refs/remotes/" + env.Cfg.Remote + "/" + trunk
	remoteSHA, err := repo.RevParse(remoteRef)
	if err != nil {
		env.Out.Warnf("No remote branch %s/%s; leaving %s alone.", env.Cfg.Remote, trunk, trunk)
		return "", nil
	}
	localSHA, err := repo.RevParse("refs/heads/" + trunk)
	if err != nil {
		return "", fmt.Errorf("trunk branch %q does not exist locally", trunk)
	}
	if localSHA == remoteSHA {
		env.Out.OK("%s is already up to date", trunk)
		return localSHA, nil
	}
	if !repo.IsAncestor(localSHA, remoteSHA) {
		return "", fmt.Errorf(
			"local %s has diverged from %s/%s\n\n"+
				"stk will not overwrite it. Reconcile the two branches yourself, then run stk sync again",
			trunk, env.Cfg.Remote, trunk)
	}
	count := repo.CountCommits(localSHA, remoteSHA)

	if env.DryRun {
		env.Out.Printf("(dry-run) would fast-forward %s by %d commit(s)", trunk, count)
		// Report what a real run would prune, not what the stale trunk holds.
		return remoteSHA, nil
	}

	worktrees, err := repo.BranchWorktrees()
	if err != nil {
		return "", err
	}
	holder := worktrees[trunk]
	switch {
	case holder == "":
		// Nobody has trunk checked out, so moving the ref is enough.
		if err := repo.UpdateRef("refs/heads/"+trunk, remoteSHA); err != nil {
			return "", err
		}
	default:
		// Update through the worktree that owns it so its index and files
		// move with the ref.
		wt := repo.R.WithDir(holder)
		status := wt.Run("status", "--porcelain", "--untracked-files=no")
		if !status.OK() {
			return "", status.Error()
		}
		if strings.TrimSpace(status.Stdout) != "" {
			env.Out.Skip("%s not updated: checked out with uncommitted changes in %s", trunk, holder)
			return localSHA, nil
		}
		if res := wt.Mutate("merge", "--ff-only", remoteRef); !res.OK() {
			return "", res.Error()
		}
	}
	env.Out.OK("%s fast-forwarded by %d commit(s)", trunk, count)
	return remoteSHA, nil
}

// cleanup removes branches git can prove are already contained in trunk.
func cleanup(env *Env, g *stack.Graph, trunkSHA string, opts SyncOptions) error {
	repo := env.Repo
	if trunkSHA == "" {
		return nil
	}

	var safe []*stack.Branch
	var held []*stack.Branch
	for _, b := range g.Tracked {
		if b.SHA == "" || b.HasProblem() {
			continue
		}
		// Git can prove the branch holds no commit that trunk lacks, so
		// deleting it discards nothing. Branches checked out in a worktree
		// are excluded below, which spares freshly created ones.
		if !repo.IsAncestor(b.SHA, trunkSHA) {
			continue
		}
		if b.Worktree != "" {
			held = append(held, b)
			continue
		}
		safe = append(safe, b)
	}

	if len(held) > 0 {
		env.Out.Printf("")
		env.Out.Printf("Safe to prune, but currently checked out:")
		env.Out.Printf("")
		for _, b := range held {
			env.Out.Printf("  %s", b.Name)
			env.Out.Printf("      %s", b.Worktree)
		}
	}
	if len(safe) == 0 {
		return nil
	}

	env.Out.Printf("")
	env.Out.Printf("The following branches are fully contained in %s:", g.Trunk.Name)
	env.Out.Printf("")
	for _, b := range safe {
		env.Out.Printf("    %s", b.Name)
	}
	env.Out.Printf("")

	if opts.Cleanup == CleanupAsk {
		if env.Confirm == nil {
			env.Out.Printf("Not removing anything; re-run with --cleanup to delete them.")
			return nil
		}
		ok, err := env.Confirm(fmt.Sprintf("Remove these %d local branches?", len(safe)), true)
		if err != nil {
			return err
		}
		if !ok {
			env.Out.Printf("Leaving them in place.")
			return nil
		}
	}
	if env.DryRun {
		env.Out.Printf("(dry-run) would remove %d branch(es)", len(safe))
		return nil
	}

	doomed := map[string]bool{}
	for _, b := range safe {
		doomed[b.ID] = true
	}

	// Survivors below a pruned branch adopt the nearest ancestor that stays,
	// so the remaining graph keeps its shape.
	for _, b := range g.Tracked {
		if doomed[b.ID] || b.HasProblem() {
			continue
		}
		if b.Parent == nil || !doomed[b.Parent.ID] {
			continue
		}
		ancestor := b.Parent
		for ancestor != nil && !ancestor.IsTrunk && doomed[ancestor.ID] {
			ancestor = ancestor.Parent
		}
		if ancestor == nil {
			ancestor = g.Trunk
		}
		if err := stack.SetParent(repo, b.Name, parentID(ancestor)); err != nil {
			return err
		}
		env.Out.OK("Reparented %s onto %s", b.Name, ancestor.Name)
	}

	sort.Slice(safe, func(i, j int) bool { return safe[i].Name < safe[j].Name })
	for _, b := range safe {
		if err := stack.ClearBase(repo, b.ID); err != nil {
			return err
		}
		// -D is justified: ancestry already proved no unique commits are lost.
		if err := repo.DeleteBranch(b.Name, true); err != nil {
			return err
		}
		// git removes the branch config section on delete, but clear it
		// explicitly in case an older git left it behind.
		if err := stack.ClearMeta(repo, b.Name); err != nil {
			return err
		}
		env.Out.OK("Removed %s", b.Name)
	}
	return nil
}
