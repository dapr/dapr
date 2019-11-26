# Tests

This contains end-to-end tests, related test framework, and docs for Dapr:

* **platform/** includes test app manager for the specific test platform
* **runner/** includes test runner to simplify e2e tests
* **apps/** includes test apps used for end-to-end tests
* **e2e/** includes e2e test drivers written using go testing

## End-To-End Tests

> Note: We're actively working on supporting diverse platforms and adding pre/post-submit tests integrated with CI.

### Setup Test environment

* Set up your kubernetes environment and install helm by following [this setup doc](https://github.com/dapr/docs/blob/master/getting-started/environment-setup.md#installing-dapr-on-a-kubernetes-cluster).
* Create your DockerHub ID
* Set the environment variables

```bash
export DAPR_REGISTRY=docker.io/your_dockerhub_id
export DAPR_TAG=dev

# Do not set DAPR_TEST_ENV if you do not use minikube
export DAPR_TEST_ENV=minikube

# Set the below environment variables if you want to use the different registry and tag for test apps
# export DAPR_TEST_REGISTRY=docker.io/your_dockerhub_id
# export DARP_TEST_TAG=dev
```

* Install redis and kafka for state, pubsub, and binding building block
```bash
make setup-helm-init
make setup-test-env
```

* Register the default component configurations for testing
```bash
make setup-test-components
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