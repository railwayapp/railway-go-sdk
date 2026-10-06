# Contributing

## Setup

```bash
go test ./...
```

CI runs the same command on every PR and on `main`.

## Releases

This repo uses release labels, not semantic commits or PR title conventions.

Every PR must have exactly one release label (the `Release Label` check fails
otherwise):

- `release/patch`
- `release/minor`
- `release/major`
- `release/skip`

Merging a `release/patch`, `release/minor` or `release/major` PR runs the
`Auto Release` workflow: it waits two minutes to batch closely spaced merges,
waits for CI on `main`, picks the largest bump among the PRs merged since the
last tag, pushes the `vX.Y.Z` tag, and publishes a GitHub release with one line
per merged PR. There is no registry step: the Go module proxy serves the module
straight from the tag, and the workflow waits until `proxy.golang.org` has it.

To cut a release by hand, run the `Create Release` workflow from the Actions tab
and pick the bump type.

A `release/major` bump to `v2.0.0` or later needs the `/vN` suffix on the module
path in `go.mod` first; the workflow refuses to tag otherwise. The version in
the `go.mod` snippet in the README is updated by hand.
