# Introduction
This chart bootstraps all of Dapr Operator components on a Kubernetes cluster using the Helm package manager.

## Chart Details
This chart installs multiple Dapr components via "child-charts":

* Dapr Component and Configuration Kubernetes CRDs
* Dapr Operator
* Dapr Placement

## Prerequisites
* Kubernetes 1.9 or newer cluster with RBAC (Role-Based Access Control) enabled is required
* Helm 3.0.2 or newer

## Resources Required
The chart deploys pods that consume minimum resources as specified in the resources configuration parameter.

## Installing the Chart

Make sure helm is initialized in your running kubernetes cluster.

For more details on initializing helm, Go [here](https://helm.sh/docs/)

1. Add Azure Container Registry as an helm repo
    ```
    helm repo add dapr https://daprio.azurecr.io/helm/v1/repo
    helm repo update
    ```

2. Install the Dapr chart on your cluster in the dapr-system namespace:
    ```
    helm install dapr dapr/dapr --namespace dapr-system
    ``` 

## Verify installation

Once the chart installation is done, verify the Dapr operator pods are running in the `dapr-system` namespace:
```
kubectl get pods --namespace dapr-system
```

## Uninstalling the Chart

To uninstall/delete the `dapr` release:
```
helm uninstall dapr -n dapr-system
```
