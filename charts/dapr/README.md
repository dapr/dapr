# Introduction
This chart bootstraps all of Dapr Operator components on a Kubernetes cluster using the Helm package manager.

## Chart Details
This chart installs multiple Dapr components via "child-charts":

* Dapr Component and Configuration Kubernetes CRDs
* Dapr RBAC components
    * Cluster Role Binding
    * Service Account
* Dapr Operator
* Dapr Placement

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

Dapr' Helm chart is hosted in an [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/),
which does not yet support anonymous access to charts therein. Until this is
resolved, adding the Helm repository from which Dapr can be installed requires
use of a shared set of read-only credentials.

Make sure helm is initialized in your running kubernetes cluster.

For more details on initializing helm, Go [here](https://docs.helm.sh/helm/#helm)

1. Add Azure Container Registry as an helm repo
    ```
    helm repo add dapr https://actionscore.azurecr.io/helm/v1/repo \
    --username 390401a7-d7a6-46da-b10f-3ceff7a1cdd5 \
    --password 485b3522-59bb-4152-8938-ca8b90108af6
    ```

2. Install the Dapr chart on your cluster in the dapr-system namespace:
    ```
    helm install dapr/dapr --name dapr --namespace dapr-system
    ``` 

## Verify installation

Once the chart installation is done, verify the Dapr operator pods are running in the `dapr-system` namespace:
```
kubectl get pods --namespace dapr-system
```

## Uninstalling the Chart

To uninstall/delete the `dapr` release but continue to track the release:
```
helm delete dapr
```

To uninstall/delete the `dapr` release completely and make its name free for later use:
```
helm delete --purge dapr
```
