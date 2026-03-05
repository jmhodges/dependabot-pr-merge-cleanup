# Dependabot PR Cleanup

A GitHub Action that tidies up Dependabot pull request summaries, especially for
use with GitHub merge queues.

## Why?

When using a GitHub [merge
queue](https://docs.github.com/en/repositories/configuring-branches-and-rulesets/configuring-pull-request-merges/managing-a-merge-queue),
GitHub includes the PR description in the merge commit. Meanwhile, Dependabot PR
descriptions are long and full of changelogs, release notes, and compatibility
scores. Including all of that in the commit history makes it hard to read.

This action, instead, makes the smaller (but still useful) git commit summary
from dependabot the PR description to avoid that problem.

## What this does

This action is to be run on dependabot PRs:

1. Saves the original Dependabot PR description as a comment (so nothing is lost)
2. Replaces the PR body with the latest commit message

So, we get a smaller commit that still links out to useful info about the
version bump.

(This action also checks if that work has already been done, and if so, it
doesn't do it again. Idempotency!)

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

| Input     | Description                                    | Required | Default                                   |
| --------- | ---------------------------------------------- | -------- | ----------------------------------------- |
| `token`   | GitHub token with `pull_requests:write`        | Yes      | `${{ github.token }}`                     |
| `repo`    | Repository in `owner/name` format              | No       | `${{ github.repository }}`                |
| `pr`      | Pull request number                            | No       | `${{ github.event.pull_request.number }}` |
| `dry-run` | Print what would happen without making changes | No       | `false`                                   |

### Additional useful info

#### Running on existing dependabot PRs

And you can run this on dependabot PRs made before you configure this action in
your repo by adding this to your repo's action's `on` block:

```yaml
workflow_dispatch:
  inputs:
    pr_number:
      description: "PR number to clean up"
      required: true
      type: number
```

#### Dry run

Preview what the action would do without modifying anything:

```yaml
- uses: jmhodges/dependabot-pr-merge-cleanup@main
  with:
    dry-run: "true"
```

## Building

Requires Go 1.24+.

```sh
go build -o dependabot-pr-merge-cleanup .
go test ./...
```

## License

MIT
