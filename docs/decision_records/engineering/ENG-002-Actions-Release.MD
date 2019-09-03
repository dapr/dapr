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

Official build release refers to the build from `release-*` branch with the following requirements:

1. Branch name is `release-*` without `-` version tag
   - `release-1.0.0` is allowed, but not `release-1.0.0-alpha`
2. Git version tag must be matched with the version in branch name
   - If git version tag is `v1.0.0-alpha`, then branch name must be `release-1.0.0`
3. Release note under ./docs/release_notes/v{semver}.md

Unless the build satisfies all requirements, the build could be succeeded and tagged with the specific version, but will not be released to the customer with `latest` tag. For example, given the build from `release-1.0.0` branch and `v1.0.0-alpha` tag without `./docs/release_notes/v1.0.0-alpha.md` file, the build will be succeeded and tagged with `v1.0.0-alpha` image but not be released to the latest build.

### Release Actions binaries for standalone users

* Integration build
  - Upload zipped binaries to `release/edge` in actionsreleases blob storage.

* Official build release
  - If build satisfies all requirements, upload zipped binaries to `release/[release_version]` and update all files in `release/latest` in actionsrelease blob storage
  - If build satisfies the requirement 1 and 2 without release note, upload zipped binaries to `release/[release_version]`, but will not update the binaries in `release/latest`. This practice will prevent us from accidently releasing unstable changes to the customers.

### Release Actions Docker Images

* Integration build
  - Publish new docker image with `edge` tag. e.g. `actionscore/actions:edge`

* Official build release
  - If build satisfies all requirements, publish docker image with version tag and latest tag
  - If build satisfies the requirement 1 and 2 without release note, publish the docker image with only `release version` tag without `latest` tag; See [ENG-001-tagging](./ENG-001-tagging.md) for more information. This practice will prevent us from accidently releasing unstable changes to the customers.

### Release new Helm chart manifest

* Integration build
  - Modify the helm template to point the actions docker image to `actionscore/actions:edge` and generate manifest
  - Upload manifest to `manifest/edge` in actionsreleases blob storage

* Official build release
  - If build satisfies all requirements
    - Modify actions image location to `actionscore/actions:v[release_version]` in helm manifest and upload the manifest to actionsreleases blob storage under `manifest/v[release_version]`
  - If build satisfies the requirement 1 and 2 without release note, publish the docker image with only `release version` tag without `latest` tag; See [ENG-001-tagging](./ENG-001-tagging.md) for more information. This practice will prevent us from accidently releasing unstable changes to the customers.

## Consequences


## Examples

