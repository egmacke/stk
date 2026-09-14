package cmd

import (
	"runtime"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
)

// Build information, set with -ldflags at release time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// SetBuildInfo lets main override the embedded build metadata.
//
// Release binaries are stamped by make dist, but a binary from
//
//	go install github.com/egmacke/stk@v0.1.0
//
// is not: the go command applies no ldflags of its own. Anything -ldflags did
// not supply is therefore taken from the metadata the toolchain records, so
// such a build reports its real version rather than "dev".
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
	adoptBuildInfo()
}

// adoptBuildInfo fills in whatever is still at its default from the module and
// VCS metadata the go command embeds in every binary.
func adoptBuildInfo() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	// Module version: set for "go install <module>@<version>", and absent or
	// "(devel)" for a build from a working tree. A working tree with uncommitted
	// changes yields a pseudo-version the toolchain has already marked "+dirty",
	// so nothing here needs to say so a second time.
	if version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}

	// VCS stamping: present for a build from a checkout, absent for a build
	// from the module proxy, which has no repository to read.
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "unknown" && s.Value != "" {
				commit = s.Value
				if len(commit) > 7 {
					commit = commit[:7]
				}
			}
		case "vcs.time":
			if date == "unknown" && s.Value != "" {
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					date = t.UTC().Format("2006-01-02")
				}
			}
		}
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
			// A binary built from the module proxy has no repository behind it,
			// so these are genuinely unknown rather than merely unset.
			if commit != "unknown" {
				p.Raw("commit: %s", commit)
			}
			if date != "unknown" {
				p.Raw("built: %s", date)
			}
			p.Raw("go: %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}
