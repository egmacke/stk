// Command stk adds stacked-branch workflows to git.
package main

import (
	"os"

	"stk/cmd"
)

// Build metadata, injected with -ldflags at release time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cmd.SetBuildInfo(version, commit, date)
	os.Exit(cmd.Execute(os.Args[1:]))
}
