# ENG-003: Test infrastrcuture

## Status

Proposal

## Context

E2E tests ensure the functional correctness in an e2e environment in order to make sure Dapr works with the user code deployments. The tests will be run before / after PR is merged or by a scheduler.

Dapr E2E tests require the test infrastructure in order to not only test Dapr functionalities, but also show these test results in a consistent way. This document will decide how to bring up the test cluster, run the test, and report the test results.

## Decisions

### Test environments

Although Dapr is designed for multi cloud environments, e2e tests will be run under Kubernetes environments for now. We will support two different options to run e2e tests with local machine and CI on the pre-built Kubernetes cluster.

* **Local machine**. contributors or developers will use [Minikube](https://github.com/kubernetes/minikube) to validate their changes and run new tests before creating Pull Request.

* **Continuous Integration**. E2E tests will be run in the pre-built [Azure Kubernetes Service](https://azure.microsoft.com/en-us/services/kubernetes-service/) before/after PR is merged or by a scheduler. Even if we will use [Azure Kubernetes Service](https://azure.microsoft.com/en-us/services/kubernetes-service/) in our test infrastructure, contributors should run e2e tests in any  RBAC-enabled Kubernetes clusters.

### Bring up test cluster

We will provide the manual instruction or simple script to bring up test infrastructure unlike the other Kubernetes projects using [kubetest](https://github.com/kubernetes/test-infra/tree/master/kubetest). Dapr E2E tests will clean up and revert all configurations in the cluster once the test is done. Without kubetest, we can create e2e tests simpler without the dependency of the 3rd party test frameworks, such as ginkgo, gomega.

### CI/CD and test result report for tests

Many Kubernetes-related projects use [Prow](https://github.com/kubernetes/test-infra/tree/master/prow), and [Testgrid](https://github.com/kubernetes/test-infra/tree/master/testgrid) for Test CI, PR, and test result management. However, we will not use them to run Dapr E2E tests and share the test result since we need to self-host them on Google cloud platform.

Instead, Dapr will use [Azure Pipeline](https://azure.microsoft.com/en-us/services/devops/pipelines/) to run e2e tests and its [test report feature](https://docs.microsoft.com/en-us/azure/devops/pipelines/test/review-continuous-test-results-after-build?view=azure-devops) without self-hosted CI and test report services. Even contributors can get their own azure pipelines accounts **for free** without self-hosting them.

## Consequences

* Dapr E2E tests will run in [Minikube](https://github.com/kubernetes/minikube) with local machine and [Azure Kubernetes Service](https://azure.microsoft.com/en-us/services/kubernetes-service/) with CI, but the tests will run in any RBAC-enabled Kubernetes clusters
* We will provide the manual instruction and scripts to build test Kubernetes cluster
* [Azure Pipeline](https://azure.microsoft.com/en-us/services/devops/pipelines/) will run e2e tests before/after PR is merged or by a scheduler and report test results
