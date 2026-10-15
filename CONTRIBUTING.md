# Contributing

Thanks for considering a contribution to Agent-Local.

## Ground rules

- **Read the ADRs first.** [docs/adr](docs/adr/README.md) records decisions that are
  expensive to reverse: one static Go binary without cgo, backends over HTTP instead of
  in-process inference, SQLite for all state, the Agent Skills format, the human gate on
  learned skills, and local-only defaults. A change that contradicts one starts with a
  new ADR, not a pull request.
- **The tiny profile is the bar.** Features must work on an 8 GB machine.
- **Fail loudly.** Never let a backend truncate, drop, or degrade silently.
- **Tests first for logic.** Hardware parsing, planners, adapters, and policies are
  tested against fixtures; no test needs a running model or network access.
- **Small, atomic commits.** One concern per commit, Conventional Commits style
  (`feat(scope):`, `fix(scope):`, `docs:`, `test:`, `build:`, `chore:`).
- **No secrets or private data.** Never include real credentials, hostnames, personal
  files, or traces from real sessions in issues, commits, or fixtures.

## Getting started

```sh
git clone https://github.com/hi-donwi/Agent-Brain.git
cd Agent-Local
make check
```

## Pull request checklist

- [ ] `make check` is green (gofmt, tidy, vet, race tests, smoke run)
- [ ] `make static-check` still produces static Linux binaries
- [ ] New logic has a unit test that failed before the change
- [ ] README, ROADMAP, or docs updated when behaviour or plans changed
- [ ] `--json` output shapes unchanged unless the change is announced as breaking
