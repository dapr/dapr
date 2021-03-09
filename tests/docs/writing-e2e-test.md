# Write E2E test

Before writing e2e test, make sure that you have [local test environment](./running-e2e-test.md) and can run the e2e tests.

In order to add new e2e test, you need to implement test app and test driver:

* **Test app**: A simple http server listening to 3000 port exposes the test entry endpoints(`/tests/{test}`) to call dapr runtime APIs. Test driver code will deploy to Kubernetes cluster with dapr sidecar.
* **Test driver**: A Go test with `e2e` build tag deploys test app to Kubernetes cluster and runs go tests by calling the test entry endpoints of test app.

## Add test app

A test app is in [/tests/apps/](../apps/) and can be shared by the multiple test drivers. Technically, it can be written in any languages, but Go is recommended to keep the code base consistent and simple. [hellodapr](../apps/hellodapr/) is the good example of test app as a boilerplate code.

### Writing test app

1. Create new test app directory under [/tests/apps/](../apps).
2. Copy `app.go`, `Dockerfile`, and `service.yaml` from [/tests/apps/hellodapr/](../apps/hellodapr/) to test app directory

  - app.go : A simple http server written in Go
  - Dockerfile : Dockerfile for test app
  - service.yaml : Test app deployment yaml for kubectl. This is for development purpose but not used by the actual e2e test.

3. Modify `app.go`
4. Run go app and validate the http endpoints of app.go

### Debug and validation

1. Add your test app name to `E2E_TEST_APPS` variable in [dapr_tests.mk](../dapr_tests.mk)
2. Build test image via make
```bash
# DAPR_TEST_REGISTRY and DAPR_TEST_TAG must be configured
make build-e2e-app-[new app directory name]
make push-e2e-app-[new app directory name]
```
3. Update `service.yaml` properly - change the metadata and registry/imagename
4. Deploy app to your k8s cluster for testing purpose
```bash
kubectl apply -f service.yaml
```
5. Get external ip for the app
```bash
kubectl get svc
```
6. Validate the external endpoint using wget/curl/postman
7. Delete test app
```bash
kubectl delete -f service.yaml
```

## Add test driver

A test driver is a typical Go test with `e2e` build tag under [/tests/e2e/](../e2e/). It can deploy test apps to a test cluster, run the tests, and clean up all test apps and any resources. We provides test runner framework to manage the test app's lifecycle during the test so that you do not need to manage the test apps by yourself. We plan to add more functionalities to test runner later, such as the log collection of test app POD.

> *Note:* Any test app you deploy should have a name unique to your test driver. Multiple test drivers may be run at the same time by the automated E2E test runner, so if you have an app name which is not unique, you may collide with another test app. Also, make sure that any stateful resources (pubsub topics, statestores, secrets, etc.) have names unique to your test driver.

### Writing test app

1. Create new test directory under [/tests/e2e/](../e2e).
1. Create go test file based on the below example:
   - `// +build e2e` must be placed at the first line of test code
   - `TestMain(m *testing.M)` defines the list of test apps and component, which will be used in `TestXxx(t *testing.T)`
   - Define new package for the test
   - Use Go test best practice

```go
// +build e2e

package hellodapr_e2e

import (
    "testing"

    kube "github.com/dapr/dapr/tests/platforms/kubernetes"
    "github.com/dapr/dapr/tests/runner"
    "github.com/stretchr/testify/require"

    ...
)

// Test runner instance
var tr *runner.TestRunner

// Go test main entry - https://golang.org/pkg/testing/#hdr-Main
func TestMain(m *testing.M) {
    testApps := []kube.AppDescription{
        {
            AppName:        "hellodapr",
            DaprEnabled:    true,
            ImageName:      "e2e-hellodapr",
            Replicas:       1,
            IngressEnabled: true,
            MetricsEnabled: true,
        },
    }

    tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)
    os.Exit(tr.Start(m))
}

func TestHelloDapr(t *testing.T) {
    externalURL := tr.Platform.AcquireAppExternalURL("hellodapr")
    require.NotEmpty(t, externalURL, "external URL must not be empty")

    // Call endpoint for "hellodapr" test app
    resp, err := httpGet(externalURL)
    require.NoError(t, err)

    ...
}

func TestStateStore(t *testing.T) {
    ...
}

```

1. Define the test apps in `TestMain()` and create test runner instance
```go
// The array of test apps which will be used for the entire tests
// defined in this test driver file
testApps := []kube.AppDescription{
    {
        AppName:        "hellodapr",     // app name
        DaprEnabled:    true,            // dapr sidecar injection
        ImageName:      "e2e-hellodapr", // docker image name 'e2e-[test app name]'
        Replicas:       1,               // number of replicas
        IngressEnabled: true,            // enable ingress endpoint
        MetricsEnabled: true,            // enable metrics endpoint
    },
}

// Create test runner instance with 'hellodapr' runner id
tr = runner.NewTestRunner("hellodapr", testApps, nil, nil)

// Start the test
os.Exit(tr.Start(m))
```

3. Write the test by implementing `TestXxx(t *testing.T)` methods
   - Acquire app external url to call test app endpoint
   - Send http request to test app endpoint

```go
func TestHelloDapr(t *testing.T) {
    // Acquire app external url
    externalURL := tr.Platform.AcquireAppExternalURL("hellodapr")
    require.NotEmpty(t, externalURL, "external URL must not be empty")

    // Call endpoint for "hellodapr" test app
    resp, err := httpGet(externalURL)
    require.NoError(t, err)

    ...

}
```

### Debug and validation

1. If you use minikube for your dev environment, set `DAPR_TEST_MINIKUBE_IP` environment variable to IP address from `minikube ip`
2. Debug Go test
  * Using [dlv test](https://github.com/go-delve/delve/blob/master/Documentation/usage/dlv_test.md). For example, if you want to debug `TestHelloDapr` test,
    ```bash
    dlv test --build-flags='-tags=e2e' ./tests/e2e/... -- -test.run ^TestHelloDapr$
    ```
  * Using VSCode + Go plugin (recommended)
  [VSCode + Go plugin](https://github.com/Microsoft/vscode-go/wiki/Debugging-Go-code-using-VS-Code) provides the good debugging experience. However, it does not provide a way to add build tag to build option so you need to remove `// +build e2e` build constraints in your go test and [helpers.go](https://github.com/dapr/dapr/blob/53bf10569fe9a9a5f484c5c9cf5760881db9a3e4/tests/e2e/helpers.go#L1) temporarily during debugging.

3. Run all e2e tests
```bash
make test-e2e-all
```
