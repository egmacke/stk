// Package ghstack reads and writes the local tracking file of the gh stack
// extension, so stk can keep it in step with its own graph.
//
// The file is gh stack's, not stk's: its shape is gh stack's schema version 1,
// reproduced here field for field, and it is written under the same advisory
// lock gh stack takes, so the two tools never write over each other. stk
// treats it as a projection of its own graph — rewritten after every change
// to the graph — plus the one thing gh stack knows that stk does not record:
// which pull request a branch has.
package ghstack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// FileName is the tracking file, in the git directory.
	FileName = "gh-stack"
	// LockName is the lock file beside it. It is left in place after use, as
	// gh stack leaves it, so that no process ever locks an unlinked inode.
	LockName = "gh-stack.lock"
	// SchemaVersion is the one layout this package understands.
	SchemaVersion = 1
)

// LockTimeout is how long Save waits for gh stack to release the file.
var LockTimeout = 5 * time.Second

const lockRetry = 100 * time.Millisecond

// ErrSchema is returned when the file was written by a newer gh stack.
var ErrSchema = errors.New("gh stack tracking file is newer than stk understands")

// ErrLocked is returned when the lock could not be taken in time.
var ErrLocked = errors.New("gh stack tracking file is locked by another process")

// PullRequest is gh stack's record of a branch's pull request.
type PullRequest struct {
	Number int    `json:"number"`
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

// BranchRef is one branch in a stack. Head is recorded for the trunk; Base,
// for every other branch, is the parent's tip the branch was last rebased
// onto — the same fact stk keeps in refs/stk/base.
type BranchRef struct {
	Branch      string       `json:"branch"`
	Head        string       `json:"head,omitempty"`
	Base        string       `json:"base,omitempty"`
	PullRequest *PullRequest `json:"pullRequest,omitempty"`
}

// Stack is one linear stack, bottom first.
type Stack struct {
	ID       string      `json:"id,omitempty"`
	Number   int         `json:"number,omitempty"`
	Trunk    BranchRef   `json:"trunk"`
	Branches []BranchRef `json:"branches"`
}

// Names lists the branches of the stack, bottom first.
func (s *Stack) Names() []string {
	out := make([]string, len(s.Branches))
	for i, b := range s.Branches {
		out[i] = b.Branch
	}
	return out
}

// Contains reports whether the stack holds branch, trunk excluded.
func (s *Stack) Contains(branch string) bool {
	for _, b := range s.Branches {
		if b.Branch == branch {
			return true
		}
	}
	return false
}

// File is the whole tracking file.
type File struct {
	SchemaVersion int     `json:"schemaVersion"`
	Repository    string  `json:"repository"`
	Stacks        []Stack `json:"stacks"`
}

// Path is where the file lives for a git directory.
func Path(gitDir string) string { return filepath.Join(gitDir, FileName) }

// Load reads the file. A missing file is an empty one.
func Load(gitDir string) (*File, error) {
	data, err := os.ReadFile(Path(gitDir))
	if errors.Is(err, os.ErrNotExist) {
		return &File{SchemaVersion: SchemaVersion, Stacks: []Stack{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", Path(gitDir), err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", Path(gitDir), err)
	}
	if f.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("%w (schema version %d)", ErrSchema, f.SchemaVersion)
	}
	if f.Stacks == nil {
		f.Stacks = []Stack{}
	}
	return &f, nil
}

// Exists reports whether gh stack has ever written a tracking file here.
func Exists(gitDir string) bool {
	_, err := os.Stat(Path(gitDir))
	return err == nil
}

// Marshal renders the file exactly as gh stack writes it, so a file stk has
// rewritten without changing anything is byte for byte the same file.
func Marshal(f *File) ([]byte, error) {
	f.SchemaVersion = SchemaVersion
	if f.Stacks == nil {
		f.Stacks = []Stack{}
	}
	return json.MarshalIndent(f, "", "  ")
}

// Equal reports whether two files would be written identically.
func Equal(a, b *File) bool {
	x, err1 := Marshal(a)
	y, err2 := Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// Save writes the file under gh stack's lock.
//
// The lock is gh stack's own protocol: an exclusive flock on gh-stack.lock,
// tried without blocking every hundred milliseconds until the timeout, so a
// gh stack command running at the same moment is waited for rather than
// raced.
func Save(gitDir string, f *File) error {
	data, err := Marshal(f)
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(gitDir, LockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Join(gitDir, LockName), err)
	}
	defer lock.Close()
	deadline := time.Now().Add(LockTimeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("locking %s: %w", filepath.Join(gitDir, LockName), err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w\n\nWait for it to finish, then try again", ErrLocked)
		}
		time.Sleep(lockRetry)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if err := os.WriteFile(Path(gitDir), data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", Path(gitDir), err)
	}
	return nil
}

// Find returns the stack holding branch and the branch's own entry, or nils.
func (f *File) Find(branch string) (*Stack, *BranchRef) {
	for i := range f.Stacks {
		for j := range f.Stacks[i].Branches {
			if f.Stacks[i].Branches[j].Branch == branch {
				return &f.Stacks[i], &f.Stacks[i].Branches[j]
			}
		}
	}
	return nil, nil
}
