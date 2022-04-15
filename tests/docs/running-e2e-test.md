# Running End-To-End Tests

E2E tests are designed for verifying the functional correctness by replicating end-user behavior from app deployment. This describes how to run e2e tests in local dev environment and run them through CI

- [Run E2E tests in local dev environment](#run-e2e-tests-in-local-dev-environment)
- [Run E2E tests through GitHub Actions](#run-e2e-tests-through-github-actions)

## Run E2E tests in local dev environment

### Prerequisites

1. Set up [Dapr development environment](https://github.com/dapr/dapr/blob/master/docs/development/setup-dapr-development-env.md)
2. [Install the latest Helm v3](https://helm.sh/docs/intro/install/)
3. Get a Docker container registry:
   - If using Docker Hub, create your Docker Hub ID
   - Other options include Azure Container Registry, GitHub Container Registry, etc
4. Set the environment variables

    ```bash
    # If using Docker Hub:
    export DAPR_REGISTRY=docker.io/your_dockerhub_id
    # You can use other registries too, for example:
    export DAPR_REGISTRY=myregistry.azurecr.io
    export DAPR_TAG=dev
    export DAPR_NAMESPACE=dapr-tests
    export DAPR_MTLS_ENABLED=true

    # If you want to run tests against Windows or arm kubernetes clusters, uncomment and set these
    # export TARGET_OS=linux
    # export TARGET_ARCH=amd64

    # If you are cross compiling (building on MacOS/Windows and running against a Linux Kubernetes cluster
    # or vice versa) uncomment and set these
    # export GOOS=linux
    # export GOARCH=amd64

    # Do not set DAPR_TEST_ENV if you do not use minikube
    export DAPR_TEST_ENV=minikube

    # If you are using minikube, you'll need to set the IP address for the minikube control plane.
    export MINIKUBE_NODE_IP=your_k8s_master_ip

    # Set the below environment variables if you want to use the different registry and tag for test apps
    # export DAPR_TEST_REGISTRY=docker.io/your_dockerhub_id
    # export DARP_TEST_TAG=dev
    # export DAPR_TEST_REGISTRY_SECRET=yourself_private_image_secret
    ```

> If you need to create the `DAPR_TEST_REGISTRY_SECRET` variable, you can use this command:
>
> ```sh
> DOCKER_REGISTRY="<url of the registry, such as myregistry.azurecr.io>"
> DOCKER_USERNAME="<your username>"
> DOCKER_PASSWORD="<your password>"
> DOCKER_EMAIL="<your email (leave empty if not required)>"
> export DAPR_TEST_REGISTRY_SECRET=$(
>   kubectl create secret docker-registry --dry-run=client docker-regcred \
>     --docker-server="${DOCKER_REGISTRY}" \
>     --docker-username="${DOCKER_USERNAME}" \
>     --docker-password="${DOCKER_PASSWORD}" \
>     --docker-email=${DOCKER_EMAIL} \
>     -o json | \
>       jq -r '.data.".dockerconfigjson"'
> )
> ```

### Option 1: Build, deploy, and run Dapr and e2e tests

If you are starting from scratch and just want to build dapr, deploy it, and run the e2e tests to your kubernetes cluster, do the following:

1. Uninstall dapr, dapr-kafka, dapr-redis, dapr-mongodb services, if they exist

   *Make sure you have DAPR_NAMESPACE set properly before you do this!*

   ```sh
   helm uninstall dapr dapr-kafka dapr-redis dapr-mongodb -n $DAPR_NAMESPACE
   ```

2. Remove the test namespace, if it exists

   ```bash
   make delete-test-namespace
   ```

3. Build, deploy, run tests from start to finish

   ```bash
   make e2e-build-deploy-run
   ```

### Option 2: Step by step guide

We also have individual targets to allow for quick iteration on parts of deployment and testing. To follow all or part of these steps individually, do the following:

Create dapr-tests namespace

```bash
make create-test-namespace
```

Install redis and kafka for state, pubsub, and binding building block

```bash
make setup-helm-init
make setup-test-env-redis
make setup-test-env-mongodb

# This may take a few minutes.  You can skip kafka install if you do not use bindings for your tests.
make setup-test-env-kafka
```

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
make setup-disable-mtls
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

### Run a subset of end-to-end tests

If you'd rather run a subset of end-to-end test, set the environmental variable `DAPR_E2E_TEST` with the name(s) of the test(s) (space-separated). These are the names of folders within the `tests/e2e` directory.

```sh
DAPR_E2E_TEST="actor_reminder" make test-e2e-all
```

## Cleanup local environment

To completely remove Dapr, test dependencies, and any lingering e2e test apps:

*Make sure you have DAPR_NAMESPACE set properly before you do this!*

```bash
# Uninstall dapr, dapr-kafka, dapr-redis services
helm uninstall dapr dapr-kafka dapr-redis dapr-mongodb -n $DAPR_NAMESPACE

# Remove the test namespace
make delete-test-namespace
```

## Run E2E tests through GitHub Actions

To keep the build infrastructure simple, Dapr uses [dapr-test GitHub Actions Workflow](https://github.com/dapr/dapr/actions?query=workflow%3Adapr-test) to run e2e tests using one of [AKS clusters](https://github.com/dapr/dapr/blob/4cd61680a3129f729deae24a51da241d0701376c/tests/test-infra/find_cluster.sh#L12-L17). A separate workflow also runs E2E in [KinD](https://kind.sigs.k8s.io/) clusters.

Once a contributor creates a pull request, E2E tests on KinD clusters are automatically executed for faster feedback. In order to run the E2E tests on AKS, ask a maintainer or approver to add `/ok-to-test` comment to the Pull Request.

## Run E2E tests on Azure AKS

This repository's automated tests (CI) use an Azure Kubernetes Service (AKS) cluster to run E2E tests.

If you want to run the tests in a similar environment, you can deploy the test infrastructure on your own using the Bicep templates in [tests/test-infra](/tests/test-infra/).

> Before you run the commands below, ensure that you have an Azure subscription, have the Azure CLI installed, and are logged into Azure (`az login`)

### Deploy AKS only

If you want to deploy AKS and the Azure Container Registry only (without Azure Cosmos DB and Azure Service Bus), you can use this script:

```sh
# Set the Azure region to use (needs to support Availability Zones)
AZURE_REGION="eastus2"

# Name prefix for your test resources
# Try to use a unique name
export TEST_PREFIX="mydapraks42"

# Set to true to add a Windows node pool to the AKS cluster
ENABLE_WINDOWS="false"

# Name of the resource group where to deploy your cluster
export TEST_RESOURCE_GROUP="MyDaprTest"

# Create a resource group
az group create \
  --resource-group "${TEST_RESOURCE_GROUP}" \
  --location "${AZURE_REGION}"

# Deploy the test infrastructure
az deployment group create \
  --resource-group "${TEST_RESOURCE_GROUP}" \
  --template-file ./tests/test-infra/azure-aks.bicep \
  --parameters namePrefix=${TEST_PREFIX} location=${AZURE_REGION} enableWindows=${ENABLE_WINDOWS}

# Authenticate with Azure Container Registry
az acr login --name "${TEST_PREFIX}acr"

# Connect to AKS
az aks get-credentials -n "${TEST_PREFIX}-aks" -g "${TEST_RESOURCE_GROUP}"

# Set the value for DAPR_REGISTRY
export DAPR_REGISTRY="${TEST_PREFIX}acr.azurecr.io"

# Set the value for DAPR_NAMESPACE as per instructions above and create the namespace
export DAPR_NAMESPACE=dapr-tests
make create-test-namespace
```

After this, run the E2E tests as per instructions above, making sure to use the newly-created Azure Container Registry as Docker registry (make sure you maintain the environmental variables set in the steps above).


### Deploy AKS and other Azure resources

This is the setup that our E2E tests use in GitHub Actions, which includes AKS, Azure Cosmos DB, and Azure Service Bus, in addition to an Azure Container Registry. To replicate the same setup, run:

> **NOTE:** This deploys an Azure Service Bus instance with ultra-high performance and it is **very expensive**. If you deploy this, don't forget to shut it down after you're done!

```sh
# Set the Azure region to use (needs to support Availability Zones)
AZURE_REGION="eastus2"

# Name prefix for your test resources
# Try to use a unique name
export TEST_PREFIX="mydapraks42"

# Set to true to add a Windows node pool to the AKS cluster
ENABLE_WINDOWS="false"

# Name of the resource group where to deploy your cluster
export TEST_RESOURCE_GROUP="MyDaprTest"

# Create a resource group
az group create \
  --resource-group "${TEST_RESOURCE_GROUP}" \
  --location "${AZURE_REGION}"

# Deploy the test infrastructure
az deployment group create \
  --resource-group "${TEST_RESOURCE_GROUP}" \
  --template-file ./tests/test-infra/azure.bicep \
  --parameters namePrefix=${TEST_PREFIX} location=${AZURE_REGION} enableWindows=${ENABLE_WINDOWS}

# Authenticate with Azure Container Registry
az acr login --name "${TEST_PREFIX}acr"

# Connect to AKS
az aks get-credentials -n "${TEST_PREFIX}-aks" -g "${TEST_RESOURCE_GROUP}"

# Set the value for DAPR_REGISTRY
export DAPR_REGISTRY="${TEST_PREFIX}acr.azurecr.io"

# Set the value for DAPR_NAMESPACE as per instructions above and create the namespace
export DAPR_NAMESPACE=dapr-tests
make create-test-namespace

# Create the Kubernetes secrets in the Dapr test namespaces to allow connecting to Cosmos DB and Service Bus
# Syntax: ./tests/test-infra/setup_azure.sh <ENABLE_COSMOSDB> <ENABLE_SERVICEBUS>
./tests/test-infra/setup_azure.sh true true
```

After this, run the E2E tests as per instructions above, making sure to use the newly-created Azure Container Registry as Docker registry (make sure you maintain the environmental variables set in the steps above).
