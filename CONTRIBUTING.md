# Contribution Guidelines

Thank you for your interest in Dapr!

Contributions come in many forms: submitting issues, writing code, participating in discussions and community calls.

This document provides the guidelines for how to contribute to the Dapr project.

## Governance

The project is governed by Microsoft.

## Issues

This section describes the guidelines for submitting issues

### Issue Types

There are 4 types of issues:

- Issue/Bug: You've found a bug with the code, and want to report it, or create an issue to track the bug.
- Issue/Discussion: You have something on your mind, which requires input form others in a discussion, before it eventually manifests as a proposal.
- Issue/Proposal: Used for items that propose a new idea or functionality. This allows feedback from others before code is written.
- Issue/Question: Use this issue type, if you need help or have a question.

### Before You File

Before you file an issue, make sure you've checked the following:

1. Is it the right repository?
    - The Dapr project is distributed across multiple repositories. Check the list of [repositories](https://github.com/dapr) if you aren't sure which repo is the correct one.
1. Check for existing issues
    - Before you create a new issue, please do a search in [open issues](https://github.com/dapr/dapr/issues) to see if the issue or feature request has already been filed.
    - If you find your issue already exists, make relevant comments and add your [redapr](https://github.com/blog/2119-add-redapr-to-pull-requests-issues-and-comments). Use a redapr:
        - 👍 up-vote
        - 👎 down-vote
1. For bugs
    - Check it's not an environment issue. For example, if running on Kubernetes, make sure prerequisites are in place. (state stores, bindings, etc.)
    - You have as much data as possible. This usually comes in the form of logs and/or stacktrace. If running on Kubernetes or other environment, look at the logs of the Dapr services (runtime, operator, placement service). More details on how to get logs can be found [here](docs/concepts/best-practices/troubleshooting/logs.md).
1. For proposals
    - Many changes to the Dapr runtime may require changes to the API. In that case, the best place to discuss the potential feature is the [Dapr Spec repo](https://github.com/dapr/spec).
    - Other examples could include bindings, state stores or entirely new components.

## Contributing to Dapr

This section describes the guidelines for contributing code / docs to Dapr.

### Pull Requests

All contributions come through pull requests. To submit a proposed change, we recommend following this workflow:

1. Make sure there's an issue (bug or proposal) raised, which sets the expectations for the contribution you are about to make.
1. Fork the relevant repo and create a new branch
1. Create your change
    - Code changes require tests
1. Update relevant documentation for the change
1. Commit and open a PR
1. Wait for the CI process to finish and make sure all checks are green
1. A maintainer of the project will be assigned, and you can expect a review within a few days

#### Use work-in-progress PRs for early feedback

A good way to communicate before investing too much time is to create a "Work-in-progress" PR and share it with your reviewers. The standard way of doing this is to add a "[WIP]" prefix in your PR's title and assign the **do-not-merge** label. This will let people looking at your PR know that it is not well baked yet.

### Use of Third-party code

- All third-party code must be placed in the `vendor/` folder.
- `vendor/` folder is managed by Go modules and stores the source code of third-party Go dependencies. - The `vendor/` folder should not be modified manually.
- Third-party code must include licenses.

A non-exclusive list of code that must be places in `vendor/`:

- Open source, free software, or commercially-licensed code.
- Tools or libraries or protocols that are open source, free software, or commercially licensed.

**Thank You!** - Your contributions to open source, large or small, make projects like this possible. Thank you for taking the time to contribute.

## Code of Conduct

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
