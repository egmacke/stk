package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"stk/internal/git"
	"stk/internal/operations"
)

// Execute runs the stk command line and returns the process exit code.
//
// Anything stk does not implement is handed to git unchanged, with git's exit
// code preserved.
func Execute(args []string) int {
	root := NewRoot()
	// Cobra normally adds these during Execute, but the passthrough check
	// below needs to see them in the command list first.
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpCmd()

	if name, ok := firstNonFlag(args); ok && !isKnown(root, name) {
		return passthrough(args)
	}

	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var silent errSilent
		if errors.As(err, &silent) {
			return 1
		}
		fmt.Fprintf(os.Stderr, "stk: %s\n", err)
		if errors.Is(err, operations.ErrConflict) {
			return 1
		}
		return 1
	}
	return 0
}

// globalFlagsWithValue are the stk flags that consume the following argument.
var globalFlagsWithValue = map[string]bool{"--cwd": true}

// firstNonFlag finds the token that names the subcommand.
func firstNonFlag(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		if strings.HasPrefix(a, "-") {
			if globalFlagsWithValue[a] {
				i++
			}
			continue
		}
		return a, true
	}
	return "", false
}

// isKnown reports whether the name matches a native stk command or alias.
func isKnown(root *cobra.Command, name string) bool {
	switch name {
	case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
		for _, alias := range c.Aliases {
			if alias == name {
				return true
			}
		}
	}
	return false
}

// stkGlobalFlags are the persistent stk flags recognised before a subcommand.
// They are consumed rather than forwarded, so "stk --no-color status" reaches
// git as "git status".
var stkGlobalFlags = map[string]bool{
	"--cwd":            true,
	"--verbose":        true,
	"--quiet":          true,
	"--dry-run":        true,
	"--no-interactive": true,
	"--interactive":    true,
	"--no-color":       true,
	"--init":           true,
}

// passthrough forwards an unrecognised command to git.
//
// stk's own options are stripped from the part of the line that precedes the
// subcommand; everything from the subcommand onwards reaches git untouched,
// and git's exit code is returned unchanged.
func passthrough(args []string) int {
	dir := ""
	var forwarded []string
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		name, value, hasValue := strings.Cut(a, "=")
		switch {
		case name == "--cwd" && hasValue:
			dir = value
		case name == "--cwd" && i+1 < len(args):
			dir = args[i+1]
			i++
		case stkGlobalFlags[name]:
			// Consumed; stk options are not git options.
		default:
			forwarded = append(forwarded, a)
		}
	}
	forwarded = append(forwarded, args[i:]...)

	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "stk: %s\n", err)
			return 128
		}
		dir = wd
	}
	return git.NewRunner(dir).Passthrough(forwarded...)
}
