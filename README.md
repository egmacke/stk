# stk

A fast, local-first git wrapper that adds persistent stacked-branch
relationships across worktrees, while leaving ordinary git as the source of
truth.

`stk` records one extra fact about your repository:

> **Branch A is the logical parent of branch B.**

Everything else — branches, commits, refs, rebases, worktrees, the index — stays
plain git. You can stop using `stk` at any moment and carry on with git alone.

There is no hosted service, no account, no stored token, no PR or CI status
tracking and no background daemon. `git` is the only dependency; `stk submit
--pull` shells out to the [GitHub CLI](https://cli.github.com) to open a pull
request when you ask it to, and everything else works with no forge at all.

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
| `stk create [branch] [--from <parent>]` | Create a branch and record its parent |
| `stk checkout [branch]`, `stk co` | Switch branches; interactive picker with no argument |
| `stk show` | The same viewer, without implying checkout is the goal |
| `stk stack [--all] [--json]` | Print the current stack |
| `stk info [branch] [--json]` | Everything stk knows about a branch |
| `stk parent` / `stk children` | Print the immediate relatives of a branch |
| `stk up [n]` / `stk down [n]` / `stk top` / `stk bottom` | Navigate the stack |
| `stk track [branch] [--parent <p>]` | Adopt an existing branch into the graph |
| `stk untrack [branch]` | Drop metadata; the git branch is never deleted |
| `stk rename [old] [new]` | Rename without breaking the stack |
| `stk move [branch] [--onto <p>]` | Re-parent a branch and restack above it |
| `stk fold [branch] [--into <b>\|--stack]` | Collapse stacked branches into one |
| `stk restack [--up\|--only]` | Rebase branches onto their parents |
| `stk submit [branch] [--pull] [--draft] [--stack]`, `stk s`, `stk ss` | Push to the remote, optionally opening pull requests |
| `stk ready [branch] [--stack] [--undo]` | Take a pull request out of draft, or put it back |
| `stk sync [--stack] [--cleanup\|--no-cleanup] [--no-restack]` | Fetch, update trunk, prune, restack |
| `stk continue` / `stk abort` | Resume or abandon an interrupted operation |
| `stk doctor [--json]` | Validate metadata against the repository |
| `stk version`, `stk completion <shell>` | Build info and shell completion |

Anything else falls through to git, exit code included:

```bash
stk status          # runs: git status
stk commit -am foo  # runs: git commit -am foo
```

### Short forms

| Short | Command | Short | Command |
| --- | --- | --- | --- |
| `c` | `create` | `co` | `checkout` |
| `r` | `restack` | `tr` | `track` |
| `utr` | `untrack` | `rn` | `rename` |
| `cont` | `continue` | `ab` | `abort` |
| `s` | `submit` | `ss` | `submit --stack` |

```bash
stk c sc-123/api
stk r --up
stk cont
stk s               # push the current branch
stk ss              # refresh every pushed branch of the stack
stk ss -pn          # ... and propose the whole stack
```

`ss` is the only short form that carries a flag of its own; every other flag
still applies through it.

A short form is a native stk command and always wins over the git passthrough,
including over a git alias of the same name. To reach a git command that shares
a name, put `--` first:

```bash
stk -- r            # runs: git r
stk -- log --oneline
```

Only these ten names are claimed. Everything else stays available to git, so
`stk mv`, `stk st` and any git alias of yours still pass straight through. If
you have `git s` aliased to something of your own, reach it with `stk -- s`. There is no prefix matching: `stk resta` is a git command, not
`restack`. User-defined aliases remain a phase-two item; write shell or git
aliases in the meantime.

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

### Publishing a stack

`stk submit` pushes the current branch to the configured remote, creating it
there if needed:

```console
$ stk submit
✓ Pushed sc-123/api to origin (created)

1 branch(es) pushed.
```

The remote branch becomes the branch's upstream, so `↑?` clears and git knows
where it went. A branch whose commit the remote already holds but whose
upstream is missing has that link recorded locally, with no push at all.

A branch a restack has rewritten no longer fast-forwards, so `stk` pushes it
with `--force-with-lease`: the remote branch is replaced only while it still
holds the commit `stk` last published. If someone else has pushed in the
meantime the push is declined and nothing is overwritten.

```console
$ stk submit --stack
✓ Pushed sc-123/api to origin (forced)
✓ Pushed sc-123/service to origin (forced)
```

### Pull requests

`--pull` (`-p`) also opens a pull request through the
[GitHub CLI](https://cli.github.com), based on the branch's **stack parent**
rather than trunk, so each review shows only that branch's commits:

```bash
stk submit --pull                  # asks for a title and body
stk submit --pull --no-prompt      # generates both
stk submit --draft                 # a draft PR; -d implies --pull
stk submit --stack --pull          # one PR per branch, each onto its parent
```

| Flag | Short | Effect |
| --- | --- | --- |
| `--pull` | `-p` | open a pull request as well as pushing |
| `--draft` | `-d` | open every new pull request as a draft |
| `--draft-from <branch>` | | open that branch and everything above it as drafts |
| `--draft-branch <branch>` | | open just that branch as a draft; repeatable |
| `--update` | `-u` | refresh the pull requests that already exist; open none |
| `--no-prompt` | `-n` | do not ask: title is the branch name, body is one bullet per commit, nothing is a draft |
| `--stack` | `-s` | submit every branch in the stack, each onto its parent |
| `--no-comment` | | leave the stack comment on each pull request alone |

Every draft flag implies `--pull`, and they are mutually exclusive — there is
never a question of which one wins.

Without `-n` the title and body are asked for, prefilled from the branch's
commits; press enter to accept an offer. Without a terminal, `--pull` needs
`-n`, so scripts and agents never hang on a prompt.

A pull request cannot be based on a branch the remote does not have, so
ancestors that have never been pushed are pushed first — they are not
proposed, only published. `--stack` is what proposes the whole chain.

### Refreshing without proposing

`--update` (`-u`) pushes and brings the pull requests that already exist up to
date, but never opens one. It is the shape of a republish after a restack:

```console
$ stk ss -u
✓ Pushed sc-123/api to origin (forced)
✓ Pull request #1 was refreshed for sc-123/api
    https://github.com/acme/tool/pull/1
✓ Pushed sc-123/service to origin (forced)
✓ Pull request #2 was refreshed for sc-123/service
⊘ sc-123/ui has no pull request; --update opens none

3 branch(es) pushed, 2 pull request(s) refreshed.
```

Because nothing is opened, there is nothing to ask about: no title, no body,
no draft question, so `-u` needs no terminal and no `-n`. It also pushes only
the branches in scope — a plain `stk submit` publishes unpushed ancestors so a
new pull request has a base, and with `--update` there is no new pull request
to give one. A draft flag alongside `-u` is an error rather than a no-op.

`stk` says which of the two things happened to each pull request: *refreshed*
when the push moved the branch under it, *already open* when nothing moved.

### Ready and draft

A stack is usually ready at the bottom and still being written at the top, so
`stk` asks one question rather than one per branch. With no draft flag given
and more than one pull request to open, it asks where the stack stops being
ready:

```console
$ stk ss -p

Ready for review up to (everything above it opens as a draft)

  main
  sc-123/api
> sc-123/service        ← everything above this opens as a draft
  sc-123/ui

✓ Opened pull request #1 for sc-123/api onto main
✓ Opened pull request #2 for sc-123/service onto sc-123/api
✓ Opened draft pull request #3 for sc-123/ui onto sc-123/service
```

The branch you pick is ready, along with everything below it. Choosing trunk
says nothing is ready yet; choosing the topmost branch says everything is.
Dismissing the question opens no drafts. Over a pipe (`--interactive`) the name
is typed instead of picked, and `-n` skips the question altogether.

The same decision is available without asking:

```bash
stk ss -p -d                              # all of them drafts
stk ss -p --draft-from sc-123/service     # service and up are drafts
stk ss -p --draft-branch sc-123/ui        # just this one
```

Draft state is only ever decided for pull requests `stk` **opens**. To change
one that is already open:

```bash
stk ready                  # this branch's pull request is ready for review
stk ready --stack          # the whole stack
stk ready --undo           # back to a draft
```

```console
$ stk ready --stack
✓ #1 is ready for review
    https://github.com/acme/tool/pull/1
⊘ #2 is already ready for review
⊘ no pull request is open for sc-123/ui
```

### The stack comment

Every pull request in a submitted stack carries **one** `stk` comment naming
the whole chain in order, so a reviewer landing on any of them can see where it
sits:

> ### Stack
>
> 1. #1 `sc-123/api`
> 2. #2 `sc-123/service` ← this pull request
> 3. #3 `sc-123/ui`
>
> Each pull request is based on the one above it in this list, so review and
> merge from the top down.

**A pull request that has landed stays in the list.** By the time one merges
its branch is gone from the local graph, so `stk` reads its own previous
comment to recover the shape of the stack and keeps the entry where it was,
labelled:

> ### Stack
>
> 1. #1 `sc-123/api` — merged
> 2. #2 `sc-123/service` ← this pull request
> 3. #3 `sc-123/ui`

An entry whose pull request was **closed** is kept the same way. One that is
still **open** but has left the stack — moved onto another parent, say — is
dropped instead: that is somebody else's stack to describe.

It is written once and then edited in place — add a branch and every existing
comment is rewritten, not duplicated. `stk` finds its own comment by a hidden
marker and addresses the edit by that comment's id, so a comment somebody else
wrote is never touched, whatever order the timeline is in. A body that already
says exactly the right thing is left alone entirely, so nothing is bumped for
no reason.

A stack of one pull request gets no comment. `--no-comment` skips the whole
business.

An open pull request's **title, body and draft state are never edited**: `stk`
pushes the branch, prints the existing PR and leaves them alone, because
someone may have rewritten them in the browser. The stack comment is a comment
rather than an edit to the description for exactly that reason.

Its **base** is a different matter, and `stk` does keep that in step. A base is
structural — it is what makes a stacked pull request show only its own commits
— so when the stack changes shape under it, `stk` retargets it:

```console
$ stk ss -u
✓ Retargeted #3 from sc-123/service onto sc-123/api
✓ Pull request #3 was refreshed for sc-123/ui
```

Without that, a `stk move`, a `stk fold`, or a merged branch below leaves the
pull request pointing at a branch that has moved or gone, and GitHub then
computes the diff from a common ancestor further back — so the review shows
commits that belong to somebody else's pull request.

A branch whose pull request has already **merged** is never proposed again:

```console
$ stk submit -p
⊘ sc-123/api was merged as #1; not opening another
    Remove the branch with stk sync --cleanup
```

```console
$ stk submit -p
✓ Pushed sc-123/api to origin (updated)
✓ Pull request #42 is already open for sc-123/api
    https://github.com/acme/tool/pull/42
```

`gh` is needed only for `--pull`; pushing works without it. Both that it is
installed and that it is logged in to the remote's host are checked **before
the first push**, so a run cannot publish a stack of branches and only then
discover it cannot propose them:

```console
$ stk submit -sp
stk: the GitHub CLI is not logged in for github.com

    gh: You are not logged into any GitHub hosts.

Log in with:

    gh auth login --hostname github.com
```

Nothing about a pull request is stored in the repository — `stk` keeps no PR
numbers, no status and no token.

## Folding a stack

Sometimes a stack turns out to be one change. `stk fold` collapses a run of
branches into the lowest one:

```console
$ stk fold --into sc-123/api
Folding into sc-123/api:

    sc-123/service   1 commit(s), deleted
    sc-123/ui        2 commit(s), deleted

sc-123/api keeps its own base and ends up with 4 commit(s).
sc-123/polish is reparented onto sc-123/api.

Fold 2 branch(es) into sc-123/api? (Y/n)
✓ sc-123/api now ends at 6f46091
✓ Reparented sc-123/polish onto sc-123/api
✓ Folded and deleted sc-123/ui (was 6f46091)
✓ Folded and deleted sc-123/service (was 2d5cb8f)
```

| Command | Effect |
| --- | --- |
| `stk fold` | the current branch into its parent |
| `stk fold --into <branch>` | everything from here down into that branch |
| `stk fold --stack`, `-s` | the whole stack into its lowest branch |
| `--yes`, `-y` | do not ask before deleting |
| `--close-pulls` | close the folded branches' pull requests too |

**Nothing is squashed and nothing is rebased.** In a consistent stack the top
branch already contains every commit below it, so a fold only fast-forwards
the surviving branch's ref up to it — which is also how `stk` can prove the
fold loses nothing. A stack that needs a restack is refused rather than
folded:

```console
$ stk fold
stk: sc-123/ui does not contain sc-123/service, so folding would rewrite history

Restack the stack first:

    stk restack
```

The commits keep their identity, so the folded branch names are the only thing
lost — and `stk` prints the command that brings each one back:

```text
Recover a folded branch with:

    git branch sc-123/ui 6f46091
```

A branching stack cannot be folded into one branch, and `stk` says so rather
than choosing a side. Branches held by another worktree are refused too. The
remote is untouched: publish the result with `stk submit`, which force-pushes
the survivor under a lease. With `--close-pulls` the folded branches' pull
requests are closed with a comment pointing at the one that absorbed them;
their remote branches are left alone, since a closed pull request can be
reopened and a deleted branch cannot.

## Status markers

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

Global flags are long-form only, deliberately. They are stripped before an
unrecognised command is handed to git, so a short `-q` on `stk commit` would
swallow git's own `-q` instead of forwarding it.

## Flag shorthands

Per-command flags have short forms, scoped to their command the way git's are:

```text
create   -f --from
init     -t --trunk        -r --remote
track    -p --parent
untrack  -p --reparent     -r --recursive
move     -o --onto
fold     -s --stack        -y --yes
restack  -u --up           -o --only
submit   -p --pull         -d --draft       -n --no-prompt   -s --stack
         -u --update
ready    -s --stack
sync     -s --stack
stack    -a --all          -l --legend      -j --json
show     -j --json
info     -j --json
doctor   -j --json
```

Shorthands bundle, as git's do, so a run of switches is one token:

```bash
stk submit -spn        # --stack --pull --no-prompt
stk stack -alj         # --all --legend --json
stk create api -fmain  # --from main, value attached
```

`--no-checkout`, `--no-select`, `--no-restack`, `--no-cleanup`, `--cleanup`,
`--autostash`, `--no-autostash`, `--no-comment`, `--draft-from`,
`--draft-branch`, `--undo`, `--into`, `--close-pulls` and `--rebase-merges`
have none: a slipped letter
should not disable a safety, delete a branch, move someone's uncommitted work
or change what a reviewer is looking at.

## Missing arguments

Commands that need a value ask for it rather than printing usage. `stk create`
asks for the branch name; `stk rename` asks for the new name; `stk track` and
`stk move` open the branch picker to choose a parent, offering only branches
that cannot create a cycle:

```bash
stk create                  # Name for the new branch:
stk rename                  # New name for api:
stk track loose             # picker: Parent of loose
stk move web                # picker: Move web onto
```

Dismissing a prompt changes nothing and exits successfully. With
`--no-interactive` the missing value stays an error, so scripts still fail
loudly:

```console
$ stk --no-interactive create
stk: no branch name given

Run:

    stk create <branch>
```

With `--interactive` but no terminal — a pipe, or an agent — the value is read
from stdin as a line of text instead of opening the picker.

## Uncommitted changes

You do not have to commit or stash before moving around a stack. When a
command needs a clean working tree, or when git refuses to carry your changes
onto the branch you asked for, `stk` parks them, does the work, and puts them
back:

```console
$ stk restack
✓ Stashed uncommitted changes
Restacking the stack containing api...

✓ api
✓ service

1 restacked, 1 already current.
✓ Restored stashed changes
```

This applies to `create`, `checkout`, `show`, `up`, `down`, `top`, `bottom`,
`move`, `restack` and `sync`. Nothing is parked when the tree is already clean,
or when git can carry the changes across on its own — that path is left exactly
as git behaves.

To have a command refuse instead, per invocation or repository-wide:

```bash
stk restack --no-autostash          # for this run
git config stk.autostash false      # for this repository; --autostash overrides
```

```console
$ stk restack --no-autostash
stk: working tree has uncommitted changes

Autostashing is off (--no-autostash, or stk.autostash = false), so stk
will not park them for you. Commit or stash them first, or re-run with
--autostash
```

Only tracked changes are parked, exactly as with `git rebase --autostash`:
untracked files are left where they are, so nothing disappears from a directory
listing.

What happens if the changes will not go back cleanly depends on whether the
command can be undone:

| Command | On a conflicting restore |
| --- | --- |
| the ones that only switch: `create`, `checkout`, `show`, `up`, `down`, `top`, `bottom` | nothing is switched, so you keep your branch and your changes and the command fails — `create` leaves the new branch behind, unchecked out |
| the ones that rewrite history: `move`, `restack`, `sync` | the rebases stand; the conflict is left in the working tree for you to resolve and the stash is kept as well, so nothing depends on that resolution |

Changes parked for a restack belong to the operation, so a conflict does not
strand them: `stk continue` restores them when the restack finishes, and
`stk abort` restores them along with every branch. While an operation is
paused they live in `refs/stk/autostash/<id>` rather than only in the stash
reflog, and `stk doctor` reports any that a killed `stk` left behind.

## Merged branches

`stk sync` offers to remove branches that add nothing to trunk. Two things
count as merged, because the forges do both:

| How it landed | How `stk` sees it |
| --- | --- |
| merge or rebase | the branch's commits are ancestors of trunk |
| **squash** | the commits are not in trunk, but the branch's content is identical to it |

The squash case is the one a stacking tool has to get right: GitHub's default
lands a stack's bottom branch as a single new commit, so ancestry alone shows
nothing, and without the content check the branch would only be noticed on the
*next* sync — after a restack had rebased it into emptiness.

Restacking removes merged work from the branches above too, without being
asked: `git rebase` drops a commit whose change is already upstream, so a
squash-merged commit does not come back as a duplicate on its children.

```console
$ stk sync --cleanup
✓ main fast-forwarded by 1 commit(s)

The following branches add nothing to main:

    sc-123/api

✓ Reparented sc-123/service onto main
✓ Removed sc-123/api
```

Branches checked out in a worktree are never deleted, only reported.

## How the metadata is stored

Everything lives inside the repository and is shared by every worktree:

| State | Where |
| --- | --- |
| trunk, default remote, autostash preference, metadata version | `stk.*` in the repository git config |
| branch identity and logical parent | `branch.<name>.stk-id` / `.stk-parent` |
| protected base commits | `refs/stk/base/<branch-id>` |
| operation snapshots | `refs/stk/snapshot/<op-id>/<branch-id>` |
| changes parked by `--autostash` | `refs/stk/autostash/<id>` |
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
- never deletes a branch git cannot prove adds nothing to trunk — either its
  commits are already in trunk, or its content is identical to trunk's — or,
  when folding, that its commits live on in the branch that absorbs it;
- never deletes or rewrites a branch checked out in another worktree;
- never loses your uncommitted changes: it parks them, puts them back, and
  leaves them in the stash list if they will not reapply (`--no-autostash` to
  refuse the operation instead);
- never rewrites a branch whose recorded base cannot be validated;
- never flattens merge commits without `--rebase-merges`;
- never hides a git conflict;
- never initialises a repository silently;
- never pushes unless you run `stk submit`, and never force-pushes without a
  lease on what it is replacing;
- never merges, and never edits a pull request it did not open in that run.

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
internal/operations/    create, track, rename, move, fold, restack, submit, ready, sync, continue, abort
internal/forge/         the only code that knows about GitHub, through gh
internal/config/        repository-wide settings
internal/ui/            branch picker, prompts, tree rendering
internal/output/        text and JSON output
```

The module path is `stk`; change it in `go.mod` when the repository gets a
permanent home.
