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
request when you ask it to — and, if you opt in, to
[`gh stack`](https://github.com/github/gh-stack) to link those pull requests
as a stack on GitHub — and everything else works with no forge at all.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/egmacke/stk/main/install.sh | sh
```

That installs a prebuilt binary for `linux` or `darwin`, on `amd64` or `arm64`,
into `~/.local/bin`. The archive is checked against the release's own
`checksums.txt` before it is unpacked; a download that does not match is
discarded rather than run. Nothing needs root.

```bash
STK_VERSION=v0.1.0 sh install.sh        # pin a version
STK_INSTALL_DIR=$HOME/bin sh install.sh # choose where it goes
```

Later:

```bash
stk upgrade           # replace this binary with the latest release
stk upgrade --check   # only say whether a newer one exists
```

`stk upgrade` verifies downloads exactly as `install.sh` does, and needs no
repository — it touches no branch, ref or metadata.

### Being told about a new release

`stk` also looks for a newer release on its own, at most once an hour, in the
background of a command you were running anyway. When it finds one, it says so
*after* the command has finished and offers to install it:

```text
✓ Restacked 3 branches

stk v1.3.0 is available (you have v1.2.0).

If you skip this, stk will not mention v1.3.0 again.
You can upgrade at any time by running:

    stk upgrade

Upgrade now? (Y/n)
```

Answering `n` records that version and the offer is not repeated — but only
that version. The next release is a new question, and `stk upgrade` still works
at any time.

The check never gets in the way. It runs concurrently with the command, is
abandoned if the answer has not arrived a second after the work is done, and a
release server that is unreachable is passed over in silence. It cannot fail a
command or change its exit code, and it says nothing at all when:

- the command failed, or stopped on a conflict — the terminal is for that;
- there is nobody to ask: no terminal, or `--no-interactive`, or `CI` is set;
- output was meant to be read by something else: `--quiet`, `--dry-run`, `--json`;
- the command is `stk upgrade`, `stk version`, help, completion, or anything
  passed through to git;
- this build is not a published release, so there is no telling which is newer.

To turn it off:

```bash
export STK_NO_UPDATE_CHECK=1          # one shell
git config --global stk.updateCheck false   # for good
git config stk.updateCheck false      # in one repository
```

With Go, which also works on platforms stk publishes no binaries for:

```bash
go install github.com/egmacke/stk@latest
```

A binary installed that way reports the version it was built from — the go
command applies no build flags of its own, so `stk` reads the module version
the toolchain records instead. `stk upgrade` works on it as usual.

From a checkout:

```bash
make install          # to $GOBIN
# or
make build && cp stk /usr/local/bin/
```

Every release also carries a build provenance attestation, so you can check
that an archive came from this repository's release workflow rather than
someone's laptop:

```bash
gh attestation verify stk_v0.1.0_linux_amd64.tar.gz --repo egmacke/stk
```

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
| `stk checkout [branch]`, `stk co` | Switch branches; interactive picker with no argument; fetches a branch only the remote has, and offers to track it |
| `stk show` | The same viewer, without implying checkout is the goal |
| `stk stack [--all] [--json]` | Print the current stack |
| `stk info [branch] [--json]` | Everything stk knows about a branch |
| `stk parent` / `stk children` | Print the immediate relatives of a branch |
| `stk up [n]` / `stk down [n]` / `stk top` / `stk bottom` | Navigate the stack |
| `stk track [branch] [--parent <p>]`, `stk track --from-pr <pr>` | Adopt an existing branch into the graph, or a whole stack from GitHub |
| `stk untrack [branch]` | Drop metadata; the git branch is never deleted |
| `stk rename [old] [new]` | Rename without breaking the stack, remote branch included |
| `stk move [branch] [--onto <p>]` | Re-parent a branch and restack above it |
| `stk fold [branch] [--into <b>\|--stack]` | Collapse stacked branches into one |
| `stk split [branch] [--at <commit>]` | Divide a branch into several stacked branches |
| `stk delete [branch...] [-y] [--remote]` | Delete branches, and offer to delete their remote branches |
| `stk restack [--up\|--only]` | Rebase branches onto their parents |
| `stk submit [branch] [--pull] [--draft] [--stack] [--force]`, `stk s`, `stk ss` | Push to the remote, optionally opening pull requests |
| `stk ready [branch] [--stack] [--undo]` | Take a pull request out of draft, or put it back |
| `stk sync [--stack] [--cleanup\|--no-cleanup] [--no-restack]` | Fetch, update trunk, prune finished branches, restack |
| `stk continue` / `stk abort` | Resume or abandon an interrupted operation |
| `stk doctor [--json]` | Validate metadata against the repository |
| `stk version`, `stk upgrade`, `stk completion <shell>` | Build info, self-update and shell completion |

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

### Branches in other worktrees

A branch another worktree has checked out is still restacked, by `stk restack`
and by `stk sync` alike. The rebase runs *inside* that worktree, which is the
only way git will rewrite the branch at all, and means its index and files move
with the ref instead of being left describing history that is no longer there.
It is the same route `stk sync` takes to fast-forward a trunk checked out
elsewhere. `stk` says where the work happened:

```console
$ stk restack

Restacking the stack containing sc-123/api...

✓ sc-123/api
✓ sc-123/service in /home/you/work/wt-service
✓ sc-123/ui

2 restacked, 1 already current.
```

Two things stop it, and both leave the branch exactly where it was:

- **Uncommitted changes there.** A working tree you are not standing in is not
  `stk`'s to disturb, so the branch is skipped and everything above it is
  reported blocked rather than rebased against uncertain state. The same goes
  for a worktree part-way through a rebase, merge, cherry-pick, revert or
  bisect.
- **A conflict.** A conflict is only `stk`'s to pause where the journal lives —
  `stk continue` and `stk abort` belong to one worktree, and a rebase left
  paused in another would wedge a checkout you never pointed `stk` at. So the
  rebase is undone there and the branch reported, for you to resolve where it
  belongs:

```text
⊘ sc-123/service blocked: conflict while rebasing in /home/you/work/wt-service
  Resolve it by running stk restack from that worktree.
```

Deleting is different: `stk sync`, `stk delete` and `stk fold` still refuse a
branch another worktree holds, because removing it would leave that worktree on
a branch that no longer exists.

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

`--force` (`-f`) is the way past that lease. The remote branch is replaced
with what you have, whatever it holds, and any commit on it that is not in
yours goes with it:

```console
$ stk submit
stk: pushing sc-123/api was refused

origin/sc-123/api has moved since stk last saw it, so the force-with-lease was
declined and nothing was overwritten. Fetch and restack, then submit again:

    stk sync

Or replace it with what you have, whatever it holds:

    stk submit --force

$ stk submit --force
✓ Pushed sc-123/api to origin (forced, no lease)
```

`stk` says `forced, no lease` rather than `forced` so the two are never
confused in a log. `-f` only ever changes a **diverged** branch: one that
fast-forwards is pushed the same way with it or without it, one the remote
already holds is still skipped, and a branch the remote has never seen is
still just created. It is also the switch for a run that must not stop to ask
anything, so it implies `-n`.

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
| `--no-prompt` | `-n` | do not ask: title from `stk.prTitle`, body is one bullet per commit, nothing is a draft |
| `--stack` | `-s` | submit every branch in the stack, each onto its parent |
| `--force` | `-f` | replace a diverged remote branch whatever it holds; implies `-n` |
| `--no-comment` | | leave the stack comment on each pull request alone |
| `--no-link` | | with `stk.githubStacks`, leave the stack on GitHub alone |

Every draft flag implies `--pull`, and they are mutually exclusive — there is
never a question of which one wins.

Without `-n` the title and body are asked for, prefilled from the branch's
commits; press enter to accept an offer. Without a terminal, `--pull` needs
`-n`, so scripts and agents never hang on a prompt.

#### Where a generated title comes from

With `-n` there is nobody to ask, so `stk` writes the title itself: the branch
name. `stk.prTitle` says so explicitly, and can say otherwise instead:

```bash
git config stk.prTitle branch   # the branch name; the default
git config stk.prTitle commit   # the subject of the branch's first commit
```

`commit` takes the *first* commit's subject, not the latest, so the title
stays put as more commits land on the branch; a branch whose name already
reads like a sentence is better served by `branch`. Either way the setting is
only about the title `stk` writes on its own — the prompt has always offered
that first subject, and still does. Any other value is refused rather than
quietly ignored.

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

### GitHub stacks

GitHub can hold the shape of a stack itself: a *stack* of pull requests, shown
on every one of them, managed with the
[`gh stack`](https://github.com/github/gh-stack) extension. `stk` can work
with it instead of alongside it. It is opt-in, per repository:

```bash
gh extension install github/gh-stack
git config stk.githubStacks true
```

With that set, `stk` and `gh stack` describe one stack, kept in step by `stk`:

- **`gh stack`'s local tracking is a mirror of `stk`'s graph.** Every command
  that changes the graph — `create`, `track`, `untrack`, `rename`, `move`,
  `restack`, `sync`, `continue`, `abort` — rewrites `gh stack`'s file
  afterwards, so `gh stack view`, `gh stack up`, `gh stack push` and the rest
  see exactly what `stk stack` sees. `gh stack`'s *base* of a branch is the
  same fact as `stk`'s: the parent's tip the branch was last rebased onto.
- **Branches added through `gh stack` are adopted by `stk`.** `gh stack add`,
  `gh stack init` or `gh stack checkout` record a branch `stk` does not know;
  the next `stk sync`, or the next change to the graph, tracks it with the
  branch below it in `gh stack`'s list as its parent and `gh stack`'s base as
  its base. For a branch both know, `stk`'s graph is the record.
- **Pull requests are shared.** `stk submit --pull` records the pull requests
  it opens or finds in `gh stack`'s tracking, so they show in `gh stack view`
  at once and in `stk stack`, `stk info` and `--json` as `#N`; `stk sync` asks
  GitHub about them and marks the merged ones. The pull requests of a stack
  are linked as a GitHub stack through `gh stack link`, and no stack comment
  is written — GitHub shows the stack on each pull request already.
- **A stack on GitHub can be brought in whole.** `stk track --from-pr <n>`
  runs `gh stack checkout` for a pull request number, URL or stack number:
  the branches are fetched and checked out, and `stk` tracks each with the
  parent GitHub has for it.
- **A stack linked outside this checkout is found by `stk sync`.** `gh
  stack`'s local tracking records only what happened here, so a stack linked
  in the browser, by a colleague, or from another clone is invisible until
  something asks GitHub itself. `stk sync` asks with the same `gh stack link`
  that `stk submit --pull` ends with — pull request numbers, so nothing is
  pushed and nothing is opened, and a run already stacked on GitHub is left as
  it stands — and gh stack's tracking comes back to what GitHub holds.

```console
$ stk sync --no-restack
Fetching origin...
✓ main is already up to date
Checking pull requests...
✓ #1, #2 are a stack on GitHub
```

`stk sync` settles every stack that runs from trunk to a tip without forking.
Where the graph forks there is no single stack to link — `gh stack link` is
additive, so linking each side in turn would gather both into one stack that is
the shape of neither — so `stk sync` says nothing and leaves the choice to
`stk submit`, which is given the branch. `stk sync --stack` names one side the
same way. `--no-pulls` skips the question entirely.

```console
$ stk track --from-pr 11
Running gh stack checkout 11...
✓ Checked out stack #3 at feat/ui
✓ Tracking feat/api with parent main (from gh stack)
✓ Tracking feat/ui with parent feat/api (from gh stack)
✓ gh stack tracking updated

2 branch(es) tracked from the stack on GitHub.
```

```console
$ stk ss -pn
✓ Pushed sc-123/api to origin (created)
✓ Opened pull request #1 for sc-123/api onto main
✓ Pushed sc-123/service to origin (created)
✓ Opened pull request #2 for sc-123/service onto sc-123/api
✓ gh stack tracking updated
✓ Linked #1, #2 as a stack on GitHub

2 branch(es) pushed, 2 pull request(s) opened, linked as a stack on GitHub.
```

The link is made by pull request **number**, never by branch name, so
`gh stack link` neither pushes nor opens anything: `stk` has already done both,
its own way, with its own lease. `--no-link` skips the link for one run.

A `gh stack` stack is strictly linear and `stk`'s graph need not be, so where
the graph forks the chain ends: each child of the fork begins a stack of its
own whose trunk is the fork branch. Every branch is in exactly one `gh stack`
stack and every parent is the graph's. On GitHub, what gets linked is the
chain through the branch being submitted — everything below it, and above it
while each branch has exactly one child. The chain also stops below a branch
with no pull request, because linking across that gap would have `gh stack`
retarget the pull request above it onto the branch below, changing what its
reviewer is looking at. A stack of one pull request is not linked.

What stays `stk`'s: pushing, rebasing, navigation and the working tree.
`gh stack` has commands for all of them, but `stk`'s know the graph, keep a
journal `stk continue` and `stk abort` can use, push under a lease on the
commit they are replacing, and never push unless asked. Because the tracking
is mirrored, `gh stack`'s versions work too, on the same stack.

That the extension is installed is checked **before the first push**, like the
login, so a run never publishes a stack it then cannot link. A repository
without stacked pull requests enabled is reported by `gh stack` after the pull
requests are open, and `stk` says so plainly — the pushes and pull requests
stand, only the link is missing — along with how to go back to the comment.
`stk doctor` reports a repository that has opted in without the extension, and
a mirror that has fallen out of step, which the next `stk sync` settles.

`gh stack` reads its tracking from the main worktree's git directory alone, so
that is where `stk` writes the mirror; `stk` itself works from any worktree.
The file is written under `gh stack`'s own lock, so the two never race. A
mirror that cannot be written — a newer `gh stack` schema, a lock held too
long — is a warning, not a failure: `stk`'s own record is already made, and
`stk doctor` reports the drift until the next change repairs it.

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

## Splitting a branch

The other direction: one branch that should have been a stack. `stk split`
cuts after the commits you choose.

```console
$ stk split

Commits on big, oldest first:

   1  3eeaaf1  Add parser
   2  ed68243  Add lexer
   3  61b2710  Wire them up
   4  185a664  Add docs

Split after which commits? (numbers, comma separated, 1-3) 1,3
Name for the branch ending at 3eeaaf1: [big-1] parser
Name for the branch ending at 61b2710: [big-2] wiring

✓ Created parser at 3eeaaf1 on main
✓ Created wiring at 61b2710 on parser
✓ big now sits on wiring
```

```text
main                  main
└─ big        →       └─ parser
                         └─ wiring
                            └─ big
```

**The branch keeps its name and ends up on top.** Its pull request therefore
keeps its identity and simply shows fewer commits, and anything already
stacked on it stays where it is — the new branches appear underneath.

**Nothing is rebased.** The commits are already in a line, so splitting points
new branches at commits that are already there and rewrites the metadata. Every
commit id is untouched.

The commits are numbered rather than picked from a full-screen list, so the
same prompt works down a pipe. For scripts, name the points and the branches
instead, oldest first:

```bash
stk split --at big~3 --name parser --at big~1 --name wiring
```

The tip cannot end a segment (there would be nothing above it), points must
run in history order, and a name that already exists is refused before
anything is created. Answering nothing changes nothing.

## Deleting branches

`stk delete` removes local branches and offers to take their remote
counterparts with them.

```console
$ stk delete sc-123/api
Deleting:

    sc-123/api   nothing that main or another branch lacks

sc-123/ui is reparented onto main, and will need a restack.

Delete 1 branch(es)? (Y/n) y

✓ Reparented sc-123/ui onto main
✓ Deleted sc-123/api (was 2d5cb8f)

These branches also exist on the remote:

    origin/sc-123/api

GitHub closes any open pull request whose head branch is deleted.

Delete 1 remote branch(es) as well? (y/N) y
✓ Deleted origin/sc-123/api
```

Every question is answered by `-y`, the remote one included. `--remote` and
`--no-remote` settle just that half without asking.

Before it asks, `stk` counts what each branch keeps that nothing else does — not
trunk, and not another branch — and says so. When the answer is not zero the
prompt defaults to *no*, and afterwards `stk` prints the one command that brings
the branch back:

```console
$ stk delete sc-123/spike
Deleting:

    sc-123/spike   3 commit(s) kept nowhere else

Delete 1 branch(es)? (y/N) y

✓ Deleted sc-123/spike (was 6f46091)

Recover a deleted branch with:

    git branch sc-123/spike 6f46091
```

Branches stacked above a deleted one are reparented onto the nearest ancestor
that survives, so the graph keeps its shape; they are left needing a restack
rather than rewritten here. Trunk is never deleted, and neither is a branch
checked out in another worktree.

With no argument the current branch is deleted, and `stk` steps down to the
nearest branch that survives first.

## Renaming a branch

Stack relationships are keyed by stable ids, so a rename leaves parents and
children exactly where they were. When the branch has been published, `stk`
offers to move the remote branch with it:

```console
$ stk rename sc-123/api sc-123/backend

sc-123/api publishes to origin/sc-123/api.

GitHub closes any open pull request whose head branch is deleted, so a pull
request open for sc-123/api will not survive the rename.

Rename origin/sc-123/api to origin/sc-123/backend as well? (y/N) y
Renamed:
    sc-123/api -> sc-123/backend
    origin/sc-123/api -> origin/sc-123/backend
```

The new name is pushed and the old one deleted in a single push, and the
upstream link follows. Say no — or pass `--no-remote` — and the remote branch
stays where it is, with the two commands to move it by hand printed for you.
`-y` and `--remote` answer yes without asking. A remote branch already using
the new name is never overwritten.

Along with `stk delete --remote`, this is the only push outside `stk submit`,
which is why it is always asked for and never assumed.

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
track    -p --parent       (--from-pr has no short form)
untrack  -p --reparent     -r --recursive
move     -o --onto
fold     -s --stack        -y --yes
restack  -u --up           -o --only
submit   -p --pull         -d --draft       -n --no-prompt   -s --stack
         -u --update       -f --force
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
`--autostash`, `--no-autostash`, `--no-comment`, `--no-link`, `--draft-from`,
`--draft-branch`, `--undo`, `--into`, `--close-pulls` and `--rebase-merges`
have none: a slipped letter
should not disable a safety, delete a branch, move someone's uncommitted work
or change what a reviewer is looking at.

`submit -f` is the deliberate exception. It is the flag you reach for when a
lease keeps declining, which is the moment you least want to be typing
`--force` in full — and unlike the flags above it does nothing at all unless
the remote branch has diverged from yours.

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

The picker opens on the branch you are already on, not on trunk, so pressing
enter straight away changes nothing. Typing filters the list, and the highlight
stays on its branch for as long as that branch still matches.

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

## Finished branches

`stk sync` offers to remove the branches you are done with. Four things say a
branch is finished, and they do not all carry the same weight:

| Signal | How `stk` sees it | Proof? |
| --- | --- | --- |
| merge or rebase | the branch's commits are ancestors of trunk | yes |
| **squash** | the commits are not in trunk, but the branch's content is identical to it | yes |
| pull request merged | the forge says it landed | yes |
| pull request closed, or the remote branch deleted | the remote is done with it | **no** |

The squash case is the one a stacking tool has to get right: GitHub's default
lands a stack's bottom branch as a single new commit, so ancestry alone shows
nothing, and without the content check the branch would only be noticed on the
*next* sync — after a restack had rebased it into emptiness.

Restacking removes merged work from the branches above too, without being
asked. `git rebase` drops a commit whose change is already upstream, which
covers a merge or a rebase — but **not** a squash, where several commits become
one and no patch matches. Replaying those on top of their own merged result is
nothing but conflicts, for work that is already in, so `stk` recognises the
shape and collapses the branch onto its parent instead:

```console
$ stk restack
✓ sc-123/api is already in main; its own commits went in with the merge
✓ sc-123/service
```

`sc-123/api` is then plainly contained in trunk, so the next `stk sync
--cleanup` can take it away, and everything above it has been rebased onto the
squashed commit exactly once.

```console
$ stk sync --cleanup
✓ main fast-forwarded by 1 commit(s)

The following branches add nothing to main:

    sc-123/api   already in main

✓ Reparented sc-123/service onto main
✓ Removed sc-123/api
```

A branch checked out *here* is stepped off first, so the one you are standing
on when its pull request lands is pruned like any other. A branch checked out
in another worktree is reported and left alone: deleting it would leave that
worktree on a branch that no longer exists. Restacking one is a different
matter — see [Branches in other worktrees](#branches-in-other-worktrees).

Whatever the survivors were proposed onto is corrected in the same pass. The
branch below them has merged or gone on the remote, and a pull request left
pointing at it makes GitHub compute the diff from further back — so the review
shows commits belonging to the pull request underneath:

```console
✓ Reparented sc-123/service onto main
✓ Removed sc-123/api
✓ Retargeted #42 from sc-123/api onto main
```

A base is the one thing `stk` maintains on a pull request it did not open, and
this is the same correction `stk submit` makes after a `stk move` or a
`stk fold`. Merged and closed pull requests are records rather than reviews, so
their bases are left alone, and `--no-pulls` skips the whole business.

The last row is the one with no proof: a closed pull request or a deleted
remote branch may still leave the local branch as the only copy of its commits.
Those are listed separately, with what each would take with it, and the prompt
defaults to *no*:

```console
$ stk sync
✓ main fast-forwarded by 2 commit(s)
Checking pull requests...

The following branches add nothing to main:

    sc-123/api   #41 merged

Remove these 1 local branches? (Y/n) y

The following branches are finished on the remote:

    sc-140/spike   #48 closed, 3 commit(s) kept nowhere else

The remote is done with them, but nothing proves their commits reached main.

Remove these 1 local branches? (y/N) n
Leaving them in place.

✓ Removed sc-123/api
```

Reading pull request states costs one `gh pr list` for the whole stack, and
another only for a branch whose pull request is older than that listing.
`--no-pulls` skips the forge entirely and works from git alone; a repository
with no reachable GitHub remote does the same on its own, without failing the
sync. `--cleanup` answers yes to both lists, `--no-cleanup` skips them.

`stk sync` never pushes, so it never touches a remote branch — deleting one is
`stk delete --remote`. Two things it does change on the forge, both structural
rather than authored and neither touching a branch: the base of a survivor's
pull request, and — with `stk.githubStacks` on — the link between the pull
requests of a stack, which is what finds a stack linked outside this checkout.

## How the metadata is stored

Everything lives inside the repository and is shared by every worktree:

| State | Where |
| --- | --- |
| trunk, default remote, autostash, GitHub stacks and PR title preferences, metadata version | `stk.*` in the repository git config |
| mirror of the graph for `gh stack` (opt-in) | `<git-common-dir>/gh-stack`, `gh stack`'s own file |
| branch identity and logical parent | `branch.<name>.stk-id` / `.stk-parent` |
| protected base commits | `refs/stk/base/<branch-id>` |
| operation snapshots | `refs/stk/snapshot/<op-id>/<branch-id>` |
| changes parked by `--autostash` | `refs/stk/autostash/<id>` |
| in-flight operation journal | `<git-common-dir>/stk/operations/current.json` |

Because the repository config and refs live in the git *common* directory,
every linked worktree sees the same stack graph. `stk init` creates no tracked
files.

Two keys are the exception, and live in your own `~/.gitconfig` rather than in
a repository, because they describe the `stk` binary rather than any one clone:

| State | Where |
| --- | --- |
| release you declined to upgrade to | `stk.skipVersion` |
| when stk last looked for a release | `stk.lastUpdateCheck` |

`stk.updateCheck` is read from whichever scope sets it, so a repository can
turn the check off for everyone working in it.

Branches are identified by a stable id rather than by name, so `git branch -m`
leaves the graph intact. Each child also records the parent commit it was last
valid against, which is what lets `stk` run a precise
`git rebase --onto <new base> <old base> <branch>` instead of guessing which
commits belong to the branch.

## Safety

`stk` is deliberately conservative:

- never silently overwrites a diverged trunk;
- never chooses a stack parent for you;
- never deletes a branch, in `stk sync` or `stk fold`, without proof its commits
  live on somewhere else — in trunk by ancestry or by identical content, in the
  branch a fold absorbs it into, or in a pull request the forge says landed;
- deletes a branch with no such proof only where you named it — `stk delete`, or
  the separate `stk sync` list that defaults to *no* — and prints the one
  command that brings it back;
- never deletes a branch checked out in another worktree, and rewrites one only
  from inside the worktree that holds it, never while that worktree has
  uncommitted changes or a git operation of its own in flight;
- never loses your uncommitted changes: it parks them, puts them back, and
  leaves them in the stash list if they will not reapply (`--no-autostash` to
  refuse the operation instead);
- never rewrites a branch whose recorded base cannot be validated;
- never flattens merge commits without `--rebase-merges`;
- never hides a git conflict;
- never initialises a repository silently;
- never pushes unless you run `stk submit`, or ask `stk rename` or `stk delete`
  to move or remove a remote branch, and never force-pushes without a lease on
  what it is replacing — unless you drop the lease by name, with
  `stk submit --force`;
- never merges, and never edits the title, body or draft state of a pull
  request it did not open in that run — only the base, which is structural;
- never replaces its own binary without asking, and never asks twice about a
  release you turned down.

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
internal/operations/    create, track, rename, move, fold, split, restack, submit, ready, sync, continue, abort
internal/forge/         the only code that knows about GitHub, through gh
internal/ghstack/       gh stack's local tracking file, read and written in step with the graph
internal/config/        repository-wide settings
internal/update/        the background check for a newer release
internal/ui/            branch picker, prompts, tree rendering
internal/output/        text and JSON output
```

The module path is `github.com/egmacke/stk`, which is what lets
`go install github.com/egmacke/stk@latest` resolve.

### Releasing

Releases are cut by merging a pull request. See [docs/RELEASING.md](docs/RELEASING.md).

Pull request titles must be [conventional commits](https://www.conventionalcommits.org)
(`feat:`, `fix:`, `docs:`, …), because they are squash-merged and the resulting
commit message on `main` is what decides the next version number and writes the
changelog. CI checks the title on every pull request.
