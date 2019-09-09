# ENG-002: Actions Release

## Status

Proposal

## Context

This record descibes how to safely release new actions binaries and the corresponding configurations without any blockers to customers and actions engineering team. It covers these release processes:

* Actions Binaries for standalone users
* Actions Docker Images
* GitHub Release

## Decisions

### Definitions of Actions release

#### Integration build release

Integration build refers to the build from `master` branch once we merge PullRequest to master branch. This build will be used for development purposes by Actions engineering team and must not be released to Actions customers and impact their environments.

#### Official build release

Official build release refers to the builds satisfying the following requirements:

1. Branch name is `release-*` without `-` version tag
   - `release-1.0.0` is allowed, but not `release-1.0.0-alpha`
2. Release note `./docs/release_notes/v{semver}.md` is given
3. Git version tag represents the official release version and must be matched with the version in branch name
   - If git version tag is `v1.0.0-alpha`, then branch name must be `release-1.0.0`

If we want to validate the current release build in `release-*` branch branch before the official release, we can build Actions without release note, but it will not released to `latest` version.

##### Release process

1. Create release note (`v{semver.md}`) under `./docs/release_notes/`
2. Release driver will create release branch from master and add tag with the version.

```bash
# If we clone from actionscore/action branch directly
$ git checkout master
$ git checkout -b release-1.0.0
```

3. Push release branch to remote origin

```bash
$ git push origin release-1.0.0
```

4. CI will be triggered automatically

5. Find the bugs and fix them

6. Create git version tag and push

```
$ git tag 1.0.0-alpha
$ git push origin 1.0.0-alpha
```

### Release Actions binaries for standalone users

* Integration build release
  - Upload zipped binaries to `release/edge` in actionsreleases blob storage.

* Official build release
  - Upload zipped binaries to `release/v{semver}`
  - Update all files in `release/latest` in actionsrelease blob storage (only if release note is given)

### Release Actions Docker Images

* Integration build release
  - Linux AMD64 : Publish docker image with `edge` tag. e.g. `actionscore/actions:edge`
  - Linux ARM : Publish docker image with `edge-arm` tag. e.g. `actionscore/actions:edge-arm`

* Official build release
  - Linux AMD64: publish docker image with `v{semver}` tag and `latest` tag
  - Linux ARM: publish docker image with `v{semver}-arm` tag and `latest-arm` tag

> `latest-{arch}` tag will be added only if release note is given.
> See [ENG-001-tagging](./ENG-001-tagging.md) for more information.

### GitHub Release

Only official build release will be released to [GitHub Release](https://github.com/actionscore/actions/releases)

## Consequences

* Customers and Actions Engineering Team can use the specific version of Actions binaries, Docker images, and helm manifest selectively
* Actions Engineering Team can release the official build without breaking customer scenario

## Examples

* Integration build: from master branch
  1. Publish actions binaries to actionsreleases blob storage under `release/edge/`
  2. Publish linux amd64 docker image with `edge` build tag
  3. Publish linux arm docker image with `edge-arm` build tag
  4. Publish helm manifest to actionsreleases blob storage under `manifest/edge`

* Official build without git version tag: from release-1.0.0 and v1.0.0-alpha tag
  1. Publish actions binaries to actionsreleases blob storage under `release/v1.0.0-alpha/`
  2. Publish linux amd64 docker image with `v1.0.0-alpha` build tag
  3. Publish linux arm docker image with `v1.0.0-alpha-arm` build tag
  4. Publish helm manifest to actionsreleases blob storage under `minifest/v1.0.0-alpha`

* Official build with git version tag: from release-1.0.0 and v1.0.0-alpha tag
  1. Publish actions binaries to `release/v1.0.0-alpha/` and `release/latest/` in actionsreleases blob storage
  2. Publish linux amd64 docker image with `v1.0.0-alpha` and `latest` build tags
  3. Publish linux arm docker image with `v1.0.0-alpha-arm` and `latest-arm` build tags
  4. Publish helm manifest to `minifest/v1.0.0-alpha` and `minifest/latest` in actionsreleases blob storage
  5. Publish actions binaries to GitHub Release
