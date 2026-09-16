package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/egmacke/stk/internal/git"
	"github.com/egmacke/stk/internal/release"
	"github.com/egmacke/stk/internal/ui"
	"github.com/egmacke/stk/internal/update"
)

// updateCheck is this run's version check, or nil when none was started.
//
// It is package state because the two halves live at opposite ends of a run:
// the check is armed once the flags are parsed, and read after the command has
// returned, and cobra gives no value to carry between them.
var updateCheck *update.Checker

// unwatchedCommands never check for a new version.
//
// upgrade and version ask about releases themselves and would ask twice;
// completion is consumed by a shell, which has no user to prompt; help is
// output the user asked to read in full.
var unwatchedCommands = map[string]bool{
	"upgrade":                       true,
	"version":                       true,
	"help":                          true,
	"completion":                    true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// armUpdateCheck starts a version check for this command, unless something
// about the run says stk should stay quiet.
//
// It is called once the flags are parsed, so the request overlaps the work the
// command is about to do rather than delaying it.
func armUpdateCheck(cmd *cobra.Command) {
	if !updateCheckWanted(cmd) {
		return
	}
	dir, err := workDir()
	if err != nil {
		return
	}
	store := update.GitStore{R: git.NewRunner(dir)}
	if !update.Enabled(store, os.Getenv) {
		return
	}
	updateCheck = update.Start(context.Background(), update.Options{
		Current: Version(),
		Store:   store,
		BaseURL: releaseSource().BaseURL,
	})
}

// releaseSource is where stk looks for published builds.
//
// The override exists so the end-to-end tests can serve a release of their
// own; unset, which is every real run, it is the repository on github.com.
// The check and the upgrade read the same one, so an accepted offer installs
// the release that was offered.
func releaseSource() release.Source {
	return release.Source{BaseURL: os.Getenv(update.EnvBaseURL)}
}

// updateCheckWanted reports whether this run is one a version notice belongs
// in at all.
//
// Everything here is about the shape of the run rather than about versions:
// whether there is someone to ask, and whether stk has been told to keep its
// output to what was asked for.
func updateCheckWanted(cmd *cobra.Command) bool {
	switch {
	case cmd == nil || cmd.Name() == "" || unwatchedCommands[cmd.Name()]:
		return false
	case cmd.Root() == cmd:
		// Bare "stk", which prints usage and does nothing.
		return false
	case globals.quiet, globals.dryRun, globals.noInteractive:
		// --quiet was asked for less output, not more; --dry-run promised to
		// change nothing, and an upgrade is a change.
		return false
	case !Interactive():
		// Nobody to answer the prompt, so nothing to gain by asking the
		// release server.
		return false
	case os.Getenv("CI") != "":
		return false
	case jsonRequested(cmd):
		// The caller is parsing this output.
		return false
	}
	return true
}

// jsonRequested reports whether the command was asked for machine-readable
// output. Only some commands offer it, so the flag may not exist.
func jsonRequested(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("json")
	return f != nil && f.Changed
}

// reportUpdate offers the newer release, if one was found, once the command
// has finished.
//
// It runs only after a command that succeeded: a user staring at a conflict or
// a failed push needs the terminal for that, and will be told within the hour
// on a run that went well.
func reportUpdate() {
	tag, ok := updateCheck.Available()
	if !ok {
		return
	}
	p := newPrinter()
	p.Warnf("")
	for _, line := range update.Notice(tag, Version()) {
		p.Warnf("%s", line)
	}

	answer, err := ui.ConfirmAnswer("Upgrade now?", true)
	if err != nil {
		return
	}
	switch answer {
	case ui.No:
		// Only a deliberate no is written down. A dismissed prompt is not a
		// decision, and recording it would retire an offer the user never
		// really saw.
		updateCheck.Decline(tag)
		return
	case ui.Unclear:
		return
	}

	// A failed upgrade leaves the command's own result standing: the work the
	// user asked for succeeded, and this was an aside.
	if err := runUpgrade(context.Background(), upgradeOptions{Target: tag}); err != nil {
		p.Fail("%s", err)
	}
}
