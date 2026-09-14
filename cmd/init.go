package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"stk/internal/config"
	"stk/internal/ghstack"
	"stk/internal/git"
	"stk/internal/output"
	"stk/internal/ui"
)

func newInitCmd() *cobra.Command {
	var trunk, remote string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise the repository for stk",
		Long: "Records the trunk branch and default remote in the repository git config.\n\n" +
			"The configuration lives in the common git directory, so every worktree of\n" +
			"the repository shares the same stack graph. No tracked files are created.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, printer, err := openRepo()
			if err != nil {
				return err
			}
			if cfg, ok, _ := config.Load(repo); ok {
				printer.Printf("Already initialised (trunk: %s, remote: %s)", cfg.Trunk, orNone(cfg.Remote))
				printer.Printf("Re-detecting...")
			}
			_, err = runInit(repo, printer, trunk, remote)
			return err
		},
	}
	cmd.Flags().StringVarP(&trunk, "trunk", "t", "", "trunk branch name (detected when omitted)")
	cmd.Flags().StringVarP(&remote, "remote", "r", "", "default remote (detected when omitted)")
	return cmd
}

// runInit detects and stores the repository-wide settings.
func runInit(repo *git.Repo, printer *output.Printer, trunk, remote string) (config.Config, error) {
	if remote == "" {
		detected, ambiguous := config.DetectRemote(repo)
		remote = detected
		if ambiguous && Interactive() {
			printer.Warnf("Several remotes are configured: %v", repo.Remotes())
			ok, err := ui.Confirm(fmt.Sprintf("Use %q as the default remote?", detected), true)
			if err != nil {
				return config.Config{}, err
			}
			if !ok {
				return config.Config{}, fmt.Errorf("choose one explicitly:\n\n    stk init --remote <name>")
			}
		} else if ambiguous {
			printer.Warnf("Several remotes are configured; defaulting to %q. Override with --remote.", detected)
		}
	} else if !repo.RemoteExists(remote) {
		return config.Config{}, fmt.Errorf("remote %q is not configured", remote)
	}

	if trunk == "" {
		trunk = config.DetectTrunk(repo, remote)
	}
	if trunk == "" {
		return config.Config{}, fmt.Errorf("cannot detect the trunk branch; name it explicitly:\n\n    stk init --trunk <branch>")
	}
	if !repo.BranchExists(trunk) {
		return config.Config{}, fmt.Errorf("trunk branch %q does not exist locally", trunk)
	}

	cfg := config.Config{Version: config.Version, Trunk: trunk, Remote: remote}
	if globals.dryRun {
		printer.Dry("would record trunk %q and remote %q", trunk, orNone(remote))
		return cfg, nil
	}
	if err := config.Save(repo, cfg); err != nil {
		return config.Config{}, err
	}
	printer.Printf("Detected trunk: %s", trunk)
	printer.Printf("Detected remote: %s", orNone(remote))
	printer.OK("stk initialised")
	if on, _ := repo.ConfigBool(config.KeyGitHubStacks, config.GitHubStacksDefault); !on && ghstack.Exists(repo.CommonDir) {
		printer.Printf("")
		printer.Printf("This repository has gh stack tracking. To keep it in step with stk:")
		printer.Printf("")
		printer.Printf("    git config %s true", config.KeyGitHubStacks)
	}
	return cfg, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
