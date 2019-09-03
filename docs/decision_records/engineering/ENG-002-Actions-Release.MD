# ENG-002: Actions Release

## Status
Proposal

## Context

This record descibes how to safely release new actions binaries and the corresponding configurations without any blockers to customers and actions engineering team. It covers these release processes:

* Actions Binaries for standalone users
* Actions Docker Images
* Helm chart manifest

## Decisions

### Definitions of Actions release

* Integration build release

Integration build refers to the build from `master` branch once we merge the pr to master. This build will be used for development purposes by Actions engineering team and must not be released to Actions customers and impact their environments.

* Official build release

Official build release refers to the build from `release-*` branch with the following requirements:

* Branch name is `release-*` without `-` version tag
  - `release-1.0.0` is allowed, but not `release-1.0.0-alpha`
* Git version tag must be matched with the version in branch name
  - If git version tag is `v1.0.0-alpha`, then branch name must be `release-1.0.0`
* Release note under ./docs/release_notes/v{semver}.md

Unless the build satisfies the requirements, the build could be succeeded and tagged with the specific version, but will not be released to the customer with `latest` tag. For example, given the build from `release-1.0.0` branch and `1.0.0-alpha` tag without `./docs/release_notes/v1.0.0-alpha.md` file, the build will be succeeded and tagged with `v1.0.0-alpha` image but not be released to the latest build.

### Release Actions binaries for standalone users



### Release Actions Docker Images

### Release new Helm chart manifest

## Consequences


## Examples

