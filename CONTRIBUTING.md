# Contributing to Actions

Thank you for your interest in Actions! 

Contributions come in many forms, not just writing code. active participation in discussions and community calls are encouraged.
This document provides a high-level overview of how you can get involved.

## Questions and Feedback

Questions, comments and feedback are welcome! we recognize the value in a vibrant and open community.
The best way to ask questions and give feedback is via the [issues](https://github.com/actionscore/actions/issues) section.
We'll make sure to answer your question or address your feedback in a timely manner.

## Reporting Issues

Have you identified a reproducible problem in Actions? Have a feature request? We want to hear about it! Here's how you can make reporting your issue as effective as possible.

### Identify Where to Report

The Actions project is distributed across multiple repositories. Try to file the issue against the correct repository. Check the list of [repositories](https://github.com/actionscore) if you aren't sure which repo is correct.

Before filing an issue, first make sure that:

* Its not an environment issue. if running on Kubernetes, make sure all prerequisites are in place. (state stores, message buses, etc.)
* You have as much data as possible. this usually comes in the form of logs and/or stacktrace. if running on Kubernetes or any other containzerized environment, look at the logs of the appropriate Actions service (runtime, operator or placement service).

### Look For an Existing Issue

Before you create a new issue, please do a search in [open issues](https://github.com/actionscore/actions/issues) to see if the issue or feature request has already been filed.

If you find your issue already exists, make relevant comments and add your [reaction](https://github.com/blog/2119-add-reactions-to-pull-requests-issues-and-comments). Use a reaction in place of a "+1" comment:

* 👍 - upvote
* 👎 - downvote

If you cannot find an existing issue that describes your bug or feature, create a new issue using the guidelines below.

### Guidelines for Bug Reports and Feature Requests

File a single issue per problem and feature request. Do not enumerate multiple bugs or feature requests in the same issue.

Do not add your issue as a comment to an existing issue unless it's for the identical input. Many issues look similar, but have different causes.

Many changes to the Actions runtime may require changes to the API. in that case, the best place to discuss the potential feature is the [Actions Spec repo](https://github.com/actionscore/spec).

The more information you can provide, the more likely someone will be successful at reproducing the issue and finding a fix.

Please include the following with each issue:

* Version of Actions (e.g. version number or git commit)

* Environment (Kubernetes, standalone, etc.)  

* List of dependencies (Redis, CosmodDB, etc.)

* Reproducible steps (1... 2... 3...) that cause the issue

* What you expected to see, versus what you actually saw

* A code snippet that demonstrates the issue or a link to a code repository the developers can easily pull down to recreate the issue locally

* Logs obtained from the relevant Actions code

### Final Checklist

Please remember to do the following:

* [ ] Search the issue repository to ensure your report is a new issue

* [ ] Make sure its not a setup or environment issue

* [ ] Simplify your code around the issue to better isolate the problem

### Follow Your Issue

Once submitted, your report will go through an issue tracking workflow and be labeled appropriately. Be sure to understand what will happen next, so you know what to expect, and how to continue to assist throughout the process.

## Contributing to Actions

So you're working on a bug fix or a new feature? that's great! please familiarize yourself with these guidelines:

### Pull Requests

To submit a proposed change:

* Fork the relevant repo

* Create a new branch

* Hack

* Add tests! this is really important. We will not approve PRs that do not contain tests

* Modify documentation if needed. Clarity is of high importance

* Wait for the CI process to finish. remember: Always Green!

Always make sure there's an issue backing up the PR.

#### Use work-in-progress PRs for early feedback
A good way to communicate before investing too much time is to create a "Work-in-progress" PR and share it with your reviewers. The standard way of doing this is to add a "WIP:" prefix in your PR's title and assign the "do-not-merge" label. This will let people looking at your PR know that it is not well baked yet.

### Third-party code
* All third-party code must be placed in the `vendor/` folders.
* `vendor/` folder is managed by Go modules and stores the source code of third-party Go dependencies. `vendor/` folder should not be modified manually.
* Third-party code must include licenses.

A non-exclusive list of code that must be places in `vendor/`:

* Open source, free software, or commercially-licensed code.
* Tools or libraries or protocols that are open source, free software, or commercially licensed.

# Thank You!

Your contributions to open source, large or small, make projects like this possible. Thank you for taking the time to contribute.
