package git

import (
	"fmt"
	"strings"
)

// ConfigGet reads a repository config value. Missing keys return "".
func (repo *Repo) ConfigGet(key string) string {
	res := repo.R.Run("config", "--local", "--get", key)
	if !res.OK() {
		return ""
	}
	return res.Out()
}

// ConfigBool reads a repository config value as a git boolean, returning def
// when the key is unset. Every spelling git accepts (true, yes, on, 1 and
// their negatives) is honoured, because the key is one users set by hand.
func (repo *Repo) ConfigBool(key string, def bool) (bool, error) {
	res := repo.R.Run("config", "--local", "--bool", "--get", key)
	if res.OK() {
		return res.Out() == "true", nil
	}
	// Exit code 1 means the key is simply absent.
	if res.ExitCode == 1 {
		return def, nil
	}
	return def, fmt.Errorf("%s is not a boolean: %q", key, repo.ConfigGet(key))
}

// ConfigSet writes a repository config value.
//
// Branch metadata lives in the repository config, which is stored in the
// common git directory and therefore shared by every worktree.
func (repo *Repo) ConfigSet(key, value string) error {
	return repo.R.Mutate("config", "--local", key, value).Error()
}

// ConfigUnset removes a repository config key, tolerating a missing one.
func (repo *Repo) ConfigUnset(key string) error {
	res := repo.R.Mutate("config", "--local", "--unset-all", key)
	if res.OK() || res.ExitCode == 5 {
		return nil
	}
	return res.Error()
}

// ConfigGetRegexp returns every repository config key matching a pattern, in
// one invocation. Values containing newlines are preserved because the output
// is NUL delimited.
func (repo *Repo) ConfigGetRegexp(pattern string) (map[string]string, error) {
	res := repo.R.Run("config", "--local", "--null", "--get-regexp", pattern)
	// Exit code 1 simply means nothing matched.
	if !res.OK() {
		if res.ExitCode == 1 {
			return map[string]string{}, nil
		}
		return nil, res.Error()
	}
	out := map[string]string{}
	for _, record := range strings.Split(res.Stdout, "\x00") {
		if record == "" {
			continue
		}
		key, value, _ := strings.Cut(record, "\n")
		out[key] = value
	}
	return out, nil
}

// ConfigGetAny reads a config value from whichever scope defines it, letting
// git resolve the precedence: a repository setting wins over the user's, which
// wins over the system's.
//
// The repository-scoped readers above are for stk's own branch metadata, which
// belongs to one repository and must never be answered by a global default.
// This one is for standing user preferences, which a repository may still
// override. Missing keys return "".
func (r *Runner) ConfigGetAny(key string) string {
	res := r.Run("config", "--get", key)
	if !res.OK() {
		return ""
	}
	return res.Out()
}

// ConfigBoolAny reads a config value from any scope as a git boolean,
// returning def when no scope defines it.
func (r *Runner) ConfigBoolAny(key string, def bool) bool {
	res := r.Run("config", "--bool", "--get", key)
	if !res.OK() {
		return def
	}
	return res.Out() == "true"
}

// ConfigSetGlobal writes a value to the user's own config file.
//
// It is used for settings that describe this machine's stk rather than one
// repository -- which release the user has already declined, and when stk last
// looked -- so the answer follows the binary across every clone.
func (r *Runner) ConfigSetGlobal(key, value string) error {
	return r.Mutate("config", "--global", key, value).Error()
}

// ConfigUnsetGlobal removes a key from the user's own config file, tolerating
// one that was never there.
func (r *Runner) ConfigUnsetGlobal(key string) error {
	res := r.Mutate("config", "--global", "--unset-all", key)
	if res.OK() || res.ExitCode == 5 {
		return nil
	}
	return res.Error()
}
