# Releasing

A release is cut by merging a pull request. Nothing is tagged by hand, and
nothing releasable is ever built on a laptop.

## The short version

1. Merge normal work into `main` with a conventional commit title.
2. release-please keeps a `chore: release X.Y.Z` pull request open, holding the
   changelog accumulated since the last tag. Review it.
3. Merge it. That tags the version, publishes the notes, builds the `linux` and
   `darwin` binaries and attaches them to the release.

## How a version number is decided

`main` is squash-merged, so each pull request becomes one commit whose message
is the pull request title. release-please reads those titles:

| Title prefix | Effect on the version | Appears in the notes as |
| --- | --- | --- |
| `feat:` | minor bump (`0.1.0` → `0.2.0`) | Features |
| `fix:` | patch bump (`0.1.0` → `0.1.1`) | Bug fixes |
| `perf:` | patch bump | Performance |
| `refactor:` | patch bump | Internal changes |
| `docs:` | patch bump | Documentation |
| `revert:` | patch bump | Reverts |
| `build:`, `ci:`, `chore:`, `test:`, `style:` | none | hidden |
| any of the above with `!`, or a `BREAKING CHANGE:` footer | see below | Breaking changes |

While the version is below `1.0.0`, a breaking change bumps the **minor**
version, not the major one. `0.x` is the promise that the command line and the
`stk.*` metadata format may still move; the `ci.yml` title check enforces the
grammar, not the judgement about what deserves a `feat:`.

A pull request whose title CI cannot parse is not a release failure — it is
worse, because release-please would silently drop it from the notes. That is
why the title is checked on every pull request rather than at release time.

## What happens on merge

`release-please.yml` runs on every push to `main`.

- Nothing releasable accumulated: it opens or updates the release pull request
  and stops.
- The merged commit *was* the release pull request: it tags the commit, writes
  `CHANGELOG.md`, publishes the GitHub release, and calls `release.yml`.

`release.yml` then, for that tag:

1. re-runs `make lint` and `make test` — a tag that cannot pass its own tests
   must not produce binaries, because a downloaded archive cannot be recalled;
2. runs `make dist`, cross-compiling `linux/{amd64,arm64}` and
   `darwin/{amd64,arm64}` and writing `checksums.txt`;
3. attests the archives with GitHub's keyless OIDC identity, recording which
   workflow built them from which commit;
4. uploads the archives to the existing release, leaving the notes untouched.

`release.yml` is called from `release-please.yml` rather than triggered by the
tag, because **GitHub does not start workflows from pushes made with
`GITHUB_TOKEN`**. A tag-triggered build would simply never fire. It still has a
`push: tags` trigger for tags pushed by a human, which that rule does not
suppress, so the two cannot double-build the same release.

## The first release

The manifest starts at `0.0.0` with `bootstrap-sha` pinned to the commit that
came before this process existed, so nothing in the history before it is
mistaken for a release. The first `feat:` merged after that produces `v0.1.0`.

## Retrying a failed build

If the tag and release exist but the archives are missing — a flaky runner, a
transient network failure — re-run the build alone, without re-running
release-please:

```
Actions → release → Run workflow → tag: v0.1.0
```

It rebuilds from the tag and re-uploads with `--clobber`. It is safe to run
more than once: the build is reproducible from the tag, and the release notes
are never rewritten.

## If a release is wrong

Do not delete or move the tag. Anyone who already ran `install.sh` or
`stk upgrade` has those bytes, and a moved tag means two different binaries
claim the same version — which `stk version` then reports as a lie.

Ship a `fix:` and release again.

## Verifying a release by hand

```bash
tag=v0.1.0
gh release download "$tag" --repo egmacke/stk
sha256sum -c checksums.txt
gh attestation verify "stk_${tag}_linux_amd64.tar.gz" --repo egmacke/stk
```

## What a tag gives you for free

Tagging `vX.Y.Z` also publishes the module: `go install
github.com/egmacke/stk@vX.Y.Z` resolves through the Go module proxy without any
step in this repository. Nothing has to be pushed anywhere for that to work, but
it does mean **a tag is permanent** — the proxy caches what it fetched, so a
moved tag serves whatever it saw first. One more reason to ship a `fix:` rather
than retag.

Binaries installed that way carry no `-ldflags`, so `stk version` falls back to
the module version the toolchain records. That path has no repository behind it,
so `commit:` and `built:` are omitted rather than printed as "unknown".

## The archive names are an interface

`make dist`, `install.sh` and `stk upgrade` all construct
`stk_<tag>_<os>_<arch>.tar.gz` and parse the `<digest>  <name>` lines of
`checksums.txt` independently. Changing either shape breaks the ability of
every already-installed copy of stk to upgrade itself, so change all three
together, and only for a good reason.

## Who finds out, and when

Publishing a release is also how installed copies of stk learn there is one.
Every stk that can prompt checks `/releases/latest` at most once an hour, in the
background of a command, and offers the release to the user once. A user who
declines records the tag in `stk.skipVersion` and is not asked about that
version again — so a release is one offer per user, not a recurring prompt.

Two consequences for anyone cutting a release:

- **A tag people should not install is not free to withdraw.** Deleting the
  release makes `/releases/latest` point at the previous tag again, but anyone
  already offered the bad one has either taken it or declined it, and declining
  is remembered. Ship a `fix:` rather than unpublish.
- **`/releases/latest` is load-bearing.** The check reads the redirect it
  serves, not the REST API, so it costs no rate limit and works for everyone
  behind a shared address. A release that is left as a draft, or marked
  pre-release, does not move that redirect and nobody is told about it.
