package git

import "strings"

// ConfigGet reads a repository config value. Missing keys return "".
func (repo *Repo) ConfigGet(key string) string {
	res := repo.R.Run("config", "--local", "--get", key)
	if !res.OK() {
		return ""
	}
	return res.Out()
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
