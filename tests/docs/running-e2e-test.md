# Running End-To-End Tests

E2E tests are designed for verifying the functional correctness by replicating end-user behavior from app deployment. This describes how to run e2e tests.

- [Run E2E tests in local dev environment](#run-e2e-tests-in-local-dev-environment)
- [Run E2E tests through GitHub Actions](#run-e2e-tests-through-github-actions)
- [Run E2E tests on Azure Aks](#run-e2e-tests-on-Azure-AKS)
- [Run E2E tests using a Wireguard Tunnel with tailscale](#run-e2e-tests-using-a-wireguard-tunnel-with-tailscale)

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

    # If you want to enable debug logs for the daprd container set this
    # export DEBUG_LOGGING=true

    # If you want to run tests against Windows or arm kubernetes clusters, uncomment and set these
    # export TARGET_OS=linux
    # export TARGET_ARCH=amd64

    # If you are cross compiling (building on MacOS/Windows and running against a Linux Kubernetes cluster
    # or vice versa) uncomment and set these
    # export GOOS=linux
    # export GOARCH=amd64

    # If you want to use a single container image `dapr` instead of individual images 
    # (like sentry, injector, daprd, etc.), uncomment and set this.
    # export ONLY_DAPR_IMAGE=true

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

1. Uninstall dapr, dapr-kafka, dapr-redis, dapr-postgres, if they exist

   *Make sure you have DAPR_NAMESPACE set properly before you do this!*

   ```sh
   helm uninstall dapr dapr-kafka dapr-redis dapr-postgres -n $DAPR_NAMESPACE
   ```

2. Remove the test namespace, if it exists

   ```bash
   make delete-test-namespace
   ```

    > Note: please make sure that you have executed helm uninstall command before you deleted dapr test namespace. Otherwise if you directly deleted the dapr test namespace without helm uninstall command and re-installed dapr control plane, the dapr sidecar injector won't work and fail for "bad certificate". And you have already run into this problem, you can recover by helm uninstall command. See https://github.com/dapr/dapr/issues/4612

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
make setup-test-env-postgres

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

#### Optional: Apply this configuration to disable mTLS

```bash
make setup-disable-mtls
```

#### Register the default component configurations for testing

```bash
make setup-test-components
```

#### Build and push test apps to docker hub

Build docker images from apps and push the images to test docker hub

```bash
# build e2e apps docker image under apps/
make build-e2e-app-all

# push e2e apps docker image to docker hub
make push-e2e-app-all
```

#### Run end-to-end test

Run end-to-end tests

```bash
# start e2e test
make test-e2e-all
```

#### Run a subset of end-to-end tests

If you'd rather run a subset of end-to-end test, set the environmental variable `DAPR_E2E_TEST` with the name(s) of the test(s) (space-separated). These are the names of folders within the `tests/e2e` directory.

```sh
DAPR_E2E_TEST="actor_reminder" make test-e2e-all
```

#### Cleanup local environment

To completely remove Dapr, test dependencies, and any lingering e2e test apps:

*Make sure you have DAPR_NAMESPACE set properly before you do this!*

```bash
# Uninstall dapr, dapr-kafka, dapr-redis, dapr-postgres, then remove dapr-zipkin
helm uninstall dapr -n $DAPR_NAMESPACE || true
helm uninstall dapr-kafka  -n $DAPR_NAMESPACE || true
helm uninstall dapr-redis  -n $DAPR_NAMESPACE || true
helm uninstall dapr-postgres  -n $DAPR_NAMESPACE || true
kubectl delete deployment dapr-zipkin -n $DAPR_NAMESPACE || true

# Remove the test namespace
make delete-test-namespace
```

## Run E2E tests through GitHub Actions

To keep the build infrastructure simple, Dapr uses [dapr-test GitHub Actions Workflow](https://github.com/dapr/dapr/actions?query=workflow%3Adapr-test) to run e2e tests using one of [AKS clusters](https://github.com/dapr/dapr/blob/4cd61680a3129f729deae24a51da241d0701376c/tests/test-infra/find_cluster.sh#L12-L17). A separate workflow also runs E2E in [KinD](https://kind.sigs.k8s.io/) clusters.

Once a contributor creates a pull request, E2E tests on KinD clusters are automatically executed for faster feedback. In order to run the E2E tests on AKS, ask a maintainer or approver to add `/ok-to-test` comment to the Pull Request.

## Run E2E tests on Azure AKS

This repository's automated tests (CI) use an Azure Kubernetes Service (AKS) cluster to run E2E tests.

If you want to run the tests in a similar environment, you can deploy the test infrastructure on your own using the Bicep templates in [tests/test-infra](/tests/test-infra/). Here are the scripts you could use.

### Deploy AKS only

If you want to deploy AKS and the Azure Container Registry only (without Azure Cosmos DB and Azure Service Bus), you can use this script:

```sh
# Set the Azure region to use (needs to support Availability Zones)
AZURE_REGION="eastus2"

# Name prefix for your test resources
# Try to use a unique name，at least 4 characters
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
# Try to use a unique name，at least 4 characters
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
# Syntax: ./tests/test-infra/setup_azure.sh <ENABLE_COSMOSDB> <ENABLE_SERVICEBUS> <ENABLE_KEY_VAULT>
./tests/test-infra/setup_azure.sh true true false
```

After this, run the E2E tests as per instructions above, making sure to use the newly-created Azure Container Registry as Docker registry (make sure you maintain the environmental variables set in the steps above).

### Run tests 
The command in [Option 2: Step by step guide](#option-2-step-by-step-guide) can also run in AKS. Here is a sample script.

```sh
export ONLY_DAPR_IMAGE=true
# Replace with the name of your Azure Container Registry
export TEST_PREFIX="testprefix"
export DAPR_REGISTRY="${TEST_PREFIX}acr.azurecr.io"

export DAPR_TAG=dev
export DAPR_NAMESPACE=dapr-tests

export DAPR_TEST_NAMESPACE="dapr-tests"
export DAPR_TEST_TAG="${DAPR_TAG}-linux-amd64"
export DAPR_TEST_REGISTRY=${DAPR_REGISTRY}

# To enable debug logging
export DEBUG_LOGGING=true

az acr login --name ${DAPR_REGISTRY}


# Build Dapr. Run it in root folder of dapr source code project. 
make build

# Build and push Docker images
DOCKERFILE=Dockerfile-mariner make docker-build docker-push
make build-e2e-app-all
make push-e2e-app-all


# Setup Dapr in Kubernetes
make create-test-namespace
make docker-deploy-k8s
make setup-3rd-party setup-test-components

# Delete all logs etc
make test-clean

# You can run a single test with:
DAPR_E2E_TEST="pubsub" make test-e2e-all 
# Or run all tests with:
make test-e2e-all 

```

### Collect container and diagnostic logs from AKS

You can optionally configure AKS to collect certain logs, including:

- All container logs, sent to Azure Log Analytics
- Diagnostic logs (`kube-apiserver` and `kube-controller-manager`), sent to Azure Log Analytics
- Audit logs (`kube-audit`), sent to Azure Storage

To do that, first provision the required resources by deploying the `azure-aks-diagnostic.bicep` template (this template is not part of the `azure.bicep` or `azure-all.bicep` templates, as it's considered shared infrastructure):

```sh
# Name of the resource group where to deploy the diagnostic resources
DIAG_RESOURCE_GROUP="MyDaprTestLogs"

# Name prefix for the diagnostic resources (should be globally-unique)
DIAG_NAME_PREFIX="mydaprdiag42"

# Create a resource group
az group create \
  --resource-group "${DIAG_RESOURCE_GROUP}" \
  --location "${AZURE_REGION}"

# Deploy the test infrastructure
az deployment group create \
  --resource-group "${DIAG_RESOURCE_GROUP}" \
  --template-file ./tests/test-infra/azure-aks-diagnostic.bicep \
  --parameters name=${DIAG_NAME_PREFIX} location=${AZURE_REGION}
```

The output of the last command includes two values that are resource IDs:

- `diagLogAnalyticsWorkspaceResourceId`, for example: `/subscriptions/<subscription>/resourcegroups/<resource group>/providers/Microsoft.OperationalInsights/workspaces/<workspace name>`
- `diagStorageResourceId`, for example: `/subscriptions/<subscription>/resourcegroups/<resource group>/providers/Microsoft.Storage/storageAccounts/<storage account name>`

Use those values as parameters when deploying the `azure.bicep` or `azure-all.bicep` templates. For example:

```sh
az deployment group create \
  --resource-group "${TEST_RESOURCE_GROUP}" \
  --template-file ./tests/test-infra/azure.bicep \
  --parameters namePrefix=${TEST_PREFIX} location=${AZURE_REGION} enableWindows=${ENABLE_WINDOWS} diagLogAnalyticsWorkspaceResourceId=... diagStorageResourceId=...
```

### Clean UP AKS environment

```sh
export DAPR_NAMESPACE=dapr-tests

# Delete all logs etc
make test-clean

helm uninstall dapr -n $DAPR_NAMESPACE || true
helm uninstall dapr-kafka  -n $DAPR_NAMESPACE || true
helm uninstall dapr-redis  -n $DAPR_NAMESPACE || true
helm uninstall dapr-mongodb  -n $DAPR_NAMESPACE || true
helm uninstall dapr-temporal -n $DAPR_NAMESPACE || true
kubectl delete deployment dapr-zipkin -n $DAPR_NAMESPACE || true
kubectl delete namespace $DAPR_NAMESPACE || true

# Remove the test namespace
make delete-test-namespace
```

## Run E2E tests using a Wireguard Tunnel with tailscale

[Tailscale](https://tailscale.com/) is a zero-config VPN that provides NAT traversal out-of-the-box allowing our services and pods to be called directly - using its ClusterIP - without needed to be exposed using a loadbalancer.

This provides a few advantages including the decrease of the total test duration.

if you want to run the tests using tailscale as your network, few things are necessary:

1. [Create a tailscale account](https://login.tailscale.com/), this will be necessary since we're going to use personal keys.
2. [Download and install](https://tailscale.com/download/) the tailscale client for your OS.
3. When you're logged in, navigate to the menu `Access Controls` and two things are necessary, edit the ACL definition with:
   1. Create a new tag that will be used later to assign permissions to keys.
    ```json
    {...
      "tagOwners": {
        "tag:dapr-tests": ["your_email_comes_here@your_domain.com"],
      }
    }
    ```
   2. Assign permissions to the created tag. Since we are going to use the [tailscale subnet router](https://tailscale.com/kb/1185/kubernetes/), it is much convenient that the subnet router should auto approve the registered routes, for that, use the following acl.
   ```json
     {...
       "autoApprovers": {
         "routes": {
           "10.0.0.0/8": ["tag:dapr-tests"],
         },
       }
     }
   ```
   > Warning: as we are using `10.0.0.0/8` we must guarantee that our CIDR block used in the kubernetes cluster must be a subset of it
4. Now, go to the Settings > Personal Settings > Keys.
5. Once in the keys section, generate a new ephemeral key by clicking in `Generate auth key`.
6. Mark as `reusable`, `ephemeral` and add the created tag `dapr-tests` and do not forget to copy out the value.

Now, we're almost set.

The next step will be install the tailscale subnet router in your kubernetes cluster, for that, run

```sh
TAILSCALE_AUTH_KEY=your_key_goes_here make setup-tailscale
```

> TIP: for security reasons you could run `unset HISTFILE` before the tailscale command so that will discard your history file

Now, you have to login on tailscale client using your personal account, to verify if the subnet router deployment works, browse to the `Machines` on the tailscale portal, the subnet router should show up there.

One more config is necessary, `TEST_E2E_USE_INTERNAL_IP=true`, you can use it as a variable when running tests as the following:

```sh
TEST_E2E_USE_INTERNAL_IP=true make test-e2e-all
```

