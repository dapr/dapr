# ENG-002: Actions Release

## Status

Proposal

## Context

This record descibes how to safely release new actions binaries and the corresponding configurations without any blockers to customers and actions engineering team. It covers these release processes:

* Actions Binaries for standalone users
* Actions Docker Images
* Helm chart manifest
* GitHub Release

## Decisions

### Definitions of Actions release

* Integration build release

Integration build refers to the build from `master` branch once we merge PullRequest to master branch. This build will be used for development purposes by Actions engineering team and must not be released to Actions customers and impact their environments.

* Official build release

Official build release refers to the builds satisfying the following requirements:

1. Branch name is `release-*` without `-` version tag
   - `release-1.0.0` is allowed, but not `release-1.0.0-alpha`
2. Git version tag represents the official release version and must be matched with the version in branch name
   - If git version tag is `v1.0.0-alpha`, then branch name must be `release-1.0.0`
3. Release note `./docs/release_notes/v{semver}.md` is given

If we want to validate the current release build in `release-*` branch branch before the official release, we can build Actions without release note, but it will not released to `latest` version.

### Release Actions binaries for standalone users

* Integration build release
  - Upload zipped binaries to `release/edge` in actionsreleases blob storage.

* Official build release
  - With release note, upload zipped binaries to `release/[release_version]` and update all files in `release/latest` in actionsrelease blob storage
  - Without release note, upload zipped binaries to `release/[release_version]`, but will not update the binaries in `release/latest`.

### Release Actions Docker Images

* Integration build release
  - Publish new docker image with `edge` tag. e.g. `actionscore/actions:edge`

* Official build release
  - With release note, publish docker image with `release version` tag and `latest` tag
  - Without release note, publish the docker image with only `release version` tag; See [ENG-001-tagging](./ENG-001-tagging.md) for more information.

### Release new Helm chart manifest

* Integration build release
  - Modify the helm template to point the actions docker image to `actionscore/actions:edge` and generate manifest
  - Upload manifest to `manifest/edge` in actionsreleases blob storage

* Official build release
  - With release note, modify actions image location to `actionscore/actions:v[release_version]` in helm manifest and upload the manifest under `manifest/v[release_version]` and `manifest/latest` in actionsreleases blob storage
  - Without release note, upload the manifest to `manifest/v[release_version]` in actionsreleases blob storage

### GitHub Release

Only official build release will be released to [GitHub Release](https://github.com/actionscore/actions/releases)

## Consequences

* Customers and Actions Engineering Team can use the specific version of Actions binaries, Docker images, and helm manifest selectively
* Actions Engineering Team can release the official build without breaking customer scenario

## Examples

* Integration build: from master branch
  1. Publish actions binaries to actionsreleases blob storage under `release/edge/`
  2. Publish docker image with `edge` build tag
  3. Publish helm manifest to actionsreleases blob storage under `manifest/edge`

* Official build without release note: from release-1.0.0 and v1.0.0-alpha tag
  1. Publish actions binaries to actionsreleases blob storage under `release/v1.0.0-alpha/`
  2. Publish docker image with `v1.0.0-alpha` build tag
  3. Publish helm manifest to actionsreleases blob storage under `minifest/v1.0.0-alpha`

* Official build with release note: from release-1.0.0 and v1.0.0-alpha tag
  1. Publish actions binaries to `release/v1.0.0-alpha/` and `release/latest/` in actionsreleases blob storage
  2. Publish docker image with `v1.0.0-alpha` and `latest` build tags
  3. Publish helm manifest to `minifest/v1.0.0-alpha` and `minifest/latest` in actionsreleases blob storage
  4. Publish actions binaries to GitHub Release
