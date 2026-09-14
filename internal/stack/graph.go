package stack

import (
	"sort"
	"sync"

	"github.com/egmacke/stk/internal/config"
	"github.com/egmacke/stk/internal/git"
)

// TrunkID identifies the synthetic trunk node. It is not a valid stk id, so it
// can never collide with a real branch.
const TrunkID = "@trunk"

// PullRequest is what gh stack's tracking records about a branch's pull
// request. stk stores nothing about pull requests itself; this is read from
// gh stack's file when the repository has opted into GitHub stacks, and is
// nil otherwise.
type PullRequest struct {
	Number int
	URL    string
	Merged bool
}

// Branch is one node of the stack graph.
type Branch struct {
	ID   string
	Name string
	SHA  string

	// ParentID is the stable id of the logical parent, or "" for trunk.
	ParentID string
	Parent   *Branch
	Children []*Branch

	// Base is the commit of the parent this branch was last valid against.
	Base string

	Upstream     string
	Ahead        int
	Behind       int
	UpstreamGone bool

	// Worktree is the path of the worktree that has this branch checked out.
	Worktree  string
	IsCurrent bool

	IsTrunk bool
	Tracked bool

	// PR is the pull request gh stack's tracking knows for the branch, when
	// the repository keeps that tracking in step with stk.
	PR *PullRequest

	// Problems recorded during reconciliation.
	Orphaned    bool // logical parent no longer exists
	InCycle     bool // parent chain loops
	DuplicateID bool // another branch claims the same stk id
	BaseMissing bool // tracked branch with no protected base ref

	needsRestack bool
}

// HasUpstream reports whether the branch has a configured upstream.
func (b *Branch) HasUpstream() bool { return b.Upstream != "" }

// NeedsRestack reports whether the branch must be rebased onto its parent.
func (b *Branch) NeedsRestack() bool { return b.needsRestack }

// HasProblem reports whether reconciliation found a metadata fault.
func (b *Branch) HasProblem() bool {
	return b.Orphaned || b.InCycle || b.DuplicateID || b.BaseMissing
}

// CheckedOutElsewhere reports whether another worktree holds this branch.
func (b *Branch) CheckedOutElsewhere() bool { return b.Worktree != "" && !b.IsCurrent }

// Graph is the whole stack model for a repository.
type Graph struct {
	Trunk  *Branch
	ByID   map[string]*Branch
	ByName map[string]*Branch

	// Tracked lists tracked branches sorted by name.
	Tracked []*Branch
	// Untracked lists local branches with no stk metadata, trunk excluded.
	Untracked []*Branch
	// Orphans lists tracked branches whose parent could not be resolved. They
	// are deliberately not attached to the tree.
	Orphans []*Branch

	Current     *Branch
	CurrentName string

	Config config.Config

	repo      *git.Repo
	dirtyOnce sync.Once
	dirty     bool
}

// Dirty reports whether the current worktree has uncommitted changes.
//
// It is computed on first use rather than during Load, because git status is
// the most expensive query in the load path and most commands never need it.
func (g *Graph) Dirty() bool {
	g.dirtyOnce.Do(func() {
		if g.repo == nil {
			return
		}
		clean, err := g.repo.IsClean()
		g.dirty = err == nil && !clean
	})
	return g.dirty
}

// Load builds the stack model. It uses a fixed, small number of git
// invocations regardless of how many branches the repository has.
func Load(repo *git.Repo, cfg config.Config) (*Graph, error) {
	branches, err := repo.Branches()
	if err != nil {
		return nil, err
	}
	meta, err := ReadMeta(repo)
	if err != nil {
		return nil, err
	}
	bases, err := ReadBases(repo)
	if err != nil {
		return nil, err
	}
	worktrees, err := repo.BranchWorktrees()
	if err != nil {
		return nil, err
	}

	g := &Graph{
		ByID:   map[string]*Branch{},
		ByName: map[string]*Branch{},
		Config: cfg,
		repo:   repo,
	}

	idOwners := map[string][]string{}
	for name, m := range meta {
		if name == cfg.Trunk {
			continue
		}
		idOwners[m.ID] = append(idOwners[m.ID], name)
	}

	for _, info := range branches {
		b := &Branch{
			Name:         info.Name,
			SHA:          info.SHA,
			Upstream:     info.Upstream,
			Ahead:        info.Ahead,
			Behind:       info.Behind,
			UpstreamGone: info.UpstreamGone,
			Worktree:     worktrees[info.Name],
			IsCurrent:    info.IsHead,
		}
		if info.Name == cfg.Trunk {
			b.IsTrunk = true
			b.ID = TrunkID
			g.Trunk = b
			g.ByName[b.Name] = b
			g.ByID[TrunkID] = b
			continue
		}
		if m, ok := meta[info.Name]; ok {
			b.Tracked = true
			b.ID = m.ID
			b.ParentID = m.ParentID
			b.DuplicateID = len(idOwners[m.ID]) > 1
			if base, ok := bases[m.ID]; ok {
				b.Base = base
			} else {
				b.BaseMissing = true
			}
			g.Tracked = append(g.Tracked, b)
			// A duplicated id would otherwise silently shadow a branch.
			if _, taken := g.ByID[m.ID]; !taken {
				g.ByID[m.ID] = b
			}
		} else {
			g.Untracked = append(g.Untracked, b)
		}
		g.ByName[b.Name] = b
	}

	if g.Trunk == nil {
		// Trunk may not exist locally yet; represent it anyway so the graph
		// still has a root and commands can report the problem.
		g.Trunk = &Branch{Name: cfg.Trunk, ID: TrunkID, IsTrunk: true}
		g.ByID[TrunkID] = g.Trunk
		g.ByName[cfg.Trunk] = g.Trunk
	}

	sortByName(g.Tracked)
	sortByName(g.Untracked)

	g.link()
	g.markUnreachable()
	g.computeRestack(repo)

	g.CurrentName = repo.CurrentBranch()
	if g.CurrentName != "" {
		g.Current = g.ByName[g.CurrentName]
	}
	return g, nil
}

func sortByName(list []*Branch) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
}

// link attaches each tracked branch to its parent, leaving unresolvable ones
// detached and flagged rather than guessing a parent for them.
func (g *Graph) link() {
	for _, b := range g.Tracked {
		if b.DuplicateID {
			b.Orphaned = true
			g.Orphans = append(g.Orphans, b)
			continue
		}
		if b.ParentID == "" {
			b.Parent = g.Trunk
			g.Trunk.Children = append(g.Trunk.Children, b)
			continue
		}
		parent, ok := g.ByID[b.ParentID]
		if !ok || parent == b {
			b.Orphaned = true
			g.Orphans = append(g.Orphans, b)
			continue
		}
		b.Parent = parent
		parent.Children = append(parent.Children, b)
	}
	sortByName(g.Trunk.Children)
	for _, b := range g.Tracked {
		sortByName(b.Children)
	}
	sortByName(g.Orphans)
}

// markUnreachable flags tracked branches that cannot reach trunk by following
// parents, distinguishing a broken parent link from a genuine loop.
func (g *Graph) markUnreachable() {
	reachable := map[*Branch]bool{g.Trunk: true}
	queue := append([]*Branch{}, g.Trunk.Children...)
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if reachable[node] {
			continue
		}
		reachable[node] = true
		queue = append(queue, node.Children...)
	}
	for _, b := range g.Tracked {
		if reachable[b] || b.Orphaned {
			continue
		}
		seen := map[*Branch]bool{}
		cur := b
		for cur != nil && !seen[cur] {
			seen[cur] = true
			cur = cur.Parent
		}
		if cur == nil {
			// The chain ends at a branch whose parent no longer exists.
			b.Orphaned = true
			g.Orphans = append(g.Orphans, b)
		} else {
			b.InCycle = true
		}
	}
	sortByName(g.Orphans)
}

// computeRestack decides which branches are out of date with their parent.
//
// The common case is answered in memory: a branch whose recorded base is still
// the parent's tip cannot need restacking. Only the remainder pay for a git
// ancestry check, and those run concurrently.
func (g *Graph) computeRestack(repo *git.Repo) {
	var pending []*Branch
	for _, b := range g.Tracked {
		if b.Orphaned || b.InCycle || b.Parent == nil || b.BaseMissing {
			continue
		}
		if b.Parent.SHA == "" || b.SHA == "" {
			continue
		}
		if b.Base == b.Parent.SHA {
			continue
		}
		pending = append(pending, b)
	}
	if len(pending) == 0 {
		return
	}
	workers := 8
	if len(pending) < workers {
		workers = len(pending)
	}
	ch := make(chan *Branch)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range ch {
				b.needsRestack = !repo.IsAncestor(b.Parent.SHA, b.SHA)
			}
		}()
	}
	for _, b := range pending {
		ch <- b
	}
	close(ch)
	wg.Wait()
}

// Resolve finds a branch by name.
func (g *Graph) Resolve(name string) (*Branch, bool) {
	b, ok := g.ByName[name]
	return b, ok
}

// Roots returns the bottom-most tracked branches, those sitting on trunk.
func (g *Graph) Roots() []*Branch { return g.Trunk.Children }

// Root returns the bottom-most branch of the stack containing b.
func (g *Graph) Root(b *Branch) *Branch {
	if b == nil || b.IsTrunk {
		return nil
	}
	cur := b
	seen := map[*Branch]bool{cur: true}
	for cur.Parent != nil && !cur.Parent.IsTrunk && !seen[cur.Parent] {
		cur = cur.Parent
		seen[cur] = true
	}
	return cur
}

// Subtree returns b and every descendant, parents always before children.
func Subtree(b *Branch) []*Branch {
	if b == nil {
		return nil
	}
	var out []*Branch
	seen := map[*Branch]bool{}
	queue := []*Branch{b}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if seen[node] {
			continue
		}
		seen[node] = true
		out = append(out, node)
		queue = append(queue, node.Children...)
	}
	return out
}

// Ancestors returns the chain from b's parent up to, but excluding, trunk.
func Ancestors(b *Branch) []*Branch {
	var out []*Branch
	seen := map[*Branch]bool{b: true}
	for cur := b.Parent; cur != nil && !cur.IsTrunk && !seen[cur]; cur = cur.Parent {
		seen[cur] = true
		out = append(out, cur)
	}
	return out
}

// Leaves returns the branches in b's subtree that have no children.
func Leaves(b *Branch) []*Branch {
	var out []*Branch
	for _, node := range Subtree(b) {
		if len(node.Children) == 0 {
			out = append(out, node)
		}
	}
	return out
}

// IsDescendant reports whether candidate is b or below b in the graph.
func IsDescendant(b, candidate *Branch) bool {
	seen := map[*Branch]bool{}
	for cur := candidate; cur != nil && !seen[cur]; cur = cur.Parent {
		seen[cur] = true
		if cur == b {
			return true
		}
	}
	return false
}
