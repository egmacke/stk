package git

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotRepository is returned when the working directory is not inside a git
// repository.
var ErrNotRepository = errors.New("not a git repository")

// Repo describes the git repository containing a working directory.
//
// CommonDir is the shared directory of the repository. Linked worktrees have
// their own GitDir but share CommonDir, so all repository-wide stk state hangs
// off CommonDir.
type Repo struct {
	R *Runner

	// Dir is the directory stk was asked to operate in.
	Dir string
	// Root is the top level of the current worktree.
	Root string
	// GitDir is this worktree's git directory.
	GitDir string
	// CommonDir is the repository-wide git directory, shared by all worktrees.
	CommonDir string

	version [2]int
}

// Discover locates the repository containing dir.
func Discover(dir string, verbose, dryRun bool) (*Repo, error) {
	r := NewRunner(dir)
	r.Verbose = verbose
	r.DryRun = dryRun

	res := r.Run("rev-parse", "--git-dir", "--git-common-dir", "--show-toplevel")
	if !res.OK() {
		if strings.Contains(res.Stderr, "not a git repository") {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, dir)
		}
		return nil, res.Error()
	}
	lines := res.Lines()
	if len(lines) < 3 {
		return nil, fmt.Errorf("%w: %s", ErrNotRepository, dir)
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Clean(filepath.Join(dir, p))
	}
	repo := &Repo{
		R:         r,
		Dir:       dir,
		GitDir:    abs(lines[0]),
		CommonDir: abs(lines[1]),
		Root:      filepath.Clean(lines[2]),
	}
	return repo, nil
}

// StkDir is the repository-wide directory holding stk state that does not fit
// in git config or refs.
func (repo *Repo) StkDir() string { return filepath.Join(repo.CommonDir, "stk") }

// Version returns the installed git version as major, minor.
func (repo *Repo) Version() (int, int) {
	if repo.version[0] != 0 {
		return repo.version[0], repo.version[1]
	}
	res := repo.R.Run("version")
	if res.OK() {
		fields := strings.Fields(res.Out())
		if len(fields) >= 3 {
			parts := strings.Split(fields[2], ".")
			if len(parts) >= 2 {
				major, _ := strconv.Atoi(parts[0])
				minor, _ := strconv.Atoi(parts[1])
				repo.version = [2]int{major, minor}
			}
		}
	}
	if repo.version[0] == 0 {
		repo.version = [2]int{2, 0}
	}
	return repo.version[0], repo.version[1]
}

// AtLeast reports whether the installed git is at least major.minor.
func (repo *Repo) AtLeast(major, minor int) bool {
	ma, mi := repo.Version()
	return ma > major || (ma == major && mi >= minor)
}

// SupportsNoUpdateRefs reports whether "git rebase --no-update-refs" exists.
// stk manages its own refs, so it must opt out of git's rebase.updateRefs.
func (repo *Repo) SupportsNoUpdateRefs() bool { return repo.AtLeast(2, 38) }
