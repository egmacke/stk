package git

import (
	"fmt"
	"strings"
)

// RevParse resolves a revision to a full object id.
func (repo *Repo) RevParse(rev string) (string, error) {
	res := repo.R.Run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if !res.OK() {
		return "", fmt.Errorf("cannot resolve %q", rev)
	}
	out := res.Out()
	if out == "" {
		return "", fmt.Errorf("cannot resolve %q", rev)
	}
	return out, nil
}

// Exists reports whether a revision resolves.
func (repo *Repo) Exists(rev string) bool {
	_, err := repo.RevParse(rev)
	return err == nil
}

// UpdateRef points a ref at a commit.
func (repo *Repo) UpdateRef(ref, value string) error {
	return repo.R.Mutate("update-ref", ref, value).Error()
}

// DeleteRef removes a ref, tolerating one that is already gone.
func (repo *Repo) DeleteRef(ref string) error {
	res := repo.R.Mutate("update-ref", "-d", ref)
	if res.OK() {
		return nil
	}
	if strings.Contains(res.Stderr, "unable to resolve reference") ||
		strings.Contains(res.Stderr, "does not exist") {
		return nil
	}
	return res.Error()
}

// ListRefs returns every ref under prefix mapped to its commit id, keyed by the
// full ref name.
func (repo *Repo) ListRefs(prefix string) (map[string]string, error) {
	res := repo.R.Run("for-each-ref", "--format=%(refname)%00%(objectname)", prefix)
	if !res.OK() {
		return nil, res.Error()
	}
	out := map[string]string{}
	for _, line := range res.Lines() {
		name, sha, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		out[name] = sha
	}
	return out, nil
}

// IsAncestor reports whether commit a is reachable from commit b.
func (repo *Repo) IsAncestor(a, b string) bool {
	return repo.R.Run("merge-base", "--is-ancestor", a, b).OK()
}

// SameTree reports whether two commits have identical content.
//
// It is how stk recognises a squash merge: the content lands on trunk under a
// new commit, so ancestry cannot see it, but the branch demonstrably adds
// nothing that trunk lacks.
func (repo *Repo) SameTree(a, b string) bool {
	return repo.R.Run("diff", "--quiet", a, b).OK()
}

// MergeBase returns the best common ancestor of a and b.
func (repo *Repo) MergeBase(a, b string) (string, error) {
	res := repo.R.Run("merge-base", a, b)
	if !res.OK() {
		return "", fmt.Errorf("no merge base between %s and %s", a, b)
	}
	return res.Out(), nil
}

// CountCommits returns the number of commits in the range from..to.
func (repo *Repo) CountCommits(from, to string) int {
	res := repo.R.Run("rev-list", "--count", from+".."+to)
	if !res.OK() {
		return 0
	}
	var n int
	fmt.Sscanf(res.Out(), "%d", &n)
	return n
}

// CountUniqueCommits returns how many commits are reachable from rev but from
// none of the other refs.
//
// It is how stk answers "what would deleting this branch throw away?" without
// guessing at a base: the survivors are named outright, and whatever is left is
// what nothing else holds.
func (repo *Repo) CountUniqueCommits(rev string, others []string) int {
	args := []string{"rev-list", "--count", rev}
	if len(others) > 0 {
		args = append(args, "--not")
		args = append(args, others...)
	}
	res := repo.R.Run(args...)
	if !res.OK() {
		return 0
	}
	var n int
	fmt.Sscanf(res.Out(), "%d", &n)
	return n
}

// HasMergeCommits reports whether the range from..to contains a merge.
func (repo *Repo) HasMergeCommits(from, to string) bool {
	res := repo.R.Run("rev-list", "--merges", "--max-count=1", from+".."+to)
	return res.OK() && res.Out() != ""
}

// ShortSHA abbreviates a commit id for display.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
