package output

import "os"

var colorEnabled bool

// EnableColor turns ANSI styling on or off for the whole process.
func EnableColor(on bool) { colorEnabled = on }

// ColorEnabled reports the current styling mode.
func ColorEnabled() bool { return colorEnabled }

// ShouldColor decides the default styling mode from the environment and
// whether stdout is a terminal.
func ShouldColor(isTTY, noColorFlag bool) bool {
	if noColorFlag {
		return false
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTTY
}

func wrap(code, s string) string {
	if !colorEnabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dim renders secondary text.
func Dim(s string) string { return wrap("2", s) }

// Bold renders emphasised text.
func Bold(s string) string { return wrap("1", s) }

// Green marks success or a healthy branch.
func Green(s string) string { return wrap("32", s) }

// Yellow marks something needing attention.
func Yellow(s string) string { return wrap("33", s) }

// Red marks a failure.
func Red(s string) string { return wrap("31", s) }

// Cyan marks the current branch.
func Cyan(s string) string { return wrap("36", s) }

// BranchName styles a branch name where it appears in a sentence, so the
// subject of an action is legible at a glance in a wall of progress lines.
func BranchName(s string) string { return Bold(s) }

// Command styles a command stk is telling the user to run.
func Command(s string) string { return Bold(s) }

// Heading styles the label of a block of output.
func Heading(s string) string { return Bold(s) }
