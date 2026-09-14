package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"stk/internal/config"
	"stk/internal/forge"
	"stk/internal/git"
	"stk/internal/operations"
	"stk/internal/output"
	"stk/internal/stack"
)

type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

type check struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

type doctorReport struct {
	OK     bool    `json:"ok"`
	Checks []check `json:"checks"`
}

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Validate stk metadata against the repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report := runDoctor()
			if asJSON {
				printer := newPrinter()
				if err := printer.JSON(report); err != nil {
					return err
				}
			} else {
				printReport(report)
			}
			if !report.OK {
				return errSilent{errors.New("stk doctor found problems")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&asJSON, "json", "j", false, "print the report as JSON")
	return cmd
}

func printReport(r doctorReport) {
	p := newPrinter()
	for _, c := range r.Checks {
		symbol := output.Green(output.SymOK)
		switch c.Status {
		case statusWarn:
			symbol = output.Yellow(output.SymRestack)
		case statusFail:
			symbol = output.Red(output.SymFailed)
		}
		line := fmt.Sprintf("%s %s", symbol, c.Name)
		if c.Detail != "" {
			line += ": " + c.Detail
		}
		p.Raw("%s", line)
	}
	p.Raw("")
	if r.OK {
		p.Raw("%s", "No problems found.")
	} else {
		p.Raw("%s", "Problems found; see above.")
	}
}

// runDoctor performs every validation the design calls for, reporting rather
// than repairing.
func runDoctor() doctorReport {
	var r doctorReport
	add := func(name string, status checkStatus, detail string, args ...any) {
		if len(args) > 0 {
			detail = fmt.Sprintf(detail, args...)
		}
		r.Checks = append(r.Checks, check{Name: name, Status: status, Detail: detail})
	}

	repo, _, err := openRepo()
	if err != nil {
		add("git repository", statusFail, "%v", err)
		r.OK = false
		return r
	}
	major, minor := repo.Version()
	add("git available", statusOK, "%d.%d", major, minor)
	if !repo.SupportsNoUpdateRefs() {
		add("git version", statusWarn, "git %d.%d predates --no-update-refs; rebases may move refs stk manages", major, minor)
	}

	if _, err := os.Stat(repo.CommonDir); err != nil {
		add("common git directory", statusFail, "%v", err)
	} else {
		add("common git directory", statusOK, repo.CommonDir)
	}

	cfg, initialised, cfgErr := config.Load(repo)
	if !initialised {
		add("repository initialised", statusFail, "run stk init")
		r.OK = false
		return r
	}
	if cfgErr != nil {
		add("repository initialised", statusFail, "%v", cfgErr)
		r.OK = false
		return r
	}
	add("repository initialised", statusOK, "metadata version %d", cfg.Version)

	if repo.BranchExists(cfg.Trunk) {
		add("trunk exists", statusOK, cfg.Trunk)
	} else {
		add("trunk exists", statusFail, "branch %q is missing", cfg.Trunk)
	}
	switch {
	case cfg.Remote == "":
		add("remote configured", statusWarn, "no default remote; sync will not fetch")
	case repo.RemoteExists(cfg.Remote):
		add("remote exists", statusOK, cfg.Remote)
	default:
		add("remote exists", statusFail, "remote %q is not configured", cfg.Remote)
	}
	if cfg.GitHubStacks {
		// A warning, not a failure: it only matters to stk submit --pull, and
		// pushing and restacking are unaffected.
		gh := &forge.GH{Dir: repo.Root}
		switch {
		case gh.Available() != nil:
			add("GitHub stacks", statusWarn, "%s is on but the GitHub CLI is not installed", config.KeyGitHubStacks)
		case gh.HasStackExtension() != nil:
			add("GitHub stacks", statusWarn, "%s is on but the gh stack extension is not installed (gh extension install github/gh-stack)", config.KeyGitHubStacks)
		default:
			add("GitHub stacks", statusOK, "gh stack is installed")
		}
	}

	g, err := stack.Load(repo, cfg)
	if err != nil {
		add("stack graph", statusFail, "%v", err)
		r.OK = false
		return r
	}

	dupes := map[string][]string{}
	for _, b := range g.Tracked {
		dupes[b.ID] = append(dupes[b.ID], b.Name)
	}
	var dupeMsgs []string
	for id, names := range dupes {
		if len(names) > 1 {
			sort.Strings(names)
			dupeMsgs = append(dupeMsgs, fmt.Sprintf("%s shared by %s", id, strings.Join(names, ", ")))
		}
	}
	if len(dupeMsgs) == 0 {
		add("branch ids unique", statusOK, "%d tracked branches", len(g.Tracked))
	} else {
		sort.Strings(dupeMsgs)
		add("branch ids unique", statusFail, "%s (a copied branch duplicates its config; untrack and re-track one of them)",
			strings.Join(dupeMsgs, "; "))
	}

	var orphans, cycles []string
	for _, b := range g.Tracked {
		if b.Orphaned {
			orphans = append(orphans, b.Name)
		}
		if b.InCycle {
			cycles = append(cycles, b.Name)
		}
	}
	if len(orphans) == 0 {
		add("parent ids resolve", statusOK, "")
	} else {
		sort.Strings(orphans)
		add("parent ids resolve", statusFail, "%s (re-track with stk track <branch> --parent <branch>)", strings.Join(orphans, ", "))
	}
	if len(cycles) == 0 {
		add("no cycles", statusOK, "")
	} else {
		sort.Strings(cycles)
		add("no cycles", statusFail, "%s", strings.Join(cycles, ", "))
	}

	var missingBase, badBase []string
	for _, b := range g.Tracked {
		if b.BaseMissing {
			missingBase = append(missingBase, b.Name)
			continue
		}
		if !repo.Exists(b.Base) {
			badBase = append(badBase, fmt.Sprintf("%s (base %s is gone)", b.Name, git.ShortSHA(b.Base)))
			continue
		}
		if b.SHA != "" && !repo.IsAncestor(b.Base, b.SHA) {
			badBase = append(badBase, fmt.Sprintf("%s (base %s is not in its history)", b.Name, git.ShortSHA(b.Base)))
		}
	}
	if len(missingBase) == 0 {
		add("base refs exist", statusOK, "")
	} else {
		sort.Strings(missingBase)
		add("base refs exist", statusFail, "%s", strings.Join(missingBase, ", "))
	}
	if len(badBase) == 0 {
		add("base history valid", statusOK, "")
	} else {
		sort.Strings(badBase)
		add("base history valid", statusFail, "%s", strings.Join(badBase, ", "))
	}

	claimed := map[string]bool{}
	for _, b := range g.Tracked {
		claimed[b.ID] = true
	}
	bases, err := stack.ReadBases(repo)
	if err == nil {
		var stale []string
		for id := range bases {
			if !claimed[id] {
				stale = append(stale, id)
			}
		}
		if len(stale) == 0 {
			add("no orphaned metadata", statusOK, "")
		} else {
			sort.Strings(stale)
			add("no orphaned metadata", statusWarn, "%d base ref(s) belong to branches that no longer exist: %s",
				len(stale), strings.Join(stale, ", "))
		}
	}

	op, opErr := operations.LoadOperation(repo)
	switch {
	case errors.Is(opErr, operations.ErrNoOperation):
		add("operation journal", statusOK, "no operation in progress")
	case opErr != nil:
		add("operation journal", statusFail, "%v", opErr)
	default:
		add("operation journal", statusWarn, "an stk %s operation is in progress (%d/%d)", op.Type, op.Position+1, len(op.Branches))
		if _, statErr := os.Stat(op.Worktree); statErr != nil {
			add("operation worktree exists", statusFail, "%s is gone; run stk abort after recreating it, or delete %s",
				op.Worktree, repo.StkDir())
		} else {
			add("operation worktree exists", statusOK, op.Worktree)
		}
	}

	stashes, stashErr := stack.ReadAutostashes(repo)
	switch {
	case stashErr != nil:
		add("parked changes", statusFail, "%v", stashErr)
	case len(stashes) == 0:
		add("no parked changes", statusOK, "")
	case opErr == nil && op.AutostashSHA != "":
		add("parked changes", statusOK, "%s is held by the operation in progress", git.ShortSHA(op.AutostashSHA))
	default:
		var shas []string
		for _, sha := range stashes {
			shas = append(shas, sha)
		}
		sort.Strings(shas)
		add("parked changes", statusWarn,
			"%d stash(es) stk parked and never restored: %s (recover with git stash apply <sha>, then git update-ref -d %s<id>)",
			len(shas), strings.Join(shas, ", "), stack.AutostashRefPrefix)
	}

	worktrees, err := repo.Worktrees()
	if err != nil {
		add("worktree state", statusFail, "%v", err)
	} else {
		var bad []string
		for _, wt := range worktrees {
			if wt.Prunable {
				bad = append(bad, wt.Path+" (prunable)")
			}
		}
		if len(bad) == 0 {
			add("worktree state consistent", statusOK, "%d worktree(s)", len(worktrees))
		} else {
			add("worktree state consistent", statusWarn, "%s; run git worktree prune", strings.Join(bad, ", "))
		}
	}

	r.OK = true
	for _, c := range r.Checks {
		if c.Status == statusFail {
			r.OK = false
		}
	}
	return r
}
