# Tests

This contains end-to-end tests, related test framework, and docs for Dapr:

* **apps/** includes test apps used for end-to-end tests
* **config/** includes component configurations for the default state/pubsub/bindings
* **docs/** includes documents how write and run e2e test
* **e2e/** includes e2e test drivers written using go testing
* **perf/** includes performance test drivers written using go testing
* **platforms/** includes test app manager for the specific test platform
* **runner/** includes test runner to simplify e2e tests
* **test-infra/** includes tools for test-infra

## End-to-end tests

> Note: We're actively working on supporting diverse platforms and adding more e2e tests. If you are interested in E2E tests, please see [Dapr E2E test](https://github.com/orgs/dapr/projects/9) project.

* [Running Performance tests](./docs/running-perf-tests.md)
* [Running E2E tests](./docs/running-e2e-test.md)
* [Writing E2E tests](./docs/writing-e2e-test.md)
