package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/release"
	"github.com/egmacke/stk/internal/update"
)

func newUpgradeCmd() *cobra.Command {
	var (
		check   bool
		target  string
		force   bool
		dirOnly string
	)
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update stk to the latest released version",
		Long: "upgrade replaces this stk binary with a newer published build.\n\n" +
			"The archive is checked against the release's own checksums before it is\n" +
			"installed; a download that does not match is discarded rather than run.\n\n" +
			"It has nothing to do with your repository or your stacks: it does not need\n" +
			"a git repository and changes no branch, ref or metadata.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd.Context(), upgradeOptions{
				Check:   check,
				Target:  target,
				Force:   force,
				DestDir: dirOnly,
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "report whether a newer version exists without installing it")
	f.StringVar(&target, "version", "", "install this `tag` instead of the latest release")
	f.BoolVarP(&force, "force", "f", false, "install even when it means reinstalling or going backwards")
	f.StringVar(&dirOnly, "dir", "", "install into `path` instead of replacing this binary")
	return cmd
}

type upgradeOptions struct {
	Check   bool
	Target  string
	Force   bool
	DestDir string
}

func runUpgrade(ctx context.Context, opts upgradeOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p := newPrinter()

	platform, err := release.Platform()
	if err != nil {
		return err
	}

	src := releaseSource()
	tag := opts.Target
	if tag == "" {
		p.Printf("Checking for a newer version...")
		tag, err = src.Latest(ctx)
		if errors.Is(err, release.ErrNoRelease) {
			return fmt.Errorf("%s has no published releases yet", release.Repo)
		}
		if err != nil {
			return err
		}
	}

	current := Version()
	order, comparable := release.Compare(current, tag)
	switch {
	case comparable && order == 0 && !opts.Force:
		p.OK("stk %s is the latest version.", current)
		return nil
	case comparable && order > 0 && !opts.Force:
		// A source build carrying a git describe string is not comparable, so
		// this only fires on a genuine downgrade the user did not ask for.
		return fmt.Errorf("stk %s is newer than %s\n\nTo go back anyway:\n\n    stk upgrade --version %s --force", current, tag, tag)
	}

	if opts.Check {
		p.Raw("stk %s is available (you have %s).", tag, current)
		p.Raw("")
		p.Raw("Install it with:")
		p.Raw("")
		p.Raw("    stk upgrade")
		return nil
	}

	dest := opts.DestDir
	if dest == "" {
		exe, err := release.Target()
		if err != nil {
			return fmt.Errorf("could not find the running stk binary: %w", err)
		}
		dest = exe
	} else {
		dest = filepath.Join(dest, release.BinaryName)
	}

	if globals.dryRun {
		p.Printf("Would install stk %s to %s", tag, dest)
		return nil
	}

	// Staged in a directory of its own so a failed verification leaves nothing
	// behind next to the binary being replaced.
	tmp, err := os.MkdirTemp("", "stk-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	p.Printf("Downloading stk %s (%s)...", tag, platform)
	staged, err := src.Fetch(ctx, tag, platform, tmp)
	if err != nil {
		return err
	}

	if err := release.Replace(dest, staged); err != nil {
		return err
	}
	// The user has just upgraded, so any version they once declined is
	// settled. Left behind, the record would silence the notice for a future
	// release that happened to carry the same tag as the declined one.
	if dir, err := workDir(); err == nil {
		update.ClearSkipped(git.NewRunner(dir))
	}
	p.OK("Installed stk %s to %s", tag, dest)
	return nil
}
