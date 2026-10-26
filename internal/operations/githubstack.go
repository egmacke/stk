package operations

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/egmacke/stk/internal/config"
	"github.com/egmacke/stk/internal/forge"
	"github.com/egmacke/stk/internal/ghstack"
	"github.com/egmacke/stk/internal/output"
	"github.com/egmacke/stk/internal/stack"
)

// requireStackExtension refuses to start a run that will need gh stack link
// before anything has been pushed, for the same reason the login is checked
// first: a stack published and then found impossible to link is a mess to
// unpick by hand.
func requireStackExtension(gh *forge.GH) error {
	err := gh.HasStackExtension()
	if errors.Is(err, forge.ErrNoStackExtension) {
		return fmt.Errorf("%w\n\nOr go back to the stack comment:\n\n    git config %s false",
			err, config.KeyGitHubStacks)
	}
	return err
}

// linkChain returns the run of branches a GitHub stack can hold: from the
// bottom of the stack up through target, and above target only while each
// branch has exactly one child.
//
// GitHub stacks are strictly linear, so where the graph forks the chain ends
// rather than picking a side; the branches on the other side belong to a stack
// of their own.
func linkChain(target *stack.Branch) []*stack.Branch {
	var chain []*stack.Branch
	for b := target; b != nil && !b.IsTrunk; b = b.Parent {
		chain = append([]*stack.Branch{b}, chain...)
	}
	for b := target; len(b.Children) == 1; {
		b = b.Children[0]
		chain = append(chain, b)
	}
	return chain
}

// linearChains decomposes the graph into the linear runs a GitHub stack can
// hold, parents before children.
//
// From each branch on trunk a run goes upward while every branch has exactly
// one child. Where a branch forks the run ends there and each child begins a
// run of its own, because a GitHub stack is strictly linear: this is the one
// decomposition in which every branch is in exactly one run and every run's
// parent relationships are the graph's.
func linearChains(g *stack.Graph) [][]*stack.Branch {
	var out [][]*stack.Branch
	var walk func(start *stack.Branch)
	walk = func(start *stack.Branch) {
		var chain []*stack.Branch
		cur := start
		for {
			chain = append(chain, cur)
			if len(cur.Children) != 1 {
				break
			}
			cur = cur.Children[0]
		}
		out = append(out, chain)
		if len(cur.Children) > 1 {
			for _, c := range cur.Children {
				walk(c)
			}
		}
	}
	for _, root := range g.Roots() {
		walk(root)
	}
	return out
}

// unforkedChains lists the stacks stk can settle on GitHub without being told
// which one is meant: those running from trunk to a tip with no fork anywhere
// along the way.
//
// Where the graph forks there is no single answer. gh stack link is additive,
// so linking each side in turn would gather both into one stack that is the
// shape of neither, and stk submit — which is given the branch — is where that
// choice belongs.
func unforkedChains(g *stack.Graph) [][]*stack.Branch {
	var out [][]*stack.Branch
	for _, chain := range linearChains(g) {
		bottom, top := chain[0], chain[len(chain)-1]
		if len(top.Children) == 0 && bottom.Parent != nil && bottom.Parent.IsTrunk {
			out = append(out, chain)
		}
	}
	return out
}

// linkGitHubStack records the shape of the stack through target on GitHub
// itself, through gh stack link, in place of the stack comment.
func linkGitHubStack(env *Env, gh *forge.GH, target *stack.Branch, prs *pullRequestCache) (bool, error) {
	return linkPullRequestChain(env, gh, linkChain(target), prs, "Linked %s as a stack on GitHub")
}

// linkPullRequestChain links the pull requests of one linear run of branches
// as a stack on GitHub, and reports whether it had two to link.
//
// Only pull requests are linked, bottom first, and only up to the first branch
// without one: a link across that gap would make gh retarget the pull request
// above it onto the branch below, which changes what its reviewer is looking
// at. A stack of one pull request is not linked, as it is not commented on.
//
// done says how the result is announced. stk submit has just built the stack
// and says it linked it; stk sync is reconciling one that may well have been
// linked already, and says only what is true either way.
func linkPullRequestChain(env *Env, gh *forge.GH, chain []*stack.Branch, prs *pullRequestCache, done string) (bool, error) {
	var numbers []int
	var gap *stack.Branch
	for i, b := range chain {
		pr, err := prs.open(gh, b.Name)
		if err != nil {
			return false, err
		}
		if pr != nil {
			numbers = append(numbers, pr.Number)
			continue
		}
		gap = b
		// Only worth mentioning when something above the gap is left out.
		for _, above := range chain[i+1:] {
			pr, err := prs.open(gh, above.Name)
			if err != nil {
				return false, err
			}
			if pr != nil {
				env.Out.Skip("%s has no pull request, so the stack on GitHub stops below it", output.BranchName(gap.Name))
				break
			}
		}
		break
	}
	if len(numbers) < 2 {
		return false, nil
	}

	list := prNumbers(numbers)
	if env.DryRun {
		env.Out.Dry("would link %s as a stack on GitHub", list)
		return true, nil
	}
	err := gh.LinkStack(env.Cfg.Trunk, env.Cfg.Remote, append(landedPullRequests(env, gh, chain, numbers), numbers...))
	if landed := refusedLanded(gh, err, numbers); landed != nil {
		err = gh.LinkStack(env.Cfg.Trunk, env.Cfg.Remote, append(landed, numbers...))
	}
	if err != nil {
		var ce *forge.CommandError
		if errors.As(err, &ce) && ce.ExitCode == forge.ExitStacksUnavailable {
			return false, fmt.Errorf(
				"stacked pull requests are not available for this repository\n\n%s\n\n"+
					"The branches were pushed and the pull requests opened; only the link\n"+
					"between them is missing. Go back to the stack comment with:\n\n    git config %s false",
				indent(ce.Message), config.KeyGitHubStacks)
		}
		return false, err
	}
	env.Out.OK(done, list)
	return true, nil
}

// landedPullRequests returns the merged pull requests of the stack on GitHub
// the chain is already linked as, bottom first, leaving out any the link names
// itself.
//
// gh stack link refuses a link that would take a pull request out of a stack,
// and a merged one is no exception, but stk drops a landed branch from its
// graph and so from the chain. Once the bottom of a stack has merged on GitHub,
// relinking what is left needs the landed pull requests named again beneath
// it. A pull request that left the stack any other way is not added back:
// that is a real difference, and gh stack's refusal says so.
//
// Nothing here is fatal: a stack stk cannot read is linked as before, and gh
// stack link has the last word.
func landedPullRequests(env *Env, gh *forge.GH, chain []*stack.Branch, numbers []int) []int {
	f, err := ghstack.Load(env.Repo.CommonDir)
	if err != nil {
		return nil
	}
	s, _ := f.Find(chain[0].Name)
	if s == nil || s.Number == 0 {
		return nil
	}
	remoteStack, err := gh.Stack(s.Number)
	if err != nil || remoteStack == nil {
		return nil
	}
	named := map[int]bool{}
	for _, n := range numbers {
		named[n] = true
	}
	var landed []int
	for _, pr := range remoteStack.PullRequests {
		if pr.IsMerged() && !named[pr.Number] {
			landed = append(landed, pr.Number)
		}
	}
	return landed
}

// currentStack finds the stack gh stack link names when it refuses a link that
// would take pull requests out of it: "Current stack: #1, #2, #3".
var currentStack = regexp.MustCompile(`(?m)^Current stack: (.*)$`)

var prRef = regexp.MustCompile(`#(\d+)`)

// refusedLanded reads a refused link for the stack it would have changed, and
// returns that stack's pull requests the link left out, bottom first, when
// every one of them has merged. It returns nil for any other failure.
//
// It is the fallback for a stack landedPullRequests cannot see: one linked in
// the browser or from another checkout and never recorded here, whose number
// gh stack's tracking does not hold. gh stack link names the stack in its
// refusal, and the pull requests' own states say whether the difference is
// only what has landed.
func refusedLanded(gh *forge.GH, err error, numbers []int) []int {
	var ce *forge.CommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "Cannot update stack") {
		return nil
	}
	m := currentStack.FindStringSubmatch(ce.Message)
	if m == nil {
		return nil
	}
	named := map[int]bool{}
	for _, n := range numbers {
		named[n] = true
	}
	var landed []int
	for _, ref := range prRef.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(ref[1])
		if named[n] {
			continue
		}
		state, err := gh.PullRequestState(n)
		if err != nil || state != "MERGED" {
			return nil
		}
		landed = append(landed, n)
	}
	return landed
}

// syncGitHubStacks asks GitHub about the stacks stk tracks and brings them
// back into step, by linking each run of pull requests as a stack there.
//
// gh stack's local tracking records only what has happened on this machine, so
// a stack linked, extended or renumbered somewhere else — in the browser, by a
// colleague, or from another checkout — is invisible to stk until something
// asks GitHub itself. Linking is that question, and the same one stk submit
// --pull ends with: gh stack link is given pull request numbers rather than
// branch names, so it pushes nothing and opens nothing, a run that is already
// a stack on GitHub is left as it stands, and either way gh stack's tracking
// comes back to what GitHub holds.
//
// target names one stack to settle; nil means every stack in the graph.
// Nothing here is fatal. The rest of the command has already done its work,
// and a forge stk cannot reach only means the check waits for the next run.
func syncGitHubStacks(env *Env, g *stack.Graph, target *stack.Branch) {
	if !env.Cfg.GitHubStacks {
		return
	}
	remote := env.Cfg.Remote
	if remote == "" || !env.Repo.RemoteExists(remote) {
		return
	}
	var chains [][]*stack.Branch
	switch {
	case target == nil:
		chains = unforkedChains(g)
	case target.Tracked && !target.HasProblem():
		chains = [][]*stack.Branch{linkChain(target)}
	}
	// A run of one branch can never hold the two pull requests a stack needs,
	// so it is not worth a round trip to find that out.
	var worth [][]*stack.Branch
	for _, chain := range chains {
		if len(chain) > 1 {
			worth = append(worth, chain)
		}
	}
	if len(worth) == 0 {
		return
	}
	if env.DryRun {
		env.Out.Dry("would check the stacks on GitHub and link any that are not linked there yet")
		return
	}
	gh, err := openForge(env, remote)
	if err != nil {
		env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
		return
	}
	if err := requireStackExtension(gh); err != nil {
		env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
		return
	}
	prs := newPullRequestCache()
	for _, chain := range worth {
		if _, err := linkPullRequestChain(env, gh, chain, prs, "%s are a stack on GitHub"); err != nil {
			// One repository-wide answer: a link refused for one stack is
			// refused for every other, and the reason is worth reading once.
			env.Out.Warnf("Stacks on GitHub not updated: %v", firstLine(err))
			return
		}
	}
}

// pullGitHubStacks brings back what has changed on GitHub in the stacks linked
// there: pull requests merged, and branches added on top of the stack — in the
// browser, by a colleague, or from another clone.
//
// It reads each stack gh stack's tracking holds a number for and compares the
// branches still under review on both sides. Where this checkout's are the
// bottom of GitHub's, the branches above are created from the remote sync has
// just fetched, and recorded in gh stack's tracking, so the adoption that
// follows tracks each with the parent GitHub has for it. Any other difference
// is reported and left alone: for a branch stk tracks, its graph is the
// record.
//
// It reports whether gh stack's tracking was rewritten. Nothing here is fatal:
// a forge stk cannot reach only means the check waits for the next run.
func pullGitHubStacks(env *Env) bool {
	if !env.Cfg.GitHubStacks {
		return false
	}
	remote := env.Cfg.Remote
	if remote == "" || !env.Repo.RemoteExists(remote) {
		return false
	}
	// A file stk cannot read is stk doctor's to report.
	f, err := ghstack.Load(env.Repo.CommonDir)
	if err != nil {
		return false
	}
	linked := false
	for _, s := range f.Stacks {
		if s.Number > 0 {
			linked = true
			break
		}
	}
	if !linked {
		return false
	}
	if env.DryRun {
		env.Out.Dry("would check the stacks on GitHub for changes made there")
		return false
	}
	gh, err := openForge(env, remote)
	if err != nil {
		env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
		return false
	}
	changed := false
	for i := range f.Stacks {
		s := &f.Stacks[i]
		if s.Number == 0 {
			continue
		}
		remoteStack, err := gh.Stack(s.Number)
		if err != nil {
			// One repository-wide answer, as for the link.
			env.Out.Warnf("Not checking the stacks on GitHub: %v", firstLine(err))
			break
		}
		if remoteStack == nil {
			// Unstacked on GitHub. The link at the end of the sync is what
			// decides whether it is stacked again.
			continue
		}
		if pullGitHubStack(env, s, remoteStack) {
			changed = true
		}
	}
	if !changed {
		return false
	}
	if err := ghstack.Save(env.Repo.CommonDir, f); err != nil {
		env.Out.Warnf("gh stack tracking not updated: %v", err)
		return false
	}
	return true
}

// pullGitHubStack brings one stack of gh stack's tracking into step with the
// same stack on GitHub, and reports whether it changed it.
//
// Merged pull requests are left out of the comparison on both sides, so a
// branch already landed, and perhaps already removed here, is not a
// difference. A closed one is: it is still in the stack, just not merged.
func pullGitHubStack(env *Env, s *ghstack.Stack, remoteStack *forge.Stack) bool {
	repo := env.Repo
	remote := env.Cfg.Remote
	changed := false

	merged := map[string]bool{}
	var remoteActive []forge.PullRequest
	for _, pr := range remoteStack.PullRequests {
		if pr.IsMerged() {
			merged[pr.Head] = true
			continue
		}
		remoteActive = append(remoteActive, pr)
	}
	var localActive []string
	for j := range s.Branches {
		ref := &s.Branches[j]
		if merged[ref.Branch] && (ref.PullRequest == nil || !ref.PullRequest.Merged) {
			if ref.PullRequest == nil {
				ref.PullRequest = &ghstack.PullRequest{}
			}
			for _, pr := range remoteStack.PullRequests {
				if pr.Head == ref.Branch {
					ref.PullRequest.Number, ref.PullRequest.URL = pr.Number, pr.URL
				}
			}
			ref.PullRequest.Merged = true
			env.Out.OK("%s merged on GitHub", output.BranchName(ref.Branch))
			changed = true
		}
		if ref.PullRequest == nil || !ref.PullRequest.Merged {
			localActive = append(localActive, ref.Branch)
		}
	}

	if len(localActive) > len(remoteActive) {
		// Grown here; the link at the end of the sync takes it to GitHub.
		return changed
	}
	for i, name := range localActive {
		if remoteActive[i].Head != name {
			var theirs []string
			for _, pr := range remoteActive {
				theirs = append(theirs, pr.Head)
			}
			env.Out.Skip("stack #%d on GitHub is %s, but here it is %s; left as it is",
				s.Number, strings.Join(theirs, ", "), strings.Join(localActive, ", "))
			return changed
		}
	}

	// GitHub's stack carries on above this checkout's: those branches are
	// fetched and recorded on top, one by one, until one cannot be.
	for _, pr := range remoteActive[len(localActive):] {
		name := pr.Head
		if !pr.IsOpen() {
			env.Out.Skip("%s on stack #%d on GitHub is %s closed; not pulled, nor anything above it", output.BranchName(name), s.Number, pr.String())
			break
		}
		if repo.BranchExists(name) {
			// Adopting it would claim it holds what GitHub has, which nothing
			// here proves.
			env.Out.Skip("%s is on stack #%d on GitHub, but a local branch of that name already exists; not pulled, nor anything above it", output.BranchName(name), s.Number)
			break
		}
		if !repo.Exists("refs/remotes/" + remote + "/" + name) {
			if err := repo.FetchBranch(remote, name); err != nil {
				env.Out.Skip("%s is on stack #%d on GitHub, but could not be fetched from %s; not pulled, nor anything above it", output.BranchName(name), s.Number, remote)
				break
			}
		}
		if err := repo.CreateTrackingBranch(name, remote); err != nil {
			env.Out.Fail("could not create %s from %s: %v", name, remote, err)
			break
		}
		s.Branches = append(s.Branches, ghstack.BranchRef{
			Branch:      name,
			PullRequest: &ghstack.PullRequest{Number: pr.Number, URL: pr.URL},
		})
		env.Out.OK("Pulled %s (%s) from stack #%d on GitHub", output.BranchName(name), pr.String(), s.Number)
		changed = true
	}
	return changed
}

// prNumbers writes pull request numbers the way GitHub does: #1, #2, #3.
func prNumbers(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = fmt.Sprintf("#%d", n)
	}
	return strings.Join(parts, ", ")
}

// indent offsets a relayed message so it reads as quoted rather than as stk's
// own words.
func indent(msg string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(msg), "\n") {
		out = append(out, "    "+line)
	}
	return strings.Join(out, "\n")
}
