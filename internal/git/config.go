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
