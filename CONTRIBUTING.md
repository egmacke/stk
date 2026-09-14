# Contributing

```bash
make lint     # gofmt + go vet
make test     # integration tests against real temporary repositories
```

## Pull request titles

Pull requests are squash-merged, so the title becomes the commit message on
`main` — and that message is what decides the next version number and writes
the release notes. Titles must be [conventional
commits](https://www.conventionalcommits.org):

```
feat: add stk upgrade
fix: restack no longer drops the autostash on abort
docs: explain the metadata format
chore: bump cobra
```

Use `feat:` for anything a user would notice, `fix:` for a bug they could hit,
and `chore:`/`ci:`/`build:`/`test:`/`style:` for work that should not appear in
the notes at all. Mark a breaking change with a `!` — `feat!: …` — or a
`BREAKING CHANGE:` footer.

CI checks the title on every pull request, because a title release-please
cannot parse does not fail anything: it silently leaves the change out of the
release notes.

Individual commits within a branch are not checked; only the title that lands
on `main`.

## Releasing

See [docs/RELEASING.md](docs/RELEASING.md). Short version: merge the
`chore: release X.Y.Z` pull request that release-please keeps open.
