package operations

import (
	"fmt"
	"sort"
	"strings"

	"stk/internal/forge"
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
	// NoPulls skips the forge entirely, so cleanup works from git alone.
	NoPulls bool
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
	// Parked before the fetch rather than before the restack: a dirty working
	// tree also stops trunk moving when trunk is checked out here.
	stash, err := Stash(env, "sync")
	if err != nil {
		return err
	}
	// Until the restack takes over, the stash is this function's to put back.
	handedOver := false
	defer func() {
		if !handedOver {
			stash.Restore(env)
		}
	}()
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
	handedOver = true
	_, err = Restack(env, g, target, RestackOptions{
		Scope:       scope,
		Heading:     "Restacking...",
		DoneMessage: "All stacks are up to date.",
		Stash:       stash,
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

// pruneCandidate is a branch stk offers to remove, and the evidence for it.
type pruneCandidate struct {
	Branch *stack.Branch
	// Reason is shown beside the branch name.
	Reason string
	// Proven means something outside the branch demonstrably holds its work:
	// trunk already contains it, or its pull request landed.
	Proven bool
}

// prListLimit is how many pull requests stk reads in one call before falling
// back to asking about a branch directly.
const prListLimit = 100

// cleanup removes branches that are finished: the ones git can prove are
// already contained in trunk, and the ones the remote says are done with.
//
// The two are kept apart, because only the first carries proof. A branch whose
// pull request was closed, or whose remote branch someone deleted, may still be
// the only copy of its commits, so it is listed with what it would take with it
// and asked about separately.
func cleanup(env *Env, g *stack.Graph, trunkSHA string, opts SyncOptions) error {
	repo := env.Repo
	if trunkSHA == "" {
		return nil
	}

	var candidates []pruneCandidate
	var unresolved []*stack.Branch
	for _, b := range g.Tracked {
		if b.SHA == "" || b.HasProblem() {
			continue
		}
		// Git can prove the branch holds nothing that trunk lacks, so
		// deleting it discards nothing.
		//
		// Ancestry is the plain case. A squash merge is the other one: the
		// content lands on trunk under a commit of its own, so the branch is
		// no ancestor of trunk and yet adds nothing to it — identical trees
		// are the proof, and without this a squash-merged branch would only
		// be noticed by the next sync, after a restack had emptied it.
		switch {
		case repo.IsAncestor(b.SHA, trunkSHA):
			candidates = append(candidates, pruneCandidate{Branch: b, Reason: "already in " + g.Trunk.Name, Proven: true})
		case repo.SameTree(trunkSHA, b.SHA):
			candidates = append(candidates, pruneCandidate{Branch: b, Reason: "squash-merged into " + g.Trunk.Name, Proven: true})
		case b.Upstream != "" || b.UpstreamGone:
			// Published at some point, so the remote has an opinion worth
			// asking for.
			unresolved = append(unresolved, b)
		}
	}
	candidates = append(candidates, remoteFinished(env, unresolved, opts)...)

	// Branches held by a worktree are reported but never touched: their refs
	// are not stk's to move or delete.
	var held []pruneCandidate
	var proven, unproven []pruneCandidate
	for _, c := range candidates {
		switch {
		case c.Branch.Worktree != "":
			held = append(held, c)
		case c.Proven:
			proven = append(proven, c)
		default:
			unproven = append(unproven, c)
		}
	}
	sortCandidates(held)
	sortCandidates(proven)
	sortCandidates(unproven)

	if len(held) > 0 {
		env.Out.Printf("")
		env.Out.Printf("Finished, but currently checked out:")
		env.Out.Printf("")
		for _, c := range held {
			env.Out.Printf("  %s", c.Branch.Name)
			env.Out.Printf("      %s", c.Branch.Worktree)
		}
	}
	if len(proven) == 0 && len(unproven) == 0 {
		return nil
	}

	// Counted only for the branches with no proof, because that is the only
	// list where the number changes the answer. The whole group is treated as
	// gone, so a branch is not counted as safe merely because another branch
	// on the same list still holds its commits.
	keepers := survivingTips(g, branchesOf(unproven))
	for i, c := range unproven {
		if n := repo.CountUniqueCommits(c.Branch.SHA, keepers); n > 0 {
			unproven[i].Reason = fmt.Sprintf("%s, %d commit(s) kept nowhere else", c.Reason, n)
		}
	}

	var doomed []*stack.Branch
	couldNotAsk := false
	for _, group := range []struct {
		heading string
		list    []pruneCandidate
		byline  string
		def     bool
	}{
		{
			heading: fmt.Sprintf("The following branches add nothing to %s:", g.Trunk.Name),
			list:    proven,
			def:     true,
		},
		{
			heading: "The following branches are finished on the remote:",
			list:    unproven,
			byline:  "The remote is done with them, but nothing proves their commits reached " + g.Trunk.Name + ".",
			def:     false,
		},
	} {
		if len(group.list) == 0 {
			continue
		}
		printPruneGroup(env, group.heading, group.byline, group.list)
		if opts.Cleanup == CleanupAlways {
			doomed = append(doomed, branchesOf(group.list)...)
			continue
		}
		if env.Confirm == nil {
			couldNotAsk = true
			continue
		}
		ok, err := env.Confirm(fmt.Sprintf("Remove these %d local branches?", len(group.list)), group.def)
		if err != nil {
			return err
		}
		if !ok {
			env.Out.Printf("Leaving them in place.")
			continue
		}
		doomed = append(doomed, branchesOf(group.list)...)
	}

	if len(doomed) == 0 {
		if couldNotAsk {
			env.Out.Printf("Not removing anything; re-run with --cleanup to delete them.")
		}
		return nil
	}
	if env.DryRun {
		env.Out.Printf("(dry-run) would remove %d branch(es)", len(doomed))
		return nil
	}

	env.Out.Printf("")
	// Survivors below a pruned branch adopt the nearest ancestor that stays,
	// so the remaining graph keeps its shape.
	if err := reparentSurvivors(env, reparented(g, doomed)); err != nil {
		return err
	}
	sort.Slice(doomed, func(i, j int) bool { return doomed[i].Name < doomed[j].Name })
	for _, b := range doomed {
		if err := dropBranch(env, b); err != nil {
			return err
		}
		env.Out.OK("Removed %s", b.Name)
	}
	return nil
}

// remoteFinished asks the forge which of the remaining branches are done with:
// their pull request landed or was closed, or their remote branch is gone.
//
// A repository with no reachable GitHub remote simply yields the branches whose
// remote counterpart has disappeared; stk never fails a sync because a pull
// request could not be read.
func remoteFinished(env *Env, branches []*stack.Branch, opts SyncOptions) []pruneCandidate {
	if len(branches) == 0 {
		return nil
	}
	prs := map[string]*forge.PullRequest{}
	if !opts.NoPulls && !env.DryRun {
		prs = pullRequestsFor(env, branches)
	}
	var out []pruneCandidate
	for _, b := range branches {
		pr := prs[b.Name]
		switch {
		case pr != nil && pr.IsMerged():
			out = append(out, pruneCandidate{Branch: b, Reason: fmt.Sprintf("%s merged", pr), Proven: true})
		case pr != nil && pr.IsClosed():
			out = append(out, pruneCandidate{Branch: b, Reason: fmt.Sprintf("%s closed", pr)})
		case b.UpstreamGone:
			out = append(out, pruneCandidate{Branch: b, Reason: b.Upstream + " is gone"})
		}
	}
	return out
}

// pullRequestsFor reads the pull request of each branch, newest first.
//
// One listing answers for a whole stack; only a branch whose pull request is
// older than that listing costs a call of its own. Nothing here is fatal: a
// forge stk cannot reach just means fewer branches are offered for removal.
func pullRequestsFor(env *Env, branches []*stack.Branch) map[string]*forge.PullRequest {
	out := map[string]*forge.PullRequest{}
	remote := env.Cfg.Remote
	if remote == "" || !env.Repo.RemoteExists(remote) {
		return out
	}
	// openForge already proves gh is installed and logged in; a repository
	// where it is not simply yields no pull requests.
	gh, err := openForge(env, remote)
	if err != nil {
		return out
	}
	env.Out.Printf("Checking pull requests...")
	list, err := gh.ListPullRequests(prListLimit)
	if err != nil {
		env.Out.Warnf("Could not read pull requests: %v", err)
		return out
	}
	for i := range list {
		// Newest first, so the first entry for a head is the current one.
		if _, seen := out[list[i].Head]; !seen {
			out[list[i].Head] = &list[i]
		}
	}
	for _, b := range branches {
		if _, ok := out[b.Name]; ok {
			continue
		}
		pr, err := gh.LatestPullRequest(b.Name)
		if err != nil || pr == nil {
			continue
		}
		out[b.Name] = pr
	}
	return out
}

func printPruneGroup(env *Env, heading, byline string, list []pruneCandidate) {
	env.Out.Printf("")
	env.Out.Printf("%s", heading)
	env.Out.Printf("")
	width := 0
	for _, c := range list {
		if len(c.Branch.Name) > width {
			width = len(c.Branch.Name)
		}
	}
	for _, c := range list {
		env.Out.Printf("    %-*s   %s", width, c.Branch.Name, c.Reason)
	}
	env.Out.Printf("")
	if byline != "" {
		env.Out.Printf("%s", byline)
		env.Out.Printf("")
	}
}

func sortCandidates(list []pruneCandidate) {
	sort.Slice(list, func(i, j int) bool { return list[i].Branch.Name < list[j].Branch.Name })
}

func branchesOf(list []pruneCandidate) []*stack.Branch {
	out := make([]*stack.Branch, 0, len(list))
	for _, c := range list {
		out = append(out, c.Branch)
	}
	return out
}
