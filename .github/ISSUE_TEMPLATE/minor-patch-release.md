---
name: Minor Patch Release
about: Minor Patch Release
title: 'Minor Patch Release Checklist'
labels: kind/bug
assignees: ''
---

- [ ] Dapr maintainer(s) communicate out the discovery of issues that require a patch release and the reason why to the Discord Maintainers and Release channels. Patch releases are made for one of categories below:
    - [ ] Security vulnerability
        - <>
    - [ ] Regression that does not have a work around:
        - <>
    - [ ] Broken mainline scenario that has a missing test case:
        - <>
- [ ] Create Tag
    - [ ] <>
- [ ] Performance tests passing
- [ ]  End to End tests passing on Linux and Windows
- [ ]  New test case written to catch future occurrences
- [ ]  Notify users to try RC (Announce on Discord Announcements channel)
    - [ ] <>
- [ ]  Update the longhaul tests to use RC
    - [ ] <>
- [ ]  Write release notes
    - [ ] <>
- [ ]  Review release notes [@dapr/maintainers-dapr]
- [ ]  Create Tag [@dapr/maintainers-dapr]
- [ ]  Backport fixes into master branch [@dapr/maintainers-dapr]
- [ ]  Update the documentation: Latest version & versions in supported releases
- [ ]  Push new tag in installer-bundle repo
- [ ]  Announce the patch release on Discord Announcements channel