# Dependabot PR Cleanup

A GitHub Action that tidies up Dependabot pull requests for use with **GitHub merge queues**.

## Why?

When using a [merge queue](https://docs.github.com/en/repositories/configuring-branches-and-rulesets/configuring-pull-request-merges/managing-a-merge-queue), GitHub includes the PR description in the merge commit. Dependabot PR descriptions are long and full of changelogs, release notes, and compatibility scores — great for reviewing, but noisy in your git history.

This action:

1. Saves the original Dependabot PR description as a **comment** (so nothing is lost)
2. Replaces the PR body with the **latest commit message** (e.g. `Bump golang.org/x/net from 0.24.0 to 0.25.0`)

The result is clean, one-line merge commits when PRs flow through the merge queue.

## Usage

Add this to a workflow that triggers on Dependabot PRs:

```yaml
name: Dependabot PR Cleanup
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  cleanup:
    runs-on: ubuntu-latest
    if: github.actor == 'dependabot[bot]'
    permissions:
      pull-requests: write
    steps:
      - uses: jmhodges/dependabot-pr-merge-cleanup@main
```

### Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `token` | GitHub token with `pull_requests:write` | Yes | `${{ github.token }}` |
| `repo` | Repository in `owner/name` format | No | `${{ github.repository }}` |
| `pr` | Pull request number | No | `${{ github.event.pull_request.number }}` |
| `dry-run` | Print what would happen without making changes | No | `false` |

### Dry run

Preview what the action would do without modifying anything:

```yaml
- uses: jmhodges/dependabot-pr-merge-cleanup@main
  with:
    dry-run: 'true'
```

## How it works

1. Fetches the PR and confirms it was authored by `dependabot[bot]`
2. Skips PRs with an already-empty body
3. Gets the latest commit message from the PR
4. Strips `Signed-off-by:` trailers from the commit message (to prevent duplication in merge commits)
5. Posts the original PR description as a comment under `## Original PR Description`
6. Replaces the PR body with the cleaned commit message

The action is **idempotent** — running it multiple times on the same PR won't create duplicate comments.

## Building

Requires Go 1.24+.

```sh
go build -o dependabot-pr-merge-cleanup .
go test ./...
```

## License

MIT
