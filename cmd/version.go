package cmd

import (
	"runtime"

	"github.com/spf13/cobra"
)

// Build information, set with -ldflags at release time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// SetBuildInfo lets main override the embedded build metadata.
func SetBuildInfo(v, c, d string) {
	if v != "" {
		version = v
	}
	if c != "" {
		commit = c
	}
	if d != "" {
		date = d
	}
}

// Version returns the build version.
func Version() string { return version }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the stk version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			p := newPrinter()
			p.Raw("stk %s", version)
			p.Raw("commit: %s", commit)
			p.Raw("built: %s", date)
			p.Raw("go: %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}
