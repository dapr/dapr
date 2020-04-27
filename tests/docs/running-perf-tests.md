# Running Performance Tests

Performance tests are designed to let you evaluate the latency and processing times for Dapr in your environment for a given hardware. The following describes how to run performance tests in a local dev environment and run them through CI:

  - [Run Performance tests in local dev environment](#run-perf-tests-in-local-dev-environment)
  - [Run Performance tests through GitHub Actions](#run-perf-tests-through-github-actions)

## Run Performance tests in local dev environment

### Prerequisites

* Set up [Dapr development environment](https://github.com/dapr/dapr/blob/master/docs/development/setup-dapr-development-env.md)
  - [Install the latest Helm v3](https://github.com/dapr/docs/blob/master/getting-started/environment-setup.md#using-helm-advanced).
* Create your DockerHub ID
* Create dapr-tests namespace
    ```bash
    kubectl create namespace dapr-tests
    ```
* Set the environment variables
    ```bash
    export DAPR_REGISTRY=docker.io/your_dockerhub_id
    export DAPR_TAG=dev
    export DAPR_NAMESPACE=dapr-tests

    # Do not set DAPR_TEST_ENV if you do not use minikube
    export DAPR_TEST_ENV=minikube
    
    # Set the below environment variables if you want to use the different registry and tag for test apps
    # export DAPR_TEST_REGISTRY=docker.io/your_dockerhub_id
    # export DARP_TEST_TAG=dev

    # Set the below environment variables to configure test specific settings.
    # DAPR_PERF_QPS sets the desired number of requests per second. Default is 1.
    # DAPR_PERF_CONNECTIONS sets the number of client connections used to send requests to Dapr. Default is 1.
    # DAPR_TEST_DURATION sets the duration of the test. Default is "1m".
    # DAPR_PAYLOAD_SIZE_KB sets a payload size in kb to test with. default is 0.
    export DAPR_PERF_QPS
    export DAPR_PERF_CONNECTIONS
    export DAPR_TEST_DURATION
    export DAPR_PAYLOAD_SIZE_KB
    ```

### Deploy your dapr runtime change

Run the below commands to build and deploy dapr from your local disk

```bash
# Build Linux binaries
make build-linux

# Build Docker image with Linux binaries
make docker-build

# Push docker image to your dockerhub registry
make docker-push

# Deploy Dapr runtime to your cluster
make docker-deploy-k8s
```

### Optional: Apply this configuration to disable mTLS

```bash
make setup-test-config
```

### Build and push test apps to docker hub

Build docker images from apps and push the images to test docker hub

```bash
# build perf apps docker image under apps/
make build-perf-app-all

# push perf apps docker image to docker hub
make push-perf-app-all
```

### Run performance tests

```bash
# start perf tests
make test-perf-all
```
