package operations

import "github.com/egmacke/stk/internal/output"

// CheckoutRemote creates a local branch from the remote's branch of the same
// name and checks it out, so a branch a colleague pushed can be checked out
// without fetching by hand first.
//
// The remote is fetched only when it has no branch of that name already, which
// keeps the common case — a name the user simply mistyped — off the network
// unless there is a chance of finding something.
//
// It reports whether the branch was found, leaving the caller to report a name
// that exists neither locally nor on the remote.
func CheckoutRemote(env *Env, name string) (bool, error) {
	repo, remote := env.Repo, env.Cfg.Remote
	if remote == "" || !repo.RemoteExists(remote) {
		return false, nil
	}
	if _, ok := repo.RemoteBranchSHA(remote, name); !ok {
		if env.DryRun {
			env.Out.Dry("would fetch %s and check %s out if it exists there", remote, output.BranchName(name))
			return true, nil
		}
		env.Out.Printf("Fetching %s...", remote)
		if err := repo.Fetch(remote); err != nil {
			return false, err
		}
		if _, ok := repo.RemoteBranchSHA(remote, name); !ok {
			return false, nil
		}
	}
	if env.DryRun {
		env.Out.Dry("would create %s from %s/%s and switch to it", output.BranchName(name), remote, name)
		return true, nil
	}
	if err := repo.CreateTrackingBranch(name, remote); err != nil {
		return false, err
	}
	env.Out.OK("Created %s from %s/%s", output.BranchName(name), remote, name)
	// The branch arrives as an ordinary git branch; where it belongs in the
	// stack is the caller's to decide.
	return true, Switch(env, name)
}
