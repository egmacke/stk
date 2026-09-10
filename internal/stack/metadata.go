// Package stack holds the stk branch graph: the metadata that records which
// branch is the logical parent of which, and the in-memory model built from it.
package stack

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"stk/internal/git"
)

const (
	// keyID is the git config key holding a branch's stable stk identity.
	keyID = "stk-id"
	// keyParent holds the stable id of the branch's logical parent. It is
	// absent when the parent is trunk.
	keyParent = "stk-parent"

	// BaseRefPrefix protects the commit a child was last valid against, so it
	// stays reachable after the parent's history is rewritten.
	BaseRefPrefix = "refs/stk/base/"
	// SnapshotRefPrefix protects branch tips for the duration of a multi-branch
	// operation, so the whole operation can be rolled back.
	SnapshotRefPrefix = "refs/stk/snapshot/"
	// AutostashRefPrefix holds uncommitted work stk has parked while it moves
	// or rewrites branches. A ref of its own, rather than only the stash
	// reflog, keeps the changes reachable if stk is killed mid-operation.
	AutostashRefPrefix = "refs/stk/autostash/"
)

// NewID returns a fresh stable branch identity.
func NewID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating branch id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// BaseRef is the protected base ref for a branch id.
func BaseRef(id string) string { return BaseRefPrefix + id }

// SnapshotRef is the snapshot ref for a branch within one operation.
func SnapshotRef(opID, branchID string) string {
	return SnapshotRefPrefix + opID + "/" + branchID
}

// AutostashRef is the ref holding one parked set of uncommitted changes.
func AutostashRef(id string) string { return AutostashRefPrefix + id }

// ReadAutostashes returns every parked stash keyed by its id.
func ReadAutostashes(repo *git.Repo) (map[string]string, error) {
	refs, err := repo.ListRefs(AutostashRefPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(refs))
	for ref, sha := range refs {
		out[strings.TrimPrefix(ref, AutostashRefPrefix)] = sha
	}
	return out, nil
}

func branchKey(name, suffix string) string {
	return "branch." + name + "." + suffix
}

// Meta is the persisted stk metadata for one branch.
type Meta struct {
	ID       string
	ParentID string
}

// ReadMeta loads the metadata of every branch in a single git invocation.
func ReadMeta(repo *git.Repo) (map[string]Meta, error) {
	raw, err := repo.ConfigGetRegexp(`^branch\..*\.stk-(id|parent)$`)
	if err != nil {
		return nil, err
	}
	out := map[string]Meta{}
	for key, value := range raw {
		rest, ok := strings.CutPrefix(key, "branch.")
		if !ok {
			continue
		}
		// Branch names may contain dots, so split on the final separator.
		idx := strings.LastIndex(rest, ".")
		if idx < 0 {
			continue
		}
		name, field := rest[:idx], rest[idx+1:]
		m := out[name]
		switch field {
		case keyID:
			m.ID = value
		case keyParent:
			m.ParentID = value
		default:
			continue
		}
		out[name] = m
	}
	// A parent id without an id of its own is not a tracked branch.
	for name, m := range out {
		if m.ID == "" {
			delete(out, name)
		}
	}
	return out, nil
}

// WriteMeta persists a branch's identity and logical parent. An empty parent
// id means the branch sits directly on trunk.
func WriteMeta(repo *git.Repo, name string, m Meta) error {
	if err := repo.ConfigSet(branchKey(name, keyID), m.ID); err != nil {
		return err
	}
	if m.ParentID == "" {
		return repo.ConfigUnset(branchKey(name, keyParent))
	}
	return repo.ConfigSet(branchKey(name, keyParent), m.ParentID)
}

// SetParent updates only the logical parent of a branch.
func SetParent(repo *git.Repo, name, parentID string) error {
	if parentID == "" {
		return repo.ConfigUnset(branchKey(name, keyParent))
	}
	return repo.ConfigSet(branchKey(name, keyParent), parentID)
}

// ClearMeta removes stk metadata from a branch, leaving the git branch itself
// untouched.
func ClearMeta(repo *git.Repo, name string) error {
	if err := repo.ConfigUnset(branchKey(name, keyID)); err != nil {
		return err
	}
	return repo.ConfigUnset(branchKey(name, keyParent))
}

// SetBase points a branch's protected base ref at a commit.
func SetBase(repo *git.Repo, id, commit string) error {
	return repo.UpdateRef(BaseRef(id), commit)
}

// ClearBase removes a branch's protected base ref.
func ClearBase(repo *git.Repo, id string) error {
	return repo.DeleteRef(BaseRef(id))
}

// ReadBases returns every protected base ref keyed by branch id.
func ReadBases(repo *git.Repo) (map[string]string, error) {
	refs, err := repo.ListRefs(BaseRefPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(refs))
	for ref, sha := range refs {
		out[strings.TrimPrefix(ref, BaseRefPrefix)] = sha
	}
	return out, nil
}
