# ENG-002: Dapr Release

## Status

Proposal

## Context

This record descibes how to safely release new dapr binaries and the corresponding configurations without any blockers to users.

## Decisions

### Integration build release

Integration build refers to the build from `master` branch once we merge PullRequest to master branch. This build will be used for development purposes and must not be released to users and impact their environments.

### Official build release

#### Pre-release build

Pre-release build will be built from `release-<major>.<minor>` branch and versioned by git version tag suffix e.g. `-alpha.0`, `-alpha.1`, etc. This build is not released to users who use the latest stable version.

**Pre-release process**
1. Create branch `release-<major>.<minor>` from master and push the branch. e.g. `release-0.1`
2. Add pre-release version tag(with suffix -alpha.0 e.g. v0.1.0-alpha.0) and push the tag
```
$ git tag "v0.1.0-alpha.0" -m "v0.1.0-alpha.0"
$ git push --tags
```
3. CI creates new build and push the images with only version tag
4. Test and validate the functionalities with the specific version
5. If there are regressions and bugs, fix them in release-* branch and merge back to master
6. Create new pre-release version tag(with suffix -alpha.1, -alpha.2, etc)
7. Repeat from 4 to 6 until all bugs are fixed


#### Release the stable version to users

Once all bugs are fixed, we will create the release note under [./docs/release_notes](https://github.com/dapr/dapr/tree/master/docs/release_notes) and run CI release manually in order to deliver the stable version to users.

### Release Patch version

We will work on the existing `release-<major>.<minor>` branch to release patch version. Once all bugs are fixed, we will add new patch version tag, such as `v0.1.1-alpha.0`, and then release the build manually.

## Consequences

* Keep master branch in a working state
* Deliver the stable version to user safely
