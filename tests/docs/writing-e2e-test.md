# Write E2E test

Before writing e2e test, make sure that you have test environment and can run the e2e tests in your dev environment.

In order to add new e2e tests, you need to write two codes:

* **Test app**: A simple http server listening to 3000 port exposes the test entry endpoint to call dapr runtime APIs. Test driver code will deploy to test Kubernetes cluster with dapr sidecar.
* **Test driver**: A Go test with `e2e` build tag deploys test app to test Kubernetes cluster and runs go tests by calling the test entry endpoints of test app.

## Add test app

### Debug and validation

## Add test driver

### Debug and validation

