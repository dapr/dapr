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

1. If a service account has not already been installed for Tiller, install one:
    ```
    $ kubectl apply -f https://github.com/Azure/helm-charts/blob/master/docs/prerequisities/helm-rbac-config.yaml
    ```

2. Install Tiller on your cluster with the service account:
    ```
    $ helm init --service-account tiller --history-max 200
    ```

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
    --username 390401a7-d7a6-46da-b10f-3ceff7a1cdd5 \
    --password 485b3522-59bb-4152-8938-ca8b90108af6
    ```

2. Install Actions chart on your cluster using the actions-system namespace:
    ```
    helm install actionscore/actions-operator --name actions --namespace actions-system
    ``` 

## Verify installation

Once the chart installation is done, verify Actions operator pods are running in the `actions-system` namespace:
```
kubectl get pods --namespace actions-system
```
 
![actions_helm_success](/actions-operator/img/actions_helm_success.png)

## Uninstalling the Chart

To uninstall/delete the `actions` release but continue to track the release:
    ```
    $ helm delete actions
    ```

To uninstall/delete the `actions` release completely and make its name free for later use:
    ```
    $ helm delete --purge actions
    ```
