package operations

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"stk/internal/git"
	"stk/internal/output"
	"stk/internal/stack"
)

// Outcome is what happened to one branch during a restack.
type Outcome string

const (
	OutcomeCurrent   Outcome = "current"
	OutcomeRestacked Outcome = "restacked"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeBlocked   Outcome = "blocked"
)

// Summary counts the outcomes of a restack run.
type Summary struct {
	Current   int `json:"current"`
	Restacked int `json:"restacked"`
	Skipped   int `json:"skipped"`
	Blocked   int `json:"blocked"`
}

// Changed reports whether any branch was rewritten.
func (s Summary) Changed() bool { return s.Restacked > 0 }

// PlanBranches returns the branches a restack of the given scope would visit,
// always parents before children.
func PlanBranches(g *stack.Graph, target *stack.Branch, scope Scope) ([]*stack.Branch, error) {
	switch scope {
	case ScopeAll:
		var out []*stack.Branch
		for _, root := range g.Roots() {
			out = append(out, stack.Subtree(root)...)
		}
		return out, nil
	case ScopeOnly:
		if target == nil || target.IsTrunk {
			return nil, errors.New("stk restack --only needs a tracked branch; trunk is never restacked")
		}
		return []*stack.Branch{target}, nil
	case ScopeUp:
		if target == nil {
			return nil, errNoCurrentBranch
		}
		if target.IsTrunk {
			// Everything above trunk is a descendant of trunk.
			return PlanBranches(g, nil, ScopeAll)
		}
		return stack.Subtree(target), nil
	case ScopeStack:
		if target == nil {
			return nil, errNoCurrentBranch
		}
		if target.IsTrunk {
			return PlanBranches(g, nil, ScopeAll)
		}
		root := g.Root(target)
		if root == nil {
			return nil, fmt.Errorf("cannot determine the stack containing %s", target.Name)
		}
		return stack.Subtree(root), nil
	}
	return nil, fmt.Errorf("unknown restack scope %q", scope)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var errNoCurrentBranch = errors.New("no branch is checked out; specify a branch or check one out first")

// RestackOptions configures a restack run.
type RestackOptions struct {
	Scope        Scope
	RebaseMerges bool
	// Heading is printed before the per-branch results.
	Heading string
	// DoneMessage is printed when nothing needed changing.
	DoneMessage string
}

// Restack makes a portion of the stack graph internally consistent by rebasing
// each branch onto its logical parent, parents before children.
func Restack(env *Env, g *stack.Graph, target *stack.Branch, opts RestackOptions) (Summary, error) {
	plan, err := PlanBranches(g, target, opts.Scope)
	if err != nil {
		return Summary{}, err
	}
	if len(plan) == 0 {
		env.Out.Printf("No tracked branches to restack.")
		return Summary{}, nil
	}
	if env.DryRun {
		PrintDryRun(env, g, plan)
		return Summary{}, nil
	}
	if err := requireCleanTree(env); err != nil {
		return Summary{}, err
	}
	if err := requireNoOperation(env); err != nil {
		return Summary{}, err
	}

	id, err := NewOperationID()
	if err != nil {
		return Summary{}, err
	}
	op := &Operation{
		ID:             id,
		Type:           "restack",
		Scope:          opts.Scope,
		Worktree:       env.Repo.Root,
		OriginalBranch: g.CurrentName,
		Snapshots:      map[string]string{},
		PrevBases:      map[string]string{},
		RebaseMerges:   opts.RebaseMerges,
		DoneMessage:    opts.DoneMessage,
	}
	if g.Current != nil {
		op.OriginalBranchID = g.Current.ID
	}
	for _, b := range plan {
		op.Branches = append(op.Branches, Step{BranchID: b.ID, Name: b.Name})
	}

	if opts.Heading != "" {
		env.Out.Printf("%s", opts.Heading)
		env.Out.Printf("")
	}
	return runPlan(env, op, g, 0)
}

// runPlan executes the journalled plan from index start onwards. Outcome
// counts and the blocked set are carried in the journal so a plan paused by a
// conflict resumes with the same knowledge it had before.
func runPlan(env *Env, op *Operation, g *stack.Graph, start int) (Summary, error) {
	sum := op.Done
	blocked := map[string]bool{}
	for _, id := range op.Blocked {
		blocked[id] = true
	}

	for i := start; i < len(op.Branches); i++ {
		step := op.Branches[i]
		op.Position = i
		op.Done = sum
		op.Blocked = sortedKeys(blocked)
		b := g.ByID[step.BranchID]
		if b == nil {
			env.Out.Skip("%s no longer tracked; skipped", step.Name)
			blocked[step.BranchID] = true
			sum.Skipped++
			continue
		}
		outcome, err := restackOne(env, op, b, blocked)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return sum, err
			}
			// A hard stop: report, undo nothing that already succeeded, and
			// leave no journal behind so the repository is not wedged.
			_ = op.Clear(env.Repo)
			restoreOriginal(env, op)
			return sum, err
		}
		switch outcome {
		case OutcomeCurrent:
			sum.Current++
			env.Out.OK("%s", b.Name)
		case OutcomeRestacked:
			sum.Restacked++
			env.Out.OK("%s", b.Name)
		case OutcomeSkipped:
			sum.Skipped++
		case OutcomeBlocked:
			sum.Blocked++
		}
	}

	restoreOriginal(env, op)
	if err := op.Clear(env.Repo); err != nil {
		return sum, err
	}
	env.Out.Printf("")
	if sum.Restacked == 0 && sum.Skipped == 0 && sum.Blocked == 0 {
		doneMessage := op.DoneMessage
		if doneMessage == "" {
			doneMessage = "Stack is up to date."
		}
		env.Out.Printf("%s", doneMessage)
	} else {
		env.Out.Printf("%s", summarise(sum))
	}
	return sum, nil
}

func summarise(s Summary) string {
	var parts []string
	if s.Restacked > 0 {
		parts = append(parts, fmt.Sprintf("%d restacked", s.Restacked))
	}
	if s.Current > 0 {
		parts = append(parts, fmt.Sprintf("%d already current", s.Current))
	}
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}
	if s.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", s.Blocked))
	}
	if len(parts) == 0 {
		return "Nothing to do."
	}
	return strings.Join(parts, ", ") + "."
}

// restackOne brings a single branch onto its parent's current tip.
func restackOne(env *Env, op *Operation, b *stack.Branch, blocked map[string]bool) (Outcome, error) {
	repo := env.Repo

	if b.HasProblem() {
		blocked[b.ID] = true
		env.Out.Skip("%s blocked: %s", b.Name, problemText(b))
		return OutcomeBlocked, nil
	}
	parent := b.Parent
	if parent == nil {
		blocked[b.ID] = true
		env.Out.Skip("%s blocked: no logical parent", b.Name)
		return OutcomeBlocked, nil
	}
	if blocked[parent.ID] {
		blocked[b.ID] = true
		env.Out.Skip("%s blocked: %s could not be processed", b.Name, parent.Name)
		return OutcomeBlocked, nil
	}

	newBase := parent.SHA
	if newBase == "" {
		blocked[b.ID] = true
		env.Out.Skip("%s blocked: parent %s does not exist locally", b.Name, parent.Name)
		return OutcomeBlocked, nil
	}
	childTip := b.SHA

	// Already contains the parent's tip: nothing to rewrite, but the recorded
	// base may be stale.
	if repo.IsAncestor(newBase, childTip) {
		if b.Base != newBase {
			if err := stack.SetBase(repo, b.ID, newBase); err != nil {
				return "", err
			}
			b.Base = newBase
		}
		return OutcomeCurrent, nil
	}

	// From here the branch must be rewritten, so a worktree holding it
	// elsewhere makes it untouchable and its descendants unverifiable.
	if b.CheckedOutElsewhere() {
		blocked[b.ID] = true
		env.Out.Skip("%s skipped: checked out in %s\n  Run stk restack from that worktree.", b.Name, b.Worktree)
		return OutcomeSkipped, nil
	}

	oldBase := b.Base
	if !repo.IsAncestor(oldBase, childTip) {
		return "", fmt.Errorf(
			"cannot restack %s safely\n\n"+
				"Its recorded base %s is no longer part of the branch's history,\n"+
				"so stk cannot tell which commits belong to %s.\n\n"+
				"Repair it with:\n\n    stk track %s --parent %s",
			b.Name, git.ShortSHA(oldBase), b.Name, b.Name, parent.Name)
	}

	if !op.RebaseMerges && repo.HasMergeCommits(oldBase, childTip) {
		return "", fmt.Errorf(
			"%s contains merge commits between %s and its tip\n\n"+
				"stk will not flatten them silently. Re-run with:\n\n    stk restack --rebase-merges",
			b.Name, git.ShortSHA(oldBase))
	}

	if err := op.Snapshot(repo, b); err != nil {
		return "", err
	}
	if err := op.Save(repo); err != nil {
		return "", err
	}

	res := repo.Rebase(newBase, oldBase, b.Name, op.RebaseMerges)
	if !res.OK() {
		if repo.RebaseInProgress() {
			if err := op.Save(repo); err != nil {
				return "", err
			}
			// Never hide git's own account of the conflict.
			echoGit(env, res)
			printConflict(env, b, parent)
			return "", ErrConflict
		}
		echoGit(env, res)
		return "", fmt.Errorf("rebasing %s onto %s failed: %w", b.Name, parent.Name, res.Error())
	}

	if err := finishBranch(env, b, newBase); err != nil {
		return "", err
	}
	return OutcomeRestacked, nil
}

// finishBranch records the new tip and base after a successful rebase.
func finishBranch(env *Env, b *stack.Branch, newBase string) error {
	tip, err := resolveHead(env.Repo, b)
	if err != nil {
		return err
	}
	b.SHA = tip
	if err := stack.SetBase(env.Repo, b.ID, newBase); err != nil {
		return err
	}
	b.Base = newBase
	return nil
}

func problemText(b *stack.Branch) string {
	switch {
	case b.DuplicateID:
		return "another branch claims the same stk id; run stk doctor"
	case b.Orphaned:
		return "its logical parent no longer exists; re-track it"
	case b.InCycle:
		return "its parent chain contains a cycle; run stk doctor"
	case b.BaseMissing:
		return "its base ref is missing; re-track it"
	}
	return "metadata problem"
}

// echoGit relays git's captured output so the user sees exactly what git said.
func echoGit(env *Env, res git.Result) {
	for _, chunk := range []string{res.Stdout, res.Stderr} {
		if text := strings.TrimRight(chunk, "\n"); text != "" {
			env.Out.Warnf("%s", text)
		}
	}
}

func printConflict(env *Env, child, parent *stack.Branch) {
	p := env.Out
	p.Warnf("")
	p.Warnf("Restacking %s onto %s", child.Name, parent.Name)
	p.Warnf("")
	p.Warnf("Conflict encountered.")
	p.Warnf("")
	p.Warnf("Resolve the conflicts and run:")
	p.Warnf("")
	p.Warnf("    stk continue")
	p.Warnf("")
	p.Warnf("Or abort the operation with:")
	p.Warnf("")
	p.Warnf("    stk abort")
	p.Warnf("")
}

// restoreOriginal returns to the branch the user started on, where git allows.
func restoreOriginal(env *Env, op *Operation) {
	if op.OriginalBranch == "" {
		return
	}
	if env.Repo.CurrentBranch() == op.OriginalBranch {
		return
	}
	if !env.Repo.BranchExists(op.OriginalBranch) {
		return
	}
	if err := env.Repo.Switch(op.OriginalBranch); err != nil {
		env.Out.Warnf("%s could not return to %s: %v", output.SymFailed, op.OriginalBranch, err)
	}
}

// PrintDryRun shows the plan a restack would follow without touching anything.
func PrintDryRun(env *Env, g *stack.Graph, plan []*stack.Branch) {
	p := env.Out
	willRewrite := map[string]bool{}
	type row struct{ name, action string }
	var rows []row
	width := 0
	for _, b := range plan {
		var action string
		switch {
		case b.HasProblem():
			action = "blocked: " + problemText(b)
		case b.Parent == nil:
			action = "blocked: no logical parent"
		case willRewrite[b.Parent.ID] && b.CheckedOutElsewhere():
			action = "skipped: checked out in " + b.Worktree
		case b.NeedsRestack() && b.CheckedOutElsewhere():
			action = "skipped: checked out in " + b.Worktree
		case b.NeedsRestack() || willRewrite[b.Parent.ID]:
			action = "rebase onto " + b.Parent.Name
			willRewrite[b.ID] = true
		default:
			action = "no changes required"
		}
		if len(b.Name) > width {
			width = len(b.Name)
		}
		rows = append(rows, row{b.Name, action})
	}
	p.Printf("Plan:")
	p.Printf("")
	for _, r := range rows {
		p.Printf("  %-*s   %s", width, r.name, r.action)
	}
	p.Printf("")
	p.Printf("No changes have been made.")
}
