package update

import "github.com/egmacke/stk/internal/git"

// GitStore keeps the check state in git's own configuration.
//
// git is already the one thing stk is guaranteed to have, and its config is
// already where every other stk setting lives, so the state needs no file
// format, no directory to create and no migration.
type GitStore struct{ R *git.Runner }

// Get reads a key from whichever scope defines it, so a repository can
// override a user-wide preference exactly as it can for any git setting.
func (s GitStore) Get(key string) string { return s.R.ConfigGetAny(key) }

// Bool reads a key as a git boolean, so every spelling git accepts works:
// this is a key users set by hand.
func (s GitStore) Bool(key string, def bool) bool { return s.R.ConfigBoolAny(key, def) }

// SetGlobal writes to the user's own config file.
func (s GitStore) SetGlobal(key, value string) error { return s.R.ConfigSetGlobal(key, value) }

// Enabled reports whether the user wants version checks at all.
//
// Both switches are honoured: the environment, for one shell or one command,
// and the config key, for good.
func Enabled(s Store, env func(string) string) bool {
	if env(EnvDisable) != "" {
		return false
	}
	return s.Bool(KeyEnabled, true)
}

// ClearSkipped forgets a declined version.
//
// An upgrade that has just happened settles the question the record was
// answering, and leaving it behind would silence the next release if the
// versions happened to line up.
func ClearSkipped(r *git.Runner) { _ = r.ConfigUnsetGlobal(KeySkipVersion) }
