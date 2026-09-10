# stk

A fast, local-first git wrapper that adds persistent stacked-branch
relationships across worktrees, while leaving ordinary git as the source of
truth.

`stk` records one extra fact about your repository:

> **Branch A is the logical parent of branch B.**

Everything else — branches, commits, refs, rebases, worktrees, the index — stays
plain git. You can stop using `stk` at any moment and carry on with git alone.

There is no hosted service, no account, no GitHub or GitLab integration, no
pull-request management and no background daemon. The only runtime dependency
is `git`.

## Install

```bash
make install          # to $GOBIN
# or
make build && cp stk /usr/local/bin/
```

Release archives for `linux/{amd64,arm64}` and `darwin/{amd64,arm64}` are built
by the `release` workflow on a `v*` tag.

## Getting started

```bash
stk init                       # detect trunk and default remote

stk create sc-123/api --from main
git commit -am "Add API"

stk create sc-123/service      # parent is the current branch
git commit -am "Add service"

stk create sc-123/ui
```

```text
main
└─ sc-123/api
   └─ sc-123/service
      └─ sc-123/ui ←
```

Rewrite anything in the stack and repair it from anywhere:

```bash
stk checkout sc-123/api
git commit --amend
stk restack
```

## Commands

| Command | What it does |
| --- | --- |
| `stk init` | Record trunk and the default remote |
| `stk create <branch> [--from <parent>]` | Create a branch and record its parent |
| `stk checkout [branch]`, `stk co` | Switch branches; interactive picker with no argument |
| `stk show` | The same viewer, without implying checkout is the goal |
| `stk stack [--all] [--json]` | Print the current stack |
| `stk info [branch] [--json]` | Everything stk knows about a branch |
| `stk parent` / `stk children` | Print the immediate relatives of a branch |
| `stk up [n]` / `stk down [n]` / `stk top` / `stk bottom` | Navigate the stack |
| `stk track <branch> --parent <p>` | Adopt an existing branch into the graph |
| `stk untrack [branch]` | Drop metadata; the git branch is never deleted |
| `stk rename [old] <new>` | Rename without breaking the stack |
| `stk move [branch] --onto <p>` | Re-parent a branch and restack above it |
| `stk restack [--up\|--only]` | Rebase branches onto their parents |
| `stk sync [--stack] [--cleanup\|--no-cleanup] [--no-restack]` | Fetch, update trunk, prune, restack |
| `stk continue` / `stk abort` | Resume or abandon an interrupted operation |
| `stk doctor [--json]` | Validate metadata against the repository |
| `stk version`, `stk completion <shell>` | Build info and shell completion |

Anything else falls through to git, exit code included:

```bash
stk status          # runs: git status
stk commit -am foo  # runs: git commit -am foo
```

### Restack scope

`stk restack` repairs the **whole logical stack**, not just the path you are
standing on. Given:

```text
main
└─ A
   └─ B ←
      └─ C
         └─ D
```

| Command | Branches processed |
| --- | --- |
| `stk restack` | A, B, C, D |
| `stk restack --up` | B, C, D |
| `stk restack --only` | B |

With sibling branches, `stk restack` from `C` still processes `A`, `B`, `D`,
`C` and `E` — the entire connected tree rooted at the lowest branch above
trunk.

### Status markers

```text
↑N   commits not pushed          ↓N   commits behind upstream
↑?   no upstream branch          !    requires restack
@    checked out in another worktree
*    uncommitted changes         ?    metadata needs repair
←    current branch
```

## Global flags

```text
--cwd <path>        run as if started in another directory
--verbose           log every git invocation
--quiet             suppress progress output
--dry-run           build and print the plan without changing anything
--no-interactive    never prompt or open a selector
--interactive       answer prompts on stdin even without a terminal
--no-color          disable coloured output
--init              initialise the repository without prompting
```

`--cwd` and `--json` make `stk` easy to drive from scripts, editors and coding
agents. Without a terminal, `stk` never opens an interactive selector.

## How the metadata is stored

Everything lives inside the repository and is shared by every worktree:

| State | Where |
| --- | --- |
| trunk, default remote, metadata version | `stk.*` in the repository git config |
| branch identity and logical parent | `branch.<name>.stk-id` / `.stk-parent` |
| protected base commits | `refs/stk/base/<branch-id>` |
| operation snapshots | `refs/stk/snapshot/<op-id>/<branch-id>` |
| in-flight operation journal | `<git-common-dir>/stk/operations/current.json` |

Because the repository config and refs live in the git *common* directory,
every linked worktree sees the same stack graph. `stk init` creates no tracked
files.

Branches are identified by a stable id rather than by name, so `git branch -m`
leaves the graph intact. Each child also records the parent commit it was last
valid against, which is what lets `stk` run a precise
`git rebase --onto <new base> <old base> <branch>` instead of guessing which
commits belong to the branch.

## Safety

`stk` is deliberately conservative:

- never silently overwrites a diverged trunk;
- never chooses a stack parent for you;
- never deletes a branch git cannot prove is contained in trunk;
- never deletes or rewrites a branch checked out in another worktree;
- never stashes your changes;
- never rewrites a branch whose recorded base cannot be validated;
- never flattens merge commits without `--rebase-merges`;
- never hides a git conflict;
- never initialises a repository silently;
- never pushes, force-pushes or merges.

When a restack hits a conflict it stops, tells you exactly what to do, and
keeps a journal so `stk continue` resumes the *original* scope and `stk abort`
restores every branch it had already rewritten — not just the last one.

## Development

```bash
make lint     # gofmt + go vet
make test     # integration tests against real temporary repositories
make dist     # cross-compiled release archives
```

The test suite drives the compiled binary against real git repositories,
including linked worktrees, and checks both the resulting commit graph and the
stk metadata after every operation.

### Layout

```text
main.go                 entry point
cmd/                    cobra commands; no direct git orchestration
internal/git/           the only code that runs git
internal/stack/         metadata and the in-memory graph
internal/operations/    create, track, rename, move, restack, sync, continue, abort
internal/config/        repository-wide settings
internal/ui/            branch picker, prompts, tree rendering
internal/output/        text and JSON output
```

The module path is `stk`; change it in `go.mod` when the repository gets a
permanent home.
