// Package cmd wires the stk command line onto the operations layer.
//
// Commands never orchestrate git directly; they resolve the repository, build
// the stack model and hand work to internal/operations.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"stk/internal/config"
	"stk/internal/git"
	"stk/internal/operations"
	"stk/internal/output"
	"stk/internal/stack"
	"stk/internal/ui"
)

type globalFlags struct {
	cwd           string
	verbose       bool
	quiet         bool
	dryRun        bool
	noInteractive bool
	interactive   bool
	noColor       bool
	autoInit      bool
}

var globals globalFlags

// app bundles everything a command needs after the repository is resolved.
type app struct {
	Repo  *git.Repo
	Cfg   config.Config
	Graph *stack.Graph
	Out   *output.Printer
	Env   *operations.Env
}

// Interactive reports whether stk may ask the user a question.
func Interactive() bool {
	if globals.noInteractive {
		return false
	}
	return globals.interactive || ui.IsTerminal()
}

// Selectable reports whether stk may open the full-screen branch picker,
// which needs a real terminal rather than merely an answerable stdin.
func Selectable() bool {
	return !globals.noInteractive && ui.IsTerminal()
}

// NewRoot builds the stk command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "stk",
		Short: "Local stacked-branch workflows on top of git",
		Long: "stk adds persistent parent/child relationships between git branches.\n\n" +
			"Git remains the source of truth: every branch, commit and rebase is an\n" +
			"ordinary git construct, and any command stk does not implement is passed\n" +
			"straight through to git.\n\n" +
			"Short forms:\n" +
			"  c    create       co   checkout     r    restack\n" +
			"  tr   track        utr  untrack      rn   rename\n" +
			"  cont continue     ab   abort\n\n" +
			"A short form is a native stk command and always wins over git. To reach\n" +
			"a git command of the same name, put -- first:\n\n" +
			"  stk -- r          runs: git r",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if globals.interactive && globals.noInteractive {
				return errors.New("use either --interactive or --no-interactive, not both")
			}
			output.EnableColor(output.ShouldColor(ui.IsTerminal(), globals.noColor))
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&globals.cwd, "cwd", "", "run as if stk were started in `path`")
	pf.BoolVar(&globals.verbose, "verbose", false, "log every git invocation")
	pf.BoolVar(&globals.quiet, "quiet", false, "suppress progress output")
	pf.BoolVar(&globals.dryRun, "dry-run", false, "show what would happen without changing anything")
	pf.BoolVar(&globals.noInteractive, "no-interactive", false, "never prompt or open a selector")
	pf.BoolVar(&globals.interactive, "interactive", false, "answer prompts on stdin even without a terminal")
	pf.BoolVar(&globals.noColor, "no-color", false, "disable coloured output")
	pf.BoolVar(&globals.autoInit, "init", false, "initialise the repository for stk without prompting")

	root.AddCommand(
		newInitCmd(),
		newCreateCmd(),
		newCheckoutCmd(),
		newShowCmd(),
		newStackCmd(),
		newInfoCmd(),
		newParentCmd(),
		newChildrenCmd(),
		newUpCmd(),
		newDownCmd(),
		newTopCmd(),
		newBottomCmd(),
		newTrackCmd(),
		newUntrackCmd(),
		newRenameCmd(),
		newMoveCmd(),
		newRestackCmd(),
		newSyncCmd(),
		newContinueCmd(),
		newAbortCmd(),
		newDoctorCmd(),
		newVersionCmd(),
	)
	return root
}

// workDir is the directory stk operates in, honouring --cwd.
func workDir() (string, error) {
	if globals.cwd != "" {
		return globals.cwd, nil
	}
	return os.Getwd()
}

func newPrinter() *output.Printer {
	p := output.New()
	p.Quiet = globals.quiet
	return p
}

// openRepo resolves the repository without requiring stk metadata.
func openRepo() (*git.Repo, *output.Printer, error) {
	dir, err := workDir()
	if err != nil {
		return nil, nil, err
	}
	repo, err := git.Discover(dir, globals.verbose, globals.dryRun)
	if err != nil {
		return nil, nil, err
	}
	return repo, newPrinter(), nil
}

// errNotInitialised is the non-interactive form of the initialisation prompt.
var errNotInitialised = errors.New("repository is not initialised\n\nRun:\n\n    stk init")

// open resolves the repository, ensures stk metadata exists and builds the
// stack model.
//
// When the repository has never been initialised the user is asked, because
// stk must never initialise silently.
func open() (*app, error) {
	repo, printer, err := openRepo()
	if err != nil {
		return nil, err
	}
	cfg, ok, err := config.Load(repo)
	if err != nil {
		return nil, err
	}
	if !ok {
		cfg, err = autoInitialise(repo, printer)
		if err != nil {
			return nil, err
		}
	}
	g, err := stack.Load(repo, cfg)
	if err != nil {
		return nil, err
	}
	a := &app{Repo: repo, Cfg: cfg, Graph: g, Out: printer}
	a.Env = &operations.Env{
		Repo:        repo,
		Cfg:         cfg,
		Out:         printer,
		Interactive: Interactive(),
		DryRun:      globals.dryRun,
		Autostash:   cfg.Autostash,
	}
	if Interactive() {
		a.Env.Confirm = ui.Confirm
	}
	return a, nil
}

func autoInitialise(repo *git.Repo, printer *output.Printer) (config.Config, error) {
	if !globals.autoInit {
		if !Interactive() {
			return config.Config{}, errNotInitialised
		}
		printer.Warnf("This repository has not been initialised for stk.")
		printer.Warnf("")
		ok, err := ui.Confirm("Initialise it now?", true)
		if err != nil {
			return config.Config{}, err
		}
		if !ok {
			return config.Config{}, errNotInitialised
		}
	}
	cfg, err := runInit(repo, printer, "", "")
	if err != nil {
		return config.Config{}, err
	}
	printer.Printf("")
	return cfg, nil
}

// resolveBranchArg returns the named branch, or the current one when no name
// was given.
func (a *app) resolveBranchArg(args []string) (*stack.Branch, error) {
	name := a.Graph.CurrentName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return nil, errors.New("HEAD is detached; name a branch explicitly")
	}
	b, ok := a.Graph.Resolve(name)
	if !ok {
		return nil, fmt.Errorf("branch %q does not exist", name)
	}
	return b, nil
}

// requireTracked returns the branch only if stk manages it.
func (a *app) requireTracked(b *stack.Branch) error {
	if b.IsTrunk {
		return fmt.Errorf("%s is the trunk branch", b.Name)
	}
	if !b.Tracked {
		return fmt.Errorf("branch %q is not tracked by stk\n\nTrack it first:\n\n    stk track %s --parent <branch>", b.Name, b.Name)
	}
	return nil
}

// branchNames completes branch names for the shell.
func branchNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	repo, _, err := openRepo()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	branches, err := repo.Branches()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, b := range branches {
		out = append(out, b.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
