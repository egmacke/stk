// Package operations implements the multi-branch stk operations: create,
// track, rename, move, restack, sync and their continue/abort handling.
package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"stk/internal/git"
	"stk/internal/stack"
)

// Scope names how much of the graph an operation covers.
type Scope string

const (
	// ScopeStack is the whole stack containing the target branch.
	ScopeStack Scope = "stack"
	// ScopeUp is the target branch and its descendants.
	ScopeUp Scope = "up"
	// ScopeOnly is the target branch alone.
	ScopeOnly Scope = "only"
	// ScopeAll is every tracked stack in the repository.
	ScopeAll Scope = "all"
)

// Step is one branch in an operation plan.
type Step struct {
	BranchID string `json:"branchId"`
	Name     string `json:"name"`
}

// Operation is the persisted journal of an in-flight multi-branch operation.
//
// It records the owning worktree, because only that worktree may continue or
// abort it.
type Operation struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Scope            Scope             `json:"scope"`
	Worktree         string            `json:"worktree"`
	OriginalBranch   string            `json:"originalBranch"`
	OriginalBranchID string            `json:"originalBranchId"`
	Branches         []Step            `json:"branches"`
	Position         int               `json:"position"`
	Snapshots        map[string]string `json:"snapshots"`
	PrevBases        map[string]string `json:"prevBases"`
	RebaseMerges     bool              `json:"rebaseMerges"`
	// Blocked holds branch ids whose descendants cannot be processed safely.
	Blocked []string `json:"blocked,omitempty"`
	// Done carries outcome counts across a conflict pause.
	Done Summary `json:"done"`
	// DoneMessage is the wording used when the operation finishes cleanly.
	DoneMessage string `json:"doneMessage,omitempty"`
	// Cleanup carries sync work that must still happen after the restack
	// portion of a sync operation finishes.
	SyncRestackRemaining bool `json:"syncRestackRemaining,omitempty"`
	// AutostashSHA and AutostashRef record changes parked for the duration of
	// the operation. They live in the journal so a conflict pause hands them
	// to stk continue and stk abort rather than losing track of them.
	AutostashSHA   string `json:"autostashSha,omitempty"`
	AutostashRef   string `json:"autostashRef,omitempty"`
	AutostashLabel string `json:"autostashLabel,omitempty"`
}

// Autostash returns the changes parked for this operation, or nil when the
// working tree was clean.
func (op *Operation) Autostash() *Autostash {
	if op.AutostashSHA == "" {
		return nil
	}
	return &Autostash{SHA: op.AutostashSHA, Ref: op.AutostashRef, Label: op.AutostashLabel}
}

// AdoptAutostash records a parked stash as belonging to this operation.
func (op *Operation) AdoptAutostash(a *Autostash) {
	if a == nil {
		return
	}
	op.AutostashSHA, op.AutostashRef, op.AutostashLabel = a.SHA, a.Ref, a.Label
}

// ErrNoOperation is returned when no stk operation is in flight.
var ErrNoOperation = errors.New("no stk operation in progress")

func operationsDir(repo *git.Repo) string {
	return filepath.Join(repo.StkDir(), "operations")
}

func journalPath(repo *git.Repo) string {
	return filepath.Join(operationsDir(repo), "current.json")
}

// NewOperationID returns a ref-safe identifier for a new operation.
func NewOperationID() (string, error) { return stack.NewID() }

// LoadOperation reads the in-flight operation, if any.
func LoadOperation(repo *git.Repo) (*Operation, error) {
	data, err := os.ReadFile(journalPath(repo))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoOperation
		}
		return nil, err
	}
	var op Operation
	if err := json.Unmarshal(data, &op); err != nil {
		return nil, fmt.Errorf("stk operation journal is corrupt (%s): %w", journalPath(repo), err)
	}
	if op.Snapshots == nil {
		op.Snapshots = map[string]string{}
	}
	if op.PrevBases == nil {
		op.PrevBases = map[string]string{}
	}
	return &op, nil
}

// SaveOperation writes the journal atomically, so an interrupted write cannot
// leave an unreadable journal behind.
func (op *Operation) Save(repo *git.Repo) error {
	dir := operationsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "current-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), journalPath(repo))
}

// Clear removes the journal and every snapshot ref the operation created.
func (op *Operation) Clear(repo *git.Repo) error {
	for id := range op.Snapshots {
		if err := repo.DeleteRef(stack.SnapshotRef(op.ID, id)); err != nil {
			return err
		}
	}
	if err := os.Remove(journalPath(repo)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// OwnedByThisWorktree reports whether the current worktree started the
// operation.
func (op *Operation) OwnedByThisWorktree(repo *git.Repo) bool {
	return filepath.Clean(op.Worktree) == filepath.Clean(repo.Root)
}

// Snapshot records a branch's tip and current base before either is rewritten.
func (op *Operation) Snapshot(repo *git.Repo, b *stack.Branch) error {
	if _, done := op.Snapshots[b.ID]; done {
		return nil
	}
	if err := repo.UpdateRef(stack.SnapshotRef(op.ID, b.ID), b.SHA); err != nil {
		return err
	}
	op.Snapshots[b.ID] = b.SHA
	op.PrevBases[b.ID] = b.Base
	return nil
}
