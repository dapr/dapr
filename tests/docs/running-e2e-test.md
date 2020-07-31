# Running End-To-End Tests

E2E tests are designed for verifying the functional correctness by replicating end-user behavior from app deployment. This describes how to run e2e tests in local dev environment and run them through CI

  - [Run E2E tests in local dev environment](#run-e2e-tests-in-local-dev-environment)
  - [Run E2E tests through GitHub Actions](#run-e2e-tests-through-github-actions)

## Run E2E tests in local dev environment

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
    export TARGET_OS=linux
    export TARGET_ARCH=amd64

    # Do not set DAPR_TEST_ENV if you do not use minikube
    export DAPR_TEST_ENV=minikube

    # Set the below environment variables if you want to use the different registry and tag for test apps
    # export DAPR_TEST_REGISTRY=docker.io/your_dockerhub_id
    # export DARP_TEST_TAG=dev
    ```
* Install redis and kafka for state, pubsub, and binding building block
    ```bash
    make setup-helm-init
    make setup-test-env-redis

    # This may take a few minutes.  You can skip kafka install if you do not use bindings for your tests.
    make setup-test-env-kafka
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

### Register the default component configurations for testing

```bash
make setup-test-components
```

### Build and push test apps to docker hub

Build docker images from apps and push the images to test docker hub

```bash
# build e2e apps docker image under apps/
make build-e2e-app-all

# push e2e apps docker image to docker hub
make push-e2e-app-all
```

### Run end-to-end test

Run end-to-end tests

```bash
# start e2e test
make test-e2e-all
```

## Run E2E tests through GitHub Actions

To keep the build infrastructure simple, Dapr uses [dapr-test GitHub Actions Workflow](https://github.com/dapr/dapr/actions?query=workflow%3Adapr-test) to run e2e tests using one of [AKS clusters](https://github.com/dapr/dapr/blob/4cd61680a3129f729deae24a51da241d0701376c/tests/test-infra/find_cluster.sh#L12-L17).

Once a contributor creates pull request, maintainer can start E2E tests by adding `/ok-to-test` comment to Pull Request.
