# Introduction
This chart bootstraps all of Actions Operator components on a Kubernetes cluster using the Helm package manager.

## Chart Details
This chart installs multiple Actions components as subcharts:

* Kubernetes Secret with Pull privileges to our Azure Container Registry (ACR)
* Actions Components Kubernetes CRD
* Actions RBAC components
    * Cluster Role Binding
    * Service Account
* Actions Controller
* Actions Placement

## Prerequisites
* Kubernetes 1.9 or newer cluster with RBAC (Role-Based Access Control) enabled is required
* Helm 2.14 or newer or alternately the ability to modify RBAC rules is also required

## Resources Required
The chart deploys pods that consume minimum resources as specified in the resources configuration parameter.

## Installing the Chart

Actions' Helm chart is hosted in an [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/),
which does not yet support anonymous access to charts therein. Until this is
resolved, adding the Helm repository from which Actions can be installed requires
use of a shared set of read-only credentials.

Make sure helm is initialized in your running kubernetes cluster.

For more details on initializing helm, Go [here](https://docs.helm.sh/helm/#helm)

1. Add Azure Container Registry as an helm repo
    ```
    helm repo add actionscore https://actionscore.azurecr.io/helm/v1/repo \
    --username a19ef04c-de6e-4f53-be74-e5a4132a0aa2 \
    --password 7d19af7a-a569-4859-ae17-fc6dee783cbb
    ```

2. Install Actions chart on your cluster using the actions-system namespace:
    ```
    $ helm install actionscore/actions-operator --name actions --namespace actions-system
    ``` 

## Uninstalling the Chart

To uninstall/delete the `actions` release but continue to track the release:
    ```
    $ helm delete actions
    ```

To uninstall/delete the `actions` release completely and make its name free for later use:
    ```
    $ helm delete --purge actions
    ```
