---
name: Endgame
about: Endgame checklist for Dapr releases
title: 'vX.Y Endgame'
labels: area/release-eng
assignees: ''
---

# vX.Y Endgame

Read about the [Dapr release lifecycle](https://github.com/dapr/community/blob/master/release-process.md)

## Release team

Release lead: TBD  
Release lead shadow(s): TBD  
Perf test lead: TBD  
Longhaul test: TBD  
Build manager: TBD

## Milestone dates

- [ ] Feature definition starts: TBD
- [ ] Feature definition ends: TBD
- [ ] Feature freeze: TBD
- [ ] Code freeze (Endgame starts ~1-2 weeks stabilization): TBD
- [ ] RC Release day: TBD
- [ ] Release day: TBD
- [ ] Announcement day: TBD

Remaining P0s:

- [ ] TBD

---

## Release Tasks

### Code freeze

- [ ] Code freeze at: TBD
- [ ] All P0 issues are resolved PRIOR to code freeze

### Components Contrib

- [ ] Check all new components are registered in runtime
- [ ] Check all new components metadata files are properly updated
- [ ] Certifications/conformance passing for components
- [ ] Update certification tests in components-contrib release branch (prior to merge back into master) to use the RC (and Go SDK RC/release if possible)
- [ ] Verify infrastructure resources
- [ ] Cut RC and update dapr/dapr to use it

### dapr/dapr (runtime)

- [ ] E2E tests passing
- [ ] Integration tests flakiness addressed
- [ ] Performance tests are passing
- [ ] Longhaul tests are passing
- [ ] Version skew tests are passing
- [ ] Cut RC and update components-contrib to use it

### dapr/cli

- [ ] Tests are passing
- [ ] Cut RC

---

### Notifications

- [ ] Notify users about dapr RCs in https://discord.com/channels/778680217417809931/1067547446768570458

--- 

## Validation

### dapr/helm-charts

- [ ] Validate the upgrade path from the previous release -- CRDs updated, no deletion of components/configs etc.

### dapr/installer-bundle

- [ ] Cut RC
- [ ] Validate RC

### SDKs

- [ ] Update protos and cut RCs for SDKs:
  - [ ] .NET @approvers-dotnet-sdk @maintainers-dotnet-sdk
  - [ ] Java @approvers-java-sdk @maintainers-java-sdk
  - [ ] Go @approvers-go-sdk @maintainers-go-sdk
  - [ ] Python @approvers-python-sdk @maintainers-python-sdk
  - [ ] JS (as applicable) @approvers-js-sdk @maintainers-js-sdk
  - [ ] Rust (as applicable) @approvers-rust-sdk @maintainers-rust-sdk
  - [ ] C++ (as applicable) @approvers-cpp-sdk @maintainers-cpp-sdk

### Quickstarts

- [ ] Update quickstarts/tutorials automated validation - covers Linux/K8s/Darwin/MacOS
- [ ] Point quickstarts to RC of SDKs/dev pkgs
  - [ ] .NET
  - [ ] Java
  - [ ] Go
  - [ ] Python
  - [ ] JS (as applicable)
  - [ ] Rust (as applicable)
  - [ ] C++ (as applicable)
- [ ] Validate quickstarts on Windows
- [ ] Validate quickstarts on Linux ARM64
- [ ] Validate quickstarts on self-hosted
- [ ] Validate quickstarts on K8s

### Longhaul / Metrics

- [ ] Update longhaul tests with RC of dapr runtime
- [ ] Validate longhaul metrics

### Milestones and Release Notes

- [ ] Confirm each repo has milestones set for relevant issues/PRs
  - [ ] .NET @maintainers-dotnet-sdk
  - [ ] Java @maintainers-java-sdk
  - [ ] Go @maintainers-go-sdk
  - [ ] Python @maintainers-python-sdk
  - [ ] JS (as applicable) @maintainers-js-sdk
  - [ ] Rust (as applicable) @maintainers-rust-sdk
  - [ ] C++ (as applicable) @maintainers-cpp-sdk
- [ ] Generate release notes
- [ ] Validate FOSSA on each repo
  - [ ] .NET @maintainers-dotnet-sdk
  - [ ] Java @maintainers-java-sdk
  - [ ] Go @maintainers-go-sdk
  - [ ] Python @maintainers-python-sdk
  - [ ] JS (as applicable) @maintainers-js-sdk
  - [ ] Rust (as applicable) @maintainers-rust-sdk
  - [ ] C++ (as applicable) @maintainers-cpp-sdk
- [ ] Edit and complete release notes HackMD
- [ ] PR release notes to dapr/dapr
- [ ] Verify release documentation

---

### User feedback

- [ ] End user verification of RCs before official release

---

## ETA for above: TBD

## RELEASE DAY: TBD

- [ ] Validate breaking changes section in release notes
- [ ] Merge release notes into release branch
- [ ] Cut tags and publish releases for:
  - [ ] components-contrib
  - [ ] dapr runtime (dapr/dapr)
  - [ ] CLI
  - [ ] Installer bundle
  - [ ] SDKs
    - [ ] .NET
    - [ ] Java
    - [ ] Go
    - [ ] Python
    - [ ] JS (as applicable)
    - [ ] Rust (as applicable)
    - [ ] C++ (as applicable)
  - [ ] Quickstarts: create release branch, update image tags, create tag
- [ ] Validate DevContainers in key repos (runtime, components-contrib, CLI)
- [ ] Update certification tests in components-contrib release branch (prior to merge back into master) to use latest runtime and Go SDK release

---

## ANNOUNCEMENT DAY: TBD

- [ ] Update supported versions table in docs
- [ ] Post release announcement in appropriate channels (X, blog, Discord)
- [ ] Update E2E tests in CLI to latest runtime version
- [ ] Merge (NO SQUASH/AUTO MERGE) release-vX.Y branch back to master for required repos (runtime, components-contrib, CLI)
- [ ] Celebrate

---

## POST RELEASE DAY

- [ ] Update Dapr K8s Operator on [OperatorHub](https://operatorhub.io/operator/dapr-kubernetes-operator)
- [ ] Update Dapr [Kratix Marketplace](https://github.com/syntasso/kratix-marketplace/tree/main/dapr)
- [ ] Update Dapr [Test Containers Module](https://github.com/diagridio/testcontainers-dapr)
- [ ] Update [Dapr Shared](https://github.com/dapr/dapr-shared)

Updated quickstarts to use released SDKs
- [x] Go
- [x] Java
- [x] JS
- [x] .NET
- [x] Rust
- [x] Python

## Repo milestones

- [ ] Dapr - https://github.com/dapr/dapr/milestone/tbd
- [ ] CLI - https://github.com/dapr/cli/milestone/tbd
- [ ] Docs - https://github.com/dapr/docs/milestone/tbd
- [ ] Components-contrib - https://github.com/dapr/components-contrib/milestone/tbd
- [ ] Java-SDK - https://github.com/dapr/java-sdk/milestone/tbd
- [ ] Python-SDK - https://github.com/dapr/python-sdk/milestone/tbd
- [ ] Dotnet-SDK - https://github.com/dapr/dotnet-sdk/milestone/tbd
- [ ] Js-SDK - https://github.com/dapr/js-sdk/milestone/tbd
- [ ] Go-SDK - https://github.com/dapr/go-sdk/milestone/tbd
- [ ] Rust-SDK - https://github.com/dapr/rust-sdk/milestone/tbd
- [ ] Dashboard - https://github.com/dapr/dashboard/milestone/tbd
- [ ] Quickstarts - https://github.com/dapr/quickstarts/milestone/tbd

## Grafana Dashboard - Longhauls

https://dapr.grafana.net/public-dashboards/86d748b233804e74a16d8243b4b64e18


