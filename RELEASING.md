# Releasing

This document covers how to publish a new version of the
`dependabot-pr-merge-cleanup` GitHub Action.

## Versioning

We use [semantic versioning](https://semver.org/). Each release produces two
git tags:

- `image-vX.Y.Z` — points at the source commit that the Docker image was
  built from. Created automatically by the release workflow.
- `vX.Y.Z` — points at the merge commit of the `action.yml` update PR. This
  is what consumers should pin to (e.g.,
  `uses: jmhodges/dependabot-pr-merge-cleanup@v1.0.1`). Created by
  publishing a GitHub Release after the PR merges.

## Prerequisites

The release workflow needs a repository secret named
`CREATE_PULL_REQUEST_TOKEN`: a personal access token (classic with `repo`
scope, or a fine-grained token with `contents: write` and
`pull-requests: write` on this repository). It's used by
[`peter-evans/create-pull-request`](https://github.com/peter-evans/create-pull-request)
to open the `action.yml` update PR. The default `GITHUB_TOKEN` can't be used
here because PRs it opens don't trigger other workflows (such as required
status checks).

The workflow itself declares these permissions in `build-for-release.yml`:

- `contents: write` — to push the `image-vX.Y.Z` tag
- `packages: write` — to push the Docker image to GHCR
- `pull-requests: write` — for the `create-pull-request` action

## Steps

1. **Pick a version.** Use semver based on the changes since the last
   release. See the [tags page](../../tags) for the previous version.

2. **Run the release workflow.**
   - Open the [Tag and Publish Image workflow](../../actions/workflows/build-for-release.yml).
   - Click **Run workflow**.
   - Enter the new version, e.g., `1.2.3` (a leading `v` is also accepted and
     will be normalized).
   - Click **Run workflow**.

   The workflow will:
   - Validate the version against the semver pattern.
   - Create and push the `image-vX.Y.Z` git tag on the commit it ran from.
   - Build the Docker image and push it to
     `ghcr.io/jmhodges/dependabot-pr-merge-cleanup:vX.Y.Z`.
   - Open a PR titled `update action.yml image to vX.Y.Z` that bumps the
     `image:` reference in `action.yml`.

3. **Review and merge the PR.** Confirm the `action.yml` diff only changes
   the image tag to the new version, then merge it.

4. **Publish a GitHub Release.** Once the PR is merged, publishing the
   release through GitHub's UI creates the `vX.Y.Z` tag at the same time —
   no separate `git tag` step is needed.

   - Go to [Releases](../../releases) and click **Draft a new release**.
   - Under **Choose a tag**, type `vX.Y.Z` and select **Create new tag:
     `vX.Y.Z` on publish**.
   - Leave the target as `main` so the tag lands on the merge commit of the
     `action.yml` update PR.
   - Set the title to `vX.Y.Z`.
   - Click **Generate release notes** to auto-populate the changelog from
     PRs merged since the previous release; edit as needed.
   - Click **Publish release**. This creates the `vX.Y.Z` tag and publishes
     the release in one step.

## Rollback

If a release turns out to be broken, don't delete or rewrite the published
tags or image — consumers may already be pinned to them. Instead:

1. Revert the `action.yml` update PR (or open a new PR setting `action.yml`
   back to the previous good image).
2. Cut a new patch release following the steps above. The bad image tag can
   stay on GHCR; nothing will reference it once `action.yml` is updated.
