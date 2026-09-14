package operations

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egmacke/stk/internal/config"
	"github.com/egmacke/stk/internal/forge"
	"github.com/egmacke/stk/internal/ghstack"
	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// ghSyncOptions carries what one command learned that gh stack's tracking
// should record too.
type ghSyncOptions struct {
	// Renames maps old branch names to new ones, so a renamed branch keeps
	// the pull request gh stack had recorded for it.
	Renames map[string]string
	// PullRequests are the pull requests a submit has just seen, by branch.
	// A nil entry means the branch was asked about and has none.
	PullRequests map[string]*forge.PullRequest
	// Untracked names the branches the command has just untracked. They are
	// still in gh stack's file until the mirror rewrites it, and must not be
	// adopted straight back.
	Untracked []string
	// Force runs the sync even when the repository has not opted in. It is
	// for stk track --from-pr, which has just made gh stack write tracking
	// that stk must read whatever the preference says.
	Force bool
}

// mirrorGHStack keeps gh stack's tracking in step after a change to the
// graph, and only warns when it cannot: stk's own record is already written,
// and stk doctor reports the drift until the next change repairs it.
func mirrorGHStack(env *Env, opts ghSyncOptions) {
	if _, err := syncGHStack(env, opts); err != nil {
		env.Out.Warnf("gh stack tracking not updated: %v", err)
	}
}

// syncGHStack brings gh stack's local tracking and the stk graph into step,
// in both directions.
//
// Branches gh stack tracks that stk does not are adopted into the graph first,
// each with the branch below it in gh stack's list as its parent and gh
// stack's recorded base as its base: that list is the user's own statement of
// the stack, made through gh stack instead of stk. Then the file is rewritten
// as the projection of the graph, so for every branch stk tracks the graph is
// the record and gh stack sees exactly what stk sees.
//
// It does nothing unless the repository has opted in, and nothing at all in a
// dry run, which has changed nothing worth mirroring. The number of branches
// adopted is returned.
func syncGHStack(env *Env, opts ghSyncOptions) (int, error) {
	if (!env.Cfg.GitHubStacks && !opts.Force) || env.DryRun {
		return 0, nil
	}
	repo := env.Repo
	existing, err := ghstack.Load(repo.CommonDir)
	if err != nil {
		return 0, err
	}
	g, err := stack.Load(repo, env.Cfg)
	if err != nil {
		return 0, err
	}
	adopted, err := adoptGHBranches(env, g, existing, opts.Untracked)
	if err != nil {
		return adopted, err
	}
	if adopted > 0 {
		if g, err = stack.Load(repo, env.Cfg); err != nil {
			return adopted, err
		}
	}
	projected := projectGHStacks(g, existing, opts)
	if ghstack.Equal(existing, projected) {
		return adopted, nil
	}
	if err := ghstack.Save(repo.CommonDir, projected); err != nil {
		return adopted, err
	}
	env.Out.OK("gh stack tracking updated")
	return adopted, nil
}

// adoptGHBranches tracks, in stk, every branch gh stack tracks that stk does
// not yet, walking each gh stack bottom first so a chain of new branches
// attaches link by link.
//
// A branch whose predecessor stk cannot resolve to a tracked branch, or to
// trunk, is left alone and said so: stk never guesses a parent, and gh stack's
// list gives none it can use.
func adoptGHBranches(env *Env, g *stack.Graph, f *ghstack.File, untracked []string) (int, error) {
	repo := env.Repo
	adopted := 0
	letGo := map[string]bool{}
	for _, name := range untracked {
		letGo[name] = true
	}
	// Branches adopted in this pass, by name, with the ids they were given,
	// because the graph in hand does not know them yet.
	newIDs := map[string]string{}
	for _, s := range f.Stacks {
		prev := s.Trunk.Branch
		prevOK := true
		for _, ref := range s.Branches {
			b := g.ByName[ref.Branch]
			switch {
			case b == nil:
				// Not a local branch: nothing to adopt, and nothing above it
				// can hang off it.
				prev, prevOK = ref.Branch, false
				continue
			case b.IsTrunk, b.Tracked:
				prev, prevOK = b.Name, !b.HasProblem()
				continue
			case letGo[b.Name]:
				// Untracked on purpose a moment ago.
				prev, prevOK = b.Name, false
				continue
			}
			parent := g.ByName[prev]
			parentID := ""
			switch {
			case !prevOK || parent == nil:
				prevOK = false
			case parent.IsTrunk:
				// Sits on trunk.
			case parent.Tracked && !parent.HasProblem():
				parentID = parent.ID
			case newIDs[prev] != "":
				parentID = newIDs[prev]
			default:
				prevOK = false
			}
			if !prevOK {
				env.Out.Skip("%s is in gh stack's tracking, but %s below it is not tracked by stk; left alone", output.BranchName(b.Name), output.BranchName(prev))
				prev = b.Name
				continue
			}
			base := ref.Base
			if base == "" || !repo.IsAncestor(base, b.SHA) {
				// gh stack's base is stale or missing; the merge base is
				// what stk track would have chosen.
				mb, err := repo.MergeBase("refs/heads/"+prev, "refs/heads/"+b.Name)
				if err != nil {
					env.Out.Skip("%s is in gh stack's tracking, but shares no history with %s; left alone", output.BranchName(b.Name), output.BranchName(prev))
					prev, prevOK = b.Name, false
					continue
				}
				base = mb
			}
			id, err := stack.NewID()
			if err != nil {
				return adopted, err
			}
			if err := stack.WriteMeta(repo, b.Name, stack.Meta{ID: id, ParentID: parentID}); err != nil {
				return adopted, err
			}
			if err := stack.SetBase(repo, id, base); err != nil {
				return adopted, err
			}
			newIDs[b.Name] = id
			env.Out.OK("Tracking %s with parent %s (from gh stack)", output.BranchName(b.Name), output.BranchName(prev))
			adopted++
			prev, prevOK = b.Name, true
		}
	}
	return adopted, nil
}

// ghChains decomposes the graph into the linear stacks gh stack can hold.
//
// From each branch on trunk the chain runs upward while every branch has one
// child. Where a branch forks, the chain ends there and each child begins a
// stack of its own whose trunk is the fork: gh stack's stacks are strictly
// linear, and this is the one decomposition in which every branch is in
// exactly one stack and every stack's parent relationships are the graph's.
func ghChains(g *stack.Graph) []ghstack.Stack {
	var out []ghstack.Stack
	var walk func(start, trunk *stack.Branch)
	walk = func(start, trunk *stack.Branch) {
		s := ghstack.Stack{Trunk: ghstack.BranchRef{Branch: trunk.Name, Head: trunk.SHA}}
		cur := start
		for {
			s.Branches = append(s.Branches, ghstack.BranchRef{Branch: cur.Name, Base: cur.Base})
			if len(cur.Children) != 1 {
				break
			}
			cur = cur.Children[0]
		}
		out = append(out, s)
		if len(cur.Children) > 1 {
			for _, c := range cur.Children {
				walk(c, cur)
			}
		}
	}
	for _, root := range g.Roots() {
		walk(root, g.Trunk)
	}
	return out
}

// projectGHStacks writes the graph into gh stack's shape, carrying over what
// only gh stack knows: each branch's pull request, and each stack's identity
// on GitHub.
//
// A stack in the existing file is replaced by the projection when it holds
// any branch stk tracks, or has just untracked. One holding none of those but
// some branch that exists is somebody else's and is kept as it is; one whose
// branches are all gone from the repository is dropped.
func projectGHStacks(g *stack.Graph, existing *ghstack.File, opts ghSyncOptions) *ghstack.File {
	// What the existing file says about each branch, under the branch's
	// current name.
	refs := map[string]ghstack.BranchRef{}
	stacks := map[string]*ghstack.Stack{}
	renamed := map[string]string{}
	for old, now := range opts.Renames {
		renamed[old] = now
	}
	for i := range existing.Stacks {
		s := &existing.Stacks[i]
		for _, ref := range s.Branches {
			name := ref.Branch
			if now, ok := renamed[name]; ok {
				name = now
			}
			refs[name] = ref
			stacks[name] = s
		}
	}

	// Branches whose place in gh stack's file is stk's to decide.
	claimed := map[string]bool{}
	for _, b := range g.Tracked {
		if !b.HasProblem() {
			claimed[b.Name] = true
		}
	}
	for _, name := range opts.Untracked {
		claimed[name] = true
	}

	out := &ghstack.File{SchemaVersion: ghstack.SchemaVersion, Repository: existing.Repository, Stacks: []ghstack.Stack{}}
	for _, chain := range ghChains(g) {
		for i := range chain.Branches {
			ref := &chain.Branches[i]
			if old, ok := refs[ref.Branch]; ok {
				ref.PullRequest = old.PullRequest
			}
			if pr, seen := opts.PullRequests[ref.Branch]; seen && pr != nil {
				ref.PullRequest = &ghstack.PullRequest{Number: pr.Number, URL: pr.URL}
			}
		}
		// The stack's identity on GitHub follows its bottom branch.
		if s, ok := stacks[chain.Branches[0].Branch]; ok {
			chain.ID, chain.Number = s.ID, s.Number
		}
		out.Stacks = append(out.Stacks, chain)
	}
	for _, s := range existing.Stacks {
		mine, alive := false, false
		for _, ref := range s.Branches {
			name := ref.Branch
			if now, ok := renamed[name]; ok {
				name = now
			}
			if claimed[name] {
				mine = true
				break
			}
			if _, exists := g.ByName[name]; exists {
				alive = true
			}
		}
		if !mine && alive {
			out.Stacks = append(out.Stacks, s)
		}
	}
	return out
}

// refreshGHPullRequests asks GitHub about the pull requests of the branches
// gh stack tracks and records what it learns: a pull request a branch has
// gained since stk last looked, and a recorded one that has since merged.
//
// It is the pull request half of gh stack sync, done with stk's own view of
// the branches. Failures are reported, not fatal: the sync it is part of has
// more important work to do.
func refreshGHPullRequests(env *Env) {
	if !env.Cfg.GitHubStacks || env.DryRun || env.Cfg.Remote == "" {
		return
	}
	repo := env.Repo
	f, err := ghstack.Load(repo.CommonDir)
	if err != nil || len(f.Stacks) == 0 {
		return
	}
	gh, err := openForge(env, env.Cfg.Remote)
	if err != nil {
		env.Out.Warnf("Not refreshing pull request state: %v", firstLine(err))
		return
	}
	recorded, merged := 0, 0
	for i := range f.Stacks {
		for j := range f.Stacks[i].Branches {
			ref := &f.Stacks[i].Branches[j]
			if !repo.BranchExists(ref.Branch) {
				continue
			}
			if ref.PullRequest == nil {
				pr, err := gh.OpenPullRequest(ref.Branch)
				if err != nil {
					env.Out.Warnf("Not refreshing pull request state: %v", firstLine(err))
					return
				}
				if pr != nil {
					ref.PullRequest = &ghstack.PullRequest{Number: pr.Number, URL: pr.URL}
					recorded++
				}
				continue
			}
			if ref.PullRequest.Merged {
				continue
			}
			state, err := gh.PullRequestState(ref.PullRequest.Number)
			if err != nil {
				env.Out.Warnf("Not refreshing pull request state: %v", firstLine(err))
				return
			}
			if state == "MERGED" {
				ref.PullRequest.Merged = true
				merged++
			}
		}
	}
	if recorded == 0 && merged == 0 {
		return
	}
	if err := ghstack.Save(repo.CommonDir, f); err != nil {
		env.Out.Warnf("gh stack tracking not updated: %v", err)
		return
	}
	var parts []string
	if recorded > 0 {
		parts = append(parts, fmt.Sprintf("%d pull request(s) recorded", recorded))
	}
	if merged > 0 {
		parts = append(parts, fmt.Sprintf("%d marked merged", merged))
	}
	env.Out.OK("gh stack tracking: %s", strings.Join(parts, ", "))
}

func firstLine(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

// TrackFromPR brings a stack that exists on GitHub into the graph.
//
// gh stack checkout does the discovery: it finds the stack the pull request
// belongs to, fetches its branches, records the stack in its own tracking and
// checks out the branch. stk then adopts what gh stack recorded, so the graph
// gains the whole stack with the parents GitHub has for it.
func TrackFromPR(env *Env, ref string) error {
	if !env.Cfg.GitHubStacks {
		return fmt.Errorf(
			"stk track --from-pr works through gh stack, which needs %s = true\n\nTurn it on with:\n\n    git config %s true",
			config.KeyGitHubStacks, config.KeyGitHubStacks)
	}
	if err := requireNoOperation(env); err != nil {
		return err
	}
	remote := env.Cfg.Remote
	if remote == "" {
		return errors.New("no default remote is configured\n\nRecord one with:\n\n    stk init --remote <name>")
	}
	if !env.Repo.RemoteExists(remote) {
		return fmt.Errorf("remote %q is not configured", remote)
	}
	gh, err := openForge(env, remote)
	if err != nil {
		return err
	}
	if err := requireStackExtension(gh); err != nil {
		return err
	}
	if env.DryRun {
		env.Out.Dry("would run gh stack checkout %s and track the branches it brings", ref)
		return nil
	}
	// gh stack checks the branch out itself, so the working tree is parked
	// the way it would be for stk checkout.
	stash, err := Stash(env, "track --from-pr")
	if err != nil {
		return err
	}
	defer stash.Restore(env)

	env.Out.Printf("Running gh stack checkout %s...", ref)
	if err := gh.CheckoutStack(ref); err != nil {
		var ce *forge.CommandError
		if errors.As(err, &ce) {
			switch ce.ExitCode {
			case 2:
				return fmt.Errorf("gh stack found no stack for %q", ref)
			case 3:
				return fmt.Errorf(
					"gh stack's local tracking disagrees with the stack on GitHub, and it could not ask how to resolve that\n\n" +
						"Run it on a terminal to choose, or drop the local tracking first:\n\n    gh stack unstack --local")
			}
		}
		return fmt.Errorf("gh stack checkout %s failed; see its output above", ref)
	}
	adopted, err := syncGHStack(env, ghSyncOptions{Force: true})
	if err != nil {
		return err
	}
	env.Out.Printf("")
	if adopted == 0 {
		env.Out.Printf("Every branch of that stack was already tracked.")
		return nil
	}
	env.Out.Printf("%d branch(es) tracked from the stack on GitHub.", adopted)
	return nil
}

// AnnotatePullRequests copies what gh stack's tracking knows about pull
// requests onto the graph, for display. It reads only, and only when the
// repository has opted in; a file stk cannot read is a matter for stk doctor.
func AnnotatePullRequests(repo *git.Repo, cfg config.Config, g *stack.Graph) {
	if !cfg.GitHubStacks {
		return
	}
	f, err := ghstack.Load(repo.CommonDir)
	if err != nil {
		return
	}
	for _, s := range f.Stacks {
		for _, ref := range s.Branches {
			if ref.PullRequest == nil {
				continue
			}
			if b := g.ByName[ref.Branch]; b != nil {
				b.PR = &stack.PullRequest{Number: ref.PullRequest.Number, URL: ref.PullRequest.URL, Merged: ref.PullRequest.Merged}
			}
		}
	}
}

// GHStackDrift compares gh stack's tracking with what stk would write and
// describes the difference, or returns "" when the two are in step.
func GHStackDrift(repo *git.Repo, g *stack.Graph) (string, error) {
	existing, err := ghstack.Load(repo.CommonDir)
	if err != nil {
		return "", err
	}
	projected := projectGHStacks(g, existing, ghSyncOptions{})
	if ghstack.Equal(existing, projected) {
		return "", nil
	}
	tracked := map[string]bool{}
	for _, b := range g.Tracked {
		tracked[b.Name] = true
	}
	var ghOnly, stkOnly []string
	inGH := map[string]bool{}
	for _, s := range existing.Stacks {
		for _, ref := range s.Branches {
			inGH[ref.Branch] = true
			if !tracked[ref.Branch] && repo.BranchExists(ref.Branch) {
				ghOnly = append(ghOnly, ref.Branch)
			}
		}
	}
	for _, b := range g.Tracked {
		if !inGH[b.Name] {
			stkOnly = append(stkOnly, b.Name)
		}
	}
	sort.Strings(ghOnly)
	sort.Strings(stkOnly)
	var parts []string
	if len(ghOnly) > 0 {
		parts = append(parts, fmt.Sprintf("tracked only by gh stack: %s", strings.Join(ghOnly, ", ")))
	}
	if len(stkOnly) > 0 {
		parts = append(parts, fmt.Sprintf("tracked only by stk: %s", strings.Join(stkOnly, ", ")))
	}
	if len(parts) == 0 {
		parts = append(parts, "order or bases differ")
	}
	return strings.Join(parts, "; "), nil
}
