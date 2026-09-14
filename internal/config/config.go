// Package config stores the repository-wide stk settings.
//
// Settings live in the repository git config, which is held in the common git
// directory and is therefore shared by every worktree of the repository.
package config

import (
	"fmt"
	"strconv"

	"stk/internal/git"
)

// Version is the metadata layout version written by this build.
const Version = 1

// AutostashDefault is what stk does with uncommitted changes when the
// repository has expressed no preference: park them and put them back.
const AutostashDefault = true

const (
	KeyVersion = "stk.version"
	KeyTrunk   = "stk.trunk"
	KeyRemote  = "stk.remote"
	// KeyAutostash turns autostashing off for every command that accepts it
	// when set to false. It is on unless the repository says otherwise.
	KeyAutostash = "stk.autostash"
	// KeyGitHubStacks makes stk submit link the pull requests of a stack as a
	// GitHub stack through the gh stack extension, instead of writing the
	// stack comment. It is off unless the repository asks for it.
	KeyGitHubStacks = "stk.githubStacks"
)

// GitHubStacksDefault is what stk does when the repository has expressed no
// preference: the stack comment, which needs nothing beyond gh itself.
const GitHubStacksDefault = false

// Config is the repository-wide stk configuration.
type Config struct {
	Version int
	Trunk   string
	Remote  string
	// Autostash makes commands that need a clean working tree park
	// uncommitted changes instead of refusing to run. It defaults to true;
	// stk.autostash = false turns it off repository-wide.
	Autostash bool
	// GitHubStacks makes stk submit publish the shape of a stack as a GitHub
	// stack, through gh stack link, rather than as a comment on each pull
	// request. It defaults to false; stk.githubStacks = true turns it on.
	GitHubStacks bool
}

// Load reads the configuration. ok is false when the repository has never been
// initialised for stk.
func Load(repo *git.Repo) (cfg Config, ok bool, err error) {
	raw := repo.ConfigGet(KeyVersion)
	if raw == "" {
		return Config{}, false, nil
	}
	v, convErr := strconv.Atoi(raw)
	if convErr != nil {
		return Config{}, false, fmt.Errorf("%s is not a number: %q", KeyVersion, raw)
	}
	if v > Version {
		return Config{}, false, fmt.Errorf(
			"repository metadata is version %d but this stk understands version %d; upgrade stk", v, Version)
	}
	cfg = Config{
		Version: v,
		Trunk:   repo.ConfigGet(KeyTrunk),
		Remote:  repo.ConfigGet(KeyRemote),
	}
	autostash, convErr := repo.ConfigBool(KeyAutostash, AutostashDefault)
	if convErr != nil {
		return cfg, true, convErr
	}
	cfg.Autostash = autostash
	githubStacks, convErr := repo.ConfigBool(KeyGitHubStacks, GitHubStacksDefault)
	if convErr != nil {
		return cfg, true, convErr
	}
	cfg.GitHubStacks = githubStacks
	if cfg.Trunk == "" {
		return cfg, true, fmt.Errorf("%s is not set; run stk init", KeyTrunk)
	}
	return cfg, true, nil
}

// Save writes the detected settings to the repository config.
//
// KeyAutostash and KeyGitHubStacks are deliberately left alone: they are
// standing preferences the user sets by hand, and re-running stk init must not
// clear them.
func Save(repo *git.Repo, cfg Config) error {
	if err := repo.ConfigSet(KeyVersion, strconv.Itoa(cfg.Version)); err != nil {
		return err
	}
	if err := repo.ConfigSet(KeyTrunk, cfg.Trunk); err != nil {
		return err
	}
	if cfg.Remote == "" {
		return repo.ConfigUnset(KeyRemote)
	}
	return repo.ConfigSet(KeyRemote, cfg.Remote)
}

// DetectTrunk guesses the trunk branch, preferring the remote's published
// default and falling back to the conventional names.
func DetectTrunk(repo *git.Repo, remote string) string {
	if remote != "" {
		res := repo.R.Run("symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if res.OK() {
			if name := trimRemote(res.Out(), remote); name != "" && repo.BranchExists(name) {
				return name
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if repo.BranchExists(name) {
			return name
		}
	}
	// Fall back to whatever this worktree has checked out.
	if cur := repo.CurrentBranch(); cur != "" {
		return cur
	}
	return ""
}

func trimRemote(ref, remote string) string {
	prefix := remote + "/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return ref[len(prefix):]
	}
	return ""
}

// DetectRemote picks the default remote. It returns ambiguous=true when there
// is more than one candidate and none is named origin.
func DetectRemote(repo *git.Repo) (name string, ambiguous bool) {
	remotes := repo.Remotes()
	switch len(remotes) {
	case 0:
		return "", false
	case 1:
		return remotes[0], false
	}
	for _, r := range remotes {
		if r == "origin" {
			return r, false
		}
	}
	return remotes[0], true
}
